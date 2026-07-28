package kb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	// Thresholds are empirical for the sample docs. Whether either one can
	// discriminate depends on the corpus, so re-measure after it changes:
	//
	// 2026-07-21, 34 questions, sections a median 34 characters long:
	//   bm25Max     hit 7.86   vs miss 11.58   (misses score higher)
	//   bestCosine  hit 0.622  vs miss 0.639   (indistinguishable)
	//
	// 2026-07-22, 29 questions, same source re-exported with static UIA text
	// captured and control sections given full-sentence Chinese lead-ins
	// (216 sections): bm25Max hit 11.74 vs miss 13.57, bestCosine 0.665 vs 0.638.
	//
	// So cosineMin is a meaningful knob and minThreshold is not: 34 characters
	// gave the embedding almost nothing to encode, and the earlier "vectors add
	// nothing" reading was a property of the thin corpus, not of the approach.
	// bm25Max has never separated hits from misses under any corpus measured.
	//
	// The cosine margin depends on the corpus (it shrank when screens were split
	// into smaller chunks, grew back when bodies gained Chinese context), so
	// treat it as a property of the current data, not a constant — re-measure.
	minThreshold = 1.2
	topK         = 3
	candidateK   = 20
	rrfK         = 60
	cosineMin    = 0.30
)

type sectionStore interface {
	Parse(ctx context.Context) ([]Section, int, error)
	Save(ctx context.Context, sections []Section) error
	Load(ctx context.Context) ([]Section, error)
}

type Service struct {
	sections   sectionStore
	llm        LLM
	embedder   Embedder
	vectors    VectorStore
	sessions   SessionStore
	embedModel string

	mu      sync.RWMutex
	vecMap  map[string][]float32
	corpus  Corpus
	indexed []Section
	ready   bool
}

type RetrievalMetrics struct {
	BM25Max    float64
	BestCosine float64
}

func NewService(sections sectionStore, llm LLM, embedder Embedder, vectors VectorStore, sessions SessionStore, embedModel string) *Service {
	return &Service{sections: sections, llm: llm, embedder: embedder, vectors: vectors, sessions: sessions, embedModel: embedModel}
}

func (s *Service) Index(ctx context.Context) (int, int, error) {
	secs, files, err := s.sections.Parse(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("parse docs: %w", err)
	}
	if err := s.sections.Save(ctx, secs); err != nil {
		return 0, 0, fmt.Errorf("save index: %w", err)
	}

	vecMap, err := s.embedSections(ctx, secs)
	if err != nil {
		return 0, 0, err
	}

	s.storeIndexSnapshot(secs, BuildCorpus(secs), vecMap, true)
	return files, len(secs), nil
}

func (s *Service) LoadOnStartup(ctx context.Context) error {
	secs, err := s.sections.Load(ctx)
	if errors.Is(err, ErrNotIndexed) || errors.Is(err, ErrIndexStale) {
		return err
	}
	if err != nil {
		return fmt.Errorf("load index: %w", err)
	}

	vecMap := map[string][]float32{}
	stale := false
	if s.vectors != nil {
		model, loaded, err := s.vectors.Load(ctx)
		if err != nil {
			return fmt.Errorf("load vectors: %w", err)
		}
		if len(loaded) > 0 && model != s.embedModel {
			stale = true
		} else {
			vecMap = loaded
		}
	}

	s.storeIndexSnapshot(secs, BuildCorpus(secs), vecMap, true)
	if stale {
		return ErrVectorsIgnored
	}
	return nil
}

func (s *Service) ChatWithMetrics(ctx context.Context, query, sessionID string) (Answer, string, RetrievalMetrics, error) {
	return s.chat(ctx, query, sessionID)
}

func (s *Service) chat(ctx context.Context, query, sessionID string) (Answer, string, RetrievalMetrics, error) {
	if strings.TrimSpace(query) == "" {
		return Answer{}, sessionID, RetrievalMetrics{}, ErrEmptyQuery
	}
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	indexed, corpus, vecMap, ready := s.indexSnapshot()
	if !ready {
		return Answer{}, sessionID, RetrievalMetrics{}, ErrNotIndexed
	}

	history := s.history(ctx, sessionID)
	contextualQuery := composeQuery(history, query)
	bm25List := corpus.RankBM25(tokenize(contextualQuery))
	for len(bm25List) > 0 && bm25List[len(bm25List)-1].Score <= 0 {
		bm25List = bm25List[:len(bm25List)-1]
	}
	if len(bm25List) > candidateK {
		bm25List = bm25List[:candidateK]
	}
	bm25Max := 0.0
	if len(bm25List) > 0 {
		bm25Max = bm25List[0].Score
	}
	var vecList []ScoredSection
	if s.embedder != nil && len(vecMap) > 0 {
		if vectors, err := s.embedder.Embed(ctx, []string{contextualQuery}); err == nil && len(vectors) > 0 {
			vecList = RankVector(indexed, vecMap, vectors[0], candidateK)
		}
	}
	bestCosine := 0.0
	if len(vecList) > 0 {
		bestCosine = vecList[0].Score
	}
	if bm25Max < minThreshold && bestCosine < cosineMin {
		answer, sessionID, err := s.deny(ctx, sessionID, query)
		return answer, sessionID, RetrievalMetrics{BM25Max: bm25Max, BestCosine: bestCosine}, err
	}
	ranked, strategy := bm25List, "markdown"
	if len(vecList) > 0 && len(bm25List) > 0 {
		ranked, strategy = FuseRRF([][]ScoredSection{bm25List, vecList}, rrfK), "hybrid"
	} else if len(vecList) > 0 {
		ranked, strategy = vecList, "vector"
	}
	sections := topSections(indexed, ranked, topK)
	if len(sections) == 0 {
		answer, sessionID, err := s.deny(ctx, sessionID, query)
		return answer, sessionID, RetrievalMetrics{BM25Max: bm25Max, BestCosine: bestCosine}, err
	}
	answer, sessionID, err := s.answerFrom(ctx, sessionID, query, sections, history, strategy)
	return answer, sessionID, RetrievalMetrics{BM25Max: bm25Max, BestCosine: bestCosine}, err
}

// deny records the turn and returns the cannot-confirm answer.
func (s *Service) deny(ctx context.Context, sessionID, query string) (Answer, string, error) {
	answer := s.cannotConfirm()
	s.appendTurn(ctx, sessionID, query, answer)
	return answer, sessionID, nil
}

// answerFrom grounds the LLM on the given sections, records the turn, and returns the answer.
func (s *Service) answerFrom(ctx context.Context, sessionID, query string, sections []Section, history []Turn, strategy string) (Answer, string, error) {
	text, err := s.llm.Answer(ctx, query, sections, history)
	if err != nil {
		return Answer{}, sessionID, fmt.Errorf("llm answer: %w", err)
	}
	// The model leads a refusal with ungroundedSentinel even when we retrieved
	// context (unrelated match, or ui_inventory only for a steps question). Strip
	// it and report grounded=false, so retrieval succeeding != answer grounded.
	text, ungrounded := splitUngrounded(text)
	// A refusal carries no usable sources. deny() already returns none, so
	// dropping them here makes "grounded == false implies no citations" hold on
	// both paths — otherwise a refusal comes back decorated with citations that
	// support nothing, which is the more misleading half of a missed sentinel.
	sources, images := citationsFor(sections), imagesOf(sections)
	if ungrounded {
		sources, images = nil, nil
	}
	answer := NewAnswer(text, sources, strategy, images, !ungrounded)
	s.appendTurn(ctx, sessionID, query, answer)
	return answer, sessionID, nil
}

func (s *Service) embedSections(ctx context.Context, secs []Section) (map[string][]float32, error) {
	if s.embedder == nil || s.vectors == nil {
		return map[string][]float32{}, nil
	}

	embs, err := s.embedder.Embed(ctx, bodiesOf(secs))
	if err != nil {
		return nil, fmt.Errorf("embed sections: %w", err)
	}
	if len(embs) != len(secs) {
		return nil, fmt.Errorf("embed sections: got %d vectors, want %d", len(embs), len(secs))
	}

	vecMap := make(map[string][]float32, len(secs))
	for i, sec := range secs {
		vecMap[sec.Citation()] = embs[i]
	}
	if err := s.vectors.Save(ctx, s.embedModel, vecMap); err != nil {
		return nil, fmt.Errorf("save vectors: %w", err)
	}
	return vecMap, nil
}

func (s *Service) storeIndexSnapshot(indexed []Section, corpus Corpus, vecMap map[string][]float32, ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.indexed = indexed
	s.corpus = corpus
	s.vecMap = vecMap
	s.ready = ready
}

func (s *Service) indexSnapshot() ([]Section, Corpus, map[string][]float32, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.indexed, s.corpus, s.vecMap, s.ready
}

func topSections(indexed []Section, ranked []ScoredSection, k int) []Section {
	if k > len(ranked) {
		k = len(ranked)
	}
	sections := make([]Section, 0, k)
	for _, scored := range ranked[:k] {
		if scored.Score <= 0 {
			continue
		}
		if scored.Index >= 0 && scored.Index < len(indexed) {
			sections = append(sections, indexed[scored.Index])
		}
	}
	return sections
}

func (s *Service) cannotConfirm() Answer {
	return NewAnswer("I cannot confirm that from the knowledge base.", nil, "", nil, false)
}

func (s *Service) history(ctx context.Context, sessionID string) []Turn {
	if s.sessions == nil {
		return nil
	}
	return s.sessions.Get(ctx, sessionID)
}

func (s *Service) appendTurn(ctx context.Context, sessionID, query string, answer Answer) {
	if s.sessions == nil {
		return
	}
	s.sessions.Append(ctx, sessionID, Turn{Query: query, Answer: answer.Text()})
}

func composeQuery(history []Turn, query string) string {
	if len(history) == 0 {
		return query
	}
	var b strings.Builder
	for _, turn := range history {
		b.WriteString(turn.Query)
		b.WriteByte(' ')
	}
	b.WriteString(query)
	return b.String()
}

func bodiesOf(sections []Section) []string {
	bodies := make([]string, 0, len(sections))
	for _, section := range sections {
		bodies = append(bodies, section.Body())
	}
	return bodies
}

// imagesOf collects the screenshot paths of the cited sections, deduplicated and
// in citation order, so an answer can point at the screen it describes.
func imagesOf(sections []Section) []string {
	images := make([]string, 0, len(sections))
	seen := map[string]bool{}
	for _, section := range sections {
		for _, image := range section.Images() {
			if seen[image] {
				continue
			}
			seen[image] = true
			images = append(images, image)
		}
	}
	return images
}

func citationsFor(sections []Section) []Citation {
	citations := make([]Citation, 0, len(sections))
	for _, section := range sections {
		citations = append(citations, NewCitation(section.File(), section.Anchor()))
	}
	return citations
}

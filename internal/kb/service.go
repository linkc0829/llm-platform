package kb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
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
	// Measured, not guessed. A module overview holds one section per screen, so a
	// button-location question needs the section for that specific screen — and the
	// section-level probe over 22 refusals found it at rank 3 for only 3 of them:
	// top-3=3, top-5=17, top-8=20, top-10=22, absent from the 20 candidates=0.
	// At 3 the answer was in the candidate set every time and cut before the model
	// saw it, which reads as the model refusing when it is retrieval trimming.
	// Re-measure with `-tags retrievalprobe` after any chunking change.
	topK       = 8
	candidateK = 20
	rrfK       = 60
	cosineMin  = 0.30
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
	if err := AuditSections(secs); err != nil {
		return 0, 0, fmt.Errorf("audit sections: %w", err)
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
	if err := AuditSections(secs); err != nil {
		return fmt.Errorf("%w: %w", ErrIndexAccessAuditFailed, err)
	}

	vecMap := map[string][]float32{}
	stale := false
	if s.vectors != nil {
		identity, loaded, invalid, err := s.loadVectorCache(ctx)
		if err != nil {
			return err
		}
		if invalid || identity != "" && identity != s.embeddingIdentity() {
			stale = true
		} else {
			vecMap = citationVectors(secs, loaded)
		}
	}

	s.storeIndexSnapshot(secs, BuildCorpus(secs), vecMap, true)
	if stale {
		return ErrVectorsIgnored
	}
	return nil
}

func (s *Service) ChatWithMetrics(ctx context.Context, principal shared.Principal, query, sessionID string) (Answer, string, RetrievalMetrics, error) {
	return s.chat(ctx, principal, query, sessionID)
}

func (s *Service) chat(ctx context.Context, principal shared.Principal, query, sessionID string) (Answer, string, RetrievalMetrics, error) {
	start := time.Now()
	ownerID := principal.ID
	if ownerID == "" {
		return Answer{}, sessionID, RetrievalMetrics{}, ErrSessionOwnerRequired
	}
	if strings.TrimSpace(query) == "" {
		return Answer{}, sessionID, RetrievalMetrics{}, ErrEmptyQuery
	}
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	history, err := s.history(ctx, sessionID, ownerID)
	if err != nil {
		return Answer{}, sessionID, RetrievalMetrics{}, fmt.Errorf("claim session: %w", err)
	}

	indexed, corpus, vecMap, ready := s.indexSnapshot()
	if !ready {
		return Answer{}, sessionID, RetrievalMetrics{}, ErrNotIndexed
	}

	contextualQuery := composeQuery(history, query)
	allow := func(section Section) bool {
		return CanSee(principal, section)
	}
	bm25List := corpus.RankBM25(tokenize(contextualQuery))
	bm25List = filterRankedSections(indexed, bm25List, allow)
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
			vecList = RankVector(indexed, vecMap, vectors[0], candidateK, allow)
		}
	}
	bestCosine := 0.0
	if len(vecList) > 0 {
		bestCosine = vecList[0].Score
	}
	if bm25Max < minThreshold && bestCosine < cosineMin {
		answer, sessionID, err := s.deny(ctx, ownerID, sessionID, query, start)
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
		answer, sessionID, err := s.deny(ctx, ownerID, sessionID, query, start)
		return answer, sessionID, RetrievalMetrics{BM25Max: bm25Max, BestCosine: bestCosine}, err
	}
	answer, sessionID, err := s.answerFrom(ctx, ownerID, sessionID, query, sections, history, strategy, start)
	return answer, sessionID, RetrievalMetrics{BM25Max: bm25Max, BestCosine: bestCosine}, err
}

// deny records the turn and returns the cannot-confirm answer.
func (s *Service) deny(ctx context.Context, ownerID, sessionID, query string, start time.Time) (Answer, string, error) {
	answer := s.cannotConfirm()
	if err := s.record(ctx, ownerID, sessionID, query, answer, start); err != nil {
		return Answer{}, sessionID, fmt.Errorf("record denied answer: %w", err)
	}
	return answer, sessionID, nil
}

// answerFrom grounds the LLM on the given sections, records the turn, and returns the answer.
func (s *Service) answerFrom(ctx context.Context, ownerID, sessionID, query string, sections []Section, history []Turn, strategy string, start time.Time) (Answer, string, error) {
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
	if err := s.record(ctx, ownerID, sessionID, query, answer, start); err != nil {
		return Answer{}, sessionID, fmt.Errorf("record answer: %w", err)
	}
	return answer, sessionID, nil
}

func (s *Service) embedSections(ctx context.Context, secs []Section) (map[string][]float32, error) {
	if s.embedder == nil || s.vectors == nil {
		return map[string][]float32{}, nil
	}

	identity, cached, _, err := s.loadVectorCache(ctx)
	if err != nil {
		return nil, err
	}
	if identity != "" && identity != s.embeddingIdentity() {
		cached = map[string][]float32{}
	}

	hashVectors := make(map[string][]float32, len(secs))
	missingHashes := make([]string, 0, len(secs))
	missingBodies := make([]string, 0, len(secs))
	seen := make(map[string]bool, len(secs))
	for _, sec := range secs {
		hash := sectionBodyHash(sec.Body())
		if seen[hash] {
			continue
		}
		seen[hash] = true
		if vector, ok := cached[hash]; ok && len(vector) > 0 {
			hashVectors[hash] = cloneVector(vector)
			continue
		}
		missingHashes = append(missingHashes, hash)
		missingBodies = append(missingBodies, sec.Body())
	}

	if len(missingBodies) > 0 {
		embs, err := s.embedder.Embed(ctx, missingBodies)
		if err != nil {
			return nil, fmt.Errorf("embed sections: %w", err)
		}
		if len(embs) != len(missingBodies) {
			return nil, fmt.Errorf("embed sections: got %d vectors, want %d", len(embs), len(missingBodies))
		}
		for i, vector := range embs {
			hashVectors[missingHashes[i]] = cloneVector(vector)
		}
	}
	if err := s.vectors.Save(ctx, s.embeddingIdentity(), hashVectors); err != nil {
		return nil, fmt.Errorf("save vectors: %w", err)
	}
	return citationVectors(secs, hashVectors), nil
}

func (s *Service) loadVectorCache(ctx context.Context) (string, map[string][]float32, bool, error) {
	identity, vectors, err := s.vectors.Load(ctx)
	if errors.Is(err, ErrVectorCacheFormat) {
		return "", map[string][]float32{}, true, nil
	}
	if err != nil {
		return "", nil, false, fmt.Errorf("load vectors: %w", err)
	}
	if vectors == nil {
		vectors = map[string][]float32{}
	}
	return identity, vectors, false, nil
}

func (s *Service) embeddingIdentity() string {
	if identifier, ok := s.embedder.(embedIdentifier); ok {
		if identity := strings.TrimSpace(identifier.EmbedIdentity()); identity != "" {
			return identity
		}
	}
	return s.embedModel
}

func sectionBodyHash(body string) string {
	hash := sha256.Sum256([]byte(body))
	return fmt.Sprintf("%x", hash)
}

func citationVectors(sections []Section, hashVectors map[string][]float32) map[string][]float32 {
	vecMap := make(map[string][]float32, len(sections))
	for _, section := range sections {
		if vector := hashVectors[sectionBodyHash(section.Body())]; len(vector) > 0 {
			vecMap[section.Citation()] = cloneVector(vector)
		}
	}
	return vecMap
}

func cloneVector(vector []float32) []float32 {
	return append([]float32(nil), vector...)
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

func filterRankedSections(indexed []Section, ranked []ScoredSection, allow func(Section) bool) []ScoredSection {
	filtered := make([]ScoredSection, 0, len(ranked))
	for _, scored := range ranked {
		if scored.Index < 0 || scored.Index >= len(indexed) {
			continue
		}
		if allow != nil && !allow(indexed[scored.Index]) {
			continue
		}
		filtered = append(filtered, scored)
	}
	return filtered
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

func (s *Service) history(ctx context.Context, sessionID, ownerID string) ([]Turn, error) {
	if s.sessions == nil {
		return nil, nil
	}
	return s.sessions.Claim(ctx, sessionID, ownerID)
}

// record logs the answered query, then appends the turn to session history.
//
// The log line is the only durable trace of what the KB was actually asked:
// session history is an in-process ring that dies with the process. Filtering
// it for grounded=false yields the questions the corpus could not answer, in
// real frequency order — the input to the next gate or re-export, which is
// otherwise only reachable by authoring eval questions and guessing.
//
// It sits before the nil-sessions guard on purpose: a caller with no session
// store still asked something worth recording.
func (s *Service) record(ctx context.Context, ownerID, sessionID, query string, answer Answer, start time.Time) error {
	zap.L().Info("kb_query",
		zap.String("q", query),
		zap.String("owner_id", ownerID),
		zap.String("session", sessionID),
		zap.Duration("took", time.Since(start)),
		zap.Bool("grounded", answer.Grounded()),
		zap.String("strategy", answer.Strategy()),
		zap.Strings("sources", citationStrings(answer.Sources())),
	)
	if s.sessions == nil {
		return nil
	}
	if err := s.sessions.Append(ctx, sessionID, ownerID, Turn{Query: query, Answer: answer.Text()}); err != nil {
		return fmt.Errorf("append session: %w", err)
	}
	return nil
}

func citationStrings(cites []Citation) []string {
	out := make([]string, 0, len(cites))
	for _, c := range cites {
		out = append(out, c.String())
	}
	return out
}

// composeQuery prefixes the query with session context so a follow-up like
// "那要什麼權限?" still names the subject it is asking about.
//
// Only the most recent turn is used. Concatenating the whole window (the store
// keeps five) drowned the current question: the result feeds both the BM25
// tokens and the embedding, so three earlier questions outweigh one current
// one. Measured over internal/kb/testdata/multiturn_probe_queries.json with
// -tags retrievalprobe:
//
//	shape          whole window   last turn
//	single-turn         8/8          8/8
//	follow-up           2/3          3/3
//	topic-switch        1/8          5/8
//	pronoun             2/2          2/2
//
// Every topic-switch miss retrieved the *previous* topic's sections. Dropping
// the older turns costs nothing because a pronoun refers to the turn just
// before it, never to the one four back.
//
// Topic-switch is still 5/8: one off-topic question is enough to outrank a
// self-sufficient query. Fixing that needs a "does this query stand alone?"
// test rather than a smaller window, which is a separate, measurable change.
func composeQuery(history []Turn, query string) string {
	if len(history) == 0 {
		return query
	}
	return history[len(history)-1].Query + " " + query
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

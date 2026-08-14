//go:build retrievalprobe

// Package-internal probe that measures retrieval alone, with no LLM in the loop.
//
// Why this exists: /chat and search_kb both report sources from the Answer, and
// an ungrounded answer now carries none — so the questions we most want to
// diagnose ("how do I check out?", which the model refuses) return an empty
// source list and tell us nothing about what was retrieved. This replays the
// exact ranking pipeline from Service.chat and prints the top-k anchors, which
// is deterministic and free of weak-model noise.
//
// Not part of `make verify`: it needs a built .kb index and a live embedder.
//
//	go test ./internal/kb/ -tags retrievalprobe -run TestRetrievalProbe -v
package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/config"
)

// probeQueries are the "how do I X" questions that motivated the change: every
// one of them should retrieve its module's step sections, not the doc boilerplate.
//
// wantArea is matched against Section.File(), which under the modular export is
// "<team>/procedures/<page>/<module>-procedure.md". The pre-modular names
// ("01_Login", "02_Main_Menu", ...) no longer occur in any path, so leaving them
// here silently reported areaHits=0 for every query — retrieval looked dead when
// only the expectation was stale. Keep these in step with the exporter's layout.
//
// The list below is the POS bundle's. Any other bundle needs its own, so set
// KB_RETRIEVAL_PROBE_QUERIES to a JSON file of [{"query":…,"wantArea":…}] —
// without that, probing a different corpus means editing this file, and the
// stale-fixture guard below turns the whole probe into a hard failure.
type probeQuery struct {
	Query    string `json:"query"`
	WantArea string `json:"wantArea"`
	// WantAnchor names the section that should answer the query. Set it to get
	// per-channel ranks: the fused list alone cannot tell you whether BM25 or the
	// vector side is the one failing, and "the file ranked" is not the same as
	// "the answering section ranked".
	WantAnchor string `json:"wantAnchor"`
}

// rankOf reports the 1-based position of the first section whose anchor contains
// want, or 0 when absent from the list.
func rankOf(list []ScoredSection, indexed []Section, want string) int {
	if want == "" {
		return 0
	}
	for i, s := range list {
		if s.Index >= 0 && s.Index < len(indexed) &&
			strings.Contains(indexed[s.Index].Anchor(), want) {
			return i + 1
		}
	}
	return 0
}

func loadProbeQueries(t *testing.T) []probeQuery {
	path := os.Getenv("KB_RETRIEVAL_PROBE_QUERIES")
	if path == "" {
		return defaultProbeQueries
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read KB_RETRIEVAL_PROBE_QUERIES=%s: %v", path, err)
	}
	var out []probeQuery
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s has no queries", path)
	}
	t.Logf("probe queries from %s: %d", path, len(out))
	return out
}

var defaultProbeQueries = []probeQuery{
	{Query: "如何執行登入?", WantArea: "POS/login-procedure.md"},
	{Query: "如何執行主選單?", WantArea: "POS/main_menu-procedure.md"},
	{Query: "如何執行點餐?", WantArea: "POS/ordering-procedure.md"},
	{Query: "如何執行套餐點餐?", WantArea: "POS/set_meal_ordering-procedure.md"},
	{Query: "如何執行訂單管理?", WantArea: "POS/order_management-procedure.md"},
	{Query: "如何執行單據重印?", WantArea: "POS/receipt_reprint-procedure.md"},
	{Query: "如何執行作廢?", WantArea: "POS/void-procedure.md"},
	{Query: "如何執行營業報表?", WantArea: "POS/business_reports-procedure.md"},
	{Query: "如何執行周邊管理?", WantArea: "POS/peripheral_management-procedure.md"},
	{Query: "如何結帳?", WantArea: ""},
	{Query: "如何用現金付款?", WantArea: ""},
	{Query: "如何作廢訂單?", WantArea: ""},
	{Query: "如何重印發票?", WantArea: ""},
	{Query: "怎麼看營業報表?", WantArea: ""},
	{Query: "如何暫存訂單?", WantArea: ""},
	{Query: "套餐訂單怎麼點?", WantArea: ""},
}

func TestRetrievalProbe(t *testing.T) {
	// go test runs in the package dir; config.LoadKB and KB_*_DIR are relative to the repo root.
	t.Chdir("../..")
	cfg, err := config.LoadKB()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if strings.EqualFold(cfg.OpenAI.LLMMode, "fake") {
		t.Fatal("KB_LLM_MODE=fake cannot probe retrieval embeddings")
	}
	oai := NewOpenAIClient(cfg.OpenAI.APIKey, cfg.OpenAI.BaseURL, cfg.OpenAI.EmbedBaseURL, cfg.OpenAI.EmbedAPIKey, cfg.OpenAI.GeminiThinkingLevel, cfg.OpenAI.ChatModel, cfg.OpenAI.EmbedModel)
	svc := NewService(NewMarkdownRepo(cfg.KB.DocsDir, cfg.KB.IndexDir), nil, oai, NewVectorRepo(cfg.KB.IndexDir), NewInProcStore(), cfg.OpenAI.EmbedModel)

	ctx := context.Background()
	if err := svc.LoadOnStartup(ctx); err != nil {
		t.Fatalf("LoadOnStartup() error = %v — build the index first (make import, then POST /index)", err)
	}
	indexed, corpus, vecMap, ready := svc.indexSnapshot()
	if !ready {
		t.Fatal("index not ready — run POST /index first")
	}
	t.Logf("indexed sections: %d, vectors: %d", len(indexed), len(vecMap))
	if evalPath := os.Getenv("KB_RETRIEVAL_PROBE_EVAL_OUT"); evalPath != "" {
		runEvalRetrievalProbe(t, ctx, oai, indexed, corpus, vecMap, evalPath)
		return
	}

	// Fail loudly on a stale wantArea. Without this a renamed layout just drives
	// areaHits to 0, which reads as "retrieval is broken" instead of "the
	// expectation no longer names a real file".
	probeQueries := loadProbeQueries(t)
	for _, tc := range probeQueries {
		if tc.WantArea == "" {
			continue
		}
		found := false
		for _, sec := range indexed {
			if strings.Contains(sec.File(), tc.WantArea) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("probeQueries wantArea %q matches no indexed file — update it to the current export layout", tc.WantArea)
		}
	}

	var stepHits, areaHits, boilerplate, total int
	for _, tc := range probeQueries {
		// Mirror Service.chat exactly: same candidate trimming, same fusion, same k.
		bm25List := corpus.RankBM25(tokenize(tc.Query))
		for len(bm25List) > 0 && bm25List[len(bm25List)-1].Score <= 0 {
			bm25List = bm25List[:len(bm25List)-1]
		}
		if len(bm25List) > candidateK {
			bm25List = bm25List[:candidateK]
		}
		var vecList []ScoredSection
		if vectors, err := oai.Embed(ctx, []string{tc.Query}); err == nil && len(vectors) > 0 {
			vecList = RankVector(indexed, vecMap, vectors[0], candidateK)
		} else if err != nil {
			t.Fatalf("Embed(%q) error = %v — is the embedder reachable?", tc.Query, err)
		}
		ranked := bm25List
		if len(vecList) > 0 && len(bm25List) > 0 {
			ranked = FuseRRF([][]ScoredSection{bm25List, vecList}, rrfK)
		} else if len(vecList) > 0 {
			ranked = vecList
		}

		var anchors []string
		hitStep, hitArea := false, false
		for _, sec := range topSections(indexed, ranked, topK) {
			total++
			anchors = append(anchors, sec.Anchor())
			if strings.Contains(sec.Anchor(), "步驟") {
				hitStep = true
			}
			if tc.WantArea != "" && strings.Contains(sec.File(), tc.WantArea) {
				hitArea = true
			}
			// The chunks that currently crowd out real steps.
			if a := sec.Anchor(); strings.Contains(a, "適用範圍") || strings.Contains(a, "操作程序") ||
				strings.Contains(a, "證據說明") || strings.Contains(a, "例外情境") || strings.Contains(a, "前置條件") {
				boilerplate++
			}
		}
		if hitStep {
			stepHits++
		}
		if hitArea {
			areaHits++
		}
		if tc.WantAnchor != "" {
			// Unfused ranks: which channel is failing is invisible in the fused list.
			t.Logf("[bm25=%-3d vec=%-3d fused=%-3d] %s  (want %q, bm25 candidates=%d)",
				rankOf(bm25List, indexed, tc.WantAnchor),
				rankOf(vecList, indexed, tc.WantAnchor),
				rankOf(ranked, indexed, tc.WantAnchor),
				tc.Query, tc.WantAnchor, len(bm25List))
		}
		t.Logf("[step=%-5v area=%-5v] %-22s -> %v", hitStep, hitArea, tc.Query, anchors)
	}

	areaAsked := 0
	for _, tc := range probeQueries {
		if tc.WantArea != "" {
			areaAsked++
		}
	}
	fmt.Printf("\n==== RETRIEVAL PROBE ====\n")
	fmt.Printf("queries                      : %d\n", len(probeQueries))
	fmt.Printf("with a 步驟 section in top-%d : %d / %d\n", topK, stepHits, len(probeQueries))
	fmt.Printf("reaching the expected area   : %d / %d\n", areaHits, areaAsked)
	fmt.Printf("boilerplate slots of %d      : %d\n", total, boilerplate)
	fmt.Printf("=========================\n")
}

type evalProbeRow struct {
	Question         string `json:"q"`
	Kind             string `json:"kind"`
	MustNotInfer     bool   `json:"mni"`
	Grounded         bool   `json:"grounded"`
	ExpectedSourceID string `json:"expected_source_id"`
}

type evalProbeStat struct {
	total, top3, top20, miss int
	// needRank[i] counts questions whose answer-bearing section first appears at
	// rank <= probeRanks[i]; answerMiss counts those absent from all candidates.
	needRank   []int
	answerable int
	answerMiss int
}

// probeRanks are the top-k values reported for the section-level measurement.
var probeRanks = []int{3, 5, 8, 10, 20}

const probeFileTopK = 3

// quotedTarget pulls the 「...」 term out of a generated question. The A2 sheet asks
// "<module> module 的「<button>」按鈕位於哪個畫面?", so the button is what the
// answering section must actually contain.
var quotedTarget = regexp.MustCompile(`「(.+?)」`)

// answerRank reports the 1-based rank of the first section whose body contains
// target, or 0 when no candidate does.
//
// File-level matching alone misled a diagnosis once: a module overview holds one
// section per screen, so a question about a button on screen 6 counted as
// "expected in top-3" whenever any section of that file ranked, even though the
// section carrying the button never reached the model. The model refusing was
// correct. Measure the section that holds the answer, not the file.
func answerRank(sections []Section, target string) int {
	if target == "" {
		return 0
	}
	for i, sec := range sections {
		if strings.Contains(sec.Body(), target) {
			return i + 1
		}
	}
	return 0
}

func runEvalRetrievalProbe(t *testing.T, ctx context.Context, oai *OpenAIClient,
	indexed []Section, corpus Corpus, vecMap map[string][]float32, evalPath string) {
	b, err := os.ReadFile(evalPath)
	if err != nil {
		t.Fatalf("read eval output: %v", err)
	}
	var rows []evalProbeRow
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("decode eval output: %v", err)
	}
	failed := make([]evalProbeRow, 0)
	for _, row := range rows {
		if !row.MustNotInfer && !row.Grounded && row.Question != "" {
			failed = append(failed, row)
		}
	}
	if len(failed) == 0 {
		t.Fatal("eval output has no non-must_not_infer grounded=false rows")
	}

	queries := make([]string, len(failed))
	for i, row := range failed {
		queries[i] = row.Question
	}
	vectors, err := oai.Embed(ctx, queries)
	if err != nil {
		t.Fatalf("Embed(eval questions) error: %v", err)
	}
	if len(vectors) != len(failed) {
		t.Fatalf("Embed(eval questions) returned %d vectors, want %d", len(vectors), len(failed))
	}

	stats := map[string]*evalProbeStat{}
	for i, row := range failed {
		stat := stats[row.Kind]
		if stat == nil {
			stat = &evalProbeStat{needRank: make([]int, len(probeRanks))}
			stats[row.Kind] = stat
		}
		stat.total++

		bm25List := corpus.RankBM25(tokenize(row.Question))
		for len(bm25List) > 0 && bm25List[len(bm25List)-1].Score <= 0 {
			bm25List = bm25List[:len(bm25List)-1]
		}
		if len(bm25List) > candidateK {
			bm25List = bm25List[:candidateK]
		}
		vecList := RankVector(indexed, vecMap, vectors[i], candidateK)
		ranked := bm25List
		if len(vecList) > 0 && len(bm25List) > 0 {
			ranked = FuseRRF([][]ScoredSection{bm25List, vecList}, rrfK)
		} else if len(vecList) > 0 {
			ranked = vecList
		}

		expectedPath := evalSourcePath(row.ExpectedSourceID)
		if containsExpectedPath(topSections(indexed, ranked, probeFileTopK), expectedPath) {
			stat.top3++
		} else if containsExpectedPath(topSections(indexed, ranked, candidateK), expectedPath) {
			stat.top20++
		} else {
			stat.miss++
		}

		target := ""
		if m := quotedTarget.FindStringSubmatch(row.Question); m != nil {
			target = m[1]
		}
		if target == "" {
			continue
		}
		stat.answerable++
		rank := answerRank(topSections(indexed, ranked, candidateK), target)
		if rank == 0 {
			stat.answerMiss++
			continue
		}
		for i, k := range probeRanks {
			if rank <= k {
				stat.needRank[i]++
			}
		}
	}

	kinds := make([]string, 0, len(stats))
	for kind := range stats {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	fmt.Printf("\n==== EVAL RETRIEVAL PROBE ====\nquestions with grounded=false: %d\n", len(failed))
	for _, kind := range kinds {
		stat := stats[kind]
		fmt.Printf("%-16s total=%d  expected in top-3=%d  only top-20=%d  miss top-20=%d\n",
			kind, stat.total, stat.top3, stat.top20, stat.miss)
	}
	fmt.Printf("top-%d = the expected FILE ranked; it does not mean the answering section did\n", probeFileTopK)
	fmt.Println("\n-- section-level: does a retrieved section actually contain the 「term」? --")
	for _, kind := range kinds {
		stat := stats[kind]
		parts := make([]string, 0, len(probeRanks))
		for i, k := range probeRanks {
			parts = append(parts, fmt.Sprintf("top-%d=%d", k, stat.needRank[i]))
		}
		fmt.Printf("%-16s total=%d  %s  not applicable=%d  absent from all %d candidates=%d\n",
			kind, stat.total, strings.Join(parts, "  "), stat.total-stat.answerable, candidateK, stat.answerMiss)
	}
	fmt.Printf("topK is currently %d — raising it only helps for questions already covered above.\n", topK)
}

func evalSourcePath(id string) string {
	team, rest, ok := strings.Cut(id, "--")
	if !ok {
		return ""
	}
	if suffix, ok := strings.CutPrefix(rest, "ui-"); ok {
		if !strings.HasPrefix(suffix, "module-") {
			return team + "/ui_inventory/" + suffix + "-ui_inventory.md"
		}
		suffix = strings.TrimPrefix(suffix, "module-")
		return team + "/ui_inventory/_modules/" + suffix + "-overview.md"
	}
	return ""
}

func containsExpectedPath(sections []Section, expectedPath string) bool {
	if expectedPath == "" {
		return false
	}
	for _, section := range sections {
		file := strings.ReplaceAll(section.File(), "\\", "/")
		if file == expectedPath || strings.HasSuffix(file, "/"+expectedPath) {
			return true
		}
	}
	return false
}

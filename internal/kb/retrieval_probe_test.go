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
	"fmt"
	"os"
	"strings"
	"testing"
)

// probeQueries are the "how do I X" questions that motivated the change: every
// one of them should retrieve its area's step sections, not the doc boilerplate.
var probeQueries = []struct{ query, wantArea string }{
	{"如何執行登入?", "01_Login"},
	{"如何執行主選單?", "02_Main_Menu"},
	{"如何執行點餐?", "03_Ordering"},
	{"如何執行套餐點餐?", "04_Set_Meal_Ordering"},
	{"如何執行訂單管理?", "05_Order_Management"},
	{"如何執行單據重印?", "06_Receipt_Reprint"},
	{"如何執行作廢?", "07_Void"},
	{"如何執行營業報表?", "08_Business_Reports"},
	{"如何執行周邊管理?", "09_Peripheral_Management"},
	{"如何結帳?", ""},
	{"如何用現金付款?", ""},
	{"如何作廢訂單?", ""},
	{"如何重印發票?", ""},
	{"怎麼看營業報表?", ""},
	{"如何暫存訂單?", ""},
	{"套餐訂單怎麼點?", ""},
}

func TestRetrievalProbe(t *testing.T) {
	docsDir := envOr("KB_DOCS_DIR", "docs")
	indexDir := envOr("KB_INDEX_DIR", ".kb")
	baseURL := envOr("OPENAI_BASE_URL", "http://192.168.22.100:11434/v1")
	embedModel := envOr("KB_EMBED_MODEL", "snowflake-arctic-embed2")

	oai := NewOpenAIClient(envOr("OPENAI_API_KEY", "x"), baseURL, envOr("KB_EMBED_BASE_URL", ""), envOr("KB_CHAT_MODEL", "llama3.1:8b"), embedModel)
	svc := NewService(NewMarkdownRepo(docsDir, indexDir), oai, oai, NewVectorRepo(indexDir), NewInProcStore(), embedModel)

	ctx := context.Background()
	if err := svc.LoadOnStartup(ctx); err != nil {
		t.Fatalf("LoadOnStartup() error = %v — build the index first (make import, then POST /index)", err)
	}
	indexed, corpus, vecMap, ready := svc.indexSnapshot()
	if !ready {
		t.Fatal("index not ready — run POST /index first")
	}
	t.Logf("indexed sections: %d, vectors: %d", len(indexed), len(vecMap))

	var stepHits, areaHits, boilerplate, total int
	for _, tc := range probeQueries {
		// Mirror Service.chat exactly: same candidate trimming, same fusion, same k.
		bm25List := corpus.RankBM25(tokenize(tc.query))
		for len(bm25List) > 0 && bm25List[len(bm25List)-1].Score <= 0 {
			bm25List = bm25List[:len(bm25List)-1]
		}
		if len(bm25List) > candidateK {
			bm25List = bm25List[:candidateK]
		}
		var vecList []ScoredSection
		if vectors, err := oai.Embed(ctx, []string{tc.query}); err == nil && len(vectors) > 0 {
			vecList = RankVector(indexed, vecMap, vectors[0], candidateK)
		} else if err != nil {
			t.Fatalf("Embed(%q) error = %v — is the embedder reachable?", tc.query, err)
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
			if tc.wantArea != "" && strings.Contains(sec.File(), tc.wantArea) {
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
		t.Logf("[step=%-5v area=%-5v] %-22s -> %v", hitStep, hitArea, tc.query, anchors)
	}

	areaAsked := 0
	for _, tc := range probeQueries {
		if tc.wantArea != "" {
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

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

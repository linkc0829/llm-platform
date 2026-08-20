package kb

import (
	"context"
	"os"
	"testing"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/config"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// These benchmarks answer one question: is the per-query work that Service.chat
// repeats worth optimising, or is it noise next to the LLM round trip it sits in
// front of? Both candidates look wasteful on paper —
//
//   - CanSee re-derives each section's tier on every query, twice (once in
//     filterRankedSections over the whole BM25 list, once inside RankVector),
//     and ClassifySection regex-scans the section body to do it. AuditSections
//     already proved every one of those values at load time, so the answer can
//     never differ.
//   - Cosine recomputes both norms per call: the query's 1395 times identically,
//     and each document's on every query even though the vector is immutable.
//
// — but "looks wasteful" is not a reason to change retrieval. Measure first.
// They read the real docs/ and .kb/ because the cost is a property of this
// corpus, not of a synthetic one, and skip when those are absent.

func benchSnapshot(tb testing.TB) ([]Section, map[string][]float32) {
	tb.Helper()
	tb.Chdir("../..")
	cfg, err := config.LoadKB()
	if err != nil {
		tb.Skipf("load config: %v", err)
	}
	if _, err := os.Stat(cfg.KB.DocsDir); err != nil {
		tb.Skipf("no corpus at %s — run make import first", cfg.KB.DocsDir)
	}
	sections, err := NewMarkdownRepo(cfg.KB.DocsDir, cfg.KB.IndexDir).Load(context.Background())
	if err != nil {
		tb.Skipf("load index: %v — run POST /index first", err)
	}
	_, hashed, err := NewVectorRepo(cfg.KB.IndexDir).Load(context.Background())
	if err != nil {
		tb.Skipf("load vectors: %v", err)
	}
	// Reproduce what storeIndexSnapshot does before a section can be queried.
	// Skipping it leaves every tier at SectionTierInvalid, and the benchmark then
	// measures a filter that rejects the whole corpus: RankVector "with_filter"
	// came out faster than "cosine_only", which is the shape of a benchmark
	// timing nothing.
	StampTiers(sections)
	visible := 0
	for _, section := range sections {
		if section.Tier() != SectionTierInvalid {
			visible++
		}
	}
	if visible == 0 {
		tb.Fatal("no section classified — the benchmark would time an empty filter")
	}
	return sections, citationVectors(sections, hashed)
}

// BenchmarkCanSeeAll is one filter pass. Service.chat does two.
func BenchmarkCanSeeAll(b *testing.B) {
	sections, _ := benchSnapshot(b)
	support := shared.Principal{ID: "bench", Teams: []string{"Store.POS"}}
	b.ReportMetric(float64(len(sections)), "sections")
	b.ResetTimer()
	for b.Loop() {
		visible := 0
		for _, section := range sections {
			if CanSee(support, section) {
				visible++
			}
		}
	}
}

// BenchmarkRankVector is the whole vector leg: filter, cosine, sort, truncate.
func BenchmarkRankVector(b *testing.B) {
	sections, vecMap := benchSnapshot(b)
	var query []float32
	for _, section := range sections {
		if v := vecMap[section.Citation()]; len(v) > 0 {
			query = v
			break
		}
	}
	if len(query) == 0 {
		b.Skip("no vectors in the index")
	}
	allow := func(section Section) bool {
		return CanSee(FullAccessPrincipal(AnonymousOwner), section)
	}
	b.ReportMetric(float64(len(query)), "dims")
	// Split the leg in two: "filter" is the tier recomputation, "cosine" is the
	// arithmetic. They have different fixes, so a single number cannot pick one.
	b.Run("with_filter", func(b *testing.B) {
		for b.Loop() {
			RankVector(sections, vecMap, query, candidateK, allow)
		}
	})
	b.Run("cosine_only", func(b *testing.B) {
		for b.Loop() {
			RankVector(sections, vecMap, query, candidateK, nil)
		}
	})
}

// BenchmarkVectorLoad is paid once per process start, not per query — it is here
// because the file is 84MB of decimal float strings and that is a storage
// decision the other two numbers should be weighed against.
func BenchmarkVectorLoad(b *testing.B) {
	b.Chdir("../..")
	cfg, err := config.LoadKB()
	if err != nil {
		b.Skipf("load config: %v", err)
	}
	repo := NewVectorRepo(cfg.KB.IndexDir)
	if _, _, err := repo.Load(context.Background()); err != nil {
		b.Skipf("load vectors: %v", err)
	}
	for b.Loop() {
		if _, _, err := repo.Load(context.Background()); err != nil {
			b.Fatalf("Load() error = %v", err)
		}
	}
}

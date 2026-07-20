package kb

import (
	"math"
	"reflect"
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		name    string
		heading string
		want    string
	}{
		{name: "spaces_to_hyphens", heading: "Refund Timeline", want: "refund-timeline"},
		{name: "strips_punctuation", heading: "Change Email Address!", want: "change-email-address"},
		{name: "cjk_preserved", heading: "動態密碼", want: "動態密碼"},
		{name: "cjk_with_punctuation", heading: "Scenario: 登入 完整操作劇本", want: "scenario-登入-完整操作劇本"},
		{name: "collapses_runs", heading: "Change -- Email", want: "change-email"},
		{name: "trims_edges", heading: " !! Change Email ?? ", want: "change-email"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slugify(tt.heading)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.heading, got, tt.want)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "pure_cjk", text: "動態密碼", want: []string{"動態", "態密", "密碼"}},
		{name: "single_cjk_char", text: "密", want: []string{"密"}},
		{name: "mixed_cjk_ascii", text: "動態 LoginViewModel", want: []string{"動態", "loginviewmodel"}},
		{name: "ascii_only", text: "Refunds: 5-7 business days. Final-sale items!", want: []string{"refunds", "5", "7", "business", "days", "final", "sale", "items"}},
		{name: "punctuation_only", text: "!?-", want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenize(tt.text)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tokenize(%q) = %#v, want %#v", tt.text, got, tt.want)
			}
		})
	}
}

func TestBM25ScoreOrdersRelevantSectionFirst(t *testing.T) {
	relevant, err := NewSection("refund_policy.md", "Refund Timeline", "Refunds are processed within 5-7 business days.", nil, nil)
	if err != nil {
		t.Fatalf("NewSection(relevant) error = %v, want nil", err)
	}
	other, err := NewSection("shipping_faq.md", "Tracking Number", "Customers receive a tracking number by email.", nil, nil)
	if err != nil {
		t.Fatalf("NewSection(other) error = %v, want nil", err)
	}
	corpus := BuildCorpus([]Section{relevant, other})

	query := tokenize("refund business days")
	relevantScore := corpus.BM25Score(0, query)
	otherScore := corpus.BM25Score(1, query)
	if relevantScore <= otherScore {
		t.Errorf("BM25Score(relevant, %v) = %f, want greater than other score %f", query, relevantScore, otherScore)
	}

	ranked := corpus.RankBM25(query)
	if len(ranked) == 0 || ranked[0].Index != 0 {
		t.Errorf("RankBM25(%v) first = %#v, want index 0 first", query, ranked)
	}
}

func TestCosine(t *testing.T) {
	tests := []struct {
		name string
		a    []float32
		b    []float32
		want float64
	}{
		{name: "orthogonal_returns_zero", a: []float32{1, 0}, b: []float32{0, 1}, want: 0},
		{name: "identical_returns_one", a: []float32{1, 1}, b: []float32{1, 1}, want: 1},
		{name: "mismatched_length_returns_zero", a: []float32{1}, b: []float32{1, 1}, want: 0},
		{name: "zero_norm_returns_zero", a: []float32{0, 0}, b: []float32{1, 1}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Cosine(tt.a, tt.b)
			if math.Abs(got-tt.want) > 0.000001 {
				t.Errorf("Cosine(%v, %v) = %f, want %f", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestFuseRRF(t *testing.T) {
	got := FuseRRF([][]ScoredSection{{{Index: 1, Score: 9}, {Index: 0, Score: 8}}, {{Index: 0, Score: 7}, {Index: 2, Score: 6}}}, 60)
	want := []int{0, 1, 2}
	for i, index := range want {
		if got[i].Index != index {
			t.Errorf("FuseRRF() index %d = %d, want %d", i, got[i].Index, index)
		}
	}
}

func TestRankVector(t *testing.T) {
	a, _ := NewSection("a.md", "A", "body", nil, nil)
	b, _ := NewSection("b.md", "B", "body", nil, nil)
	got := RankVector([]Section{a, b}, map[string][]float32{a.Citation(): {1, 0}, b.Citation(): {0, 1}}, []float32{1, 0}, 1)
	if len(got) != 1 || got[0].Index != 0 {
		t.Errorf("RankVector() = %#v, want index 0 only", got)
	}
}

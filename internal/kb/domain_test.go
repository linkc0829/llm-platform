package kb

import (
	"math"
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
	input := "Refunds: 5-7 business days. Final-sale items!"
	want := []string{"refunds", "5", "7", "business", "days", "final", "sale", "items"}

	got := tokenize(input)
	if len(got) != len(want) {
		t.Fatalf("tokenize(%q) length = %d, want %d; got %#v", input, len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tokenize(%q)[%d] = %q, want %q", input, i, got[i], want[i])
		}
	}
}

func TestBM25ScoreOrdersRelevantSectionFirst(t *testing.T) {
	relevant, err := NewSection("refund_policy.md", "Refund Timeline", "Refunds are processed within 5-7 business days.")
	if err != nil {
		t.Fatalf("NewSection(relevant) error = %v, want nil", err)
	}
	other, err := NewSection("shipping_faq.md", "Tracking Number", "Customers receive a tracking number by email.")
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

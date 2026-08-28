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

func TestSectionEvidenceClass(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		heading string
		body    string
		meta    map[string]string
		want    string
	}{
		{name: "crawler_ui_inventory", file: "登入__00_動態密碼登入.md", heading: "登入/00_動態密碼登入", body: "## 按鈕\n- Enter", want: "ui_inventory"},
		{name: "export_heading_ui_inventory", file: "login.md", heading: "按鈕", body: "- Enter", want: "ui_inventory"},
		{name: "nested_ui_inventory_heading", file: "login.md", heading: "Login", body: "### 按鈕\n- Enter", want: "ui_inventory"},
		{name: "ui_inventory_file_applies_to_all_sections", file: "login-ui_inventory.md", heading: "畫面:登入", body: "說明", want: "ui_inventory"},
		{name: "recorded_procedure_real_corpus_format", file: "login-procedure.md", heading: "步驟 1", body: "- 動作證據：`recorded`", want: "procedure"},
		{name: "visual_procedure_real_corpus_format", file: "login-procedure.md", heading: "步驟 1", body: "- 動作證據：`vision_inferred`", want: "procedure_visual"},
		{name: "unlabeled_procedure_real_corpus_format", file: "login-procedure.md", heading: "步驟 1", body: "- 動作證據：`recorded_unlabeled`", want: "procedure_unlabeled"},
		{name: "recorded_procedure", file: "login-procedure.md", heading: "步驟 1", body: "- **動作證據**:`recorded`", want: "procedure"},
		{name: "visual_procedure", file: "login-procedure.md", heading: "步驟 1", body: "- **動作證據**:`vision_inferred`", want: "procedure_visual"},
		{name: "recorded_unlabeled_procedure", file: "login-procedure.md", heading: "步驟 1", body: "- **動作證據**:`recorded_unlabeled`", want: "procedure_unlabeled"},
		{name: "inferred_procedure", file: "login-procedure.md", heading: "步驟 1", body: "- **動作證據**:`inferred`", want: "procedure_inferred"},
		{name: "not_attributable_procedure", file: "login-procedure.md", heading: "步驟 1", body: "- **動作證據**:`not_attributable`", want: "procedure_inferred"},
		{name: "static_procedure_steps_are_general", file: "login-procedure.md", heading: "步驟", body: "此畫面為靜態，無錄製到的後續操作。", want: "general"},
		{name: "procedure_template_with_bare_token_is_general", file: "login-procedure.md", heading: "證據說明", body: "`recorded` 僅為範例。", want: "general"},
		{name: "procedure_file_does_not_use_generic_fallback", file: "login-procedure.md", heading: "步驟", body: "**Given** 登入畫面", want: "general"},
		{name: "generic_procedure_wins_over_ui_marker", file: "flow.md", heading: "步驟 1", body: "## 按鈕\n**When** 點擊確認", want: "procedure"},
		{name: "declared_procedure_static_section_is_general", file: "login-procedure.md", heading: "適用範圍", body: "單一使用者目標：登入。", meta: map[string]string{"doc_type": "procedure"}, want: "general"},
		{name: "declared_procedure_recorded_step_real_format", file: "login-procedure.md", heading: "步驟 1", body: "- 動作證據：`recorded`", meta: map[string]string{"doc_type": "procedure"}, want: "procedure"},
		{name: "declared_procedure_visual_step_real_format", file: "login-procedure.md", heading: "步驟 1", body: "- 動作證據：`vision_inferred`", meta: map[string]string{"doc_type": "procedure"}, want: "procedure_visual"},
		{name: "declared_procedure_recorded_step", file: "login-procedure.md", heading: "步驟 1", body: "- **動作證據**:`recorded`", meta: map[string]string{"doc_type": "procedure"}, want: "procedure"},
		{name: "declared_procedure_visual_step", file: "login-procedure.md", heading: "步驟 1", body: "- **動作證據**:`vision_inferred`", meta: map[string]string{"doc_type": "procedure"}, want: "procedure_visual"},
		{name: "declared_procedure_unlabeled_step", file: "login-procedure.md", heading: "步驟 1", body: "- **動作證據**:`recorded_unlabeled`", meta: map[string]string{"doc_type": "procedure"}, want: "procedure_unlabeled"},
		{name: "declared_procedure_not_attributable_step", file: "login-procedure.md", heading: "步驟 1", body: "- **動作證據**:`not_attributable`", meta: map[string]string{"doc_type": "procedure"}, want: "procedure_inferred"},
		{name: "declared_ui_inventory_applies_to_any_section", file: "login-ui_inventory.md", heading: "適用範圍", body: "說明", meta: map[string]string{"doc_type": "ui_inventory"}, want: "ui_inventory"},
		{name: "engineering_reference", file: "repo-map-pos.md", heading: "POS Repo Map", body: "source revision: sha256:test", meta: map[string]string{"doc_type": "engineering_reference"}, want: "engineering_reference"},
		{name: "unknown_doc_type_is_general", file: "login-ui_inventory.md", heading: "按鈕", body: "- Enter", meta: map[string]string{"doc_type": "typo"}, want: "general"},
		{name: "general", file: "guide.md", heading: "說明", body: "一般說明", want: "general"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			section, err := NewSection(tt.file, tt.heading, tt.body, tt.meta, nil)
			if err != nil {
				t.Fatalf("NewSection(%q, %q) error = %v, want nil", tt.file, tt.heading, err)
			}
			if got := section.EvidenceClass(); got != tt.want {
				t.Errorf("Section.EvidenceClass(%q, %q) = %q, want %q", tt.file, tt.heading, got, tt.want)
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

func TestFuseRRFOrdersEqualScoresByIndex(t *testing.T) {
	got := FuseRRF([][]ScoredSection{{{Index: 2}, {Index: 1}}, {{Index: 1}, {Index: 2}}}, 60)
	want := []int{1, 2}
	for i, index := range want {
		if got[i].Index != index {
			t.Errorf("FuseRRF() index %d = %d, want %d", i, got[i].Index, index)
		}
	}
}

func TestRankVector(t *testing.T) {
	a, _ := NewSection("a.md", "A", "body", nil, nil)
	b, _ := NewSection("b.md", "B", "body", nil, nil)
	got := RankVector([]Section{a, b}, map[string][]float32{a.Citation(): {1, 0}, b.Citation(): {0, 1}}, []float32{1, 0}, 1, nil)
	if len(got) != 1 || got[0].Index != 0 {
		t.Errorf("RankVector() = %#v, want index 0 only", got)
	}
}

func TestValidateCorpus(t *testing.T) {
	valid := map[string]string{"id": "Store.POS--login", "team": "Store.POS", "product": "POS", "doc_type": "procedure", "version": "v1", "access_level": "internal", "owner": "POS", "last_reviewed": "2026-07-21"}
	tests := []struct {
		name string
		secs []Section
		want int
	}{
		{name: "valid_corpus", secs: []Section{corpusSection(t, "a.md", valid)}, want: 0},
		{name: "missing_required_field", secs: []Section{corpusSection(t, "a.md", map[string]string{"id": "x"})}, want: 7},
		{name: "duplicate_id_across_files", secs: []Section{corpusSection(t, "a.md", valid), corpusSection(t, "b.md", valid)}, want: 1},
		{name: "reports_each_file_once", secs: []Section{corpusSection(t, "a.md", map[string]string{"id": "x"}), corpusSection(t, "a.md", map[string]string{"id": "x"})}, want: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(ValidateCorpus(tt.secs)); got != tt.want {
				t.Errorf("ValidateCorpus() errors = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestValidateCorpusIsDeterministic(t *testing.T) {
	secs := []Section{corpusSection(t, "b.md", map[string]string{}), corpusSection(t, "a.md", map[string]string{})}
	first, second := ValidateCorpus(secs), ValidateCorpus(secs)
	for i := range first {
		if first[i].Error() != second[i].Error() {
			t.Errorf("ValidateCorpus() error %d = %q, want %q", i, first[i], second[i])
		}
	}
}

func corpusSection(t *testing.T, file string, meta map[string]string) Section {
	t.Helper()
	section, err := NewSection(file, "Heading", "body", meta, nil)
	if err != nil {
		t.Fatalf("NewSection(%q) error = %v, want nil", file, err)
	}
	return section
}

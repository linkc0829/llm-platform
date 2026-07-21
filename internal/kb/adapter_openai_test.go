package kb

import (
	"strings"
	"testing"
)

func TestGroundedPromptIncludesEvidenceClasses(t *testing.T) {
	sections := []Section{
		mustSection(t, "login-ui_inventory.md", "畫面:登入", "控制項", nil),
		mustSection(t, "login-procedure.md", "步驟 1", "- **動作證據**:`recorded`", nil),
		mustSection(t, "login-procedure.md", "步驟 2", "- **動作證據**:`inferred`", nil),
		mustSection(t, "guide.md", "說明", "一般說明", nil),
	}

	got := groundedPrompt("如何登入？", sections)
	for _, want := range []string{
		"[login-ui_inventory.md#畫面-登入] (evidence: ui_inventory)",
		"[login-procedure.md#步驟-1] (evidence: procedure)",
		"[login-procedure.md#步驟-2] (evidence: procedure_inferred)",
		"[guide.md#說明] (evidence: general)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("groundedPrompt() = %q, want substring %q", got, want)
		}
	}
}

func TestGroundingSystemWhitelistsProcedureEvidence(t *testing.T) {
	for _, want := range []string{
		"Only sections tagged evidence: procedure may support an action order",
		"ui_inventory, procedure_unlabeled, procedure_inferred, or general cannot support steps or sequence",
		"never which control was clicked",
	} {
		if !strings.Contains(groundingSystem, want) {
			t.Errorf("groundingSystem = %q, want substring %q", groundingSystem, want)
		}
	}
}

func mustSection(t *testing.T, file, heading, body string, meta map[string]string) Section {
	t.Helper()
	section, err := NewSection(file, heading, body, meta, nil)
	if err != nil {
		t.Fatalf("NewSection(%q, %q) error = %v, want nil", file, heading, err)
	}
	return section
}

package kb

import (
	"context"
	"strings"
	"testing"
)

func TestFakeLLMAnswersFromFirstSection(t *testing.T) {
	section, err := NewSection("refund_policy.md", "Refund Timeline", "Refunds take 5-7 business days.", nil, nil)
	if err != nil {
		t.Fatalf("NewSection() error = %v, want nil", err)
	}
	llm := NewFakeLLM()

	answer, err := llm.Answer(context.Background(), "How long do refunds take?", []Section{section}, nil)
	if err != nil {
		t.Fatalf("FakeLLM.Answer() error = %v, want nil", err)
	}
	for _, want := range []string{"[fake LLM]", "How long do refunds take?", "refund_policy.md#refund-timeline", "Refunds take 5-7 business days."} {
		if !strings.Contains(answer.Text, want) {
			t.Errorf("FakeLLM.Answer() = %q, want substring %q", answer.Text, want)
		}
	}
}

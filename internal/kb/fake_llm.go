package kb

import (
	"context"
	"strings"
)

type FakeLLM struct{}

func NewFakeLLM() *FakeLLM { return &FakeLLM{} }

func (f *FakeLLM) Answer(_ context.Context, query string, sections []Section, _ []Turn) (string, error) {
	if len(sections) == 0 {
		return "I cannot confirm that from the knowledge base.", nil
	}
	var b strings.Builder
	b.WriteString("[fake LLM] Question: ")
	b.WriteString(strings.TrimSpace(query))
	b.WriteString("\n\nRelevant context from ")
	b.WriteString(sections[0].Citation())
	b.WriteString(": ")
	b.WriteString(strings.TrimSpace(sections[0].Body()))
	return b.String(), nil
}

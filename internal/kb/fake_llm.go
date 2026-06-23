package kb

import (
	"context"
	"strings"
)

// FakeLLM is a local/demo mode for quota-free manual verification; its vectors are intentionally simple and corpus-coupled.
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

func (f *FakeLLM) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vectors = append(vectors, fakeVector(text))
	}
	return vectors, nil
}

func fakeVector(text string) []float32 {
	tokens := tokenize(text)
	vec := []float32{0, 0, 0, 0}
	for _, token := range tokens {
		switch token {
		case "refund", "refunds", "refunded", "refundable", "return", "returned", "purchase", "money", "back", "unhappy", "sale", "digital", "gift", "cards":
			vec[0]++
		case "account", "email", "address", "settings", "verify", "verification":
			vec[1]++
		case "shipping", "tracking", "delivery", "carrier", "package", "order":
			vec[2]++
		case "restaurant", "restaurants", "nearby", "food":
			vec[3]++
		}
	}
	return vec
}

package kb

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// OpenAIClient implements LLM and Embedder.
type OpenAIClient struct {
	client              openai.Client
	embedClient         openai.Client
	chatModel           openai.ChatModel
	embedModel          openai.EmbeddingModel
	geminiThinkingLevel string
}

func NewOpenAIClient(apiKey, baseURL, embedBaseURL, embedAPIKey, geminiThinkingLevel, chatModel, embedModel string) *OpenAIClient {
	opts := openAIOptions(apiKey, baseURL)
	if embedBaseURL == "" {
		embedBaseURL = baseURL
	}
	if embedAPIKey == "" {
		embedAPIKey = apiKey
	}
	return &OpenAIClient{
		client:              openai.NewClient(opts...),
		embedClient:         openai.NewClient(openAIOptions(embedAPIKey, embedBaseURL)...),
		chatModel:           openai.ChatModel(chatModel),
		embedModel:          openai.EmbeddingModel(embedModel),
		geminiThinkingLevel: geminiThinkingLevel,
	}
}

func openAIOptions(apiKey, baseURL string) []option.RequestOption {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return opts
}

// ungroundedSentinel is the exact prefix the model must lead with when it
// cannot answer from the context. The service strips it and sets Grounded=false,
// so callers get a structured signal instead of parsing the refusal prose —
// which proved unreliable across English, Chinese, and weak-model phrasings.
const ungroundedSentinel = "[UNGROUNDED]"

const embeddingBatchSize = 32

// ungroundedPattern matches the sentinel tolerantly. A weak model asked for an
// exact token does not reliably produce one: llama3.1:8b emitted [UNEQUIPPED]
// for a refusal, which an exact prefix match missed, so the refusal was reported
// as grounded=true with citations attached. Accept any leading bracketed
// UN-token, and let common decoration (markdown emphasis, a code fence, an
// "Answer:" lead-in) sit in front of it.
//
// Best effort by construction: a model that refuses in plain prose with no token
// at all still reads as grounded. The deny path in service.go never consults the
// model, so it stays exact regardless.
// Trailing [*_:：,.、 -]* also consumes the closing emphasis and any punctuation
// joining the token to its reason, so the stripped text starts at the reason.
var ungroundedPattern = regexp.MustCompile(`^\s*(?:` + "```" + `[a-zA-Z]*\s*)?(?:[*_> ]*)(?:(?i:answer|答案|回答)\s*[:：]\s*)?[*_ ]*\[\s*UN[A-Z_ ]*\s*\][*_:：,.、 -]*`)

// splitUngrounded reports whether text is an ungrounded refusal, returning the
// text with the sentinel removed.
func splitUngrounded(text string) (string, bool) {
	loc := ungroundedPattern.FindStringIndex(strings.TrimSpace(text))
	if loc == nil {
		return strings.TrimSpace(text), false
	}
	trimmed := strings.TrimSpace(text)
	return strings.TrimSpace(trimmed[loc[1]:]), true
}

const groundingSystem = `You answer questions ONLY using the provided context sections. ` +
	`Cite sources as filename#anchor. ` +
	`Only sections tagged evidence: procedure or procedure_visual may support an action order, steps, or sequence. ` +
	`Sections tagged ui_inventory, procedure_unlabeled, procedure_inferred, or general cannot support steps or sequence. ` +
	// The restriction above is about steps only, but the model kept generalising it into
	// "ui_inventory cannot answer anything": a retrieval probe found 47 of 52 refusals had
	// the right section in the top 3, most of them button-location questions answered by
	// ui_inventory. Say affirmatively what ui_inventory IS for, or the guardrail silently
	// suppresses the questions that section type exists to answer.
	`ui_inventory sections are the authoritative record of which controls a screen contains: ` +
	`when the question asks whether a control exists, what a screen contains, or which screen ` +
	`a control appears on, answer it directly from them — that is an inventory question, ` +
	`not a step or sequence question, and the steps-only restriction above does not apply. ` +
	`Reply in the same language as the question. ` +
	`A procedure_visual section is a coordinate-and-screenshot visual review, not a UIA-resolved control; identify it as visual evidence when relying on it. ` +
	`A procedure_unlabeled section proves only that a click occurred, never which control was clicked. ` +
	// Refuse on absence, not on wording. The model kept quoting the correct answer and
	// then declining: asked which API 點餐 calls, it printed POST /terminal/v1/order and
	// added "but the context does not explicitly define which one 點餐 itself maps to".
	// Reciting the verified symbol proves the evidence arrived; withholding it afterwards
	// is not caution, it is a wrong answer. This must not loosen must_not_infer — the
	// condition is that the answer is PRESENT, which says nothing about inventing one.
	`If the context contains a concrete answer — the symbol, endpoint, screen name, or method the question asks for — state it, even when the context does not repeat the question's own wording. ` +
	`Do not refuse on the grounds that a mapping is "not explicitly defined" while the context in fact supplies it; refuse when the answer is absent, never when it is present but phrased differently. ` +
	`Whenever you cannot answer the question from the context — the context is unrelated to the question, or it only lists controls while the question asks for operation steps or order the knowledge base does not record — begin your reply with the exact token ` + ungroundedSentinel + ` followed by a brief reason, and never invent or infer steps.`

func (o *OpenAIClient) Answer(ctx context.Context, query string, sections []Section, history []Turn) (string, error) {
	messages := []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(groundingSystem)}
	for _, turn := range history {
		messages = append(messages, openai.UserMessage(turn.Query), openai.AssistantMessage(turn.Answer))
	}
	messages = append(messages, openai.UserMessage(groundedPrompt(query, sections)))

	params := openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    o.chatModel,
	}
	if o.geminiThinkingLevel != "" {
		params.SetExtraFields(map[string]any{"extra_body": map[string]any{
			"google": map[string]any{"thinking_config": map[string]string{"thinking_level": o.geminiThinkingLevel}},
		}})
	}
	completion, err := o.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("openai chat: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("openai chat: no choices returned")
	}
	return strings.TrimSpace(completion.Choices[0].Message.Content), nil
}

func (o *OpenAIClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += embeddingBatchSize {
		end := min(start+embeddingBatchSize, len(texts))
		batch, err := o.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, fmt.Errorf("openai embed batch %d: %w", start/embeddingBatchSize+1, err)
		}
		vectors = append(vectors, batch...)
	}
	return vectors, nil
}

func (o *OpenAIClient) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	response, err := o.embedClient.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: texts,
		},
		Model:          o.embedModel,
		EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat,
	})
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}

	vectors := make([][]float32, len(response.Data))
	for _, item := range response.Data {
		if item.Index < 0 || int(item.Index) >= len(vectors) {
			return nil, fmt.Errorf("openai embed: response index %d out of range", item.Index)
		}
		vec := make([]float32, len(item.Embedding))
		for i, value := range item.Embedding {
			vec[i] = float32(value)
		}
		vectors[item.Index] = vec
	}
	if len(vectors) != len(texts) {
		return nil, fmt.Errorf("openai embed: got %d vectors, want %d", len(vectors), len(texts))
	}
	return vectors, nil
}

func groundedPrompt(query string, sections []Section) string {
	var b strings.Builder
	b.WriteString("Context sections:\n")
	for _, section := range sections {
		b.WriteString("\n[")
		b.WriteString(section.Citation())
		b.WriteString("] (evidence: ")
		b.WriteString(section.EvidenceClass())
		b.WriteString(")\n")
		b.WriteString(section.Body())
		b.WriteByte('\n')
	}
	b.WriteString("\nQuestion: ")
	b.WriteString(query)
	return b.String()
}

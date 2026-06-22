package kb

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// OpenAIClient implements LLM. Phase 4 adds Embedder on the same adapter.
type OpenAIClient struct {
	client     openai.Client
	chatModel  openai.ChatModel
	embedModel string
}

func NewOpenAIClient(apiKey string) *OpenAIClient {
	return &OpenAIClient{
		client:     openai.NewClient(option.WithAPIKey(apiKey)),
		chatModel:  openai.ChatModelGPT4oMini,
		embedModel: "text-embedding-3-small",
	}
}

const groundingSystem = `You answer questions ONLY using the provided context sections. ` +
	`If the answer is not contained in the context, reply that you cannot confirm it from ` +
	`the knowledge base. Cite sources as filename#anchor.`

func (o *OpenAIClient) Answer(ctx context.Context, query string, sections []Section, history []Turn) (string, error) {
	messages := []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(groundingSystem)}
	for _, turn := range history {
		messages = append(messages, openai.UserMessage(turn.Query), openai.AssistantMessage(turn.Answer))
	}
	messages = append(messages, openai.UserMessage(groundedPrompt(query, sections)))

	completion, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    o.chatModel,
	})
	if err != nil {
		return "", fmt.Errorf("openai chat: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("openai chat: no choices returned")
	}
	return strings.TrimSpace(completion.Choices[0].Message.Content), nil
}

func groundedPrompt(query string, sections []Section) string {
	var b strings.Builder
	b.WriteString("Context sections:\n")
	for _, section := range sections {
		b.WriteString("\n[")
		b.WriteString(section.Citation())
		b.WriteString("]\n")
		b.WriteString(section.Body())
		b.WriteByte('\n')
	}
	b.WriteString("\nQuestion: ")
	b.WriteString(query)
	return b.String()
}

package kb

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// OpenAIClient implements LLM and Embedder.
type OpenAIClient struct {
	client      openai.Client
	embedClient openai.Client
	chatModel   openai.ChatModel
	embedModel  openai.EmbeddingModel
}

func NewOpenAIClient(apiKey, baseURL, embedBaseURL, chatModel, embedModel string) *OpenAIClient {
	opts := openAIOptions(apiKey, baseURL)
	if embedBaseURL == "" {
		embedBaseURL = baseURL
	}
	return &OpenAIClient{
		client:      openai.NewClient(opts...),
		embedClient: openai.NewClient(openAIOptions(apiKey, embedBaseURL)...),
		chatModel:   openai.ChatModel(chatModel),
		embedModel:  openai.EmbeddingModel(embedModel),
	}
}

func openAIOptions(apiKey, baseURL string) []option.RequestOption {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return opts
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

func (o *OpenAIClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
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
		b.WriteString("]\n")
		b.WriteString(section.Body())
		b.WriteByte('\n')
	}
	b.WriteString("\nQuestion: ")
	b.WriteString(query)
	return b.String()
}

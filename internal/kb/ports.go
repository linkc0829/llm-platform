package kb

import "context"

// Outbound ports for the kb feature. Implementations live in repo_*/adapter_*/memory_*.
type LLM interface {
	Answer(ctx context.Context, query string, sections []Section, history []Turn) (string, error)
}

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type VectorStore interface {
	Load(ctx context.Context) (string, map[string][]float32, error)
	Save(ctx context.Context, model string, vectors map[string][]float32) error
}

type SessionStore interface {
	Claim(ctx context.Context, sessionID, ownerID string) ([]Turn, error)
	Append(ctx context.Context, sessionID, ownerID string, turn Turn) error
}

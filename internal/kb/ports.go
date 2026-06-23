package kb

import "context"

// Outbound ports for the kb feature. Implementations live in repo_*/adapter_*/memory_*.
// Filled in across phases: SectionStore, LLM, Embedder, VectorStore, SessionStore.
type SectionStore interface {
	Load(ctx context.Context) ([]Section, error)
	Save(ctx context.Context, sections []Section) error
}

type LLM interface {
	Answer(ctx context.Context, query string, sections []Section, history []Turn) (string, error)
}

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type VectorStore interface {
	Load(ctx context.Context) (map[string][]float32, error)
	Save(ctx context.Context, model string, vectors map[string][]float32) error
}

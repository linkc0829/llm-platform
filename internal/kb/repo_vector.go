package kb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type VectorRepo struct {
	indexDir string
}

func NewVectorRepo(indexDir string) *VectorRepo {
	return &VectorRepo{indexDir: indexDir}
}

func (r *VectorRepo) Load(ctx context.Context) (map[string][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	b, err := os.ReadFile(r.metadataPath())
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]float32{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read vector metadata: %w", err)
	}

	var in vectorMetaJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("unmarshal vector metadata: %w", err)
	}
	if in.Vectors == nil {
		return map[string][]float32{}, nil
	}
	return in.Vectors, nil
}

func (r *VectorRepo) Save(ctx context.Context, model string, vectors map[string][]float32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(r.vectorDir(), 0o755); err != nil {
		return fmt.Errorf("create vector index dir: %w", err)
	}

	out := vectorMetaJSON{Model: model, Vectors: vectors}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vector metadata: %w", err)
	}
	if err := os.WriteFile(r.metadataPath(), b, 0o600); err != nil {
		return fmt.Errorf("write vector metadata: %w", err)
	}
	return nil
}

func (r *VectorRepo) vectorDir() string {
	return filepath.Join(r.indexDir, "faiss_index")
}

func (r *VectorRepo) metadataPath() string {
	return filepath.Join(r.vectorDir(), "metadata.json")
}

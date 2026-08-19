package kb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/atomicfile"
)

const vectorFormatVersion = 1

type VectorRepo struct {
	indexDir string
}

func NewVectorRepo(indexDir string) *VectorRepo {
	return &VectorRepo{indexDir: indexDir}
}

func (r *VectorRepo) Load(ctx context.Context) (string, map[string][]float32, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}

	b, err := os.ReadFile(r.metadataPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", map[string][]float32{}, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("read vector metadata: %w", err)
	}

	var in vectorMetaJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return "", map[string][]float32{}, fmt.Errorf("%w: unmarshal vector metadata: %w", ErrVectorCacheFormat, err)
	}
	if in.Version != vectorFormatVersion {
		return "", map[string][]float32{}, fmt.Errorf("%w: unsupported vector metadata version %d", ErrVectorCacheFormat, in.Version)
	}
	if strings.TrimSpace(in.Identity) == "" {
		return "", map[string][]float32{}, fmt.Errorf("%w: vector metadata has no embedding identity", ErrVectorCacheFormat)
	}
	for key := range in.Vectors {
		if !validVectorHash(key) {
			return "", map[string][]float32{}, fmt.Errorf("%w: invalid vector key %q", ErrVectorCacheFormat, key)
		}
	}
	if in.Vectors == nil {
		return in.Identity, map[string][]float32{}, nil
	}
	return in.Identity, in.Vectors, nil
}

func validVectorHash(key string) bool {
	if len(key) != sha256.Size*2 || key != strings.ToLower(key) {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}

func (r *VectorRepo) Save(ctx context.Context, identity string, vectors map[string][]float32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(identity) == "" {
		return fmt.Errorf("save vector metadata: embedding identity is empty")
	}
	if err := os.MkdirAll(r.vectorDir(), 0o755); err != nil {
		return fmt.Errorf("create vector index dir: %w", err)
	}

	out := vectorMetaJSON{Identity: identity, Version: vectorFormatVersion, Vectors: vectors}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vector metadata: %w", err)
	}
	if err := atomicfile.Replace(r.metadataPath(), b, 0o600); err != nil {
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

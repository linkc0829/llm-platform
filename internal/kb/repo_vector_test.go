package kb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVectorRepoSaveWritesVersionAndIdentity(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())
	hash := sectionBodyHash("body")
	want := map[string][]float32{hash: {1, 0}}

	if err := repo.Save(context.Background(), "model@endpoint", want); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}
	raw, err := os.ReadFile(repo.metadataPath())
	if err != nil {
		t.Fatalf("ReadFile(metadata) error = %v, want nil", err)
	}
	var metadata vectorMetaJSON
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("Unmarshal(metadata) error = %v, want nil", err)
	}
	if metadata.Version != vectorFormatVersion {
		t.Errorf("metadata version = %d, want %d", metadata.Version, vectorFormatVersion)
	}
	if metadata.Identity != "model@endpoint" {
		t.Errorf("metadata identity = %q, want model@endpoint", metadata.Identity)
	}
	if len(metadata.Vectors) != 1 || len(metadata.Vectors[hash]) != 2 {
		t.Errorf("metadata vectors = %#v, want one hash vector", metadata.Vectors)
	}
}

func TestVectorRepoRoundTripsBodyHashKeys(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())
	hash := sectionBodyHash("body")
	want := map[string][]float32{hash: {1, 0}}

	if err := repo.Save(context.Background(), "model@endpoint", want); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	identity, vectors, err := repo.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if identity != "model@endpoint" {
		t.Errorf("Load() identity = %q, want model@endpoint", identity)
	}
	if len(vectors[hash]) == 0 {
		t.Errorf("Load() vectors = %#v, want non-empty vector at body hash %q", vectors, hash)
	}
}

func TestVectorRepoRejectsInvalidFormats(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "missing_version", data: `{"identity":"model","vectors":{}}`},
		{name: "unknown_version", data: `{"identity":"model","version":99,"vectors":{}}`},
		{name: "empty_identity", data: `{"identity":"","version":1,"vectors":{}}`},
		{name: "invalid_json", data: "{"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := NewVectorRepo(t.TempDir())
			writeVectorMetadata(t, repo, []byte(tt.data))

			identity, vectors, err := repo.Load(context.Background())
			if !errors.Is(err, ErrVectorCacheFormat) {
				t.Fatalf("Load() error = %v, want ErrVectorCacheFormat", err)
			}
			if identity != "" || len(vectors) != 0 {
				t.Errorf("Load() = identity:%q vectors:%#v, want empty usable values", identity, vectors)
			}
		})
	}
}

func TestVectorRepoRejectsCitationKeyedCache(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())
	writeVectorMetadata(t, repo, []byte(`{"identity":"model","version":1,"vectors":{"guide.md#guide":[1,0]}}`))

	_, vectors, err := repo.Load(context.Background())
	if !errors.Is(err, ErrVectorCacheFormat) {
		t.Fatalf("Load() error = %v, want ErrVectorCacheFormat", err)
	}
	if len(vectors) != 0 {
		t.Errorf("Load() vectors = %#v, want empty map", vectors)
	}
}

func TestVectorRepoMissingFileIsCacheMiss(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())

	identity, vectors, err := repo.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for missing file", err)
	}
	if identity != "" || len(vectors) != 0 {
		t.Errorf("Load() = identity:%q vectors:%#v, want empty cache miss", identity, vectors)
	}
}

func TestVectorRepoReadFailureIsNotFormatError(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())
	if err := os.MkdirAll(repo.vectorDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll(vectorDir) error = %v", err)
	}
	if err := os.Mkdir(repo.metadataPath(), 0o755); err != nil {
		t.Fatalf("Mkdir(metadataPath) error = %v", err)
	}

	_, _, err := repo.Load(context.Background())
	if err == nil {
		t.Fatal("Load() error = nil, want read error")
	}
	if errors.Is(err, ErrVectorCacheFormat) {
		t.Errorf("Load() error = %v, must remain an IO/read error", err)
	}
}

func TestVectorRepoSaveLeavesCompleteJSONAndNoTemporaryFile(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())
	if err := repo.Save(context.Background(), "model", map[string][]float32{sectionBodyHash("body"): {1, 0}}); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}
	if err := repo.Save(context.Background(), "model", map[string][]float32{sectionBodyHash("body"): {0, 1}}); err != nil {
		t.Fatalf("second Save() error = %v, want nil", err)
	}

	raw, err := os.ReadFile(repo.metadataPath())
	if err != nil {
		t.Fatalf("ReadFile(metadata) error = %v, want nil", err)
	}
	var metadata vectorMetaJSON
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("Unmarshal(metadata) error = %v, want complete JSON", err)
	}
	matches, err := filepath.Glob(filepath.Join(repo.vectorDir(), ".metadata.json.tmp-*"))
	if err != nil {
		t.Fatalf("Glob(temporary files) error = %v, want nil", err)
	}
	if len(matches) != 0 {
		t.Errorf("temporary files = %v, want none", matches)
	}
}

func writeVectorMetadata(t *testing.T, repo *VectorRepo, data []byte) {
	t.Helper()
	if err := os.MkdirAll(repo.vectorDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll(vectorDir) error = %v", err)
	}
	if err := os.WriteFile(repo.metadataPath(), data, 0o600); err != nil {
		t.Fatalf("WriteFile(metadata) error = %v", err)
	}
}

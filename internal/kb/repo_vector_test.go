package kb

import (
	"context"
	"encoding/binary"
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
	raw, err := os.ReadFile(repo.vectorPath())
	if err != nil {
		t.Fatalf("ReadFile(vectors) error = %v, want nil", err)
	}
	if got := string(raw[:len(vectorFileMagic)]); got != vectorFileMagic {
		t.Errorf("magic = %q, want %q", got, vectorFileMagic)
	}
	version := binary.LittleEndian.Uint32(raw[len(vectorFileMagic):])
	if version != vectorFormatVersion {
		t.Errorf("version = %d, want %d", version, vectorFormatVersion)
	}
	identity, vectors, err := decodeVectors(raw)
	if err != nil {
		t.Fatalf("decodeVectors() error = %v, want nil", err)
	}
	if identity != "model@endpoint" {
		t.Errorf("identity = %q, want model@endpoint", identity)
	}
	if len(vectors) != 1 || len(vectors[hash]) != 2 {
		t.Errorf("vectors = %#v, want one hash vector", vectors)
	}
}

func TestVectorRepoRoundTripsBodyHashKeys(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())
	hash := sectionBodyHash("body")
	// Components that are not exactly representable as short decimals: the point
	// of the binary format is that a float32 survives the trip bit for bit.
	want := map[string][]float32{hash: {0.1, -3.4028235e+38, 1e-45}}

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
	got := vectors[hash]
	if len(got) != len(want[hash]) {
		t.Fatalf("Load() vector = %#v, want %#v", got, want[hash])
	}
	for i := range got {
		if got[i] != want[hash][i] {
			t.Errorf("Load() vector[%d] = %v, want %v", i, got[i], want[hash][i])
		}
	}
}

func TestVectorRepoRejectsInvalidFormats(t *testing.T) {
	good, err := encodeVectors("model", map[string][]float32{sectionBodyHash("body"): {1, 0}})
	if err != nil {
		t.Fatalf("encodeVectors() error = %v, want nil", err)
	}
	wrongVersion := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(wrongVersion[len(vectorFileMagic):], 99)
	emptyIdentity, err := encodeVectors("model", nil)
	if err != nil {
		t.Fatalf("encodeVectors(empty) error = %v, want nil", err)
	}
	binary.LittleEndian.PutUint32(emptyIdentity[len(vectorFileMagic)+4:], 0)

	tests := []struct {
		name string
		data []byte
	}{
		{name: "not_a_kbvec_file", data: []byte(`{"identity":"model","version":1,"vectors":{}}`)},
		{name: "unknown_version", data: wrongVersion},
		{name: "empty_identity", data: emptyIdentity},
		{name: "truncated", data: good[:len(good)-4]},
		{name: "empty_file", data: []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := NewVectorRepo(t.TempDir())
			writeVectorFile(t, repo, tt.data)

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

// A citation-keyed cache cannot reach the file any more -- keys are the raw
// 32-byte digest -- so the guard moves to the writer. Persisting one would put
// the pre-body-hash cache back into circulation.
func TestVectorRepoSaveRejectsCitationKeyedCache(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())

	err := repo.Save(context.Background(), "model", map[string][]float32{"guide.md#guide": {1, 0}})
	if err == nil {
		t.Fatal("Save(citation-keyed) error = nil, want a rejection")
	}
	if _, statErr := os.Stat(repo.vectorPath()); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("Stat(vectors) = %v, want the file never written", statErr)
	}
}

func TestVectorRepoSaveRejectsRaggedVectors(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())
	ragged := map[string][]float32{
		sectionBodyHash("a"): {1, 0},
		sectionBodyHash("b"): {1, 0, 0},
	}

	if err := repo.Save(context.Background(), "model", ragged); err == nil {
		t.Fatal("Save(ragged) error = nil, want a rejection — mixed widths mean two models were merged")
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

// The upgrade path. Treating a leftover JSON cache as "no cache" would silently
// drop the service to BM25-only while it kept answering.
func TestVectorRepoLegacyJSONIsAFormatErrorNotACacheMiss(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())
	if err := os.MkdirAll(repo.vectorDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll(vectorDir) error = %v", err)
	}
	if err := os.WriteFile(repo.legacyPath(), []byte(`{"identity":"model","version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile(legacy) error = %v", err)
	}

	_, vectors, err := repo.Load(context.Background())
	if !errors.Is(err, ErrVectorCacheFormat) {
		t.Fatalf("Load() error = %v, want ErrVectorCacheFormat naming the JSON file", err)
	}
	if len(vectors) != 0 {
		t.Errorf("Load() vectors = %#v, want empty map", vectors)
	}
}

func TestVectorRepoSaveRemovesSupersededJSON(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())
	if err := os.MkdirAll(repo.vectorDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll(vectorDir) error = %v", err)
	}
	if err := os.WriteFile(repo.legacyPath(), []byte(`{"identity":"model","version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile(legacy) error = %v", err)
	}

	if err := repo.Save(context.Background(), "model", map[string][]float32{sectionBodyHash("body"): {1, 0}}); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}
	if _, err := os.Stat(repo.legacyPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Stat(legacy) = %v, want it removed — otherwise Load keeps reporting a stale format", err)
	}
	if _, _, err := repo.Load(context.Background()); err != nil {
		t.Errorf("Load() after rebuild error = %v, want nil", err)
	}
}

func TestVectorRepoReadFailureIsNotFormatError(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())
	if err := os.MkdirAll(repo.vectorDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll(vectorDir) error = %v", err)
	}
	if err := os.Mkdir(repo.vectorPath(), 0o755); err != nil {
		t.Fatalf("Mkdir(vectorPath) error = %v", err)
	}

	_, _, err := repo.Load(context.Background())
	if err == nil {
		t.Fatal("Load() error = nil, want read error")
	}
	if errors.Is(err, ErrVectorCacheFormat) {
		t.Errorf("Load() error = %v, must remain an IO/read error", err)
	}
}

func TestVectorRepoSaveLeavesCompleteFileAndNoTemporaryFile(t *testing.T) {
	repo := NewVectorRepo(t.TempDir())
	if err := repo.Save(context.Background(), "model", map[string][]float32{sectionBodyHash("body"): {1, 0}}); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}
	if err := repo.Save(context.Background(), "model", map[string][]float32{sectionBodyHash("body"): {0, 1}}); err != nil {
		t.Fatalf("second Save() error = %v, want nil", err)
	}

	if _, _, err := repo.Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v, want a complete file", err)
	}
	matches, err := filepath.Glob(filepath.Join(repo.vectorDir(), ".vectors.bin.tmp-*"))
	if err != nil {
		t.Fatalf("Glob(temporary files) error = %v, want nil", err)
	}
	if len(matches) != 0 {
		t.Errorf("temporary files = %v, want none", matches)
	}
}

// Two identical inputs must produce identical bytes: comparing digests across
// runs is how the incremental embedding cache was shown to be correct.
func TestVectorRepoEncodingIsDeterministic(t *testing.T) {
	vectors := map[string][]float32{
		sectionBodyHash("a"): {1, 0},
		sectionBodyHash("b"): {0, 1},
		sectionBodyHash("c"): {0.5, 0.5},
	}
	first, err := encodeVectors("model", vectors)
	if err != nil {
		t.Fatalf("encodeVectors() error = %v, want nil", err)
	}
	for i := 0; i < 8; i++ {
		again, err := encodeVectors("model", vectors)
		if err != nil {
			t.Fatalf("encodeVectors() error = %v, want nil", err)
		}
		if string(again) != string(first) {
			t.Fatalf("encodeVectors() differed between runs — map iteration order leaked into the file")
		}
	}
}

func writeVectorFile(t *testing.T, repo *VectorRepo, data []byte) {
	t.Helper()
	if err := os.MkdirAll(repo.vectorDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll(vectorDir) error = %v", err)
	}
	if err := os.WriteFile(repo.vectorPath(), data, 0o600); err != nil {
		t.Fatalf("WriteFile(vectors) error = %v", err)
	}
}

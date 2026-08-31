package kb

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/linkc0829/llm-platform/internal/platform/atomicfile"
)

// Vectors are stored as little-endian float32, not JSON.
//
// JSON wrote every one of 1395 × 3072 components as a decimal string: an 84MB
// file whose parse dominated startup and every cache-hit /index, measured at
// 1.72s in BenchmarkVectorLoad against 0.21s for reading the same bytes. The
// binary form is 4 bytes per component and needs no decimal conversion.
//
// Layout, all integers little-endian:
//
//	"KBVEC\x00"        magic
//	uint32             format version
//	uint32 + bytes     embedding identity (UTF-8)
//	uint32             vector count
//	uint32             components per vector
//	count × (32-byte sha256 key + dims × float32)
//
// Keys are the raw digest rather than its hex text, which is where the other
// half of the saving comes from; Load hex-encodes them back so callers still
// see the lowercase-hex keys sectionBodyHash produces.
const (
	vectorFormatVersion = 2
	vectorFileMagic     = "KBVEC\x00"
	vectorHeaderMax     = 1 << 20 // identity is a model name; anything larger is a corrupt length
)

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

	b, err := os.ReadFile(r.vectorPath())
	if errors.Is(err, os.ErrNotExist) {
		// A JSON cache left by an older build is not "no cache": falling through
		// as a miss would drop the service to BM25-only and keep answering, which
		// is the kind of degradation nobody notices. Name it instead.
		if _, statErr := os.Stat(r.legacyPath()); statErr == nil {
			return "", map[string][]float32{}, fmt.Errorf(
				"%w: %s is the previous JSON format", ErrVectorCacheFormat, r.legacyPath())
		}
		return "", map[string][]float32{}, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("read vector metadata: %w", err)
	}

	identity, vectors, err := decodeVectors(b)
	if err != nil {
		return "", map[string][]float32{}, fmt.Errorf("%w: %w", ErrVectorCacheFormat, err)
	}
	return identity, vectors, nil
}

func decodeVectors(b []byte) (string, map[string][]float32, error) {
	read := func(n int) ([]byte, error) {
		if len(b) < n {
			return nil, fmt.Errorf("vector metadata truncated: want %d more bytes, have %d", n, len(b))
		}
		out := b[:n]
		b = b[n:]
		return out, nil
	}
	magic, err := read(len(vectorFileMagic))
	if err != nil {
		return "", nil, err
	}
	if string(magic) != vectorFileMagic {
		return "", nil, errors.New("vector metadata is not a KBVEC file")
	}
	head, err := read(4)
	if err != nil {
		return "", nil, err
	}
	if version := binary.LittleEndian.Uint32(head); version != vectorFormatVersion {
		return "", nil, fmt.Errorf("unsupported vector metadata version %d", version)
	}

	head, err = read(4)
	if err != nil {
		return "", nil, err
	}
	identityLen := binary.LittleEndian.Uint32(head)
	if identityLen > vectorHeaderMax {
		return "", nil, fmt.Errorf("vector metadata identity length %d is implausible", identityLen)
	}
	raw, err := read(int(identityLen))
	if err != nil {
		return "", nil, err
	}
	identity := string(raw)
	if strings.TrimSpace(identity) == "" {
		return "", nil, errors.New("vector metadata has no embedding identity")
	}

	head, err = read(4)
	if err != nil {
		return "", nil, err
	}
	count := binary.LittleEndian.Uint32(head)
	head, err = read(4)
	if err != nil {
		return "", nil, err
	}
	dims := binary.LittleEndian.Uint32(head)
	if count > 0 && dims == 0 {
		return "", nil, errors.New("vector metadata declares vectors of zero length")
	}
	// Reject a corrupt count before allocating for it.
	if want := int64(count) * int64(sha256.Size+4*int(dims)); want != int64(len(b)) {
		return "", nil, fmt.Errorf("vector metadata declares %d vectors of %d dims (%d bytes) but %d remain",
			count, dims, want, len(b))
	}

	vectors := make(map[string][]float32, count)
	for i := uint32(0); i < count; i++ {
		key, err := read(sha256.Size)
		if err != nil {
			return "", nil, err
		}
		vector := make([]float32, dims)
		for j := range vector {
			component, err := read(4)
			if err != nil {
				return "", nil, err
			}
			vector[j] = math.Float32frombits(binary.LittleEndian.Uint32(component))
		}
		vectors[hex.EncodeToString(key)] = vector
	}
	return identity, vectors, nil
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

	b, err := encodeVectors(identity, vectors)
	if err != nil {
		return fmt.Errorf("marshal vector metadata: %w", err)
	}
	if err := atomicfile.Replace(r.vectorPath(), b, 0o600); err != nil {
		return fmt.Errorf("write vector metadata: %w", err)
	}
	// The JSON file is now unreadable by this build, and leaving it behind means
	// Load's legacy branch keeps firing after a successful rebuild.
	if err := os.Remove(r.legacyPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove superseded vector metadata: %w", err)
	}
	return nil
}

func encodeVectors(identity string, vectors map[string][]float32) ([]byte, error) {
	// One shared width: the map comes from a single embedding model, and a
	// mismatch means two models' output got merged, which must not be persisted.
	dims := 0
	for _, vector := range vectors {
		dims = len(vector)
		break
	}
	// Sorted so the file is byte-identical for identical input -- the property
	// that proved incremental embedding correct by comparing two runs' digests.
	keys := make([]string, 0, len(vectors))
	for key := range vectors {
		if !validVectorHash(key) {
			return nil, fmt.Errorf("invalid vector key %q", key)
		}
		if len(vectors[key]) != dims {
			return nil, fmt.Errorf("vector %s has %d dims, want %d", key, len(vectors[key]), dims)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	if len(identity) > vectorHeaderMax {
		return nil, fmt.Errorf("embedding identity is %d bytes, want at most %d", len(identity), vectorHeaderMax)
	}
	identityLen, err := headerCount("identity length", len(identity))
	if err != nil {
		return nil, err
	}
	vectorCount, err := headerCount("vector count", len(keys))
	if err != nil {
		return nil, err
	}
	vectorDims, err := headerCount("vector dims", dims)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, len(vectorFileMagic)+16+len(identity)+len(keys)*(sha256.Size+4*dims))
	out = append(out, vectorFileMagic...)
	out = binary.LittleEndian.AppendUint32(out, vectorFormatVersion)
	out = binary.LittleEndian.AppendUint32(out, identityLen)
	out = append(out, identity...)
	out = binary.LittleEndian.AppendUint32(out, vectorCount)
	out = binary.LittleEndian.AppendUint32(out, vectorDims)
	for _, key := range keys {
		raw, err := hex.DecodeString(key)
		if err != nil {
			return nil, fmt.Errorf("decode vector key %q: %w", key, err)
		}
		out = append(out, raw...)
		for _, component := range vectors[key] {
			out = binary.LittleEndian.AppendUint32(out, math.Float32bits(component))
		}
	}
	return out, nil
}

// headerCount narrows a length to the uint32 the header stores, refusing rather
// than truncating: a silently wrapped count writes a file that decodes as
// garbage, which is worse than failing the index run that produced it.
func headerCount(what string, n int) (uint32, error) {
	if n < 0 || uint64(n) > math.MaxUint32 {
		return 0, fmt.Errorf("%s %d does not fit the file header", what, n)
	}
	// #nosec G115 -- the range is checked on the line above; this helper exists
	// so that check lives in exactly one place.
	return uint32(n), nil
}

func validVectorHash(key string) bool {
	if len(key) != sha256.Size*2 || key != strings.ToLower(key) {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}

func (r *VectorRepo) vectorDir() string {
	return filepath.Join(r.indexDir, "faiss_index")
}

func (r *VectorRepo) vectorPath() string {
	return filepath.Join(r.vectorDir(), "vectors.bin")
}

// legacyPath is the pre-binary JSON cache, kept only so Load can say so.
func (r *VectorRepo) legacyPath() string {
	return filepath.Join(r.vectorDir(), "metadata.json")
}

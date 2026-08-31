package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/linkc0829/llm-platform/internal/shared"
)

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error = %v, want nil", err)
	}
	if !strings.HasPrefix(token, TokenPrefix) {
		t.Errorf("GenerateToken() = %q, want prefix %q", token, TokenPrefix)
	}
	raw, err := decodeTokenPayload(token)
	if err != nil {
		t.Fatalf("GenerateToken() payload error = %v, want nil", err)
	}
	if len(raw) != tokenBytes {
		t.Errorf("GenerateToken() payload length = %d, want %d", len(raw), tokenBytes)
	}
}

func TestHashAndVerifyToken(t *testing.T) {
	token := "kb_exact-header-value"
	hash := HashToken(token)
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "exact token", value: token, want: true},
		{name: "one character changed", value: "kb_exact-header-valuE", want: false},
		{name: "trimmed token", value: " kb_exact-header-value", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VerifyToken(tc.value, hash); got != tc.want {
				t.Errorf("VerifyToken(%q, %q) = %t, want %t", tc.value, hash, got, tc.want)
			}
		})
	}
}

func TestLoadFileRejectsInvalidSnapshots(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{name: "malformed json", content: []byte("{")},
		{name: "missing principal id", content: marshalDocument(t, Record{TokenSHA256: HashToken("kb_test")})},
		{name: "invalid token hash", content: marshalDocument(t, Record{Principal: shared.Principal{ID: "p_test"}, TokenSHA256: "not-a-hash"})},
		{name: "duplicate principal id", content: marshalDocument(t,
			Record{Principal: shared.Principal{ID: "p_test"}, TokenSHA256: HashToken("kb_one")},
			Record{Principal: shared.Principal{ID: "p_test"}, TokenSHA256: HashToken("kb_two")},
		)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(path, tc.content, 0o600); err != nil {
				t.Fatalf("WriteFile(%q) error = %v", path, err)
			}
			if _, err := LoadFile(path, zap.NewNop()); err == nil {
				t.Errorf("LoadFile(%q) error = nil, want startup rejection", path)
			}
		})
	}
}

func TestLoadFileRejectsMissingOrOversizedFile(t *testing.T) {
	if _, err := LoadFile("", zap.NewNop()); err == nil {
		t.Error(`LoadFile("") error = nil, want missing path rejection`)
	}

	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, make([]byte, MaxAuthFileSize+1), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	if _, err := LoadFile(path, zap.NewNop()); err == nil {
		t.Errorf("LoadFile(%q) error = nil, want oversized-file rejection", path)
	}
}

func TestLoadFileResolvesExactTokenAndReturnsPrincipalCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	want := shared.Principal{ID: "p_test", Name: "alice", Teams: []string{"store"}, Engineering: true}
	if err := os.WriteFile(path, marshalDocument(t, Record{Principal: want, TokenSHA256: HashToken("kb_secret")}), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	store, err := LoadFile(path, zap.NewNop())
	if err != nil {
		t.Fatalf("LoadFile(%q) error = %v, want nil", path, err)
	}
	got, err := store.Resolve(context.Background(), "kb_secret")
	if err != nil {
		t.Fatalf("Resolve(%q) error = %v, want nil", "kb_secret", err)
	}
	if got.ID != want.ID || got.Name != want.Name || got.Engineering != want.Engineering || len(got.Teams) != 1 || got.Teams[0] != "store" {
		t.Errorf("Resolve(%q) = %+v, want %+v", "kb_secret", got, want)
	}
	got.Teams[0] = "changed"
	again, err := store.Resolve(context.Background(), "kb_secret")
	if err != nil {
		t.Fatalf("Resolve(%q) second call error = %v, want nil", "kb_secret", err)
	}
	if again.Teams[0] != "store" {
		t.Errorf("Resolve(%q) returned mutable principal state %q, want %q", "kb_secret", again.Teams[0], "store")
	}

	if _, err := store.Resolve(context.Background(), "kb_wrong"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Resolve(%q) error = %v, want ErrInvalidToken", "kb_wrong", err)
	}
}

func decodeTokenPayload(token string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, TokenPrefix))
}

func marshalDocument(t *testing.T, records ...Record) []byte {
	t.Helper()
	contents, err := json.Marshal(fileDocument{Principals: records})
	if err != nil {
		t.Fatalf("Marshal(auth document) error = %v", err)
	}
	return contents
}

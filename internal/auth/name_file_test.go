package auth

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

func TestLoadFileRejectsDuplicatePrincipalNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	content := marshalDocument(t,
		Record{Principal: shared.Principal{ID: "p_one", Name: "alice"}, TokenSHA256: HashToken("kb_one")},
		Record{Principal: shared.Principal{ID: "p_two", Name: "alice"}, TokenSHA256: HashToken("kb_two")},
	)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	if _, err := LoadFile(path, zap.NewNop()); err == nil {
		t.Errorf("LoadFile(%q) error = nil, want duplicate-name rejection", path)
	}
}

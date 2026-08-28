package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/lockfile"
)

func TestStoreCreatePersistsAndResolvesImmediately(t *testing.T) {
	store, path := newBootstrapStore(t)
	record, token, err := store.CreateToken(context.Background(), "p_admin", TokenSpec{
		Name:        "alice",
		Teams:       []string{"Store.POS"},
		Engineering: true,
		Indexer:     true,
	})
	if err != nil {
		t.Fatalf("CreateToken() error = %v, want nil", err)
	}
	if record.Admin {
		t.Error("CreateToken().Admin = true, want false")
	}
	if token == "" {
		t.Fatal("CreateToken() token = empty, want one-time plaintext token")
	}
	principal, err := store.Resolve(context.Background(), token)
	if err != nil {
		t.Fatalf("Resolve(created token) error = %v, want nil", err)
	}
	if principal.ID != record.ID || !principal.Indexer || !principal.Engineering {
		t.Errorf("Resolve(created token) = %+v, want ID %q with capabilities", principal, record.ID)
	}

	reloaded, err := LoadFile(path, zap.NewNop())
	if err != nil {
		t.Fatalf("LoadFile(%q) error = %v, want nil", path, err)
	}
	if _, err := reloaded.Resolve(context.Background(), token); err != nil {
		t.Fatalf("Resolve(reloaded token) error = %v, want nil", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v, want nil", path, err)
	}
	if string(contents) == "" || containsToken(contents, token) {
		t.Errorf("auth snapshot contains plaintext token, contents = %q", contents)
	}
}

func TestStoreAPICannotDeleteAdminAndDeleteIsImmediate(t *testing.T) {
	store, _ := newBootstrapStore(t)
	admin, adminToken, err := store.CreateAdminToken(context.Background(), "cli", "admin")
	if err != nil {
		t.Fatalf("CreateAdminToken() error = %v, want nil", err)
	}
	regular, regularToken, err := store.CreateToken(context.Background(), admin.ID, TokenSpec{Name: "alice"})
	if err != nil {
		t.Fatalf("CreateToken() error = %v, want nil", err)
	}
	if err := store.DeleteToken(context.Background(), admin.ID, admin.ID); !errors.Is(err, ErrAdminTokenProtected) {
		t.Errorf("DeleteToken(admin) error = %v, want ErrAdminTokenProtected", err)
	}
	if _, err := store.Resolve(context.Background(), adminToken); err != nil {
		t.Fatalf("Resolve(admin token) error = %v, want admin to remain", err)
	}
	if err := store.DeleteToken(context.Background(), admin.ID, regular.ID); err != nil {
		t.Fatalf("DeleteToken(regular) error = %v, want nil", err)
	}
	if _, err := store.Resolve(context.Background(), regularToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Resolve(deleted token) error = %v, want ErrInvalidToken", err)
	}
}

func TestStoreWriteFailureDoesNotPublishMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(lockfile.Path(path), []byte("held"), 0o600); err != nil {
		t.Fatalf("WriteFile(lock) error = %v", err)
	}
	store, err := NewBootstrapStore(path, zap.NewNop())
	if err != nil {
		t.Fatalf("NewBootstrapStore(%q) error = %v, want nil", path, err)
	}
	if _, _, err := store.CreateToken(context.Background(), "p_admin", TokenSpec{Name: "alice"}); !errors.Is(err, lockfile.ErrLocked) {
		t.Errorf("CreateToken() error = %v, want lockfile.ErrLocked", err)
	}
	if _, err := store.Resolve(context.Background(), "kb_never-published"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Resolve(after failed write) error = %v, want ErrInvalidToken", err)
	}
}

func TestStoreRejectsOversizedSerializedSnapshot(t *testing.T) {
	store, _ := newBootstrapStore(t)
	_, _, err := store.CreateToken(context.Background(), "p_admin", TokenSpec{
		Name:  "oversized",
		Teams: []string{strings.Repeat("x", int(MaxAuthFileSize))},
	})
	if !errors.Is(err, ErrAuthSnapshotTooLarge) {
		t.Fatalf("CreateToken() error = %v, want ErrAuthSnapshotTooLarge", err)
	}
	if _, err := store.ListTokens(context.Background()); err != nil {
		t.Fatalf("ListTokens() error = %v, want nil", err)
	}
	if _, err := store.Resolve(context.Background(), "token-that-was-not-published"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Resolve(after oversized write) error = %v, want ErrInvalidToken", err)
	}
}

func TestStoreLastAdminRequiresForce(t *testing.T) {
	store, _ := newBootstrapStore(t)
	admin, token, err := store.CreateAdminToken(context.Background(), "cli", "admin")
	if err != nil {
		t.Fatalf("CreateAdminToken() error = %v, want nil", err)
	}
	if err := store.DeleteAdminToken(context.Background(), "cli", admin.ID, false); !errors.Is(err, ErrLastAdminRequiresForce) {
		t.Errorf("DeleteAdminToken(last admin) error = %v, want ErrLastAdminRequiresForce", err)
	}
	if err := store.DeleteAdminToken(context.Background(), "cli", admin.ID, true); err != nil {
		t.Fatalf("DeleteAdminToken(last admin, force) error = %v, want nil", err)
	}
	if _, err := store.Resolve(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Resolve(revoked admin token) error = %v, want ErrInvalidToken", err)
	}
}

func TestStoreConcurrentCreatesSerializeByName(t *testing.T) {
	store, _ := newBootstrapStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := store.CreateToken(context.Background(), "p_admin", TokenSpec{Name: "alice"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	var successes, duplicates int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrPrincipalNameExists):
			duplicates++
		default:
			t.Errorf("concurrent CreateToken() error = %v, want nil or ErrPrincipalNameExists", err)
		}
	}
	if successes != 1 || duplicates != 1 {
		t.Errorf("concurrent CreateToken() outcomes = successes %d, duplicates %d, want 1 and 1", successes, duplicates)
	}
}

func newBootstrapStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := NewBootstrapStore(path, zap.NewNop())
	if err != nil {
		t.Fatalf("NewBootstrapStore(%q) error = %v, want nil", path, err)
	}
	return store, path
}

func containsToken(contents []byte, token string) bool {
	return len(contents) > 0 && string(contents) != token && contains(string(contents), token)
}

func contains(contents, token string) bool {
	for i := 0; i+len(token) <= len(contents); i++ {
		if contents[i:i+len(token)] == token {
			return true
		}
	}
	return false
}

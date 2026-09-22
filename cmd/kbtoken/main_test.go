package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/linkc0829/llm-platform/internal/auth"
	"github.com/linkc0829/llm-platform/internal/platform/config"
)

func TestCreateAdminRecoversEmptyAuthFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := osWriteFile(path, []byte(`{"principals":[]}`)); err != nil {
		t.Fatalf("write empty auth file: %v", err)
	}
	cfg := &config.Config{Auth: config.AuthConfig{File: path}}
	var output bytes.Buffer
	if err := createAdmin(cfg, zap.NewNop(), []string{"-name", "admin"}, &output, io.Discard); err != nil {
		t.Fatalf("createAdmin() error = %v, want nil", err)
	}
	var created createAdminResponse
	if err := json.Unmarshal(output.Bytes(), &created); err != nil {
		t.Fatalf("createAdmin() output decode error = %v", err)
	}
	store, err := auth.LoadFile(path, zap.NewNop())
	if err != nil {
		t.Fatalf("LoadFile(%q) error = %v, want nil", path, err)
	}
	principal, err := store.Resolve(context.Background(), created.Token)
	if err != nil {
		t.Fatalf("Resolve(created admin) error = %v, want nil", err)
	}
	if !principal.Admin || principal.ID != created.ID {
		t.Errorf("Resolve(created admin) = %+v, want admin ID %q", principal, created.ID)
	}
}

func TestRotateAdminCreatesDifferentIDAndRevokeRequiresForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := auth.NewBootstrapStore(path, zap.NewNop())
	if err != nil {
		t.Fatalf("NewBootstrapStore() error = %v", err)
	}
	old, oldToken, err := store.CreateAdminToken(context.Background(), "cli", "admin")
	if err != nil {
		t.Fatalf("CreateAdminToken() error = %v", err)
	}
	cfg := &config.Config{Auth: config.AuthConfig{File: path}}

	var rotatedOutput bytes.Buffer
	if err := rotateAdmin(cfg, zap.NewNop(), []string{"-id", old.ID}, &rotatedOutput, io.Discard); err != nil {
		t.Fatalf("rotateAdmin() error = %v, want nil", err)
	}
	var rotated createAdminResponse
	if err := json.Unmarshal(rotatedOutput.Bytes(), &rotated); err != nil {
		t.Fatalf("rotateAdmin() output decode error = %v", err)
	}
	if rotated.ID == old.ID {
		t.Errorf("rotateAdmin() new ID = %q, want different from old ID %q", rotated.ID, old.ID)
	}

	var revokeOutput bytes.Buffer
	if err := revokeAdmin(cfg, zap.NewNop(), []string{"-id", old.ID}, &revokeOutput, io.Discard); err != nil {
		t.Fatalf("revokeAdmin(rotated store) error = %v, want nil", err)
	}
	if strings.TrimSpace(revokeOutput.String()) != old.ID {
		t.Errorf("revokeAdmin() output = %q, want old ID %q", revokeOutput.String(), old.ID)
	}
	if _, err := auth.LoadFile(path, zap.NewNop()); err != nil {
		t.Fatalf("LoadFile(%q) after rotating and revoking old admin error = %v, want one admin remaining", path, err)
	}

	if err := revokeAdmin(cfg, zap.NewNop(), []string{"-id", rotated.ID}, &bytes.Buffer{}, io.Discard); !errors.Is(err, auth.ErrLastAdminRequiresForce) {
		t.Errorf("revokeAdmin(last admin) error = %v, want ErrLastAdminRequiresForce", err)
	}
	if _, err := auth.LoadFile(path, zap.NewNop()); err != nil {
		t.Fatalf("LoadFile(%q) after rejected revoke error = %v, want admin retained", path, err)
	}
	if _, err := auth.LoadFile(path, zap.NewNop()); err != nil {
		t.Fatalf("LoadFile(%q) before force revoke error = %v, want valid snapshot", path, err)
	}
	if err := revokeAdmin(cfg, zap.NewNop(), []string{"-id", rotated.ID, "-force"}, &bytes.Buffer{}, io.Discard); err != nil {
		t.Fatalf("revokeAdmin(-force) error = %v, want nil", err)
	}
	if _, err := auth.LoadFile(path, zap.NewNop()); err == nil {
		t.Error("LoadFile() after forced last-admin revoke error = nil, want empty snapshot rejected for service startup")
	}
	recovered, err := auth.LoadBootstrapFile(path, zap.NewNop())
	if err != nil {
		t.Fatalf("LoadBootstrapFile(%q) after forced revoke error = %v, want empty recovery snapshot", path, err)
	}
	if _, err := recovered.Resolve(context.Background(), oldToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("Resolve(revoked old token) error = %v, want ErrInvalidToken", err)
	}
}

func TestEnsureServerStoppedRejectsRunningHealthEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	port := server.Listener.Addr().(*net.TCPAddr).Port
	cfg := &config.Config{HTTP: config.HTTPConfig{BindAddress: "127.0.0.1", Port: port}}
	if err := ensureServerStopped(cfg); err == nil {
		t.Fatal("ensureServerStopped() error = nil, want running service rejection")
	}
}

func osWriteFile(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0o600)
}

func TestCreateServiceToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := auth.NewBootstrapStore(path, zap.NewNop())
	if err != nil {
		t.Fatalf("NewBootstrapStore() error = %v", err)
	}
	_, _, err = store.CreateAdminToken(context.Background(), "cli", "admin")
	if err != nil {
		t.Fatalf("CreateAdminToken() error = %v", err)
	}
	cfg := &config.Config{Auth: config.AuthConfig{File: path}}

	var output bytes.Buffer
	args := []string{"-name", "kb-service", "-workload", "rag", "-trusted", "-engineering"}
	if err := create(cfg, zap.NewNop(), args, &output, io.Discard); err != nil {
		t.Fatalf("create() error = %v", err)
	}
	var created createAdminResponse
	if err := json.Unmarshal(output.Bytes(), &created); err != nil {
		t.Fatalf("decode output: %v", err)
	}

	loadedStore, err := auth.LoadFile(path, zap.NewNop())
	if err != nil {
		t.Fatalf("LoadFile error = %v", err)
	}
	p, err := loadedStore.Resolve(context.Background(), created.Token)
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if p.Name != "kb-service" || p.Workload != "rag" || !p.Trusted || !p.Engineering {
		t.Errorf("resolved principal mismatch: %+v", p)
	}
}


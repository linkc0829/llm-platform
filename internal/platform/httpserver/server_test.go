package httpserver

import (
	"testing"

	"go.uber.org/zap"
)

func TestWrapUsesConfiguredBindAddress(t *testing.T) {
	server := Wrap(New(zap.NewNop()), Config{Port: 12598, BindAddress: "127.0.0.1"}, zap.NewNop())
	if got, want := server.srv.Addr, "127.0.0.1:12598"; got != want {
		t.Errorf("Wrap(bind address) Addr = %q, want %q", got, want)
	}
}

func TestWrapKeepsAllInterfacesWhenBindAddressIsEmpty(t *testing.T) {
	server := Wrap(New(zap.NewNop()), Config{Port: 12598}, zap.NewNop())
	if got, want := server.srv.Addr, ":12598"; got != want {
		t.Errorf("Wrap(empty bind address) Addr = %q, want %q", got, want)
	}
}

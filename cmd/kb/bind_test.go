package main

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/linkc0829/llm-platform/internal/kb"
	"github.com/linkc0829/llm-platform/internal/platform/config"
)

func TestHTTPBindAddress(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{
			name: "auth_enabled_uses_configured_address",
			cfg: config.Config{HTTP: config.HTTPConfig{
				BindAddress: "0.0.0.0",
			}},
			want: "0.0.0.0",
		},
		{
			name: "auth_disabled_forces_localhost",
			cfg: config.Config{
				HTTP: config.HTTPConfig{BindAddress: "0.0.0.0"},
				Auth: config.AuthConfig{Disabled: true},
			},
			want: "127.0.0.1",
		},
		{
			name: "auth_enabled_preserves_empty_default",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := httpBindAddress(&tc.cfg); got != tc.want {
				t.Errorf("httpBindAddress() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHTTPOwnerIDProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{
			name: "auth_disabled_uses_anonymous",
			cfg:  config.Config{Auth: config.AuthConfig{Disabled: true}},
			want: kb.AnonymousOwner,
		},
		{
			name: "auth_enabled_has_no_anonymous_fallback",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := httpOwnerIDProvider(&tc.cfg)(c); got != tc.want {
				t.Errorf("httpOwnerIDProvider(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

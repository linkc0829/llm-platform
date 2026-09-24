package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Docker marks the container unhealthy only on a non-zero exit, so a 503 from
// /ready or a hung server must be an error, not a pass.
func TestCheck(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{"ok", func(http.ResponseWriter, *http.Request) {}, false},
		{"not_ready", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }, true},
		{"hangs", func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			err := check(srv.URL, 200*time.Millisecond)
			if (err != nil) != tt.wantErr {
				t.Errorf("check() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

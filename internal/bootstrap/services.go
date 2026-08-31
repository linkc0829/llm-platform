package bootstrap

import (
	"github.com/linkc0829/llm-platform/internal/auth"
	"github.com/linkc0829/llm-platform/internal/kb"
	"github.com/linkc0829/llm-platform/internal/platform/config"
)

// Services holds the composition-root snapshots shared by the HTTP adapters.
// Auth is the exact validated in-memory snapshot used for request resolution.
type Services struct {
	KB   *kb.Service
	Auth *auth.Store
}

// NewServices wires the KB service together with the already-validated auth
// snapshot. It does not read auth.json again.
func NewServices(cfg *config.Config, authStore *auth.Store) *Services {
	return &Services{
		KB:   NewKBService(cfg),
		Auth: authStore,
	}
}

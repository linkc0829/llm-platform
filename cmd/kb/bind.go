package main

import (
	"github.com/gin-gonic/gin"

	"github.com/linkc0829/llm-platform/internal/auth"
	"github.com/linkc0829/llm-platform/internal/kb"
	"github.com/linkc0829/llm-platform/internal/platform/config"
	"github.com/linkc0829/llm-platform/internal/shared"
)

func httpOwnerIDProvider(cfg *config.Config) func(*gin.Context) string {
	if cfg.Auth.Disabled {
		return func(*gin.Context) string { return kb.AnonymousOwner }
	}
	return auth.PrincipalIDFromContext
}
func httpPrincipalProvider(cfg *config.Config) func(*gin.Context) shared.Principal {
	if cfg.Auth.Disabled {
		return kb.AnonymousPrincipal
	}
	return func(c *gin.Context) shared.Principal {
		principal, _ := auth.PrincipalFromContext(c)
		return principal
	}
}
func httpBindAddress(cfg *config.Config) string {
	if cfg.Auth.Disabled {
		return "127.0.0.1"
	}
	return cfg.HTTP.BindAddress
}

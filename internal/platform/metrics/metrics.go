// Package metrics exposes a Prometheus registry and the /metrics handler.
package metrics

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the application-wide Prometheus registry.
type Registry struct {
	reg *prometheus.Registry
}

func New() *Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector())
	r.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return &Registry{reg: r}
}

func (r *Registry) Prometheus() *prometheus.Registry { return r.reg }

// Handler returns a Gin handler that serves /metrics.
func (r *Registry) Handler() gin.HandlerFunc {
	h := promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// Health is a simple liveness handler.
func Health() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type contextKey string

const metricsContextKey contextKey = "gateway.metrics"

// Handler proxies OpenAI-compatible requests with admission control,
// stream usage extraction, and structured usage logging.
type Handler struct {
	upstreamURL *url.URL
	upstreamKey string
	limiter     *Limiter
	resolver    TokenResolver
	logger      *zap.Logger
	proxy       *httputil.ReverseProxy
}

// NewHandler constructs a gateway Handler and initializes its reverse proxy.
func NewHandler(upstreamURL *url.URL, upstreamKey string, limiter *Limiter, resolver TokenResolver, headerTimeout time.Duration, logger *zap.Logger) *Handler {
	if headerTimeout <= 0 {
		headerTimeout = 300 * time.Second
	}

	h := &Handler{
		upstreamURL: upstreamURL,
		upstreamKey: upstreamKey,
		limiter:     limiter,
		resolver:    resolver,
		logger:      logger,
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: headerTimeout,
	}

	proxy := &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstreamURL)
			if upstreamKey != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+upstreamKey)
			} else {
				pr.Out.Header.Del("Authorization")
			}
			pr.Out.Header.Del("X-On-Behalf-Of")
		},
		ModifyResponse: func(resp *http.Response) error {
			if m, ok := resp.Request.Context().Value(metricsContextKey).(*requestMetrics); ok {
				m.setStatusCode(resp.StatusCode)
				if resp.StatusCode == http.StatusOK {
					contentType := resp.Header.Get("Content-Type")
					if strings.Contains(contentType, "text/event-stream") {
						resp.Body = newSSETrackingReader(resp.Body, m, time.Now())
					} else if isCompletionPath(resp.Request.URL.Path) {
						resp.Body = newJSONTrackingReader(resp.Body, m)
					}
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(r.Context().Err(), context.Canceled) || errors.Is(err, context.Canceled) {
				if m, ok := r.Context().Value(metricsContextKey).(*requestMetrics); ok {
					m.setError("client_canceled")
					m.setStatusCode(499)
				}
				return
			}
			if errors.Is(r.Context().Err(), context.DeadlineExceeded) || isTimeoutError(err) {
				if m, ok := r.Context().Value(metricsContextKey).(*requestMetrics); ok {
					m.setError("upstream_timeout")
					m.setStatusCode(http.StatusGatewayTimeout)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusGatewayTimeout)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "upstream_timeout",
				})
				return
			}
			if m, ok := r.Context().Value(metricsContextKey).(*requestMetrics); ok {
				m.setError("bad_gateway")
				m.setStatusCode(http.StatusBadGateway)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "bad_gateway",
			})
		},
	}
	h.proxy = proxy
	return h
}

// Proxy authenticates the request, enforces admission control, injects
// stream options if needed, forwards to upstream, and logs usage.
func (h *Handler) Proxy(c *gin.Context) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok || h.resolver == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	principal, err := h.resolver.Resolve(c.Request.Context(), token)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	effectiveUser := principal.ID
	onBehalfOf := strings.TrimSpace(c.GetHeader("X-On-Behalf-Of"))
	// Fallback to principal.ID when X-On-Behalf-Of is empty on a trusted token is intentional:
	// it permits trusted service/machine-to-machine callers to act on their own behalf without impersonation.
	if principal.Trusted && onBehalfOf != "" {
		effectiveUser = onBehalfOf
	}

	start := time.Now()
	metrics := &requestMetrics{}
	defer func() {
		duration := time.Since(start)
		prompt, completion, model, status, ttft, errStr := metrics.snapshot()
		if status == 0 {
			status = c.Writer.Status()
		}

		fields := []zap.Field{
			zap.String("user_id", effectiveUser),
			zap.String("principal_id", principal.ID),
			zap.String("workload", defaultWorkload(principal.Workload)),
			zap.String("path", c.Request.URL.Path),
			zap.String("model", model),
			zap.Int("status", status),
			zap.Int64("prompt_tokens", prompt),
			zap.Int64("completion_tokens", completion),
			zap.Int64("latency_ms", duration.Milliseconds()),
		}
		if ttft > 0 {
			fields = append(fields, zap.Int64("ttft_ms", ttft.Milliseconds()))
		}
		if errStr != "" {
			fields = append(fields, zap.String("error", errStr))
		}
		zap.L().Info("gateway_usage", fields...)
	}()

	release, ok, reason := h.limiter.TryAcquire(effectiveUser)
	if !ok {
		// Rejections are logged too: A2 counts them to prove overload sheds, not queues.
		metrics.setError(reason)
		metrics.setStatusCode(http.StatusTooManyRequests)
		c.Header("Retry-After", "1")
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": reason})
		return
	}
	defer release()

	if c.Request.Method == http.MethodPost && isCompletionPath(c.Request.URL.Path) {
		bodyBytes, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20))
		if err != nil {
			metrics.setError("payload_too_large")
			metrics.setStatusCode(http.StatusRequestEntityTooLarge)
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "payload too large"})
			return
		}

		var payload map[string]any
		dec := json.NewDecoder(bytes.NewReader(bodyBytes))
		dec.UseNumber()
		if err := dec.Decode(&payload); err == nil {
			if stream, ok := payload["stream"].(bool); ok && stream {
				metrics.isStream = true
				streamOpts, ok := payload["stream_options"].(map[string]any)
				if !ok {
					streamOpts = make(map[string]any)
					payload["stream_options"] = streamOpts
				}
				streamOpts["include_usage"] = true

				if newBody, err := json.Marshal(payload); err == nil {
					bodyBytes = newBody
				}
			}
			if model, ok := payload["model"].(string); ok && model != "" {
				metrics.setModel(model)
			}
		}

		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		c.Request.ContentLength = int64(len(bodyBytes))
		c.Request.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))
	}

	ctx := context.WithValue(c.Request.Context(), metricsContextKey, metrics)
	c.Request = c.Request.WithContext(ctx)

	h.proxy.ServeHTTP(c.Writer, c.Request)
}

// Healthz serves an unauthenticated liveness and readiness probe.
func (h *Handler) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func isCompletionPath(path string) bool {
	return strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/completions")
}

func defaultWorkload(w string) string {
	if w == "" {
		return "unknown"
	}
	return w
}

func bearerToken(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return "", false
	}
	return fields[1], true
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

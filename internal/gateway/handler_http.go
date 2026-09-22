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
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type contextKey string

const metricsContextKey contextKey = "gateway.metrics"

// Handler proxies OpenAI-compatible requests with admission control,
// stream usage extraction, and structured usage logging.
type Handler struct {
	upstreamURL      *url.URL
	upstreamKey      string
	embedUpstreamURL *url.URL
	embedUpstreamKey string
	embedModel       string
	limiter          *Limiter
	resolver         TokenResolver
	logger           *zap.Logger
	proxy            *httputil.ReverseProxy
}

// NewHandler constructs a gateway Handler and initializes its reverse proxy.
// ponytail: one embed model/upstream pair. Upgrade to a model→upstream map when a second model appears.
func NewHandler(
	upstreamURL *url.URL, upstreamKey string,
	embedUpstreamURL *url.URL, embedUpstreamKey, embedModel string,
	limiter *Limiter, resolver TokenResolver, headerTimeout time.Duration, logger *zap.Logger,
) *Handler {
	if headerTimeout <= 0 {
		headerTimeout = 300 * time.Second
	}

	h := &Handler{
		upstreamURL:      upstreamURL,
		upstreamKey:      upstreamKey,
		embedUpstreamURL: embedUpstreamURL,
		embedUpstreamKey: embedUpstreamKey,
		embedModel:       embedModel,
		limiter:          limiter,
		resolver:         resolver,
		logger:           logger,
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
			targetURL := upstreamURL
			targetKey := upstreamKey
			isEmbed := isEmbeddingPath(pr.In.URL.Path) && embedUpstreamURL != nil

			if isEmbed {
				targetURL = embedUpstreamURL
				targetKey = embedUpstreamKey
				pr.SetURL(targetURL)
				inboundTrimmed := strings.TrimPrefix(pr.In.URL.Path, "/v1")
				pr.Out.URL.Path = singleJoiningSlash(targetURL.Path, inboundTrimmed)
				pr.Out.URL.RawPath = ""
			} else {
				pr.SetURL(targetURL)
			}

			if targetKey != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+targetKey)
			} else {
				pr.Out.Header.Del("Authorization")
			}
			pr.Out.Header.Del("X-On-Behalf-Of")
			// Forwarding the caller's Accept-Encoding makes Transport hand back the
			// upstream's gzip bytes undecoded, and usage parsing then silently
			// yields zero. Dropping it lets Transport negotiate and decompress.
			pr.Out.Header.Del("Accept-Encoding")
		},
		ModifyResponse: func(resp *http.Response) error {
			if m, ok := resp.Request.Context().Value(metricsContextKey).(*requestMetrics); ok {
				m.setStatusCode(resp.StatusCode)
				if resp.StatusCode == http.StatusOK {
					contentType := resp.Header.Get("Content-Type")
					if strings.Contains(contentType, "text/event-stream") {
						resp.Body = newSSETrackingReader(resp.Body, m, time.Now())
					} else if isCompletionPath(resp.Request.URL.Path) || isEmbeddingPath(resp.Request.URL.Path) {
						// ponytail: buffers whole embedding response (~2MB per 32-text index batch), cap if memory matters.
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
		if inputCount, inputChars := metrics.input(); inputCount > 0 {
			fields = append(fields, zap.Int("input_count", inputCount), zap.Int("input_chars", inputChars))
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

	if isEmbeddingPath(c.Request.URL.Path) {
		if c.Request.Method != http.MethodPost {
			if h.embedModel != "" {
				metrics.setError("method_not_allowed")
				metrics.setStatusCode(http.StatusMethodNotAllowed)
				c.AbortWithStatusJSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
				return
			}
		} else {
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
			decodeErr := dec.Decode(&payload)

			var modelStr string
			if decodeErr == nil && payload != nil {
				if m, ok := payload["model"].(string); ok {
					modelStr = m
				}
				// Some upstreams (Google's OpenAI-compatible endpoint) return no usage
				// for embeddings, so the input size is the only cost signal left.
				metrics.setInput(countEmbeddingInput(payload["input"]))
			}
			if modelStr != "" {
				metrics.setModel(modelStr)
			}

			if h.embedModel != "" && (decodeErr != nil || modelStr == "" || modelStr != h.embedModel) {
				metrics.setError("unknown_embedding_model")
				metrics.setStatusCode(http.StatusBadRequest)
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unknown_embedding_model"})
				return
			}

			c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			c.Request.ContentLength = int64(len(bodyBytes))
			c.Request.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))
		}
	} else if c.Request.Method == http.MethodPost && isCompletionPath(c.Request.URL.Path) {
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

// countEmbeddingInput counts the texts in an embeddings "input" (a string or an
// array of strings / token arrays) and their characters. Token arrays count as
// inputs with zero characters.
func countEmbeddingInput(input any) (count, chars int) {
	switch v := input.(type) {
	case string:
		return 1, utf8.RuneCountInString(v)
	case []any:
		for _, item := range v {
			count++
			if text, ok := item.(string); ok {
				chars += utf8.RuneCountInString(text)
			}
		}
	}
	return count, chars
}

func isEmbeddingPath(path string) bool {
	return strings.HasSuffix(strings.TrimRight(path, "/"), "/embeddings")
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
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

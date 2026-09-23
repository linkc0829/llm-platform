package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

type requestMetrics struct {
	mu               sync.Mutex
	promptTokens     int64
	completionTokens int64
	model            string
	statusCode       int
	ttft             time.Duration
	inputCount       int
	inputChars       int
	hasFirstToken    bool
	err              string
	isStream         bool
	// hideUsage drops the usage-only SSE chunk the gateway asked for on the
	// client's behalf. Set before proxying, read once the response starts.
	hideUsage bool
}

func (m *requestMetrics) setModel(model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.model == "" {
		m.model = model
	}
}

func (m *requestMetrics) setUsage(prompt, completion int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promptTokens = prompt
	m.completionTokens = completion
}

func (m *requestMetrics) setInput(count, chars int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputCount, m.inputChars = count, chars
}

func (m *requestMetrics) input() (count, chars int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inputCount, m.inputChars
}

func (m *requestMetrics) setTTFT(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasFirstToken {
		m.ttft = d
		m.hasFirstToken = true
	}
}

func (m *requestMetrics) setStatusCode(code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusCode = code
}

func (m *requestMetrics) setError(errStr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err == "" {
		m.err = errStr
	}
}

func (m *requestMetrics) snapshot() (prompt, completion int64, model string, status int, ttft time.Duration, errStr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.promptTokens, m.completionTokens, m.model, m.statusCode, m.ttft, m.err
}

type chatCompletionUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type chatChunkChoice struct {
	Delta struct {
		Content string `json:"content"`
	} `json:"delta"`
	Text string `json:"text"`
}

type chatChunk struct {
	Model   string               `json:"model"`
	Choices []chatChunkChoice    `json:"choices"`
	Usage   *chatCompletionUsage `json:"usage"`
}

// sseTrackingReader records usage and TTFT from an SSE stream and passes it on
// line by line, dropping the usage-only chunk when metrics.hideUsage is set.
type sseTrackingReader struct {
	rc        io.ReadCloser
	metrics   *requestMetrics
	startTime time.Time
	lineBuf   bytes.Buffer // bytes read but not yet a full line
	out       bytes.Buffer // lines ready for the client
	scratch   [4096]byte
	err       error
}

func newSSETrackingReader(rc io.ReadCloser, metrics *requestMetrics, startTime time.Time) *sseTrackingReader {
	return &sseTrackingReader{
		rc:        rc,
		metrics:   metrics,
		startTime: startTime,
	}
}

func (r *sseTrackingReader) Read(p []byte) (int, error) {
	for r.out.Len() == 0 && r.err == nil {
		n, err := r.rc.Read(r.scratch[:])
		if n > 0 {
			r.lineBuf.Write(r.scratch[:n])
			r.processLines()
		}
		if err != nil {
			r.recordError(err)
			if errors.Is(err, io.EOF) && r.lineBuf.Len() > 0 {
				// A last line without a trailing newline still belongs to the client.
				r.processLine(r.lineBuf.Bytes())
				r.lineBuf.Reset()
			}
			r.err = err
		}
	}
	if r.out.Len() > 0 {
		return r.out.Read(p)
	}
	return 0, r.err
}

func (r *sseTrackingReader) recordError(err error) {
	switch {
	case errors.Is(err, io.EOF):
	case errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "canceled") || strings.Contains(err.Error(), "broken pipe"):
		// Headers already went out as 200; the log must still say the answer never arrived.
		r.metrics.setError("client_canceled")
		r.metrics.setStatusCode(499)
	case isTimeoutError(err):
		r.metrics.setError("upstream_timeout")
	default:
		r.metrics.setError(err.Error())
	}
}

func (r *sseTrackingReader) Close() error {
	return r.rc.Close()
}

func (r *sseTrackingReader) processLines() {
	for {
		i := bytes.IndexByte(r.lineBuf.Bytes(), '\n')
		if i < 0 {
			return
		}
		line := r.lineBuf.Next(i + 1)
		r.processLine(line)
	}
}

// processLine records metrics from one SSE line and queues it for the client
// unless it is the usage-only chunk the client did not ask for. The blank line
// that ended that event still goes out; an event with no data is ignored.
func (r *sseTrackingReader) processLine(line []byte) {
	if !r.track(strings.TrimSpace(string(line))) {
		r.out.Write(line)
	}
}

// track reports whether the line should be hidden from the client.
func (r *sseTrackingReader) track(line string) bool {
	if !strings.HasPrefix(line, "data:") {
		return false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" || payload == "[DONE]" {
		return false
	}

	var chunk chatChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return false
	}

	if chunk.Model != "" {
		r.metrics.setModel(chunk.Model)
	}

	for _, ch := range chunk.Choices {
		if ch.Delta.Content != "" || ch.Text != "" {
			r.metrics.setTTFT(time.Since(r.startTime))
			break
		}
	}

	if chunk.Usage != nil {
		r.metrics.setUsage(chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens)
		return r.metrics.hideUsage && len(chunk.Choices) == 0
	}
	return false
}

type jsonTrackingReader struct {
	rc      io.ReadCloser
	metrics *requestMetrics
	buf     bytes.Buffer
}

func newJSONTrackingReader(rc io.ReadCloser, metrics *requestMetrics) *jsonTrackingReader {
	return &jsonTrackingReader{
		rc:      rc,
		metrics: metrics,
	}
}

func (r *jsonTrackingReader) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if n > 0 {
		r.buf.Write(p[:n])
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			r.parseResponse()
		} else if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "canceled") || strings.Contains(err.Error(), "broken pipe") {
			// Headers already went out as 200; the log must still say the answer never arrived.
			r.metrics.setError("client_canceled")
			r.metrics.setStatusCode(499)
		} else if isTimeoutError(err) {
			r.metrics.setError("upstream_timeout")
		} else {
			r.metrics.setError(err.Error())
		}
	}
	return n, err
}

func (r *jsonTrackingReader) Close() error {
	r.parseResponse()
	return r.rc.Close()
}

func (r *jsonTrackingReader) parseResponse() {
	if r.buf.Len() == 0 {
		return
	}
	var resp struct {
		Model string               `json:"model"`
		Usage *chatCompletionUsage `json:"usage"`
	}
	if err := json.Unmarshal(r.buf.Bytes(), &resp); err == nil {
		if resp.Model != "" {
			r.metrics.setModel(resp.Model)
		}
		if resp.Usage != nil {
			r.metrics.setUsage(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		}
	}
	r.buf.Reset()
}

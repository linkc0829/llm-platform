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

type sseTrackingReader struct {
	rc        io.ReadCloser
	metrics   *requestMetrics
	startTime time.Time
	lineBuf   bytes.Buffer
}

func newSSETrackingReader(rc io.ReadCloser, metrics *requestMetrics, startTime time.Time) *sseTrackingReader {
	return &sseTrackingReader{
		rc:        rc,
		metrics:   metrics,
		startTime: startTime,
	}
}

func (r *sseTrackingReader) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if n > 0 {
		r.lineBuf.Write(p[:n])
		r.processLines()
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			// Process any trailing line left in buffer
			r.processLines()
		} else if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "canceled") || strings.Contains(err.Error(), "broken pipe") {
			r.metrics.setError("client_canceled")
		} else if isTimeoutError(err) {
			r.metrics.setError("upstream_timeout")
		} else {
			r.metrics.setError(err.Error())
		}
	}
	return n, err
}

func (r *sseTrackingReader) Close() error {
	return r.rc.Close()
}

func (r *sseTrackingReader) processLines() {
	for {
		lineBytes, err := r.lineBuf.ReadBytes('\n')
		if err != nil {
			// Not a full line yet; restore unread bytes back into buffer
			if len(lineBytes) > 0 {
				r.lineBuf.Write(lineBytes)
			}
			break
		}

		line := strings.TrimSpace(string(lineBytes))
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var chunk chatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
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
		}
	}
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
			r.metrics.setError("client_canceled")
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

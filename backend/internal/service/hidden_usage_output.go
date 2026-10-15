package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usageoutput"
	"github.com/gin-gonic/gin"
)

const hiddenUsageOutputContextKey = "pk_hidden_usage_output"

var errHiddenUsageOutput = errors.New("upstream response usage could not be processed")

// HiddenUsageOutputMiddleware transforms only the final HTTP client copy, after
// existing protocol/cache conversion. Parsers and settlement retain their own
// original bytes and counters, including paths that parse after writing.
// WebSocket frames have their own final writer and never pass through this one.
func HiddenUsageOutputMiddleware(configs ...*config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		finish := BeginHiddenUsageOutput(c, configs...)
		defer finish()
		c.Next()
	}
}

// BeginHiddenUsageOutput also supports async workers with a copied Gin context
// and a fresh recorder. A copied context must not reuse its parent's writer.
// The returned cleanup is idempotent and must run before reading a recorder.
func BeginHiddenUsageOutput(c *gin.Context, configs ...*config.Config) func() {
	if c == nil || c.Writer == nil {
		return func() {}
	}
	maxBytes := defaultUpstreamResponseReadMaxBytes
	if prior, ok := c.Get(hiddenUsageOutputContextKey); ok {
		if writer, ok := prior.(*hiddenUsageOutputWriter); ok {
			if writer.owner == c {
				return func() {}
			}
			// c.Copy() copies the internal key, but its output belongs to a new
			// request. Only inherit the immutable buffer limit, never the rate.
			maxBytes = writer.maxBytes
		}
	}
	if len(configs) > 0 && configs[0] != nil {
		maxBytes = resolveUpstreamResponseReadLimit(configs[0])
		if int64(configs[0].Gateway.MaxLineSize) > maxBytes {
			maxBytes = int64(configs[0].Gateway.MaxLineSize)
		}
	}
	original := c.Writer
	writer := &hiddenUsageOutputWriter{
		ResponseWriter: original,
		owner:          c,
		multiplier:     1,
		options:        hiddenUsageOutputOptions(c),
		maxBytes:       maxBytes,
	}
	c.Set(hiddenUsageOutputContextKey, writer)
	c.Writer = writer
	var once sync.Once
	return func() {
		once.Do(func() {
			if err := writer.finish(); err != nil {
				slog.Error("downstream usage output failed", "error", err)
				MarkOpsStreamError(c, "upstream_error", errHiddenUsageOutput.Error(), http.StatusBadGateway)
				_ = c.Error(errHiddenUsageOutput)
				writer.writeFailure()
			}
			if c.Writer == writer {
				c.Writer = original
			}
		})
	}
}

// HiddenUsageOutputError reports a failure of this context's output transform,
// including after its writer has been finalized and restored. Detached task
// handlers must use this rather than HTTP status once a keepalive committed 200.
// Internal parse details and account settings are never returned to callers.
func HiddenUsageOutputError(c *gin.Context) error {
	if c == nil {
		return nil
	}
	value, ok := c.Get(hiddenUsageOutputContextKey)
	if !ok {
		return nil
	}
	writer, ok := value.(*hiddenUsageOutputWriter)
	if !ok || writer.owner != c {
		return nil
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.err != nil {
		return errHiddenUsageOutput
	}
	return nil
}

// BindHiddenUsageOutput snapshots the account actually used by the forwarder.
// Scheduler retries update this one writer instead of wrapping it a second time.
func BindHiddenUsageOutput(c *gin.Context, account *Account) {
	if c == nil {
		return
	}
	value, ok := c.Get(hiddenUsageOutputContextKey)
	if !ok {
		return
	}
	writer, ok := value.(*hiddenUsageOutputWriter)
	if !ok || writer.owner != c {
		return
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.transportErr != nil || writer.closed {
		return
	}
	multiplier := account.HiddenBillingMultiplier()
	if len(writer.pending) != 0 && writer.multiplier != multiplier {
		writer.err = errors.New("account changed during a partial downstream response")
		return
	}
	writer.multiplier = multiplier
}

func hiddenUsageOutputOptions(c *gin.Context) usageoutput.Options {
	path := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	options := usageoutput.Options{Protocol: usageoutput.OpenAI}
	switch {
	case strings.Contains(path, "/messages"), strings.HasSuffix(path, "/systemone"):
		options.Protocol = usageoutput.Anthropic
	case strings.Contains(path, "/v1beta/"), strings.Contains(path, "/cachedContents"), strings.Contains(path, ":generateContent"), strings.Contains(path, ":streamGenerateContent"), strings.Contains(path, ":countTokens"):
		options.Protocol = usageoutput.Gemini
	}
	options.AllowBare = strings.HasSuffix(path, "/count_tokens") || strings.HasSuffix(path, "/input_tokens") || strings.HasSuffix(path, ":countTokens")
	return options
}

type hiddenUsageOutputWriter struct {
	gin.ResponseWriter
	mu                sync.Mutex
	owner             *gin.Context
	multiplier        float64
	options           usageoutput.Options
	maxBytes          int64
	pending           []byte
	prefixWritten     int
	streaming         bool
	modeSet           bool
	sentNonWhitespace bool
	err               error // response decoding/scaling failure only
	transportErr      error // original downstream write failure; never an upstream error
	closed            bool
}

var _ gin.ResponseWriter = (*hiddenUsageOutputWriter)(nil)

func (w *hiddenUsageOutputWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *hiddenUsageOutputWriter) stripContentLength() {
	if w.multiplier != 1 {
		w.ResponseWriter.Header().Del("Content-Length")
	}
}

func (w *hiddenUsageOutputWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stripContentLength()
	w.ResponseWriter.WriteHeader(code)
}

func (w *hiddenUsageOutputWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stripContentLength()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *hiddenUsageOutputWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *hiddenUsageOutputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.transportErr != nil {
		return 0, w.transportErr
	}
	if w.err != nil {
		return 0, errHiddenUsageOutput
	}
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	if w.multiplier == 1 || len(data) == 0 {
		return w.writeUnchanged(data)
	}
	w.stripContentLength()
	contentType := strings.ToLower(w.ResponseWriter.Header().Get("Content-Type"))
	streaming := w.streaming
	jsonBody := w.modeSet && !streaming
	if !w.modeSet {
		streaming = strings.Contains(contentType, "text/event-stream")
		jsonBody = strings.Contains(contentType, "json")
	}
	if !w.modeSet && (contentType == "" || strings.HasPrefix(contentType, "text/plain")) {
		trimmed := bytes.TrimSpace(data)
		jsonBody = len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
		if len(trimmed) > 0 {
			for _, prefix := range []string{"data:", "event:", "id:", "retry:", ":"} {
				if bytes.HasPrefix(trimmed, []byte(prefix)) || bytes.HasPrefix([]byte(prefix), trimmed) {
					streaming = true
					break
				}
			}
		}
	}
	if len(w.pending) == 0 && !streaming && !jsonBody {
		return w.writeUnchanged(data)
	}
	if len(w.pending) == 0 {
		w.streaming = streaming
		w.modeSet = true
		// JSON keepalive whitespace is already valid output. Passing it on
		// immediately preserves the real first-byte timestamp and heartbeat.
		if !streaming && len(bytes.TrimSpace(data)) == 0 {
			return w.writeUnchanged(data)
		}
		if !streaming && json.Valid(data) {
			if int64(len(data)) > w.maxBytes {
				w.err = ErrUpstreamResponseBodyTooLarge
				return 0, errHiddenUsageOutput
			}
			transformed, err := usageoutput.ScaleJSON(data, w.multiplier, w.options)
			if err != nil {
				w.err = err
				return 0, errHiddenUsageOutput
			}
			if err := w.writeBytes(transformed); err != nil {
				return 0, err
			}
			return len(data), nil
		}
	}
	if int64(len(w.pending))+int64(len(data)) > w.maxBytes {
		w.err = ErrUpstreamResponseBodyTooLarge
		return 0, errHiddenUsageOutput
	}
	// Own the bytes: some forwarders parse the original body after c.Data.
	w.pending = append(w.pending, data...)
	if err := w.drain(false); err != nil {
		if w.transportErr != nil {
			return 0, w.transportErr
		}
		w.err = err
		return 0, errHiddenUsageOutput
	}
	if len(w.pending) > 0 && w.prefixWritten == 0 {
		// Emit one unchanged framing byte while retaining partial numeric
		// fields. A successful buffered Write must not invent an earlier
		// first-byte measurement in the heartbeat writer above this layer.
		if err := w.writeBytes(w.pending[:1]); err != nil {
			return 0, err
		}
		w.prefixWritten = 1
	}
	return len(data), nil
}

func (w *hiddenUsageOutputWriter) writeBytes(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	_, err := w.writeUnchanged(data)
	return err
}

func (w *hiddenUsageOutputWriter) writeUnchanged(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if n > 0 && len(bytes.TrimSpace(data[:min(n, len(data))])) > 0 {
		w.sentNonWhitespace = true
	}
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.transportErr = err
	}
	return n, err
}

func (w *hiddenUsageOutputWriter) drain(final bool) error {
	for len(w.pending) > 0 {
		end := len(w.pending)
		var transformed []byte
		var err error
		if w.streaming {
			end = hiddenUsageSSEFrameEnd(w.pending)
			if end == 0 {
				if !final {
					return nil
				}
				end = len(w.pending)
			}
			transformed, err = rewriteHiddenUsageSSEFrame(w.pending[:end], w.multiplier, w.options)
		} else {
			if !json.Valid(w.pending) {
				if !final {
					return nil
				}
				return errors.New("incomplete downstream JSON response")
			}
			transformed, err = usageoutput.ScaleJSON(w.pending, w.multiplier, w.options)
		}
		if err != nil {
			return err
		}
		if len(transformed) < w.prefixWritten || !bytes.Equal(transformed[:w.prefixWritten], w.pending[:w.prefixWritten]) {
			return errors.New("downstream usage rewrite changed a framing prefix")
		}
		if err := w.writeBytes(transformed[w.prefixWritten:]); err != nil {
			return err
		}
		w.pending = w.pending[end:]
		w.prefixWritten = 0
	}
	w.pending = nil
	return nil
}

func hiddenUsageSSEFrameEnd(data []byte) int {
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' && data[i] != '\r' {
			continue
		}
		end := i + 1
		if data[i] == '\r' && end < len(data) && data[end] == '\n' {
			end++
		}
		if i == start {
			return end
		}
		start = end
		i = end - 1
	}
	return 0
}

func rewriteHiddenUsageSSEFrame(frame []byte, multiplier float64, options usageoutput.Options) ([]byte, error) {
	lines := hiddenUsageSSELines(frame)
	var indices []int
	var prefixes, endings, payloads [][]byte
	for i, line := range lines {
		content := bytes.TrimSuffix(line, []byte{'\n'})
		content = bytes.TrimSuffix(content, []byte{'\r'})
		if !bytes.HasPrefix(content, []byte("data:")) {
			continue
		}
		start := len("data:")
		if len(content) > start && content[start] == ' ' {
			start++
		}
		indices = append(indices, i)
		prefixes = append(prefixes, line[:start])
		endings = append(endings, line[len(content):])
		payloads = append(payloads, content[start:])
	}
	if len(indices) == 0 {
		return frame, nil
	}
	payload := bytes.Join(payloads, []byte{'\n'})
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[DONE]")) {
		return frame, nil
	}
	// Provider text and error sentinels are not usage JSON. A truncated JSON
	// event, however, must not fall back to exposing untransformed counters.
	if !json.Valid(payload) && trimmed[0] != '{' && trimmed[0] != '[' {
		return frame, nil
	}
	transformed, err := usageoutput.ScaleJSON(payload, multiplier, options)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(transformed, payload) {
		return frame, nil
	}
	replacement := bytes.Split(transformed, []byte{'\n'})
	if len(replacement) != len(indices) {
		return nil, errors.New("downstream usage rewrite changed SSE line framing")
	}
	for i, index := range indices {
		line := make([]byte, 0, len(prefixes[i])+len(replacement[i])+len(endings[i]))
		line = append(line, prefixes[i]...)
		line = append(line, replacement[i]...)
		line = append(line, endings[i]...)
		lines[index] = line
	}
	return bytes.Join(lines, nil), nil
}

func hiddenUsageSSELines(frame []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(frame); i++ {
		if frame[i] != '\n' && frame[i] != '\r' {
			continue
		}
		end := i + 1
		if frame[i] == '\r' && end < len(frame) && frame[end] == '\n' {
			end++
		}
		lines = append(lines, frame[start:end])
		start = end
		i = end - 1
	}
	if start < len(frame) {
		lines = append(lines, frame[start:])
	}
	return lines
}

func (w *hiddenUsageOutputWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil || w.transportErr != nil || w.closed {
		return
	}
	w.stripContentLength()
	w.ResponseWriter.Flush()
}

func (w *hiddenUsageOutputWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.transportErr != nil {
		return nil, nil, w.transportErr
	}
	if w.err != nil || len(w.pending) != 0 {
		return nil, nil, errHiddenUsageOutput
	}
	return w.ResponseWriter.Hijack()
}

func (w *hiddenUsageOutputWriter) finish() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.transportErr != nil {
		// The client connection already failed. Do not drain/retry its bytes or
		// report the transport error as invalid upstream usage during cleanup.
		w.closed = true
		w.pending = nil
		return nil
	}
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.err == nil {
		if err := w.drain(true); w.transportErr == nil {
			w.err = err
		}
	}
	w.pending = nil
	return w.err
}

func (w *hiddenUsageOutputWriter) writeFailure() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.transportErr != nil {
		return
	}
	message := `{"error":{"type":"upstream_error","message":"Upstream response usage could not be processed"}}`
	if !w.Written() {
		w.ResponseWriter.Header().Del("Content-Length")
		w.ResponseWriter.Header().Set("Content-Type", "application/json")
		w.ResponseWriter.WriteHeader(http.StatusBadGateway)
		_, _ = w.ResponseWriter.WriteString(message)
		return
	}
	if !w.sentNonWhitespace && strings.Contains(w.ResponseWriter.Header().Get("Content-Type"), "json") {
		// Image keepalive may have committed HTTP 200 using whitespace only.
		// The status cannot be replaced, but the body can still be valid JSON.
		_, _ = w.ResponseWriter.WriteString(message)
		return
	}
	if strings.Contains(w.ResponseWriter.Header().Get("Content-Type"), "text/event-stream") {
		_, _ = fmt.Fprintf(w.ResponseWriter, "\n\nevent: error\ndata: %s\n\n", message)
		w.ResponseWriter.Flush()
	}
}

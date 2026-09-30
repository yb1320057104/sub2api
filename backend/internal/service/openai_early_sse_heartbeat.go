package service

import (
	"bufio"
	"context"
	"encoding/json"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const earlySSEHeartbeatKey = "openai_early_sse_heartbeat"

type firstResponseTimingKey struct{}

type firstResponseTiming struct {
	started time.Time
	// Store elapsed milliseconds plus one, so a successful zero-ms write is distinct from no write.
	value atomic.Int64
}

func (t *firstResponseTiming) record() {
	ms := max(int64(0), time.Since(t.started).Milliseconds())
	t.value.CompareAndSwap(0, ms+1)
}

// FirstResponseMsFromContext measures the first nonempty downstream body write,
// including an SSE comment. It is independent of upstream first-token timing.
func FirstResponseMsFromContext(ctx context.Context) *int {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(firstResponseTimingKey{}).(*firstResponseTiming)
	if t == nil || t.value.Load() == 0 {
		return nil
	}
	ms := int(t.value.Load() - 1)
	return &ms
}

// CopyFirstResponseTiming preserves timing when usage work gets a detached context.
func CopyFirstResponseTiming(parent, base context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	if parent != nil {
		if t, ok := parent.Value(firstResponseTimingKey{}).(*firstResponseTiming); ok {
			return context.WithValue(base, firstResponseTimingKey{}, t)
		}
	}
	return base
}

// StartEarlySSEHeartbeat is called only for validated Responses, Chat and Messages
// SSE requests. Compact, WebSocket, images and nonstreaming requests are excluded
// by the handler. Disabling early comments still collects first-response timing.
func (s *OpenAIGatewayService) StartEarlySSEHeartbeat(c *gin.Context, requestStart time.Time, protocol string) func() {
	settings := StreamEarlyHeartbeatSettings{}
	if s != nil && s.settingService != nil && c != nil && c.Request != nil {
		settings = s.settingService.GetStreamEarlyHeartbeatSettings(c.Request.Context())
	}
	return startEarlySSEHeartbeat(c, requestStart, protocol, settings)
}

func earlySSEHeartbeatDelay(settings StreamEarlyHeartbeatSettings) time.Duration {
	// Bound defensively even if a corrupt stored setting bypasses API validation.
	low := min(max(settings.MinDelayMS, 0), 30000)
	high := min(max(settings.MaxDelayMS, low), 30000)
	return time.Duration(low+rand.IntN(high-low+1)) * time.Millisecond
}

func startEarlySSEHeartbeat(c *gin.Context, requestStart time.Time, protocol string, settings StreamEarlyHeartbeatSettings) func() {
	if c == nil || c.Writer == nil || c.Request == nil || earlySSEWriterFromContext(c) != nil {
		return func() {}
	}
	if _, compact := c.Get(openAICompactSSEKeepaliveKey); compact {
		return func() {}
	}
	if requestStart.IsZero() {
		requestStart = time.Now()
	}
	timing := &firstResponseTiming{started: requestStart}
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), firstResponseTimingKey{}, timing))
	original := c.Writer
	w := &earlySSEHeartbeatWriter{
		ResponseWriter: original, c: c, protocol: protocol,
		headers: original.Header().Clone(), stableHeaders: original.Header().Clone(),
		status: original.Status(), timing: timing,
		stop: make(chan struct{}), wake: make(chan struct{}, 1), done: make(chan struct{}),
	}
	if settings.Enabled {
		w.firstDue = requestStart.Add(earlySSEHeartbeatDelay(settings))
	}
	c.Set(earlySSEHeartbeatKey, w)
	c.Writer = w
	go w.run(c.Request.Context().Done())
	return func() {
		w.mu.Lock()
		w.stopLocked()
		if !original.Written() {
			w.copyHeadersLocked(w.headers)
			original.WriteHeader(w.status)
		}
		w.mu.Unlock()
		<-w.done
		if c.Writer == w {
			c.Writer = original
		}
	}
}

// Header belongs exclusively to the request goroutine. The timer only reads
// stableHeaders, cloned before it starts. Returning a shared header map and then
// locking Write would still race with Header().Set calls while committing a beat.
type earlySSEHeartbeatWriter struct {
	gin.ResponseWriter
	c             *gin.Context
	protocol      string
	headers       http.Header
	stableHeaders http.Header
	timing        *firstResponseTiming
	mu            sync.Mutex
	status        int
	stopped       bool
	committed     bool
	terminal      bool
	bytes         int
	firstDue      time.Time
	repeatDue     time.Time
	interval      time.Duration
	stop          chan struct{}
	wake          chan struct{}
	done          chan struct{}
}

func (w *earlySSEHeartbeatWriter) stopLocked() {
	if !w.stopped {
		w.stopped = true
		close(w.stop)
	}
}

func (w *earlySSEHeartbeatWriter) run(requestDone <-chan struct{}) {
	defer close(w.done)
	for {
		w.mu.Lock()
		if w.stopped {
			w.mu.Unlock()
			return
		}
		due := w.firstDue
		if !w.repeatDue.IsZero() && (due.IsZero() || w.repeatDue.Before(due)) {
			due = w.repeatDue
		}
		w.mu.Unlock()
		var timer *time.Timer
		var tick <-chan time.Time
		if !due.IsZero() {
			timer = time.NewTimer(max(time.Duration(0), time.Until(due)))
			tick = timer.C
		}
		select {
		case <-requestDone:
			w.mu.Lock()
			w.stopLocked()
			w.mu.Unlock()
		case <-w.stop:
		case <-w.wake:
		case <-tick:
			w.beat()
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (w *earlySSEHeartbeatWriter) copyHeadersLocked(source http.Header) {
	target := w.ResponseWriter.Header()
	for key := range target {
		delete(target, key)
	}
	for key, values := range source {
		target[key] = append([]string(nil), values...)
	}
}

func (w *earlySSEHeartbeatWriter) beat() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	now := time.Now()
	if (w.firstDue.IsZero() || now.Before(w.firstDue)) && (w.repeatDue.IsZero() || now.Before(w.repeatDue)) {
		return
	}
	if !w.ResponseWriter.Written() {
		w.copyHeadersLocked(w.stableHeaders)
		h := w.ResponseWriter.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		h.Del("Content-Length")
		w.ResponseWriter.WriteHeader(http.StatusOK)
	} else if !strings.Contains(w.ResponseWriter.Header().Get("Content-Type"), "text/event-stream") {
		w.stopLocked()
		return
	}
	// No gin.Context access from the timer goroutine: all accounting is local.
	w.committed = true
	n, err := w.ResponseWriter.Write([]byte(": keepalive\n\n"))
	w.bytes += n
	if n > 0 {
		w.timing.record()
	}
	if err != nil {
		w.stopLocked()
		return
	}
	w.ResponseWriter.Flush()
	w.firstDue = time.Time{}
	if w.interval > 0 {
		w.repeatDue = time.Now().Add(w.interval)
	}
}

// Passthrough's existing periodic pre-output keepalive uses this same scheduler
// and writer. It remains available when the optional early heartbeat is disabled.
func (w *earlySSEHeartbeatWriter) periodic(interval time.Duration) func() {
	w.mu.Lock()
	if !w.stopped {
		w.interval = interval
		w.repeatDue = time.Now().Add(interval)
	}
	w.mu.Unlock()
	w.notify()
	return func() {
		w.mu.Lock()
		w.interval = 0
		w.repeatDue = time.Time{}
		w.mu.Unlock()
		w.notify()
	}
}

func (w *earlySSEHeartbeatWriter) notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func earlySSEWriterFromContext(c *gin.Context) *earlySSEHeartbeatWriter {
	if c == nil {
		return nil
	}
	value, _ := c.Get(earlySSEHeartbeatKey)
	w, _ := value.(*earlySSEHeartbeatWriter)
	return w
}

func EarlySSEHeartbeatCommitted(c *gin.Context) bool {
	w := earlySSEWriterFromContext(c)
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.committed
}

func StopEarlySSEHeartbeatCommitted(c *gin.Context) bool {
	w := earlySSEWriterFromContext(c)
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
	return w.committed
}

// EarlySSEAdjustedWrittenSize excludes only this controller's comments. Other
// stream comments are subtracted by OpenAICompactKeepaliveAdjustedWrittenSize.
func EarlySSEAdjustedWrittenSize(c *gin.Context) int {
	if c == nil || c.Writer == nil {
		return -1
	}
	w := earlySSEWriterFromContext(c)
	if w == nil {
		return c.Writer.Size()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	size := w.ResponseWriter.Size()
	if w.bytes > 0 {
		if size <= w.bytes {
			return -1
		}
		return size - w.bytes
	}
	return size
}

func (w *earlySSEHeartbeatWriter) Header() http.Header { return w.headers }

func (w *earlySSEHeartbeatWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = code
	// Successful streaming headers often arrive well before the first body byte.
	// Errors take ownership immediately so a fast JSON error keeps its HTTP status.
	if code >= 300 {
		w.stopLocked()
	}
}

func (w *earlySSEHeartbeatWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
	if w.terminal {
		return len(data), nil
	}
	if w.committed && (w.status >= 400 || (strings.Contains(w.headers.Get("Content-Type"), "application/json") && json.Valid(data))) {
		frame, errType, message, status := w.errorFrame(data)
		w.terminal = true
		MarkOpsStreamError(w.c, errType, message, status)
		MarkResponseCommitted(w.c)
		n, err := w.ResponseWriter.Write(frame)
		if n > 0 {
			w.timing.record()
		}
		w.ResponseWriter.Flush()
		if err != nil {
			return 0, err
		}
		return len(data), nil
	}
	if !w.ResponseWriter.Written() {
		w.copyHeadersLocked(w.headers)
		w.ResponseWriter.WriteHeader(w.status)
	}
	n, err := w.ResponseWriter.Write(data)
	if n > 0 {
		w.timing.record()
	}
	return n, err
}

func (w *earlySSEHeartbeatWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *earlySSEHeartbeatWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.ResponseWriter.Written() {
		w.copyHeadersLocked(w.headers)
		w.ResponseWriter.WriteHeader(w.status)
	}
	w.ResponseWriter.WriteHeaderNow()
}

func (w *earlySSEHeartbeatWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.ResponseWriter.Written() {
		w.copyHeadersLocked(w.headers)
		w.ResponseWriter.WriteHeader(w.status)
	}
	w.ResponseWriter.Flush()
}

func (w *earlySSEHeartbeatWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ResponseWriter.Written() {
		return w.ResponseWriter.Status()
	}
	return w.status
}

func (w *earlySSEHeartbeatWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Size()
}

func (w *earlySSEHeartbeatWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Written()
}

func (w *earlySSEHeartbeatWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
	return w.ResponseWriter.Hijack()
}

func (w *earlySSEHeartbeatWriter) errorFrame(data []byte) ([]byte, string, string, int) {
	errType, message, code := "upstream_error", "Upstream request failed", ""
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil {
		if body.Error.Type != "" {
			errType = body.Error.Type
		}
		if body.Error.Message != "" {
			message = body.Error.Message
		}
		code = body.Error.Code
	}
	status := w.status
	if status < 400 {
		status = http.StatusBadGateway
	}
	errorObject := map[string]any{"type": errType, "message": message}
	if code != "" {
		errorObject["code"] = code
	}
	event, payload := "error", map[string]any{"error": errorObject}
	switch w.protocol {
	case "responses":
		if code == "" {
			code = errType
		}
		event = "response.failed"
		payload = map[string]any{
			"type": event, "sequence_number": 0,
			"response": map[string]any{
				"id": "resp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
				"object": "response", "created_at": time.Now().Unix(),
				"status": "failed", "output": []any{},
				"error": map[string]any{"code": code, "message": message},
			},
		}
	case "messages":
		payload["type"] = "error"
	}
	encoded, _ := json.Marshal(payload)
	return []byte("event: " + event + "\ndata: " + string(encoded) + "\n\n"), errType, message, status
}

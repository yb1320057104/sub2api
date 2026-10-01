package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
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
	ms := time.Since(t.started).Milliseconds()
	if ms < 0 {
		ms = 0
	}
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
// by the handler. Disabling early heartbeats still collects first-response timing.
func (s *OpenAIGatewayService) StartEarlySSEHeartbeat(c *gin.Context, requestStart time.Time, protocol string, requestBody ...[]byte) func() {
	settings := StreamEarlyHeartbeatSettings{}
	if s != nil && s.settingService != nil && c != nil && c.Request != nil {
		settings = s.settingService.GetStreamEarlyHeartbeatSettings(c.Request.Context())
	}
	return startEarlySSEHeartbeat(c, requestStart, protocol, settings, earlySpaceRequestSnapshot(c, requestBody...))
}

func earlySSEHeartbeatDelay(settings StreamEarlyHeartbeatSettings) time.Duration {
	// Bound defensively even if a corrupt stored setting bypasses API validation.
	low := settings.MinDelayMS
	if low < 0 {
		low = 0
	} else if low > 30000 {
		low = 30000
	}
	high := settings.MaxDelayMS
	if high < low {
		high = low
	} else if high > 30000 {
		high = 30000
	}
	return time.Duration(low+rand.IntN(high-low+1)) * time.Millisecond
}

func startEarlySSEHeartbeat(c *gin.Context, requestStart time.Time, protocol string, settings StreamEarlyHeartbeatSettings, snapshots ...earlySpaceRequest) func() {
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
		mode:    StreamEarlyHeartbeatModeComment,
		headers: original.Header().Clone(), stableHeaders: original.Header().Clone(),
		status: original.Status(), timing: timing,
		stop: make(chan struct{}), wake: make(chan struct{}, 1), done: make(chan struct{}),
	}
	if settings.Mode == StreamEarlyHeartbeatModeDownstreamPing {
		w.mode = StreamEarlyHeartbeatModeDownstreamPing
	}
	if settings.Enabled && settings.Mode == StreamEarlyHeartbeatModeSpace {
		request := earlySpaceRequestSnapshot(c)
		if len(snapshots) > 0 {
			request = snapshots[0]
		}
		if (protocol == "responses" && !request.structured) || (protocol == "chat" && request.model != "" && !request.chatResponsesInput) {
			w.mode = StreamEarlyHeartbeatModeSpace
			w.space = newEarlySSESpaceState(protocol, request.model, requestStart)
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.EarlySSESpaceRetryGate, w.prepareSpaceRetry))
		}
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
		w.finishSpaceLocked()
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
	mode          string
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
	space         *earlySSESpaceState
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
			delay := time.Until(due)
			if delay < 0 {
				delay = 0
			}
			timer = time.NewTimer(delay)
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
	firstDue := !w.firstDue.IsZero() && !now.Before(w.firstDue)
	if !firstDue && (w.repeatDue.IsZero() || now.Before(w.repeatDue)) {
		return
	}
	if w.space != nil && firstDue {
		w.space.due = true
		w.firstDue = time.Time{}
		// Partial source frames belong to the request goroutine. Never insert
		// a placeholder until their meaning and event boundary are known.
		if len(w.space.pending) > 0 {
			if w.interval > 0 {
				w.repeatDue = now.Add(w.interval)
			}
			return
		}
		if w.space.canInject() {
			if err := w.emitSpaceLocked(); err != nil {
				w.space.err = err
				w.stopLocked()
			}
			return
		}
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
	// Ping is an explicit compatibility option for downstream gateways that
	// count data events. It is not a Responses/Chat model event; strict clients
	// must use the default comment mode. Periodic keepalive remains a comment.
	frame := ": keepalive\n\n"
	if firstDue && w.mode == StreamEarlyHeartbeatModeDownstreamPing {
		frame = "data: {\"type\":\"ping\"}\n\n"
		if w.protocol == "messages" {
			frame = "event: ping\n" + frame
		}
	}
	w.committed = true
	n, err := w.ResponseWriter.Write([]byte(frame))
	if err == nil && n < len(frame) {
		err = io.ErrShortWrite
	}
	w.bytes += n
	if n > 0 {
		w.timing.record()
	}
	if err != nil {
		w.stopLocked()
		return
	}
	w.ResponseWriter.Flush()
	// A periodic comment can arrive before the configured first ping. Keep
	// that ping pending; comment mode retains its original coalescing behavior.
	if firstDue || (w.mode != StreamEarlyHeartbeatModeDownstreamPing && w.mode != StreamEarlyHeartbeatModeSpace) {
		w.firstDue = time.Time{}
	}
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

// EarlySSEAdjustedWrittenSize excludes only this controller's heartbeat bytes.
// Other stream heartbeats are subtracted by OpenAICompactKeepaliveAdjustedWrittenSize.
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
	if w.space != nil {
		return w.writeSpaceLocked(data)
	}
	return w.writeOrdinaryLocked(data)
}

func (w *earlySSEHeartbeatWriter) writeOrdinaryLocked(data []byte) (int, error) {
	transportHeartbeat, protocolPing := classifyCompleteSSEHeartbeat(data)
	if len(data) > 0 && !transportHeartbeat {
		w.stopLocked()
	}
	if w.terminal {
		return len(data), nil
	}
	if w.committed && (w.status >= 400 || (strings.Contains(w.headers.Get("Content-Type"), "application/json") && json.Valid(data))) {
		frame, errType, message, status := w.errorFrame(data)
		w.terminal = true
		MarkOpsStreamError(w.c, errType, message, status)
		MarkResponseCommitted(w.c)
		n, err := w.ResponseWriter.Write(frame)
		if err == nil && n < len(frame) {
			err = io.ErrShortWrite
		}
		if n > 0 {
			w.timing.record()
		}
		if err != nil {
			w.stopLocked()
			return 0, err
		}
		w.ResponseWriter.Flush()
		return len(data), nil
	}
	if !w.ResponseWriter.Written() {
		w.copyHeadersLocked(w.headers)
		w.ResponseWriter.WriteHeader(w.status)
	}
	n, err := w.ResponseWriter.Write(data)
	if err == nil && n < len(data) {
		err = io.ErrShortWrite
	}
	if n > 0 {
		w.timing.record()
		if transportHeartbeat {
			// Queue and stream readers already account for their own heartbeats.
			// Do not add them to bytes, which counts only this timer's writes.
			// A queue comment does not satisfy the explicit data-ping mode. A
			// real queue ping does; both leave periodic keepalive available.
			w.committed = true
			if err == nil {
				if (protocolPing && w.mode != StreamEarlyHeartbeatModeSpace) || (w.mode != StreamEarlyHeartbeatModeDownstreamPing && w.mode != StreamEarlyHeartbeatModeSpace) {
					w.firstDue = time.Time{}
				}
				if w.interval > 0 {
					w.repeatDue = time.Now().Add(w.interval)
				}
				w.notify()
			}
		}
	}
	if err != nil {
		w.stopLocked()
	}
	return n, err
}

// Only complete transport frames are exempt from semantic takeover. Mixed,
// partial, metadata and real data frames must still stop the timer, so a comment
// can never be inserted into the middle of a business event.
func isCompleteSSEHeartbeat(data []byte) bool {
	heartbeat, _ := classifyCompleteSSEHeartbeat(data)
	return heartbeat
}

// classifyCompleteSSEHeartbeat also distinguishes a data ping from comments so
// queue comments cannot consume a pending downstream-compatible first ping.
func classifyCompleteSSEHeartbeat(data []byte) (heartbeat, ping bool) {
	if !bytes.HasSuffix(data, []byte("\n\n")) && !bytes.HasSuffix(data, []byte("\r\n\r\n")) {
		return false, false
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return false, false
	}
	if trimmed[0] != ':' && !bytes.HasPrefix(trimmed, []byte("data:")) && !bytes.HasPrefix(trimmed, []byte("event: ping")) {
		return false, false
	}
	var pingData []byte
	hasPingEvent := false
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 || line[0] == ':' {
			continue
		}
		if bytes.Equal(line, []byte("event: ping")) {
			hasPingEvent = true
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) || pingData != nil {
			return false, false
		}
		pingData = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
	}
	if pingData == nil {
		// An event name without a data payload is not one of our ping frames.
		return !hasPingEvent && trimmed[0] == ':', false
	}
	// The supported queue ping is tiny. Avoid decoding ordinary model chunks
	// merely to discover that they are not transport frames.
	if len(pingData) > 64 || !bytes.Contains(pingData, []byte(`"ping"`)) {
		return false, false
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(pingData, &payload) != nil || len(payload) != 1 {
		return false, false
	}
	var kind string
	ping = json.Unmarshal(payload["type"], &kind) == nil && kind == "ping"
	return ping, ping
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
				"id":     "resp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
				"object": "response", "created_at": time.Now().Unix(),
				"status": "failed", "output": []any{},
				"error": map[string]any{"code": code, "message": message},
			},
		}
		if w.space != nil && w.space.responseID != "" {
			payload["sequence_number"] = w.space.nextSequence
			w.space.nextSequence++
			response := payload["response"].(map[string]any)
			response["id"] = w.space.responseID
			if w.space.active {
				output, _ := w.space.mergeOutputSnapshot(nil, true)
				placeholder, _ := json.Marshal(w.space.placeholderItem())
				response["output"] = append([]json.RawMessage{placeholder}, output...)
			}
		}
	case "messages":
		payload["type"] = "error"
	}
	encoded, _ := json.Marshal(payload)
	return []byte("event: " + event + "\ndata: " + string(encoded) + "\n\n"), errType, message, status
}

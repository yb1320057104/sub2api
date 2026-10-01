package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// Space mode changes model output deliberately. Its state is request-local and
// protected by the writer mutex; neither the timer nor this transformer reads Gin.
type earlySpaceRequest struct {
	model              string
	structured         bool
	chatResponsesInput bool
}

func earlySpaceRequestSnapshot(c *gin.Context, body ...[]byte) earlySpaceRequest {
	var snapshot earlySpaceRequest
	if c != nil && c.Request != nil {
		snapshot.model, _ = c.Request.Context().Value(ctxkey.Model).(string)
	}
	if len(body) > 0 {
		if snapshot.model == "" {
			snapshot.model = gjson.GetBytes(body[0], "model").String()
		}
		snapshot.structured = earlySpaceStructuredFormat(gjson.GetBytes(body[0], "text.format.type").String())
		// A supported Cursor-style /chat/completions request can take a native
		// Responses route. Its actual SSE dialect is unknown until selection.
		snapshot.chatResponsesInput = gjson.GetBytes(body[0], "input").Exists() && !gjson.GetBytes(body[0], "messages").Exists()
	}
	return snapshot
}

func earlySpaceStructuredFormat(kind string) bool {
	return kind == "json_schema" || kind == "json_object"
}

type earlySpaceObject map[string]json.RawMessage

type earlySpacePart struct {
	added    bool
	done     bool
	textDone bool
	text     strings.Builder
	snapshot earlySpaceObject
}

type earlySpaceItem struct {
	added    bool
	done     bool
	parts    map[int]*earlySpacePart
	snapshot earlySpaceObject
}

type earlySSESpaceState struct {
	protocol      string
	model         string
	created       int64
	chatID        string
	responseID    string
	placeholderID string
	nextSequence  int64
	due           bool
	active        bool
	written       bool
	bypass        bool
	err           error
	pending       []byte
	frameScan     int
	lineStart     int
	items         map[int]*earlySpaceItem
}

func newEarlySSESpaceState(protocol, model string, started time.Time) *earlySSESpaceState {
	identifier := strings.ReplaceAll(uuid.NewString(), "-", "")
	return &earlySSESpaceState{
		protocol: protocol, model: model, created: started.Unix(),
		chatID: "chatcmpl_pk_" + identifier, placeholderID: "msg_pk_" + identifier,
		items: make(map[int]*earlySpaceItem),
	}
}

func (s *earlySSESpaceState) canInject() bool {
	return s != nil && s.due && !s.active && !s.bypass && s.err == nil &&
		(s.protocol == "chat" || s.responseID != "")
}

// EarlySSESpaceOutputCommitted also covers a partial placeholder write. Callers
// must not retry a different upstream attempt after any of this output escaped.
func EarlySSESpaceOutputCommitted(c *gin.Context) bool {
	w := earlySSEWriterFromContext(c)
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.space != nil && w.space.written
}

// PrepareEarlySSESpaceRetry makes the retry decision atomic with the timer.
// Before visible output it cancels this request's placeholder permanently, so
// an earlier attempt's response ID cannot leak into a subsequent attempt.
// Existing comment/ping modes are deliberately untouched.
func PrepareEarlySSESpaceRetry(c *gin.Context) bool {
	w := earlySSEWriterFromContext(c)
	if w == nil {
		return true
	}
	return w.prepareSpaceRetry()
}

func (w *earlySSEHeartbeatWriter) prepareSpaceRetry() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.space
	if s == nil {
		return true
	}
	if s.written || s.err != nil {
		return false
	}
	w.stopLocked()
	w.firstDue, w.repeatDue = time.Time{}, time.Time{}
	w.interval = 0
	s.bypass, s.due = true, false
	s.responseID = ""
	s.pending = nil
	return true
}

func earlySpaceSet(object earlySpaceObject, key string, value any) {
	encoded, _ := json.Marshal(value)
	object[key] = encoded
}

func earlySpaceString(object earlySpaceObject, key string) string {
	var value string
	_ = json.Unmarshal(object[key], &value)
	return value
}

// Lifecycle snapshots can be partial. Preserve the source's new fields while
// filling known empty accumulator fields from events already sent downstream.
func earlySpaceMergeSnapshot(cached, source earlySpaceObject) earlySpaceObject {
	merged := earlySpaceObject{}
	for key, value := range cached {
		merged[key] = value
	}
	for key, value := range source {
		merged[key] = value
	}
	for _, key := range []string{"text", "refusal", "arguments", "input"} {
		if earlySpaceString(source, key) == "" && earlySpaceString(cached, key) != "" {
			merged[key] = cached[key]
		}
	}
	for _, key := range []string{"content", "summary", "annotations"} {
		var before, after []json.RawMessage
		if json.Unmarshal(cached[key], &before) != nil || len(before) == 0 {
			continue
		}
		if raw, exists := source[key]; exists && string(raw) != "null" && json.Unmarshal(raw, &after) != nil {
			continue
		}
		for index, raw := range before {
			if index >= len(after) {
				after = append(after, raw)
				continue
			}
			var cachedPart, sourcePart earlySpaceObject
			if json.Unmarshal(raw, &cachedPart) == nil && json.Unmarshal(after[index], &sourcePart) == nil {
				after[index], _ = json.Marshal(earlySpaceMergeSnapshot(cachedPart, sourcePart))
			}
		}
		earlySpaceSet(merged, key, after)
	}
	return merged
}

func earlySpaceArrayPart(object earlySpaceObject, field string, index int, source earlySpaceObject) error {
	var parts []json.RawMessage
	_ = json.Unmarshal(object[field], &parts)
	if index > len(parts) {
		return errors.New("space mode: sparse Responses " + field + " snapshot")
	}
	if index == len(parts) {
		parts = append(parts, nil)
	}
	var cached earlySpaceObject
	_ = json.Unmarshal(parts[index], &cached)
	parts[index], _ = json.Marshal(earlySpaceMergeSnapshot(cached, source))
	earlySpaceSet(object, field, parts)
	return nil
}

func earlySpaceIndex(object earlySpaceObject, key string) (int, bool) {
	raw, exists := object[key]
	if !exists {
		return 0, false
	}
	var index int
	if json.Unmarshal(raw, &index) != nil || index < 0 || index == int(^uint(0)>>1) {
		return 0, false
	}
	return index, true
}

func (s *earlySSESpaceState) placeholderPart() earlySpaceObject {
	return earlySpaceObject{
		"type": json.RawMessage(`"output_text"`), "text": json.RawMessage(`" "`),
		"annotations": json.RawMessage(`[]`), "logprobs": json.RawMessage(`[]`),
	}
}

func (s *earlySSESpaceState) placeholderItem() earlySpaceObject {
	item := earlySpaceObject{
		"type": json.RawMessage(`"message"`), "role": json.RawMessage(`"assistant"`),
		"status": json.RawMessage(`"completed"`), "sub2api_placeholder": json.RawMessage(`true`),
	}
	earlySpaceSet(item, "id", s.placeholderID)
	earlySpaceSet(item, "content", []earlySpaceObject{s.placeholderPart()})
	return item
}

func (s *earlySSESpaceState) responseFrame(event earlySpaceObject) ([]byte, error) {
	earlySpaceSet(event, "sequence_number", s.nextSequence)
	s.nextSequence++
	if _, exists := event["output_index"]; exists {
		index, valid := earlySpaceIndex(event, "output_index")
		if !valid {
			return nil, errors.New("space mode: invalid Responses output_index")
		}
		earlySpaceSet(event, "output_index", index+1)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	return []byte("event: " + earlySpaceString(event, "type") + "\ndata: " + string(payload) + "\n\n"), nil
}

func (w *earlySSEHeartbeatWriter) prepareSpaceHeadersLocked() {
	if w.ResponseWriter.Written() {
		return
	}
	w.copyHeadersLocked(w.stableHeaders)
	h := w.ResponseWriter.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	h.Del("Content-Length")
	w.ResponseWriter.WriteHeader(http.StatusOK)
}

func (w *earlySSEHeartbeatWriter) writeSpaceBytesLocked(data []byte) error {
	if !w.ResponseWriter.Written() {
		w.copyHeadersLocked(w.headers)
		w.ResponseWriter.WriteHeader(w.status)
	}
	n, err := w.ResponseWriter.Write(data)
	if n > 0 {
		w.timing.record()
		if w.space.active {
			w.space.written = true
		}
	}
	if err == nil && n < len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.space.err = err
		w.stopLocked()
	}
	return err
}

func (w *earlySSEHeartbeatWriter) emitSpaceLocked() error {
	s := w.space
	if !s.canInject() || w.stopped {
		return nil
	}
	w.prepareSpaceHeadersLocked()
	if !strings.Contains(w.ResponseWriter.Header().Get("Content-Type"), "text/event-stream") {
		s.bypass = true
		w.stopLocked()
		return nil
	}
	var frames bytes.Buffer
	if s.protocol == "chat" {
		// The third frame pushes the space through gateways which retain the
		// previous Chat chunk to inspect its usage before forwarding it.
		for _, delta := range []any{map[string]any{"role": "assistant"}, map[string]any{"content": " "}, map[string]any{}} {
			chunk := map[string]any{
				"id": s.chatID, "object": "chat.completion.chunk", "created": s.created, "model": s.model,
				"choices":             []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}},
				"sub2api_placeholder": true,
			}
			payload, _ := json.Marshal(chunk)
			fmt.Fprintf(&frames, "data: %s\n\n", payload)
		}
	} else {
		item := s.placeholderItem()
		earlySpaceSet(item, "status", "in_progress")
		earlySpaceSet(item, "content", []any{})
		part := s.placeholderPart()
		earlySpaceSet(part, "text", "")
		for _, spec := range []struct {
			typeName string
			extra    map[string]any
		}{
			{"response.output_item.added", map[string]any{"item": item}},
			{"response.content_part.added", map[string]any{"content_index": 0, "part": part}},
			{"response.output_text.delta", map[string]any{"content_index": 0, "delta": " ", "logprobs": []any{}}},
			{"response.output_text.done", map[string]any{"content_index": 0, "text": " ", "logprobs": []any{}}},
			{"response.content_part.done", map[string]any{"content_index": 0, "part": s.placeholderPart()}},
			{"response.output_item.done", map[string]any{"item": s.placeholderItem()}},
		} {
			event := map[string]any{"type": spec.typeName, "sequence_number": s.nextSequence, "output_index": 0, "item_id": s.placeholderID, "sub2api_placeholder": true}
			s.nextSequence++
			for key, value := range spec.extra {
				event[key] = value
			}
			payload, _ := json.Marshal(event)
			fmt.Fprintf(&frames, "event: %s\ndata: %s\n\n", spec.typeName, payload)
		}
	}
	s.active = true
	w.committed = true
	w.firstDue = time.Time{}
	// A visible space commits this attempt. Unlike comment/ping bytes it must
	// not be removed from the size used by pre-output failover checks.
	w.stopLocked()
	if err := w.writeSpaceBytesLocked(frames.Bytes()); err != nil {
		return err
	}
	w.ResponseWriter.Flush()
	return nil
}

// A frame is bounded independently of Write calls. Recognize LF, CRLF and CR
// without exposing a partial event to the timer or splitting a CRLF pair.
func (s *earlySSESpaceState) frameEnd() int {
	data := s.pending
	for i := s.frameScan; i < len(data); i++ {
		if data[i] != '\n' && data[i] != '\r' {
			continue
		}
		end := i + 1
		if data[i] == '\r' {
			if end == len(data) {
				s.frameScan = i
				return 0
			}
			if data[end] == '\n' {
				end++
			}
		}
		if i == s.lineStart {
			s.frameScan, s.lineStart = 0, 0
			return end
		}
		s.lineStart = end
		i = end - 1
	}
	s.frameScan = len(data)
	return 0
}

type earlySpaceFrame struct {
	raw       []byte
	lines     []string
	data      string
	eventType string
	hasData   bool
}

func parseEarlySpaceFrame(raw []byte) earlySpaceFrame {
	normalized := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
	frame := earlySpaceFrame{raw: raw, lines: strings.Split(strings.TrimSuffix(normalized, "\n\n"), "\n")}
	var data []string
	for _, line := range frame.lines {
		field, value, found := strings.Cut(line, ":")
		if !found {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			frame.hasData = true
			data = append(data, value)
		case "event":
			frame.eventType = value
		}
	}
	frame.data = strings.Join(data, "\n")
	return frame
}

func (f earlySpaceFrame) replaceData(payload []byte) []byte {
	var out strings.Builder
	written := false
	for _, line := range f.lines {
		field, _, _ := strings.Cut(line, ":")
		if field == "data" {
			if !written {
				_, _ = out.WriteString("data: ")
				_, _ = out.Write(payload)
				_ = out.WriteByte('\n')
				written = true
			}
			continue
		}
		_, _ = out.WriteString(line)
		_ = out.WriteByte('\n')
	}
	_ = out.WriteByte('\n')
	return []byte(out.String())
}

func (w *earlySSEHeartbeatWriter) writeSpaceLocked(data []byte) (int, error) {
	s := w.space
	if w.terminal {
		return len(data), nil
	}
	if s.err != nil {
		return 0, s.err
	}
	if s.bypass {
		return w.writeOrdinaryLocked(data)
	}
	if w.status >= 400 || (strings.Contains(w.headers.Get("Content-Type"), "application/json") && json.Valid(data)) {
		s.pending = nil
		return w.writeOrdinaryLocked(data)
	}
	s.pending = append(s.pending, data...)
	// This opt-in transformer never buffers an unbounded unterminated frame.
	const maxSpaceFrameBytes = 16 * 1024 * 1024
	for len(s.pending) > 0 {
		end := s.frameEnd()
		if end > maxSpaceFrameBytes {
			return 0, w.failSpaceLocked(errors.New("space mode: SSE frame exceeds 16 MiB"))
		}
		if end == 0 {
			if len(s.pending) > maxSpaceFrameBytes {
				return 0, w.failSpaceLocked(errors.New("space mode: SSE frame exceeds 16 MiB"))
			}
			break
		}
		frame := s.pending[:end]
		s.pending = s.pending[end:]
		if err := w.processSpaceFrameLocked(parseEarlySpaceFrame(frame)); err != nil {
			return 0, w.failSpaceLocked(err)
		}
		if s.bypass && len(s.pending) > 0 {
			remainder := append([]byte(nil), s.pending...)
			s.pending = nil
			if _, err := w.writeOrdinaryLocked(remainder); err != nil {
				return 0, err
			}
		}
	}
	if len(s.pending) == 0 {
		s.pending = nil
		if s.canInject() {
			if err := w.emitSpaceLocked(); err != nil {
				return 0, err
			}
		}
	}
	return len(data), nil
}

func (w *earlySSEHeartbeatWriter) processSpaceFrameLocked(frame earlySpaceFrame) error {
	s := w.space
	if !frame.hasData || strings.TrimSpace(frame.data) == "" {
		// Keep complete comments and SSE fields, and do not consume the space.
		w.committed = true
		return w.writeSpaceBytesLocked(frame.raw)
	}
	if strings.TrimSpace(frame.data) == "[DONE]" {
		w.stopLocked()
		s.bypass = !s.active
		return w.writeSpaceBytesLocked(frame.raw)
	}
	var object earlySpaceObject
	if json.Unmarshal([]byte(frame.data), &object) != nil || object == nil {
		if !s.active {
			s.bypass = true
			_, err := w.writeOrdinaryLocked(frame.raw)
			return err
		}
		return errors.New("space mode: malformed JSON in SSE data")
	}
	kind := earlySpaceString(object, "type")
	if kind == "" {
		kind = frame.eventType
	}
	if s.protocol == "chat" {
		if !s.active {
			if kind == "ping" {
				w.committed = true
				return w.writeSpaceBytesLocked(frame.raw)
			}
			s.bypass = true
			_, err := w.writeOrdinaryLocked(frame.raw)
			return err
		}
		if _, exists := object["choices"]; exists {
			earlySpaceSet(object, "id", s.chatID)
			earlySpaceSet(object, "created", s.created)
		}
		payload, err := json.Marshal(object)
		if err != nil {
			return err
		}
		return w.writeSpaceBytesLocked(frame.replaceData(payload))
	}
	if !s.active {
		metadata := kind == "response.created" || kind == "response.in_progress" || kind == "response.queued"
		if !metadata {
			if kind == "ping" || kind == "keepalive" {
				w.committed = true
				return w.writeSpaceBytesLocked(frame.raw)
			}
			s.bypass = true
			_, err := w.writeOrdinaryLocked(frame.raw)
			return err
		}
		var response earlySpaceObject
		_ = json.Unmarshal(object["response"], &response)
		if output := gjson.GetBytes(object["response"], "output"); output.IsArray() && len(output.Array()) > 0 {
			s.bypass = true
			_, err := w.writeOrdinaryLocked(frame.raw)
			return err
		}
		if earlySpaceStructuredFormat(gjson.GetBytes(object["response"], "text.format.type").String()) {
			s.bypass = true
			w.mode = StreamEarlyHeartbeatModeComment
			_, err := w.writeOrdinaryLocked(frame.raw)
			return err
		}
		if kind == "response.created" {
			s.responseID = earlySpaceString(response, "id")
		}
		if sequence, err := strconv.ParseInt(string(object["sequence_number"]), 10, 64); err == nil && sequence >= s.nextSequence {
			s.nextSequence = sequence + 1
		}
		w.committed = true
		if err := w.writeSpaceBytesLocked(frame.raw); err != nil {
			return err
		}
		if s.canInject() && len(s.pending) == 0 {
			return w.emitSpaceLocked()
		}
		return nil
	}
	if kind == "ping" || kind == "keepalive" {
		return w.writeSpaceBytesLocked(frame.raw)
	}
	if err := s.prepareResponseEvent(object, kind, func(event earlySpaceObject) error {
		frame, err := s.responseFrame(event)
		if err != nil {
			return err
		}
		return w.writeSpaceBytesLocked(frame)
	}); err != nil {
		return err
	}
	// Preserve SSE id/retry/comment fields and unknown JSON fields on source events.
	if _, exists := object["output_index"]; exists {
		index, valid := earlySpaceIndex(object, "output_index")
		if !valid {
			return errors.New("space mode: invalid Responses output_index")
		}
		earlySpaceSet(object, "output_index", index+1)
	}
	earlySpaceSet(object, "sequence_number", s.nextSequence)
	s.nextSequence++
	payload, err := json.Marshal(object)
	if err != nil {
		return err
	}
	return w.writeSpaceBytesLocked(frame.replaceData(payload))
}

func (s *earlySSESpaceState) item(index int) *earlySpaceItem {
	item := s.items[index]
	if item == nil {
		item = &earlySpaceItem{parts: make(map[int]*earlySpacePart)}
		s.items[index] = item
	}
	return item
}

func (i *earlySpaceItem) part(index int) *earlySpacePart {
	part := i.parts[index]
	if part == nil {
		part = &earlySpacePart{}
		i.parts[index] = part
	}
	return part
}

func (s *earlySSESpaceState) mergeOutputSnapshot(output []json.RawMessage, terminal bool) ([]json.RawMessage, error) {
	// SDK accumulators replace their entire response on lifecycle events. Fill
	// absent output from streamed events and preserve each source's newer fields.
	for index, item := range s.items {
		if !item.added || item.snapshot == nil {
			continue
		}
		if terminal && index < len(output) && len(output[index]) > 0 && string(output[index]) != "null" {
			continue
		}
		if index > len(s.items)+len(output) {
			return nil, errors.New("space mode: sparse Responses output snapshot")
		}
		for len(output) <= index {
			output = append(output, json.RawMessage(`null`))
		}
		current := earlySpaceMergeSnapshot(nil, item.snapshot)
		if earlySpaceString(current, "type") == "message" {
			var parts []json.RawMessage
			_ = json.Unmarshal(current["content"], &parts)
			for partIndex, part := range item.parts {
				if partIndex > len(item.parts)+len(parts) {
					return nil, errors.New("space mode: sparse Responses content snapshot")
				}
				for len(parts) <= partIndex {
					parts = append(parts, json.RawMessage(`null`))
				}
				var cachedPart earlySpaceObject
				_ = json.Unmarshal(parts[partIndex], &cachedPart)
				parts[partIndex], _ = json.Marshal(earlySpaceMergeSnapshot(cachedPart, part.snapshot))
			}
			earlySpaceSet(current, "content", parts)
		}
		var source earlySpaceObject
		_ = json.Unmarshal(output[index], &source)
		current = earlySpaceMergeSnapshot(current, source)
		output[index], _ = json.Marshal(current)
		if !terminal {
			// The next delta applies to the snapshot delivered in this frame.
			item.snapshot = current
			var parts []earlySpaceObject
			_ = json.Unmarshal(current["content"], &parts)
			for partIndex, part := range item.parts {
				if partIndex < len(parts) {
					part.snapshot = parts[partIndex]
				}
			}
		}
	}
	return output, nil
}

func (s *earlySSESpaceState) prepareResponseEvent(object earlySpaceObject, kind string, emit func(earlySpaceObject) error) error {
	index, hasIndex := earlySpaceIndex(object, "output_index")
	contentIndex, _ := earlySpaceIndex(object, "content_index")
	if hasIndex {
		item := s.item(index)
		switch kind {
		case "response.output_item.added":
			item.added = true
			_ = json.Unmarshal(object["item"], &item.snapshot)
		case "response.content_part.added":
			item.part(contentIndex).added = true
			_ = json.Unmarshal(object["part"], &item.part(contentIndex).snapshot)
		case "response.output_text.delta":
			part := item.part(contentIndex)
			delta := earlySpaceString(object, "delta")
			_, _ = part.text.WriteString(delta)
			if part.snapshot == nil {
				part.snapshot = earlySpaceObject{"type": json.RawMessage(`"output_text"`)}
			}
			earlySpaceSet(part.snapshot, "text", earlySpaceString(part.snapshot, "text")+delta)
		case "response.refusal.delta", "response.refusal.done":
			part := item.part(contentIndex)
			if part.snapshot == nil {
				part.snapshot = earlySpaceObject{"type": json.RawMessage(`"refusal"`)}
			}
			if kind == "response.refusal.delta" {
				earlySpaceSet(part.snapshot, "refusal", earlySpaceString(part.snapshot, "refusal")+earlySpaceString(object, "delta"))
			} else {
				part.snapshot["refusal"] = object["refusal"]
			}
		case "response.output_text.annotation.added":
			part := item.part(contentIndex)
			if part.snapshot == nil {
				part.snapshot = earlySpaceObject{"type": json.RawMessage(`"output_text"`)}
			}
			annotationIndex, valid := earlySpaceIndex(object, "annotation_index")
			var annotation earlySpaceObject
			if !valid || json.Unmarshal(object["annotation"], &annotation) != nil || annotation == nil {
				return errors.New("space mode: malformed Responses annotation")
			}
			if err := earlySpaceArrayPart(part.snapshot, "annotations", annotationIndex, annotation); err != nil {
				return err
			}
		case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done":
			summaryIndex, valid := earlySpaceIndex(object, "summary_index")
			if !valid || item.snapshot == nil {
				return errors.New("space mode: malformed Responses reasoning summary")
			}
			var part earlySpaceObject
			if kind == "response.reasoning_summary_part.added" || kind == "response.reasoning_summary_part.done" {
				if json.Unmarshal(object["part"], &part) != nil || part == nil {
					return errors.New("space mode: malformed Responses reasoning part")
				}
			} else {
				part = earlySpaceObject{"type": json.RawMessage(`"summary_text"`)}
				if kind == "response.reasoning_summary_text.done" {
					part["text"] = object["text"]
				} else {
					var parts []earlySpaceObject
					_ = json.Unmarshal(item.snapshot["summary"], &parts)
					if summaryIndex >= len(parts) {
						return errors.New("space mode: reasoning delta preceded its summary part")
					}
					earlySpaceSet(part, "text", earlySpaceString(parts[summaryIndex], "text")+earlySpaceString(object, "delta"))
				}
			}
			if err := earlySpaceArrayPart(item.snapshot, "summary", summaryIndex, part); err != nil {
				return err
			}
		case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
			key := "arguments"
			if kind == "response.custom_tool_call_input.delta" {
				key = "input"
			}
			if item.snapshot != nil {
				earlySpaceSet(item.snapshot, key, earlySpaceString(item.snapshot, key)+earlySpaceString(object, "delta"))
			}
		case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
			key := "arguments"
			if kind == "response.custom_tool_call_input.done" {
				key = "input"
			}
			if item.snapshot != nil {
				item.snapshot[key] = object[key]
			}
		case "response.output_text.done", "response.content_part.done":
			var part earlySpaceObject
			if kind == "response.content_part.done" {
				_ = json.Unmarshal(object["part"], &part)
			} else {
				part = earlySpaceObject{"type": json.RawMessage(`"output_text"`), "text": object["text"]}
			}
			if part != nil && earlySpaceString(part, "type") == "output_text" {
				message := earlySpaceObject{"type": json.RawMessage(`"message"`), "role": json.RawMessage(`"assistant"`)}
				for key, value := range item.snapshot {
					message[key] = value
				}
				if earlySpaceString(message, "id") == "" {
					earlySpaceSet(message, "id", earlySpaceString(object, "item_id"))
				}
				earlySpaceSet(message, "content", []earlySpaceObject{part})
				if err := s.supplementMessage(index, message, false, kind, contentIndex, emit); err != nil {
					return err
				}
			}
			if kind == "response.output_text.done" {
				item.part(contentIndex).textDone = true
				item.part(contentIndex).snapshot = earlySpaceMergeSnapshot(item.part(contentIndex).snapshot, part)
			} else {
				item.part(contentIndex).done = true
				item.part(contentIndex).snapshot = part
			}
		case "response.output_item.done":
			var output earlySpaceObject
			if json.Unmarshal(object["item"], &output) == nil {
				if err := s.supplementMessage(index, output, false, "", 0, emit); err != nil {
					return err
				}
				item.snapshot = output
			}
			item.done = true
		}
	}
	if rawResponse, exists := object["response"]; exists {
		var response earlySpaceObject
		if json.Unmarshal(rawResponse, &response) != nil || response == nil {
			return errors.New("space mode: malformed Responses response object")
		}
		if id := earlySpaceString(response, "id"); id != "" && id != s.responseID {
			return errors.New("space mode: upstream response ID changed after output")
		}
		var output []json.RawMessage
		if raw, exists := response["output"]; exists && string(raw) != "null" {
			if err := json.Unmarshal(raw, &output); err != nil {
				return errors.New("space mode: malformed Responses output snapshot")
			}
		}
		terminal := kind == "response.completed" || kind == "response.done" || kind == "response.incomplete" || kind == "response.failed" || kind == "response.cancelled" || kind == "response.canceled"
		if terminal {
			for index, raw := range output {
				var item earlySpaceObject
				if json.Unmarshal(raw, &item) == nil {
					if err := s.supplementMessage(index, item, true, "", 0, emit); err != nil {
						return err
					}
				}
			}
		}
		var err error
		output, err = s.mergeOutputSnapshot(output, terminal)
		if err != nil {
			return err
		}
		placeholder, _ := json.Marshal(s.placeholderItem())
		earlySpaceSet(response, "output", append([]json.RawMessage{placeholder}, output...))
		earlySpaceSet(object, "response", response)
	}
	return nil
}

// An early space makes some downstream converters believe text has already
// streamed. Materialize terminal-only real text per item/part before the terminal
// so those converters cannot silently replace the answer with the placeholder.
func (s *earlySSESpaceState) supplementMessage(index int, output earlySpaceObject, closeItem bool, stopBefore string, contentOffset int, emit func(earlySpaceObject) error) error {
	item := s.item(index)
	if item.done {
		return nil
	}
	identifier := earlySpaceString(output, "id")
	if identifier == "" {
		return errors.New("space mode: terminal message has no item ID")
	}
	send := func(kind string, partIndex int, extra map[string]any) error {
		event := earlySpaceObject{}
		earlySpaceSet(event, "type", kind)
		earlySpaceSet(event, "output_index", index)
		earlySpaceSet(event, "item_id", identifier)
		if partIndex >= 0 {
			earlySpaceSet(event, "content_index", partIndex)
		}
		for key, value := range extra {
			earlySpaceSet(event, key, value)
		}
		return emit(event)
	}
	if earlySpaceString(output, "type") != "message" {
		if !item.added {
			if err := send("response.output_item.added", -1, map[string]any{"item": output}); err != nil {
				return err
			}
			item.added = true
			item.snapshot = output
		}
		if closeItem {
			if err := send("response.output_item.done", -1, map[string]any{"item": output}); err != nil {
				return err
			}
			item.done = true
			item.snapshot = output
		}
		return nil
	}
	var content []earlySpaceObject
	if raw, exists := output["content"]; exists && string(raw) != "null" {
		if json.Unmarshal(raw, &content) != nil {
			return errors.New("space mode: malformed message content")
		}
	}
	if !item.added {
		initial := earlySpaceObject{}
		for key, value := range output {
			initial[key] = value
		}
		earlySpaceSet(initial, "status", "in_progress")
		earlySpaceSet(initial, "content", []any{})
		if err := send("response.output_item.added", -1, map[string]any{"item": initial}); err != nil {
			return err
		}
		item.added = true
		item.snapshot = initial
	}
	for ordinal, finalPart := range content {
		partIndex := ordinal + contentOffset
		text := earlySpaceString(finalPart, "text")
		part := item.part(partIndex)
		seen := part.text.String()
		if part.done {
			continue
		}
		isText := earlySpaceString(finalPart, "type") == "output_text"
		if !part.added {
			initial := earlySpaceObject{}
			for key, value := range finalPart {
				initial[key] = value
			}
			if isText {
				earlySpaceSet(initial, "text", "")
			}
			if err := send("response.content_part.added", partIndex, map[string]any{"part": initial}); err != nil {
				return err
			}
			part.added = true
			part.snapshot = initial
		}
		if isText && strings.HasPrefix(text, seen) {
			if missing := strings.TrimPrefix(text, seen); missing != "" {
				if err := send("response.output_text.delta", partIndex, map[string]any{"delta": missing}); err != nil {
					return err
				}
				_, _ = part.text.WriteString(missing)
			}
		}
		if stopBefore == "response.output_text.done" {
			continue
		}
		if isText && !part.textDone {
			if err := send("response.output_text.done", partIndex, map[string]any{"text": text}); err != nil {
				return err
			}
			part.textDone = true
		}
		if stopBefore == "response.content_part.done" {
			continue
		}
		if err := send("response.content_part.done", partIndex, map[string]any{"part": finalPart}); err != nil {
			return err
		}
		part.done = true
		part.snapshot = finalPart
	}
	if closeItem && item.added {
		if err := send("response.output_item.done", -1, map[string]any{"item": output}); err != nil {
			return err
		}
		item.done = true
		item.snapshot = output
	}
	return nil
}

func (w *earlySSEHeartbeatWriter) failSpaceLocked(err error) error {
	s := w.space
	w.stopLocked()
	if s.err != nil {
		return s.err
	}
	s.pending = nil
	if s.active && !w.terminal {
		payload, _ := json.Marshal(map[string]any{"error": map[string]any{"type": "upstream_error", "message": err.Error()}})
		frame, _, _, _ := w.errorFrame(payload)
		w.terminal = true
		MarkOpsStreamError(w.c, "upstream_error", err.Error(), http.StatusBadGateway)
		MarkResponseCommitted(w.c)
		if writeErr := w.writeSpaceBytesLocked(frame); writeErr != nil {
			return writeErr
		}
		w.ResponseWriter.Flush()
	}
	s.err = err
	return err
}

func (w *earlySSEHeartbeatWriter) finishSpaceLocked() {
	s := w.space
	if s == nil || len(s.pending) == 0 || s.err != nil || w.terminal {
		return
	}
	pending := append([]byte(nil), s.pending...)
	s.pending = nil
	// At EOF a trailing CR is a complete line ending, not a possible prefix
	// of CRLF. Revisit that boundary before declaring the event truncated.
	if bytes.HasSuffix(pending, []byte("\r")) {
		s.pending = append(pending, '\n')
		if end := s.frameEnd(); end == len(s.pending) {
			frame := s.pending[:end]
			s.pending = nil
			if err := w.processSpaceFrameLocked(parseEarlySpaceFrame(frame)); err != nil {
				_ = w.failSpaceLocked(err)
			}
			return
		}
		s.pending = nil
	}
	if !s.active {
		s.bypass = true
		_, _ = w.writeOrdinaryLocked(pending)
		return
	}
	// Treat EOF without an event boundary as an incomplete stream; never leak
	// an unrewritten tail after indices/IDs have changed.
	_ = w.failSpaceLocked(errors.New("space mode: incomplete SSE event at end of stream"))
}

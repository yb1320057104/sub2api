package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const keepaliveTestInterval = 10 * time.Millisecond

// waitForKeepaliveBeats 等待至少一次心跳写出。读取 recorder 前必须先经
// StopOpenAICompactSSEKeepaliveCommitted 停拍建立 happens-before。
func waitForKeepaliveBeats() {
	time.Sleep(20 * keepaliveTestInterval)
}

// stripKeepaliveComments 去掉 SSE 注释块，返回真实事件文本。
func stripKeepaliveComments(body string) string {
	var blocks []string
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		if strings.HasPrefix(strings.TrimSpace(block), ":") {
			continue
		}
		blocks = append(blocks, block)
	}
	return strings.Join(blocks, "\n\n")
}

func TestStartOpenAICompactSSEKeepalive_NoopWhenUnmarkedOrDisabled(t *testing.T) {
	// 未标记 client stream：不启动。
	c, rec := newCompactBridgeTestContext(t, false)
	stop := StartOpenAICompactSSEKeepalive(c, keepaliveTestInterval)
	waitForKeepaliveBeats()
	stop()
	require.Zero(t, rec.Body.Len())
	require.False(t, StopOpenAICompactSSEKeepaliveCommitted(c))

	// interval=0（配置禁用）：不启动。
	c, rec = newCompactBridgeTestContext(t, true)
	stop = StartOpenAICompactSSEKeepalive(c, 0)
	waitForKeepaliveBeats()
	stop()
	require.Zero(t, rec.Body.Len())
	require.False(t, StopOpenAICompactSSEKeepaliveCommitted(c))
}

func TestOpenAICompactSSEKeepalive_CommitsHeadersAndComments(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	stop := StartOpenAICompactSSEKeepalive(c, keepaliveTestInterval)
	defer stop()
	waitForKeepaliveBeats()

	require.True(t, StopOpenAICompactSSEKeepaliveCommitted(c))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	require.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"))
	require.Contains(t, rec.Body.String(), ": keepalive\n\n")
}

func TestOpenAICompactSSEKeepalive_StopBeforeFirstBeatKeepsWriterUntouched(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	stop := StartOpenAICompactSSEKeepalive(c, time.Hour)
	stop()
	waitForKeepaliveBeats()
	require.Zero(t, rec.Body.Len())
	require.False(t, StopOpenAICompactSSEKeepaliveCommitted(c))
}

func TestOpenAIAdjustedWrittenSizeExcludesResponsesStreamKeepalive(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	n, err := c.Writer.Write([]byte(":\n\n"))
	require.NoError(t, err)
	recordOpenAIStreamKeepaliveBytes(c, n)

	require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))

	_, err = c.Writer.Write([]byte("data: semantic\n\n"))
	require.NoError(t, err)
	require.Equal(t, len("data: semantic\n\n"), OpenAICompactKeepaliveAdjustedWrittenSize(c))
	require.Equal(t, ":\n\ndata: semantic\n\n", rec.Body.String())
}

// 心跳已提交后，2xx 桥接续写事件而不重复提交响应头。
func TestWriteOpenAICompactSSEBridge_AfterKeepaliveCommitAppendsEvents(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	stop := StartOpenAICompactSSEKeepalive(c, keepaliveTestInterval)
	defer stop()
	waitForKeepaliveBeats()

	finalResponse := []byte(`{"id":"resp_ka_1","output":[{"id":"cmp_ka","type":"compaction","encrypted_content":"x"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	require.True(t, writeOpenAICompactSSEBridge(c, http.StatusOK, finalResponse))

	require.Equal(t, http.StatusOK, rec.Code)
	events := parseCompactBridgeSSE(t, stripKeepaliveComments(rec.Body.String()))
	require.Len(t, events, 2)
	require.Equal(t, "response.output_item.done", events[0][0])
	require.Equal(t, "compaction", gjson.Get(events[0][1], "item.type").String())
	require.Equal(t, "response.completed", events[1][0])
	require.Equal(t, "resp_ka_1", gjson.Get(events[1][1], "response.id").String())
}

// 心跳已提交后上游非 2xx：状态码无法回传，必须以 response.failed 终止事件
// 收尾（Codex 将其作为终止事件处理），并标记流内错误供 ops 采集。
func TestWriteOpenAICompactSSEBridge_AfterKeepaliveCommitFailureEmitsFailedEvent(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	stop := StartOpenAICompactSSEKeepalive(c, keepaliveTestInterval)
	defer stop()
	waitForKeepaliveBeats()

	require.True(t, writeOpenAICompactSSEBridge(c, http.StatusBadGateway, []byte(`{"error":{"message":"upstream exploded"}}`)))

	events := parseCompactBridgeSSE(t, stripKeepaliveComments(rec.Body.String()))
	require.Len(t, events, 1)
	require.Equal(t, "response.failed", events[0][0])
	require.Equal(t, "failed", gjson.Get(events[0][1], "response.status").String())
	require.Contains(t, gjson.Get(events[0][1], "response.error.message").String(), "upstream exploded")
	require.NotEmpty(t, gjson.Get(events[0][1], "response.id").String())

	streamErr, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, http.StatusBadGateway, streamErr.IntendedStatus)
}

// 心跳未提交时非 2xx 行为不变：返回 false，调用方按原 JSON+状态码写回。
func TestWriteOpenAICompactSSEBridge_BeforeKeepaliveCommitFailureKeepsJSONPath(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	stop := StartOpenAICompactSSEKeepalive(c, time.Hour)
	stop()

	require.False(t, writeOpenAICompactSSEBridge(c, http.StatusBadGateway, []byte(`{"error":{"message":"fast fail"}}`)))
	require.Zero(t, rec.Body.Len())
}

// 未被显式拦截的写回路径（直接操作 c.Writer）也必须与心跳互斥：包装器在
// 请求侧任何响应构造时停拍。-race 下验证无数据竞争，且停拍后不再有心跳
// 字节写出。
func TestOpenAICompactKeepaliveWriter_RequestSideWriteSuspendsBeats(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	stop := StartOpenAICompactSSEKeepalive(c, keepaliveTestInterval)
	defer stop()
	waitForKeepaliveBeats()

	// 模拟未拦截路径的直接写回（如 Forward 内部本地拒绝的 c.JSON）。
	_, err := c.Writer.Write([]byte(`{"error":"local reject"}`))
	require.NoError(t, err)

	lenAfterWrite := rec.Body.Len()
	waitForKeepaliveBeats()
	require.Equal(t, lenAfterWrite, rec.Body.Len(), "请求侧写回后心跳必须停止")
	require.Contains(t, rec.Body.String(), ": keepalive\n\n")
	require.Contains(t, rec.Body.String(), `{"error":"local reject"}`)
}

func TestOpenAICompactKeepaliveWriter_NilInnerWriter_NoPanic(t *testing.T) {
	w := &openAICompactKeepaliveWriter{
		k: &openAICompactSSEKeepalive{stop: make(chan struct{})},
	}
	w.ResponseWriter = nil

	assert.NotPanics(t, func() {
		assert.Equal(t, 0, w.Status())
	})
	assert.NotPanics(t, func() {
		assert.Equal(t, 0, w.Size())
	})
	assert.NotPanics(t, func() {
		assert.False(t, w.Written())
	})
	assert.NotPanics(t, func() {
		assert.NotNil(t, w.Header())
	})
	assert.NotPanics(t, func() {
		n, err := w.Write([]byte("test"))
		assert.Equal(t, 0, n)
		assert.NoError(t, err)
	})
	assert.NotPanics(t, func() {
		n, err := w.WriteString("test")
		assert.Equal(t, 0, n)
		assert.NoError(t, err)
	})
	assert.NotPanics(t, func() {
		w.WriteHeader(http.StatusOK)
	})
	assert.NotPanics(t, func() {
		w.WriteHeaderNow()
	})
	assert.NotPanics(t, func() {
		w.Flush()
	})
	assert.NotPanics(t, func() {
		conn, rw, err := w.Hijack()
		assert.Nil(t, conn)
		assert.Nil(t, rw)
		assert.Error(t, err)
	})
	assert.NotPanics(t, func() {
		ch := w.CloseNotify()
		assert.NotNil(t, ch)
	})
	assert.NotPanics(t, func() {
		assert.Nil(t, w.Pusher())
	})
}

func TestOpenAICompactKeepaliveWriter_NilKeepalive_NoPanic(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	w := &openAICompactKeepaliveWriter{ResponseWriter: c.Writer}

	assert.NotPanics(t, func() {
		assert.Equal(t, 0, w.Status())
	})
	assert.NotPanics(t, func() {
		assert.Equal(t, 0, w.Size())
	})
	assert.NotPanics(t, func() {
		assert.False(t, w.Written())
	})
	assert.NotPanics(t, func() {
		w.Header().Set("X-Test", "ok")
	})
	assert.NotPanics(t, func() {
		w.WriteHeader(http.StatusAccepted)
	})
	assert.NotPanics(t, func() {
		n, err := w.WriteString("ok")
		assert.Equal(t, 2, n)
		assert.NoError(t, err)
	})
	assert.NotPanics(t, func() {
		w.Flush()
	})
	require.Equal(t, "ok", rec.Header().Get("X-Test"))
	require.Equal(t, "ok", rec.Body.String())
}

func TestOpenAICompactKeepaliveWriter_DelegatesWhenReady(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	stop := StartOpenAICompactSSEKeepalive(c, time.Hour)
	defer stop()

	w, ok := c.Writer.(*openAICompactKeepaliveWriter)
	require.True(t, ok)

	w.Header().Set("X-Test", "ok")
	w.WriteHeader(http.StatusAccepted)
	n, err := w.WriteString("ready")
	require.NoError(t, err)
	require.Equal(t, len("ready"), n)

	require.Equal(t, http.StatusAccepted, w.Status())
	require.Equal(t, len("ready"), w.Size())
	require.True(t, w.Written())
	require.Equal(t, "ok", rec.Header().Get("X-Test"))
	require.Equal(t, "ready", rec.Body.String())
}

// fast policy block 在心跳提交后必须降级为 response.failed 终止事件。
func TestWriteOpenAIFastPolicyBlockedResponse_AfterKeepaliveCommit(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	stop := StartOpenAICompactSSEKeepalive(c, keepaliveTestInterval)
	defer stop()
	waitForKeepaliveBeats()

	writeOpenAIFastPolicyBlockedResponse(c, &OpenAIFastBlockedError{Message: "tier blocked"})

	require.Equal(t, http.StatusOK, rec.Code)
	events := parseCompactBridgeSSE(t, stripKeepaliveComments(rec.Body.String()))
	require.Len(t, events, 1)
	require.Equal(t, "response.failed", events[0][0])
	require.Equal(t, "permission_error", gjson.Get(events[0][1], "response.error.code").String())
	require.Contains(t, gjson.Get(events[0][1], "response.error.message").String(), "tier blocked")
}

// failover"是否已写响应"判定的口径：心跳字节必须被排除，否则 compact 在
// 上游等待期间发过心跳后，可换号的 failover 会被误判放弃；真实响应字节
// 写出后口径必须变化。
func TestOpenAICompactKeepaliveAdjustedWrittenSize_ExcludesHeartbeatBytes(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	// 无心跳的请求：等价于 c.Writer.Size()。
	require.Equal(t, c.Writer.Size(), OpenAICompactKeepaliveAdjustedWrittenSize(c))

	stop := StartOpenAICompactSSEKeepalive(c, keepaliveTestInterval)
	defer stop()
	before := OpenAICompactKeepaliveAdjustedWrittenSize(c)
	waitForKeepaliveBeats()
	require.Equal(t, before, OpenAICompactKeepaliveAdjustedWrittenSize(c), "仅心跳字节不得改变判定口径")

	// 真实响应字节写出（经包装器，先停拍再写）后口径必须变化。
	_, err := c.Writer.Write([]byte("real-bytes"))
	require.NoError(t, err)
	require.Equal(t, len("real-bytes"), OpenAICompactKeepaliveAdjustedWrittenSize(c))
	require.Contains(t, rec.Body.String(), ": keepalive\n\n")
}

func TestOpenAIStreamClientOutputStarted_IgnoresCompactKeepaliveBytes(t *testing.T) {
	c, _ := newCompactBridgeTestContext(t, true)
	stop := StartOpenAICompactSSEKeepalive(c, keepaliveTestInterval)
	defer stop()
	waitForKeepaliveBeats()

	require.True(t, c.Writer.Written())
	require.False(t, openAIStreamClientOutputStarted(c, false), "keepalive comments are not semantic output")

	_, err := c.Writer.Write([]byte("real-output"))
	require.NoError(t, err)
	require.True(t, openAIStreamClientOutputStarted(c, false))
}

// fast policy block 在心跳未提交时保持 403 JSON 原语义。
func TestWriteOpenAIFastPolicyBlockedResponse_BeforeKeepaliveCommit(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, true)
	stop := StartOpenAICompactSSEKeepalive(c, time.Hour)
	defer stop()

	writeOpenAIFastPolicyBlockedResponse(c, &OpenAIFastBlockedError{Message: "tier blocked"})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "permission_error", gjson.Get(rec.Body.String(), "error.type").String())
}

func firePKEarlyHeartbeat(t *testing.T, w *earlySSEHeartbeatWriter) {
	t.Helper()
	require.NotNil(t, w)
	w.mu.Lock()
	w.firstDue = time.Now()
	w.mu.Unlock()
	w.beat()
}

func TestPKEarlyHeartbeat_HeaderPreparationDoesNotCancelFirstComment(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	c.Writer.Header().Set("X-Request-ID", "gateway-request")
	stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{})
	defer stop()
	w := earlySSEWriterFromContext(c)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("X-Codex-Turn-State", "account-private")
	c.Writer.Header().Set("Content-Length", "100")
	c.Writer.WriteHeader(http.StatusOK)
	firePKEarlyHeartbeat(t, w)

	require.True(t, EarlySSEHeartbeatCommitted(c))
	require.True(t, c.Writer.Written(), "HTTP commit remains distinct from semantic output")
	require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
	require.NotNil(t, FirstResponseMsFromContext(c.Request.Context()))
	require.Equal(t, "text/event-stream", rec.Result().Header.Get("Content-Type"))
	require.Equal(t, "gateway-request", rec.Result().Header.Get("X-Request-ID"))
	require.Empty(t, rec.Result().Header.Get("X-Codex-Turn-State"), "account headers cannot be sent before an account produces output")
	require.Empty(t, rec.Result().Header.Get("Content-Length"))
	require.Equal(t, ": keepalive\n\n", rec.Body.String())

	output := "event: response.output_text.delta\ndata: {\"delta\":\"hello\"}\n\n"
	_, err := c.Writer.WriteString(output)
	require.NoError(t, err)
	require.Equal(t, len(output), OpenAICompactKeepaliveAdjustedWrittenSize(c))
	firePKEarlyHeartbeat(t, w)
	require.Equal(t, ": keepalive\n\n"+output, rec.Body.String(), "real output permanently takes ownership")
}

func TestPKEarlyHeartbeat_FlushedHeadersStillAllowCommentBeforeBody(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	stop := startEarlySSEHeartbeat(c, time.Now(), "chat", StreamEarlyHeartbeatSettings{})
	defer stop()
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.WriteHeaderNow()
	c.Writer.Flush()
	require.Nil(t, FirstResponseMsFromContext(c.Request.Context()), "header-only flush is not a body packet")
	firePKEarlyHeartbeat(t, earlySSEWriterFromContext(c))
	require.Equal(t, ": keepalive\n\n", rec.Body.String())
}

func TestPKEarlyHeartbeat_EnabledTimerUsesRequestStart(t *testing.T) {
	for _, mode := range []string{"", StreamEarlyHeartbeatModeDownstreamPing} {
		t.Run(mode, func(t *testing.T) {
			c, rec := newCompactBridgeTestContext(t, false)
			stop := startEarlySSEHeartbeat(c, time.Now().Add(-50*time.Millisecond), "responses", StreamEarlyHeartbeatSettings{Enabled: true, Mode: mode, MinDelayMS: 20, MaxDelayMS: 20})
			defer stop()
			require.Eventually(t, func() bool { return EarlySSEHeartbeatCommitted(c) }, time.Second, time.Millisecond)
			require.True(t, StopEarlySSEHeartbeatCommitted(c))
			want := ": keepalive\n\n"
			if mode == StreamEarlyHeartbeatModeDownstreamPing {
				want = "data: {\"type\":\"ping\"}\n\n"
			}
			require.Equal(t, want, rec.Body.String())
			first := FirstResponseMsFromContext(c.Request.Context())
			require.NotNil(t, first)
			require.GreaterOrEqual(t, *first, 50)
		})
	}
}

func TestPKEarlyHeartbeat_FastErrorKeepsHTTPStatusAndJSON(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{})
	defer stop()
	c.JSON(http.StatusForbidden, map[string]any{"error": map[string]any{"type": "permission_error", "message": "denied"}})
	firePKEarlyHeartbeat(t, earlySSEWriterFromContext(c))
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.False(t, EarlySSEHeartbeatCommitted(c))
	require.Equal(t, "denied", gjson.Get(rec.Body.String(), "error.message").String())
	require.NotContains(t, rec.Body.String(), "keepalive")
}

func TestPKEarlyHeartbeat_LateJSONErrorUsesProtocolTerminal(t *testing.T) {
	for _, protocol := range []string{"responses", "chat", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			c, rec := newCompactBridgeTestContext(t, false)
			stop := startEarlySSEHeartbeat(c, time.Now(), protocol, StreamEarlyHeartbeatSettings{})
			defer stop()
			firePKEarlyHeartbeat(t, earlySSEWriterFromContext(c))
			c.JSON(http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"type": "overloaded_error", "code": "capacity", "message": "try later"}})
			require.Equal(t, http.StatusOK, rec.Code)
			require.Greater(t, OpenAICompactKeepaliveAdjustedWrittenSize(c), 0)
			events := parseCompactBridgeSSE(t, stripKeepaliveComments(rec.Body.String()))
			require.Len(t, events, 1)
			if protocol == "responses" {
				require.Equal(t, "response.failed", events[0][0])
				require.Equal(t, "capacity", gjson.Get(events[0][1], "response.error.code").String())
				require.Equal(t, "try later", gjson.Get(events[0][1], "response.error.message").String())
				require.NotEmpty(t, gjson.Get(events[0][1], "response.id").String())
				require.Positive(t, gjson.Get(events[0][1], "response.created_at").Int())
				require.True(t, gjson.Get(events[0][1], "sequence_number").Exists())
			} else {
				require.Equal(t, "error", events[0][0])
				require.Equal(t, "try later", gjson.Get(events[0][1], "error.message").String())
				if protocol == "messages" {
					require.Equal(t, "error", gjson.Get(events[0][1], "type").String())
				}
			}
			streamErr, ok := GetOpsStreamError(c)
			require.True(t, ok)
			require.Equal(t, http.StatusServiceUnavailable, streamErr.IntendedStatus)
			require.True(t, IsResponseCommitted(c))
			before := rec.Body.String()
			_, err := c.Writer.WriteString("must not append a second terminal or raw JSON")
			require.NoError(t, err)
			require.Equal(t, before, rec.Body.String())
		})
	}
}

func TestPKEarlyHeartbeat_HeaderMutationDoesNotRaceTimer(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{})
	defer stop()
	w := earlySSEWriterFromContext(c)
	w.mu.Lock()
	w.firstDue = time.Now()
	w.mu.Unlock()
	var beat sync.WaitGroup
	beat.Add(1)
	go func() {
		defer beat.Done()
		w.beat()
	}()
	for i := 0; i < 1000; i++ {
		c.Writer.Header().Set("X-Upstream-ID", "private")
		c.Writer.Header().Del("X-Upstream-ID")
		c.Writer.WriteHeader(http.StatusOK)
		_ = c.Writer.Size()
	}
	beat.Wait()
	require.True(t, StopEarlySSEHeartbeatCommitted(c))
	require.Equal(t, ": keepalive\n\n", rec.Body.String())
}

func TestPKEarlyHeartbeat_ReusesExistingPassthroughKeepalive(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{})
	defer stop()
	w := earlySSEWriterFromContext(c)
	stopPeriodic := startOpenAISSEKeepalive(c, time.Hour)
	require.Same(t, w, c.Writer, "periodic keepalive must not add a second writer or timer")
	w.mu.Lock()
	w.repeatDue = time.Now()
	w.mu.Unlock()
	w.beat()
	require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
	stopPeriodic()
	w.beat()
	require.Equal(t, ": keepalive\n\n", rec.Body.String())
	require.True(t, StopOpenAICompactSSEKeepaliveCommitted(c), "existing error paths must also observe early commit")
}

func TestPKEarlyHeartbeat_AdjustmentCombinesStreamComments(t *testing.T) {
	c, _ := newCompactBridgeTestContext(t, false)
	stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{})
	defer stop()
	firePKEarlyHeartbeat(t, earlySSEWriterFromContext(c))
	queueBytes, queueErr := c.Writer.WriteString(": queue\n\n")
	require.NoError(t, queueErr)
	c.Set(GatewayStreamHeartbeatBytesKey, queueBytes)
	n, err := c.Writer.WriteString(":\n\n")
	require.NoError(t, err)
	recordOpenAIStreamKeepaliveBytes(c, n)
	require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
	_, err = c.Writer.WriteString("data: semantic\n\n")
	require.NoError(t, err)
	require.Equal(t, len("data: semantic\n\n"), OpenAICompactKeepaliveAdjustedWrittenSize(c))
}

func TestPKEarlyHeartbeat_ExternalHeartbeatAllowsLaterPeriodicKeepalive(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, frame := range []string{":\n\n", "data: {\"type\": \"ping\"}\n\n"} {
			for _, streamAccounting := range []bool{false, true} {
				t.Run(fmt.Sprintf("enabled=%t/stream=%t/frame=%q", enabled, streamAccounting, frame), func(t *testing.T) {
					c, rec := newCompactBridgeTestContext(t, false)
					stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{Enabled: enabled, MinDelayMS: 30000, MaxDelayMS: 30000})
					defer stop()
					w := earlySSEWriterFromContext(c)
					c.Writer.Header().Set("Content-Type", "text/event-stream")
					n, err := c.Writer.WriteString(frame)
					require.NoError(t, err)
					c.Writer.Flush()
					if streamAccounting {
						recordOpenAIStreamKeepaliveBytes(c, n)
					} else {
						c.Set(GatewayStreamHeartbeatBytesKey, n)
					}
					w.mu.Lock()
					stopped, firstDue, internalBytes := w.stopped, w.firstDue, w.bytes
					w.mu.Unlock()
					require.False(t, stopped, "external heartbeat must not end the shared controller")
					require.True(t, firstDue.IsZero(), "external heartbeat replaces the pending early first beat")
					require.Zero(t, internalBytes, "external heartbeat must not be double-counted")
					before := OpenAICompactKeepaliveAdjustedWrittenSize(c)
					require.Equal(t, -1, before, "queue and stream comments are both nonsemantic")
					stopPeriodic := startOpenAISSEKeepalive(c, time.Hour)
					defer stopPeriodic()
					require.Same(t, w, c.Writer)
					w.mu.Lock()
					w.repeatDue = time.Now()
					w.mu.Unlock()
					w.beat()
					require.Equal(t, frame+": keepalive\n\n", rec.Body.String(), "periodic keepalive must still work after a queue/stream heartbeat")
					require.Equal(t, before, OpenAICompactKeepaliveAdjustedWrittenSize(c))
					_, err = c.Writer.WriteString("data: semantic\n\n")
					require.NoError(t, err)
					w.mu.Lock()
					stopped = w.stopped
					w.mu.Unlock()
					require.True(t, stopped, "semantic output still permanently takes ownership")
				})
			}
		}
	}
}

func TestPKEarlyHeartbeat_OnlyCompleteTransportFramesPreserveController(t *testing.T) {
	for _, frame := range []string{":\n\n", ": keepalive\r\n\r\n", ": first\n: second\n\n", "data: {\"type\":\"ping\"}\n\n", "event: ping\ndata: {\"type\":\"ping\"}\n\n"} {
		require.True(t, isCompleteSSEHeartbeat([]byte(frame)), frame)
	}
	for _, frame := range []string{"", "\n\n", ": incomplete\n", "event: ping\n\n", ": comment\nevent: ping\n\n", ": comment\n\ndata: {\"delta\":\"hello\"}\n\n", "data: {\"type\":\"ping\",\"content\":\"hello\"}\n\n", "event: response.created\ndata: {}\n\n", "data: [DONE]\n\n"} {
		require.False(t, isCompleteSSEHeartbeat([]byte(frame)), frame)
	}
}

func TestPKEarlyHeartbeat_QueueOnlyLateJSONErrorUsesSSE(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	stop := startEarlySSEHeartbeat(c, time.Now(), "messages", StreamEarlyHeartbeatSettings{})
	defer stop()
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	n, err := c.Writer.WriteString("data: {\"type\": \"ping\"}\n\n")
	require.NoError(t, err)
	c.Set(GatewayStreamHeartbeatBytesKey, n)
	c.Writer.Flush()
	require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
	require.True(t, StopEarlySSEHeartbeatCommitted(c), "queue-only output has committed SSE even without a timer beat")
	c.JSON(http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"type": "api_error", "message": "no account"}})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "event: error\ndata: ")
	require.Contains(t, rec.Body.String(), `"message":"no account"`)
	require.Contains(t, rec.Body.String(), `"type":"error"`)
	require.Greater(t, OpenAICompactKeepaliveAdjustedWrittenSize(c), 0)
	streamErr, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, http.StatusServiceUnavailable, streamErr.IntendedStatus)
}

func TestPKEarlyHeartbeat_QueueAccountingWithoutController(t *testing.T) {
	c, _ := newCompactBridgeTestContext(t, false)
	n, err := c.Writer.WriteString(":\n\n")
	require.NoError(t, err)
	c.Set(GatewayStreamHeartbeatBytesKey, n)
	require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
	_, err = c.Writer.WriteString("data: real\n\n")
	require.NoError(t, err)
	require.Equal(t, len("data: real\n\n"), OpenAICompactKeepaliveAdjustedWrittenSize(c))
}

func TestPKEarlyHeartbeat_CancellationStopsPendingTimer(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	ctx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{Enabled: true, MinDelayMS: 30000, MaxDelayMS: 30000})
	defer stop()
	w := earlySSEWriterFromContext(c)
	cancel()
	select {
	case <-w.done:
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not stop the pending heartbeat")
	}
	firePKEarlyHeartbeat(t, w)
	require.Empty(t, rec.Body.String())
	require.False(t, EarlySSEHeartbeatCommitted(c))
}

func TestPKEarlyHeartbeat_RandomDelayBounds(t *testing.T) {
	settings := StreamEarlyHeartbeatSettings{MinDelayMS: 25, MaxDelayMS: 75}
	for i := 0; i < 1000; i++ {
		delay := earlySSEHeartbeatDelay(settings)
		require.GreaterOrEqual(t, delay, 25*time.Millisecond)
		require.LessOrEqual(t, delay, 75*time.Millisecond)
	}
	require.Equal(t, 42*time.Millisecond, earlySSEHeartbeatDelay(StreamEarlyHeartbeatSettings{MinDelayMS: 42, MaxDelayMS: 42}))
	require.Equal(t, time.Duration(0), earlySSEHeartbeatDelay(StreamEarlyHeartbeatSettings{MinDelayMS: -10, MaxDelayMS: -1}))
}

func TestPKFirstResponse_DisabledHeartbeatStillRecordsBodyAndCopiesContext(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	var service *OpenAIGatewayService
	stop := service.StartEarlySSEHeartbeat(c, time.Now().Add(-20*time.Millisecond), "chat")
	defer stop()
	require.Nil(t, FirstResponseMsFromContext(c.Request.Context()))
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("X-Upstream-ID", "preserved-before-commit")
	_, err := c.Writer.WriteString("data: first\n\n")
	require.NoError(t, err)
	first := FirstResponseMsFromContext(c.Request.Context())
	require.NotNil(t, first)
	require.GreaterOrEqual(t, *first, 20)
	_, err = c.Writer.WriteString("data: second\n\n")
	require.NoError(t, err)
	require.Equal(t, first, FirstResponseMsFromContext(c.Request.Context()))
	require.NotContains(t, rec.Body.String(), "keepalive")
	require.Equal(t, "preserved-before-commit", rec.Result().Header.Get("X-Upstream-ID"))
	baseKey := struct{ name string }{"retained"}
	base := context.WithValue(context.Background(), baseKey, "base value")
	detached := CopyFirstResponseTiming(c.Request.Context(), base)
	require.Equal(t, first, FirstResponseMsFromContext(detached))
	require.Equal(t, "base value", detached.Value(baseKey))
	require.Nil(t, FirstResponseMsFromContext(context.Background()))
	require.Nil(t, FirstResponseMsFromContext(nil)) //nolint:staticcheck // Exercise the documented nil-context guard.
}

func TestPKEarlyHeartbeat_StopRestoresWriterAndPendingHeaders(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	original := c.Writer
	stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{})
	w := earlySSEWriterFromContext(c)
	c.Writer.Header().Set("X-Pending", "kept")
	stop()
	stop()
	require.Same(t, original, c.Writer)
	require.Equal(t, "kept", c.Writer.Header().Get("X-Pending"))
	firePKEarlyHeartbeat(t, w)
	require.Empty(t, rec.Body.String())
}

func TestPKEarlyHeartbeat_DownstreamPingModeAndLegacyDefault(t *testing.T) {
	for _, mode := range []string{"", StreamEarlyHeartbeatModeComment, "unknown", StreamEarlyHeartbeatModeDownstreamPing} {
		for _, protocol := range []string{"responses", "chat", "messages"} {
			t.Run(mode+"/"+protocol, func(t *testing.T) {
				c, rec := newCompactBridgeTestContext(t, false)
				stop := startEarlySSEHeartbeat(c, time.Now(), protocol, StreamEarlyHeartbeatSettings{
					Enabled: true, Mode: mode, MinDelayMS: 30000, MaxDelayMS: 30000,
				})
				defer stop()
				w := earlySSEWriterFromContext(c)
				c.Writer.Header().Set("X-Upstream-Private", "must-stay-private")
				firePKEarlyHeartbeat(t, w)
				want := ": keepalive\n\n"
				if mode == StreamEarlyHeartbeatModeDownstreamPing {
					want = "data: {\"type\":\"ping\"}\n\n"
					if protocol == "messages" {
						want = "event: ping\n" + want
					}
				}
				require.Equal(t, want, rec.Body.String())
				require.Equal(t, "text/event-stream", rec.Result().Header.Get("Content-Type"))
				require.Empty(t, rec.Result().Header.Get("X-Upstream-Private"))
				require.True(t, c.Writer.Written())
				require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
				require.False(t, openAIStreamClientOutputStarted(c, false), "our own ping must preserve local failover")
				require.NotNil(t, FirstResponseMsFromContext(c.Request.Context()))
				w.beat()
				require.Equal(t, want, rec.Body.String(), "the first ping is sent only once")
				body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
				_, err := c.Writer.WriteString(body)
				require.NoError(t, err)
				firePKEarlyHeartbeat(t, w)
				require.Equal(t, want+body, rec.Body.String(), "real output remains byte-for-byte unchanged and stops the timer")
				require.Equal(t, len(body), OpenAICompactKeepaliveAdjustedWrittenSize(c))
			})
		}
	}
	require.True(t, openAIStreamDataStartsTTFT(`{"type":"ping"}`, "", false, OpenAITTFTModeSemantic))
	require.False(t, openAIStreamDataStartsTTFT(`{"type":"ping"}`, "", false, OpenAITTFTModeVisible))
	require.False(t, isOpenAIChatUsageOnlyStreamChunk(`{"type":"ping"}`))
}

func TestPKEarlyHeartbeat_DownstreamPingKeepsPeriodicComments(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{
		Enabled: true, Mode: StreamEarlyHeartbeatModeDownstreamPing, MinDelayMS: 30000, MaxDelayMS: 30000,
	})
	defer stop()
	w := earlySSEWriterFromContext(c)
	stopPeriodic := startOpenAISSEKeepalive(c, time.Hour)
	defer stopPeriodic()
	w.mu.Lock()
	w.repeatDue = time.Now()
	w.mu.Unlock()
	w.beat()
	w.mu.Lock()
	firstDue := w.firstDue
	w.mu.Unlock()
	require.False(t, firstDue.IsZero(), "an earlier periodic comment cannot consume the pending ping")
	firePKEarlyHeartbeat(t, w)
	w.mu.Lock()
	w.repeatDue = time.Now()
	w.mu.Unlock()
	w.beat()
	w.beat()
	require.Equal(t, ": keepalive\n\ndata: {\"type\":\"ping\"}\n\n: keepalive\n\n", rec.Body.String())
	require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
}

func TestPKEarlyHeartbeat_DownstreamPingExternalHeartbeatAccounting(t *testing.T) {
	for _, frame := range []string{": queue\n\n", "data: {\"type\":\"ping\"}\n\n", "event: ping\ndata: {\"type\":\"ping\"}\n\n"} {
		t.Run(frame, func(t *testing.T) {
			c, rec := newCompactBridgeTestContext(t, false)
			stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{
				Enabled: true, Mode: StreamEarlyHeartbeatModeDownstreamPing, MinDelayMS: 30000, MaxDelayMS: 30000,
			})
			defer stop()
			w := earlySSEWriterFromContext(c)
			c.Writer.Header().Set("Content-Type", "text/event-stream")
			n, err := c.Writer.WriteString(frame)
			require.NoError(t, err)
			c.Set(GatewayStreamHeartbeatBytesKey, n)
			c.Writer.Flush()
			w.mu.Lock()
			firstDue, internalBytes := w.firstDue, w.bytes
			w.mu.Unlock()
			require.Zero(t, internalBytes, "queue bytes belong to the queue accounting")
			want := frame
			if strings.HasPrefix(frame, ":") {
				require.False(t, firstDue.IsZero(), "a queue comment leaves the compatibility ping pending")
				firePKEarlyHeartbeat(t, w)
				want += "data: {\"type\":\"ping\"}\n\n"
			} else {
				require.True(t, firstDue.IsZero(), "an actual queue ping already satisfies the first ping")
				w.beat()
			}
			require.Equal(t, want, rec.Body.String())
			require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
			require.False(t, openAIStreamClientOutputStarted(c, false))
		})
	}
}

func TestPKEarlyHeartbeat_DownstreamPingDisabledAndCanceled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			c, rec := newCompactBridgeTestContext(t, false)
			ctx, cancel := context.WithCancel(c.Request.Context())
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{
				Enabled: enabled, Mode: StreamEarlyHeartbeatModeDownstreamPing, MinDelayMS: 30000, MaxDelayMS: 30000,
			})
			defer stop()
			w := earlySSEWriterFromContext(c)
			w.beat()
			require.Empty(t, rec.Body.String())
			if !enabled {
				w.mu.Lock()
				firstDue := w.firstDue
				w.mu.Unlock()
				require.True(t, firstDue.IsZero())
			}
			cancel()
			select {
			case <-w.done:
			case <-time.After(time.Second):
				t.Fatal("cancellation did not stop compatibility ping")
			}
			firePKEarlyHeartbeat(t, w)
			require.Empty(t, rec.Body.String())
		})
	}
}

func TestPKEarlyHeartbeat_DownstreamPingErrorBoundaries(t *testing.T) {
	for _, protocol := range []string{"responses", "chat", "messages"} {
		for _, committed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/committed=%t", protocol, committed), func(t *testing.T) {
				c, rec := newCompactBridgeTestContext(t, false)
				stop := startEarlySSEHeartbeat(c, time.Now(), protocol, StreamEarlyHeartbeatSettings{
					Enabled: true, Mode: StreamEarlyHeartbeatModeDownstreamPing, MinDelayMS: 30000, MaxDelayMS: 30000,
				})
				defer stop()
				w := earlySSEWriterFromContext(c)
				prefix := ""
				if committed {
					firePKEarlyHeartbeat(t, w)
					prefix = rec.Body.String()
				}
				c.JSON(http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"type": "overloaded_error", "message": "try later"}})
				firePKEarlyHeartbeat(t, w)
				if !committed {
					require.Equal(t, http.StatusServiceUnavailable, rec.Code)
					require.Equal(t, "try later", gjson.Get(rec.Body.String(), "error.message").String())
					require.NotContains(t, rec.Body.String(), "ping")
					return
				}
				require.Equal(t, http.StatusOK, rec.Code)
				events := parseCompactBridgeSSE(t, strings.TrimPrefix(rec.Body.String(), prefix))
				require.Len(t, events, 1)
				if protocol == "responses" {
					require.Equal(t, "response.failed", events[0][0])
					require.Equal(t, "try later", gjson.Get(events[0][1], "response.error.message").String())
				} else {
					require.Equal(t, "error", events[0][0])
					require.Equal(t, "try later", gjson.Get(events[0][1], "error.message").String())
				}
				streamErr, ok := GetOpsStreamError(c)
				require.True(t, ok)
				require.Equal(t, http.StatusServiceUnavailable, streamErr.IntendedStatus)
				require.True(t, IsResponseCommitted(c))
				body := rec.Body.String()
				_, err := c.Writer.WriteString("must not append after terminal")
				require.NoError(t, err)
				require.Equal(t, body, rec.Body.String())
			})
		}
	}
}

// Feed the frame emitted by the controller through the actual downstream
// readers. This protects the compatibility claim independently of the small
// event-classification helpers, and keeps text and original usage observable.
func TestPKEarlyHeartbeat_DownstreamPingReaderTimingAndUsage(t *testing.T) {
	readers := []struct {
		name string
		chat bool
		run  func(*OpenAIGatewayService, *gin.Context, *http.Response, time.Time) (*int, *OpenAIUsage, error)
	}{
		{"responses", false, func(s *OpenAIGatewayService, c *gin.Context, r *http.Response, start time.Time) (*int, *OpenAIUsage, error) {
			v, err := s.handleStreamingResponse(c.Request.Context(), r, c, &Account{ID: 1, Platform: PlatformOpenAI}, start, "test-model", "test-model")
			if v == nil {
				return nil, nil, err
			}
			return v.firstTokenMs, v.usage, err
		}},
		{"responses_passthrough", false, func(s *OpenAIGatewayService, c *gin.Context, r *http.Response, start time.Time) (*int, *OpenAIUsage, error) {
			v, err := s.handleStreamingResponsePassthrough(c.Request.Context(), r, c, &Account{ID: 1, Platform: PlatformOpenAI}, start, "test-model", "test-model")
			if v == nil {
				return nil, nil, err
			}
			return v.firstTokenMs, v.usage, err
		}},
		{"chat_raw", true, func(s *OpenAIGatewayService, c *gin.Context, r *http.Response, start time.Time) (*int, *OpenAIUsage, error) {
			v, err := s.streamRawChatCompletions(c, r, &Account{ID: 1, Platform: PlatformOpenAI}, "test-model", "test-model", "test-model", nil, nil, start, 0)
			if v == nil {
				return nil, nil, err
			}
			return v.FirstTokenMs, &v.Usage, err
		}},
	}
	for _, readerCase := range readers {
		for _, mode := range []string{StreamEarlyHeartbeatModeComment, StreamEarlyHeartbeatModeDownstreamPing} {
			for _, ttftMode := range []string{OpenAITTFTModeSemantic, OpenAITTFTModeVisible} {
				if readerCase.chat && ttftMode == OpenAITTFTModeVisible {
					continue // Responses-only TTFT setting does not govern raw Chat.
				}
				t.Run(readerCase.name+"/"+mode+"/"+ttftMode, func(t *testing.T) {
					gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{openAITTFTMode: ttftMode, expiresAt: time.Now().Add(time.Minute).UnixNano()})
					t.Cleanup(func() { gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{}) })
					emitter, emitted := newCompactBridgeTestContext(t, false)
					stop := startEarlySSEHeartbeat(emitter, time.Now(), "responses", StreamEarlyHeartbeatSettings{
						Enabled: true, Mode: mode, MinDelayMS: 30000, MaxDelayMS: 30000,
					})
					firePKEarlyHeartbeat(t, earlySSEWriterFromContext(emitter))
					stop()
					firstFrame := emitted.Body.String()
					item := `{"id":"msg_real","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}`
					realStream := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_real\",\"model\":\"test-model\"}}\n\n" +
						"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"msg_real\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"in_progress\",\"content\":[]}}\n\n" +
						"event: response.content_part.added\ndata: {\"type\":\"response.content_part.added\",\"item_id\":\"msg_real\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\",\"annotations\":[]}}\n\n" +
						"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_real\",\"output_index\":0,\"content_index\":0,\"delta\":\"hello\"}\n\n" +
						"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":" + item + "}\n\n" +
						"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_real\",\"status\":\"completed\",\"output\":[" + item + "],\"usage\":{\"input_tokens\":11,\"output_tokens\":7,\"total_tokens\":18}}}\n\n"
					if readerCase.chat {
						realStream = "data: {\"id\":\"chat_real\",\"object\":\"chat.completion.chunk\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n" +
							"data: {\"id\":\"chat_real\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
							"data: {\"id\":\"chat_real\",\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18}}\n\n" +
							"data: [DONE]\n\n"
					}
					pr, pw := io.Pipe()
					defer func() { _ = pr.Close() }()
					defer func() { _ = pw.Close() }()
					writerDone := make(chan struct{})
					textReady := make(chan int, 1)
					started := time.Now()
					go func() {
						defer close(writerDone)
						defer func() { _ = pw.Close() }()
						if _, err := io.WriteString(pw, firstFrame); err != nil {
							return
						}
						time.Sleep(100 * time.Millisecond)
						textReady <- int(time.Since(started).Milliseconds())
						_, _ = io.WriteString(pw, realStream)
					}()
					c, rec := newCompactBridgeTestContext(t, false)
					svc := newCompactBridgeTestService()
					svc.cfg.Gateway.MaxLineSize = defaultMaxLineSize
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: pr}
					firstToken, usage, err := readerCase.run(svc, c, resp, started)
					require.NoError(t, err)
					require.NotNil(t, firstToken)
					require.NotNil(t, usage)
					select {
					case <-writerDone:
					case <-time.After(time.Second):
						t.Fatal("downstream reader left the upstream writer blocked")
					}
					var textAt int
					select {
					case textAt = <-textReady:
					default:
						t.Fatal("upstream never supplied real text")
					}
					if mode == StreamEarlyHeartbeatModeDownstreamPing && ttftMode == OpenAITTFTModeSemantic {
						require.Less(t, *firstToken, textAt, "the actual downstream reader counts ping before text")
					} else {
						require.GreaterOrEqual(t, *firstToken, textAt, "comments and visible TTFT still wait for real output")
					}
					require.Equal(t, 11, usage.InputTokens)
					require.Equal(t, 7, usage.OutputTokens)
					var text string
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						payload, ok := extractOpenAISSEDataLine(line)
						if !ok {
							continue
						}
						if readerCase.chat {
							text += gjson.Get(payload, "choices.0.delta.content").String()
						} else if gjson.Get(payload, "type").String() == "response.output_text.delta" {
							text += gjson.Get(payload, "delta").String()
						}
					}
					require.Equal(t, "hello", text, "no placeholder character may enter the model text")
					require.Contains(t, rec.Body.String(), firstFrame)
					require.Contains(t, rec.Body.String(), "\"total_tokens\":18")
				})
			}
		}
	}
}

func TestPKEarlyHeartbeat_DownstreamPingPreservesLocalReaderFailover(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			c, rec := newCompactBridgeTestContext(t, false)
			stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{
				Enabled: true, Mode: StreamEarlyHeartbeatModeDownstreamPing, MinDelayMS: 30000, MaxDelayMS: 30000,
			})
			defer stop()
			firePKEarlyHeartbeat(t, earlySSEWriterFromContext(c))
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_failed\"}}\n\n" +
					"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failed\",\"status\":\"failed\",\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"Our servers are currently overloaded. Please try again later.\"}}}\n\n"))}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
			var err error
			if passthrough {
				_, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
			} else {
				_, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
			}
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.RetryableOnSameAccount)
			require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
			require.Equal(t, "data: {\"type\":\"ping\"}\n\n", rec.Body.String())
		})
	}
}

// Deliberately return a short count without an error to exercise defensive
// handling of middleware writers that violate the io.Writer contract.
type pkEarlySSEShortWriter struct {
	gin.ResponseWriter
	limit int
	calls int
}

func (w *pkEarlySSEShortWriter) Write(data []byte) (int, error) {
	w.calls++
	if w.limit >= 0 && w.limit < len(data) {
		data = data[:w.limit]
	}
	return w.ResponseWriter.Write(data)
}

func TestPKEarlyHeartbeat_ShortTimerWriteStopsWithoutCompletingPing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		protocol string
		frame    string
	}{
		{"comment", StreamEarlyHeartbeatModeComment, "responses", ": keepalive\n\n"},
		{"responses ping", StreamEarlyHeartbeatModeDownstreamPing, "responses", "data: {\"type\":\"ping\"}\n\n"},
		{"messages ping", StreamEarlyHeartbeatModeDownstreamPing, "messages", "event: ping\ndata: {\"type\":\"ping\"}\n\n"},
	} {
		for _, written := range []int{0, 7} {
			t.Run(fmt.Sprintf("%s/bytes=%d", tc.name, written), func(t *testing.T) {
				c, rec := newCompactBridgeTestContext(t, false)
				short := &pkEarlySSEShortWriter{ResponseWriter: c.Writer, limit: written}
				c.Writer = short
				stop := startEarlySSEHeartbeat(c, time.Now(), tc.protocol, StreamEarlyHeartbeatSettings{
					Enabled: true, Mode: tc.mode, MinDelayMS: 30000, MaxDelayMS: 30000,
				})
				defer stop()
				w := earlySSEWriterFromContext(c)
				firePKEarlyHeartbeat(t, w)
				w.mu.Lock()
				stopped, firstDue, heartbeatBytes := w.stopped, w.firstDue, w.bytes
				w.mu.Unlock()
				require.True(t, stopped)
				require.False(t, firstDue.IsZero(), "a partial frame must not satisfy the first heartbeat")
				require.Equal(t, written, heartbeatBytes)
				require.Equal(t, tc.frame[:written], rec.Body.String())
				require.False(t, rec.Flushed, "a short frame is a write failure, not a successful flush")
				if written == 0 {
					require.Nil(t, FirstResponseMsFromContext(c.Request.Context()))
				} else {
					require.NotNil(t, FirstResponseMsFromContext(c.Request.Context()), "partial nonempty writes still count as first response")
					require.Equal(t, -1, EarlySSEAdjustedWrittenSize(c))
				}
				firePKEarlyHeartbeat(t, w)
				require.Equal(t, 1, short.calls, "write failure must permanently stop the timer")
				require.Equal(t, tc.frame[:written], rec.Body.String())
			})
		}
	}
}

func TestPKEarlyHeartbeat_ShortBusinessAndQueueWritesReturnError(t *testing.T) {
	for _, frame := range []string{
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n",
		": queue\n\n",
		"data: {\"type\":\"ping\"}\n\n",
	} {
		for _, written := range []int{0, 7} {
			t.Run(fmt.Sprintf("%q/bytes=%d", frame, written), func(t *testing.T) {
				c, rec := newCompactBridgeTestContext(t, false)
				short := &pkEarlySSEShortWriter{ResponseWriter: c.Writer, limit: written}
				c.Writer = short
				stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{
					Enabled: true, Mode: StreamEarlyHeartbeatModeDownstreamPing, MinDelayMS: 30000, MaxDelayMS: 30000,
				})
				defer stop()
				c.Writer.Header().Set("Content-Type", "text/event-stream")
				n, err := c.Writer.WriteString(frame)
				require.Equal(t, written, n)
				require.ErrorIs(t, err, io.ErrShortWrite)
				w := earlySSEWriterFromContext(c)
				w.mu.Lock()
				stopped, firstDue, heartbeatBytes := w.stopped, w.firstDue, w.bytes
				w.mu.Unlock()
				require.True(t, stopped)
				require.False(t, firstDue.IsZero(), "a partial queue ping cannot satisfy the pending ping")
				require.Zero(t, heartbeatBytes, "external bytes must not be counted as timer bytes")
				require.Equal(t, frame[:written], rec.Body.String())
				if written == 0 {
					require.Nil(t, FirstResponseMsFromContext(c.Request.Context()))
				} else {
					require.NotNil(t, FirstResponseMsFromContext(c.Request.Context()))
				}
				firePKEarlyHeartbeat(t, w)
				require.Equal(t, 1, short.calls)
			})
		}
	}
}

func TestPKEarlyHeartbeat_ShortLateErrorDoesNotReportSuccess(t *testing.T) {
	for _, protocol := range []string{"responses", "chat", "messages"} {
		for _, written := range []int{0, 7} {
			t.Run(fmt.Sprintf("%s/bytes=%d", protocol, written), func(t *testing.T) {
				c, rec := newCompactBridgeTestContext(t, false)
				short := &pkEarlySSEShortWriter{ResponseWriter: c.Writer, limit: -1}
				c.Writer = short
				stop := startEarlySSEHeartbeat(c, time.Now(), protocol, StreamEarlyHeartbeatSettings{
					Enabled: true, Mode: StreamEarlyHeartbeatModeDownstreamPing, MinDelayMS: 30000, MaxDelayMS: 30000,
				})
				defer stop()
				w := earlySSEWriterFromContext(c)
				firePKEarlyHeartbeat(t, w)
				prefix := rec.Body.String()
				first := FirstResponseMsFromContext(c.Request.Context())
				w.mu.Lock()
				short.limit = written
				w.mu.Unlock()
				c.Writer.Header().Set("Content-Type", "application/json")
				c.Writer.WriteHeader(http.StatusServiceUnavailable)
				n, err := c.Writer.Write([]byte(`{"error":{"type":"overloaded_error","message":"try later"}}`))
				require.Zero(t, n, "a truncated replacement frame cannot acknowledge the original JSON")
				require.ErrorIs(t, err, io.ErrShortWrite)
				require.Len(t, rec.Body.String(), len(prefix)+written)
				require.Equal(t, first, FirstResponseMsFromContext(c.Request.Context()))
				w.mu.Lock()
				stopped, terminal, heartbeatBytes := w.stopped, w.terminal, w.bytes
				w.mu.Unlock()
				require.True(t, stopped)
				require.True(t, terminal)
				require.Equal(t, len(prefix), heartbeatBytes, "terminal error bytes are not transport heartbeat bytes")
				firePKEarlyHeartbeat(t, w)
				require.Equal(t, 2, short.calls)
				before := rec.Body.String()
				_, err = c.Writer.WriteString("must not append after partial terminal")
				require.NoError(t, err)
				require.Equal(t, before, rec.Body.String())
				require.Equal(t, 2, short.calls)
			})
		}
	}
}

func newPKEarlySpaceWriter(t *testing.T, protocol string) (*gin.Context, *earlySSEHeartbeatWriter, func() string) {
	t.Helper()
	c, recorder := newCompactBridgeTestContext(t, false)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Model, "gpt-test"))
	stop := startEarlySSEHeartbeat(c, time.Now(), protocol, StreamEarlyHeartbeatSettings{
		Enabled: true, Mode: StreamEarlyHeartbeatModeSpace, MinDelayMS: 30000, MaxDelayMS: 30000,
	})
	t.Cleanup(stop)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	return c, earlySSEWriterFromContext(c), recorder.Body.String
}

func pkSpaceEvents(body string) []gjson.Result {
	var events []gjson.Result
	forEachOpenAISSEFrame(body, func(_ string, data []byte) {
		events = append(events, gjson.ParseBytes(data))
	})
	return events
}

const pkSpaceCreated = "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_real\",\"object\":\"response\",\"created_at\":17,\"model\":\"gpt-test\",\"status\":\"in_progress\",\"output\":[]}}\n\n"

func TestPKEarlyHeartbeat_SpaceChatFramesAndFragmentedRealUsage(t *testing.T) {
	c, w, body := newPKEarlySpaceWriter(t, "chat")
	firePKEarlyHeartbeat(t, w)
	events := pkSpaceEvents(body())
	require.Len(t, events, 3)
	require.Equal(t, "assistant", events[0].Get("choices.0.delta.role").String())
	require.Equal(t, " ", events[1].Get("choices.0.delta.content").String())
	require.Equal(t, "{}", events[2].Get("choices.0.delta").Raw)
	id := events[0].Get("id").String()
	require.NotEmpty(t, id)
	require.Equal(t, "gpt-test", events[0].Get("model").String())
	require.False(t, events[0].Get("usage").Exists())
	require.True(t, EarlySSESpaceOutputCommitted(c))
	require.Positive(t, EarlySSEAdjustedWrittenSize(c), "visible output must stop pre-output failover")
	real := "id: retained\r\ndata: {\"id\":\"chat_upstream\",\"object\":\"chat.completion.chunk\",\"created\":18,\"model\":\"gpt-test\",\r\ndata: \"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7},\"unknown\":{\"integer\":9007199254740993}}\r\n\r\n"
	for _, char := range []byte(real) {
		n, err := c.Writer.Write([]byte{char})
		require.NoError(t, err)
		require.Equal(t, 1, n)
	}
	events = pkSpaceEvents(body())
	require.Len(t, events, 4)
	require.Equal(t, id, events[3].Get("id").String())
	require.Equal(t, events[0].Get("created").Int(), events[3].Get("created").Int())
	require.Equal(t, "hello", events[3].Get("choices.0.delta.content").String())
	require.Equal(t, int64(11), events[3].Get("usage.prompt_tokens").Int())
	require.Equal(t, int64(7), events[3].Get("usage.completion_tokens").Int())
	require.Equal(t, "9007199254740993", events[3].Get("unknown.integer").Raw)
	require.Contains(t, body(), "id: retained")
}

func TestPKEarlyHeartbeat_SpaceResponsesWaitsForRealIDAndKeepsSnapshots(t *testing.T) {
	c, w, body := newPKEarlySpaceWriter(t, "responses")
	firePKEarlyHeartbeat(t, w)
	require.Empty(t, pkSpaceEvents(body()), "unknown response ID must not create a synthetic response")
	require.False(t, EarlySSESpaceOutputCommitted(c))
	w.mu.Lock()
	firstDueCleared := w.firstDue.IsZero()
	w.mu.Unlock()
	require.True(t, firstDueCleared, "a due placeholder awaiting its ID must not busy-spin")
	_, err := c.Writer.WriteString(pkSpaceCreated)
	require.NoError(t, err)
	events := pkSpaceEvents(body())
	require.Len(t, events, 7)
	require.Equal(t, "resp_real", events[0].Get("response.id").String())
	require.Equal(t, " ", events[3].Get("delta").String())
	require.True(t, EarlySSESpaceOutputCommitted(c))
	_, err = c.Writer.WriteString("data: {\"type\":\"response.in_progress\",\"sequence_number\":1,\"response\":{\"id\":\"resp_real\",\"output\":[],\"unknown\":true}}\n\n")
	require.NoError(t, err)
	events = pkSpaceEvents(body())
	require.Equal(t, " ", events[len(events)-1].Get("response.output.0.content.0.text").String())
	require.True(t, events[len(events)-1].Get("response.unknown").Bool())
	terminal := "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_real\",\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"id\":\"fc_real\",\"call_id\":\"call_real\",\"name\":\"lookup\",\"arguments\":\"{}\"},{\"type\":\"message\",\"id\":\"msg_real\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"refusal\",\"refusal\":\"\"},{\"type\":\"output_text\",\"text\":\"hello\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":11,\"output_tokens\":7},\"opaque\":9007199254740993}}\n\n"
	_, err = c.Writer.WriteString(terminal)
	require.NoError(t, err)
	events = pkSpaceEvents(body())
	var realText strings.Builder
	var added []string
	for i, event := range events {
		require.Equal(t, int64(i), event.Get("sequence_number").Int())
		if event.Get("type").String() == "response.output_item.added" {
			added = append(added, event.Get("item.id").String())
			require.Equal(t, int64(len(added)-1), event.Get("output_index").Int())
		}
		if event.Get("type").String() == "response.output_text.delta" && !event.Get("sub2api_placeholder").Bool() {
			realText.WriteString(event.Get("delta").String())
			require.Equal(t, int64(2), event.Get("output_index").Int())
			require.Equal(t, int64(1), event.Get("content_index").Int())
		}
	}
	require.Equal(t, "hello", realText.String(), "terminal-only real text must reach downstream converters")
	require.Len(t, added, 3)
	last := events[len(events)-1]
	require.Equal(t, "resp_real", last.Get("response.id").String())
	require.Equal(t, "fc_real", last.Get("response.output.1.id").String())
	require.Equal(t, "msg_real", last.Get("response.output.2.id").String())
	require.Equal(t, int64(11), last.Get("response.usage.input_tokens").Int())
	require.Equal(t, int64(7), last.Get("response.usage.output_tokens").Int())
	require.Equal(t, "9007199254740993", last.Get("response.opaque").Raw)
}

func TestPKEarlyHeartbeat_SpaceRealDeltasAndMissingTerminalOutput(t *testing.T) {
	c, w, body := newPKEarlySpaceWriter(t, "responses")
	_, err := c.Writer.WriteString(pkSpaceCreated)
	require.NoError(t, err)
	firePKEarlyHeartbeat(t, w)
	for _, payload := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_real","type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg_real","part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_real","delta":"hello"}`,
		`{"type":"response.in_progress","response":{"id":"resp_real","output":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_real","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}}`,
		`{"type":"response.completed","response":{"id":"resp_real","status":"completed","usage":{"input_tokens":11,"output_tokens":7}}}`,
	} {
		_, err := c.Writer.WriteString("data: " + payload + "\n\n")
		require.NoError(t, err)
	}
	events := pkSpaceEvents(body())
	deltas := 0
	for _, event := range events {
		if event.Get("type").String() == "response.output_text.delta" && !event.Get("sub2api_placeholder").Bool() {
			deltas++
		}
		if event.Get("type").String() == "response.in_progress" {
			require.Equal(t, "hello", event.Get("response.output.1.content.0.text").String())
		}
	}
	require.Equal(t, 1, deltas, "real text must not be duplicated at item.done or completed")
	require.Equal(t, "hello", events[len(events)-1].Get("response.output.1.content.0.text").String())
}

func TestPKEarlyHeartbeat_SpacePartialCommentDeadlineAndFastOutput(t *testing.T) {
	for _, protocol := range []string{"chat", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			c, w, body := newPKEarlySpaceWriter(t, protocol)
			if protocol == "responses" {
				_, err := c.Writer.WriteString(pkSpaceCreated)
				require.NoError(t, err)
			}
			_, err := c.Writer.WriteString(": partial")
			require.NoError(t, err)
			firePKEarlyHeartbeat(t, w)
			require.False(t, EarlySSESpaceOutputCommitted(c))
			_, err = c.Writer.WriteString("\r\n\r\n")
			require.NoError(t, err)
			require.True(t, EarlySSESpaceOutputCommitted(c))
			require.Contains(t, body(), "sub2api_placeholder")
		})
	}
	c, w, body := newPKEarlySpaceWriter(t, "responses")
	firePKEarlyHeartbeat(t, w)
	real := pkSpaceCreated + "data: {\"type\":\"response.output_text.delta\",\"delta\":\"fast\"}\n\n"
	_, err := c.Writer.WriteString(real)
	require.NoError(t, err)
	require.NotContains(t, body(), "sub2api_placeholder", "real output in the same write wins over a deferred placeholder")
	require.False(t, EarlySSESpaceOutputCommitted(c))
}

func TestPKEarlyHeartbeat_SpaceFallbacksAndShortWrites(t *testing.T) {
	t.Run("messages stays comments", func(t *testing.T) {
		c, w, body := newPKEarlySpaceWriter(t, "messages")
		firePKEarlyHeartbeat(t, w)
		require.Equal(t, ": keepalive\n\n", body())
		require.False(t, EarlySSESpaceOutputCommitted(c))
	})
	t.Run("structured request stays comments", func(t *testing.T) {
		c, recorder := newCompactBridgeTestContext(t, false)
		snapshot := earlySpaceRequestSnapshot(c, []byte(`{"model":"gpt-test","text":{"format":{"type":"json_schema"}}}`))
		stop := startEarlySSEHeartbeat(c, time.Now(), "responses", StreamEarlyHeartbeatSettings{Enabled: true, Mode: StreamEarlyHeartbeatModeSpace, MinDelayMS: 30000, MaxDelayMS: 30000}, snapshot)
		defer stop()
		firePKEarlyHeartbeat(t, earlySSEWriterFromContext(c))
		require.Equal(t, ": keepalive\n\n", recorder.Body.String())
	})
	t.Run("chat input shape stays comments", func(t *testing.T) {
		c, recorder := newCompactBridgeTestContext(t, false)
		snapshot := earlySpaceRequestSnapshot(c, []byte(`{"model":"gpt-test","input":"hello"}`))
		stop := startEarlySSEHeartbeat(c, time.Now(), "chat", StreamEarlyHeartbeatSettings{Enabled: true, Mode: StreamEarlyHeartbeatModeSpace, MinDelayMS: 30000, MaxDelayMS: 30000}, snapshot)
		defer stop()
		firePKEarlyHeartbeat(t, earlySSEWriterFromContext(c))
		require.Equal(t, ": keepalive\n\n", recorder.Body.String())
		require.Nil(t, c.Request.Context().Value(ctxkey.EarlySSESpaceRetryGate))
	})
	for _, count := range []int{0, 7} {
		t.Run(fmt.Sprintf("short bytes %d", count), func(t *testing.T) {
			c, _ := newCompactBridgeTestContext(t, false)
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Model, "gpt-test"))
			short := &pkEarlySSEShortWriter{ResponseWriter: c.Writer, limit: count}
			c.Writer = short
			stop := startEarlySSEHeartbeat(c, time.Now(), "chat", StreamEarlyHeartbeatSettings{Enabled: true, Mode: StreamEarlyHeartbeatModeSpace, MinDelayMS: 30000, MaxDelayMS: 30000})
			defer stop()
			w := earlySSEWriterFromContext(c)
			firePKEarlyHeartbeat(t, w)
			require.Equal(t, count > 0, EarlySSESpaceOutputCommitted(c))
			_, err := c.Writer.WriteString("data: {}\n\n")
			require.ErrorIs(t, err, io.ErrShortWrite)
			require.Equal(t, 1, short.calls)
		})
	}
}

func TestPKEarlyHeartbeat_SpaceRetryGateCancelsOnlySpace(t *testing.T) {
	for _, mode := range []string{StreamEarlyHeartbeatModeComment, StreamEarlyHeartbeatModeDownstreamPing, StreamEarlyHeartbeatModeSpace} {
		t.Run(mode, func(t *testing.T) {
			c, rec := newCompactBridgeTestContext(t, false)
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Model, "gpt-test"))
			stop := startEarlySSEHeartbeat(c, time.Now(), "chat", StreamEarlyHeartbeatSettings{Enabled: true, Mode: mode, MinDelayMS: 30000, MaxDelayMS: 30000})
			defer stop()
			require.True(t, PrepareEarlySSESpaceRetry(c))
			firePKEarlyHeartbeat(t, earlySSEWriterFromContext(c))
			if mode == StreamEarlyHeartbeatModeSpace {
				require.Empty(t, rec.Body.String())
				gate, ok := c.Request.Context().Value(ctxkey.EarlySSESpaceRetryGate).(func() bool)
				require.True(t, ok)
				require.True(t, gate())
			} else {
				require.NotEmpty(t, rec.Body.String(), "existing heartbeat modes retain their timer")
				require.Nil(t, c.Request.Context().Value(ctxkey.EarlySSESpaceRetryGate))
			}
		})
	}
	c, w, _ := newPKEarlySpaceWriter(t, "chat")
	firePKEarlyHeartbeat(t, w)
	require.False(t, PrepareEarlySSESpaceRetry(c), "published space must reject another attempt")
	gate, ok := c.Request.Context().Value(ctxkey.EarlySSESpaceRetryGate).(func() bool)
	require.True(t, ok)
	require.False(t, gate(), "transport fallback uses the same atomic gate")
}

func TestPKEarlyHeartbeat_SpaceResponseLateErrorUsesRealID(t *testing.T) {
	c, w, body := newPKEarlySpaceWriter(t, "responses")
	_, err := c.Writer.WriteString(pkSpaceCreated)
	require.NoError(t, err)
	firePKEarlyHeartbeat(t, w)
	_, err = c.Writer.WriteString("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"fc_real\",\"type\":\"function_call\",\"arguments\":\"{}\"}}\n\n")
	require.NoError(t, err)
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusServiceUnavailable)
	_, err = c.Writer.WriteString(`{"error":{"type":"overloaded_error","message":"later"}}`)
	require.NoError(t, err)
	events := pkSpaceEvents(body())
	last := events[len(events)-1]
	require.Equal(t, "response.failed", last.Get("type").String())
	require.Equal(t, "resp_real", last.Get("response.id").String())
	require.Equal(t, " ", last.Get("response.output.0.content.0.text").String())
	require.Equal(t, "{}", last.Get("response.output.1.arguments").String())
	require.Equal(t, int64(len(events)-1), last.Get("sequence_number").Int())
	require.False(t, last.Get("response.usage").Exists())
}

func TestPKEarlyHeartbeat_SpaceProgressPreservesSourceAndKnownDeltas(t *testing.T) {
	c, w, body := newPKEarlySpaceWriter(t, "responses")
	_, err := c.Writer.WriteString(pkSpaceCreated)
	require.NoError(t, err)
	firePKEarlyHeartbeat(t, w)
	for _, payload := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_real","type":"function_call","arguments":""}}`,
		`{"type":"response.in_progress","response":{"id":"resp_real","output":[{"id":"fc_real","type":"function_call","arguments":"{}","status":"in_progress","opaque":9007199254740993}]}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"rs_real","type":"reasoning","summary":[]}}`,
		`{"type":"response.reasoning_summary_part.added","output_index":1,"summary_index":0,"part":{"type":"summary_text","text":""}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":1,"summary_index":0,"delta":"a"}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"id":"msg_real","type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":2,"content_index":0,"part":{"type":"refusal","refusal":""}}`,
		`{"type":"response.refusal.delta","output_index":2,"content_index":0,"delta":"no"}`,
		`{"type":"response.content_part.added","output_index":2,"content_index":1,"part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.annotation.added","output_index":2,"content_index":1,"annotation_index":0,"annotation":{"type":"url_citation","url":"https://example.com","title":"source","start_index":0,"end_index":1}}`,
		`{"type":"response.in_progress","response":{"id":"resp_real","output":[]}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":1,"summary_index":0,"delta":"b"}`,
		`{"type":"response.refusal.delta","output_index":2,"content_index":0,"delta":"!"}`,
		`{"type":"response.in_progress","response":{"id":"resp_real","output":[]}}`,
	} {
		_, err := c.Writer.WriteString("data: " + payload + "\n\n")
		require.NoError(t, err)
	}
	events := pkSpaceEvents(body())
	last := events[len(events)-1]
	require.Equal(t, "{}", last.Get("response.output.1.arguments").String())
	require.Equal(t, "9007199254740993", last.Get("response.output.1.opaque").Raw)
	require.Equal(t, "in_progress", last.Get("response.output.1.status").String())
	require.Equal(t, "ab", last.Get("response.output.2.summary.0.text").String())
	require.Equal(t, "no!", last.Get("response.output.3.content.0.refusal").String())
	require.Equal(t, "https://example.com", last.Get("response.output.3.content.1.annotations.0.url").String())
}

func TestPKEarlyHeartbeat_SpaceCancellationPreventsDeferredInjection(t *testing.T) {
	for _, protocol := range []string{"chat", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			c, w, body := newPKEarlySpaceWriter(t, protocol)
			_, err := c.Writer.WriteString(": pending")
			require.NoError(t, err)
			firePKEarlyHeartbeat(t, w)
			w.mu.Lock()
			w.stopLocked()
			w.mu.Unlock()
			_, err = c.Writer.WriteString("\n\n")
			require.NoError(t, err)
			if protocol == "responses" {
				_, err = c.Writer.WriteString(pkSpaceCreated)
				require.NoError(t, err)
			}
			require.NotContains(t, body(), "sub2api_placeholder")
			require.False(t, EarlySSESpaceOutputCommitted(c))
		})
	}
}

func TestPKEarlyHeartbeat_SpaceCRFrameEOFAndStructuredCreated(t *testing.T) {
	t.Run("CR-only terminal boundary", func(t *testing.T) {
		c, w, body := newPKEarlySpaceWriter(t, "chat")
		firePKEarlyHeartbeat(t, w)
		_, err := c.Writer.WriteString("data: {\"id\":\"real\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\r\r")
		require.NoError(t, err)
		w.mu.Lock()
		w.finishSpaceLocked()
		w.mu.Unlock()
		events := pkSpaceEvents(body())
		require.Len(t, events, 4)
		require.Equal(t, "hello", events[3].Get("choices.0.delta.content").String())
		require.NotContains(t, body(), "incomplete SSE")
	})
	for _, field := range []string{`"text":{"format":{"type":"json_object"}},`, `"output":[{"id":"already","type":"message","content":[]}],`} {
		t.Run(field, func(t *testing.T) {
			c, w, body := newPKEarlySpaceWriter(t, "responses")
			firePKEarlyHeartbeat(t, w)
			_, err := c.Writer.WriteString("data: {\"type\":\"response.created\",\"response\":{" + field + "\"id\":\"resp_real\"}}\n\n")
			require.NoError(t, err)
			require.NotContains(t, body(), "sub2api_placeholder")
			require.False(t, EarlySSESpaceOutputCommitted(c))
		})
	}
}

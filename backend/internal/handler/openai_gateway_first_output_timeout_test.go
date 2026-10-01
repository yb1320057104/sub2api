package handler

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIForwardMayFailoverOnlyAfterNonSemanticWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	before := service.OpenAICompactKeepaliveAdjustedWrittenSize(c)

	_, err := fmt.Fprint(c.Writer, ":\n\n")
	require.NoError(t, err)
	c.Writer.Flush()

	require.True(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{
		SafeToFailoverAfterWrite: true,
	}))
	require.False(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{}))
}

func TestOpenAIFirstOutputFailoverStopsAfterOneAccountSwitch(t *testing.T) {
	failoverErr := &service.UpstreamFailoverError{SafeToFailoverAfterWrite: true}
	count := 0

	require.False(t, openAIFirstOutputFailoverExhausted(failoverErr, &count))
	require.Equal(t, 1, count)
	require.True(t, openAIFirstOutputFailoverExhausted(failoverErr, &count))
	require.Equal(t, 1, count)
}

func TestOpenAIRequestAllowsFailoverReplayStopsCanceledClient(t *testing.T) {
	require.False(t, openAIRequestAllowsFailoverReplay(nil))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	requestCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(requestCtx)

	require.True(t, openAIRequestAllowsFailoverReplay(c))
	cancel()
	require.False(t, openAIRequestAllowsFailoverReplay(c))
}

type earlySpaceFailoverSettingsRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *earlySpaceFailoverSettingsRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func (r *earlySpaceFailoverSettingsRepo) SetMultiple(_ context.Context, values map[string]string) error {
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func startEarlySpaceFailoverTestWriter(t *testing.T, protocol, mode string, delay int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	setOpsRequestContext(c, "gpt-test", true)
	settings := service.NewSettingService(&earlySpaceFailoverSettingsRepo{values: map[string]string{}}, nil)
	ctx := context.Background()
	previous := settings.GetStreamEarlyHeartbeatSettings(ctx)
	t.Cleanup(func() {
		require.NoError(t, settings.UpdateSettings(ctx, &service.SystemSettings{
			StreamEarlyHeartbeatEnabled:    previous.Enabled,
			StreamEarlyHeartbeatMode:       previous.Mode,
			StreamEarlyHeartbeatMinDelayMS: previous.MinDelayMS,
			StreamEarlyHeartbeatMaxDelayMS: previous.MaxDelayMS,
		}))
	})
	// The forwarding cache is shared between SettingService instances. Use the
	// real successful-write path so each fixture gets its requested snapshot.
	require.NoError(t, settings.UpdateSettings(ctx, &service.SystemSettings{
		StreamEarlyHeartbeatEnabled:    true,
		StreamEarlyHeartbeatMode:       mode,
		StreamEarlyHeartbeatMinDelayMS: delay,
		StreamEarlyHeartbeatMaxDelayMS: delay,
	}))
	gateway := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, settings, nil,
	)
	// Initialize SSE headers before starting the timer so its header snapshot
	// matches the stream before any zero-delay heartbeat can write.
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	stop := gateway.StartEarlySSEHeartbeat(c, time.Now(), protocol)
	t.Cleanup(stop)
	return c, rec
}

func TestPKEarlySpaceFailoverRejectsSpaceWrittenBeforeForwardSnapshot(t *testing.T) {
	for _, protocol := range []string{"chat", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			c, _ := startEarlySpaceFailoverTestWriter(t, protocol, service.StreamEarlyHeartbeatModeSpace, 0)
			if protocol == "responses" {
				_, err := c.Writer.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_real\",\"output\":[]}}\n\n"))
				require.NoError(t, err)
			}
			require.Eventually(t, func() bool { return service.EarlySSESpaceOutputCommitted(c) }, time.Second, time.Millisecond)
			before := service.OpenAICompactKeepaliveAdjustedWrittenSize(c)
			// Space sent during queueing must not prevent the first upstream attempt.
			require.True(t, openAIRequestAllowsFailoverReplay(c))
			require.False(t, openAIForwardMayFailover(c, before, nil))
			require.False(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{SafeToFailoverAfterWrite: true}))
		})
	}
}

func TestPKEarlySpaceFailoverCancelsPendingSpaceForLaterAttempt(t *testing.T) {
	c, rec := startEarlySpaceFailoverTestWriter(t, "responses", service.StreamEarlyHeartbeatModeSpace, 30000)
	before := service.OpenAICompactKeepaliveAdjustedWrittenSize(c)
	_, err := c.Writer.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_old\",\"output\":[]}}\n\n"))
	require.NoError(t, err)
	require.True(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{SafeToFailoverAfterWrite: true}))
	_, err = c.Writer.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_next\",\"output\":[]}}\n\n"))
	require.NoError(t, err)
	require.False(t, service.EarlySSESpaceOutputCommitted(c))
	require.Contains(t, rec.Body.String(), "resp_next")
	require.NotContains(t, rec.Body.String(), "sub2api_placeholder")
}

func TestPKEarlySpaceFailoverKeepsCommentAndPingBehavior(t *testing.T) {
	for _, mode := range []string{service.StreamEarlyHeartbeatModeComment, service.StreamEarlyHeartbeatModeDownstreamPing} {
		t.Run(mode, func(t *testing.T) {
			c, _ := startEarlySpaceFailoverTestWriter(t, "responses", mode, 0)
			require.Eventually(t, func() bool { return service.FirstResponseMsFromContext(c.Request.Context()) != nil }, time.Second, time.Millisecond)
			before := service.OpenAICompactKeepaliveAdjustedWrittenSize(c)
			require.False(t, service.EarlySSESpaceOutputCommitted(c))
			require.True(t, openAIForwardMayFailover(c, before, nil))
			_, err := c.Writer.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_real\"}}\n\n"))
			require.NoError(t, err)
			require.False(t, openAIForwardMayFailover(c, before, nil))
			require.True(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{SafeToFailoverAfterWrite: true}))
		})
	}
}

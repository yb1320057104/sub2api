package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
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
	mu     sync.RWMutex
	values map[string]string
}

func (r *earlySpaceFailoverSettingsRepo) Get(ctx context.Context, key string) (*service.Setting, error) {
	value, err := r.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}
	return &service.Setting{Key: key, Value: value}, nil
}

func (r *earlySpaceFailoverSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (r *earlySpaceFailoverSettingsRepo) Set(ctx context.Context, key, value string) error {
	return r.SetMultiple(ctx, map[string]string{key: value})
}

func (r *earlySpaceFailoverSettingsRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func (r *earlySpaceFailoverSettingsRepo) SetMultiple(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func (r *earlySpaceFailoverSettingsRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make(map[string]string, len(r.values))
	for key, value := range r.values {
		values[key] = value
	}
	return values, nil
}

func (r *earlySpaceFailoverSettingsRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
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
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
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

// These fixtures replace only the external HTTP peer and persistence. The test
// enters the real handler, which selects accounts, forwards/parses the stream,
// decides whether to fail over, and submits RecordUsage itself.
type pkEarlyHandlerBillingUpstream struct {
	service.HTTPUpstream
	scenario    string
	beforeFirst func()
	accountIDs  []int64
	paths       []string
}

const pkEarlyHandlerUsageStream = "event: response.created\n" +
	`data: {"type":"response.created","response":{"id":"resp_pk_early","object":"response","model":"gpt-5.4","status":"in_progress","output":[]}}` + "\n\n" +
	"event: response.output_item.added\n" +
	`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_pk_early","role":"assistant","status":"in_progress","content":[]}}` + "\n\n" +
	"event: response.content_part.added\n" +
	`data: {"type":"response.content_part.added","output_index":0,"item_id":"msg_pk_early","content_index":0,"part":{"type":"output_text","text":""}}` + "\n\n" +
	"event: response.output_text.delta\n" +
	`data: {"type":"response.output_text.delta","output_index":0,"item_id":"msg_pk_early","content_index":0,"delta":"actual answer","usage":{"input_tokens":1200,"output_tokens":13,"total_tokens":1213,"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":200}}}` + "\n\n"

const pkEarlyHandlerUsageCompleted = "event: response.completed\n" +
	`data: {"type":"response.completed","response":{"id":"resp_pk_early","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_pk_early","role":"assistant","status":"completed","content":[{"type":"output_text","text":"actual answer"}]}],"usage":{"input_tokens":1200,"output_tokens":13,"total_tokens":1213,"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":200}}}}` + "\n\n"

type pkEarlyHandlerInterruptedBody struct {
	io.Reader
}

func (b *pkEarlyHandlerInterruptedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func (*pkEarlyHandlerInterruptedBody) Close() error { return nil }

func (u *pkEarlyHandlerBillingUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.accountIDs = append(u.accountIDs, accountID)
	u.paths = append(u.paths, req.URL.Path)
	if len(u.accountIDs) == 1 && u.beforeFirst != nil {
		u.beforeFirst()
	}
	if u.scenario == "retry_then_complete" && len(u.accountIDs) == 1 {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temporary upstream failure"}}`)),
		}, nil
	}
	body := io.NopCloser(strings.NewReader(pkEarlyHandlerUsageStream + pkEarlyHandlerUsageCompleted))
	if u.scenario == "interrupted_after_usage" {
		body = &pkEarlyHandlerInterruptedBody{Reader: strings.NewReader(pkEarlyHandlerUsageStream)}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{fmt.Sprintf("pk-early-handler-%s-%d", u.scenario, accountID)},
		},
		Body: body,
	}, nil
}

func TestPKEarlyHeartbeatHandlerBillingAndFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scenario := range []string{"complete", "retry_then_complete", "interrupted_after_usage"} {
		for _, mode := range []string{"disabled", service.StreamEarlyHeartbeatModeComment, service.StreamEarlyHeartbeatModeDownstreamPing, service.StreamEarlyHeartbeatModeSpace} {
			t.Run(scenario+"/"+mode, func(t *testing.T) {
				cfg := &config.Config{}
				cfg.Default.RateMultiplier = 1
				cfg.Security.URLAllowlist.Enabled = false
				cfg.Gateway.MaxAccountSwitches = 1
				groupID := int64(9500)
				accounts := make([]service.Account, 2)
				for i := range accounts {
					accounts[i] = service.Account{
						ID: int64(9501 + i), Name: fmt.Sprintf("pk-early-handler-%d", i),
						Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
						Status: service.StatusActive, Schedulable: true, Priority: i + 1, Concurrency: 1,
						GroupIDs:    []int64{groupID},
						Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.example.test"},
						Extra: map[string]any{
							openai_compat.ExtraKeyResponsesMode:        string(openai_compat.ResponsesSupportModeForceResponses),
							service.AccountExtraCacheConversionEnabled: false,
							service.AccountExtraKiloBridgeEnabled:      false,
						},
					}
				}

				settings := service.NewSettingService(&earlySpaceFailoverSettingsRepo{values: map[string]string{}}, nil)
				previous := settings.GetStreamEarlyHeartbeatSettings(context.Background())
				t.Cleanup(func() {
					require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{
						StreamEarlyHeartbeatEnabled: previous.Enabled, StreamEarlyHeartbeatMode: previous.Mode,
						StreamEarlyHeartbeatMinDelayMS: previous.MinDelayMS, StreamEarlyHeartbeatMaxDelayMS: previous.MaxDelayMS,
					}))
				})
				configuredMode := mode
				if mode == "disabled" {
					configuredMode = service.StreamEarlyHeartbeatModeComment
				}
				require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{
					StreamEarlyHeartbeatEnabled: mode != "disabled", StreamEarlyHeartbeatMode: configuredMode,
					StreamEarlyHeartbeatMinDelayMS: 0, StreamEarlyHeartbeatMaxDelayMS: 0,
				}))

				capture := &systemOneUsageCapture{
					logs: make(chan *service.UsageLog, 4), commands: make(chan *service.UsageBillingCommand, 4),
				}
				billing := service.NewBillingService(cfg, nil)
				// Pin the v0.2.18 GPT-5.4 fallback price card instead of deriving
				// the expected charge through the same billing code under test.
				// Input is split into disjoint 900 regular / 200 write / 100 read.
				const expectedCost = 900*2.5e-6 + 13*15e-6 + 200*2.5e-6 + 100*0.25e-6

				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.4","stream":true,"messages":[{"role":"user","content":"hello"}]}`))
				c.Request.Header.Set("Content-Type", "application/json")
				upstream := &pkEarlyHandlerBillingUpstream{scenario: scenario}
				upstream.beforeFirst = func() {
					if mode == "disabled" {
						require.Nil(t, service.FirstResponseMsFromContext(c.Request.Context()), "no body was written before the upstream request")
						return
					}
					// Wait on the production writer's synchronized state, never read
					// ResponseRecorder while the heartbeat goroutine can write it.
					require.Eventually(t, func() bool {
						if mode == service.StreamEarlyHeartbeatModeSpace {
							return service.EarlySSESpaceOutputCommitted(c)
						}
						return service.FirstResponseMsFromContext(c.Request.Context()) != nil
					}, time.Second, time.Millisecond, "the real heartbeat timer must emit before the upstream reply")
				}
				billingCache := service.NewBillingCacheService(newHandlerInflightCache(100), nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billingCache.Stop)
				concurrencyCache := &concurrencyCacheMock{
					acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
					acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
				}
				concurrency := service.NewConcurrencyService(concurrencyCache)
				gateway := service.NewOpenAIGatewayService(
					&openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}, nil, capture, capture,
					nil, nil, nil, nil, cfg, nil, concurrency, billing, nil, nil, upstream,
					&service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil,
				)
				h := NewOpenAIGatewayHandler(gateway, concurrency, billingCache,
					service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
				apiKey := &service.APIKey{
					ID: 9503, UserID: 9504, GroupID: &groupID,
					User:  &service.User{ID: 9504, Status: service.StatusActive, Balance: 100},
					Group: &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1},
				}
				c.Set(string(middleware.ContextKeyAPIKey), apiKey)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 1})

				// No direct RecordUsage call: the real handler owns the entire
				// forwarding, retry and synchronous usage-submission lifecycle.
				h.ChatCompletions(c)

				wantAccountIDs := []int64{9501}
				blockedAfterSpace := scenario == "retry_then_complete" && mode == service.StreamEarlyHeartbeatModeSpace
				if scenario == "retry_then_complete" && !blockedAfterSpace {
					wantAccountIDs = append(wantAccountIDs, 9502)
				}
				require.Equal(t, wantAccountIDs, upstream.accountIDs, "only safe attempts may switch accounts: status=%d body=%s", rec.Code, rec.Body.String())
				require.Equal(t, int32(1), atomic.LoadInt32(&concurrencyCache.releaseUserCalled))
				require.Equal(t, int32(len(wantAccountIDs)), atomic.LoadInt32(&concurrencyCache.releaseAccountCalled))
				for _, path := range upstream.paths {
					require.Equal(t, "/v1/responses", path, "exercise the real Chat-to-Responses forwarding bridge")
				}
				output := rec.Body.String() // handler Stop has joined the heartbeat goroutine
				switch mode {
				case "disabled":
					require.NotContains(t, output, ": keepalive")
					require.NotContains(t, output, "sub2api_placeholder")
				case service.StreamEarlyHeartbeatModeComment:
					require.Contains(t, output, ": keepalive")
				case service.StreamEarlyHeartbeatModeDownstreamPing:
					require.Contains(t, output, `{"type":"ping"}`)
				case service.StreamEarlyHeartbeatModeSpace:
					require.Contains(t, output, "sub2api_placeholder")
					require.Contains(t, output, `"content":" "`, "the actual Chat placeholder is one U+0020 character")
				}
				if blockedAfterSpace {
					require.Contains(t, output, "error")
					require.Empty(t, capture.commands, "an HTTP error with no upstream usage must not be charged")
					require.Empty(t, capture.logs)
					return
				}
				require.Contains(t, output, "actual answer")
				require.Len(t, capture.commands, 1, "the handler must settle observed usage exactly once")
				require.Len(t, capture.logs, 1, "failed pre-output attempts must not add a second usage record")
				command, usageLog := <-capture.commands, <-capture.logs
				require.Equal(t, wantAccountIDs[len(wantAccountIDs)-1], command.AccountID)
				require.Equal(t, 900, command.InputTokens)
				require.Equal(t, 13, command.OutputTokens)
				require.Equal(t, 100, command.CacheReadTokens)
				require.Equal(t, 200, command.CacheCreationTokens)
				require.Equal(t, service.QuantizeUsageBillingAmount(expectedCost), command.BalanceCost)
				require.Zero(t, command.SubscriptionCost)
				require.InDelta(t, expectedCost, usageLog.ActualCost, 1e-12)
				require.InDelta(t, expectedCost, usageLog.TotalCost, 1e-12)
				require.NotNil(t, usageLog.FirstResponseMs, "disabled heartbeat still measures real downstream output")
				if scenario == "interrupted_after_usage" {
					require.Contains(t, output, "error", "the handler must expose the interrupted stream instead of claiming completion")
				} else {
					require.Contains(t, output, "[DONE]")
				}
			})
		}
	}
}

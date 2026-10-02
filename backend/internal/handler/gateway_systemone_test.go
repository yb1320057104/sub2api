package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const validSystemOneHandlerBody = `{"model":"jev-latest","state":"sample","questions":{"q":{"type":"noul","instructions":"Evaluate"}}}`

func newSystemOneHandlerContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func TestSystemOneRequiresAuthentication(t *testing.T) {
	c, recorder := newSystemOneHandlerContext(validSystemOneHandlerBody)
	(&GatewayHandler{}).SystemOne(c)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), "authentication_error")
}

func TestSystemOneRejectsNonTypeSafeGroupBeforeScheduling(t *testing.T) {
	c, recorder := newSystemOneHandlerContext(validSystemOneHandlerBody)
	groupID := int64(3)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 4, UserID: 5, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI}})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 5, Concurrency: 1})

	(&GatewayHandler{cfg: &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1 << 20}}}).SystemOne(c)
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Contains(t, recorder.Body.String(), "only available for TypeSafe")
}

func newTypeSafeGroupContext(t *testing.T, path, body, groupPlatform string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	c, recorder := newSystemOneHandlerContext(body)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	groupID := int64(9)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 4, UserID: 5, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: groupPlatform}})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 5, Concurrency: 1})
	return c, recorder
}

func TestRejectSystemOneOnlyPlatform(t *testing.T) {
	write := func(c *gin.Context, status int, errType, message string) {
		c.JSON(status, gin.H{"type": errType, "message": message})
	}

	c, recorder := newTypeSafeGroupContext(t, "/v1/messages", `{}`, service.PlatformTypeSafe)
	require.True(t, rejectSystemOneOnlyPlatform(c, mustAPIKey(t, c), write))
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Contains(t, recorder.Body.String(), systemOneOnlyPlatformMessage)

	c, _ = newTypeSafeGroupContext(t, "/v1/messages", `{}`, service.PlatformComposite)
	require.False(t, rejectSystemOneOnlyPlatform(c, mustAPIKey(t, c), write))
	c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), service.PlatformTypeSafe))
	require.True(t, rejectSystemOneOnlyPlatform(c, mustAPIKey(t, c), write))

	c, _ = newTypeSafeGroupContext(t, "/v1/messages", `{}`, service.PlatformAnthropic)
	require.False(t, rejectSystemOneOnlyPlatform(c, mustAPIKey(t, c), write))

	c, _ = newTypeSafeGroupContext(t, "/antigravity/v1/messages", `{}`, service.PlatformTypeSafe)
	c.Set(string(middleware2.ContextKeyForcePlatform), service.PlatformAntigravity)
	require.False(t, rejectSystemOneOnlyPlatform(c, mustAPIKey(t, c), write))
}

func mustAPIKey(t *testing.T, c *gin.Context) *service.APIKey {
	t.Helper()
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	require.True(t, ok)
	return apiKey
}

func TestTypeSafeGroupsRejectNonSystemOneProtocolsBeforeScheduling(t *testing.T) {
	h := &GatewayHandler{
		cfg:            &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1 << 20}},
		gatewayService: &service.GatewayService{},
	}
	for _, tc := range []struct {
		name    string
		path    string
		body    string
		handler func(*gin.Context)
	}{
		{"messages", "/v1/messages", `{"model":"jev-latest","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`, h.Messages},
		{"count_tokens", "/v1/messages/count_tokens", `{"model":"jev-latest","messages":[{"role":"user","content":"hi"}]}`, h.CountTokens},
		{"chat_completions", "/v1/chat/completions", `{"model":"jev-latest","messages":[{"role":"user","content":"hi"}]}`, h.ChatCompletions},
		{"responses", "/v1/responses", `{"model":"jev-latest","input":"hi"}`, h.Responses},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := newTypeSafeGroupContext(t, tc.path, tc.body, service.PlatformTypeSafe)
			tc.handler(c)
			require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), systemOneOnlyPlatformMessage)
		})
	}
}

type systemOneUsageCapture struct {
	service.UsageLogRepository
	service.UsageBillingRepository
	logs     chan *service.UsageLog
	commands chan *service.UsageBillingCommand
}

func (r *systemOneUsageCapture) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.logs <- log
	return true, nil
}

func (r *systemOneUsageCapture) Apply(_ context.Context, command *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.commands <- command
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

func TestPKSystemOneUsageSnapshotsRequestBeforeAsyncWorker(t *testing.T) {
	pool := newUsageRecordTestPool(t)
	workerStarted := make(chan struct{})
	releaseWorker := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(releaseWorker) }) }
	// Cleanup runs before pool.Stop, including if an assertion fails while the
	// worker is deliberately blocked.
	t.Cleanup(unblock)
	require.False(t, pool.Submit(func(context.Context) {
		close(workerStarted)
		<-releaseWorker
	}).Dropped())
	select {
	case <-workerStarted:
	case <-time.After(time.Second):
		t.Fatal("usage worker did not start")
	}

	capture := &systemOneUsageCapture{
		logs:     make(chan *service.UsageLog, 1),
		commands: make(chan *service.UsageBillingCommand, 1),
	}
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	billing := service.NewBillingService(cfg, nil)
	gateway := service.NewGatewayService(
		nil, nil, capture, capture, nil, nil, nil, nil, cfg, nil, nil, billing, nil, nil,
		nil, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	h := &GatewayHandler{gatewayService: gateway, usageRecordWorkerPool: pool}
	c, _ := newSystemOneHandlerContext(validSystemOneHandlerBody)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.RequestedPublicModel, "jev-latest"))
	groupID := int64(3)
	apiKey := &service.APIKey{
		ID: 4, UserID: 5, User: &service.User{ID: 5}, GroupID: &groupID,
		Group: &service.Group{ID: groupID, Platform: service.PlatformTypeSafe, RateMultiplier: 1},
	}
	account := &service.Account{ID: 6, Platform: service.PlatformTypeSafe, Type: service.AccountTypeAPIKey}
	mapping := service.ChannelMappingResult{ChannelID: 7, BillingModelSource: service.BillingModelSourceRequested}
	result := &service.SystemOneForwardResult{ForwardResult: service.ForwardResult{
		RequestID: "systemone-original-request", Model: "jev-latest",
		Usage: service.ClaudeUsage{InputTokens: 100, OutputTokens: 7},
	}}
	h.recordSystemOneUsage(c, apiKey, account, nil, mapping, "jev-latest", []byte(validSystemOneHandlerBody), result, 5, time.Now())
	require.Equal(t, uint64(1), pool.Stats().WaitingTasks, "billing must still be queued before Gin request reuse")

	// Model a later request borrowing the same Gin context. Its known, more
	// expensive requested model must not change the original request's bill.
	nextRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request = nextRequest.WithContext(context.WithValue(nextRequest.Context(), ctxkey.RequestedPublicModel, "gpt-4o"))
	unblock()

	select {
	case usage := <-capture.logs:
		require.Equal(t, "jev-latest", usage.RequestedModel)
		require.NotNil(t, usage.ChannelID)
		require.Equal(t, int64(7), *usage.ChannelID)
		require.NotNil(t, usage.InboundEndpoint)
		require.Equal(t, "/v1/systemone", *usage.InboundEndpoint)
		require.Equal(t, 100, usage.InputTokens)
		require.Equal(t, 7, usage.OutputTokens)
		require.InDelta(t, 100*0.042/1_000_000, usage.ActualCost, 1e-12)
	case <-time.After(time.Second):
		t.Fatal("System One usage was not recorded")
	}
	select {
	case command := <-capture.commands:
		require.Equal(t, "systemone-original-request", command.RequestID)
		require.Equal(t, "jev-latest", command.Model)
		require.InDelta(t, 100*0.042/1_000_000, command.BalanceCost, 1e-12)
	default:
		t.Fatal("System One billing was not applied before its usage record")
	}
}

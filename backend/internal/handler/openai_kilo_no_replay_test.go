package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type kiloNoReplayHTTPPeer struct {
	service.HTTPUpstream
	scenario   string
	accountIDs []int64
	posts      int
	beforeSend func()
}

func (u *kiloNoReplayHTTPPeer) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.accountIDs = append(u.accountIDs, accountID)
	if len(u.accountIDs) == 1 && u.scenario == "before_post" {
		return nil, service.ErrProxyCapacity
	}
	// Model the same write boundary that net/http reports in production; a
	// disconnect after headers are sent cannot establish non-execution.
	if trace := httptrace.ContextClientTrace(req.Context()); trace != nil {
		if trace.WroteHeaders != nil {
			trace.WroteHeaders()
		}
		if trace.WroteRequest != nil {
			trace.WroteRequest(httptrace.WroteRequestInfo{})
		}
	}
	u.posts++
	if u.beforeSend != nil {
		u.beforeSend()
	}
	if len(u.accountIDs) == 1 && u.scenario == "after_post" {
		return nil, errors.New("synthetic disconnect after request write")
	}
	wire := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_kilo_handler\",\"model\":\"gpt-5.4\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"safe answer\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_kilo_handler\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"gpt-5.4\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"safe answer\"}]}],\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n"
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}, nil
}

func runKiloNoReplayHandler(t *testing.T, protocol, scenario string, managed, committed bool) (*kiloNoReplayHTTPPeer, *httptest.ResponseRecorder) {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	groupID := int64(9640)
	accounts := make([]service.Account, 2)
	for i := range accounts {
		accounts[i] = service.Account{
			ID: int64(9641 + i), Name: "kilo-no-replay", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Priority: i + 1, Concurrency: 1, GroupIDs: []int64{groupID},
			Credentials: map[string]any{
				"api_key": "synthetic", "base_url": service.KiloGatewayBaseURL,
				service.KiloGatewayMachineIDCredential: "0fc2c56e-bbd5-49de-a380-96b76b7f5495",
				"pool_mode":                            true, "pool_mode_retry_count": float64(1),
				"pool_mode_retry_status_codes": []any{float64(http.StatusBadGateway)},
			},
			Extra: map[string]any{
				service.AccountExtraKiloBridgeEnabled: true,
				service.KiloGatewayManagedExtra:       managed,
				service.KiloGatewayProfileExtra:       service.KiloGatewayCLIProfile,
				openai_compat.ExtraKeyResponsesMode:   string(openai_compat.ResponsesSupportModeForceResponses),
			},
		}
	}
	peer := &kiloNoReplayHTTPPeer{scenario: scenario}
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewOpenAIGatewayService(
		&openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}, nil, nil, nil,
		nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil,
		billingCache, peer, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	path, body := "/v1/responses", `{"model":"gpt-5.4","input":"hello","stream":false}`
	switch protocol {
	case "chat":
		path, body = "/v1/chat/completions", `{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`
	case "messages":
		path, body = "/v1/messages", `{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"max_tokens":128,"stream":false}`
	}
	if committed {
		body = strings.Replace(body, `"stream":false`, `"stream":true`, 1)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	apiKey := &service.APIKey{
		ID: 9643, UserID: 9644, GroupID: &groupID, AllowLongContext: true,
		User: &service.User{ID: 9644, Status: service.StatusActive, Balance: 100},
		Group: &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformOpenAI,
			Status: service.StatusActive, RateMultiplier: 1, AllowMessagesDispatch: true},
	}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 0})
	if committed {
		peer.beforeSend = func() {
			c.Header("Content-Type", "text/event-stream")
			_, err := c.Writer.Write([]byte(": downstream already started\n\n"))
			require.NoError(t, err)
			c.Writer.Flush()
		}
	}
	switch protocol {
	case "chat":
		h.ChatCompletions(c)
	case "messages":
		h.Messages(c)
	default:
		h.Responses(c)
	}
	return peer, rec
}

func TestKiloHandlerSubmittedPOSTNeverReplays(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"responses", "chat", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			peer, rec := runKiloNoReplayHandler(t, protocol, "after_post", true, false)
			require.Equal(t, []int64{9641}, peer.accountIDs, "neither same-account nor cross-account replay is permitted")
			require.Equal(t, 1, peer.posts)
			require.Equal(t, http.StatusBadGateway, rec.Code)
			require.True(t, gjson.ValidBytes(rec.Body.Bytes()), "no stream bytes were sent, so return an HTTP JSON error: %s", rec.Body.String())
			require.NotEmpty(t, gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
			require.NotContains(t, rec.Body.String(), "event:")
		})
	}
}

func TestKiloHandlerSubmittedPOSTReturnsCommittedStreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"responses", "chat", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			peer, rec := runKiloNoReplayHandler(t, protocol, "after_post", true, true)
			require.Equal(t, []int64{9641}, peer.accountIDs)
			require.Equal(t, 1, peer.posts)
			require.Equal(t, http.StatusOK, rec.Code, "the response was already committed before the failure")
			require.Contains(t, rec.Body.String(), ": downstream already started")
			if protocol == "responses" {
				require.Contains(t, rec.Body.String(), "response.failed")
			} else {
				require.Contains(t, rec.Body.String(), "event: error")
			}
		})
	}
}

func TestKiloHandlerPreSubmitFailureNeverFallsBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"responses", "chat", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			peer, rec := runKiloNoReplayHandler(t, protocol, "before_post", true, false)
			require.Equal(t, []int64{9641}, peer.accountIDs, "an unavailable Kilo route cannot switch to another account family")
			require.Equal(t, 0, peer.posts)
			require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest)
			require.NotContains(t, rec.Body.String(), "safe answer")
		})
	}
}

func TestKiloHandlerLegacyBridgeKeepsFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"responses", "chat", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			peer, rec := runKiloNoReplayHandler(t, protocol, "after_post", false, false)
			require.Equal(t, []int64{9641, 9642}, peer.accountIDs)
			require.Equal(t, 2, peer.posts)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Contains(t, rec.Body.String(), "safe answer")
		})
	}
}

func TestKiloHandlerSubmissionEvidence(t *testing.T) {
	ctx, state := observeManagedKiloSubmission(context.Background(), &service.Account{
		Platform: service.PlatformOpenAI, Extra: map[string]any{
			service.AccountExtraKiloBridgeEnabled: true,
			service.KiloGatewayManagedExtra:       true,
			service.KiloGatewayProfileExtra:       service.KiloGatewayCLIProfile,
		},
	})
	require.NotNil(t, state)
	require.True(t, state.blocksReplay(nil, &service.UpstreamFailoverError{}), "selecting managed Kilo forbids fallback even before submission")
	require.False(t, state.written.Load(), "blocking fallback is a policy, not fabricated wire evidence")
	require.True(t, state.blocksReplay(nil, &service.UpstreamFailoverError{HeaderTimeoutSubmitted: true}))
	trace := httptrace.ContextClientTrace(ctx)
	require.NotNil(t, trace)
	trace.WroteRequest(httptrace.WroteRequestInfo{Err: io.ErrUnexpectedEOF})
	require.True(t, state.blocksReplay(nil, &service.UpstreamFailoverError{}), "a partial request write cannot prove non-execution")
	_, ordinary := observeManagedKiloSubmission(context.Background(), &service.Account{Platform: service.PlatformOpenAI})
	require.Nil(t, ordinary)
	require.False(t, ordinary.blocksReplay(nil, &service.UpstreamFailoverError{HeaderTimeoutSubmitted: true}))
}

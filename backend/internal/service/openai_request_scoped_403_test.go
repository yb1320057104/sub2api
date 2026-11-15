//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const relayContentPolicy403 = `{"error":{"message":"内容审计命中风险规则，请调整输入后重试","type":"content_policy_violation"}}`
const relayWebSocket403 = `{"error":{"message":"failed to WebSocket dial: expected handshake response status code 101 but got 403","type":"upstream_error"}}`

func TestOpenAIRequestScoped403NeverPenalizesCredential(t *testing.T) {
	bodies := map[string]string{
		"content_type": relayContentPolicy403,
		"content_code": `{"error":{"code":"content_policy_violation","type":"invalid_request_error","message":"blocked"}}`,
		"websocket":    relayWebSocket403,
		"websocket_json_then_sse": relayWebSocket403 + "event: response.failed\n" +
			`data: {"type":"response.failed","response":{"status":"failed","error":{"code":"upstream_error","message":"Upstream request failed"}}}` + "\n\n",
	}
	policies := map[string]map[string]any{
		"default_without_pool_mode": nil,
		"custom_error_code": {
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusForbidden)},
		},
		"explicit_account_cooldown": {
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{map[string]any{
				"error_code": float64(http.StatusForbidden), "keywords": []any{"error"},
				"duration_minutes": float64(10), "scope": TempUnschedScopeAccount,
			}},
		},
	}
	for bodyName, body := range bodies {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for policyName, credentials := range policies {
				t.Run(bodyName+"/"+accountType+"/"+policyName, func(t *testing.T) {
					h := newOpenAI403TestHarness(t, 601, 1, 2, 3, 4, 5)
					h.account.Type = accountType
					h.account.Credentials = credentials
					gateway := &OpenAIGatewayService{rateLimitService: h.svc}
					for range openAI403DisableThreshold + 1 {
						require.Equal(t, ErrorPolicySkipped, h.svc.CheckErrorPolicy(
							context.Background(), h.account, http.StatusForbidden, []byte(body), "test-model",
						))
						require.False(t, h.handle(body))
						require.False(t, gateway.handleOpenAIAccountUpstreamError(
							context.Background(), h.account, http.StatusForbidden, nil, []byte(body), "test-model",
						))
					}
					h.requireNoAccountPenalty(t)
					require.Zero(t, h.repo.rateLimitedCalls)
					require.False(t, gateway.isOpenAIAccountRuntimeBlocked(h.account))
					require.False(t, gateway.isOpenAIAccountModelRuntimeBlocked(h.account, "test-model"))
					// Ignored relay errors must not spend a real account denial's
					// strike budget or prevent it from receiving its first strike.
					h.account.Credentials = nil
					require.True(t, h.handle(`{"error":{"code":"account_deactivated","message":"Account disabled"}}`))
					require.Equal(t, 1, h.counter.increments)
					require.Equal(t, 1, h.repo.tempCalls)
					require.Zero(t, h.repo.setErrorCalls)
				})
			}
		}
	}
}

func TestOpenAIRequestScoped403PreservesRequestFailover(t *testing.T) {
	for _, body := range []string{relayContentPolicy403, relayWebSocket403} {
		h := newOpenAI403TestHarness(t, 602, 1)
		h.account.Type = AccountTypeAPIKey
		gateway := &OpenAIGatewayService{rateLimitService: h.svc, cfg: &config.Config{}}
		ctx, _ := newOpenAIUpstreamErrorTestContext(t)
		failover := gateway.failoverOpenAIUpstreamHTTPError(
			context.Background(), ctx, h.account,
			newOpenAIUpstreamErrorResponse(http.StatusForbidden, body), []byte(body), "", "test-model",
		)
		require.NotNil(t, failover, "only credential penalties change, not the current request's failover")
		require.Equal(t, http.StatusForbidden, failover.StatusCode)
		require.Equal(t, body, string(failover.ResponseBody))
		require.False(t, failover.RetryableOnSameAccount)
		h.requireNoAccountPenalty(t)
		require.False(t, gateway.isOpenAIAccountRuntimeBlocked(h.account))
	}
}

func TestOpenAIRequestScoped403DoesNotTrustEchoedOrCredentialErrors(t *testing.T) {
	for name, body := range map[string]string{
		"echoed_content_code":               `{"error":{"code":"account_deactivated","message":"Account disabled"},"input":{"error":{"type":"content_policy_violation"}}}`,
		"content_code_in_message":           `{"error":{"message":"workspace forbidden; input mentioned content_policy_violation"}}`,
		"content_type_with_credential_code": `{"error":{"type":"content_policy_violation","code":"account_deactivated","message":"Account disabled"}}`,
		"wrong_websocket_error_type":        `{"error":{"type":"permission_error","message":"failed to WebSocket dial: expected handshake response status code 101 but got 403"}}`,
		"websocket_with_credential_code":    `{"error":{"type":"upstream_error","code":"account_deactivated","message":"failed to WebSocket dial: expected handshake response status code 101 but got 403"}}`,
		"unrelated_websocket_error":         `{"error":{"type":"upstream_error","message":"WebSocket account access revoked"}}`,
		"error_in_later_event":              `{"error":{"message":"Account disabled"}}` + "\nevent: error\ndata: " + relayContentPolicy403,
		"malformed_json":                    `{"error":{"type":"content_policy_violation"}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newOpenAI403TestHarness(t, 603, int64(openAI403DisableThreshold))
			h.account.Type = AccountTypeAPIKey
			require.True(t, h.handle(body))
			require.Equal(t, 1, h.counter.increments)
			require.Equal(t, 1, h.repo.setErrorCalls)
		})
	}
}

func TestOpenAIRequestScoped403KeepsOtherPlatformsAndStatuses(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, status := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusTooManyRequests, http.StatusBadGateway} {
		require.False(t, isOpenAIRequestScoped403(account, status, []byte(relayContentPolicy403)))
		require.False(t, isOpenAIRequestScoped403(account, status, []byte(relayWebSocket403)))
	}
	for _, platform := range []string{PlatformAnthropic, PlatformGemini, PlatformGrok} {
		h := newOpenAI403TestHarness(t, 604, 1)
		h.account.Platform = platform
		h.account.Type = AccountTypeAPIKey
		require.True(t, h.handle(relayContentPolicy403))
		require.Equal(t, 1, h.repo.setErrorCalls)
	}
	require.False(t, isOpenAIRequestScoped403(nil, http.StatusForbidden, []byte(relayContentPolicy403)))
}

func TestOpenAIPoolRelayWebSocketHandshake502KeepsPoolAvailable(t *testing.T) {
	for _, tc := range []struct {
		name      string
		codes     []any
		wantRetry bool
	}{
		{name: "default", wantRetry: true},
		{name: "explicit_empty", codes: []any{}},
		{name: "explicit_inner_403_only", codes: []any{float64(http.StatusForbidden)}},
		{name: "explicit_outer_502", codes: []any{float64(http.StatusBadGateway)}, wantRetry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newOpenAI403TestHarness(t, 605, 1, 2, 3, 4, 5)
			h.account.Type = AccountTypeAPIKey
			h.account.Credentials = map[string]any{
				"pool_mode":                  true,
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{map[string]any{
					"error_code": float64(http.StatusBadGateway), "keywords": []any{"error"},
					"duration_minutes": float64(10), "scope": TempUnschedScopeAccount,
				}},
			}
			if tc.codes != nil {
				h.account.Credentials["pool_mode_retry_status_codes"] = tc.codes
			}
			gateway := &OpenAIGatewayService{rateLimitService: h.svc, cfg: &config.Config{}}
			for range 4 {
				require.Equal(t, ErrorPolicySkipped, h.svc.CheckErrorPolicy(
					context.Background(), h.account, http.StatusBadGateway, []byte(relayWebSocket403), "test-model",
				))
				require.False(t, h.svc.HandleUpstreamError(
					context.Background(), h.account, http.StatusBadGateway, nil, []byte(relayWebSocket403), "test-model",
				))
				failover := gateway.failoverOpenAIUpstreamHTTPError(context.Background(), nil, h.account,
					newOpenAIUpstreamErrorResponse(http.StatusBadGateway, relayWebSocket403), []byte(relayWebSocket403), "", "test-model")
				require.NotNil(t, failover)
				require.Equal(t, tc.wantRetry, failover.RetryableOnSameAccount)
				require.True(t, failover.RequestScopedTransient)
				require.Equal(t, openAIPoolRelayWebSocketHandshakeReason, failover.Reason)
				require.False(t, failover.SafeToFailoverAfterWrite)
				require.False(t, failover.IsCredentialFailure())
				require.False(t, failover.IsOpenAICapacityShed())
			}
			h.requireNoAccountPenalty(t)
			require.Zero(t, h.repo.rateLimitedCalls)
			require.False(t, gateway.isOpenAIAccountRuntimeBlocked(h.account))
			require.False(t, gateway.isOpenAIAccountModelRuntimeBlocked(h.account, "test-model"))
			// A separate, explicit disable decision must not be undone by retry classification.
			failure := gateway.newOpenAIAccountFailoverError(h.account, http.StatusBadGateway, nil, []byte(relayWebSocket403), "", true, false)
			require.False(t, failure.RetryableOnSameAccount)
			require.False(t, failure.RequestScopedTransient)
		})
	}
}

func TestOpenAIPoolRelayWebSocketHandshake502RequiresExactErrorEnvelope(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}
	for name, body := range map[string]string{
		"plain":               relayWebSocket403,
		"code_and_request_id": `{"error":{"code":"upstream_error","type":"upstream_error","message":"` + openAIRelayWebSocket403Message + `","param":null},"request_id":"synthetic"}`,
	} {
		t.Run(name, func(t *testing.T) {
			require.True(t, isOpenAIPoolRelayWebSocketHandshakeError(account, http.StatusBadGateway, []byte(body)))
		})
	}
	for name, body := range map[string]string{
		"credential_code":             `{"error":{"code":"account_deactivated","type":"upstream_error","message":"` + openAIRelayWebSocket403Message + `"}}`,
		"quota_code":                  `{"error":{"code":"insufficient_quota","type":"upstream_error","message":"` + openAIRelayWebSocket403Message + `"}}`,
		"wrong_type":                  `{"error":{"type":"permission_error","message":"` + openAIRelayWebSocket403Message + `"}}`,
		"content_policy":              relayContentPolicy403,
		"message_only":                `{"message":"` + openAIRelayWebSocket403Message + `"}`,
		"echoed_input":                `{"input":` + relayWebSocket403 + `}`,
		"output":                      `{"output":[{"type":"function_call"}],"error":{"type":"upstream_error","message":"` + openAIRelayWebSocket403Message + `"}}`,
		"terminal_response":           `{"response":` + relayWebSocket403 + `}`,
		"trailing_event":              relayWebSocket403 + "\nevent: response.failed\n",
		"trailing_object":             relayWebSocket403 + relayWebSocket403,
		"truncated":                   `{"error":{"type":"upstream_error"`,
		"different_status_in_message": `{"error":{"type":"upstream_error","message":"failed to WebSocket dial: expected handshake response status code 101 but got 401"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			require.False(t, isOpenAIPoolRelayWebSocketHandshakeError(account, http.StatusBadGateway, []byte(body)))
		})
	}
	for _, status := range []int{200, 400, 401, 403, 429, 500, 503, 504} {
		require.False(t, isOpenAIPoolRelayWebSocketHandshakeError(account, status, []byte(relayWebSocket403)))
	}
	for _, other := range []*Account{
		nil,
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: account.Credentials},
		{Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: account.Credentials},
	} {
		require.False(t, isOpenAIPoolRelayWebSocketHandshakeError(other, http.StatusBadGateway, []byte(relayWebSocket403)))
	}
}

func TestOpenAIPoolRelayWebSocketHandshake502PreservesOther5xxCooldown(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusServiceUnavailable} {
		repo := &errorPolicyRepoStub{}
		gateway := &OpenAIGatewayService{rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil)}
		account := &Account{ID: 606, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}
		for range 2 {
			require.False(t, gateway.handleOpenAIAccountUpstreamError(context.Background(), account, status, nil,
				[]byte(`{"error":{"type":"upstream_error","message":"upstream unavailable"}}`), "test-model"))
		}
		require.True(t, gateway.isOpenAIAccountModelRuntimeBlocked(account, "test-model"))
	}
}

func TestOpenAIPoolRelayWebSocketHandshake502DoesNotEnableStreamTerminalRetry(t *testing.T) {
	account := &Account{ID: 607, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}
	gateway := &OpenAIGatewayService{}
	failure := gateway.newOpenAIStreamFailoverErrorWithModel(nil, account, false, "", []byte(relayWebSocket403), openAIRelayWebSocket403Message, "test-model")
	require.False(t, failure.RetryableOnSameAccount)
	require.False(t, failure.RequestScopedTransient)
	require.False(t, failure.SafeToFailoverAfterWrite)
	require.NotEqual(t, openAIPoolRelayWebSocketHandshakeReason, failure.Reason)
}

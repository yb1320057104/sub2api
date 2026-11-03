//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func pk50MessagesChatFastPolicyFixture(
	t *testing.T,
	body []byte,
	settings *OpenAIFastPolicySettings,
	stream bool,
	observedTier string,
) (*OpenAIGatewayService, *gin.Context, *httptest.ResponseRecorder, *httpUpstreamRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	tierJSON := ""
	if observedTier != "" {
		tierJSON = fmt.Sprintf(`,"service_tier":%q`, observedTier)
	}
	contentType := "application/json"
	responseBody := `{"id":"chatcmpl_fast_policy","object":"chat.completion","model":"gpt-5.6-sol"` + tierJSON + `,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	if stream {
		contentType = "text/event-stream"
		responseBody = strings.Join([]string{
			`data: {"id":"chatcmpl_fast_policy","object":"chat.completion.chunk","model":"gpt-5.6-sol"` + tierJSON + `,"choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
			"",
			`data: {"id":"chatcmpl_fast_policy","object":"chat.completion.chunk","model":"gpt-5.6-sol","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			"",
			`data: {"id":"chatcmpl_fast_policy","object":"chat.completion.chunk","model":"gpt-5.6-sol","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
			"",
			"data: [DONE]",
			"",
			"",
		}, "\n")
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}}
	svc := newOpenAIGatewayServiceWithSettings(t, settings)
	svc.cfg = rawChatCompletionsTestConfig()
	svc.httpUpstream = upstream
	return svc, c, rec, upstream
}

func TestPK50MessagesChatFastPolicyMissingTier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		matcher        string
		action         string
		scope          string
		userID         int64
		ruleUserIDs    []int64
		modelWhitelist []string
		incomingTier   string
		wantTier       string
	}{
		{name: "default leaves omitted tier unchanged"},
		{name: "unconverted input tier does not become billable", incomingTier: OpenAIFastTierUltrafast},
		{name: "legacy all does not match omitted tier", matcher: OpenAIFastTierAny, action: OpenAIFastPolicyActionForceUltrafast},
		{name: "missing forces priority", matcher: OpenAIFastTierMissing, action: OpenAIFastPolicyActionForcePriority, wantTier: OpenAIFastTierPriority},
		{name: "missing forces ultrafast", matcher: OpenAIFastTierMissing, action: OpenAIFastPolicyActionForceUltrafast, wantTier: OpenAIFastTierUltrafast},
		{
			name: "selected user and mapped model", matcher: OpenAIFastTierMissing,
			action: OpenAIFastPolicyActionForceUltrafast, scope: BetaPolicyScopeAPIKey,
			userID: 42, ruleUserIDs: []int64{42}, modelWhitelist: []string{"gpt-5.6-sol"}, wantTier: OpenAIFastTierUltrafast,
		},
		{
			name: "other user excluded", matcher: OpenAIFastTierMissing,
			action: OpenAIFastPolicyActionForceUltrafast, userID: 43, ruleUserIDs: []int64{42},
		},
		{
			name: "unknown user excluded", matcher: OpenAIFastTierMissing,
			action: OpenAIFastPolicyActionForceUltrafast, ruleUserIDs: []int64{42},
		},
		{
			name: "other model excluded", matcher: OpenAIFastTierMissing,
			action: OpenAIFastPolicyActionForceUltrafast, userID: 42,
			ruleUserIDs: []int64{42}, modelWhitelist: []string{"gpt-5.6-luna"},
		},
		{
			name: "OAuth scope excludes API key", matcher: OpenAIFastTierMissing,
			action: OpenAIFastPolicyActionForceUltrafast, scope: BetaPolicyScopeOAuth,
		},
	}
	for _, tt := range tests {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tt.name, stream), func(t *testing.T) {
				settings := DefaultOpenAIFastPolicySettings()
				if tt.action != "" {
					scope := tt.scope
					if scope == "" {
						scope = BetaPolicyScopeAll
					}
					settings.Rules = []OpenAIFastPolicyRule{{
						ServiceTier: tt.matcher, Action: tt.action, Scope: scope,
						UserIDs: tt.ruleUserIDs, ModelWhitelist: tt.modelWhitelist, FallbackAction: BetaPolicyActionPass,
					}}
				}
				tierJSON := ""
				if tt.incomingTier != "" {
					tierJSON = fmt.Sprintf(`,"service_tier":%q`, tt.incomingTier)
				}
				body := []byte(fmt.Sprintf(`{"model":"test-model-alias","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":%t%s}`, stream, tierJSON))
				svc, c, rec, upstream := pk50MessagesChatFastPolicyFixture(t, body, settings, stream, "")
				account := forceChatMessagesFallbackAccount()
				account.Credentials["model_mapping"] = map[string]any{"test-model-alias": "gpt-5.6-sol"}
				ctx := context.WithValue(context.Background(), ctxkey.UserID, tt.userID)

				result, err := svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, http.StatusOK, rec.Code)
				require.Equal(t, "http://upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
				require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(upstream.lastBody, "model").String())
				outboundTier := gjson.GetBytes(upstream.lastBody, "service_tier")
				require.Equal(t, tt.wantTier, outboundTier.String())
				require.Equal(t, tt.wantTier, optionalStringValue(result.ServiceTier))
				if tt.wantTier == "" {
					require.False(t, outboundTier.Exists())
					require.Nil(t, result.ServiceTier)
				}
				require.Empty(t, result.UpstreamResponseServiceTier)
				require.Equal(t, stream, result.Stream)
				if stream {
					require.NotNil(t, result.FirstTokenMs)
					require.Contains(t, rec.Body.String(), "event: message_stop")
				}
				resolution := ApplyOpenAIServiceTierBillingResolution(account, result)
				require.Equal(t, tt.wantTier, resolution.Billing)
				require.False(t, resolution.Downgraded)
			})
		}
	}
}

func TestPK50MessagesChatFastPolicyGroupAndActions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name     string
		action   string
		wantTier string
	}{
		{name: "trusted group without rule", wantTier: OpenAIFastTierPriority},
		{name: "pass", action: BetaPolicyActionPass, wantTier: OpenAIFastTierPriority},
		{name: "filter", action: BetaPolicyActionFilter},
		{name: "block", action: BetaPolicyActionBlock},
		{name: "force priority", action: OpenAIFastPolicyActionForcePriority, wantTier: OpenAIFastTierPriority},
		{name: "force ultrafast", action: OpenAIFastPolicyActionForceUltrafast, wantTier: OpenAIFastTierUltrafast},
	}
	for _, tt := range tests {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tt.name, stream), func(t *testing.T) {
				settings := DefaultOpenAIFastPolicySettings()
				if tt.action != "" {
					settings.Rules = []OpenAIFastPolicyRule{{
						ServiceTier: OpenAIFastTierPriority, Action: tt.action, Scope: BetaPolicyScopeAll,
						ErrorMessage: "selected service tier is disabled",
					}}
				}
				body := []byte(fmt.Sprintf(`{"model":"gpt-5.6-sol","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":%t}`, stream))
				svc, c, rec, upstream := pk50MessagesChatFastPolicyFixture(t, body, settings, stream, "default")
				account := forceChatMessagesFallbackAccount()
				ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
					ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIFast: true,
				})

				result, err := svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
				if tt.action == BetaPolicyActionBlock {
					var blocked *OpenAIFastBlockedError
					require.True(t, errors.As(err, &blocked))
					require.Nil(t, result)
					require.Empty(t, upstream.requests, "local policy must block before upstream dispatch")
					require.Equal(t, http.StatusForbidden, rec.Code)
					require.Equal(t, "error", gjson.GetBytes(rec.Body.Bytes(), "type").String())
					require.Equal(t, "forbidden_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
					require.Equal(t, blocked.Message, gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
					require.Equal(t, "selected service tier is disabled", blocked.Message)
					require.True(t, HasOpsClientBusinessLimited(c))
					require.Equal(t, OpsClientBusinessLimitedReasonLocalPolicyDenied, OpsClientBusinessLimitedReason(c))
					return
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, http.StatusOK, rec.Code)
				outboundTier := gjson.GetBytes(upstream.lastBody, "service_tier")
				require.Equal(t, tt.wantTier, outboundTier.String())
				require.Equal(t, tt.wantTier, optionalStringValue(result.ServiceTier))
				require.Equal(t, "default", result.UpstreamResponseServiceTier)
				require.Equal(t, 3, result.Usage.InputTokens)
				require.Equal(t, 2, result.Usage.OutputTokens)
				require.Equal(t, stream, result.Stream)
				if stream {
					require.NotNil(t, result.FirstTokenMs)
					require.Contains(t, rec.Body.String(), "event: message_stop")
				}

				// Forwarding retains both observations; usage settlement alone lowers
				// an API-key request to the authoritative response tier.
				resolution := ApplyOpenAIServiceTierBillingResolution(account, result)
				require.Equal(t, tt.wantTier, resolution.Requested)
				require.Equal(t, "default", resolution.Observed)
				if tt.wantTier == "" {
					require.False(t, outboundTier.Exists())
					require.Nil(t, result.ServiceTier)
					require.Empty(t, resolution.Billing)
					require.False(t, resolution.Downgraded)
				} else {
					require.True(t, resolution.Downgraded)
					require.Equal(t, "default", resolution.Billing)
					require.Equal(t, "default", optionalStringValue(result.ServiceTier))
				}
			})
		}
	}
}

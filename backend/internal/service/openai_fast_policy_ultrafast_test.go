package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPK50OpenAIFastPolicyForceUltrafast(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, transport := range []string{"http", "ws"} {
		t.Run(transport, func(t *testing.T) {
			apply := func(ctx context.Context, rule OpenAIFastPolicyRule, model, fields string) string {
				t.Helper()
				svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{rule}})
				body := []byte(`{"type":"response.create","input":[],"model":"` + model + `"` + fields + `}`)
				var result []byte
				var err error
				if transport == "ws" {
					var blocked *OpenAIFastBlockedError
					result, blocked, err = svc.applyOpenAIFastPolicyToWSResponseCreate(ctx, account, model, body)
					require.Nil(t, blocked)
				} else {
					result, err = svc.applyOpenAIFastPolicyToBody(ctx, account, model, body)
				}
				require.NoError(t, err)
				require.Equal(t, "[]", gjson.GetBytes(result, "input").Raw)
				return gjson.GetBytes(result, "service_tier").String()
			}
			rule := OpenAIFastPolicyRule{ServiceTier: OpenAIFastTierAny, Action: OpenAIFastPolicyActionForceUltrafast, Scope: BetaPolicyScopeAll}
			for _, tier := range []string{"priority", "fast", "flex", "auto", "default", "scale", "ultrafast"} {
				require.Equal(t, "ultrafast", apply(context.Background(), rule, "gpt-5.6-sol", `,"service_tier":"`+tier+`"`), tier)
			}
			require.Empty(t, apply(context.Background(), rule, "gpt-5.6-sol", ""), "all must not start upgrading omitted tiers")
			rule.ServiceTier = OpenAIFastTierMissing
			rule.UserIDs = []int64{42}
			ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
			require.Equal(t, "ultrafast", apply(ctx, rule, "gpt-5.6-sol", ""))
			require.Empty(t, apply(context.Background(), rule, "gpt-5.6-sol", ""), "user scope must be preserved")
			rule.ModelWhitelist = []string{"gpt-5.6*"}
			rule.FallbackAction = OpenAIFastPolicyActionForcePriority
			require.Equal(t, "ultrafast", apply(ctx, rule, "gpt-5.6-sol", ""))
			require.Equal(t, "priority", apply(ctx, rule, "gpt-5.5", ""))
			rule.Action, rule.FallbackAction = BetaPolicyActionPass, OpenAIFastPolicyActionForceUltrafast
			require.Empty(t, apply(ctx, rule, "gpt-5.6-sol", ""))
			require.Equal(t, "ultrafast", apply(ctx, rule, "gpt-5.5", ""), "other-model action also supports ultrafast")
		})
	}
}

func TestPK50OpenAIFastPolicyUltrafastSettingRoundtrip(t *testing.T) {
	repo := &openAIFastPolicyRepoStub{}
	svc := NewSettingService(repo, &config.Config{})
	settings := &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
		ServiceTier: OpenAIFastTierMissing, Action: OpenAIFastPolicyActionForceUltrafast,
		Scope: BetaPolicyScopeAll, ModelWhitelist: []string{"gpt-5.6*"}, FallbackAction: OpenAIFastPolicyActionForceUltrafast,
	}}}
	require.NoError(t, svc.SetOpenAIFastPolicySettings(context.Background(), settings))
	got, err := svc.GetOpenAIFastPolicySettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, settings, got)
}

func TestPK50OpenAIFastPolicyUltrafastWSNonCreateUnchanged(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
		ServiceTier: OpenAIFastTierAny, Action: OpenAIFastPolicyActionForceUltrafast, Scope: BetaPolicyScopeAll,
	}}})
	frame := []byte(`{"type":"session.update","service_tier":"priority"}`)
	got, blocked, err := svc.applyOpenAIFastPolicyToWSResponseCreate(context.Background(), &Account{Platform: PlatformOpenAI}, "gpt-5.6-sol", frame)
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.Equal(t, frame, got)
}

func TestPK50OpenAIFastPolicyUltrafastForwardAndBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "chat/completions"} {
		for _, tierFields := range []string{"", `,"service_tier":"priority"`} {
			t.Run(endpoint+"/"+tierFields, func(t *testing.T) {
				matcher := OpenAIFastTierAny
				if tierFields == "" {
					matcher = OpenAIFastTierMissing
				}
				svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
					ServiceTier: matcher, Action: OpenAIFastPolicyActionForceUltrafast, Scope: BetaPolicyScopeAll,
				}}})
				svc.cfg = &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}
				responseBody := `{"id":"resp_1","object":"response","status":"completed","model":"gpt-5.6-sol","service_tier":"ultrafast","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`
				contentType := "application/json"
				input := `"input":"hello"`
				if endpoint == "chat/completions" {
					input = `"messages":[{"role":"user","content":"hello"}]`
					responseBody = "data: {\"type\":\"response.completed\",\"response\":" + responseBody + "}\n\ndata: [DONE]\n\n"
					contentType = "text/event-stream"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}},
					Body: io.NopCloser(strings.NewReader(responseBody)),
				}}
				svc.httpUpstream = upstream
				account := &Account{
					ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
					Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"openai_responses_supported": true},
					Status: StatusActive, Schedulable: true,
				}
				body := []byte(`{"model":"gpt-5.6-sol",` + input + `,"stream":false` + tierFields + `}`)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				var result *OpenAIForwardResult
				var err error
				if endpoint == "responses" {
					result, err = svc.Forward(context.Background(), c, account, body)
				} else {
					result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.6-sol")
				}
				require.NoError(t, err)
				require.Equal(t, "ultrafast", gjson.GetBytes(upstream.lastBody, "service_tier").String())
				require.NotNil(t, result.ServiceTier)
				require.Equal(t, "ultrafast", *result.ServiceTier)
				require.Equal(t, "ultrafast", ApplyOpenAIServiceTierBillingResolution(account, result).Billing)
				priorityMultiplier := 9.0
				require.Equal(t, 2.0, configuredServiceTierMultiplier(*result.ServiceTier, &ModelPricing{FastMultiplier: &priorityMultiplier}), "ultrafast must not silently use the priority multiplier")
			})
		}
	}
}

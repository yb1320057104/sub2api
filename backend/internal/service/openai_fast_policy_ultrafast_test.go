package service

import (
	"bytes"
	"context"
	"errors"
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
				require.Equal(t, 6.0, configuredServiceTierMultiplier(*result.ServiceTier, &ModelPricing{FastMultiplier: &priorityMultiplier}), "ultrafast must not silently use the priority multiplier")
			})
		}
	}
}

func TestGroupUltrafastPolicyHTTPAndWS(t *testing.T) {
	for _, transport := range []string{"http", "ws"} {
		for _, action := range []string{"", BetaPolicyActionFilter, BetaPolicyActionBlock, OpenAIFastPolicyActionForcePriority} {
			t.Run(transport+"/"+action, func(t *testing.T) {
				settings := DefaultOpenAIFastPolicySettings()
				if action != "" {
					settings.Rules = []OpenAIFastPolicyRule{{ServiceTier: OpenAIFastTierUltrafast, Action: action, Scope: BetaPolicyScopeAll}}
				}
				svc := newOpenAIGatewayServiceWithSettings(t, settings)
				account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
				ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
					ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIUltrafast: true, ForceOpenAIFast: true,
				})
				for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
					for _, fields := range []string{"", `,"service_tier":"priority"`, `,"service_tier":"flex"`, `,"service_tier":"ultrafast"`} {
						body := []byte(`{"type":"response.create","input":[],"previous_response_id":"resp_previous"` + fields + `}`)
						original := append([]byte(nil), body...)
						var got []byte
						var blocked *OpenAIFastBlockedError
						var err error
						if transport == "ws" {
							got, blocked, err = svc.applyOpenAIFastPolicyToWSResponseCreate(ctx, account, model, body)
						} else {
							got, err = svc.applyOpenAIFastPolicyToBody(ctx, account, model, body)
							if errors.As(err, &blocked) {
								err = nil
							}
						}
						require.NoError(t, err)
						require.Equal(t, original, body, "never mutate a retry's input")
						if action == BetaPolicyActionBlock {
							require.NotNil(t, blocked)
							continue
						}
						require.Nil(t, blocked)
						wantTier := OpenAIFastTierUltrafast
						if action == BetaPolicyActionFilter {
							wantTier = ""
						}
						if action == OpenAIFastPolicyActionForcePriority {
							wantTier = OpenAIFastTierPriority
						}
						require.Equal(t, wantTier, gjson.GetBytes(got, "service_tier").String())
						require.Equal(t, "resp_previous", gjson.GetBytes(got, "previous_response_id").String())
						require.Equal(t, "[]", gjson.GetBytes(got, "input").Raw)
					}
				}
			})
		}
	}
}

func TestGroupUltrafastPolicyScopeAndModels(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	for _, tc := range []struct {
		name, model, groupPlatform, accountPlatform, want string
		hydrated, ultra, fast                             bool
	}{
		{"astra", "gpt-6-astra", PlatformOpenAI, PlatformOpenAI, "ultrafast", true, true, false},
		{"sol", "gpt-6.1-sol", PlatformComposite, PlatformOpenAI, "ultrafast", true, true, false},
		{"unmapped alias", "gpt-6", PlatformOpenAI, PlatformOpenAI, "flex", true, true, false},
		{"unmapped effort alias", "gpt-6.1-sol-high", PlatformOpenAI, PlatformOpenAI, "flex", true, true, false},
		{"other model", "gpt-6-luna", PlatformOpenAI, PlatformOpenAI, "flex", true, true, false},
		{"similar name", "gpt-6.1-solitude", PlatformOpenAI, PlatformOpenAI, "flex", true, true, false},
		{"unhydrated", "gpt-6-astra", PlatformOpenAI, PlatformOpenAI, "flex", false, true, false},
		{"wrong group", "gpt-6-astra", PlatformAnthropic, PlatformOpenAI, "flex", true, true, false},
		{"wrong provider", "gpt-6-astra", PlatformComposite, PlatformGrok, "flex", true, true, false},
		{"disabled", "gpt-6-astra", PlatformOpenAI, PlatformOpenAI, "flex", true, false, false},
		{"both target", "gpt-6-astra", PlatformOpenAI, PlatformOpenAI, "ultrafast", true, true, true},
		{"both other", "gpt-5.5", PlatformOpenAI, PlatformOpenAI, "priority", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
				ID: 7, Platform: tc.groupPlatform, Status: StatusActive, Hydrated: tc.hydrated,
				ForceOpenAIUltrafast: tc.ultra, ForceOpenAIFast: tc.fast,
			})
			account := &Account{Platform: tc.accountPlatform, Type: AccountTypeAPIKey}
			body := []byte(`{"model":"public-alias","service_tier":"flex","input":"hi"}`)
			got, err := svc.applyOpenAIFastPolicyToBody(ctx, account, tc.model, body)
			require.NoError(t, err)
			require.Equal(t, tc.want, gjson.GetBytes(got, "service_tier").String())
			require.Equal(t, "public-alias", gjson.GetBytes(got, "model").String(), "tier selection must not rewrite models")
			frame := []byte(`{"type":"session.update","service_tier":"flex"}`)
			got, blocked, err := svc.applyOpenAIFastPolicyToWSResponseCreate(ctx, account, tc.model, frame)
			require.NoError(t, err)
			require.Nil(t, blocked)
			require.Equal(t, frame, got)
		})
	}
}

func TestGroupUltrafastForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"responses", "passthrough", "chat/completions", "messages"} {
		for _, action := range []string{"", BetaPolicyActionFilter, BetaPolicyActionBlock, OpenAIFastPolicyActionForcePriority} {
			t.Run(route+"/"+action, func(t *testing.T) {
				settings := DefaultOpenAIFastPolicySettings()
				if action != "" {
					settings.Rules = []OpenAIFastPolicyRule{{ServiceTier: OpenAIFastTierUltrafast, Action: action, Scope: BetaPolicyScopeAll}}
				}
				svc := newOpenAIGatewayServiceWithSettings(t, settings)
				svc.cfg = &config.Config{}
				responseBody := `{"id":"resp_group_ultra","object":"response","status":"completed","model":"gpt-6.1-sol","service_tier":"ultrafast","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + responseBody + "}\n\ndata: [DONE]\n\n")),
				}}
				svc.httpUpstream = upstream
				account := &Account{
					ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
					Credentials: map[string]any{"access_token": "test-oauth", "chatgpt_account_id": "test-account"},
					Extra:       map[string]any{"openai_passthrough": route == "passthrough", "openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff},
					Status:      StatusActive, Schedulable: true, RateMultiplier: f64p(1),
				}
				ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
					ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIUltrafast: true,
				})
				input := `"input":"hello","instructions":"test"`
				if route == "chat/completions" || route == "messages" {
					input = `"messages":[{"role":"user","content":"hello"}],"max_tokens":32`
				}
				body := []byte(`{"model":"gpt-6.1-sol",` + input + `,"stream":false}`)
				original := append([]byte(nil), body...)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+route, bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
				var result *OpenAIForwardResult
				var err error
				switch route {
				case "chat/completions":
					result, err = svc.ForwardAsChatCompletions(ctx, c, account, body, "", "gpt-6.1-sol")
				case "messages":
					result, err = svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
				default:
					result, err = svc.Forward(ctx, c, account, body)
				}
				require.Equal(t, original, body)
				if action == BetaPolicyActionBlock {
					var blocked *OpenAIFastBlockedError
					require.ErrorAs(t, err, &blocked)
					require.Nil(t, result)
					require.Empty(t, upstream.requests)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				wantTier := OpenAIFastTierUltrafast
				if action == BetaPolicyActionFilter {
					wantTier = ""
				}
				if action == OpenAIFastPolicyActionForcePriority {
					wantTier = OpenAIFastTierPriority
				}
				require.Equal(t, wantTier, gjson.GetBytes(upstream.lastBody, "service_tier").String())
				require.Equal(t, wantTier, optionalStringValue(result.ServiceTier))
			})
		}
	}
}

func TestGroupUltrafastForwardUsesMappedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ name, requested, mapped, tier string }{
		{"alias to supported", "my-model", "gpt-6.1-sol", "ultrafast"},
		{"supported to other", "gpt-6.1-sol", "gpt-5.5", "flex"},
		{"unmapped alias", "gpt-6", "gpt-6", "flex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
			svc.cfg = &config.Config{}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"id":"resp_mapping","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
			}}
			svc.httpUpstream = upstream
			account := &Account{
				ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "test-key", "model_mapping": map[string]any{tc.requested: tc.mapped}},
				Extra:       map[string]any{"openai_responses_supported": true}, Status: StatusActive, Schedulable: true,
			}
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
				ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIUltrafast: true,
			})
			body := []byte(`{"model":"` + tc.requested + `","input":"hello","stream":false,"service_tier":"flex"}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			result, err := svc.Forward(ctx, c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tc.mapped, gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, tc.tier, gjson.GetBytes(upstream.lastBody, "service_tier").String())
			require.Equal(t, tc.tier, optionalStringValue(result.ServiceTier))
		})
	}
}

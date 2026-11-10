package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPK50KiloMessagesReasoningContinuationRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, failure := range []struct {
		name string
		body string
	}{
		{name: "not_found", body: `{"error":{"code":"previous_response_not_found","message":"Previous response not found","type":"invalid_request_error"}}`},
		{name: "unsupported", body: `{"error":{"message":"previous_response_id is only supported on Responses WebSocket v2","type":"invalid_request_error"}}`},
	} {
		for _, effort := range []string{"ultra", "persistent"} {
			for _, stream := range []bool{false, true} {
				for _, requestedContext := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/stream=%v/requested-context=%v", failure.name, effort, stream, requestedContext), func(t *testing.T) {
						account := pk50KiloMessagesRetryAccount()
						upstream := &httpUpstreamRecorder{responses: []*http.Response{
							{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(failure.body))},
							openAICompatSSECompletedResponse("resp_recovered", "gpt-6-astra"),
						}}
						svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}, httpUpstream: upstream}
						body := []byte(fmt.Sprintf(`{"model":"client-model","max_tokens":64,"output_config":{"effort":%q},"messages":[{"role":"user","content":"first"},{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"first"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"found"},{"type":"text","text":"second"}]}],"tools":[{"name":"lookup","input_schema":{"type":null,"required":null,"properties":{"q":{"type":"string"}}}}],"stream":%t}`, effort, stream))
						clientBody := append([]byte(nil), body...)
						ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "", []ReasoningEffortMapping{
							{From: effort, To: "high", MatchType: "exact", Model: "client-model"},
							{From: "high", To: ReasoningEffortMappingDeny},
						}, "")
						if requestedContext {
							ctx = WithRequestedReasoningEffort(ctx, effort)
						}
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)).WithContext(ctx)
						svc.bindOpenAICompatSessionResponseID(ctx, c, account, "stable-cache-key", "resp_previous")

						result, err := svc.ForwardAsAnthropic(ctx, c, account, body, "stable-cache-key", "")
						require.NoError(t, err)
						require.NotNil(t, result)
						require.Equal(t, http.StatusOK, rec.Code)
						require.Equal(t, "resp_recovered", result.ResponseID)
						require.Len(t, upstream.requests, 2, "continuation recovery retries once")
						require.Equal(t, "resp_previous", gjson.GetBytes(upstream.bodies[0], "previous_response_id").String())
						require.False(t, gjson.GetBytes(upstream.bodies[1], "previous_response_id").Exists())
						require.Equal(t, failure.name == "unsupported", svc.isOpenAICompatSessionContinuationDisabled(ctx, c, account, "stable-cache-key"), "not-found deletes the old binding; unsupported disables continuation")
						for _, sent := range upstream.bodies {
							require.Equal(t, "high", gjson.GetBytes(sent, "reasoning.effort").String(), "retry must not map high a second time")
							require.Equal(t, "gpt-6-astra", gjson.GetBytes(sent, "model").String())
							require.Equal(t, "stable-cache-key", gjson.GetBytes(sent, "prompt_cache_key").String())
							require.Equal(t, "lookup", gjson.GetBytes(sent, "tools.0.name").String())
							require.Equal(t, "object", gjson.GetBytes(sent, "tools.0.parameters.type").String())
							require.False(t, gjson.GetBytes(sent, "tools.0.parameters.required").Exists())
						}
						// The recovery must replay the complete history, including tool
						// identity and output, rather than only the last turn.
						replay := upstream.bodies[1]
						require.Equal(t, int64(5), gjson.GetBytes(replay, "input.#").Int())
						require.Equal(t, "first", gjson.GetBytes(replay, "input.1.content.0.text").String())
						require.Equal(t, "custom_tool_call", gjson.GetBytes(replay, "input.2.type").String())
						require.Equal(t, "call_1", gjson.GetBytes(replay, "input.2.call_id").String())
						require.JSONEq(t, `{"q":"first"}`, gjson.GetBytes(replay, "input.2.input").String())
						require.Equal(t, "custom_tool_call_output", gjson.GetBytes(replay, "input.3.type").String())
						require.Equal(t, "call_1", gjson.GetBytes(replay, "input.3.call_id").String())
						require.Equal(t, "input_text", gjson.GetBytes(replay, "input.3.output.0.type").String())
						require.Equal(t, "found", gjson.GetBytes(replay, "input.3.output.0.text").String())
						require.Equal(t, "second", gjson.GetBytes(replay, "input.4.content.0.text").String())
						require.Equal(t, clientBody, body, "forwarding must retain the client input")
						require.NotNil(t, result.RequestedReasoningEffort)
						require.Equal(t, effort, *result.RequestedReasoningEffort)
						require.NotNil(t, result.ReasoningEffort)
						require.Equal(t, "high", *result.ReasoningEffort)
						require.Equal(t, 5, result.Usage.InputTokens)
						require.Equal(t, 2, result.Usage.OutputTokens)
						billing := openAICustomerBillingResult(account, result)
						require.Equal(t, effort, *billing.ReasoningEffort)
						require.Equal(t, result.Usage, billing.Usage)
						require.Equal(t, "high", *result.ReasoningEffort, "customer billing must not rewrite upstream metadata")
					})
				}
			}
		}
	}
}

func TestPK50KiloMessagesReasoningInitialDenialDoesNotRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, effort := range []string{"ultra", "persistent"} {
		for _, policy := range []struct {
			name     string
			max      string
			mappings []ReasoningEffortMapping
			over     string
		}{
			{name: "mapping", mappings: []ReasoningEffortMapping{{From: effort, To: ReasoningEffortMappingDeny}}},
			{name: "ceiling", max: "high", over: ReasoningEffortOverLimitDeny},
		} {
			t.Run(effort+"/"+policy.name, func(t *testing.T) {
				account := pk50KiloMessagesRetryAccount()
				upstream := &httpUpstreamRecorder{}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				body := []byte(fmt.Sprintf(`{"model":"client-model","max_tokens":64,"output_config":{"effort":%q},"messages":[{"role":"user","content":"hello"}],"stream":false}`, effort))
				ctx := WithOpenAIReasoningEffortPolicy(context.Background(), policy.max, policy.mappings, policy.over)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)).WithContext(ctx)
				svc.bindOpenAICompatSessionResponseID(ctx, c, account, "stable-cache-key", "resp_previous")

				result, err := svc.ForwardAsAnthropic(ctx, c, account, body, "stable-cache-key", "")
				require.Nil(t, result)
				require.True(t, IsReasoningEffortPolicyDenied(err))
				require.Equal(t, http.StatusForbidden, rec.Code)
				require.Empty(t, upstream.requests, "a cached continuation cannot bypass initial policy denial")
			})
		}
	}
}

func TestPK50KiloMessagesReasoningContinuationRetryFailureIsTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, message := range []string{
		"Previous response not found",
		"previous_response_id is only supported on Responses WebSocket v2",
	} {
		t.Run(message, func(t *testing.T) {
			failure := func() *http.Response {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":{"message":%q,"type":"invalid_request_error"}}`, message))),
				}
			}
			account := pk50KiloMessagesRetryAccount()
			upstream := &httpUpstreamRecorder{responses: []*http.Response{failure(), failure()}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			body := []byte(`{"model":"client-model","max_tokens":64,"output_config":{"effort":"ultra"},"messages":[{"role":"user","content":"hello"}],"stream":false}`)
			ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "", []ReasoningEffortMapping{
				{From: "ultra", To: "high"}, {From: "high", To: ReasoningEffortMappingDeny},
			}, "")
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)).WithContext(ctx)
			svc.bindOpenAICompatSessionResponseID(ctx, c, account, "stable-cache-key", "resp_previous")

			_, err := svc.ForwardAsAnthropic(ctx, c, account, body, "stable-cache-key", "")
			require.Error(t, err)
			require.False(t, IsReasoningEffortPolicyDenied(err), "the retry must report the upstream failure, not a second mapping denial")
			require.Len(t, upstream.requests, 2, "the existing continuation retry budget must not grow")
			require.False(t, gjson.GetBytes(upstream.bodies[1], "previous_response_id").Exists())
			for _, sent := range upstream.bodies {
				require.Equal(t, "high", gjson.GetBytes(sent, "reasoning.effort").String())
			}
		})
	}
}

func pk50KiloMessagesRetryAccount() *Account {
	return &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "test",
			"base_url":      "https://api.kilo.ai/api/gateway",
			"model_mapping": map[string]any{"client-model": "gpt-6-astra"},
		},
		Extra: map[string]any{AccountExtraKiloBridgeEnabled: true},
	}
}

func TestKiloMessagesFixedKeyEffortPrecedesClientAliasPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, extra, max, over, want string
		denied                       bool
	}{
		{name: "client alias is replaced", extra: `,"output_config":{"effort":"ultra"}`, want: "high"},
		{name: "omitted effort is recorded", want: "high"},
		{name: "disabled thinking still uses key policy", extra: `,"thinking":{"type":"disabled"}`, want: "high"},
		{name: "group ceiling still caps fixed effort", extra: `,"output_config":{"effort":"persistent"}`, max: "medium", over: "downgrade", want: "medium"},
		{name: "group ceiling still denies fixed effort", extra: `,"output_config":{"effort":"ultra"}`, max: "medium", over: ReasoningEffortOverLimitDeny, denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := pk50KiloMessagesRetryAccount()
			upstream := &httpUpstreamRecorder{responses: []*http.Response{openAICompatSSECompletedResponse("resp_fixed", "gpt-6-astra")}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			body := []byte(`{"model":"client-model","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"stream":false` + tc.extra + `}`)
			original := append([]byte(nil), body...)
			ctx := WithAPIKeyOpenAIReasoningEffortPolicy(context.Background(), "high", tc.max, []ReasoningEffortMapping{
				{From: "ultra", To: ReasoningEffortMappingDeny},
				{From: "persistent", To: ReasoningEffortMappingDeny},
				{From: "high", To: ReasoningEffortMappingDeny},
			}, tc.over)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)).WithContext(ctx)
			result, err := svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
			require.Equal(t, original, body)
			if tc.denied {
				require.True(t, IsReasoningEffortPolicyDenied(err))
				require.Equal(t, http.StatusForbidden, rec.Code)
				require.Empty(t, upstream.requests)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 1)
			require.Equal(t, tc.want, gjson.GetBytes(upstream.bodies[0], "reasoning.effort").String())
			require.NotNil(t, result.ReasoningEffort)
			require.Equal(t, tc.want, *result.ReasoningEffort)
			require.Equal(t, 5, result.Usage.InputTokens)
			require.Equal(t, 2, result.Usage.OutputTokens)
		})
	}
}

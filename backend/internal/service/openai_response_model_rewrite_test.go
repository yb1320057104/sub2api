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
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestMappedResponseModelPreservesOtherData(t *testing.T) {
	svc := &OpenAIGatewayService{}
	body := `{"model":"alias","text":"mapped alias","tool":{"model":"mapped","arguments":"{\"model\":\"alias\"}"}}`
	want := `{"model":"public","text":"mapped alias","tool":{"model":"mapped","arguments":"{\"model\":\"alias\"}"}}`
	require.Equal(t, want, string(svc.replaceModelInResponseBody([]byte(body), "mapped", "public")))
	require.Equal(t, "data: "+want, svc.replaceModelInSSELine("data: "+body, "mapped", "public"))
	for _, body := range []string{
		`{"model":"alias",`, `{"model":"alias"} trailing`, `{"model":null}`, `{"model":42}`, `{"model":{}}`, `{"model":[]}`, `{"model":true}`,
		`{"text":"alias","tool":{"model":"alias"}}`,
	} {
		require.Equal(t, body, string(svc.replaceModelInResponseBody([]byte(body), "mapped", "public")))
		require.Equal(t, "data: "+body, svc.replaceModelInSSELine("data: "+body, "mapped", "public"))
	}
	for _, models := range [][2]string{{"same", "same"}, {"", "public"}, {"mapped", ""}} {
		body := `{"model":"alias","response":{"model":"alias"}}`
		require.Equal(t, body, string(svc.replaceModelInResponseBody([]byte(body), models[0], models[1])))
		require.Equal(t, "data: "+body, svc.replaceModelInSSELine("data: "+body, models[0], models[1]))
	}
	require.Equal(t, `data: {"response":{"model":42}}`, svc.replaceModelInSSELine(`data: {"response":{"model":42}}`, "mapped", "public"))
	require.Equal(t, `{"model":"public"}`, string(svc.replaceModelInResponseBody([]byte(`{"model":""}`), "mapped", "public")))
}

// Exercise both streaming processors so substring fast paths cannot bypass the rewrite.
func TestMappedResponseModelForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, returned := range []string{"zhipu/glm-5.3", "glm-5.3-alias"} {
			for _, mapped := range []string{"ZHIPU/GLM-5.3", "public"} {
				for _, kind := range []string{"json", "chat", "responses"} {
					name := kind + "/" + returned + "/" + mapped
					if passthrough {
						name += "/passthrough"
					}
					t.Run(name, func(t *testing.T) {
						model := returned
						if mapped != "public" {
							model = "public"
						}
						payload := `{"model":"` + returned + `","choices":[{"delta":{"content":"keep alias","tool_calls":[{"function":{"arguments":"{\"model\":\"alias\"}"}}]}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
						want := strings.Replace(payload, `"model":"`+returned+`"`, `"model":"`+model+`"`, 1)
						contentType := "application/json"
						body := payload
						if kind == "responses" {
							payload = `{"type":"response.completed","response":{"id":"resp_1","created_at":1700000000,"model":"` + returned + `","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`
							want = strings.Replace(payload, `"model":"`+returned+`"`, `"model":"`+model+`"`, 1)
						}
						if kind != "json" {
							contentType = "text/event-stream"
							body = "data: " + payload + "\n\ndata: [DONE]\n\n"
						}
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
						resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
						svc := &OpenAIGatewayService{}
						account := &Account{ID: 1}
						var err error
						if kind == "json" {
							if passthrough {
								_, err = svc.handleNonStreamingResponsePassthrough(context.Background(), resp, c, account, "public", mapped)
							} else {
								_, err = svc.handleNonStreamingResponse(context.Background(), resp, c, account, "public", mapped)
							}
						} else {
							if passthrough {
								_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "public", mapped)
							} else {
								_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "public", mapped)
							}
						}
						require.NoError(t, err)
						require.Contains(t, rec.Body.String(), want)
						require.Equal(t, returned, observedUpstreamResponseModel(c))
					})
				}
			}
		}
	}
}

func TestPKKiloBridgeResponsesExposeRequestedEffort(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, mode := range []string{"json", "stream", "sse-to-json"} {
			for _, effort := range []string{"ultra", "persistent", "max"} {
				t.Run(fmt.Sprintf("passthrough=%v/%s/%s", passthrough, mode, effort), func(t *testing.T) {
					response := `{"id":"resp_kilo_effort","object":"response","status":"completed","model":"client-model","reasoning":{"effort":"max","summary":"auto"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"max remains generated text"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`
					contentType := "application/json"
					wire := response
					if mode != "json" {
						contentType = "text/event-stream"
						created := `{"type":"response.created","response":{"id":"resp_kilo_effort","reasoning":{"effort":"max"}}}`
						if effort != "max" {
							// Exercise multiline restoration with cache conversion disabled.
							created = "{\"type\":\"response.created\",\n" +
								"data: \"response\":{\"id\":\"resp_kilo_effort\",\"reasoning\":{\"effort\":\"max\"}}}"
						}
						wire = "event: response.created\ndata: " + created + "\n\n" +
							"data: {\"type\":\"response.output_text.delta\",\"delta\":\"max remains generated text\"}\n\n" +
							"data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(wire))}}
					account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://api.kilo.ai/api/gateway"}, Extra: map[string]any{AccountExtraKiloBridgeEnabled: true, "openai_passthrough": passthrough}}
					svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}, httpUpstream: upstream}
					body := []byte(fmt.Sprintf(`{"model":"client-model","stream":%v,"reasoning":{"effort":%q},"input":"hello"}`, mode == "stream", effort))
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
					SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
					result, err := svc.Forward(context.Background(), c, account, body)
					require.NoError(t, err)
					require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
					require.Equal(t, "max", *result.ReasoningEffort)
					require.Equal(t, effort, *result.RequestedReasoningEffort)
					require.Equal(t, 3, result.Usage.InputTokens)
					require.Equal(t, 2, result.Usage.OutputTokens)
					require.Contains(t, rec.Body.String(), "max remains generated text")
					if mode == "stream" {
						metadataEvents := 0
						for _, line := range strings.Split(rec.Body.String(), "\n") {
							data, ok := extractOpenAISSEDataLine(line)
							if !ok {
								continue
							}
							value := gjson.Get(data, "response.reasoning.effort")
							if value.Exists() {
								require.Equal(t, effort, value.String())
								metadataEvents++
							}
						}
						require.Equal(t, 2, metadataEvents)
					} else {
						require.Equal(t, effort, gjson.Get(rec.Body.String(), "reasoning.effort").String())
					}
				})
			}
		}
	}
}

func TestPKKiloBridgeResponseEffortMetadataScope(t *testing.T) {
	body := []byte(`{"reasoning":{"effort":"max","summary":"auto"},"reasoning_effort":"max","response":{"reasoning":{"effort":"max"},"reasoning_effort":"max"},"output":[{"type":"custom_tool_call","input":"max"}],"metadata":{"reasoning_effort":"max"},"usage":{"output_tokens":2}}`)
	account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{AccountExtraKiloBridgeEnabled: true}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	bindKiloBridgeResponseEffort(WithRequestedReasoningEffort(context.Background(), "ultra"), c, account, []byte(`{"reasoning":{"effort":"high"}}`))
	converted, err := rewriteKiloBridgeResponseJSON(c, account, body)
	require.NoError(t, err)
	for _, path := range []string{"reasoning.effort", "reasoning_effort", "response.reasoning.effort", "response.reasoning_effort"} {
		require.Equal(t, "ultra", gjson.GetBytes(converted, path).String())
	}
	require.Equal(t, "max", gjson.GetBytes(converted, "output.0.input").String())
	require.Equal(t, "max", gjson.GetBytes(converted, "metadata.reasoning_effort").String())
	require.Equal(t, "auto", gjson.GetBytes(converted, "reasoning.summary").String())
	require.Equal(t, int64(2), gjson.GetBytes(converted, "usage.output_tokens").Int())
	for _, effort := range []string{"max", "high", ""} {
		bindKiloBridgeResponseEffort(context.Background(), c, account, []byte(fmt.Sprintf(`{"reasoning":{"effort":%q}}`, effort)))
		unchanged, err := rewriteKiloBridgeResponseJSON(c, account, body)
		require.NoError(t, err)
		require.Equal(t, body, unchanged)
	}
	for _, disabled := range []*Account{nil, {Platform: PlatformOpenAI}, {Platform: PlatformAnthropic, Extra: account.Extra}} {
		bindKiloBridgeResponseEffort(context.Background(), c, disabled, []byte(`{"reasoning":{"effort":"ultra"}}`))
		unchanged, err := rewriteKiloBridgeResponseJSON(c, disabled, body)
		require.NoError(t, err)
		require.Equal(t, body, unchanged)
	}
}

func TestPKKiloBridgeErrorDetailPrivacy(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{AccountExtraKiloBridgeEnabled: true}}
	for _, tc := range []struct {
		message string
		private bool
	}{
		{"reasoning.effort 'max' is unsupported", true},
		{"ULTRA mapped to MAX", true},
		{"request routed to max", true},
		{"请求路由到max失败", true},
		{"max_tokens exceeds the model limit", false},
		{"maximum request size exceeded", false},
		{"max concurrent requests exceeded", false},
		{"invalid tools[0].parameters", false},
	} {
		t.Run(tc.message, func(t *testing.T) {
			for _, effort := range []string{"ultra", "persistent", "max"} {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				bindKiloBridgeResponseEffort(context.Background(), c, account, []byte(fmt.Sprintf(`{"reasoning":{"effort":%q}}`, effort)))
				want := tc.message
				if tc.private && effort != "max" {
					want = "Upstream request failed"
				}
				require.Equal(t, want, sanitizeKiloBridgeClientErrorMessage(c, tc.message))
				payload := []byte(fmt.Sprintf(`{"error":{"type":"invalid_request_error","code":"unsupported_value","param":"reasoning.effort","message":%q}}`, tc.message))
				converted, err := rewriteKiloBridgeResponseJSON(c, account, payload)
				require.NoError(t, err)
				require.Equal(t, want, gjson.GetBytes(converted, "error.message").String())
				require.Equal(t, "unsupported_value", gjson.GetBytes(converted, "error.code").String())
				require.Equal(t, "reasoning.effort", gjson.GetBytes(converted, "error.param").String())
				writeOpenAIUpstreamClientError(c, http.StatusBadRequest, payload, tc.message)
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Equal(t, want, gjson.Get(rec.Body.String(), "error.message").String())
				require.Equal(t, "unsupported_value", gjson.Get(rec.Body.String(), "error.code").String())
				// Internal diagnostics retain the actual provider rejection.
				require.Equal(t, tc.message, gjson.GetBytes(payload, "error.message").String())
			}
		})
	}
}

func TestPKKiloBridgeSynthesizedFailurePreservesUpstreamMetadata(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%v", passthrough), func(t *testing.T) {
			wire := `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_kilo_failure","object":"response","created_at":1700000000,"status":"in_progress"}}` + "\n\n" +
				`data: {"type":"response.output_text.delta","sequence_number":1,"delta":"partial"}` + "\n\n" +
				`data: {"type":"error","sequence_number":2,"error":{"code":"unsupported_value","message":"reasoning effort routed to max is unsupported"}}` + "\n\n"
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{AccountExtraKiloBridgeEnabled: true}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			bindKiloBridgeResponseEffort(context.Background(), c, account, []byte(`{"reasoning":{"effort":"ultra"}}`))
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
			var streamErr error
			if passthrough {
				_, streamErr = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "client-model", "client-model")
			} else {
				_, streamErr = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "client-model", "client-model")
			}
			var failures []string
			for _, line := range strings.Split(rec.Body.String(), "\n") {
				payload, ok := strings.CutPrefix(line, "data: ")
				if ok && gjson.Get(payload, "type").String() == "response.failed" {
					failures = append(failures, payload)
				}
			}
			require.Len(t, failures, 1, "upstream error must end with one valid Responses failure")
			require.EqualValues(t, 1700000000, gjson.Get(failures[0], "response.created_at").Int())
			require.EqualValues(t, 2, gjson.Get(failures[0], "sequence_number").Int())
			require.Equal(t, "resp_kilo_failure", gjson.Get(failures[0], "response.id").String())
			require.Equal(t, "Upstream request failed", gjson.Get(failures[0], "response.error.message").String())
			require.Contains(t, rec.Body.String(), `"delta":"partial"`)
			require.NotContains(t, rec.Body.String(), "routed to max")
			require.ErrorContains(t, streamErr, "routed to max", "internal diagnostics must retain the original provider error")
		})
	}
}

func TestPKKiloBridgeErrorForwardingHidesInternalEffort(t *testing.T) {
	for _, protocol := range []string{"responses", "passthrough", "chat", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", protocol, stream), func(t *testing.T) {
				status := http.StatusBadRequest
				contentType := "application/json"
				wire := `{"error":{"type":"invalid_request_error","code":"unsupported_value","param":"reasoning.effort","message":"reasoning effort routed to max is unsupported"}}`
				if stream {
					status = http.StatusOK
					contentType = "text/event-stream"
					wire = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_failure\"}}\n\n" +
						"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
						"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failure\",\"status\":\"failed\",\"error\":{\"code\":\"unsupported_value\",\"message\":\"reasoning effort routed to max is unsupported\"}}}\n\n"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(wire))}}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://api.kilo.ai/api/gateway"}, Extra: map[string]any{AccountExtraKiloBridgeEnabled: true, "openai_passthrough": protocol == "passthrough"}}
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}, httpUpstream: upstream}
				path := "/v1/responses"
				body := []byte(fmt.Sprintf(`{"model":"client-model","stream":%v,"reasoning":{"effort":"ultra"},"input":"hello"}`, stream))
				switch protocol {
				case "chat":
					path = "/v1/chat/completions"
					body = []byte(fmt.Sprintf(`{"model":"client-model","stream":%v,"reasoning_effort":"ultra","messages":[{"role":"user","content":"hello"}]}`, stream))
				case "messages":
					path = "/v1/messages"
					body = []byte(fmt.Sprintf(`{"model":"client-model","stream":%v,"max_tokens":1024,"output_config":{"effort":"ultra"},"messages":[{"role":"user","content":"hello"}]}`, stream))
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				switch protocol {
				case "chat":
					_, _ = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				case "messages":
					_, _ = svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
				default:
					_, _ = svc.Forward(context.Background(), c, account, body)
				}
				require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
				require.NotContains(t, rec.Body.String(), "routed to max")
				require.Contains(t, rec.Body.String(), "Upstream request failed")
				require.Contains(t, c.GetString(OpsUpstreamErrorMessageKey), "routed to max")
			})
		}
	}
}

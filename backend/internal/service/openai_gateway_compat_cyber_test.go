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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAuditSettlementProviderErrorCodeRequiresNegotiation(t *testing.T) {
	for _, negotiated := range []bool{false, true} {
		for _, endpoint := range []string{"chat", "messages", "responses"} {
			t.Run(endpoint+map[bool]string{false: "/native", true: "/negotiated"}[negotiated], func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				ctx := context.Background()
				if negotiated {
					ctx = context.WithValue(ctx, auditSettlementContextKey{}, &auditSettlementTracker{})
				}
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
				MarkOpsCyberPolicy(c, CyberPolicyMark{Code: "cyber_policy"})
				switch endpoint {
				case "chat":
					writeChatCompletionsError(c, 400, "invalid_request_error", "safe blocked")
				case "messages":
					writeAnthropicError(c, 400, "invalid_request_error", "safe blocked")
				case "responses":
					writeOpenAIPassthroughErrorEnvelope(c, 400, nil, "safe blocked")
				}
				require.Equal(t, negotiated, gjson.Get(recorder.Body.String(), "error.code").String() == "cyber_policy")
				for _, event := range []string{"response.failed", "error"} {
					payload := auditSettlementCyberEvent(c, []byte(`{"type":"error","error":{"message":"safe blocked"}}`), event)
					path := "error.code"
					if event == "response.failed" {
						path = "response.error.code"
					}
					require.Equal(t, negotiated, gjson.GetBytes(payload, path).String() == "cyber_policy")
					require.Contains(t, string(payload), "safe blocked")
				}
			})
		}
	}
}

// compatCyberOAuthAccount 是 compat cyber 测试共用的 OAuth 账号。
func compatCyberOAuthAccount() *Account {
	return &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}
}

// compatCyberUpstreamSSE 构造上游 responses SSE：response.created 后 response.failed(cyber_policy)。
func compatCyberUpstreamSSE() string {
	return strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_cyber","model":"gpt-5.5","status":"in_progress","output":[]}}`,
		"",
		`event: response.failed`,
		`data: {"type":"response.failed","response":{"id":"resp_cyber","object":"response","model":"gpt-5.5","status":"failed","output":[],"error":{"code":"cyber_policy","message":"flagged for cyber policy"}}}`,
		"",
	}, "\n")
}

func compatCyberUpstreamRecorder() *httpUpstreamRecorder {
	return &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_cyber"}},
		Body:       io.NopCloser(strings.NewReader(compatCyberUpstreamSSE())),
	}}
}

// C-1: chat completions 非流式客户端（buffered 路径）cyber 命中——不 failover、标记已设、
// 以 chat 错误格式回写、丢弃 result（使 handler 落入 tokens=0 免费用量行而非 RecordUsage 扣费）。
func TestForwardAsChatCompletions_BufferedCyberPolicyNoFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	svc := &OpenAIGatewayService{httpUpstream: compatCyberUpstreamRecorder()}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, compatCyberOAuthAccount(), body, "", "gpt-5.5")
	require.Error(t, err)
	require.Nil(t, result, "cyber must drop result so handler writes tokens=0 free row, not RecordUsage")
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "cyber must NOT trigger failover")
	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark, "cyber mark must be set for handler-side recording")
	require.Equal(t, "cyber_policy", mark.Code)
	require.True(t, c.Writer.Written(), "cyber error must be written to client (passthrough)")
}

// I-1: chat completions 流式客户端 cyber 命中——result 必须被丢弃（返回 nil），
// 使 handler forwardErrored 分支走 tokens=0 免费行，而非 RecordUsage(CyberBlocked) 扣费。
func TestForwardAsChatCompletions_StreamCyberPolicyDropsResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	svc := &OpenAIGatewayService{httpUpstream: compatCyberUpstreamRecorder()}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, compatCyberOAuthAccount(), body, "", "gpt-5.5")
	require.Error(t, err)
	require.Nil(t, result, "cyber must drop result so handler does not bill via RecordUsage")
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "cyber must NOT trigger failover")
	require.NotNil(t, GetOpsCyberPolicy(c), "cyber mark must be set")
	require.Contains(t, rec.Body.String(), "data: [DONE]", "stream must terminate with [DONE]")
}

// anthropic 非流式客户端（buffered 路径）cyber 命中——不 failover、标记已设、以 anthropic 错误格式回写、丢弃 result。
func TestForwardAsAnthropic_BufferedCyberPolicyNoFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","max_tokens":1024,"messages":[{"role":"user","content":"hi"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	svc := &OpenAIGatewayService{httpUpstream: compatCyberUpstreamRecorder()}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, compatCyberOAuthAccount(), body, "", "gpt-5.5")
	require.Error(t, err)
	require.Nil(t, result, "cyber must drop result so handler writes tokens=0 free row")
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "cyber must NOT trigger failover")
	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark, "cyber mark must be set")
	require.Equal(t, "cyber_policy", mark.Code)
	require.True(t, c.Writer.Written(), "anthropic cyber error must be written to client")
	require.Contains(t, rec.Body.String(), `"type":"error"`, "must use anthropic error envelope")
}

// anthropic 流式客户端 cyber 命中——不 failover、标记已设、下发 anthropic SSE error 事件、丢弃 result。
func TestForwardAsAnthropic_StreamCyberPolicyNoFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","max_tokens":1024,"messages":[{"role":"user","content":"hi"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	svc := &OpenAIGatewayService{httpUpstream: compatCyberUpstreamRecorder()}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, compatCyberOAuthAccount(), body, "", "gpt-5.5")
	require.Error(t, err)
	require.Nil(t, result, "cyber must drop result so handler does not bill via RecordUsage")
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "cyber must NOT trigger failover")
	require.NotNil(t, GetOpsCyberPolicy(c), "cyber mark must be set")
	require.Contains(t, rec.Body.String(), "event: error", "must emit anthropic SSE error event")
}

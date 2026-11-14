package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPK64ProviderToolProtocolRejectionUsesAnotherAccount(t *testing.T) {
	message := "gpt-6.1-sol requires Responses for tool calls; this account only supports Chat Completions"
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{}}
	for _, test := range []struct {
		name, message, code, typ string
		want                     bool
	}{
		{name: "explicit", message: message, typ: "invalid_request_error", want: true},
		{name: "reasoning", message: "gpt-6-luna requires Responses for tool calls with reasoning; this account only supports Chat Completions. Use reasoning_effort=none or a Responses-capable account", want: true},
		{name: "unrelated schema", message: "Invalid schema for function: " + message},
		{name: "wrong code", message: message, code: "invalid_encrypted_content"},
		{name: "wrong type", message: message, typ: "authentication_error"},
		{name: "trailing echo", message: message + " in user input"},
		{name: "unrelated model", message: "deepseek-chat requires Responses for tool calls; this account only supports Chat Completions"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"error": map[string]string{"message": test.message, "code": test.code, "type": test.typ}})
			require.NoError(t, err)
			require.Equal(t, test.want, svc.shouldFailoverOpenAIUpstreamResponse(account, 400, test.message, body))
			if !test.want {
				return
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			failure := svc.failoverOpenAIUpstreamHTTPError(context.Background(), c, account, &http.Response{StatusCode: 400, Header: http.Header{}}, body, test.message, "gpt-6.1-sol")
			require.NotNil(t, failure)
			require.False(t, c.Writer.Written(), "a fresh compatible selection must remain possible")
			require.True(t, failure.IsOpenAIToolProtocolUnavailable())
			require.False(t, failure.RetryableOnSameAccount)
			require.Equal(t, http.StatusServiceUnavailable, failure.ClientStatusCode)
			require.Empty(t, account.Extra, "a model-specific refusal must not poison the global Responses capability")
			direct := &OpenAIGatewayService{}
			require.False(t, direct.shouldFailoverOpenAIUpstreamResponse(account, 400, test.message, body), "direct callers have no scheduler to consume a failover")
		})
	}
}

func TestIsUpstreamModelNotFoundError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       []byte
		want       bool
	}{
		{
			name:       "404 model not found message",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"model not found"}}`),
			want:       true,
		},
		{
			name:       "404 model_not_found code",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"code":"model_not_found","message":"The requested model was not found"}}`),
			want:       true,
		},
		{
			name:       "404 named model without copula",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Model gpt-example not found"}}`),
			want:       true,
		},
		{
			name:       "404 single quoted named model",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"The model 'gpt-example' not found."}}`),
			want:       true,
		},
		{
			name:       "404 double quoted named model",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"The requested model \"gpt-example\" not found!"}}`),
			want:       true,
		},
		{
			name:       "404 backtick quoted namespaced model",
			statusCode: http.StatusNotFound,
			body:       []byte("{\"error\":{\"message\":\"Model `provider/gpt-example` not found.\"}}"),
			want:       true,
		},
		{
			name:       "404 unquoted namespaced model",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Model provider/gpt-example:latest not found."}}`),
			want:       true,
		},
		{
			name:       "404 plaintext named model",
			statusCode: http.StatusNotFound,
			body:       []byte(`Model gpt-example not found`),
			want:       true,
		},
		{
			name:       "404 model registry endpoint is not a model",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Model registry endpoint not found"}}`),
		},
		{
			name:       "404 model endpoint noun is not a model",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Model endpoint not found"}}`),
		},
		{
			name:       "404 quoted registry description is not a model",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Model 'registry endpoint' not found"}}`),
		},
		{
			name:       "404 absolute model URL path is not a model",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Model /v1/models not found"}}`),
		},
		{
			name:       "404 relative model URL path is not a model",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Model v1/models not found"}}`),
		},
		{
			name:       "404 model URL is not a model identifier",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Model https://example.test/v1/models not found"}}`),
		},
		{
			name:       "404 mismatched model quotes are not accepted",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Model 'gpt-example\" not found"}}`),
		},
		{
			name:       "404 named model trailing echo is not accepted",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Model gpt-example not found in user input"}}`),
		},
		{
			name:       "404 auth code overrides named model without copula",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"code":"invalid_api_key","message":"Model gpt-example not found"}}`),
		},
		{
			name:       "404 auth type overrides named model without copula",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"type":"authentication_error","message":"Model gpt-example not found"}}`),
		},
		{
			name:       "404 echoed named model outside error is ignored",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Endpoint not found"},"request":{"input":"Model gpt-example not found"}}`),
		},
		{
			name:       "404 unknown model message",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"unknown model gpt-5.4"}}`),
			want:       true,
		},
		{
			name:       "404 endpoint not found is not model specific",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"endpoint not found"}}`),
			want:       false,
		},
		{
			name:       "404 arbitrary body is not model specific",
			statusCode: http.StatusNotFound,
			body:       []byte(`404 page not found`),
			want:       false,
		},
		{
			name:       "404 relay model type",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"type":"model_not_found","message":"Model \"gpt-6-luna\" is not supported by any configured account in this group"}}`),
			want:       true,
		},
		{
			name:       "404 relay model message without code",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Model \"gpt-6-luna\" is not supported by any configured account in this group"}}`),
			want:       true,
		},
		{
			name:       "404 auth code overrides echoed model refusal",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"code":"invalid_api_key","message":"Model \"gpt-6-luna\" is not supported by any configured account in this group"}}`),
		},
		{
			name:       "404 google numeric envelope remains model specific",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"code":404,"message":"models/gemini-example is not found for API version v1beta","status":"NOT_FOUND"}}`),
			want:       true,
		},
		{
			name:       "404 top level auth code overrides model refusal",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"code":"invalid_api_key","message":"Model not found"}`),
		},
		{
			name:       "404 auth type overrides model message",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"type":"authentication_error","message":"Model not found"}}`),
		},
		{
			name:       "404 route error with echoed model",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"Endpoint not found"},"request":{"model":"gpt-6-luna","input":"model not found"}}`),
		},
		{
			name:       "404 account lookup is not a model refusal",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"error":{"message":"API key not found for model gpt-6-luna"}}`),
		},
		{
			name:       "non 404 does not match",
			statusCode: http.StatusBadRequest,
			body:       []byte(`{"error":{"message":"model not found"}}`),
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUpstreamModelNotFoundError(tt.statusCode, tt.body); got != tt.want {
				t.Fatalf("isUpstreamModelNotFoundError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPK65ModelNotFound404FailoverSkipsSameAccount(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"pool_mode": true, "pool_mode_retry_count": float64(3)}}
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{}}
	for _, body := range []string{
		`{"error":{"code":"model_not_found","message":"Model \"gpt-6-luna\" is not supported by any configured account in this group"}}`,
		`{"error":{"type":"model_not_found","message":"Model \"gpt-6-luna\" is not supported by any configured account in this group"}}`,
		`{"error":{"message":"Model gpt-example not found"}}`,
	} {
		require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(account, http.StatusNotFound, "", []byte(body)))
		direct := &OpenAIGatewayService{}
		require.False(t, direct.shouldFailoverOpenAIUpstreamResponse(account, http.StatusNotFound, "", []byte(body)))
		failure := newOpenAIUpstreamFailoverError(http.StatusNotFound, http.Header{}, []byte(body), "", true)
		require.True(t, failure.IsOpenAIModelRouteUnavailable())
		require.False(t, failure.RetryableOnSameAccount)
		require.False(t, failure.RequestScopedTransient)
		require.Equal(t, NextAccountRetry, failure.NextAccountAction)
		require.Equal(t, http.StatusServiceUnavailable, failure.ClientStatusCode)
		require.Equal(t, OpenAIModelRouteUnavailableMessage, failure.ClientMessage)
	}
	for _, body := range []string{
		`{"error":{"code":"invalid_api_key","message":"Model not found"}}`,
		`{"error":{"message":"Endpoint not found"},"model":"gpt-6-luna"}`,
		`{"error":{"message":"Model registry endpoint not found"}}`,
	} {
		require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(account, http.StatusNotFound, "", []byte(body)))
		require.False(t, newOpenAIUpstreamFailoverError(http.StatusNotFound, nil, []byte(body), "", false).IsOpenAIModelRouteUnavailable())
	}
}

func TestAntigravityModelNotFoundKeepsBare404Fallback(t *testing.T) {
	if !isModelNotFoundError(http.StatusNotFound, []byte(`endpoint not found`)) {
		t.Fatal("antigravity model-not-found helper should keep bare 404 fallback")
	}
}

func TestIsOpenAICodexPlanGatedModelError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       []byte
		want       bool
	}{
		{
			name:       "400 codex plan gated detail payload",
			statusCode: http.StatusBadRequest,
			body:       []byte(`{"detail":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account."}`),
			want:       true,
		},
		{
			name:       "400 codex plan gated error message payload",
			statusCode: http.StatusBadRequest,
			body:       []byte(`{"error":{"message":"The 'gpt-5.4' model is not supported when using Codex with a ChatGPT account."}}`),
			want:       true,
		},
		{
			name:       "400 unrelated invalid request does not match",
			statusCode: http.StatusBadRequest,
			body:       []byte(`{"error":{"message":"Invalid schema for response_format 'agentic_plan'"}}`),
			want:       false,
		},
		{
			name:       "404 with plan gated message does not match",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"detail":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account."}`),
			want:       false,
		},
		{
			name:       "400 empty body does not match",
			statusCode: http.StatusBadRequest,
			body:       nil,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOpenAICodexPlanGatedModelError(tt.statusCode, tt.body); got != tt.want {
				t.Fatalf("isOpenAICodexPlanGatedModelError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsOpenAICompatibleModelNotFound400(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "structured code", body: `{"error":{"code":"model_not_found","message":"No such deployment"}}`, want: true},
		{name: "unknown provider", body: `{"error":{"message":"Unknown provider for model claude-x"}}`, want: true},
		{name: "model not found", body: `{"error":{"message":"Model not found: claude-x"}}`, want: true},
		{name: "unknown model", body: `{"error":{"message":"unknown model: claude-x"}}`, want: true},
		{name: "model unsupported", body: `{"error":{"message":"The requested model is not supported"}}`, want: true},
		{name: "plain text compatible gateway", body: `unknown provider for model claude-x`, want: true},
		{name: "invalid parameter remains terminal", body: `{"error":{"code":"invalid_request_error","message":"Invalid value for temperature"}}`, want: false},
		{name: "structured non-matching code overrides model not found message", body: `{"error":{"code":"invalid_request_error","message":"Model not found: claude-x"}}`, want: false},
		{name: "structured non-matching error code overrides unsupported message", body: `{"error":{"code":"upstream_error","message":"The requested model is not supported"}}`, want: false},
		{name: "echoed phrase is ignored", body: `{"error":{"message":"Invalid request"},"echo":"model not found"}`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOpenAICompatibleModelNotFound400([]byte(tt.body)); got != tt.want {
				t.Fatalf("isOpenAICompatibleModelNotFound400() = %v, want %v", got, tt.want)
			}
		})
	}
}

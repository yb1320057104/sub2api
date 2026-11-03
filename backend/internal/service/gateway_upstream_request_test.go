//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAnthropicCustomRelayRequestsCarryExplicitProxyRoutingMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, countTokens := range []bool{false, true} {
		t.Run(map[bool]string{false: "messages", true: "count tokens"}[countTokens], func(t *testing.T) {
			gateway := &GatewayService{cfg: &config.Config{}}
			proxyID := int64(3)
			account := &Account{ID: 11, Platform: PlatformAnthropic, Type: AccountTypeOAuth,
				Extra:   map[string]any{"custom_base_url_enabled": true, "custom_base_url": "https://relay.example"},
				ProxyID: &proxyID, Proxy: &Proxy{ID: proxyID, Protocol: "http", Host: "legacy.invalid", Port: 1234}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			body := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello"}]}`)
			var request *http.Request
			var err error
			if countTokens {
				request, _, err = gateway.buildCountTokensRequest(t.Context(), c, account, body, "fake-token", "oauth", "claude-sonnet-4", false)
			} else {
				request, _, err = gateway.buildUpstreamRequest(t.Context(), c, account, body, "fake-token", "oauth", "claude-sonnet-4", false, false)
			}
			require.NoError(t, err)
			fallback, marked := AccountProxyRelayFromContext(request.Context())
			require.True(t, marked)
			require.Equal(t, account.Proxy.URL(), fallback)
			require.Equal(t, fallback, request.URL.Query().Get("proxy"))
			require.Equal(t, "relay.example", request.URL.Host)
		})
	}
}

// Scenario: OAuth / setup-token 凭据只能走签发方协议，API Key 与其他凭据类型不受限
func TestAnthropicMessagesCredentialAllowed(t *testing.T) {
	cases := []struct {
		platform string
		typ      string
		allowed  bool
	}{
		{PlatformAnthropic, AccountTypeOAuth, true},
		{PlatformAnthropic, AccountTypeSetupToken, true},
		{PlatformAnthropic, AccountTypeServiceAccount, true},
		{PlatformAnthropic, AccountTypeBedrock, true},
		{PlatformTypeSafe, AccountTypeOAuth, true},
		{PlatformOpenAI, AccountTypeAPIKey, true},
		{PlatformDeepseek, AccountTypeAPIKey, true},
		{PlatformOpenAI, AccountTypeOAuth, false},
		{PlatformOpenAI, AccountTypeSetupToken, false},
		{PlatformGrok, AccountTypeOAuth, false},
		{PlatformGemini, AccountTypeOAuth, false},
		{PlatformAntigravity, AccountTypeOAuth, false},
	}
	for _, tc := range cases {
		allowed := anthropicMessagesCredentialAllowed(&Account{ID: 1, Platform: tc.platform, Type: tc.typ})
		require.Equal(t, tc.allowed, allowed, "%s/%s", tc.platform, tc.typ)
	}
}

// Scenario: 回归 —— OpenAI OAuth 账号误入 Anthropic 管线时，在构建请求前即被拒绝
// （ChatGPT token 不会发往 api.anthropic.com），且只让 handler 换号、不处罚账号
func TestGatewayBuildUpstreamRequestRejectsForeignOAuthWithoutPenalty(t *testing.T) {
	account := &Account{ID: 25, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	body := []byte(`{"model":"gpt-6-astra","messages":[]}`)

	req, _, err := (&GatewayService{}).buildUpstreamRequest(context.Background(), nil, account, body, "chatgpt-token", "oauth", "gpt-6-astra", true, false)
	require.Nil(t, req)

	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.False(t, failoverErr.RetryableOnSameAccount, "same-account retry would end in temp-unschedulable")
	require.False(t, failoverErr.IsCredentialFailure())
	require.Equal(t, anthropicMessagesForeignOAuthReason, failoverErr.Reason)
}

// Scenario: count_tokens 对不匹配凭据按「平台不支持」返回 404，不取 token、不出站
func TestForwardCountTokensRejectsForeignOAuthAsUnsupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	account := &Account{ID: 25, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	parsed := &ParsedRequest{Model: "gpt-6-astra", Body: NewRequestBodyRef([]byte(`{"model":"gpt-6-astra","messages":[]}`))}

	err := (&GatewayService{}).ForwardCountTokens(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, w.Code)
}

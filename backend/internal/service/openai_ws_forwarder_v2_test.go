package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForwardOpenAIWSV2_OpaqueToolResultRetryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, failure, item string
		wantRecovery        bool
		wantDials, wantSent int32
	}{
		{"opaque read failure", "read", `{"type":"agent_message","encrypted_content":"fixture-opaque"}`, true, 1, 1},
		{"opaque incomplete terminal", "terminal", `{"type":"compaction","encrypted_content":"fixture-opaque"}`, true, 1, 1},
		{"opaque server error", "server", `{"type":"agent_message","encrypted_content":"fixture-opaque"}`, true, 1, 1},
		{"opaque handshake recovery", "handshake", `{"type":"agent_message","encrypted_content":"fixture-opaque"}`, false, 2, 1},
		{"ordinary reasoning unchanged", "read", `{"type":"reasoning","encrypted_content":"fixture-opaque","summary":[]}`, false, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dials, sent atomic.Int32
			var mu sync.Mutex
			var received [][]byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				dial := dials.Add(1)
				if tc.failure == "handshake" && dial == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				_, payload, err := conn.Read(r.Context())
				if err != nil {
					return
				}
				n := sent.Add(1)
				mu.Lock()
				received = append(received, append([]byte(nil), payload...))
				mu.Unlock()
				if n == 1 {
					switch tc.failure {
					case "read":
						return
					case "terminal":
						_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.completed"}`))
						return
					case "server":
						_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"error","error":{"type":"server_error","message":"fixture processing interrupted"}}`))
						return
					}
				}
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_fixture_retry","model":"gpt-5.5","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`))
			}))
			defer srv.Close()
			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
			pool := newOpenAIWSConnPool(cfg)
			defer pool.Close()
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
			account := &Account{ID: 5885, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "fixture-key", "base_url": srv.URL}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
			body := []byte(`{"model":"gpt-5.5","stream":false,"input":[` + tc.item + `,{"role":"user","content":"Preserve the existing ledger"},{"type":"function_call","name":"update_ledger","call_id":"fixture-once","arguments":"{}"},{"type":"function_call_output","call_id":"fixture-once","output":"already applied once"}]}`)
			c, rec := newOpenAIUpstreamErrorTestContext(t)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result, err := svc.Forward(ctx, c, account, body)
			if tc.wantRecovery {
				var recovery *OpenAIAgentRecoveryError
				require.ErrorAs(t, err, &recovery)
				require.Equal(t, "encrypted_history_recovery_unverified_owner", recovery.ClientCode())
				require.Nil(t, result)
				require.False(t, c.Writer.Written(), "the HTTP handler must serialize the typed recovery refusal")
			} else {
				require.NoError(t, err, rec.Body.String())
				require.NotNil(t, result)
				require.True(t, result.OpenAIWSMode)
			}
			require.Equal(t, tc.wantDials, dials.Load())
			require.Equal(t, tc.wantSent, sent.Load())
			mu.Lock()
			defer mu.Unlock()
			for _, payload := range received {
				require.True(t, strings.Contains(string(payload), "already applied once"))
				require.Contains(t, string(payload), "fixture-opaque")
			}
		})
	}
}

// HTTP POST /v1/responses -> forwardOpenAIWSV2 keeps the canonical outbound
// tier separate from response.completed.service_tier for usage-time billing.
func TestForwardOpenAIWSV2_KeepsOutboundAndObservedServiceTiersSeparate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		requestTier string
		stream      bool
	}{
		{name: "priority_nonstream", requestTier: "priority", stream: false},
		{name: "fast_stream", requestTier: "fast", stream: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

			cfg := &config.Config{}
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
			cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

			captureConn := &openAIWSCaptureConn{
				events: [][]byte{
					[]byte(`{"type":"response.completed","response":{"id":"resp_tier_v2","model":"gpt-5.5","status":"completed","service_tier":"default","usage":{"input_tokens":1,"output_tokens":1}}}`),
				},
			}
			captureDialer := &openAIWSCaptureDialer{conn: captureConn}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(captureDialer)

			svc := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     &httpUpstreamRecorder{},
				cache:            &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}
			account := &Account{
				ID:          5882,
				Name:        "openai-ws-v2-tier",
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra:       map[string]any{"responses_websockets_v2_enabled": true},
			}

			body := []byte(fmt.Sprintf(
				`{"model":"gpt-5.5","stream":%t,"service_tier":%q,"input":[{"type":"input_text","text":"hi"}]}`,
				tc.stream, tc.requestTier,
			))
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.OpenAIWSMode, "must take HTTP POST → forwardOpenAIWSV2, not HTTP fallback")
			require.Equal(t, tc.stream, result.Stream)
			require.Equal(t, "resp_tier_v2", result.RequestID)
			require.NotNil(t, result.ServiceTier)
			require.Equal(t, "priority", *result.ServiceTier)
			require.Equal(t, "default", result.UpstreamResponseServiceTier)
			require.Equal(t, "priority", captureConn.lastWrite["service_tier"],
				"outbound WS payload still carries the requested Fast tier")
		})
	}
}

func TestForwardOpenAIWSV2_MarksCyberPolicyForFailureEventShapes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		upstreamEvent []byte
		wantError     bool
		wantInput     int
		wantOutput    int
	}{
		{
			name:          "error_before_return",
			upstreamEvent: []byte(`{"type":"error","error":{"type":"rate_limit_error","code":"cyber_policy","message":"rate limit exceeded by cyber policy"},"usage":{"input_tokens":5,"output_tokens":1}}`),
			wantError:     true,
			wantInput:     5,
			wantOutput:    1,
		},
		{
			name:          "response_failed_terminal",
			upstreamEvent: []byte(`{"type":"response.failed","response":{"id":"resp_cyber","status":"failed","error":{"code":"cyber_policy","message":"blocked by cyber policy"},"usage":{"input_tokens":9,"output_tokens":2}}}`),
			wantInput:     9,
			wantOutput:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)

			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			captureConn := &openAIWSCaptureConn{events: [][]byte{append([]byte(nil), tt.upstreamEvent...)}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})
			svc := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     &httpUpstreamRecorder{},
				cache:            &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}
			account := &Account{
				ID: 5883, Name: "openai-ws-v2-cyber", Platform: PlatformOpenAI,
				Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra:       map[string]any{"responses_websockets_v2_enabled": true},
			}

			result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.5","stream":false,"input":"hello"}`))
			if tt.wantError {
				require.Error(t, err)
				require.Nil(t, result)
				var failoverErr *UpstreamFailoverError
				require.False(t, errors.As(err, &failoverErr))
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
			}

			mark := GetOpsCyberPolicy(c)
			require.NotNil(t, mark)
			require.Equal(t, "cyber_policy", mark.Code)
			require.Equal(t, tt.wantInput, mark.UpstreamInTok)
			require.Equal(t, tt.wantOutput, mark.UpstreamOutTok)
		})
	}
}

func TestForwardOpenAIWSV2_OverloadBeforeOutputAllowsAccountFailover(t *testing.T) {
	for _, mode := range []string{"stream", "nonstream", "after_output"} {
		stream := mode != "nonstream"
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			conn := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.created","response":{"id":"resp_test"}}`),
				[]byte(`{"type":"error","error":{"code":"server_is_overloaded","type":"service_unavailable_error","message":"overloaded","headers":{"x-retry-metadata":"NO_MORE_RETRY"}}}`),
			}}
			if mode == "after_output" {
				conn.events = append(conn.events[:1], append([][]byte{[]byte(`{"type":"response.output_text.delta","delta":"hello"}`)}, conn.events[1:]...)...)
			}
			pool := newOpenAIWSConnPool(cfg)
			defer pool.Close()
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: conn})
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
			account := &Account{ID: 5884, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
			_, err := svc.Forward(context.Background(), c, account, []byte(fmt.Sprintf(`{"model":"gpt-6-astra","stream":%t,"input":"hi"}`, stream)))
			var failover *UpstreamFailoverError
			if mode == "after_output" {
				require.Error(t, err)
				require.False(t, errors.As(err, &failover))
				require.Contains(t, rec.Body.String(), "hello")
				return
			}
			require.ErrorAs(t, err, &failover)
			require.Equal(t, http.StatusServiceUnavailable, failover.StatusCode)
			require.False(t, failover.RetryableOnSameAccount)
			require.True(t, failover.ShouldRetryNextAccount())
			require.False(t, c.Writer.Written())
			require.Empty(t, rec.Body.String())
		})
	}
}

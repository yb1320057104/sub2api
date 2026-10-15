package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// The billed turn result of a WS passthrough image generation must carry the
// image input tokens and the tool_usage.image_gen backfill, as HTTP does.
func TestOpenAIWSPassthroughTurnResultCarriesImageUsage(t *testing.T) {
	testHiddenBillingWebSocketUsage(t, OpenAIWSIngressModePassthrough, 1, false)
}

func TestPKHiddenBillingWebSocketOutputPreservesOriginalUsage(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModePassthrough, OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			testHiddenBillingWebSocketUsage(t, mode, 2, true)
		})
	}
}

func testHiddenBillingWebSocketUsage(t *testing.T, mode string, hiddenMultiplier float64, conversion bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8

	originalEvent := []byte(`{"type":"response.completed","response":{"id":"resp_ws_image","model":"gpt-5.4","output":[{"id":"ig_ws_1","type":"image_generation_call","status":"completed","result":"final-image"}],"usage":{"input_tokens":120,"output_tokens":70,"total_tokens":190,"input_tokens_details":{"image_tokens":40,"cached_tokens":10,"cache_write_tokens":20},"output_tokens_details":{"reasoning_tokens":6}},"tool_usage":{"image_gen":{"input_tokens_details":{"image_tokens":900},"output_tokens_details":{"image_tokens":55}}}}}`)
	originalCopy := append([]byte(nil), originalEvent...)
	upstreamConn := &openAIWSCaptureConn{
		events: [][]byte{originalEvent},
	}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: upstreamConn})
	t.Cleanup(pool.Close)
	svc := &OpenAIGatewayService{
		cfg:                       cfg,
		httpUpstream:              &httpUpstreamRecorder{},
		cache:                     &stubGatewayCache{},
		openaiWSResolver:          NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:             NewCodexToolCorrector(),
		openaiWSPassthroughDialer: &openAIWSCaptureDialer{conn: upstreamConn},
		openaiWSPool:              pool,
	}
	account := &Account{
		ID: 453, Name: "openai-ws-image-usage", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_mode": mode,
			AccountExtraHiddenRateMultiplier:             hiddenMultiplier,
			AccountExtraCacheConversionEnabled:           conversion,
			AccountExtraCacheConversionMultiplier:        0.5,
		},
	}

	resultCh := make(chan *OpenAIForwardResult, 1)
	hooks := &OpenAIWSIngressHooks{
		AfterTurn: func(_ int, result *OpenAIForwardResult, turnErr error) {
			if turnErr == nil && result != nil {
				resultCh <- result
			}
		},
	}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginCtx.Request = r.Clone(r.Context())
		readCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		_, firstMessage, readErr := conn.Read(readCtx)
		cancel()
		if readErr != nil {
			return
		}
		_ = svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, hooks)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	require.NoError(t, clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.4","tools":[{"type":"image_generation"}],"input":"draw"}`)))
	cancelWrite()
	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, payload, readErr := clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, readErr)
	require.Equal(t, "final-image", gjson.GetBytes(payload, "response.output.0.result").String())
	require.False(t, gjson.GetBytes(payload, "hidden_rate_multiplier").Exists())
	require.False(t, gjson.GetBytes(payload, "response.usage.hidden_rate_multiplier").Exists())
	if conversion {
		require.Equal(t, int64(220), gjson.GetBytes(payload, "response.usage.input_tokens").Int())
		require.Equal(t, int64(140), gjson.GetBytes(payload, "response.usage.output_tokens").Int())
		require.Equal(t, int64(360), gjson.GetBytes(payload, "response.usage.total_tokens").Int())
		require.Equal(t, int64(40), gjson.GetBytes(payload, "response.usage.input_tokens_details.cached_tokens").Int())
		require.Equal(t, int64(0), gjson.GetBytes(payload, "response.usage.input_tokens_details.cache_write_tokens").Int())
		require.Equal(t, int64(80), gjson.GetBytes(payload, "response.usage.input_tokens_details.image_tokens").Int())
		require.Equal(t, int64(12), gjson.GetBytes(payload, "response.usage.output_tokens_details.reasoning_tokens").Int())
		require.Equal(t, int64(1800), gjson.GetBytes(payload, "response.tool_usage.image_gen.input_tokens_details.image_tokens").Int())
		require.Equal(t, int64(110), gjson.GetBytes(payload, "response.tool_usage.image_gen.output_tokens_details.image_tokens").Int())
	} else {
		require.JSONEq(t, string(originalCopy), string(payload))
	}
	_ = clientConn.Close(coderws.StatusNormalClosure, "done")

	select {
	case result := <-resultCh:
		require.Equal(t, 120, result.Usage.InputTokens)
		require.Equal(t, 70, result.Usage.OutputTokens)
		require.Equal(t, 10, result.Usage.CacheReadInputTokens)
		require.Equal(t, 20, result.Usage.CacheCreationInputTokens)
		require.Equal(t, 40, result.Usage.ImageInputTokens, "usage image input wins over the hosted tool breakdown")
		require.Equal(t, 55, result.Usage.ImageOutputTokens, "tool_usage.image_gen fills the missing image output counter")
	case <-time.After(5 * time.Second):
		t.Fatal("passthrough turn result was not reported")
	}
	require.Equal(t, originalCopy, originalEvent, "client output rewriting must not mutate upstream usage bytes")
}

func TestPKHiddenBillingWebSocketBinaryUsageAndOpaqueFrames(t *testing.T) {
	for _, tc := range []struct {
		name       string
		multiplier *float64
		wantTokens int64
	}{
		{name: "legacy default", wantTokens: 100},
		{name: "zero", multiplier: ptrFloat64WS(0), wantTokens: 0},
		{name: "decimal", multiplier: ptrFloat64WS(0.29), wantTokens: 29},
		{name: "double", multiplier: ptrFloat64WS(2), wantTokens: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":100,"total_tokens":200}},"delta":"literal input_tokens:100"}`)
			original := append([]byte(nil), payload...)
			opaque := []byte{0, 0xff, 0x81, 0x01}
			done := make(chan struct{})
			writeErr := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					writeErr <- err
					return
				}
				defer func() { _ = conn.CloseNow() }()
				out := &openAIWSClientFrameConn{conn: conn, hiddenRateMultiplier: tc.multiplier}
				for _, frame := range []struct {
					kind coderws.MessageType
					body []byte
				}{{coderws.MessageText, payload}, {coderws.MessageBinary, payload}, {coderws.MessageBinary, opaque}} {
					if err := out.WriteFrame(r.Context(), frame.kind, frame.body); err != nil {
						writeErr <- err
						return
					}
				}
				writeErr <- nil
				<-done
			}))
			defer server.Close()
			defer close(done)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			for _, wantKind := range []coderws.MessageType{coderws.MessageText, coderws.MessageBinary} {
				kind, body, err := client.Read(ctx)
				require.NoError(t, err)
				require.Equal(t, wantKind, kind)
				require.Equal(t, tc.wantTokens, gjson.GetBytes(body, "response.usage.input_tokens").Int())
				require.Equal(t, tc.wantTokens, gjson.GetBytes(body, "response.usage.output_tokens").Int())
				require.Equal(t, 2*tc.wantTokens, gjson.GetBytes(body, "response.usage.total_tokens").Int())
				require.Equal(t, "literal input_tokens:100", gjson.GetBytes(body, "delta").String())
			}
			kind, body, err := client.Read(ctx)
			require.NoError(t, err)
			require.Equal(t, coderws.MessageBinary, kind)
			require.Equal(t, opaque, body)
			require.NoError(t, <-writeErr)
			require.Equal(t, original, payload)
		})
	}
}

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccount_BillingRateMultiplier_DefaultsToOneWhenNil(t *testing.T) {
	var a Account
	require.NoError(t, json.Unmarshal([]byte(`{"id":1,"name":"acc","status":"active"}`), &a))
	require.Nil(t, a.RateMultiplier)
	require.Equal(t, 1.0, a.BillingRateMultiplier())
}

func TestPKHiddenBillingSettings(t *testing.T) {
	defaults, err := normalizeHiddenBillingExtra(nil, nil)
	require.NoError(t, err)
	require.Nil(t, defaults, "omission must preserve the legacy extra shape")
	require.Equal(t, 1.0, (&Account{Extra: defaults}).HiddenBillingMultiplier())
	require.Equal(t, 1.0, (*Account)(nil).HiddenBillingMultiplier())
	cleared, err := normalizeHiddenBillingExtra(map[string]any{}, map[string]any{"quota_limit": 100.0})
	require.NoError(t, err)
	require.NotNil(t, cleared)
	require.Empty(t, cleared, "clearing old quota settings must retain the original empty-object behavior")

	for _, value := range []any{0, 1.0, 2.0, 0.29, 0.000001, 1000.0, json.Number("2.123456")} {
		current := map[string]any{AccountExtraHiddenRateMultiplier: value}
		updated, err := normalizeHiddenBillingExtra(map[string]any{"unrelated": true}, current)
		require.NoError(t, err)
		expected, ok := parseHiddenRateMultiplier(value)
		require.True(t, ok)
		require.Equal(t, expected, updated[AccountExtraHiddenRateMultiplier])
		require.Equal(t, value, current[AccountExtraHiddenRateMultiplier], "caller maps must not be mutated")
	}
	for _, value := range []any{nil, "2", true, -1.0, 1001.0, 0.1234567, math.NaN(), math.Inf(1)} {
		extra := map[string]any{AccountExtraHiddenRateMultiplier: value}
		require.Error(t, ValidateHiddenBillingExtra(extra))
		svc := &adminServiceImpl{}
		require.Error(t, svc.UpdateAccountExtra(context.Background(), 1, extra))
		_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: extra})
		require.Error(t, err, "invalid bulk settings must fail before touching any account")
	}

	accountRate := 7.0
	account, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, RateMultiplier: &accountRate}, map[string]any{AccountExtraHiddenRateMultiplier: 2.0})
	require.NoError(t, err)
	require.Equal(t, 2.0, account.HiddenBillingMultiplier())
	require.Equal(t, 7.0, account.BillingRateMultiplier())
}

func TestPKHiddenBillingBridgePrecisionAndIdempotency(t *testing.T) {
	account := &Account{Extra: map[string]any{AccountExtraHiddenRateMultiplier: 0.29}}
	cost := &CostBreakdown{TotalCost: 100, ActualCost: 100}
	log := &UsageLog{TotalCost: 100, ActualCost: 100, RateMultiplier: 1}
	require.NoError(t, applyHiddenBillingBridge(account, cost, log))
	require.Equal(t, 29.0, cost.ActualCost)
	require.Equal(t, 29.0, log.ActualCost)
	require.Equal(t, 100.0, log.TotalCost)
	account.Extra[AccountExtraHiddenRateMultiplier] = 3.0
	require.NoError(t, applyHiddenBillingBridge(account, cost, log))
	require.Equal(t, 29.0, cost.ActualCost, "bridge must not multiply a recorded snapshot twice")
	require.Equal(t, 0.29, log.HiddenBillingMultiplier())

	account.Extra[AccountExtraHiddenRateMultiplier] = 2.0
	cost = &CostBreakdown{TotalCost: 0.000000025, ActualCost: 0.000000025}
	log = &UsageLog{TotalCost: cost.TotalCost, RateMultiplier: 1}
	require.NoError(t, applyHiddenBillingBridge(account, cost, log))
	require.Equal(t, 0.00000005, log.ActualCost)

	cost = &CostBreakdown{ActualCost: math.MaxFloat64}
	log = &UsageLog{}
	require.Error(t, applyHiddenBillingBridge(account, cost, log))
	require.Nil(t, log.HiddenRateMultiplier, "overflow must not publish a successful snapshot")
}

func TestAccount_BillingRateMultiplier_AllowsZero(t *testing.T) {
	v := 0.0
	a := Account{RateMultiplier: &v}
	require.Equal(t, 0.0, a.BillingRateMultiplier())
}

func TestAccount_BillingRateMultiplier_NegativeFallsBackToOne(t *testing.T) {
	v := -1.0
	a := Account{RateMultiplier: &v}
	require.Equal(t, 1.0, a.BillingRateMultiplier())
}

func newPKHiddenBillingHTTPContext(t *testing.T, path, contentType string, multiplier float64, configs ...*config.Config) (*gin.Context, *httptest.ResponseRecorder, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	if contentType != "" {
		c.Writer.Header().Set("Content-Type", contentType)
	}
	finish := BeginHiddenUsageOutput(c, configs...)
	t.Cleanup(finish)
	BindHiddenUsageOutput(c, &Account{ID: 41, Extra: map[string]any{AccountExtraHiddenRateMultiplier: multiplier}})
	return c, recorder, finish
}

func writePKHiddenBillingHTTPChunks(t *testing.T, c *gin.Context, body []byte, mode string) {
	t.Helper()
	steps := []int{len(body)}
	switch mode {
	case "bytes":
		steps = []int{1}
	case "mixed":
		steps = []int{7, 1, 19, 2, 5}
	}
	for offset, chunk := 0, 0; offset < len(body); chunk++ {
		end := min(offset+steps[chunk%len(steps)], len(body))
		data := body[offset:end]
		var n int
		var err error
		if chunk%2 == 0 {
			n, err = c.Writer.Write(data)
		} else {
			n, err = c.Writer.WriteString(string(data))
		}
		require.NoError(t, err, "chunk starting at %d", offset)
		require.Equal(t, len(data), n, "Write reports caller bytes, not rewritten bytes")
		offset = end
	}
}

func pkHiddenBillingHTTPJSON(input, output, cached, reasoning int64) []byte {
	return []byte(fmt.Sprintf(` {"usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d,"input_tokens_details":{"cached_tokens":%d},"output_tokens_details":{"reasoning_tokens":%d}},"output":[{"text":"input_tokens: 10"}],"metadata":{"input_tokens":999}} `,
		input, output, input+output, cached, reasoning))
}

func TestPKHiddenBillingHTTPJSONChunksAndHeaders(t *testing.T) {
	for _, multiplier := range []float64{0, 1, 2, 0.5} {
		for _, mode := range []string{"whole", "bytes", "mixed"} {
			t.Run(fmt.Sprintf("rate_%g/%s", multiplier, mode), func(t *testing.T) {
				body := pkHiddenBillingHTTPJSON(10, 6, 4, 2)
				original := bytes.Clone(body)
				c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/v1/responses", "application/json; charset=utf-8", multiplier)
				c.Writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
				c.Writer.Header().Set("X-Request-Id", "unmodified-id")
				c.Writer.WriteHeader(http.StatusCreated)
				require.Equal(t, http.StatusCreated, c.Writer.Status())
				require.False(t, c.Writer.Written())
				writePKHiddenBillingHTTPChunks(t, c, body, mode)
				finish()
				require.Equal(t, http.StatusCreated, recorder.Code)
				require.True(t, c.Writer.Written())
				require.Equal(t, recorder.Body.Len(), c.Writer.Size())
				require.Equal(t, original, body, "upstream parsing/settlement may still read this slice")
				if multiplier == 1 {
					require.Equal(t, string(original), recorder.Body.String())
					require.Equal(t, strconv.Itoa(len(body)), recorder.Header().Get("Content-Length"))
				} else {
					want := pkHiddenBillingHTTPJSON(int64(10*multiplier), int64(6*multiplier), int64(4*multiplier), int64(2*multiplier))
					require.JSONEq(t, string(want), recorder.Body.String())
					require.Empty(t, recorder.Header().Get("Content-Length"))
				}
				require.Equal(t, "application/json; charset=utf-8", recorder.Header().Get("Content-Type"))
				require.Equal(t, "unmodified-id", recorder.Header().Get("X-Request-Id"))
				require.NotContains(t, recorder.Body.String(), "multiplier")
				require.Empty(t, c.Errors)
			})
		}
	}
}

func pkHiddenBillingHTTPSSE(eol string, input, output int64) string {
	return ": keepalive" + eol + eol +
		"event: ping" + eol + `data: {"type":"ping"}` + eol + eol +
		"id: stable-id" + eol + "event: response.completed" + eol +
		`data: {"type":"response.completed",` + eol +
		fmt.Sprintf(`data: "response":{"usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d},"output":[{"text":"raw input 10"}]}}`, input, output, input+output) + eol + eol +
		"data: [DONE]" + eol + eol +
		fmt.Sprintf(`data: {"response":{"usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}}}`, input, output, input+output)
}

func TestPKHiddenBillingHTTPOutputCopyKeepsUpstreamAccountingUsage(t *testing.T) {
	t.Run("OpenAI parses before and after output", func(t *testing.T) {
		body := pkHiddenBillingHTTPJSON(10, 6, 4, 2)
		original := bytes.Clone(body)
		before, ok := openAIUsageFromGJSON(gjson.GetBytes(body, "usage"))
		require.True(t, ok)
		require.Equal(t, 10, before.InputTokens)
		require.Equal(t, 6, before.OutputTokens)
		require.Equal(t, 4, before.CacheReadInputTokens)
		c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/v1/responses", "application/json", 2)
		c.Data(http.StatusOK, "application/json", body)
		after, ok := openAIUsageFromGJSON(gjson.GetBytes(body, "usage"))
		require.True(t, ok)
		require.Equal(t, before, after, "the original usage retained for billing must not acquire the output multiplier")
		finish()
		require.Equal(t, original, body)
		require.JSONEq(t, string(pkHiddenBillingHTTPJSON(20, 12, 8, 4)), recorder.Body.String())
	})
	t.Run("Gemini extracts accounting after c.Data", func(t *testing.T) {
		body := []byte(`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":6,"thoughtsTokenCount":2,"cachedContentTokenCount":4,"totalTokenCount":18},"candidates":[{"content":{"parts":[{"text":"promptTokenCount: 10"}]}}]}`)
		original := bytes.Clone(body)
		c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/v1beta/models/gemini-3.8-flash:generateContent", "application/json", 2)
		c.Data(http.StatusOK, "application/json", body)
		usage := extractGeminiUsage(body)
		require.NotNil(t, usage)
		require.Equal(t, 6, usage.InputTokens)
		require.Equal(t, 8, usage.OutputTokens)
		require.Equal(t, 4, usage.CacheReadInputTokens)
		finish()
		require.Equal(t, original, body)
		require.JSONEq(t, `{"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":12,"thoughtsTokenCount":4,"cachedContentTokenCount":8,"totalTokenCount":36},"candidates":[{"content":{"parts":[{"text":"promptTokenCount: 10"}]}}]}`, recorder.Body.String())
	})
}

func TestPKHiddenBillingHTTPSSEFramingAcrossWrites(t *testing.T) {
	for _, eol := range []struct{ name, value string }{{"LF", "\n"}, {"CRLF", "\r\n"}, {"CR", "\r"}} {
		for _, multiplier := range []float64{0, 1, 2, 0.5} {
			for _, mode := range []string{"whole", "bytes", "mixed"} {
				t.Run(fmt.Sprintf("%s/rate_%g/%s", eol.name, multiplier, mode), func(t *testing.T) {
					body := []byte(pkHiddenBillingHTTPSSE(eol.value, 10, 6))
					original := bytes.Clone(body)
					c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/v1/responses", "text/event-stream", multiplier)
					writePKHiddenBillingHTTPChunks(t, c, body, mode)
					c.Writer.Flush()
					require.True(t, recorder.Flushed)
					finish()
					require.Equal(t, pkHiddenBillingHTTPSSE(eol.value, int64(10*multiplier), int64(6*multiplier)), recorder.Body.String(), "framing, pings, DONE, generated text and the final unterminated frame must survive")
					require.Equal(t, original, body)
					require.Empty(t, c.Errors)
				})
			}
		}
	}
}

func TestPKHiddenBillingHTTPMissingContentTypeKeepsScalingAcrossFrames(t *testing.T) {
	for _, mode := range []string{"whole", "bytes", "mixed"} {
		t.Run("SSE/"+mode, func(t *testing.T) {
			c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/responses", "", 2)
			first := []byte("data: {\"usage\":{\"input_tokens\":2,\"output_tokens\":4,\"total_tokens\":6}}\n\n")
			second := []byte("data: {\"usage\":{\"input_tokens\":6,\"output_tokens\":8,\"total_tokens\":14}}\n\n")
			writePKHiddenBillingHTTPChunks(t, c, first, mode)
			writePKHiddenBillingHTTPChunks(t, c, second, mode)
			finish()
			require.Equal(t, "data: {\"usage\":{\"input_tokens\":4,\"output_tokens\":8,\"total_tokens\":12}}\n\ndata: {\"usage\":{\"input_tokens\":12,\"output_tokens\":16,\"total_tokens\":28}}\n\n", recorder.Body.String())
			require.Empty(t, c.Errors)
		})
		t.Run("JSON/"+mode, func(t *testing.T) {
			c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/responses", "", 2)
			writePKHiddenBillingHTTPChunks(t, c, pkHiddenBillingHTTPJSON(10, 6, 4, 2), mode)
			finish()
			require.JSONEq(t, string(pkHiddenBillingHTTPJSON(20, 12, 8, 4)), recorder.Body.String())
			require.Empty(t, c.Errors)
		})
	}
}

func TestPKHiddenBillingHTTPBufferedWriteHasRealPrefixWithoutTokenLeak(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(strconv.FormatBool(streaming), func(t *testing.T) {
			contentType := "application/json"
			first := `{"usage":{"input_tokens":12`
			last := `3,"output_tokens":2,"total_tokens":125}}`
			want := `{"usage":{"input_tokens":246,"output_tokens":4,"total_tokens":250}}`
			if streaming {
				contentType = "text/event-stream"
				first = "data: " + first
				last += "\n\n"
				want = "data: " + want + "\n\n"
			}
			c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/responses", contentType, 2)
			n, err := c.Writer.WriteString(first)
			require.NoError(t, err)
			require.Equal(t, len(first), n)
			require.True(t, c.Writer.Written(), "successful partial Write must really write a safe framing prefix")
			require.Equal(t, 1, c.Writer.Size())
			require.Equal(t, first[:1], recorder.Body.String())
			c.Writer.Flush()
			require.True(t, recorder.Flushed)
			require.NotContains(t, recorder.Body.String(), "input_tokens")
			require.NotContains(t, recorder.Body.String(), "12")
			n, err = c.Writer.Write([]byte(last))
			require.NoError(t, err)
			require.Equal(t, len(last), n)
			finish()
			require.Equal(t, want, recorder.Body.String())
			require.Empty(t, c.Errors)
		})
	}
}

func TestPKHiddenBillingHTTPOpaqueBodyAndIdentity(t *testing.T) {
	for _, multiplier := range []float64{1, 2} {
		c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/v1/tts", "audio/wav", multiplier)
		body := append([]byte{'R', 'I', 'F', 'F', 0, 255}, []byte(`{"usage":{"input_tokens":10}}`)...)
		original := bytes.Clone(body)
		c.Writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
		writePKHiddenBillingHTTPChunks(t, c, body, "mixed")
		finish()
		require.Equal(t, original, recorder.Body.Bytes())
		require.Equal(t, original, body)
		require.Equal(t, "audio/wav", recorder.Header().Get("Content-Type"))
		if multiplier == 1 {
			require.Equal(t, strconv.Itoa(len(body)), recorder.Header().Get("Content-Length"))
		}
	}
}

func TestPKHiddenBillingHTTPBindingSnapshotsAndIdempotentInstall(t *testing.T) {
	c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/v1/responses", "application/json", 7)
	originalWriter := c.Writer
	finishNested := BeginHiddenUsageOutput(c)
	require.Same(t, originalWriter, c.Writer)
	account := &Account{ID: 42, Extra: map[string]any{AccountExtraHiddenRateMultiplier: 0.5}}
	BindHiddenUsageOutput(c, account)
	account.Extra[AccountExtraHiddenRateMultiplier] = 10.0
	finishNested()
	writePKHiddenBillingHTTPChunks(t, c, pkHiddenBillingHTTPJSON(10, 6, 4, 2), "whole")
	finish()
	finish()
	require.JSONEq(t, string(pkHiddenBillingHTTPJSON(5, 3, 2, 1)), recorder.Body.String(), "latest selected account is frozen at binding, and nested install must not multiply twice")
	require.Empty(t, c.Errors)
}

func TestPKHiddenBillingHTTPOverflowAfterJSONKeepaliveHasErrorBody(t *testing.T) {
	for _, keepalive := range []bool{false, true} {
		t.Run(strconv.FormatBool(keepalive), func(t *testing.T) {
			c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/v1/images/generations", "application/json", 2)
			if keepalive {
				n, err := c.Writer.WriteString(" \n")
				require.NoError(t, err)
				require.Equal(t, 2, n)
				c.Writer.Flush()
				require.True(t, recorder.Flushed)
			}
			body := []byte(`{"usage":{"input_tokens":9223372036854775807}}`)
			original := bytes.Clone(body)
			n, err := c.Writer.Write(body)
			require.Error(t, err)
			require.Zero(t, n)
			finish()
			wantStatus := http.StatusBadGateway
			if keepalive {
				wantStatus = http.StatusOK
			}
			require.Equal(t, wantStatus, recorder.Code)
			require.JSONEq(t, `{"error":{"type":"upstream_error","message":"Upstream response usage could not be processed"}}`, recorder.Body.String())
			require.NotContains(t, recorder.Body.String(), "9223372036854775807")
			require.NotContains(t, recorder.Body.String(), "multiplier")
			require.Equal(t, original, body)
			require.NotEmpty(t, c.Errors)
			require.ErrorIs(t, HiddenUsageOutputError(c), errHiddenUsageOutput)
			streamErrors := GetOpsStreamErrors(c)
			require.NotEmpty(t, streamErrors, "invalid upstream usage remains a transform failure")
			require.Equal(t, http.StatusBadGateway, streamErrors[0].IntendedStatus)
		})
	}
}

func TestPKHiddenBillingHTTPBufferLimitAndIncompleteFrames(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{UpstreamResponseReadMaxBytes: 48}}
	t.Run("whole JSON is bounded", func(t *testing.T) {
		c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/responses", "application/json", 2, cfg)
		_, err := c.Writer.Write(pkHiddenBillingHTTPJSON(10, 6, 4, 2))
		require.Error(t, err)
		finish()
		require.Equal(t, http.StatusBadGateway, recorder.Code)
		require.Contains(t, recorder.Body.String(), "Upstream response usage could not be processed")
		require.NotContains(t, recorder.Body.String(), "input_tokens")
	})
	t.Run("SSE fragments cannot exceed the limit", func(t *testing.T) {
		c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/responses", "text/event-stream", 2, cfg)
		_, err := c.Writer.WriteString(`data: {"usage":{"input_tokens":`)
		require.NoError(t, err)
		_, err = c.Writer.WriteString(strings.Repeat("9", 48))
		require.Error(t, err)
		c.Writer.Flush()
		require.False(t, recorder.Flushed, "a failed writer must not flush retained unscaled bytes")
		finish()
		require.True(t, recorder.Flushed, "the terminal SSE error is flushed")
		require.Contains(t, recorder.Body.String(), "event: error")
		require.NotContains(t, recorder.Body.String(), "input_tokens")
		require.NotContains(t, recorder.Body.String(), "999")
		require.NotEmpty(t, c.Errors)
	})
	t.Run("incomplete JSON does not leak token digits", func(t *testing.T) {
		c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/responses", "application/json", 2)
		_, err := c.Writer.WriteString(`{"usage":{"input_tokens":123`)
		require.NoError(t, err)
		finish()
		require.NotContains(t, recorder.Body.String(), "123")
		require.NotContains(t, recorder.Body.String(), "input_tokens")
		require.NotEmpty(t, c.Errors)
	})
	t.Run("changing rate in a partial SSE fails closed", func(t *testing.T) {
		c, recorder, finish := newPKHiddenBillingHTTPContext(t, "/responses", "text/event-stream", 2)
		_, err := c.Writer.WriteString(`data: {"usage":{"input_tokens":123`)
		require.NoError(t, err)
		BindHiddenUsageOutput(c, &Account{ID: 43, Extra: map[string]any{AccountExtraHiddenRateMultiplier: 3}})
		_, err = c.Writer.WriteString("}}\n\n")
		require.Error(t, err)
		finish()
		require.Contains(t, recorder.Body.String(), "event: error")
		require.NotContains(t, recorder.Body.String(), "input_tokens")
		require.NotContains(t, recorder.Body.String(), "multiplier")
	})
}

type pkHiddenBillingHTTPFailingWriter struct {
	gin.ResponseWriter
	failAt     int
	writes     int
	flushes    int
	writeErr   error
	shortWrite bool
}

func (w *pkHiddenBillingHTTPFailingWriter) Write(data []byte) (int, error) {
	w.writes++
	if w.writes != w.failAt {
		return w.ResponseWriter.Write(data)
	}
	if w.shortWrite {
		n := max(len(data)-1, 0)
		if n > 0 {
			return w.ResponseWriter.Write(data[:n])
		}
		return 0, nil
	}
	return 0, w.writeErr
}

func (w *pkHiddenBillingHTTPFailingWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *pkHiddenBillingHTTPFailingWriter) Flush() {
	w.flushes++
	w.ResponseWriter.Flush()
}

func TestPKHiddenBillingHTTPTransportErrorsPreserveCauseAndDoNotReportUpstreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	closed := errors.New("test downstream connection closed")
	for _, transport := range []struct {
		name        string
		err         error
		cause       error
		shortWrite  bool
		wantNetwork bool
	}{
		{name: "canceled", err: fmt.Errorf("downstream write: %w", context.Canceled), cause: context.Canceled},
		{name: "closed_pipe", err: io.ErrClosedPipe, cause: io.ErrClosedPipe},
		{name: "network", err: fmt.Errorf("client socket: %w", &net.OpError{Op: "write", Net: "tcp", Err: closed}), cause: closed, wantNetwork: true},
		{name: "short_write", cause: io.ErrShortWrite, shortWrite: true},
	} {
		for _, phase := range []struct {
			name, contentType, first, second string
			multiplier                       float64
			failAt                           int
			failOnFinish                     bool
		}{
			{name: "whole_json", contentType: "application/json", first: `{"usage":{"input_tokens":10}}`, multiplier: 2, failAt: 1},
			{name: "whole_sse", contentType: "text/event-stream", first: "data: {\"usage\":{\"input_tokens\":10}}\n\n", multiplier: 2, failAt: 1},
			{name: "prefix", contentType: "text/event-stream", first: `data: {"usage":{"input_tokens":1`, multiplier: 2, failAt: 1},
			{name: "buffered_json", contentType: "application/json", first: `{"usage":{"input_tokens":1`, second: "0}}", multiplier: 2, failAt: 2},
			{name: "finish", contentType: "text/event-stream", first: `data: {"usage":{"input_tokens":10}}`, multiplier: 2, failAt: 2, failOnFinish: true},
			{name: "identity", contentType: "application/json", first: `{"usage":{"input_tokens":10}}`, multiplier: 1, failAt: 1},
			{name: "opaque", contentType: "audio/wav", first: "RIFF\x00audio", multiplier: 2, failAt: 1},
		} {
			t.Run(transport.name+"/"+phase.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				failing := &pkHiddenBillingHTTPFailingWriter{
					ResponseWriter: c.Writer, failAt: phase.failAt,
					writeErr: transport.err, shortWrite: transport.shortWrite,
				}
				c.Writer = failing
				c.Writer.Header().Set("Content-Type", phase.contentType)
				finish := BeginHiddenUsageOutput(c)
				t.Cleanup(finish)
				BindHiddenUsageOutput(c, &Account{Extra: map[string]any{AccountExtraHiddenRateMultiplier: phase.multiplier}})
				writer := c.Writer
				_, writeErr := writer.Write([]byte(phase.first))
				if phase.second != "" {
					require.NoError(t, writeErr)
					_, writeErr = writer.WriteString(phase.second)
				}
				if phase.failOnFinish {
					require.NoError(t, writeErr)
					finish()
					_, writeErr = writer.WriteString("do not retry after finish")
				}
				require.ErrorIs(t, writeErr, transport.cause)
				require.NotErrorIs(t, writeErr, errHiddenUsageOutput)
				if transport.wantNetwork {
					var networkErr *net.OpError
					require.ErrorAs(t, writeErr, &networkErr)
					require.Equal(t, "write", networkErr.Op)
				}
				require.NoError(t, HiddenUsageOutputError(c), "transport failure is not malformed usage")
				finish()
				finish()
				require.Equal(t, phase.failAt, failing.writes, "finish must not retry downstream bytes or append an error frame")
				_, retryErr := writer.WriteString("do not retry")
				require.ErrorIs(t, retryErr, transport.cause)
				writer.Flush()
				_, _, hijackErr := writer.Hijack()
				require.ErrorIs(t, hijackErr, transport.cause)
				require.Equal(t, phase.failAt, failing.writes)
				require.Zero(t, failing.flushes)
				require.False(t, recorder.Flushed)
				require.Empty(t, GetOpsStreamErrors(c), "client disconnect must not become an upstream SLA error")
				require.Empty(t, c.Errors)
				require.NoError(t, HiddenUsageOutputError(c))
				require.NotContains(t, recorder.Body.String(), "Upstream response usage could not be processed")
				require.NotContains(t, recorder.Body.String(), "event: error")
				require.NotEqual(t, http.StatusBadGateway, recorder.Code)
			})
		}
	}
}

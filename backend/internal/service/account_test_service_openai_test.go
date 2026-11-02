//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// --- shared test helpers ---

type queuedHTTPUpstream struct {
	responses []*http.Response
	errors    []error
	requests  []*http.Request
	routes    []string
	tlsFlags  []bool
}

func (u *queuedHTTPUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected Do call")
}

func (u *queuedHTTPUpstream) DoWithTLS(req *http.Request, route string, _ int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.requests = append(u.requests, req)
	u.routes = append(u.routes, route)
	u.tlsFlags = append(u.tlsFlags, profile != nil)
	if len(u.errors) > 0 {
		err := u.errors[0]
		u.errors = u.errors[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(u.responses) == 0 {
		return nil, fmt.Errorf("no mocked response")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

func newJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// --- test functions ---

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/1/test", nil)
	return c, rec
}

func TestPKAccountCandyUpstreamAccountUsesConfiguredKey(t *testing.T) {
	response := newJSONResponse(http.StatusOK, `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"<final_answer>21</final_answer>"}]}]}`)
	response.Header.Set("Content-Type", "application/json")
	upstream := &queuedHTTPUpstream{responses: []*http.Response{response}}
	svc := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
	account := &Account{
		ID:          1819,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeUpstream,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "upstream-candy-test-key",
			"base_url": "https://upstream.example.com",
			"model_mapping": map[string]any{
				"gpt-6-astra": "gpt-6.1-sol",
			},
		},
	}
	cfg := defaultAccountCandyConfig()
	text, err := svc.runAccountCandyProbe(context.Background(), account, account, cfg)
	require.NoError(t, err)
	require.Equal(t, "<final_answer>21</final_answer>", text)
	require.Len(t, upstream.requests, 1)
	request := upstream.requests[0]
	require.Equal(t, "Bearer upstream-candy-test-key", request.Header.Get("Authorization"))
	require.Equal(t, "https://upstream.example.com/v1/responses", request.URL.String())
	body, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(body, "model").String())
	require.Equal(t, "medium", gjson.GetBytes(body, "reasoning.effort").String())
	prompt := gjson.GetBytes(body, "input.0.content.0.text").String()
	require.True(t, strings.HasPrefix(prompt, intelligenceTestCandyPrompt))
	require.Contains(t, prompt, "<final_answer>")

	// The additional account type belongs only to the candy probe; the
	// existing connectivity-test routing and credentials contract stay intact.
	c, _ := newTestContext()
	err = svc.testOpenAIAccountConnection(c, account, cfg.Model, "", AccountTestModeDefault)
	require.ErrorContains(t, err, "Unsupported account type")
	require.Len(t, upstream.requests, 1)
}

func TestPKAccountCandyProxyRetryRequiresUnsentTransportFailure(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "temporary network failure", IsTemporary: true}}
	for _, tt := range []struct {
		name       string
		written    bool
		response   bool
		cancel     bool
		err        error
		wantRotate bool
	}{
		{name: "connect failure", err: dialErr, wantRotate: true},
		{name: "local dial timeout", err: &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}, wantRotate: true},
		{name: "SOCKS connect wraps dial timeout", err: &net.OpError{Op: "socks connect", Net: "socks5", Err: &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}}, wantRotate: true},
		{name: "SOCKS connect tunnel interrupted", err: &net.OpError{Op: "socks connect", Net: "socks5", Err: io.ErrUnexpectedEOF}, wantRotate: true},
		{name: "untyped deadline", err: context.DeadlineExceeded},
		{name: "parent cancelled during dial", cancel: true, err: dialErr},
		{name: "already wrote request", written: true, err: dialErr},
		{name: "response received", response: true, err: dialErr},
		{name: "untyped plugin error", err: fmt.Errorf("dial timeout secret-proxy")},
		{name: "read failure", err: &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}},
		{name: "cancelled", err: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), accountCandyProxyRetryKey{}, true)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test", strings.NewReader("payload"))
			require.NoError(t, err)
			calls := 0
			_, err = doAccountCandyTLSHandshakeRetry(request, func(attempt *http.Request) (*http.Response, error) {
				calls++
				if tt.cancel {
					cancel()
				}
				if tt.written {
					httptrace.ContextClientTrace(attempt.Context()).WroteHeaders()
				}
				if tt.response {
					return newJSONResponse(http.StatusUnauthorized, ""), tt.err
				}
				return nil, tt.err
			})
			require.Equal(t, 1, calls)
			require.Equal(t, tt.wantRotate, accountCandyCanChangeProxy(err))
		})
	}
}

func TestPKAccountCandyProxyRetryUsesDistinctTemporaryNativeRoutes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	columns := []string{"id", "protocol", "host", "port", "username", "password", "status", "expires_at"}
	// Skip the original route even if a second database ID points at it.
	mock.ExpectQuery("SELECT id,protocol,host,port").WithArgs(int64(0)).WillReturnRows(sqlmock.NewRows(columns).AddRow(8, "http", "original.example.test", 8080, "", "", "active", nil))
	mock.ExpectQuery("SELECT id,protocol,host,port").WithArgs(int64(8)).WillReturnRows(sqlmock.NewRows(columns).AddRow(9, "http", "backup.example.test", 8080, "", "", "active", nil))
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM proxies").WithArgs(int64(9), "http", "backup.example.test", 8080, "", "").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	upstream := &queuedHTTPUpstream{
		errors:    []error{&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "unavailable"}}},
		responses: []*http.Response{newJSONResponse(http.StatusOK, `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"<final_answer>21</final_answer>"}]}]}`)},
	}
	tests := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
	originalProxy := &Proxy{ID: 7, Protocol: "http", Host: "original.example.test", Port: 8080}
	account := &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeUpstream, Concurrency: 1, ProxyID: &originalProxy.ID, Proxy: originalProxy, Credentials: map[string]any{"api_key": "key", "base_url": "https://upstream.example.test"}}
	var attempts []int
	text, err := runAccountCandyWithProxyRetry(context.Background(), db, tests, account, account, defaultAccountCandyConfig(), func(_ context.Context, _ *Account, attempt int) error {
		attempts = append(attempts, attempt)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "<final_answer>21</final_answer>", text)
	require.Equal(t, []int{1, 2}, attempts)
	require.Equal(t, []string{"http://original.example.test:8080", "http://backup.example.test:8080"}, upstream.routes)
	for _, request := range upstream.requests {
		body, readErr := io.ReadAll(request.Body)
		require.NoError(t, readErr)
		require.Contains(t, gjson.GetBytes(body, "input.0.content.0.text").String(), "<final_answer>")
		require.Equal(t, "Bearer key", request.Header.Get("Authorization"))
	}
	require.Equal(t, int64(7), *account.ProxyID)
	require.Same(t, originalProxy, account.Proxy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPKAccountCandyProxyRetryDoesNotRotateAfterHTTPResponse(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusTooManyRequests, `{"error":{"message":"rate limited"}}`)}}
	tests := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
	account := &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeUpstream, Concurrency: 1, Credentials: map[string]any{"api_key": "key", "base_url": "https://upstream.example.test"}}
	_, err = runAccountCandyWithProxyRetry(context.Background(), db, tests, account, account, defaultAccountCandyConfig(), nil)
	require.ErrorContains(t, err, "HTTP 429")
	require.Len(t, upstream.requests, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPKAccountCandyProxyRetryHonorsParentDeadline(t *testing.T) {
	ctx := context.WithValue(context.Background(), accountCandyProxyRetryKey{}, true)
	ctx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test", strings.NewReader("payload"))
	require.NoError(t, err)
	calls := 0
	_, err = doAccountCandyTLSHandshakeRetry(request, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, accountCandyCanChangeProxy(err))
	require.Zero(t, calls)
}

func TestPKAuthorizationBatchSupplementParsesIndependentRows(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	input := "\r\n A@Example.com ---- keep password spaces ----" + strings.ToLower(secret) + "\r\n" +
		"a@example.com----other----" + secret + "\n" +
		"not an email----password-canary----" + secret + "\n" +
		"b@example.com--------" + secret + "\n" +
		"c@example.com----password-canary----123456\n" +
		"d@example.com----password-canary\n" +
		"e@example.com----password-canary----" + secret
	rows, err := parseAuthorizationSupplement(input)
	require.NoError(t, err)
	require.Len(t, rows, 7)
	require.Equal(t, 2, rows[0].result.Line)
	require.Equal(t, "a@example.com", rows[0].secret.Email)
	require.Equal(t, " keep password spaces ", rows[0].secret.Password)
	require.Equal(t, secret, rows[0].secret.TwoFA)
	for index, want := range []string{"", "duplicate_email", "invalid_email", "invalid_password", "invalid_twofa", "invalid_format", ""} {
		require.Equal(t, want, rows[index].result.Status)
		if want != "" {
			require.Empty(t, rows[index].secret)
		}
		raw, marshalErr := json.Marshal(rows[index].result)
		require.NoError(t, marshalErr)
		require.NotContains(t, string(raw), "password-canary")
		require.NotContains(t, string(raw), secret)
	}
	_, err = parseAuthorizationSupplement(strings.Repeat("a@example.com----p----"+secret+"\n", 201))
	require.Error(t, err)
	_, err = parseAuthorizationSupplement(strings.Repeat("x", (2<<20)+1))
	require.Error(t, err)
	_, err = parseAuthorizationSupplement("\n \r\n")
	require.Error(t, err)
}

type pkAuthorizationSupplementEncryptor struct{ plaintext string }

func (e *pkAuthorizationSupplementEncryptor) Encrypt(value string) (string, error) {
	e.plaintext = value
	return "encrypted-supplement-value", nil
}
func (*pkAuthorizationSupplementEncryptor) Decrypt(string) (string, error) {
	return "", fmt.Errorf("unexpected decrypt")
}

func TestPKAuthorizationBatchSupplementMatchesExistingAccountsAndPreservesJobs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	for _, row := range []struct {
		email string
		ids   []int64
	}{
		{email: "missing@example.com"},
		{email: "duplicate@example.com", ids: []int64{1, 2}},
		{email: "valid@example.com", ids: []int64{9}},
	} {
		mock.ExpectBegin()
		mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
		// Keep the single-account history -> job -> account lock order.
		mock.ExpectExec("UPDATE pk_authorization_jobs j SET settings=").WithArgs(row.email).WillReturnResult(sqlmock.NewResult(0, 1))
		matched := sqlmock.NewRows([]string{"id", "supported"})
		for _, id := range row.ids {
			matched.AddRow(id, true)
		}
		mock.ExpectQuery("SELECT a.id,").WithArgs(row.email).WillReturnRows(matched)
		if len(row.ids) != 1 {
			mock.ExpectRollback()
			continue
		}
		// Existing jobs keep their encrypted snapshots; no job/account is created.
		mock.ExpectQuery("SELECT lower").WithArgs(int64(9)).WillReturnRows(sqlmock.NewRows([]string{"email", "version"}).AddRow(row.email, "v1"))
		mock.ExpectQuery("SELECT id,md5").WithArgs(row.email).WillReturnRows(sqlmock.NewRows([]string{"id", "revision"}).AddRow(int64(21), "r1"))
		mock.ExpectExec("UPDATE pk_authorization_history SET secret_encrypted=").WithArgs(int64(21), "encrypted-supplement-value").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
	}
	cfg := &config.Config{}
	cfg.Totp.EncryptionKeyConfigured = true
	encryptor := &pkAuthorizationSupplementEncryptor{}
	svc := &AccountAuthorizationService{db: db, cfg: cfg, encryptor: encryptor}
	input := &AuthorizationSupplementBatch{Accounts: "missing@example.com----secret-canary----JBSWY3DPEHPK3PXP\nduplicate@example.com----secret-canary----JBSWY3DPEHPK3PXP\nvalid@example.com----secret-canary----JBSWY3DPEHPK3PXP"}
	result, err := svc.SupplementBatch(context.Background(), input)
	require.NoError(t, err)
	require.Empty(t, input.Accounts)
	require.Equal(t, 1, result.Saved)
	require.Equal(t, "not_found", result.Items[0].Status)
	require.Equal(t, "ambiguous_email", result.Items[1].Status)
	require.Equal(t, "saved", result.Items[2].Status)
	require.Equal(t, int64(9), result.Items[2].AccountID)
	require.Contains(t, encryptor.plaintext, "secret-canary")
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret-canary")
	require.NotContains(t, string(encoded), "JBSWY3DPEHPK3PXP")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPKAuthorizationBatchSupplementCancellationAndSafeErrors(t *testing.T) {
	cfg := &config.Config{}
	cfg.Totp.EncryptionKeyConfigured = true
	svc := &AccountAuthorizationService{cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := svc.SupplementBatch(ctx, &AuthorizationSupplementBatch{Accounts: "a@example.com----password----JBSWY3DPEHPK3PXP"})
	require.NoError(t, err)
	require.True(t, result.Cancelled)
	require.Zero(t, result.Saved)
	require.Equal(t, "cancelled", result.Items[0].Status)
	require.Equal(t, "failed", authorizationSupplementStatus(fmt.Errorf("driver rejected secret-password-2fa-canary")))
}

func TestPKAccountCandyReplyDetectsBodyAndNativeTerminal(t *testing.T) {
	const answer = "<final_answer>21</final_answer>"
	const completed = `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"<final_answer>21</final_answer>"}]}]}`
	terminal := func(kind string) string {
		return fmt.Sprintf("data: {\"type\":%q,\"response\":%s}\n\n", kind, completed)
	}
	chatStream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"<final_answer>21</final_answer>\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	for _, tt := range []struct {
		name   string
		header http.Header
		body   string
		chat   bool
	}{
		{name: "json without content type", body: completed},
		{name: "json mislabeled as SSE", header: http.Header{"Content-Type": {"text/event-stream"}}, body: completed},
		{name: "json with BOM and whitespace", body: "\ufeff \r\n" + completed},
		{name: "completed SSE", header: http.Header{"Content-Type": {"text/event-stream"}}, body: terminal("response.completed")},
		{name: "done without content type", body: terminal("response.done")},
		{name: "done with noncanonical header", header: http.Header{"content-type": {"text/event-stream"}}, body: terminal("response.done")},
		{name: "done mislabeled as JSON", header: http.Header{"Content-Type": {"application/json"}}, body: terminal("response.done")},
		{name: "SSE whitespace and BOM", body: " \t\ufeff\t " + strings.ReplaceAll(terminal("response.done"), "\n\n", " \r\n\t \r\n")},
		{name: "terminal at EOF", body: strings.TrimSpace(terminal("response.done"))},
		{name: "chat mislabeled as JSON", header: http.Header{"Content-Type": {"application/json"}}, body: chatStream, chat: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := newJSONResponse(http.StatusOK, tt.body)
			response.Header = tt.header
			text, err := readAccountCandyReply(response, tt.chat)
			require.NoError(t, err)
			require.Equal(t, answer, text)
		})
	}
}

func TestPKAccountCandyReplyRejectsIncompleteAndUnsafeBodies(t *testing.T) {
	const completed = `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"<final_answer>21</final_answer>"}]}]}`
	terminal := func(response string) string {
		return fmt.Sprintf("data: {\"type\":\"response.done\",\"response\":%s}\n\n", response)
	}
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{name: "empty", want: "正文为空或类型不受支持"},
		{name: "plain answer", body: "21", want: "类型不受支持"},
		{name: "HTML error", body: "<html>upstream-secret</html>", want: "类型不受支持"},
		{name: "invalid JSON", body: `{"status":`, want: "JSON 响应格式无效"},
		{name: "JSON error", body: `{"error":{"message":"Unsupported parameter","api_key":"upstream-secret"}}`, want: "Unsupported parameter"},
		{name: "invalid UTF8", body: "\xff", want: "无效文本编码"},
		{name: "delta without terminal", body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n", want: "缺少完整成功结束标记"},
		{name: "DONE alone", body: "data: [DONE]\n\n", want: "缺少完整最终回复"},
		{name: "done without completed status", body: terminal(strings.Replace(completed, `"status":"completed",`, "", 1)), want: "模型响应未完整结束"},
		{name: "done still in progress", body: terminal(strings.Replace(completed, `"status":"completed"`, `"status":"in_progress"`, 1)), want: "模型响应未完整结束"},
		{name: "done truncated", body: terminal(strings.Replace(completed, `{"status"`, `{"incomplete_details":{"reason":"max_output_tokens"},"status"`, 1)), want: "模型响应不完整"},
		{name: "done empty final text", body: terminal(strings.ReplaceAll(completed, "<final_answer>21</final_answer>", "")), want: "模型响应未完整结束"},
		{name: "done wrong role", body: terminal(strings.Replace(completed, `"role":"assistant"`, `"role":"user"`, 1)), want: "模型消息角色无效"},
		{name: "done tool output", body: terminal(strings.Replace(completed, `"type":"message"`, `"type":"function_call"`, 1)), want: "工具请求"},
		{name: "prior tool output", body: "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\"}}\n\n" + terminal(completed), want: "工具请求"},
		{name: "prior tool arguments", body: "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"upstream-secret\"}\n\n" + terminal(completed), want: "工具请求"},
		{name: "prior error", body: "data: {\"type\":\"error\",\"error\":{\"message\":\"Invalid model\",\"api_key\":\"upstream-secret\"}}\n\n" + terminal(completed), want: "Invalid model"},
		{name: "prior failed terminal", body: "data: {\"type\":\"response.failed\"}\n\n" + terminal(completed), want: "模型返回错误"},
		{name: "oversized final text", body: terminal(strings.ReplaceAll(completed, "<final_answer>21</final_answer>", strings.Repeat("2", intelligenceTestArtifactLimit+1))), want: "模型响应未完整结束"},
		{name: "oversized SSE line", body: "data: " + strings.Repeat("x", 1024*1024) + "\n\n", want: "事件行超过 1 MiB"},
		{name: "oversized leading whitespace line", body: strings.Repeat(" ", 1024*1024) + terminal(completed), want: "事件行超过 1 MiB"},
		{name: "oversized event with trailing whitespace", body: "data: {\"type\":\"response.done\"," + strings.Repeat(" ", 600*1024) + "\ndata: \"response\":" + completed + "}" + strings.Repeat(" ", 600*1024) + "\n\n", want: "响应事件超过 1 MiB"},
		{name: "oversized response", body: strings.Repeat(": ping\n", intelligenceTestResponseLimit/len(": ping\n")+1) + terminal(completed), want: "模型响应超过 4 MiB"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text, err := readAccountCandyReply(newJSONResponse(http.StatusOK, tt.body), false)
			require.Empty(t, text)
			require.ErrorContains(t, err, tt.want)
			require.IsType(t, &accountCandyProbeError{}, err)
			require.NotContains(t, err.Error(), "upstream-secret")
		})
	}
}

func TestPKAccountCandyReplyReadFailureUsesSafeClassification(t *testing.T) {
	response := newJSONResponse(http.StatusOK, "")
	response.Body = io.NopCloser(io.MultiReader(
		strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n"),
		iotest.ErrReader(fmt.Errorf("upstream-secret")),
	))
	text, err := readAccountCandyReply(response, false)
	require.Empty(t, text)
	require.ErrorContains(t, err, "读取模型响应失败")
	require.NotContains(t, err.Error(), "upstream-secret")
}

func TestPKAccountCandyUpstreamErrorDetails(t *testing.T) {
	const diagnostic = `{"message":"Unsupported reasoning.effort for gpt-6-astra; credential=private-echo-value","code":"unsupported_parameter","type":"invalid_request_error","param":"reasoning.effort","api_key":"hidden-field"}`
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"HTTP error", `{"error":` + diagnostic + `}`, http.StatusBadRequest},
		{"JSON error", `{"error":` + diagnostic + `}`, http.StatusOK},
		{"top-level error", "data: " + strings.Replace(diagnostic, `"type":"invalid_request_error"`, `"type":"error"`, 1) + "\n\n", http.StatusOK},
		{"failed response", `data: {"type":"response.failed","response":{"error":` + diagnostic + `}}` + "\n\n", http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(tt.status, tt.body)}}
			svc := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
			account := &Account{ID: 1819, Platform: PlatformOpenAI, Type: AccountTypeUpstream, Concurrency: 2, Credentials: map[string]any{"api_key": "private-echo-value", "base_url": "https://upstream.example.com"}}
			text, err := svc.runAccountCandyProbe(context.Background(), account, account, defaultAccountCandyConfig())
			require.Empty(t, text)
			require.ErrorContains(t, err, "Unsupported reasoning.effort")
			require.ErrorContains(t, err, "unsupported_parameter")
			require.ErrorContains(t, err, "gpt-6-astra")
			require.NotContains(t, err.Error(), "private-echo-value")
			require.NotContains(t, err.Error(), "hidden-field")
			require.Contains(t, accountCandySafeError(err), "unsupported_parameter")
		})
	}
	for _, body := range []string{`{"error":` + diagnostic + `}`, `data: {"error":` + diagnostic + `}` + "\n\n"} {
		text, err := readAccountCandyReply(newJSONResponse(http.StatusOK, body), true)
		require.Empty(t, text)
		require.ErrorContains(t, err, "unsupported_parameter")
	}
	account := &Account{Credentials: map[string]any{"api_key": strings.Repeat("credential-secret", 200)}, Proxy: &Proxy{Username: "proxy-user", Password: "proxy-pass"}}
	detail := redactCandyAccountError("Bad credential: "+account.GetCredential("api_key")+" http://proxy-user:proxy-pass@example.com Bearer bearer-secret", account)
	require.NotContains(t, detail, "credential-secret")
	require.NotContains(t, detail, "proxy-pass")
	require.NotContains(t, detail, "bearer-secret")
	require.LessOrEqual(t, len([]rune(redactCandyAccountError(strings.Repeat("x", 3000)))), accountCandyErrorDetailLimit)
}

type openAIAccountTestRepo struct {
	mockAccountRepoForGemini
	updatedExtra       map[string]any
	bulkUpdatedIDs     []int64
	bulkUpdatedPayload AccountBulkUpdate
	rateLimitedID      int64
	rateLimitedAt      *time.Time
	clearedErrorID     int64
	setErrorID         int64
	setErrorMsg        string
}

func (r *openAIAccountTestRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updatedExtra = updates
	return nil
}

func (r *openAIAccountTestRepo) BulkUpdate(_ context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	r.bulkUpdatedIDs = append([]int64(nil), ids...)
	r.bulkUpdatedPayload = updates
	return int64(len(ids)), nil
}

func (r *openAIAccountTestRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedID = id
	r.rateLimitedAt = &resetAt
	return nil
}

func (r *openAIAccountTestRepo) ClearError(_ context.Context, id int64) error {
	r.clearedErrorID = id
	return nil
}

func (r *openAIAccountTestRepo) SetError(_ context.Context, id int64, errorMsg string) error {
	r.setErrorID = id
	r.setErrorMsg = errorMsg
	return nil
}

func TestAccountTestService_OpenAISuccessPersistsSnapshotFromHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))
	resp.Header.Set("x-codex-primary-used-percent", "88")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "604800")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	resp.Header.Set("x-codex-secondary-used-percent", "42")
	resp.Header.Set("x-codex-secondary-reset-after-seconds", "18000")
	resp.Header.Set("x-codex-secondary-window-minutes", "300")

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          89,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.requests[0].Context()))
	require.NotEmpty(t, repo.updatedExtra)
	require.Equal(t, 42.0, repo.updatedExtra["codex_5h_used_percent"])
	require.Equal(t, 88.0, repo.updatedExtra["codex_7d_used_percent"])
	require.Contains(t, recorder.Body.String(), "test_complete")
}

func TestAccountTestService_OpenAIOAuthTestNormalizesGPT56Alias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))

	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{httpUpstream: upstream}
	account := &Account{
		ID:          90,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.6", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)

	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(body, "model").String())
}

func TestAccountTestService_OpenAIShadowUsesParentCredentialsAndShadowModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))

	parentID := int64(100)
	parent := &Account{
		ID:       parentID,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token":       "parent-token",
			"chatgpt_account_id": "org-parent",
		},
	}
	shadow := &Account{
		ID:              200,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		Status:          StatusActive,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		Concurrency:     2,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.3-codex-spark": "gpt-5.3-codex-spark",
			},
		},
	}

	repo := &openAIAccountTestRepo{
		mockAccountRepoForGemini: mockAccountRepoForGemini{
			accountsByID: map[int64]*Account{
				parentID: parent,
				200:      shadow,
			},
		},
	}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}

	err := svc.TestAccountConnection(ctx, shadow.ID, "gpt-5.3-codex-spark", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	require.Equal(t, "Bearer parent-token", req.Header.Get("Authorization"))
	require.Equal(t, "org-parent", req.Header.Get("chatgpt-account-id"))
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.3-codex-spark", gjson.GetBytes(body, "model").String())
	require.Contains(t, recorder.Body.String(), `"success":true`)
}

func TestAccountTestService_OpenAIStreamEOFBeforeCompletedFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"hi"}

`))

	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{httpUpstream: upstream}
	account := &Account{
		ID:          90,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Contains(t, recorder.Body.String(), "response.completed")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestAccountTestService_DeepSeekCustomBaseURLUsesV1ResponsesPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          91,
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"base_url":     "https://relay.example.com/v1",
			"api_protocol": APIProtocolResponses,
		},
		Extra: map[string]any{
			openai_compat.ExtraKeyResponsesSupported: true,
		},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://relay.example.com/v1/responses", upstream.requests[0].URL.String())
}

func TestAccountTestService_DeepSeekResponsesRoutesToOpenAIProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          93,
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"base_url":     "https://relay.example.com/v1",
			"api_protocol": APIProtocolResponses,
		},
		Extra: map[string]any{
			openai_compat.ExtraKeyResponsesSupported: true,
		},
	}
	repo := &openAIAccountTestRepo{
		mockAccountRepoForGemini: mockAccountRepoForGemini{
			accountsByID: map[int64]*Account{93: account},
		},
	}
	svc.accountRepo = repo

	err := svc.TestAccountConnection(ctx, account.ID, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://relay.example.com/v1/responses", upstream.requests[0].URL.String())
}

func TestAccountTestService_DeepSeekDefaultBaseURLUsesNativeResponsesPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          92,
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"api_protocol": APIProtocolResponses,
		},
		Extra: map[string]any{
			openai_compat.ExtraKeyResponsesSupported: true,
		},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://api.deepseek.com/responses", upstream.requests[0].URL.String())
}

func TestAccountTestService_OpenAI429PersistsSnapshotAndRateLimitState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","resets_at":1777283883}}`)
	resp.Header.Set("x-codex-primary-used-percent", "100")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "604800")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	resp.Header.Set("x-codex-secondary-used-percent", "100")
	resp.Header.Set("x-codex-secondary-reset-after-seconds", "18000")
	resp.Header.Set("x-codex-secondary-window-minutes", "300")

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          88,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusError,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.NotEmpty(t, repo.updatedExtra)
	require.Equal(t, 100.0, repo.updatedExtra["codex_5h_used_percent"])
	require.Equal(t, account.ID, repo.rateLimitedID)
	require.NotNil(t, repo.rateLimitedAt)
	require.Equal(t, account.ID, repo.clearedErrorID)
	require.Equal(t, StatusActive, account.Status)
	require.Empty(t, account.ErrorMessage)
	require.NotNil(t, account.RateLimitResetAt)
}

func TestAccountTestService_OpenAI429BodyOnlyPersistsRateLimitAndClearsStaleError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","resets_at":"1777283883"}}`)

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:           77,
		Platform:     PlatformOpenAI,
		Type:         AccountTypeOAuth,
		Status:       StatusError,
		ErrorMessage: "Access forbidden (403): account may be suspended or lack permissions",
		Concurrency:  1,
		Credentials:  map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, account.ID, repo.rateLimitedID)
	require.NotNil(t, repo.rateLimitedAt)
	require.Equal(t, account.ID, repo.clearedErrorID)
	require.Equal(t, StatusActive, account.Status)
	require.Empty(t, account.ErrorMessage)
	require.NotNil(t, account.RateLimitResetAt)
	require.Empty(t, repo.updatedExtra)
}

func TestAccountTestService_OpenAI429SyncsObservedPlanType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","plan_type":"free","resets_at":1777283883}}`)

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          81,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token", "plan_type": "plus"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, []int64{account.ID}, repo.bulkUpdatedIDs)
	require.Equal(t, "free", repo.bulkUpdatedPayload.Credentials["plan_type"])
	require.Equal(t, "free", account.Credentials["plan_type"])
	require.Equal(t, account.ID, repo.rateLimitedID)
	require.NotNil(t, account.RateLimitResetAt)
}

func TestAccountTestService_OpenAI429ActiveAccountDoesNotClearError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","resets_in_seconds":3600}}`)

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          78,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, account.ID, repo.rateLimitedID)
	require.NotNil(t, repo.rateLimitedAt)
	require.Zero(t, repo.clearedErrorID)
	require.Equal(t, StatusActive, account.Status)
	require.NotNil(t, account.RateLimitResetAt)
}

func TestAccountTestService_OpenAI429WithoutResetSignalDoesNotMutateRuntimeState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached"}}`)

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:           79,
		Platform:     PlatformOpenAI,
		Type:         AccountTypeOAuth,
		Status:       StatusError,
		ErrorMessage: "stale 403",
		Concurrency:  1,
		Credentials:  map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Zero(t, repo.rateLimitedID)
	require.Nil(t, repo.rateLimitedAt)
	require.Zero(t, repo.clearedErrorID)
	require.Equal(t, StatusError, account.Status)
	require.Equal(t, "stale 403", account.ErrorMessage)
	require.Nil(t, account.RateLimitResetAt)
}

func TestAccountTestService_OpenAI401SetsPermanentErrorOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusUnauthorized, `{"error":"bad token"}`)

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          80,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, account.ID, repo.setErrorID)
	require.Contains(t, repo.setErrorMsg, "Authentication failed (401)")
	require.Zero(t, repo.rateLimitedID)
	require.Zero(t, repo.clearedErrorID)
	require.Nil(t, account.RateLimitResetAt)
}

func TestAccountTestService_OpenAIAPIKeyResponsesUsesCodexProbeHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\"}\n\n"))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          95,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example/v1",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	require.Equal(t, "https://compat-upstream.example/v1/responses", req.URL.String())
	requireOpenAICodexProbeHeaders(t, req.Header)
}

func TestAccountTestService_OpenAIAPIKeyResponsesUnsupportedUsesChatCompletionsPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          91,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example/v1",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "hello", "")
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-test", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	body := recorder.Body.String()
	require.Contains(t, body, "pong")
	require.Contains(t, body, "已通过 /v1/chat/completions 验证")
	require.Contains(t, body, `"success":true`)
	require.NotContains(t, body, "当前测试接口仅支持 Responses API 路径")
}

func TestAccountTestService_OpenAIChatCompletionsPathReturns4xx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	upstream := &httpUpstreamRecorder{resp: newJSONResponse(http.StatusBadRequest, `{"error":{"message":"bad request"}}`)}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          92,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Contains(t, err.Error(), "Chat Completions API (/v1/chat/completions) returned 400")
	require.Contains(t, recorder.Body.String(), "/v1/chat/completions")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestAccountTestService_OpenAIChatCompletionsPathTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	upstream := &httpUpstreamRecorder{err: context.DeadlineExceeded}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          93,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Contains(t, err.Error(), "Chat Completions API (/v1/chat/completions) request failed")
	require.Contains(t, err.Error(), context.DeadlineExceeded.Error())
	require.Contains(t, recorder.Body.String(), "/v1/chat/completions")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestAccountTestService_OpenAIChatCompletionsPathRejectsNonJSONStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: not-json\n\n")),
	}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		ID:          94,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example",
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Contains(t, err.Error(), "Invalid Chat Completions response from /v1/chat/completions")
	require.Contains(t, recorder.Body.String(), "/v1/chat/completions")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

//go:build unit

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
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
	mock.ExpectQuery("SELECT p.id,p.protocol,p.host,p.port.*NOT EXISTS.*a.deleted_at IS NULL").WithArgs(int64(0)).WillReturnRows(sqlmock.NewRows(columns).AddRow(8, "http", "original.example.test", 8080, "", "", "active", nil))
	pkCandyExpectFreeCandidate(mock, 8, 9, "backup.example.test")
	pkCandyExpectFreeCheck(mock, 9, "backup.example.test", true)
	pkCandyExpectUnlock(mock, 9)
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

func pkCandyExpectFreeCheck(mock sqlmock.Sqlmock, id int64, host string, free bool) {
	mock.ExpectQuery("SELECT pg_backend_pid\\(\\),EXISTS.*NOT EXISTS.*a.deleted_at IS NULL").WithArgs(id, "http", host, 8080, "", "").WillReturnRows(sqlmock.NewRows([]string{"pid", "free"}).AddRow(9001, free))
}

func pkCandyExpectFreeCandidate(mock sqlmock.Sqlmock, after, id int64, host string) {
	mock.ExpectQuery("SELECT p.id,p.protocol,p.host,p.port.*NOT EXISTS.*a.deleted_at IS NULL").WithArgs(after).WillReturnRows(sqlmock.NewRows([]string{"id", "protocol", "host", "port", "username", "password", "status", "expires_at"}).AddRow(id, "http", host, 8080, "", "", "active", nil))
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"locked", "pid"}).AddRow(true, 9001))
	pkCandyExpectFreeCheck(mock, id, host, true)
}

func pkCandyExpectUnlock(mock sqlmock.Sqlmock, id int64) {
	mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
}

func pkCandyRetryTestAccount() *Account {
	proxy := &Proxy{ID: 7, Protocol: "http", Host: "original.example.test", Port: 8080}
	return &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeUpstream, Concurrency: 1, Status: StatusActive, Schedulable: true, ProxyID: &proxy.ID, Proxy: proxy, Credentials: map[string]any{"api_key": "key", "base_url": "https://upstream.example.test"}}
}

func TestPKAccountCandyProxyEgressFailureRetriesBeforeModelAndMarksUsable(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	pkCandyExpectFreeCandidate(mock, 0, 9, "backup.example.test")
	pkCandyExpectFreeCheck(mock, 9, "backup.example.test", true)
	pkCandyExpectFreeCheck(mock, 9, "backup.example.test", true)
	pkCandyExpectUnlock(mock, 9)
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusOK, `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"<final_answer>20</final_answer>"}]}]}`)}}
	tests := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
	session := newAccountCandyProxySession(db)
	var probed []int64
	session.probe = func(_ context.Context, account *Account) (string, *time.Time, string) {
		probed = append(probed, *account.ProxyID)
		if *account.ProxyID == 7 {
			return "", nil, "当前尝试的出口IP探测失败；未回退直连"
		}
		now := time.Now()
		return "8.8.8.8", &now, ""
	}
	account := pkCandyRetryTestAccount()
	text, err := runAccountCandyWithProxyRetry(context.Background(), db, tests, account, account, defaultAccountCandyConfig(), func(ctx context.Context, route *Account, attempt int) error {
		_, _, failure := session.observe(ctx, route, attempt)
		if failure != "" {
			return &accountCandyRetryableNetworkError{}
		}
		return nil
	}, session)
	require.NoError(t, err)
	require.Contains(t, text, "20") // A complete wrong answer still proves proxy connectivity.
	require.Equal(t, []int64{7, 9}, probed)
	require.Equal(t, []string{"http://backup.example.test:8080"}, upstream.routes)
	require.NotNil(t, session.usable)
	require.Equal(t, int64(9), session.usable.proxy.ID)
	require.Equal(t, int64(7), *account.ProxyID) // Binding waits for the guarded final transaction.
	session.close()
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPKAccountCandyProxyUnknownReservationDropsPhysicalConnection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT p.id,p.protocol,p.host,p.port.*NOT EXISTS.*a.deleted_at IS NULL").WithArgs(int64(0)).WillReturnRows(sqlmock.NewRows([]string{"id", "protocol", "host", "port", "username", "password", "status", "expires_at"}).AddRow(9, "http", "backup.example.test", 8080, "", "", "active", nil))
	// PostgreSQL could already have acquired the lock when decoding the
	// response fails. A missing reservation entry must not keep that session.
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(int64(9)).WillReturnRows(sqlmock.NewRows([]string{"locked", "pid"}).AddRow(true, "invalid-pid"))
	mock.ExpectClose()
	session := newAccountCandyProxySession(db)
	first, err := session.next(context.Background(), pkCandyRetryTestAccount(), nil)
	require.NoError(t, err)
	_, err = session.next(context.Background(), pkCandyRetryTestAccount(), first)
	require.Error(t, err)
	require.True(t, session.untrustedConnection)
	require.Empty(t, session.reserved)
	session.close()
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPKAccountCandyProxySharedModelsUseOneReservationAndBudget(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	pkCandyExpectFreeCandidate(mock, 0, 9, "backup.example.test")
	pkCandyExpectFreeCandidate(mock, 9, 10, "third.example.test")
	pkCandyExpectUnlock(mock, 9)
	pkCandyExpectUnlock(mock, 10)
	session := newAccountCandyProxySession(db)
	account := pkCandyRetryTestAccount()
	first, err := session.next(context.Background(), account, nil)
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan *accountCandyProxyAttempt, 8)
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, err := session.next(context.Background(), account, first)
			results <- next
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var second *accountCandyProxyAttempt
	for current := range results {
		if second == nil {
			second = current
		}
		require.Same(t, second, current)
	}
	third, err := session.next(context.Background(), account, second)
	require.NoError(t, err)
	require.Equal(t, 3, third.number)
	// A sibling already completed the second route's IP probe but was still
	// persisting progress when another model advanced to the third route.
	// The retired route cannot send a new model request even while still free.
	require.True(t, accountCandyCanChangeProxy(session.checkAttempt(context.Background(), second)))
	newest, err := session.next(context.Background(), account, second)
	require.NoError(t, err)
	require.Same(t, third, newest)
	_, err = session.next(context.Background(), account, third)
	require.ErrorContains(t, err, "3个不同出口")
	session.close()
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPKRadarCandyExpiredExecutionStillPersistsTerminalResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	identity, err := accountCandyCanonicalIdentity([]byte(`[]`))
	require.NoError(t, err)
	run := &RadarRun{AccountCandyTestResult: AccountCandyTestResult{ID: 12, AccountID: 19, OriginalGroupIDs: []int64{3}, identity: identity}, Generation: "generation", leaseToken: "owner", groupRule: &RadarGroupRule{SourceGroupID: 3, PassGroupIDs: []int64{4}, FailGroupID: 5}}
	cfg := &RadarConfig{Generation: "generation", IntervalMinutes: 60}
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT enabled,interval_minutes,target_group_id,generation").WillReturnRows(sqlmock.NewRows([]string{"enabled", "interval", "target", "generation"}).AddRow(true, 60, nil, "generation"))
	mock.ExpectQuery("SELECT value FROM settings").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`[]`))
	mock.ExpectQuery("SELECT model,effort,enabled").WillReturnRows(sqlmock.NewRows([]string{"model", "effort", "enabled"}).AddRow("gpt-6-astra", "medium", true))
	mock.ExpectQuery("SELECT COALESCE.*FROM pk_radar_account_state.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"owned"}).AddRow(true))
	mock.ExpectQuery("SELECT verdict,status,moved_to_group_id.*FROM pk_radar_account_runs.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"verdict", "status", "moved", "moved_ids", "move_error"}).AddRow("unknown", "running", nil, `[]`, ""))
	mock.ExpectQuery("SELECT parent_account_id FROM accounts.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"parent"}).AddRow(nil))
	mock.ExpectQuery("SELECT id FROM proxies.*FOR SHARE").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("SELECT jsonb_build_array.*FROM accounts").WillReturnRows(sqlmock.NewRows([]string{"identity"}).AddRow(`[]`))
	mock.ExpectQuery("SELECT id FROM groups.*FOR SHARE").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
	mock.ExpectQuery("SELECT group_id FROM account_groups.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"group_id"}).AddRow(3))
	mock.ExpectExec("UPDATE pk_radar_account_model_results SET status='cancelled'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT count\\(\\*\\),count").WillReturnRows(sqlmock.NewRows([]string{"total", "completed", "failed"}).AddRow(1, 0, 0))
	mock.ExpectExec("UPDATE pk_radar_account_runs SET status").WithArgs(int64(12), "failed", "unknown", nil, "本轮执行失败，请查看逐模型结果", "", int64(1000), "", nil, "", "[]", "").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE pk_radar_account_state SET active_run_id=NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM pk_radar_account_runs").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	execution, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	svc := &AccountCandyTestService{db: db}
	require.NoError(t, svc.finishRadar(context.Background(), execution, run, cfg, execution.Err(), 1000))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPKAccountCandyProxyOccupiedBeforeSendIsNeverUsed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	pkCandyExpectFreeCandidate(mock, 0, 9, "backup.example.test")
	pkCandyExpectFreeCheck(mock, 9, "backup.example.test", false)
	mock.ExpectQuery("SELECT p.id,p.protocol,p.host,p.port.*NOT EXISTS.*a.deleted_at IS NULL").WithArgs(int64(9)).WillReturnRows(sqlmock.NewRows([]string{"id", "protocol", "host", "port", "username", "password", "status", "expires_at"}))
	pkCandyExpectUnlock(mock, 9)
	upstream := &queuedHTTPUpstream{}
	tests := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
	account := pkCandyRetryTestAccount()
	_, err = runAccountCandyWithProxyRetry(context.Background(), db, tests, account, account, defaultAccountCandyConfig(), func(_ context.Context, _ *Account, attempt int) error {
		if attempt == 1 {
			return &accountCandyRetryableNetworkError{}
		}
		return nil
	})
	require.ErrorContains(t, err, "尚未分配账号")
	require.Empty(t, upstream.requests)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPKAccountCandyProxyBindRequiresFreshVacancyAndPreservesManualState(t *testing.T) {
	for _, tt := range []struct {
		name    string
		changed bool
		free    bool
	}{
		{"free proxy commits", false, true}, {"manual allocation wins", false, false}, {"manual pause wins", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			session := newAccountCandyProxySession(db)
			session.originalStatus, session.originalSchedulable = StatusActive, true
			session.backendPID = 9001
			session.conn, err = db.Conn(context.Background())
			require.NoError(t, err)
			defer session.conn.Close()
			proxy := &Proxy{ID: 9, Protocol: "http", Host: "backup.example.test", Port: 8080}
			session.usable = &accountCandyProxyAttempt{proxy: proxy, number: 2}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT status,schedulable FROM accounts.*FOR UPDATE").WithArgs(int64(19)).WillReturnRows(sqlmock.NewRows([]string{"status", "schedulable"}).AddRow(StatusActive, !tt.changed))
			if !tt.changed {
				mock.ExpectQuery("SELECT id FROM proxies.*FOR UPDATE").WithArgs(int64(9), "http", "backup.example.test", 8080, "", "").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
				pkCandyExpectFreeCheck(mock, 9, "backup.example.test", tt.free)
				if tt.free {
					mock.ExpectExec("UPDATE accounts SET proxy_id=.*upstream_billing_probe").WithArgs(int64(19), int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(SchedulerOutboxEventAccountChanged, int64(19)).WillReturnResult(sqlmock.NewResult(1, 1))
				}
			}
			mock.ExpectCommit()
			tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelSerializable})
			require.NoError(t, err)
			message, err := session.bindUsable(context.Background(), tx, 19)
			require.NoError(t, err)
			if tt.changed {
				require.Contains(t, message, "调度设置已更改")
			} else if tt.free {
				require.Contains(t, message, "切换")
			} else {
				require.Contains(t, message, "已分配账号")
			}
			require.NoError(t, tx.Commit())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPKRadarCandyNetworkRetryIsIsolatedFromAuthorization(t *testing.T) {
	for _, tt := range []struct {
		name     string
		radar    bool
		err      error
		response bool
		want     bool
	}{
		{"radar read reset", true, &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}, false, true},
		{"radar EOF", true, io.EOF, false, true},
		{"radar timeout", true, context.DeadlineExceeded, false, true},
		{"authorization read reset", false, &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}, false, false},
		{"unknown plugin error", true, fmt.Errorf("transport failed"), false, false},
		{"HTTP error remains final", true, io.EOF, true, false},
		{"cancelled", true, context.Canceled, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), accountCandyProxyRetryKey{}, true)
			ctx = context.WithValue(ctx, radarCandyProxyRetryKey{}, tt.radar)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test", strings.NewReader("payload"))
			require.NoError(t, err)
			_, err = doAccountCandyTLSHandshakeRetry(req, func(r *http.Request) (*http.Response, error) {
				httptrace.ContextClientTrace(r.Context()).WroteHeaders()
				if tt.response {
					return newJSONResponse(http.StatusUnauthorized, ""), tt.err
				}
				return nil, tt.err
			})
			require.Equal(t, tt.want, accountCandyCanChangeProxy(err))
		})
	}
}

func TestPKRadarCandyCanContinuePastThreeDistinctFreeProxies(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	pkCandyExpectFreeCandidate(mock, 0, 9, "second.example.test")
	pkCandyExpectFreeCandidate(mock, 9, 10, "third.example.test")
	pkCandyExpectFreeCandidate(mock, 10, 11, "fourth.example.test")
	for _, id := range []int64{9, 10, 11} {
		pkCandyExpectUnlock(mock, id)
	}
	session := newAccountCandyProxySession(db)
	session.limit = 0
	var previous *accountCandyProxyAttempt
	for i := 1; i <= 4; i++ {
		previous, err = session.next(context.Background(), pkCandyRetryTestAccount(), previous)
		require.NoError(t, err)
		require.Equal(t, i, previous.number)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = session.next(ctx, pkCandyRetryTestAccount(), previous)
	require.ErrorIs(t, err, context.Canceled)
	session.close()
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

func TestPKAccountCandyHTTPStatusSurvivesSafeErrorFormatting(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, status := range []int{401, 402, 429, 500, 502, 503} {
			for _, body := range []string{
				`{"error":{"message":"credential private-echo-value rejected","code":"account_rejected","api_key":"hidden-field"}}`,
				`<html><body>private-echo-value raw upstream page</body></html>`,
				`{"error":{"message":"<html>private-echo-value raw upstream page</html>"}}`,
			} {
				t.Run(fmt.Sprintf("chat=%t/status=%d/%d", chat, status, len(body)), func(t *testing.T) {
					upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(status, body)}}
					svc := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
					account := &Account{ID: 91, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "private-echo-value", "base_url": "https://upstream.example.com"}}
					if chat {
						account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
					}
					text, err := svc.runAccountCandyProbe(context.Background(), account, account, defaultAccountCandyConfig())
					require.Empty(t, text)
					require.ErrorContains(t, err, fmt.Sprintf("HTTP %d", status))
					message := accountCandySafeError(err)
					require.Contains(t, message, fmt.Sprintf("HTTP %d", status))
					for _, secret := range []string{"private-echo-value", "hidden-field", "<html>", "raw upstream page"} {
						require.NotContains(t, message, secret)
					}
					require.Len(t, upstream.requests, 1)
				})
			}
		}
	}
}

func TestPKAccountCandyTransportFailureHasNoInventedHTTPStatus(t *testing.T) {
	for _, chat := range []bool{false, true} {
		upstream := &queuedHTTPUpstream{errors: []error{&net.OpError{Op: "dial", Net: "tcp", Err: fmt.Errorf("private-proxy-password")}}}
		svc := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
		account := &Account{ID: 92, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "private-key", "base_url": "https://upstream.example.com"}}
		if chat {
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
		}
		text, err := svc.runAccountCandyProbe(context.Background(), account, account, defaultAccountCandyConfig())
		require.Empty(t, text)
		require.ErrorContains(t, err, "未收到 HTTP 响应")
		require.Contains(t, accountCandySafeError(err), "未收到 HTTP 响应")
		require.NotContains(t, accountCandySafeError(err), "private-proxy-password")
		require.NotContains(t, accountCandySafeError(err), "HTTP 500")
	}
}

func TestPKAccountCandyHTTP200BodyFailureKeepsStatusAndRadarRetryBoundary(t *testing.T) {
	for _, radar := range []bool{false, true} {
		for _, network := range []bool{false, true} {
			ctx, _ := newTestContext()
			ctx.Set(accountCandyProbeKey, &accountCandyProbe{})
			if radar {
				ctx.Request = ctx.Request.WithContext(context.WithValue(ctx.Request.Context(), radarCandyProxyRetryKey{}, true))
			}
			response := newJSONResponse(http.StatusOK, "data: malformed JSON\n\n")
			if network {
				response.Body = io.NopCloser(io.MultiReader(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n"), iotest.ErrReader(io.ErrUnexpectedEOF)))
			}
			err := finishAccountCandyProbe(ctx, response, false)
			require.ErrorContains(t, err, "HTTP 200")
			require.Contains(t, accountCandySafeError(err), "HTTP 200")
			require.Equal(t, radar && network, accountCandyCanChangeProxy(err))
		}
	}
}

func TestPKAccountCandyHTTP200CompleteJSONIsNotReplayed(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		chat, failure bool
	}{
		{name: "responses complete answer", body: `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"<final_answer>21</final_answer>"}]}]}`},
		{name: "chat complete answer", chat: true, body: `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"<final_answer>21</final_answer>"}}]}`},
		{name: "responses complete upstream error", body: `{"error":{"code":"account_rejected","message":"account rejected"}}`, failure: true},
		{name: "chat complete upstream error", chat: true, body: `{"error":{"code":"account_rejected","message":"account rejected"}}`, failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := newTestContext()
			probe := &accountCandyProbe{}
			ctx.Set(accountCandyProbeKey, probe)
			ctx.Request = ctx.Request.WithContext(context.WithValue(ctx.Request.Context(), radarCandyProxyRetryKey{}, true))
			response := newJSONResponse(http.StatusOK, "")
			response.Body = io.NopCloser(io.MultiReader(strings.NewReader(tc.body), iotest.ErrReader(io.ErrUnexpectedEOF)))
			err := finishAccountCandyProbe(ctx, response, tc.chat)
			if tc.failure {
				require.ErrorContains(t, err, "HTTP 200")
				require.ErrorContains(t, err, "account_rejected")
			} else {
				require.NoError(t, err)
				require.Contains(t, probe.text, "21")
			}
			require.False(t, accountCandyCanChangeProxy(err), "complete answer or upstream error must never replay")
		})
	}
}

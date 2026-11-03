package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
	"github.com/stretchr/testify/require"
)

func TestPKBinanceProxyInputNormalizedBeforePersistence(t *testing.T) {
	raw := "http://fixture123-region-HK-sid-Ab12Cd-t-5:fixturePASS123@proxy.invalid:3000"
	input, err := normalizeBinanceRechargeAccountInput(BinanceRechargeAccountInput{Name: "fixture", RecipientUID: "123", ProxyURL: "\uFEFF[" + raw + "](" + raw + ")\u200B"})
	require.NoError(t, err)
	require.Equal(t, raw, input.ProxyURL)
	require.NoError(t, validateBinanceAccountProxy(input.ProxyURL, false))
	// Empty means keep the saved route; invisible-only input cannot erase it.
	_, err = normalizeBinanceRechargeAccountInput(BinanceRechargeAccountInput{Name: "fixture", RecipientUID: "123", ProxyURL: "\u200B"})
	require.ErrorContains(t, err, "没有有效地址")
}

func TestPKBinanceBackupAlternatesWithBoundedCancelableBackoff(t *testing.T) {
	for successAt := 0; successAt <= 5; successAt++ {
		t.Run(string(rune('0'+successAt)), func(t *testing.T) {
			var routes []bool
			var waits []time.Duration
			var states []BinanceQueryStatus
			err := runBinanceRouteAttempts(context.Background(), true, func(backup bool) error {
				routes = append(routes, backup)
				if len(routes) == successAt {
					return nil
				}
				return &binancepay.RequestError{Reason: "timeout"}
			}, func(status BinanceQueryStatus) error { states = append(states, status); return nil }, func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil })
			attempts := successAt
			if successAt == 0 {
				attempts = 5
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, []bool{false, true, false, true, false}[:attempts], routes)
			wantWaits := attempts - 2
			if wantWaits < 0 {
				wantWaits = 0
			}
			require.Len(t, waits, wantWaits)
			for index, delay := range waits {
				require.Equal(t, time.Duration(index+1)*5*time.Second, delay)
			}
			for _, status := range states {
				require.LessOrEqual(t, status.Attempt, 5)
				if status.State == "waiting" {
					require.NotNil(t, status.RetryAt)
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := runBinanceRouteAttempts(ctx, true, func(bool) error { calls++; return &binancepay.RequestError{Reason: "dns"} }, func(BinanceQueryStatus) error { return nil }, func(ctx context.Context, _ time.Duration) error { cancel(); return binanceWait(ctx, time.Hour) })
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 2, calls)
}

func TestPKBinanceBackupStopsOnBusinessRateLimitAndLostLease(t *testing.T) {
	for _, failure := range []error{
		&binancepay.RateLimitError{StatusCode: 429, RetryAfter: time.Minute},
		&binancepay.RateLimitError{StatusCode: 418, RetryAfter: time.Hour},
		&binancepay.RequestError{Reason: "api_key_ip_permission", HTTPStatus: 403, ProviderCode: "-2015"},
		&binancepay.RequestError{Reason: "proxy_auth", HTTPStatus: 407},
		&binancepay.RequestError{Reason: "signature"},
		&binancepay.RequestError{Reason: "tls"},
		errBinanceAccountProxy, context.Canceled,
	} {
		calls := 0
		err := runBinanceRouteAttempts(context.Background(), true, func(bool) error { calls++; return failure }, func(BinanceQueryStatus) error { return nil }, func(context.Context, time.Duration) error { t.Fatal("non-network error waited"); return nil })
		require.ErrorIs(t, err, failure)
		require.Equal(t, 1, calls)
	}
	err := runBinanceRouteAttempts(context.Background(), true, func(bool) error { t.Fatal("lost lease reached provider"); return nil }, func(BinanceQueryStatus) error { return context.Canceled }, binanceWait)
	require.ErrorIs(t, err, context.Canceled)
	calls := 0
	err = runBinanceRouteAttempts(context.Background(), false, func(backup bool) error {
		require.False(t, backup)
		calls++
		return &binancepay.RequestError{Reason: "network"}
	}, func(BinanceQueryStatus) error { t.Fatal("legacy operation changed state"); return nil }, binanceWait)
	require.Error(t, err)
	require.Equal(t, 1, calls)
	ctx, cancel := context.WithCancel(context.Background())
	limited := &binancepay.RateLimitError{StatusCode: 429, RetryAfter: time.Hour}
	err = runBinanceRouteAttempts(ctx, true, func(bool) error { cancel(); return limited }, func(BinanceQueryStatus) error { return ctx.Err() }, binanceWait)
	require.ErrorIs(t, err, limited, "cancellation must not erase a known provider cooldown")
}

func TestPKBinanceBackupSavePreserveReplaceClearBusyAndRecovery(t *testing.T) {
	const primary = "cipher:http://primary.invalid:3128"
	const oldBackup = "cipher:http://backup.invalid:3128"
	for _, tc := range []struct {
		name, replacement    string
		clear, busy, recover bool
	}{
		{name: "preserve"}, {name: "replace", replacement: "http://replacement.invalid:3128"},
		{name: "clear", clear: true}, {name: "busy", replacement: "http://replacement.invalid:3128", busy: true},
		{name: "recover", replacement: "http://replacement.invalid:3128", recover: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			state := BinanceRechargeAccount{backupProxyURLCiphertext: oldBackup}
			if tc.recover {
				state.failureCount = 3
				state.LastError = binanceRechargePollError(&binancepay.RequestError{Reason: "network"})
			}
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WithArgs(int64(1)).WillReturnRows(pkBinanceAccountRows(primary, false, state))
			if tc.clear || tc.replacement != "" {
				mock.ExpectQuery(`SELECT poll_token<>'' AND api_locked_until>NOW\(\)`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"polling"}).AddRow(tc.busy))
			}
			want := oldBackup
			if tc.clear {
				want = ""
			}
			if tc.replacement != "" {
				want = "cipher:" + tc.replacement
			}
			if tc.busy {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec(`UPDATE binance_recharge_accounts SET name=\$2`).WithArgs(int64(1), "fixture", "12345", "cipher:key", "cipher:secret", primary, false, true, 30, "1.000000", want, false).WillReturnResult(sqlmock.NewResult(0, 1))
				if tc.recover {
					mock.ExpectExec(`UPDATE binance_recharge_accounts SET next_poll_at=NOW\(\),bills_next_query_at=NOW\(\),failure_count=0`).WithArgs(int64(1), state.LastError).WillReturnResult(sqlmock.NewResult(0, 1))
				}
				state.backupProxyURLCiphertext = want
				mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WithArgs(int64(1)).WillReturnRows(pkBinanceAccountRows(primary, false, state))
				mock.ExpectCommit()
			}
			svc := NewBinanceRechargeService(db, pkBinanceProxyEncryptor{}, &pkBinanceBillsClient{}, nil, nil)
			account, err := svc.SaveAccount(context.Background(), 1, BinanceRechargeAccountInput{Name: "fixture", RecipientUID: "12345", Enabled: true, OrderTTLMinutes: 30, BackupProxyURL: tc.replacement, ClearBackupProxy: tc.clear})
			if tc.busy {
				require.ErrorContains(t, err, "查询进行中")
			} else {
				require.NoError(t, err)
				require.Equal(t, want != "", account.BackupProxyConfigured)
				body, err := json.Marshal(account)
				require.NoError(t, err)
				for _, secret := range []string{"cipher:", "primary.invalid", "backup.invalid", "replacement.invalid"} {
					require.NotContains(t, string(body), secret)
				}
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPKBinanceBackupRequiresPrimaryAndExplicitClear(t *testing.T) {
	for _, clearPrimary := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		mock.ExpectBegin()
		primary := ""
		if clearPrimary {
			primary = "cipher:http://primary.invalid:3128"
		}
		mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WillReturnRows(pkBinanceAccountRows(primary, false))
		mock.ExpectRollback()
		svc := NewBinanceRechargeService(db, pkBinanceProxyEncryptor{}, nil, nil, nil)
		_, err = svc.SaveAccount(context.Background(), 1, BinanceRechargeAccountInput{Name: "fixture", RecipientUID: "12345", ClearProxy: clearPrimary, BackupProxyURL: "http://backup.invalid:3128"})
		require.ErrorContains(t, err, "主专属代理")
		require.NoError(t, mock.ExpectationsWereMet())
		db.Close()
	}
}

func TestPKBinanceBackupUsesSavedRoutesAndStopsAtProxyAuth(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int64
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls.Add(1)
		if r.Method != http.MethodConnect || r.Host != "api.binance.com:443" {
			t.Error("unexpected proxy target")
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls.Add(1)
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer backup.Close()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	for i := 0; i < 3; i++ {
		mock.ExpectExec(`UPDATE binance_recharge_accounts SET query_status=\$3::jsonb`).WithArgs(int64(1), "fixture-lease", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	svc := NewBinanceRechargeService(db, pkBinanceProxyEncryptor{}, &pkBinanceBillsClient{}, nil, nil)
	a := &BinanceRechargeAccount{ID: 1, proxyURLCiphertext: "cipher:" + primary.URL, backupProxyURLCiphertext: "cipher:" + backup.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = svc.withBinanceAccountRoutes(ctx, a, "fixture-lease", func(client BinanceRechargeClient) error { return client.Probe(ctx, "fixture-key", "fixture-secret") })
	var failure *binancepay.RequestError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "proxy_auth", failure.Reason)
	require.Equal(t, int64(1), primaryCalls.Load())
	require.Equal(t, int64(1), backupCalls.Load())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPKBinanceBackupPreflightAndLeaseValidation(t *testing.T) {
	svc := NewBinanceRechargeService(nil, pkBinanceProxyEncryptor{}, nil, nil, nil)
	preflight := true
	_, _, err := svc.prepareBinanceBackupProxy(BinanceRechargeAccount{}, BinanceRechargeAccountInput{BackupProxyURL: "socks5://fixture:secret@proxy.invalid:1080", BackupProxyPreflight: &preflight})
	require.ErrorContains(t, err, "备用代理预探测")
	_, _, err = svc.prepareBinanceBackupProxy(BinanceRechargeAccount{}, BinanceRechargeAccountInput{BackupProxyPreflight: &preflight})
	require.ErrorContains(t, err, "必须配置备用代理")
	value, enabled, err := svc.prepareBinanceBackupProxy(BinanceRechargeAccount{backupProxyURLCiphertext: "unreadable", BackupProxyPreflight: true}, BinanceRechargeAccountInput{ClearBackupProxy: true})
	require.NoError(t, err)
	require.Empty(t, value)
	require.False(t, enabled)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`UPDATE binance_recharge_accounts SET query_status=\$3::jsonb`).WillReturnResult(sqlmock.NewResult(0, 0))
	svc.db = db
	a := &BinanceRechargeAccount{ID: 1, proxyURLCiphertext: "cipher:http://primary.invalid:3128", backupProxyURLCiphertext: "cipher:http://backup.invalid:3128"}
	err = svc.withBinanceAccountRoutes(context.Background(), a, "stale", func(BinanceRechargeClient) error { t.Fatal("stale lease contacted provider"); return nil })
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPKBinanceProxyErrorsKeepSafeSpecificReason(t *testing.T) {
	for _, tc := range []struct{ raw, hint string }{
		{"http://fixture:private-secret@proxy.invalid:99999", "端口"},
		{"http://fixture:private-secret@proxy.invalid:80/path", "路径"},
		{"ftp://fixture:private-secret@proxy.invalid:80", "开头"},
		{"http://fixture:private-secret@proxy.\u200Binvalid:80", "隐藏字符"},
	} {
		err := validateBinanceAccountProxy(tc.raw, false)
		require.ErrorIs(t, err, errBinanceAccountProxy)
		var invalid *binancepay.ProxyConfigurationError
		require.True(t, errors.As(err, &invalid))
		inputErr := binanceProxyInputError(err)
		require.ErrorContains(t, inputErr, tc.hint)
		require.False(t, strings.Contains(inputErr.Error(), "private-secret"))
		require.False(t, strings.Contains(inputErr.Error(), "proxy.invalid"))
	}
}

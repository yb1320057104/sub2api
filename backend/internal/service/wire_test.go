package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/zeromicro/go-zero/core/collection"
)

func TestPKCyberPolicyValidationAndMasking(t *testing.T) {
	for input, want := range map[string]string{"6.7": "6.700000", "0.29": "0.290000", "0.000001": "0.000001", "1000": "1000.000000"} {
		got, err := normalizeCyberPolicyMultiplier(input)
		if err != nil || got != want {
			t.Fatalf("multiplier %s: %s %v", input, got, err)
		}
	}
	for _, input := range []string{"", "0", "-1", "1e2", "NaN", "1000.000001", "0.0000001", " 6.7", "6.7 "} {
		if _, err := normalizeCyberPolicyMultiplier(input); err == nil {
			t.Fatalf("accepted invalid multiplier %q", input)
		}
	}
	for input, want := range map[string]string{"": "用户***", "alice@example.com": "ali***", "中文用户名": "中文用***", "😀😃😄😁": "😀😃😄***", "李": "李***", "abc": "abc***"} {
		if got := maskCyberPolicyIdentity(input); got != want {
			t.Fatalf("mask %q = %q, want %q", input, got, want)
		}
	}
	encoded, err := json.Marshal(CyberPolicyPublicRanking{Items: []CyberPolicyPublicRank{{Rank: 1, DisplayName: maskCyberPolicyIdentity("alice@example.com"), TriggerCount: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"alice@example.com", "user_id", "email", "balance", "multiplier", "pending_count", "auto_disable_count"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("public DTO leaks %s: %s", forbidden, encoded)
		}
	}
	now := time.Date(2026, 10, 2, 18, 0, 0, 0, time.UTC)
	window, err := ParseCyberPolicyWindow(CyberPolicyWindowInput{Timezone: "Asia/Shanghai"}, now)
	if err != nil || window.StartDate != "2026-10-03" || window.EndDate != "2026-10-03" || window.From.Hour() != 0 {
		t.Fatalf("local today: %+v %v", window, err)
	}
	window, err = ParseCyberPolicyWindow(CyberPolicyWindowInput{StartDate: "2026-03-08", EndDate: "2026-03-08", Timezone: "America/New_York"}, now)
	if err != nil || window.To.Sub(window.From) != 23*time.Hour {
		t.Fatalf("DST calendar day: %+v %v", window, err)
	}
	for _, input := range []CyberPolicyWindowInput{
		{StartDate: "2026-10-04", EndDate: "2026-10-03", Timezone: "UTC"},
		{StartDate: "2026-02-30", EndDate: "2026-03-01", Timezone: "UTC"},
		{StartDate: "2025-01-01", EndDate: "2026-01-02", Timezone: "UTC"},
		{StartDate: "2026-10-03", Timezone: "UTC"}, {Timezone: "Invalid/Timezone"},
	} {
		if _, err := ParseCyberPolicyWindow(input, now); err == nil {
			t.Fatalf("accepted invalid window %+v", input)
		}
	}
}

type pkBinanceLifecycleClient struct{ entered chan struct{} }

func TestPKBinanceCreditMultiplierValidation(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"1", "1.000000"}, {"6.7", "6.700000"}, {"0.000001", "0.000001"},
		{"999.999999", "999.999999"}, {"1000", "1000.000000"}, {"1000.000000", "1000.000000"},
		{"0001.0203", "1.020300"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := normalizeBinanceCreditMultiplier(tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("normalize multiplier %q: got %q, error %v, want %q", tc.input, got, err, tc.want)
			}
			input, err := normalizeBinanceRechargeAccountInput(BinanceRechargeAccountInput{
				Name: "fixture", RecipientUID: "12345", CreditMultiplier: &tc.input,
			})
			if err != nil || input.CreditMultiplier == nil || *input.CreditMultiplier != tc.want {
				t.Fatalf("account input did not preserve the validated multiplier: %v", err)
			}
		})
	}
	for _, value := range []string{
		"", " ", " 1", "1 ", "1\n", "0", "0.000000", "0.0000001", "-1", "+1", "1000.000001", "1001",
		"1.0000000", "1.1234567", "1e2", "1E-6", ".5", "1.", "1,5", "NaN", "Infinity", "0x10", "１２", "1..0",
		"99999999999999999999999999999999999999",
	} {
		t.Run("reject "+value, func(t *testing.T) {
			if _, err := normalizeBinanceCreditMultiplier(value); err == nil {
				t.Fatalf("invalid multiplier %q was accepted", value)
			}
			// No database or provider is available here: rejection must happen
			// before persistence, encryption or any Binance network operation.
			svc := NewBinanceRechargeService(nil, nil, nil, nil, nil)
			_, err := svc.SaveAccount(context.Background(), 1, BinanceRechargeAccountInput{
				Name: "fixture", RecipientUID: "12345", CreditMultiplier: &value,
			})
			if err == nil || !strings.Contains(err.Error(), "倍率") {
				t.Fatalf("invalid account multiplier must fail validation first: %v", err)
			}
		})
	}
	input, err := normalizeBinanceRechargeAccountInput(BinanceRechargeAccountInput{Name: "fixture", RecipientUID: "12345"})
	if err != nil || input.CreditMultiplier != nil {
		t.Fatalf("an omitted multiplier must remain distinguishable from an explicit replacement: %v", err)
	}
}

func TestPKBinanceProxyStartupUsesLoadedConfig(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cfg       *config.Config
		wantError bool
	}{
		{name: "missing configuration", wantError: true},
		{name: "default direct", cfg: &config.Config{}},
		{name: "persistent gateway configuration", cfg: &config.Config{BinanceRecharge: config.BinanceRechargeConfig{ProxyURL: "http://fixture-user:fixture-pass@127.0.0.1:17990", ProxyPreflight: true}}},
		{name: "static SOCKS remains supported", cfg: &config.Config{BinanceRecharge: config.BinanceRechargeConfig{ProxyURL: "socks5://127.0.0.1:17990"}}},
		{name: "preflight without gateway", cfg: &config.Config{BinanceRecharge: config.BinanceRechargeConfig{ProxyPreflight: true}}, wantError: true},
		{name: "invalid gateway fails", cfg: &config.Config{BinanceRecharge: config.BinanceRechargeConfig{ProxyURL: "http://fixture-user:fixture-pass@localhost:17990/private"}}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := ProvideBinanceRechargeService(tc.cfg, nil, nil, nil, nil)
			if tc.wantError {
				if err == nil || svc != nil {
					t.Fatal("invalid dedicated proxy must fail startup")
				}
				return
			}
			if err != nil || svc == nil {
				t.Fatalf("loaded Binance configuration was rejected: %v", err)
			}
			svc.Stop()
		})
	}
}

func TestPKBinanceProxyPreflightConfigurationKeepsTypedHint(t *testing.T) {
	err := validateBinanceAccountProxy("socks5://fixture-user:fixture-pass@localhost:1234", true)
	if !errors.Is(err, binancepay.ErrProxyPreflightConfiguration) {
		t.Fatalf("SOCKS5 preflight should retain the typed configuration error: %v", err)
	}
	for _, raw := range []string{"http://fixture-user:fixture-pass@localhost:1234/private", "http://fixture-user:fixture-pass@localhost:99999", "http://fixture-user:fixture-pass@localhost:bad"} {
		if err := validateBinanceAccountProxy(raw, true); !errors.Is(err, errBinanceAccountProxy) {
			t.Fatal("malformed proxy must retain the generic safe error")
		}
	}
}

type pkBinanceProxyEncryptor struct{}

func (pkBinanceProxyEncryptor) Encrypt(value string) (string, error) { return "cipher:" + value, nil }
func (pkBinanceProxyEncryptor) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "cipher:") {
		return "", errors.New("fixture decryption failed")
	}
	return strings.TrimPrefix(value, "cipher:"), nil
}

func TestPKBinanceAccountProxyIsolatesChannelsAndNeverFallsBack(t *testing.T) {
	defaultClient := &pkBinanceBillsClient{}
	svc := NewBinanceRechargeService(nil, pkBinanceProxyEncryptor{}, defaultClient, nil, nil)
	var requests [2]atomic.Int32
	var clients [2]BinanceRechargeClient
	for i := range clients {
		index := i
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests[index].Add(1)
			if r.Method != http.MethodConnect || r.Host != "api.binance.com:443" {
				t.Errorf("unexpected proxy destination: %s %s", r.Method, r.Host)
			}
			w.WriteHeader(http.StatusBadGateway)
		}))
		t.Cleanup(proxy.Close)
		cipher, _ := svc.encryptor.Encrypt(proxy.URL)
		client, finish, err := svc.accountClient(&BinanceRechargeAccount{proxyURLCiphertext: cipher})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(finish)
		clients[i] = client
	}
	now := time.Now()
	for i, client := range clients {
		if _, err := client.History(context.Background(), "fixture-key", "fixture-secret", "12345", now.Add(-time.Hour), now); err == nil {
			t.Fatal("failed proxy must fail history")
		}
		if err := client.Probe(context.Background(), "fixture-key", "fixture-secret"); err == nil {
			t.Fatal("failed proxy must fail permission probe")
		}
		bills := client.(binanceRechargeBillsClient)
		if _, err := bills.Bills(context.Background(), "fixture-key", "fixture-secret", "12345", now.Add(-time.Hour), now); err == nil {
			t.Fatal("failed proxy must fail bills")
		}
		if got := requests[i].Load(); got != 3 {
			t.Fatalf("channel %d used its proxy %d times, want 3", i, got)
		}
	}
	if defaultClient.calls != 0 {
		t.Fatal("dedicated proxy failed over to startup client")
	}
	legacy, finish, err := svc.accountClient(&BinanceRechargeAccount{})
	if err != nil || legacy != defaultClient {
		t.Fatal("unconfigured channel did not retain startup route")
	}
	finish()
	for _, account := range []*BinanceRechargeAccount{
		{proxyURLCiphertext: "broken-cipher"},
		{proxyURLCiphertext: "cipher:"},
		{proxyURLCiphertext: "cipher:http://example.test/private"},
		{ProxyPreflight: true},
	} {
		if client, _, err := svc.accountClient(account); !errors.Is(err, errBinanceAccountProxy) || client != nil {
			t.Fatal("unusable dedicated proxy must not return a fallback client")
		}
	}
}

func TestPKBinanceAccountProxyPreflightFailureDoesNotReachBinance(t *testing.T) {
	var preflights, connects atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/_binance/preflight" {
			preflights.Add(1)
			user, password, ok := r.BasicAuth()
			if !ok || user != "fixture-user" || password != "fixture-pass" {
				t.Error("preflight authentication missing")
			}
		} else {
			connects.Add(1)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer proxy.Close()
	proxyURL := strings.Replace(proxy.URL, "http://", "http://fixture-user:fixture-pass@", 1)
	svc := NewBinanceRechargeService(nil, pkBinanceProxyEncryptor{}, &pkBinanceBillsClient{}, nil, nil)
	client, finish, err := svc.accountClient(&BinanceRechargeAccount{proxyURLCiphertext: "cipher:" + proxyURL, ProxyPreflight: true})
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if err := client.Probe(context.Background(), "fixture-key", "fixture-secret"); !errors.Is(err, binancepay.ErrProxyPreflight) {
		t.Fatal("gateway failure did not fail closed")
	}
	if preflights.Load() != 1 || connects.Load() != 0 {
		t.Fatal("failed preflight reached Binance")
	}
}

func pkBinanceAccountRows(proxy string, preflight bool, states ...BinanceRechargeAccount) *sqlmock.Rows {
	var state BinanceRechargeAccount
	if len(states) > 0 {
		state = states[0]
	}
	status, _ := json.Marshal(state.QueryStatus)
	return sqlmock.NewRows([]string{"id", "name", "recipient_uid", "enabled", "ttl", "multiplier", "key", "secret", "proxy", "preflight", "checked", "error", "created", "updated", "has_orders", "next_poll_at", "api_locked_until", "bills_next_query_at", "failure_count", "has_poll_token", "backup", "backup_preflight", "query_status"}).AddRow(1, "fixture", "12345", true, 30, "1.000000", "cipher:key", "cipher:secret", proxy, preflight, nil, state.LastError, time.Now(), time.Now(), true, state.nextPollAt, state.apiLockedUntil, state.billsNextQueryAt, state.failureCount, state.hasPollToken, state.backupProxyURLCiphertext, state.BackupProxyPreflight, status)
}

func TestPKBinanceAccountProxySavePreflightHints(t *testing.T) {
	for _, tc := range []struct {
		name, savedProxy, replacement, wantReason, wantHint string
		create, savedPreflight, preflight, beforeDB         bool
	}{
		{name: "new SOCKS preflight", create: true, replacement: "socks5://fixture-user:fixture-pass@proxy.test:1080", preflight: true, wantReason: "BINANCE_PROXY_PREFLIGHT_UNSUPPORTED"},
		{name: "replacement SOCKS preflight", savedProxy: "http://proxy.test:3128", replacement: "socks5://fixture-user:fixture-pass@proxy.test:1080", preflight: true, wantReason: "BINANCE_PROXY_PREFLIGHT_UNSUPPORTED"},
		{name: "enable saved SOCKS preflight", savedProxy: "socks5://fixture-user:fixture-pass@proxy.test:1080", preflight: true, wantReason: "BINANCE_PROXY_PREFLIGHT_UNSUPPORTED"},
		{name: "enable saved HTTP without authentication", savedProxy: "http://proxy.test:3128", preflight: true, wantReason: "BINANCE_PROXY_PREFLIGHT_UNSUPPORTED"},
		{name: "enable saved HTTP missing password", savedProxy: "http://fixture-user@proxy.test:3128", preflight: true, wantReason: "BINANCE_PROXY_PREFLIGHT_UNSUPPORTED"},
		{name: "malformed replacement gets safe port hint before DB", replacement: "http://fixture-user:fixture-pass@proxy.test:99999", preflight: true, wantReason: "BINANCE_PROXY_INVALID", wantHint: "端口须为 1 至 65535", beforeDB: true},
		{name: "malformed saved proxy stays generic", savedProxy: "http://fixture-user:fixture-pass@proxy.test:99999", preflight: true, wantReason: "BINANCE_PROXY_INVALID"},
		{name: "enable without proxy", preflight: true, wantReason: "BINANCE_PROXY_REQUIRED"},
		{name: "disable saved SOCKS preflight", savedProxy: "socks5://fixture-user:fixture-pass@proxy.test:1080", savedPreflight: true},
		{name: "enable authenticated gateway", savedProxy: "https://fixture-user:fixture-pass@proxy.test:3128", preflight: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			proxyCiphertext := ""
			if tc.savedProxy != "" {
				proxyCiphertext = "cipher:" + tc.savedProxy
			}
			id := int64(1)
			if !tc.beforeDB {
				mock.ExpectBegin()
			}
			if tc.create {
				id = 0
			} else if !tc.beforeDB {
				mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WithArgs(id).WillReturnRows(pkBinanceAccountRows(proxyCiphertext, tc.savedPreflight))
			}
			if tc.wantReason != "" {
				if !tc.beforeDB {
					mock.ExpectRollback()
				}
			} else {
				mock.ExpectQuery(`SELECT poll_token<>'' AND api_locked_until>NOW\(\)`).WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"polling"}).AddRow(false))
				mock.ExpectExec(`UPDATE binance_recharge_accounts SET name=\$2`).WithArgs(id, "fixture", "12345", "cipher:key", "cipher:secret", proxyCiphertext, tc.preflight, true, 30, "1.000000", "", false).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WithArgs(id).WillReturnRows(pkBinanceAccountRows(proxyCiphertext, tc.preflight))
				mock.ExpectCommit()
			}
			svc := NewBinanceRechargeService(db, pkBinanceProxyEncryptor{}, &pkBinanceBillsClient{}, nil, nil)
			account, err := svc.SaveAccount(context.Background(), id, BinanceRechargeAccountInput{Name: "fixture", RecipientUID: "12345", Enabled: true, OrderTTLMinutes: 30, ProxyURL: tc.replacement, ProxyPreflight: &tc.preflight})
			if tc.wantReason != "" {
				if infraerrors.Reason(err) != tc.wantReason || infraerrors.Code(err) != http.StatusBadRequest || account != nil {
					t.Fatalf("save must reject with safe configuration hint %s: %v", tc.wantReason, err)
				}
				if tc.wantHint != "" && !strings.Contains(err.Error(), tc.wantHint) {
					t.Fatal("malformed replacement did not report its safe configuration reason")
				}
				for _, private := range []string{"fixture-user", "fixture-pass", "proxy.test", "cipher:"} {
					if strings.Contains(err.Error(), private) {
						t.Fatal("save error exposed private proxy information")
					}
				}
			} else if err != nil || account == nil || account.ProxyPreflight != tc.preflight {
				t.Fatalf("supported preflight configuration was rejected: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPKBinanceAccountProxySavePreserveClearAndMask(t *testing.T) {
	for _, tc := range []struct {
		name, replacement, wantProxy string
		clear, polling, reject       bool
	}{
		{name: "omitted proxy preserved", wantProxy: "cipher:http://old-user:old-password@proxy.test:3128"},
		{name: "replace encrypted proxy", replacement: "socks5://new-user:new-password@proxy.test:1080", wantProxy: "cipher:socks5://new-user:new-password@proxy.test:1080"},
		{name: "explicit clear", clear: true},
		{name: "active query rejects replacement", replacement: "http://proxy.test:3129", polling: true, reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			old := "cipher:http://old-user:old-password@proxy.test:3128"
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WillReturnRows(pkBinanceAccountRows(old, false))
			if tc.replacement != "" || tc.clear {
				mock.ExpectQuery(`SELECT poll_token<>'' AND api_locked_until>NOW\(\)`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"polling"}).AddRow(tc.polling))
			}
			if tc.reject {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec(`UPDATE binance_recharge_accounts SET name=\$2`).WithArgs(int64(1), "fixture", "12345", "cipher:key", "cipher:secret", tc.wantProxy, false, true, 30, "1.000000", "", false).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WillReturnRows(pkBinanceAccountRows(tc.wantProxy, false))
				mock.ExpectCommit()
			}
			svc := NewBinanceRechargeService(db, pkBinanceProxyEncryptor{}, &pkBinanceBillsClient{}, nil, nil)
			account, err := svc.SaveAccount(context.Background(), 1, BinanceRechargeAccountInput{Name: "fixture", RecipientUID: "12345", Enabled: true, OrderTTLMinutes: 30, ProxyURL: tc.replacement, ClearProxy: tc.clear})
			if (err != nil) != tc.reject {
				t.Fatalf("save error = %v", err)
			}
			if !tc.reject {
				if account.ProxyConfigured != (tc.wantProxy != "") {
					t.Fatal("proxy configuration status incorrect")
				}
				body, err := json.Marshal(account)
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{"old-password", "new-password", "proxy.test", "cipher:", "proxy_url"} {
					if strings.Contains(string(body), secret) {
						t.Fatalf("account response leaks %s", secret)
					}
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
	input := BinanceRechargeAccountInput{Name: "fixture", RecipientUID: "12345", ProxyURL: "http://proxy.test:3128", ClearProxy: true}
	if _, err := normalizeBinanceRechargeAccountInput(input); err == nil {
		t.Fatal("ambiguous clear and replacement accepted")
	}
	redacted := RedactAuditBody([]byte(`{"proxy_url":"http://private-user:private-password@proxy.test:3128","clear_proxy":true}`), "application/json")
	if strings.Contains(redacted, "private-password") || strings.Contains(redacted, "proxy.test") {
		t.Fatal("proxy credentials leaked to audit")
	}
}

type pkBinanceRotatingEncryptor struct{}

func (pkBinanceRotatingEncryptor) Encrypt(value string) (string, error) { return "new:" + value, nil }
func (pkBinanceRotatingEncryptor) Decrypt(value string) (string, error) {
	if strings.HasPrefix(value, "cipher:") {
		return strings.TrimPrefix(value, "cipher:"), nil
	}
	return strings.TrimPrefix(value, "new:"), nil
}

func TestPKBinanceSaveRecoversOnlyChangedConnectionAndKnownFailure(t *testing.T) {
	now := time.Now().UTC()
	networkError := binanceRechargePollError(errors.New("private transport details"))
	for _, tc := range []struct {
		name, proxy, key, secret, lastError string
		locked, token, clear, reset, reject bool
	}{
		{name: "network failure new proxy", proxy: "http://proxy.test:3129", lastError: networkError, reset: true},
		{name: "network failure changed key", key: "repaired-key", lastError: networkError, reset: true},
		{name: "timeout changed secret", secret: "repaired-secret", lastError: binanceRechargePollError(context.DeadlineExceeded), reset: true},
		{name: "clear failed proxy", clear: true, lastError: networkError, reset: true},
		{name: "same proxy is no reset despite new ciphertext", proxy: "http://proxy.test:3128", lastError: networkError},
		{name: "same key and secret is no reset", key: "key", secret: "secret", lastError: networkError},
		{name: "plain save is no reset", lastError: networkError},
		{name: "legacy real rate limit retained", proxy: "http://proxy.test:3129", lastError: binanceRechargeRateLimitMessage},
		{name: "unknown historical error retained", proxy: "http://proxy.test:3129", lastError: "unknown older failure"},
		{name: "hard provider lock retained if error was overwritten", proxy: "http://proxy.test:3129", lastError: networkError, locked: true},
		{name: "active query rejects proxy change", proxy: "http://proxy.test:3129", lastError: networkError, locked: true, token: true, reject: true},
		{name: "active query rejects credential change", key: "repaired-key", lastError: networkError, locked: true, token: true, reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			state := BinanceRechargeAccount{LastError: tc.lastError, nextPollAt: now.Add(16 * time.Minute), billsNextQueryAt: now.Add(16 * time.Minute), failureCount: 5, hasPollToken: tc.token}
			if tc.locked {
				state.apiLockedUntil = now.Add(time.Hour)
			}
			oldProxy := "cipher:http://proxy.test:3128"
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WithArgs(int64(1)).WillReturnRows(pkBinanceAccountRows(oldProxy, false, state))
			changed := (tc.proxy != "" && tc.proxy != "http://proxy.test:3128") || (tc.key != "" && tc.key != "key") || (tc.secret != "" && tc.secret != "secret") || tc.clear
			if changed {
				mock.ExpectQuery(`SELECT poll_token<>'' AND api_locked_until>NOW\(\)`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"polling"}).AddRow(tc.locked && tc.token))
			}
			if tc.reject {
				mock.ExpectRollback()
			} else {
				key, secret, proxy := "cipher:key", "cipher:secret", oldProxy
				if tc.key != "" {
					key = "new:" + tc.key
				}
				if tc.secret != "" {
					secret = "new:" + tc.secret
				}
				if tc.proxy != "" {
					proxy = "new:" + tc.proxy
				}
				if tc.clear {
					proxy = ""
				}
				mock.ExpectExec(`UPDATE binance_recharge_accounts SET name=\$2`).WithArgs(int64(1), "fixture", "12345", key, secret, proxy, false, true, 30, "1.000000", "", false).WillReturnResult(sqlmock.NewResult(0, 1))
				if tc.reset {
					mock.ExpectExec(`UPDATE binance_recharge_accounts SET next_poll_at=NOW\(\),bills_next_query_at=NOW\(\),failure_count=0,last_error='',poll_token='' WHERE id=\$1 AND api_locked_until<=NOW\(\) AND failure_count>0 AND last_error=\$2`).WithArgs(int64(1), tc.lastError).WillReturnResult(sqlmock.NewResult(0, 1))
					state = BinanceRechargeAccount{}
				}
				mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WithArgs(int64(1)).WillReturnRows(pkBinanceAccountRows(proxy, false, state))
				mock.ExpectCommit()
			}
			svc := NewBinanceRechargeService(db, pkBinanceRotatingEncryptor{}, &pkBinanceBillsClient{}, nil, nil)
			account, err := svc.SaveAccount(context.Background(), 1, BinanceRechargeAccountInput{Name: "fixture", RecipientUID: "12345", Enabled: true, OrderTTLMinutes: 30, ProxyURL: tc.proxy, APIKey: tc.key, APISecret: tc.secret, ClearProxy: tc.clear})
			if tc.reject {
				if infraerrors.Reason(err) != "BINANCE_ACCOUNT_POLLING" {
					t.Fatalf("active query not preserved: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if tc.reset && (account.CheckState != "ready" || account.RetryAfterSeconds != 0) {
					t.Fatalf("repair not ready: %+v", account)
				}
				if !tc.reset && account.CheckState == "ready" {
					t.Fatal("unchanged or protected cooldown was erased")
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPKBinanceCooldownSeparatesWaitReasonsWithoutCallingProvider(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, state, lastError string
		next, lock, bills      time.Time
		token                  bool
		seconds                int64
	}{
		{name: "running", state: "running", lock: now.Add(80 * time.Second), token: true, seconds: 80},
		{name: "normal interval", state: "interval_wait", next: now.Add(20 * time.Second), bills: now.Add(20 * time.Second), seconds: 20},
		{name: "network backoff", state: "retry_wait", next: now.Add(960 * time.Second), bills: now.Add(960 * time.Second), lastError: binanceRechargePollError(errors.New("network")), seconds: 960},
		{name: "legacy rate limit", state: "rate_limited", next: now.Add(120 * time.Second), bills: now.Add(180 * time.Second), lastError: binanceRechargeRateLimitMessage, seconds: 180},
		{name: "provider lock survives overwritten error", state: "rate_limited", lock: now.Add(3600 * time.Second), lastError: "private stale error", seconds: 3600},
	} {
		for _, bills := range []bool{false, true} {
			t.Run(tc.name+strconv.FormatBool(bills), func(t *testing.T) {
				db, mock, err := sqlmock.New()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = db.Close() }()
				mock.ExpectQuery(`UPDATE binance_recharge_accounts`).WillReturnRows(sqlmock.NewRows([]string{"failure_count"}))
				mock.ExpectQuery(`SELECT next_poll_at,api_locked_until,bills_next_query_at`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"next", "locked", "bills", "token", "error"}).AddRow(tc.next, tc.lock, tc.bills, tc.token, tc.lastError))
				client := &pkBinanceBillsClient{}
				svc := NewBinanceRechargeService(db, pkBinanceLifecycleEncryptor{}, client, nil, nil)
				if bills {
					_, err = svc.Bills(context.Background(), 1, now.Add(-time.Hour), now)
				} else {
					_, err = svc.CheckAccount(context.Background(), 1)
				}
				if err == nil || infraerrors.Code(err) != http.StatusTooManyRequests {
					t.Fatalf("expected cooldown: %v", err)
				}
				status := infraerrors.FromError(err)
				seconds, _ := strconv.ParseInt(status.Metadata["retry_after_seconds"], 10, 64)
				if status.Metadata["check_state"] != tc.state || seconds < tc.seconds-2 || seconds > tc.seconds {
					t.Fatalf("incorrect state metadata: %+v", status.Metadata)
				}
				if _, err := time.Parse(time.RFC3339, status.Metadata["check_available_at"]); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(err.Error(), "private stale error") {
					t.Fatal("unsafe stored error exposed")
				}
				if client.calls != 0 {
					t.Fatal("cooldown queried provider")
				}
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

type pkBinanceCheckFailureClient struct {
	err   error
	calls int
}

func (c *pkBinanceCheckFailureClient) History(context.Context, string, string, string, time.Time, time.Time) ([]binancepay.Receipt, error) {
	c.calls++
	return nil, c.err
}
func (*pkBinanceCheckFailureClient) Probe(context.Context, string, string) error { return nil }
func (*pkBinanceCheckFailureClient) CloseIdleConnections()                       {}

func TestPKBinancePollPersistsProviderLockOnlyForRateLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		delay   float64
		limited bool
	}{
		{name: "429", err: &binancepay.RateLimitError{StatusCode: 429, RetryAfter: 2 * time.Minute}, delay: 120, limited: true},
		{name: "418", err: &binancepay.RateLimitError{StatusCode: 418, RetryAfter: 24 * time.Hour}, delay: 86400, limited: true},
		{name: "network", err: errors.New("private proxy address"), delay: 60},
		{name: "proxy authentication", err: &binancepay.RequestError{Stage: "time_sync", Reason: "proxy_auth", HTTPStatus: 407}, delay: 60},
		{name: "IP whitelist", err: &binancepay.RequestError{Stage: "pay_history", Reason: "api_key_ip_permission", HTTPStatus: 401, ProviderCode: "-2015"}, delay: 60},
		{name: "regional access", err: &binancepay.RequestError{Stage: "time_sync", Reason: "region_restricted", HTTPStatus: 451}, delay: 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			mock.ExpectQuery(`UPDATE binance_recharge_accounts SET next_poll_at`).WithArgs(int64(1), sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"failure_count"}).AddRow(0))
			mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WithArgs(int64(1)).WillReturnRows(pkBinanceAccountRows("", false))
			mock.ExpectExec(`UPDATE binance_recharge_accounts SET last_checked_at=NOW\(\),last_error=\$3, next_poll_at=NOW\(\)\+make_interval\(secs=>\$4\),failure_count=\$5,poll_token='', api_locked_until=CASE WHEN \$6 THEN NOW\(\)\+make_interval\(secs=>\$4\) ELSE to_timestamp\(0\) END,`).WithArgs(int64(1), sqlmock.AnyArg(), binanceRechargePollError(tc.err), tc.delay, 1, tc.limited).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(`UPDATE binance_recharge_orders SET status='EXPIRED'`).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
			client := &pkBinanceCheckFailureClient{err: tc.err}
			svc := NewBinanceRechargeService(db, pkBinanceLifecycleEncryptor{}, client, nil, nil)
			_, err = svc.CheckAccount(context.Background(), 1)
			if infraerrors.Reason(err) != "BINANCE_CHECK_FAILED" || client.calls != 1 {
				t.Fatalf("unexpected result: %v", err)
			}
			if strings.Contains(err.Error(), "private proxy address") {
				t.Fatal("private network error escaped")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func (c *pkBinanceLifecycleClient) History(ctx context.Context, _, _, _ string, _, _ time.Time) ([]binancepay.Receipt, error) {
	close(c.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*pkBinanceLifecycleClient) Probe(context.Context, string, string) error { return nil }
func (*pkBinanceLifecycleClient) CloseIdleConnections()                       {}

type pkBinanceLifecycleEncryptor struct{}

func (pkBinanceLifecycleEncryptor) Encrypt(s string) (string, error) { return s, nil }
func (pkBinanceLifecycleEncryptor) Decrypt(s string) (string, error) { return s, nil }

type pkBinanceBillsClient struct {
	calls int
	err   error
}

func (*pkBinanceBillsClient) History(context.Context, string, string, string, time.Time, time.Time) ([]binancepay.Receipt, error) {
	panic("a read-only bill query must never invoke settlement history")
}
func (*pkBinanceBillsClient) Probe(context.Context, string, string) error { return nil }
func (*pkBinanceBillsClient) CloseIdleConnections()                       {}
func (c *pkBinanceBillsClient) Bills(context.Context, string, string, string, time.Time, time.Time) (*binancepay.BillsPage, error) {
	c.calls++
	return &binancepay.BillsPage{Items: []binancepay.Bill{{ID: "bill", Amount: "1.12345678", Direction: "incoming"}}, Total: 1, Limit: 100}, c.err
}

func TestPKBinanceBillsReadDoesNotMutateOrdersOrRescheduleCollection(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`UPDATE binance_recharge_accounts a`).WillReturnRows(sqlmock.NewRows([]string{"failure_count"}).AddRow(0))
	mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WillReturnRows(pkBinanceAccountRows("", false))
	// The only successful-read mutation releases the shared API lock. There is
	// no order, receipt, user balance or next_poll_at update on this path.
	mock.ExpectExec(`UPDATE binance_recharge_accounts SET api_locked_until=to_timestamp\(0\),poll_token='' WHERE id=\$1 AND poll_token=\$2`).WillReturnResult(sqlmock.NewResult(0, 1))
	client := &pkBinanceBillsClient{}
	svc := NewBinanceRechargeService(db, pkBinanceLifecycleEncryptor{}, client, nil, nil)
	now := time.Now()
	page, err := svc.Bills(context.Background(), 1, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 || page.Total != 1 || page.Items[0].Amount != "1.12345678" {
		t.Fatal("live bill result changed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPKBinanceBillsRespectSharedLeaseAndProviderBackoff(t *testing.T) {
	for _, limited := range []bool{false, true} {
		t.Run(map[bool]string{false: "busy account", true: "rate limited"}[limited], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			client := &pkBinanceBillsClient{}
			if !limited {
				mock.ExpectQuery(`UPDATE binance_recharge_accounts a`).WillReturnRows(sqlmock.NewRows([]string{"failure_count"}))
				mock.ExpectQuery(`SELECT next_poll_at,api_locked_until,bills_next_query_at`).WillReturnRows(sqlmock.NewRows([]string{"next", "locked", "bills", "token", "error"}).AddRow(time.Now(), time.Now().Add(time.Minute), time.Now(), true, ""))
			} else {
				client.err = &binancepay.RateLimitError{StatusCode: 418, RetryAfter: 24 * time.Hour}
				mock.ExpectQuery(`UPDATE binance_recharge_accounts a`).WillReturnRows(sqlmock.NewRows([]string{"failure_count"}).AddRow(0))
				mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WillReturnRows(pkBinanceAccountRows("", false))
				mock.ExpectExec(`UPDATE binance_recharge_accounts SET api_locked_until=CASE WHEN \$6 THEN NOW\(\)\+make_interval\(secs=>\$4\) ELSE to_timestamp\(0\) END,poll_token='',failure_count=\$3, next_poll_at=GREATEST`).WithArgs(int64(1), sqlmock.AnyArg(), 1, float64(86400), "币安 API 限流，已延后查收", true).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			svc := NewBinanceRechargeService(db, pkBinanceLifecycleEncryptor{}, client, nil, nil)
			now := time.Now()
			if _, err := svc.Bills(context.Background(), 1, now.Add(-time.Hour), now); err == nil {
				t.Fatal("busy or rate-limited bill query must fail")
			}
			if !limited && client.calls != 0 {
				t.Fatal("busy account bypassed the shared lock")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPKBinanceLifecycle_StopWithoutStart(t *testing.T) {
	svc := NewBinanceRechargeService(nil, nil, nil, nil, nil)
	svc.Stop()
	svc.Start()
	svc.Stop()
	if svc.started || !svc.stopped {
		t.Fatal("stopped service must never start later")
	}
}

func TestPKBinanceLifecycle_StopCancelsPollingAndWaits(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`SELECT a.id FROM binance_recharge_accounts`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`UPDATE binance_recharge_accounts SET next_poll_at`).WillReturnRows(sqlmock.NewRows([]string{"failure_count"}).AddRow(0))
	mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WillReturnRows(pkBinanceAccountRows("", false))
	mock.ExpectExec(`UPDATE binance_recharge_accounts SET last_checked_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE binance_recharge_orders SET status='EXPIRED'`).WillReturnResult(sqlmock.NewResult(0, 0))
	client := &pkBinanceLifecycleClient{entered: make(chan struct{})}
	svc := NewBinanceRechargeService(db, pkBinanceLifecycleEncryptor{}, client, nil, nil)
	svc.Start()
	svc.Start()
	select {
	case <-client.entered:
	case <-time.After(3 * time.Second):
		svc.Stop()
		t.Fatal("poll never reached history client")
	}
	done := make(chan struct{})
	go func() { svc.Stop(); svc.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not cancel and join history request")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProvideTimingWheelService_ReturnsError(t *testing.T) {
	original := newTimingWheel
	t.Cleanup(func() { newTimingWheel = original })

	newTimingWheel = func(_ time.Duration, _ int, _ collection.Execute) (*collection.TimingWheel, error) {
		return nil, errors.New("boom")
	}

	svc, err := ProvideTimingWheelService()
	if err == nil {
		t.Fatalf("期望返回 error，但得到 nil")
	}
	if svc != nil {
		t.Fatalf("期望返回 nil svc，但得到非空")
	}
}

func TestProvideTimingWheelService_Success(t *testing.T) {
	svc, err := ProvideTimingWheelService()
	if err != nil {
		t.Fatalf("期望 err 为 nil，但得到: %v", err)
	}
	if svc == nil {
		t.Fatalf("期望 svc 非空，但得到 nil")
	}
	svc.Stop()
}

func TestBackgroundStartGate_DefaultStartsSynchronously(t *testing.T) {
	gate := newBackgroundStartGate("")
	t.Cleanup(gate.stop)
	called := false
	gate.start("service", func() { called = true })
	gate.start("optional", nil)
	if !called {
		t.Fatal("default startup must complete before the provider returns")
	}
}

func TestBackgroundStartGate_ExistingMarkerStartsSynchronously(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	gate := newBackgroundStartGate(path)
	t.Cleanup(gate.stop)
	called := false
	gate.start("service", func() { called = true })
	if !called {
		t.Fatal("a persisted marker must restore synchronous startup after restart")
	}
}

func TestBackgroundStartGate_AtomicActivationStartsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active")
	gate := newBackgroundStartGate(path)
	gate.pollInterval = time.Millisecond
	t.Cleanup(gate.stop)
	started := make(chan string, 3)
	gate.start("first", func() { started <- "first" })
	gate.start("second", func() { started <- "second" })
	select {
	case name := <-started:
		t.Fatalf("%s started before activation", name)
	default:
	}
	if err := os.WriteFile(path+".tmp", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"first", "second"} {
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not start after activation", want)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// Activation remains effective, including registrations after the file is removed.
	gate.start("third", func() { started <- "third" })
	select {
	case got := <-started:
		if got != "third" {
			t.Fatalf("unexpected repeated startup: %q", got)
		}
	default:
		t.Fatal("an activated gate must start later providers synchronously")
	}
	gate.stop()
	select {
	case name := <-started:
		t.Fatalf("%s started more than once", name)
	default:
	}
}

func TestBackgroundStartGate_StopCancelsPendingAndFutureStarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active")
	gate := newBackgroundStartGate(path)
	gate.pollInterval = time.Millisecond
	t.Cleanup(gate.stop)
	var starts atomic.Int32
	gate.start("pending", func() { starts.Add(1) })
	gate.stop()
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	gate.start("late", func() { starts.Add(1) })
	if got := starts.Load(); got != 0 {
		t.Fatalf("shutdown must prevent pending and late starts, got %d", got)
	}
}

func TestBackgroundStartGate_StopWaitsForStartingCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active")
	gate := newBackgroundStartGate(path)
	gate.pollInterval = time.Millisecond
	entered := make(chan struct{})
	release := make(chan struct{})
	// Always release the callback before cleanup, including assertion failures.
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		gate.stop()
	})
	gate.start("blocking", func() {
		close(entered)
		<-release
	})
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("callback did not begin")
	}
	stopped := make(chan struct{})
	go func() {
		gate.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("shutdown returned while a Start callback was still running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish after Start returned")
	}
	var lateStarts atomic.Int32
	gate.start("after-cleanup", func() { lateStarts.Add(1) })
	if lateStarts.Load() != 0 {
		t.Fatal("service started after cleanup")
	}
}

func TestBackgroundStartGate_RejectsDirectoryActivation(t *testing.T) {
	gate := newBackgroundStartGate(t.TempDir())
	gate.pollInterval = time.Millisecond
	t.Cleanup(gate.stop)
	var starts atomic.Int32
	gate.start("service", func() { starts.Add(1) })
	gate.stop()
	if starts.Load() != 0 {
		t.Fatal("a directory must not activate background services")
	}
}

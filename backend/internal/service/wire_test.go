package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
	"github.com/zeromicro/go-zero/core/collection"
)

type pkBinanceLifecycleClient struct{ entered chan struct{} }

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
	mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "recipient_uid", "enabled", "ttl", "key", "secret", "checked", "error", "created", "updated", "has_orders"}).AddRow(1, "test", "12345", true, 30, "test-key", "test-secret", nil, "", time.Now(), time.Now(), true))
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
				mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
			} else {
				client.err = &binancepay.RateLimitError{StatusCode: 418, RetryAfter: 24 * time.Hour}
				mock.ExpectQuery(`UPDATE binance_recharge_accounts a`).WillReturnRows(sqlmock.NewRows([]string{"failure_count"}).AddRow(0))
				mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "recipient_uid", "enabled", "ttl", "key", "secret", "checked", "error", "created", "updated", "has_orders"}).AddRow(1, "test", "12345", true, 30, "test-key", "test-secret", nil, "", time.Now(), time.Now(), true))
				mock.ExpectExec(`UPDATE binance_recharge_accounts SET api_locked_until=to_timestamp\(0\),poll_token='',failure_count=\$3, next_poll_at=GREATEST`).WithArgs(int64(1), sqlmock.AnyArg(), 1, float64(86400), "币安 API 限流，已延后查收").WillReturnResult(sqlmock.NewResult(0, 1))
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
	mock.ExpectQuery(`SELECT a.id, a.name, a.recipient_uid`).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "recipient_uid", "enabled", "ttl", "key", "secret", "checked", "error", "created", "updated", "has_orders"}).AddRow(1, "test", "12345", true, 30, "test-key", "test-secret", nil, "", time.Now(), time.Now(), true))
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

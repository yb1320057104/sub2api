//go:build unit

package bepusdt

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/v03413/bepusdt/app/model"
)

func TestBEpusdtEmbeddedWalletAllocationAndProtection(t *testing.T) {
	cleanup, err := InitializeForTest(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(cleanup)
	ctx := context.Background()
	require.Error(t, SaveWallet(ctx, model.Wallet{Address: "invalid", TradeType: "usdt.erc20", Status: 1}))
	require.NoError(t, SaveWallet(ctx, model.Wallet{Name: "Ethereum wallet", Address: "0x1111111111111111111111111111111111111111", TradeType: "usdt.erc20", Status: 1}))
	require.NoError(t, SaveRate(ctx, "CNY", "USDT", "7.2"))
	wallets, err := Wallets(ctx)
	require.NoError(t, err)
	require.Len(t, wallets, 1)
	request := CreateRequest{OrderID: "first-order", Amount: "28.80", TradeType: "usdt.erc20", Currency: "CNY", NotifyURL: "https://merchant.example/api/v1/payment/webhook/bepusdt", ReturnURL: "https://merchant.example/payment/result", ExpiresAt: time.Now().Add(5 * time.Minute)}
	first, err := Create(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 4.0, decimal.RequireFromString(first.Amount).InexactFloat64())
	require.Equal(t, wallets[0].Address, first.Address)
	request.OrderID = "second-order"
	second, err := Create(ctx, request)
	require.NoError(t, err)
	require.NotEqual(t, first.Amount, second.Amount, "simultaneous orders need distinct transfer amounts")
	wallets[0].Status = 0
	require.ErrorContains(t, SaveWallet(ctx, wallets[0]), "pending payments")
	require.ErrorContains(t, DeleteWallet(ctx, wallets[0].ID), "pending payments")
	require.NoError(t, Cancel(ctx, first.TradeId))
	require.Error(t, Cancel(ctx, first.TradeId))
	require.NoError(t, Cancel(ctx, second.TradeId))
	require.NoError(t, DeleteWallet(ctx, wallets[0].ID))
}

func TestBEpusdtEmbeddedBEP20AllocationNormalizesWalletAddress(t *testing.T) {
	cleanup, err := InitializeForTest(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(cleanup)
	ctx := context.Background()
	address := "0xAbCdEf1234567890AbCdEf1234567890AbCdEf12"
	require.NoError(t, SaveWallet(ctx, model.Wallet{Address: address, TradeType: "usdt.bep20", Status: 1}))
	require.NoError(t, SaveRate(ctx, "USD", "USDT", "1"))
	req := CreateRequest{OrderID: "mixed-case-first", Amount: "28.80", TradeType: "usdt.bep20", Currency: "USD", ExpiresAt: time.Now().Add(5 * time.Minute)}
	first, err := Create(ctx, req)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(address), first.Address, "new EVM payment addresses are normalized")
	// Reproduce a historical order whose display address kept its original
	// casing while the scanner matched the normalized address.
	require.NoError(t, model.Db.Model(&model.Order{}).Where("id = ?", first.ID).Update("address", address).Error)
	first, err = Order(ctx, first.TradeId)
	require.NoError(t, err)
	require.Equal(t, address, first.Address)
	require.NotEqual(t, first.Address, first.MatchAddress)
	req.OrderID = "mixed-case-second"
	second, err := Create(ctx, req)
	require.NoError(t, err)
	require.NotEqual(t, first.Amount, second.Amount, "normalized wallet address must reserve the first payment amount")
	// Existing records without a normalized matching address remain reserved.
	require.NoError(t, model.Db.Model(&model.Order{}).Where("id = ?", first.ID).Update("match_address", "").Error)
	legacy, err := Order(ctx, first.TradeId)
	require.NoError(t, err)
	require.Equal(t, address, legacy.Address)
	require.Empty(t, legacy.MatchAddress)
	req.OrderID = "mixed-case-legacy"
	third, err := Create(ctx, req)
	require.NoError(t, err)
	require.NotEqual(t, first.Amount, third.Amount)
	require.NotEqual(t, second.Amount, third.Amount)
}

func TestBEpusdtEmbeddedPreservesMerchantDeadline(t *testing.T) {
	cleanup, err := InitializeForTest(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(cleanup)
	ctx := context.Background()
	require.NoError(t, SaveWallet(ctx, model.Wallet{Address: "0x1111111111111111111111111111111111111111", TradeType: "usdt.bep20", Status: 1}))
	require.NoError(t, SaveRate(ctx, "USD", "USDT", "1"))
	for _, timeout := range []time.Duration{2 * time.Minute, 5 * time.Minute, 2 * time.Hour} {
		t.Run(timeout.String(), func(t *testing.T) {
			deadline := time.Now().Add(timeout)
			order, err := Create(ctx, CreateRequest{OrderID: "deadline-" + timeout.String(), Amount: "10", TradeType: "usdt.bep20", Currency: "USD", ExpiresAt: deadline})
			require.NoError(t, err)
			require.True(t, deadline.Equal(order.ExpiredAt), "engine deadline must equal the merchant deadline")
			saved, err := Order(ctx, order.TradeId)
			require.NoError(t, err)
			require.True(t, deadline.Equal(saved.ExpiredAt), "deadline must survive persistence")
		})
	}
	_, err = Create(ctx, CreateRequest{OrderID: "expired-deadline", Amount: "10", TradeType: "usdt.bep20", Currency: "USD", ExpiresAt: time.Now().Add(-time.Second)})
	require.ErrorContains(t, err, "expired")
}

func TestBEpusdtEmbeddedSettingsAllowlistAndPersistence(t *testing.T) {
	dir := t.TempDir()
	cleanup, err := InitializeForTest(dir)
	require.NoError(t, err)
	ctx := context.Background()
	secret, err := Token()
	require.NoError(t, err)
	require.Len(t, secret, 64)
	require.Equal(t, "1", model.GetC(model.BlockOffsetConfirm))
	require.Error(t, SaveSettings(ctx, map[string]string{"api_auth_token": "tampered"}))
	require.Error(t, SaveSettings(ctx, map[string]string{"rate_sync_interval": "0"}))
	require.Error(t, SaveSettings(ctx, map[string]string{"rpc_endpoint_tron": "https://grpc.example"}))
	require.Error(t, SaveSettings(ctx, map[string]string{"rpc_endpoint_tron": "grpc.example:70000"}))
	require.Error(t, SaveSettings(ctx, map[string]string{"rpc_endpoint_base": "not-a-url"}))
	require.Error(t, SaveSettings(ctx, map[string]string{"rate_sync_coingecko_api_url": "file:///etc/passwd"}))
	require.NoError(t, SaveSettings(ctx, map[string]string{"rpc_endpoint_tron": "grpc.example:50051"}))
	require.NoError(t, SaveSettings(ctx, map[string]string{"rpc_endpoint_ethereum": "https://rpc.example", "rate_sync_interval": "3600"}))
	cleanup()
	cleanup, err = InitializeForTest(dir)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	saved, err := Settings()
	require.NoError(t, err)
	require.Equal(t, "https://rpc.example", saved["rpc_endpoint_ethereum"])
	persisted, err := Token()
	require.NoError(t, err)
	require.Equal(t, secret, persisted, "restart must preserve callback verification")
}

func TestSharedWorkerRoleRejectsMixedHAFlags(t *testing.T) {
	for _, tc := range []struct {
		worker, background      string
		fallback, want, invalid bool
	}{
		{"", "", true, true, false}, {"", "", false, false, false},
		{"true", "active", false, true, false}, {"false", "disabled", true, false, false},
		{"true", "disabled", true, false, true}, {"false", "active", false, false, true},
		{"", "active", true, false, true}, {"true", "", true, false, true}, {"1", "active", true, false, true},
	} {
		got, err := sharedWorkerRole(tc.fallback, tc.worker, tc.background)
		if tc.invalid {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		}
	}
}

type fenceTestConnector struct {
	opened  int
	granted bool
	last    *fenceTestConn
}

func (c *fenceTestConnector) Driver() driver.Driver { return nil }
func (c *fenceTestConnector) Connect(context.Context) (driver.Conn, error) {
	c.opened++
	c.last = &fenceTestConn{granted: c.granted}
	return c.last, nil
}

type fenceTestConn struct{ granted, closed bool }

func (c *fenceTestConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *fenceTestConn) Close() error                        { c.closed = true; return nil }
func (c *fenceTestConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c *fenceTestConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &fenceTestRows{granted: c.granted}, nil
}

type fenceTestRows struct{ granted, read bool }

func (r *fenceTestRows) Columns() []string { return []string{"locked"} }
func (r *fenceTestRows) Close() error      { return nil }
func (r *fenceTestRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	values[0] = r.granted
	return nil
}

func TestScannerConnectorForbidsTransparentReconnect(t *testing.T) {
	base := &fenceTestConnector{granted: true}
	c := &scannerConnector{base: base}
	_, err := c.Connect(context.Background())
	require.ErrorIs(t, err, errScannerFence)
	require.Zero(t, base.opened)
	c.arm()
	conn, err := c.Connect(context.Background())
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	_, err = c.Connect(context.Background())
	require.ErrorIs(t, err, errScannerFence)
	require.Equal(t, 1, base.opened)
	// Only the supervisor, after joining the previous term, can arm another.
	c.arm()
	_, err = c.Connect(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, base.opened)
}

func TestScannerConnectorClosesUnownedSession(t *testing.T) {
	base := &fenceTestConnector{granted: false}
	c := &scannerConnector{base: base, armed: true}
	_, err := c.Connect(context.Background())
	require.ErrorIs(t, err, errScannerFence)
	require.True(t, base.last.closed)
}

// The explicit test DSN must name an isolated, disposable database. No test
// connects by default, and scanner callbacks are replaced before activation.
func TestSharedPostgresMultiProcess(t *testing.T) {
	dsn := os.Getenv("BEPUSDT_ISOLATED_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL not supplied")
	}
	require.Equal(t, "1", os.Getenv("BEPUSDT_DISPOSABLE_DATABASE"))
	if action := os.Getenv("BEPUSDT_TEST_CHILD"); action != "" {
		runSharedChild(t, dsn, action)
		return
	}
	t.Setenv("SUB2API_WORKER_ONLY", "true")
	t.Setenv("SUB2API_BACKGROUND_MODE", "active")
	SetDataDir(t.TempDir())
	var started, joined atomic.Int32
	var callbacks sync.WaitGroup
	oldStart, oldWait := startSharedScanner, waitSharedScanner
	oldResult := sharedTermResult
	sharedTermResult = func(err error) { t.Logf("isolated scanner term: %v", err) }
	startSharedScanner = func(ctx context.Context) {
		started.Add(1)
		callbacks.Add(1)
		go func() { defer callbacks.Done(); <-ctx.Done(); time.Sleep(100 * time.Millisecond); joined.Add(1) }()
	}
	waitSharedScanner = callbacks.Wait
	t.Cleanup(func() { startSharedScanner, waitSharedScanner = oldStart, oldWait; sharedTermResult = oldResult })
	require.NoError(t, ConfigureSharedPostgres(dsn, true))
	require.NoError(t, Resume())
	t.Cleanup(func() { Stop(); shared.enabled = false; shared.cancel = nil; runtime.ready = false })
	require.Eventually(t, func() bool { return ensureShared() == nil }, 90*time.Second, 100*time.Millisecond)
	require.Zero(t, started.Load(), "an unconfigured worker must remain idle")
	invoke := func(action, out string) *exec.Cmd {
		path, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(path, "-test.run=^TestSharedPostgresMultiProcess$", "-test.timeout=180s")
		cmd.Env = append(os.Environ(), "BEPUSDT_TEST_CHILD="+action, "BEPUSDT_TEST_OUTPUT="+out, "SUB2API_WORKER_ONLY=false", "SUB2API_BACKGROUND_MODE=disabled")
		return cmd
	}
	firstFile := filepath.Join(t.TempDir(), "first.json")
	output, err := invoke("setup", firstFile).CombinedOutput()
	require.NoError(t, err, string(output))
	var first map[string]string
	raw, err := os.ReadFile(firstFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &first))
	require.Len(t, first["token"], 64)
	require.Eventually(t, func() bool { s, e := sharedSnapshot(context.Background()); return e == nil && s.Active }, 10*time.Second, 100*time.Millisecond)
	require.EqualValues(t, 1, started.Load())
	base, err := pq.NewConnector(dsn)
	require.NoError(t, err)
	other := &scannerConnector{base: base, armed: true}
	_, err = other.Connect(context.Background())
	require.Error(t, err, "a second scanner cannot own this database")
	files := []string{filepath.Join(t.TempDir(), "orders-a.json"), filepath.Join(t.TempDir(), "orders-b.json")}
	commands := []*exec.Cmd{invoke("allocate-a", files[0]), invoke("allocate-b", files[1])}
	var outputs [2]strings.Builder
	for i, c := range commands {
		c.Stdout = &outputs[i]
		c.Stderr = &outputs[i]
		require.NoError(t, c.Start())
	}
	for i, c := range commands {
		require.NoError(t, c.Wait(), outputs[i].String())
	}
	amounts := make(map[string]bool)
	for _, file := range files {
		var items []string
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &items))
		for _, amount := range items {
			require.False(t, amounts[amount], "cross-process amounts must be unique")
			amounts[amount] = true
		}
	}
	require.Len(t, amounts, 6)
	output, err = invoke("protect", filepath.Join(t.TempDir(), "protect.json")).CombinedOutput()
	require.NoError(t, err, string(output))
	control, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer control.Close()
	_, err = control.Exec("UPDATE bep_conf SET v = $1 WHERE k = $2", "https://changed.invalid", model.RpcEndpointEthereum)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return model.GetC(model.RpcEndpointEthereum) == "https://changed.invalid" }, 5*time.Second, 100*time.Millisecond)
	var pid int
	require.NoError(t, model.Db.Raw("SELECT pg_backend_pid()").Scan(&pid).Error)
	_, err = control.Exec("SELECT pg_terminate_backend($1)", pid)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return joined.Load() == 1 && started.Load() == 2 }, 90*time.Second, 100*time.Millisecond)
	secondFile := filepath.Join(t.TempDir(), "second.json")
	output, err = invoke("token", secondFile).CombinedOutput()
	require.NoError(t, err, string(output))
	var second map[string]string
	raw, err = os.ReadFile(secondFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &second))
	require.Equal(t, first["token"], second["token"])
	Stop()
	require.Equal(t, started.Load(), joined.Load(), "shutdown must join callbacks before returning")
	conn, err := other.Connect(context.Background())
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func runSharedChild(t *testing.T, dsn, action string) {
	SetDataDir(t.TempDir())
	startSharedScanner = func(context.Context) { t.Fatal("API must never start scanners") }
	require.NoError(t, ConfigureSharedPostgres(dsn, false))
	defer Stop()
	require.NoError(t, Resume())
	require.Nil(t, shared.cancel, "API Resume remains inert")
	probeOnCreate.Store(false)
	ctx := context.Background()
	var result any
	switch {
	case action == "setup":
		require.NoError(t, SaveWallet(ctx, model.Wallet{Address: "0x1111111111111111111111111111111111111111", TradeType: "usdt.erc20", Status: 1}))
		require.NoError(t, SaveRate(ctx, "USD", "USDT", "1"))
		fallthrough
	case action == "token":
		token, err := Token()
		require.NoError(t, err)
		result = map[string]string{"token": token}
	case strings.HasPrefix(action, "allocate"):
		amounts := []string{}
		for i := 0; i < 3; i++ {
			order, err := Create(ctx, CreateRequest{OrderID: fmt.Sprintf("%s-%d", action, i), Amount: "10", TradeType: "usdt.erc20", Currency: "USD", ExpiresAt: time.Now().Add(5 * time.Minute)})
			require.NoError(t, err)
			amounts = append(amounts, order.Amount)
		}
		result = amounts
	case action == "protect":
		wallets, err := Wallets(ctx)
		require.NoError(t, err)
		require.Len(t, wallets, 1)
		wallets[0].Status = 0
		require.ErrorContains(t, SaveWallet(ctx, wallets[0]), "pending payments")
		require.ErrorContains(t, DeleteWallet(ctx, wallets[0].ID), "pending payments")
	default:
		t.Fatal("unexpected child action")
	}
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(os.Getenv("BEPUSDT_TEST_OUTPUT"), raw, 0600))
}

package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

const accountCandyProxyAttempts = 3

// Native account egress and login proxies are separate pools. Rotation is
// temporary for this probe: never persist the replacement into the account.
// The original immutable account identity continues to guard every attempt.
func runAccountCandyWithProxyRetry(
	ctx context.Context,
	db *sql.DB,
	tests *AccountTestService,
	account, credential *Account,
	cfg *AccountCandyTestConfig,
	beforeAttempt func(context.Context, *Account, int) error,
) (string, error) {
	if account == nil || credential == nil || tests == nil {
		return "", candyProbeFailure("测试账号或服务不可用；未转组。")
	}
	ctx = context.WithValue(ctx, accountCandyProxyRetryKey{}, true)
	ctx = tlsfingerprint.WithConnectTimeout(ctx, 20*time.Second)
	routeAccount := *account
	used := make(map[string]struct{}, accountCandyProxyAttempts)
	var after int64
	for attempt := 1; attempt <= accountCandyProxyAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		route, err := accountCandyProxyURL(&routeAccount)
		if err != nil {
			return "", err
		}
		used[route] = struct{}{}
		if beforeAttempt != nil {
			if err = beforeAttempt(ctx, &routeAccount, attempt); err != nil {
				return "", err
			}
		}
		if attempt > 1 {
			var unchanged bool
			err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM proxies WHERE id=$1 AND protocol=$2 AND host=$3 AND port=$4 AND COALESCE(username,'')=$5 AND COALESCE(password,'')=$6 AND deleted_at IS NULL AND status='active' AND (expires_at IS NULL OR expires_at>NOW()))`, routeAccount.Proxy.ID, routeAccount.Proxy.Protocol, routeAccount.Proxy.Host, routeAccount.Proxy.Port, routeAccount.Proxy.Username, routeAccount.Proxy.Password).Scan(&unchanged)
			if err != nil || !unchanged {
				return "", candyProbeFailure("本轮备用代理已更改或不可用，已停止测试；未转组。")
			}
		}
		text, err := tests.runAccountCandyProbe(ctx, &routeAccount, credential, cfg)
		if err == nil || !accountCandyCanChangeProxy(err) {
			return text, err
		}
		if attempt == accountCandyProxyAttempts {
			return "", candyProbeFailure("账号测试连接失败，已在本轮尝试3个不同出口；未转组。")
		}
		proxy, next, err := nextAccountCandyProxy(ctx, db, after, used)
		if err != nil {
			return "", err
		}
		after = next
		if proxy == nil {
			return "", candyProbeFailure(fmt.Sprintf("账号测试连接失败，本轮已尝试%d个出口；IP管理没有其他可用代理，未转组。", attempt))
		}
		// The new account value owns only its transient route. The credential
		// snapshot and saved proxy binding remain unchanged for admission/CAS.
		routeAccount.ProxyID, routeAccount.Proxy = &proxy.ID, proxy
		timer := time.NewTimer(time.Duration(attempt) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return "", candyProbeFailure("账号测试连接失败，已停止换代理重试；未转组。")
}

func nextAccountCandyProxy(ctx context.Context, db *sql.DB, after int64, used map[string]struct{}) (*Proxy, int64, error) {
	if db == nil {
		return nil, after, nil
	}
	for {
		proxy := &Proxy{}
		err := db.QueryRowContext(ctx, `SELECT id,protocol,host,port,COALESCE(username,''),COALESCE(password,''),status,expires_at FROM proxies WHERE id>$1 AND deleted_at IS NULL AND status='active' AND (expires_at IS NULL OR expires_at>NOW()) ORDER BY id LIMIT 1`, after).Scan(&proxy.ID, &proxy.Protocol, &proxy.Host, &proxy.Port, &proxy.Username, &proxy.Password, &proxy.Status, &proxy.ExpiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, after, nil
		}
		if err != nil {
			return nil, after, candyProbeFailure("无法读取可用代理，已停止换代理重试；未转组。")
		}
		after = proxy.ID
		candidate := &Account{ProxyID: &proxy.ID, Proxy: proxy}
		route, err := accountCandyProxyURL(candidate)
		if err != nil {
			continue
		}
		if _, exists := used[route]; exists {
			continue
		}
		return proxy, after, nil
	}
}

// A process-local worker limit alone permits N times as many tests when more
// than one application replica is active. Claims hold accountCandyLockID while
// checking durable leases, so manual and scheduled runs share three slots.
func accountCandyCapacityAvailable(ctx context.Context, tx *sql.Tx) (bool, error) {
	var active int
	err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM pk_radar_account_state WHERE active_run_id IS NOT NULL AND lease_until>NOW())+(SELECT COUNT(*) FROM pk_account_candy_state WHERE active_result_id IS NOT NULL AND lease_until>NOW())`).Scan(&active)
	return active < accountCandyWorkers, err
}

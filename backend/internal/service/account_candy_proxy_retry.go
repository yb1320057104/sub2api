package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

const accountCandyProxyAttempts = 3

// All models in a radar run share one route budget. Their immutable account
// identity remains unchanged until final result/group persistence succeeds.
func runAccountCandyWithProxyRetry(
	ctx context.Context,
	db *sql.DB,
	tests *AccountTestService,
	account, credential *Account,
	cfg *AccountCandyTestConfig,
	beforeAttempt func(context.Context, *Account, int) error,
	shared ...*accountCandyProxySession,
) (string, error) {
	if account == nil || credential == nil || tests == nil {
		return "", candyProbeFailure("测试账号或服务不可用；未转组。")
	}
	ctx = context.WithValue(ctx, accountCandyProxyRetryKey{}, true)
	ctx = tlsfingerprint.WithConnectTimeout(ctx, 20*time.Second)
	session := newAccountCandyProxySession(db)
	if len(shared) > 0 && shared[0] != nil {
		session = shared[0]
	} else {
		defer session.close()
	}
	if session.limit == 0 {
		ctx = context.WithValue(ctx, radarCandyProxyRetryKey{}, true)
	}
	recovery, _ := ctx.Value(accountCandyRecoveryKey{}).(*accountCandyRecovery)
	if recovery == nil {
		ctx, recovery = withAccountCandyRecovery(ctx, nil)
	}
	var rejected *accountCandyProxyAttempt
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		selected, err := session.next(ctx, account, rejected)
		if err != nil {
			return "", err
		}
		routeAccount := *account
		routeAccount.ProxyID, routeAccount.Proxy = selected.proxyID, selected.proxy
		_, err = accountCandyProxyURL(&routeAccount)
		if err != nil {
			err = &accountCandyRetryableNetworkError{}
		}
		if beforeAttempt != nil {
			if err == nil {
				err = beforeAttempt(ctx, &routeAccount, selected.number)
			}
		}
		if err == nil {
			err = session.checkAttempt(ctx, selected)
		}
		text := ""
		requested := false
		if err == nil {
			err = recovery.begin(ctx)
			if err == nil {
				requested = true
				text, err = tests.runAccountCandyProbe(ctx, &routeAccount, credential, cfg)
			}
		}
		if err == nil && strings.TrimSpace(text) != "" {
			session.succeeded(selected)
		}
		if err == nil || ctx.Err() != nil {
			return text, err
		}
		var unsent *accountCandyRetryableNetworkError
		if requested && errors.As(err, &unsent) {
			// Preflight and provably unsent connections retain their existing
			// route budget and do not consume response-recovery attempts.
			recovery.attempts--
		}
		if requested && accountCandyResponseCanRecover(err) {
			if err = recovery.pause(ctx, err); err != nil {
				return "", err
			}
			// A body interruption may be transient on a static route. Keep the
			// same route, but repeat all identity/lease/config checks above.
			rejected = nil
			continue
		}
		if !accountCandyCanChangeProxy(err) {
			return text, err
		}
		if requested && !errors.As(err, &unsent) {
			if err = recovery.pause(ctx, err); err != nil {
				return "", err
			}
		}
		rejected = selected
	}
}

type accountCandyProxyAttempt struct {
	proxyID     *int64
	proxy       *Proxy
	number      int
	retired     bool
	probed      bool
	ip          string
	checkedAt   *time.Time
	egressError string
}

// A dedicated database connection holds advisory reservations across network
// work without keeping a database transaction open. Manual assignment remains
// possible; every use and the final bind reject a newly occupied candidate.
type accountCandyProxySession struct {
	mu                  sync.Mutex
	db                  *sql.DB
	conn                *sql.Conn
	backendPID          int
	untrustedConnection bool
	reserved            []int64
	used                map[string]struct{}
	after               int64
	current             *accountCandyProxyAttempt
	usable              *accountCandyProxyAttempt
	originalStatus      string
	originalSchedulable bool
	probe               func(context.Context, *Account) (string, *time.Time, string)
	limit               int
}

func newAccountCandyProxySession(db *sql.DB) *accountCandyProxySession {
	return &accountCandyProxySession{db: db, used: make(map[string]struct{}), probe: probeRadarEgress, limit: accountCandyProxyAttempts}
}

func (s *accountCandyProxySession) next(ctx context.Context, account *Account, rejected *accountCandyProxyAttempt) (*accountCandyProxyAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.untrustedConnection {
		return nil, candyProbeFailure("代理预留连接已中断，停止本轮换代理；未转组。")
	}
	if s.current == nil {
		s.current = &accountCandyProxyAttempt{proxyID: account.ProxyID, proxy: account.Proxy, number: 1}
		s.originalStatus, s.originalSchedulable = account.Status, account.Schedulable
		if route, err := accountCandyProxyURL(account); err == nil {
			s.used[route] = struct{}{}
		}
		return s.current, nil
	}
	if (rejected == nil || rejected != s.current) && !s.current.retired {
		return s.current, nil
	}
	s.current.retired = true
	if s.usable == s.current {
		s.usable = nil
	}
	if s.limit > 0 && s.current.number >= s.limit {
		return nil, candyProbeFailure("出口探测或账号测试连接失败，本轮已尝试3个不同出口；未转组。")
	}
	proxy, err := s.nextFreeProxy(ctx)
	if err != nil {
		return nil, err
	}
	if proxy == nil {
		return nil, candyProbeFailure(fmt.Sprintf("出口探测或账号测试连接失败，本轮已尝试%d个出口；IP管理没有尚未分配账号的可用代理，未转组。", s.current.number))
	}
	s.current = &accountCandyProxyAttempt{proxyID: &proxy.ID, proxy: proxy, number: s.current.number + 1}
	return s.current, nil
}

func (s *accountCandyProxySession) nextFreeProxy(ctx context.Context) (*Proxy, error) {
	if s.db == nil {
		return nil, nil
	}
	if s.conn == nil {
		var err error
		s.conn, err = s.db.Conn(ctx)
		if err != nil {
			return nil, candyProbeFailure("无法预留空闲代理，已停止重试；未转组。")
		}
	}
	for {
		proxy := &Proxy{}
		err := s.conn.QueryRowContext(ctx, `SELECT p.id,p.protocol,p.host,p.port,COALESCE(p.username,''),COALESCE(p.password,''),p.status,p.expires_at FROM proxies p WHERE p.id>$1 AND p.deleted_at IS NULL AND p.status='active' AND (p.expires_at IS NULL OR p.expires_at>NOW()) AND NOT EXISTS(SELECT 1 FROM pk_effective_account_proxy_bindings b WHERE b.proxy_id=p.id) ORDER BY p.id LIMIT 1`, s.after).Scan(&proxy.ID, &proxy.Protocol, &proxy.Host, &proxy.Port, &proxy.Username, &proxy.Password, &proxy.Status, &proxy.ExpiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, candyProbeFailure("无法读取空闲代理，已停止换代理重试；未转组。")
		}
		s.after = proxy.ID
		candidate := &Account{ProxyID: &proxy.ID, Proxy: proxy}
		route, err := accountCandyProxyURL(candidate)
		if err != nil {
			continue
		}
		if _, exists := s.used[route]; exists {
			continue
		}
		var locked bool
		var pid int
		err = s.conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended('pk_candy_retry_proxy:'||$1::text,0)),pg_backend_pid()`, proxy.ID).Scan(&locked, &pid)
		if err != nil {
			// The server may have acquired the lock before the response was
			// interrupted. Never return an unknown session lock to the pool.
			s.untrustedConnection = true
			return nil, candyProbeFailure("无法预留空闲代理，已停止重试；未转组。")
		}
		if locked {
			s.reserved = append(s.reserved, proxy.ID)
		}
		if s.backendPID != 0 && s.backendPID != pid {
			s.untrustedConnection = true
			return nil, candyProbeFailure("代理预留连接发生变化，已停止重试；未转组。")
		}
		s.backendPID = pid
		if !locked {
			continue
		}
		if err = s.checkFreeLocked(ctx, proxy); err != nil {
			if accountCandyCanChangeProxy(err) {
				continue
			}
			return nil, err
		}
		s.used[route] = struct{}{}
		return proxy, nil
	}
}

func (s *accountCandyProxySession) checkFree(ctx context.Context, proxy *Proxy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkFreeLocked(ctx, proxy)
}

func (s *accountCandyProxySession) checkAttempt(ctx context.Context, selected *accountCandyProxyAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if selected != s.current || selected.retired {
		return &accountCandyRetryableNetworkError{}
	}
	if selected.number > 1 {
		return s.checkFreeLocked(ctx, selected.proxy)
	}
	return ctx.Err()
}

func (s *accountCandyProxySession) checkFreeLocked(ctx context.Context, proxy *Proxy) error {
	if s.untrustedConnection {
		return candyProbeFailure("无法确认备用代理预留及空闲状态，已停止使用；未转组。")
	}
	var free bool
	var pid int
	err := s.conn.QueryRowContext(ctx, `SELECT pg_backend_pid(),EXISTS(SELECT 1 FROM proxies p WHERE p.id=$1 AND p.protocol=$2 AND p.host=$3 AND p.port=$4 AND COALESCE(p.username,'')=$5 AND COALESCE(p.password,'')=$6 AND p.deleted_at IS NULL AND p.status='active' AND (p.expires_at IS NULL OR p.expires_at>NOW()) AND NOT EXISTS(SELECT 1 FROM pk_effective_account_proxy_bindings b WHERE b.proxy_id=p.id))`, proxy.ID, proxy.Protocol, proxy.Host, proxy.Port, proxy.Username, proxy.Password).Scan(&pid, &free)
	if err != nil || pid != s.backendPID {
		s.untrustedConnection = true
		return candyProbeFailure("无法确认备用代理预留及空闲状态，已停止使用；未转组。")
	}
	if !free {
		return &accountCandyRetryableNetworkError{}
	}
	return nil
}

func (s *accountCandyProxySession) observe(ctx context.Context, route *Account, number int) (string, *time.Time, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	selected := s.current
	// A sibling can advance after a failed send while this callback is pending.
	// Do not attribute its cached IP to an older route.
	if selected == nil || selected.number != number {
		return "", nil, "本轮出口已切换，请使用最新备用代理重试"
	}
	if !selected.probed {
		if selected.number > 1 {
			if err := s.checkFreeLocked(ctx, selected.proxy); err != nil {
				return "", nil, "备用代理已分配账号或不可用，未进行出口探测"
			}
		}
		selected.ip, selected.checkedAt, selected.egressError = s.probe(ctx, route)
		selected.probed = true
	}
	return selected.ip, selected.checkedAt, selected.egressError
}

func (s *accountCandyProxySession) succeeded(attempt *accountCandyProxyAttempt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !attempt.retired && attempt.number > 1 && attempt.probed && attempt.egressError == "" && attempt.checkedAt != nil && (s.usable == nil || attempt.number > s.usable.number) {
		s.usable = attempt
	}
}

// Called only after all models settle and the caller has checked/locked the
// run lease, immutable identity and group snapshot in its result transaction.
// Group changes happen first because their admission check uses the old route.
func (s *accountCandyProxySession) bindUsable(ctx context.Context, tx *sql.Tx, accountID int64) (string, error) {
	if s == nil || s.usable == nil {
		return "", nil
	}
	proxy := s.usable.proxy
	var status string
	var schedulable bool
	if err := tx.QueryRowContext(ctx, `SELECT status,schedulable FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, accountID).Scan(&status, &schedulable); err != nil {
		return "", err
	}
	if status != StatusActive || !schedulable || status != s.originalStatus || schedulable != s.originalSchedulable {
		return "账号状态或调度设置已更改，未保存本轮备用代理", nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM proxies WHERE id=$1 AND protocol=$2 AND host=$3 AND port=$4 AND COALESCE(username,'')=$5 AND COALESCE(password,'')=$6 AND deleted_at IS NULL AND status='active' AND (expires_at IS NULL OR expires_at>NOW()) FOR UPDATE`, proxy.ID, proxy.Protocol, proxy.Host, proxy.Port, proxy.Username, proxy.Password).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "本轮备用代理已更改或不可用，保留账号原代理", nil
	}
	if err != nil {
		return "", err
	}
	// The result transaction uses SERIALIZABLE. Its snapshot may predate a
	// manual binding that committed before the proxy lock was obtained. A fresh
	// read on the reserved connection closes that window; FK key-share locks
	// now prevent another account from acquiring this proxy until commit.
	if err = s.checkFree(ctx, proxy); err != nil {
		return "本轮备用代理已分配账号或不可用，保留账号原代理", nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET proxy_id=$2,proxy_fallback_origin_id=NULL,extra=COALESCE(extra,'{}'::jsonb)-'upstream_billing_probe',updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, accountID, proxy.ID); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id,payload) VALUES($1,$2,'{}'::jsonb)`, SchedulerOutboxEventAccountChanged, accountID); err != nil {
		return "", err
	}
	return fmt.Sprintf("已将账号切换至通过本轮测试的空闲代理 #%d", proxy.ID), nil
}

func (s *accountCandyProxySession) close() {
	if s.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	broken := s.untrustedConnection
	for _, id := range s.reserved {
		var unlocked bool
		if err := s.conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock(hashtextextended('pk_candy_retry_proxy:'||$1::text,0))`, id).Scan(&unlocked); err != nil || !unlocked {
			broken = true
		}
	}
	// Returning a connection with a leaked session lock to the pool would
	// permanently hide that proxy; discard the physical connection on failure.
	if broken {
		_ = s.conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = s.conn.Close()
}

// A process-local worker limit alone permits N times as many tests when more
// than one application replica is active. Claims hold accountCandyLockID while
// checking durable leases, so manual and scheduled runs share three slots.
func accountCandyCapacityAvailable(ctx context.Context, tx *sql.Tx) (bool, error) {
	var active int
	err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM pk_radar_account_state WHERE active_run_id IS NOT NULL AND lease_until>NOW())+(SELECT COUNT(*) FROM pk_account_candy_state WHERE active_result_id IS NOT NULL AND lease_until>NOW())`).Scan(&active)
	return active < accountCandyWorkers, err
}

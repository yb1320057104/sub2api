package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

// Start and Stop also support a service held behind the PK background start
// gate: Stop before Start permanently cancels startup and never blocks.
func (s *BinanceRechargeService) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped || s.db == nil || s.client == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.started = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(binanceRechargePollInterval)
		defer ticker.Stop()
		s.runBinanceRechargePoll(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runBinanceRechargePoll(ctx)
			}
		}
	}()
}

func (s *BinanceRechargeService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	if s.client != nil {
		s.client.CloseIdleConnections()
	}
}

func (s *BinanceRechargeService) runBinanceRechargePoll(ctx context.Context) {
	// Disabling an account stops order creation, never historical collection.
	rows, err := s.db.QueryContext(ctx, `SELECT a.id FROM binance_recharge_accounts a
WHERE a.next_poll_at<=NOW() AND a.api_locked_until<=NOW() AND a.api_key_encrypted<>'' AND a.api_secret_encrypted<>''
AND (a.enabled OR EXISTS(SELECT 1 FROM binance_recharge_orders o WHERE o.account_id=a.id)) ORDER BY a.id`)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("binance recharge account polling unavailable")
		}
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return
		}
		ids = append(ids, id)
	}
	readErr := rows.Err()
	_ = rows.Close()
	if readErr != nil {
		return
	}
	// Bounded account concurrency prevents one slow account starving the rest.
	jobs := make(chan int64)
	var workers sync.WaitGroup
	for i := 0; i < 3; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for id := range jobs {
				if ctx.Err() != nil {
					continue
				}
				_, _ = s.CheckAccount(ctx, id)
			}
		}()
	}
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return
		}
	}
	close(jobs)
	workers.Wait()
}

// CheckAccount only obtains receipt evidence from the fixed-origin API client.
// The persisted polling lease coordinates background and manual requests across
// all app instances, and preserves provider rate-limit cooldowns after restart.
func (s *BinanceRechargeService) CheckAccount(ctx context.Context, accountID int64) (*BinanceRechargeCheckResult, error) {
	now := time.Now().UTC()
	return s.checkAccountWindow(ctx, accountID, now.Add(-binanceRechargeHistoryWindow), now)
}

func (s *BinanceRechargeService) checkAccountWindow(ctx context.Context, accountID int64, from, to time.Time) (*BinanceRechargeCheckResult, error) {
	if s.client == nil || s.encryptor == nil {
		return nil, infraerrors.ServiceUnavailable("BINANCE_UNAVAILABLE", "币安查收服务不可用")
	}
	ctx, cancel := context.WithTimeout(ctx, binanceRechargePollTimeout)
	defer cancel()
	token := uuid.NewString()
	var failures int
	err := s.db.QueryRowContext(ctx, `UPDATE binance_recharge_accounts SET next_poll_at=NOW()+INTERVAL '120 seconds',api_locked_until=NOW()+INTERVAL '120 seconds',poll_token=$2
WHERE id=$1 AND next_poll_at<=NOW() AND api_locked_until<=NOW() RETURNING failure_count`, accountID, token).Scan(&failures)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, s.binanceCooldownError(ctx, accountID, false)
	}
	if err != nil {
		return nil, err
	}
	result := &BinanceRechargeCheckResult{}
	a, err := scanBinanceRechargeAccount(s.db.QueryRowContext(ctx, `SELECT `+binanceAccountColumns+` FROM binance_recharge_accounts a WHERE a.id=$1`, accountID))
	if err == nil {
		var apiKey, secret string
		apiKey, err = s.encryptor.Decrypt(a.apiKeyEncrypted)
		if err == nil {
			secret, err = s.encryptor.Decrypt(a.apiSecretEncrypted)
		}
		if err != nil {
			err = errBinanceCredentialDecrypt
		}
		if err == nil {
			var receipts []binancepay.Receipt
			var client BinanceRechargeClient
			var finish func()
			client, finish, err = s.accountClient(a)
			if err == nil {
				receipts, err = client.History(ctx, apiKey, secret, a.RecipientUID, from, to)
				finish()
			}
			if err == nil {
				for _, receipt := range receipts {
					var matched, review bool
					matched, review, err = s.acceptReceipt(ctx, a, receipt)
					if err != nil {
						err = errBinanceLocalProcessing
						break
					}
					if matched {
						result.Matched++
					}
					if review {
						result.Review++
					}
				}
			}
		}
	}
	// A cancelled HTTP request must not strand the account or erase a cooldown.
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	delay := binanceRechargePollInterval
	message := ""
	providerLimited := false
	if err != nil {
		failures++
		if failures > 6 {
			failures = 6
		}
		delay = time.Duration(1<<uint(failures)) * binanceRechargePollInterval
		var limited *binancepay.RateLimitError
		providerLimited = errors.As(err, &limited)
		if providerLimited && limited.RetryAfter > delay {
			delay = limited.RetryAfter
		}
		message = binanceRechargePollError(err)
	} else {
		failures = 0
	}
	_, finishErr := s.db.ExecContext(finishCtx, `UPDATE binance_recharge_accounts SET last_checked_at=NOW(),last_error=$3,
next_poll_at=NOW()+make_interval(secs=>$4),failure_count=$5,poll_token='',
api_locked_until=CASE WHEN $6 THEN NOW()+make_interval(secs=>$4) ELSE to_timestamp(0) END,
bills_next_query_at=CASE WHEN $3<>'' THEN GREATEST(bills_next_query_at,NOW()+make_interval(secs=>$4)) ELSE bills_next_query_at END
WHERE id=$1 AND poll_token=$2`, accountID, token, message, delay.Seconds(), failures, providerLimited)
	_, expiryErr := s.db.ExecContext(finishCtx, `UPDATE binance_recharge_orders SET status='EXPIRED',updated_at=NOW() WHERE account_id=$1 AND status='PENDING' AND expires_at<NOW()`, accountID)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("BINANCE_CHECK_FAILED", message)
	}
	if finishErr != nil {
		return nil, finishErr
	}
	if expiryErr != nil {
		return nil, expiryErr
	}
	return result, nil
}

const binanceRechargeRateLimitMessage = "币安 API 限流，已延后查收"

// A nonempty token identifies an in-flight query. A future API lock without
// a token is a durable provider cooldown, shared by both query paths. Older
// releases stored that cooldown only in next_poll_at/bills_next_query_at and
// this exact fixed error; retain it until both windows have elapsed.
func (a *BinanceRechargeAccount) setCheckState(now time.Time, bills bool) {
	a.CheckState, a.RetryAfterSeconds, a.CheckAvailableAt = "ready", 0, nil
	until := a.nextPollAt
	if bills {
		until = a.billsNextQueryAt
	}
	switch {
	case a.hasPollToken && a.apiLockedUntil.After(now):
		a.CheckState, until = "running", a.apiLockedUntil
	case (!a.hasPollToken && a.apiLockedUntil.After(now)) ||
		(a.LastError == binanceRechargeRateLimitMessage && (a.nextPollAt.After(now) || a.billsNextQueryAt.After(now))):
		a.CheckState = "rate_limited"
		for _, candidate := range []time.Time{a.apiLockedUntil, a.nextPollAt, a.billsNextQueryAt} {
			if candidate.After(until) {
				until = candidate
			}
		}
	case until.After(now):
		a.CheckState = "interval_wait"
		if a.LastError != "" {
			a.CheckState = "retry_wait"
		}
	default:
		return
	}
	a.CheckAvailableAt = &until
	a.RetryAfterSeconds = int64((until.Sub(now) + time.Second - 1) / time.Second)
}

func (s *BinanceRechargeService) binanceCooldownError(ctx context.Context, accountID int64, bills bool) error {
	var a BinanceRechargeAccount
	err := s.db.QueryRowContext(ctx, `SELECT next_poll_at,api_locked_until,bills_next_query_at,poll_token<>'',last_error FROM binance_recharge_accounts WHERE id=$1`, accountID).
		Scan(&a.nextPollAt, &a.apiLockedUntil, &a.billsNextQueryAt, &a.hasPollToken, &a.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return infraerrors.NotFound("BINANCE_ACCOUNT_NOT_FOUND", "收款账户不存在")
	}
	if err != nil {
		return err
	}
	a.setCheckState(time.Now().UTC(), bills)
	reason := "BINANCE_CHECK_COOLDOWN"
	if bills {
		reason = "BINANCE_BILLS_COOLDOWN"
	}
	message := "距下次查询尚需等待 %d 秒，请稍后刷新"
	switch a.CheckState {
	case "running":
		message = "币安查询正在进行，当前查询锁剩余 %d 秒，请稍后刷新"
	case "retry_wait":
		message = "上次币安查询失败，尚需等待 %d 秒；可查看渠道最近错误"
	case "rate_limited":
		message = "币安 API 限流，尚需等待 %d 秒，请稍后刷新"
	case "ready":
		// The previous holder may have just completed, or a bill query is
		// yielding to due collection. Never start another request from here.
		a.CheckState, a.RetryAfterSeconds = "interval_wait", 1
		available := time.Now().UTC().Add(time.Second)
		a.CheckAvailableAt = &available
		message = "查询状态正在更新或等待优先查收，%d 秒后请刷新"
	}
	return infraerrors.TooManyRequests(reason, fmt.Sprintf(message, a.RetryAfterSeconds)).WithMetadata(map[string]string{
		"check_state":         a.CheckState,
		"retry_after_seconds": strconv.FormatInt(a.RetryAfterSeconds, 10),
		"check_available_at":  a.CheckAvailableAt.UTC().Format(time.RFC3339),
	})
}

func (s *BinanceRechargeService) CheckOrder(ctx context.Context, orderID, userID int64) (*BinanceRechargeOrder, error) {
	o, err := s.GetOrder(ctx, orderID, userID)
	if err != nil {
		return nil, err
	}
	if o.Status == BinanceRechargeCompleted {
		return o, nil
	}
	to := o.ExpiresAt.Add(binanceRechargeHistoryWindow)
	if now := time.Now().UTC(); to.After(now) {
		to = now
	}
	if _, err := s.checkAccountWindow(ctx, o.AccountID, o.CreatedAt, to); err != nil {
		return nil, err
	}
	return s.GetOrder(ctx, orderID, userID)
}

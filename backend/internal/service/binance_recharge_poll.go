package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
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
WHERE a.next_poll_at<=NOW() AND a.api_key_encrypted<>'' AND a.api_secret_encrypted<>''
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
	err := s.db.QueryRowContext(ctx, `UPDATE binance_recharge_accounts SET next_poll_at=NOW()+INTERVAL '120 seconds',poll_token=$2
WHERE id=$1 AND next_poll_at<=NOW() RETURNING failure_count`, accountID, token).Scan(&failures)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if e := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM binance_recharge_accounts WHERE id=$1)`, accountID).Scan(&exists); e != nil {
			return nil, e
		}
		if !exists {
			return nil, infraerrors.NotFound("BINANCE_ACCOUNT_NOT_FOUND", "收款账户不存在")
		}
		return nil, infraerrors.TooManyRequests("BINANCE_CHECK_COOLDOWN", "查收进行中或限流等待中，请稍后刷新")
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
		if err == nil {
			var receipts []binancepay.Receipt
			receipts, err = s.client.History(ctx, apiKey, secret, a.RecipientUID, from, to)
			if err == nil {
				for _, receipt := range receipts {
					var matched, review bool
					matched, review, err = s.acceptReceipt(ctx, a, receipt)
					if err != nil {
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
	if err != nil {
		failures++
		if failures > 6 {
			failures = 6
		}
		delay = time.Duration(1<<uint(failures)) * binanceRechargePollInterval
		var limited *binancepay.RateLimitError
		if errors.As(err, &limited) && limited.RetryAfter > delay {
			delay = limited.RetryAfter
		}
		message = binanceRechargePollError(err)
	} else {
		failures = 0
	}
	_, finishErr := s.db.ExecContext(finishCtx, `UPDATE binance_recharge_accounts SET last_checked_at=NOW(),last_error=$3,
next_poll_at=NOW()+make_interval(secs=>$4),failure_count=$5,poll_token='' WHERE id=$1 AND poll_token=$2`, accountID, token, message, delay.Seconds(), failures)
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

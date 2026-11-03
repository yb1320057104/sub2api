package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
)

// Status contains only fixed labels, bounded counters and server timestamps.
// It never contains an address, credential, raw response or signed URL.
type BinanceQueryStatus struct {
	Route     string     `json:"route,omitempty"`
	Attempt   int        `json:"attempt,omitempty"`
	State     string     `json:"state,omitempty"`
	RetryAt   *time.Time `json:"retry_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

func (s *BinanceQueryStatus) sanitize() {
	if s.Route != "primary" && s.Route != "backup" {
		*s = BinanceQueryStatus{}
		return
	}
	if s.Attempt < 1 || s.Attempt > 5 {
		*s = BinanceQueryStatus{}
		return
	}
	switch s.State {
	case "running", "waiting", "succeeded", "failed":
	default:
		*s = BinanceQueryStatus{}
	}
}

func binanceWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Retry only safe, read-only provider operations. Do not put local receipt
// settlement in operation: it runs once, after a complete successful history.
func runBinanceRouteAttempts(ctx context.Context, backup bool,
	operation func(bool) error, progress func(BinanceQueryStatus) error,
	wait func(context.Context, time.Duration) error) error {
	if !backup {
		return operation(false)
	}
	delays := [...]time.Duration{0, 0, 5 * time.Second, 10 * time.Second, 15 * time.Second}
	for index, delay := range delays {
		if err := ctx.Err(); err != nil {
			return err
		}
		status := BinanceQueryStatus{Route: "primary", Attempt: index + 1, State: "running"}
		useBackup := index%2 == 1
		if useBackup {
			status.Route = "backup"
		}
		if delay > 0 {
			next := time.Now().UTC().Add(delay)
			status.State, status.RetryAt = "waiting", &next
			if err := progress(status); err != nil {
				return err
			}
			if err := wait(ctx, delay); err != nil {
				return err
			}
			status.State, status.RetryAt = "running", nil
		}
		if err := progress(status); err != nil {
			return err
		}
		err := operation(useBackup)
		if err == nil {
			status.State = "succeeded"
			return progress(status)
		}
		var limited *binancepay.RateLimitError
		if errors.As(err, &limited) {
			status.State = "failed"
			_ = progress(status)
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if index == len(delays)-1 || !binancepay.IsRetryableNetworkError(err) {
			status.State = "failed"
			// Preserve rate-limit errors even if recording the status fails.
			_ = progress(status)
			return err
		}
	}
	return errors.New("binance: route attempts exhausted")
}

func (s *BinanceRechargeService) withBinanceAccountRoutes(ctx context.Context, a *BinanceRechargeAccount, token string, operation func(BinanceRechargeClient) error) error {
	if a == nil {
		return errBinanceAccountProxy
	}
	backup := a.backupProxyURLCiphertext != ""
	if backup && a.proxyURLCiphertext == "" {
		return errBinanceAccountProxy
	}
	return runBinanceRouteAttempts(ctx, backup, func(useBackup bool) error {
		route := *a
		if useBackup {
			route.proxyURLCiphertext, route.ProxyPreflight = a.backupProxyURLCiphertext, a.BackupProxyPreflight
		}
		client, finish, err := s.accountClient(&route)
		if err != nil {
			return err
		}
		defer finish()
		return operation(client)
	}, func(status BinanceQueryStatus) error {
		now := time.Now().UTC()
		status.UpdatedAt = &now
		body, err := json.Marshal(status)
		if err != nil {
			return err
		}
		result, err := s.db.ExecContext(ctx, `UPDATE binance_recharge_accounts SET query_status=$3::jsonb WHERE id=$1 AND poll_token=$2 AND api_locked_until>NOW()`, a.ID, token, string(body))
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return context.Canceled
		}
		return nil
	}, binanceWait)
}

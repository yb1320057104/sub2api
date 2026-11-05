package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const accountCandyResponseAttempts = 3

type accountCandyRecoveryKey struct{}

// A model owns one budget across all routes. A sibling model never inherits
// its partial answer or attempts. Progress stays within the existing run lease.
type accountCandyRecovery struct {
	attempts int
	last     string
	progress func(context.Context, string) error
}

func (r *accountCandyRecovery) message() string {
	if r.last == "" {
		return ""
	}
	return fmt.Sprintf("本轮已请求%d次，重新请求%d次（最多%d次请求）；最近原因：%s", r.attempts, max(0, r.attempts-1), accountCandyResponseAttempts, r.last)
}

func (r *accountCandyRecovery) begin(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.attempts >= accountCandyResponseAttempts {
		return candyProbeFailure("本轮该模型已达到3次请求上限；未转组。")
	}
	r.attempts++
	if r.progress != nil && r.last != "" {
		if err := r.progress(ctx, r.message()); err != nil {
			r.attempts--
			return err
		}
	}
	return nil
}

func (r *accountCandyRecovery) pause(ctx context.Context, err error) error {
	r.last = accountCandySafeError(err)
	if r.attempts >= accountCandyResponseAttempts {
		return fmt.Errorf("本轮该模型已达到3次请求上限：%w", err)
	}
	timer := time.NewTimer(time.Duration(max(1, r.attempts)) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func accountCandyResponseCanRecover(err error) bool {
	var failure *accountCandyProbeError
	return errors.As(err, &failure) && failure.httpStatus == http.StatusOK && failure.recoverable
}

func withAccountCandyRecovery(ctx context.Context, progress func(context.Context, string) error) (context.Context, *accountCandyRecovery) {
	r := &accountCandyRecovery{progress: progress}
	return context.WithValue(ctx, accountCandyRecoveryKey{}, r), r
}

func runAccountCandyResponseRecovery(ctx context.Context, before func(context.Context) error, attempt func(context.Context) (string, error)) (string, error) {
	r, _ := ctx.Value(accountCandyRecoveryKey{}).(*accountCandyRecovery)
	if r == nil {
		ctx, r = withAccountCandyRecovery(ctx, nil)
	}
	for {
		if before != nil {
			if err := before(ctx); err != nil {
				return "", err
			}
		}
		if err := r.begin(ctx); err != nil {
			return "", err
		}
		text, err := attempt(ctx)
		if err == nil || ctx.Err() != nil || !accountCandyResponseCanRecover(err) {
			return text, err
		}
		if err = r.pause(ctx, err); err != nil {
			return "", err
		}
	}
}

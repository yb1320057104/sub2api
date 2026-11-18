package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

func newOpenAIRecoveryGeneration() string { return uuid.NewString() }

func (r *accountRepository) MarkOpenAIAuthRejected(ctx context.Context, id int64, token string, terminal bool, until time.Time) (bool, error) {
	return r.markOpenAIAuthState(ctx, id, token, "", nil, terminal, until, "upstream_401")
}

// Legacy callers must reread the observed recovery generation. The v2 probe
// uses RecoverVerifiedOpenAIAuth directly with its pre-probe snapshot instead.
func (r *accountRepository) RecoverOpenAIAuthIfUnchanged(ctx context.Context, id int64, token string) (bool, error) {
	account, err := r.GetByID(ctx, id)
	if err != nil {
		return false, err
	}
	if account.GetOpenAIAccessToken() != token {
		return false, nil
	}
	return r.RecoverVerifiedOpenAIAuth(ctx, id, account.Credentials, service.OpenAIAuthRecoveryGeneration(account))
}

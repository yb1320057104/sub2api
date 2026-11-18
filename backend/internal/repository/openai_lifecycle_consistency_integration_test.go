//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOpenAILifecyclePostgresPreservesIndependentPoliciesAndFaultOrder(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	a := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "lifecycle-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 1, Schedulable: true, Credentials: map[string]any{"access_token": "synthetic-a", "refresh_token": "synthetic-refresh"}})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM account_token_guard_v2_accounts WHERE account_id=$1", a.ID)
		require.NoError(t, integrationEntClient.Account.DeleteOneID(a.ID).Exec(ctx))
	})
	require.True(t, a.Schedulable)
	marked, err := repo.MarkOpenAIAuthRejected(ctx, a.ID, "synthetic-a", true, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.True(t, marked)
	observed, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	generation := service.OpenAIAuthRecoveryGeneration(observed)
	require.NotEmpty(t, generation)
	require.Equal(t, service.StatusActive, observed.Status)
	require.True(t, observed.Schedulable)
	require.False(t, observed.IsSchedulable())
	marked, err = repo.MarkOpenAIAuthRejected(ctx, a.ID, "synthetic-a", true, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.True(t, marked)
	recovered, err := repo.RecoverVerifiedOpenAIAuth(ctx, a.ID, observed.Credentials, generation)
	require.NoError(t, err)
	require.False(t, recovered, "old successful probe must not erase a newer 401")
	latest, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	recovered, err = repo.RecoverVerifiedOpenAIAuth(ctx, a.ID, latest.Credentials, service.OpenAIAuthRecoveryGeneration(latest))
	require.NoError(t, err)
	require.True(t, recovered)
	current, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.True(t, current.IsSchedulable())
	until := time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Microsecond)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET status='disabled',schedulable=false,rate_limited_at=NOW(),rate_limit_reset_at=$2,overload_until=$2,temp_unschedulable_until=$2,temp_unschedulable_reason='independent403' WHERE id=$1`, a.ID, until)
	require.NoError(t, err)
	applied, err := repo.ApplyOpenAIRefresh(ctx, a.ID, current.Credentials, current.ProxyID, map[string]any{"access_token": "synthetic-b", "refresh_token": "synthetic-next"})
	require.NoError(t, err)
	require.True(t, applied)
	current, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "synthetic-b", current.GetOpenAIAccessToken())
	require.Equal(t, service.StatusDisabled, current.Status)
	require.False(t, current.Schedulable)
	require.Equal(t, until, *current.RateLimitResetAt)
	require.Equal(t, until, *current.OverloadUntil)
	require.Equal(t, "independent403", current.TempUnschedulableReason)
	applied, err = repo.ApplyOpenAIRefresh(ctx, a.ID, observed.Credentials, observed.ProxyID, map[string]any{"access_token": "stale-c"})
	require.NoError(t, err)
	require.False(t, applied)
	marked, err = repo.MarkOpenAIRefreshFailure(ctx, a.ID, observed.Credentials, observed.ProxyID, true, time.Now().Add(time.Minute), "invalid_refresh_token")
	require.NoError(t, err)
	require.False(t, marked)
}

func TestOpenAILifecyclePostgresRefreshTokenCASWithUnchangedAccess(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	a := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "same-access-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 1, Schedulable: true, Credentials: map[string]any{"access_token": "synthetic-access", "refresh_token": "old-refresh"}})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
		require.NoError(t, integrationEntClient.Account.DeleteOneID(a.ID).Exec(ctx))
	})
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{refresh_token}','"new-refresh"') WHERE id=$1`, a.ID)
	require.NoError(t, err)
	applied, err := repo.MarkOpenAIRefreshFailure(ctx, a.ID, a.Credentials, a.ProxyID, true, time.Now().Add(time.Minute), "invalid_refresh_token")
	require.NoError(t, err)
	require.False(t, applied)
	current, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.False(t, service.OpenAIAuthRecoveryPending(current))
	require.True(t, current.Schedulable)
}

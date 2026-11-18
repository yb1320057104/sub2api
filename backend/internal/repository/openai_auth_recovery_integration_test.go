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

func TestOpenAIAuthRecoveryPostgresTokenCASAndPolicy(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	a := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "auth-recovery-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 1, Schedulable: true, Credentials: map[string]any{"access_token": "old-token", "refresh_token": "synthetic"}})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM account_token_guard_v2_accounts WHERE account_id=$1", a.ID)
		require.NoError(t, integrationEntClient.Account.DeleteOneID(a.ID).Exec(ctx))
	})
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO account_token_guard_v2_accounts(account_id,enabled,auto_relogin_enabled,next_probe_at,relogin_fail_streak,auto_relogin_exhausted) VALUES($1,true,true,NOW()+INTERVAL '1 hour',3,true)`, a.ID)
	require.NoError(t, err)
	changed, err := repo.MarkOpenAIAuthRejected(ctx, a.ID, "stale-token", false, time.Now().Add(10*time.Minute))
	require.NoError(t, err)
	require.False(t, changed)
	changed, err = repo.MarkOpenAIAuthRejected(ctx, a.ID, "old-token", false, time.Now().Add(10*time.Minute))
	require.NoError(t, err)
	require.True(t, changed)
	var due, exhausted bool
	var failures int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT next_probe_at<=NOW(),relogin_fail_streak,auto_relogin_exhausted FROM account_token_guard_v2_accounts WHERE account_id=$1`, a.ID).Scan(&due, &failures, &exhausted))
	require.True(t, due)
	require.Equal(t, 3, failures)
	require.True(t, exhausted, "business401 cannot reset login budget")
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{access_token}','"new-token"'),status='error',schedulable=false,error_message='Authentication failed (401): rejected',temp_unschedulable_until=NOW()+INTERVAL '1 hour',temp_unschedulable_reason='OAuth 401: rejected',extra=extra||'{"openai_two_fa_admission":{}}'::jsonb WHERE id=$1`, a.ID)
	require.NoError(t, err)
	changed, err = repo.RecoverOpenAIAuthIfUnchanged(ctx, a.ID, "old-token")
	require.NoError(t, err)
	require.False(t, changed)
	changed, err = repo.RecoverOpenAIAuthIfUnchanged(ctx, a.ID, "new-token")
	require.NoError(t, err)
	require.True(t, changed)
	current, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusActive, current.Status)
	require.False(t, current.Schedulable)
	require.Nil(t, current.TempUnschedulableUntil)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra='{}',status='error',error_message='manual investigation',temp_unschedulable_until=$2,temp_unschedulable_reason='upstream429' WHERE id=$1`, a.ID, time.Now().Add(time.Hour))
	require.NoError(t, err)
	changed, err = repo.RecoverOpenAIAuthIfUnchanged(ctx, a.ID, "new-token")
	require.NoError(t, err)
	require.False(t, changed)
	current, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusError, current.Status)
	require.NotNil(t, current.TempUnschedulableUntil)
}

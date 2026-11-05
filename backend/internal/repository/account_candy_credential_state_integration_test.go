//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func pk52CandySnapshot(t *testing.T, account, owner *service.Account) service.AccountCandyCredentialStateSnapshot {
	t.Helper()
	raw, err := json.Marshal(owner.Credentials)
	require.NoError(t, err)
	return service.AccountCandyCredentialStateSnapshot{AccountID: account.ID, AccountType: account.Type,
		AccountStatus: account.Status, AccountSchedulable: account.Schedulable, AccountParentID: account.ParentAccountID,
		CredentialAccountID: owner.ID, CredentialsJSON: string(raw), CredentialType: owner.Type,
		CredentialStatus: owner.Status, CredentialSchedulable: owner.Schedulable, CredentialProxyID: owner.ProxyID}
}

type pk52CandyOutboxFailure struct{ sqlExecutor }

func (s pk52CandyOutboxFailure) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	// Force the actual outbox NOT NULL constraint to reject this statement.
	args[12] = nil
	return s.sqlExecutor.ExecContext(ctx, query, args...)
}

func TestPK52CandyCredentialAtomicState(t *testing.T) {
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	client := tx.Client()
	repo := newAccountRepositoryWithSQL(client, client, nil)
	makeAccount := func(name string) *service.Account {
		return mustCreateAccount(t, client, &service.Account{Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
			Credentials: map[string]any{"access_token": "old-secret", "refresh_token": "refresh-secret", "_token_version": 1}})
	}
	countOutbox := func(id int64) int {
		rows, err := client.QueryContext(ctx, `SELECT count(*) FROM scheduler_outbox WHERE account_id=$1 AND event_type=$2`, id, service.SchedulerOutboxEventAccountChanged)
		require.NoError(t, err)
		defer rows.Close()
		require.True(t, rows.Next())
		var count int
		require.NoError(t, rows.Scan(&count))
		return count
	}
	t.Run("matching_and_repeated", func(t *testing.T) {
		a := makeAccount("pk52-candy-match")
		cache := &schedulerCacheRecorder{}
		repo.schedulerCache = cache
		defer func() { repo.schedulerCache = nil }()
		snapshot := pk52CandySnapshot(t, a, a)
		before := countOutbox(a.ID)
		updated, err := repo.SetAccountCandyCredentialErrorIfMatch(ctx, snapshot)
		require.NoError(t, err)
		require.True(t, updated)
		current, err := repo.GetByID(ctx, a.ID)
		require.NoError(t, err)
		require.Equal(t, service.StatusError, current.Status)
		require.False(t, current.Schedulable)
		require.Len(t, cache.setAccounts, 1)
		require.Equal(t, service.StatusError, cache.setAccounts[0].Status)
		require.False(t, cache.setAccounts[0].Schedulable)
		require.Equal(t, service.AccountCandyCredentialErrorMessage, current.ErrorMessage)
		require.NotContains(t, current.ErrorMessage, "old-secret")
		require.Equal(t, a.Credentials["access_token"], current.Credentials["access_token"])
		require.Equal(t, before+1, countOutbox(a.ID))
		updated, err = repo.SetAccountCandyCredentialErrorIfMatch(ctx, snapshot)
		require.NoError(t, err)
		require.False(t, updated)
		require.Equal(t, before+1, countOutbox(a.ID))
	})
	for _, tc := range []struct{ name, sql string }{
		{"refreshed", `UPDATE accounts SET credentials='{"access_token":"new-secret","refresh_token":"new-refresh","_token_version":2}'::jsonb WHERE id=$1`},
		{"manual_pause", `UPDATE accounts SET schedulable=false WHERE id=$1`},
		{"manual_error", `UPDATE accounts SET status='error',error_message='manual' WHERE id=$1`},
		{"deleted", `UPDATE accounts SET deleted_at=NOW() WHERE id=$1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := makeAccount("pk52-candy-" + tc.name)
			snapshot := pk52CandySnapshot(t, a, a)
			_, err := client.ExecContext(ctx, tc.sql, a.ID)
			require.NoError(t, err)
			before := countOutbox(a.ID)
			updated, err := repo.SetAccountCandyCredentialErrorIfMatch(ctx, snapshot)
			require.NoError(t, err)
			require.False(t, updated)
			require.Equal(t, before, countOutbox(a.ID))
		})
	}
	t.Run("outbox_failure_rolls_back_state", func(t *testing.T) {
		a := makeAccount("pk52-candy-outbox")
		_, err := client.ExecContext(ctx, "SAVEPOINT pk52_outbox")
		require.NoError(t, err)
		broken := newAccountRepositoryWithSQL(client, pk52CandyOutboxFailure{client}, nil)
		updated, err := broken.SetAccountCandyCredentialErrorIfMatch(ctx, pk52CandySnapshot(t, a, a))
		require.Error(t, err)
		require.False(t, updated)
		_, err = client.ExecContext(ctx, "ROLLBACK TO SAVEPOINT pk52_outbox")
		require.NoError(t, err)
		current, err := repo.GetByID(ctx, a.ID)
		require.NoError(t, err)
		require.Equal(t, service.StatusActive, current.Status)
		require.True(t, current.Schedulable)
	})
}

func TestPK52CandyCredentialShadowAndConcurrentRefresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	owner := mustCreateAccount(t, client, &service.Account{Name: "pk52-candy-parent", Platform: service.PlatformOpenAI, Credentials: map[string]any{"access_token": "old"}})
	other := mustCreateAccount(t, client, &service.Account{Name: "pk52-candy-other", Platform: service.PlatformOpenAI, Credentials: map[string]any{"access_token": "other"}})
	shadow := mustCreateAccount(t, client, &service.Account{Name: "pk52-candy-shadow", Platform: service.PlatformOpenAI, ParentAccountID: &owner.ID, QuotaDimension: "spark"})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id=$1`, shadow.ID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id IN ($1,$2)`, owner.ID, other.ID)
	})
	snapshot := pk52CandySnapshot(t, shadow, owner)
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET parent_account_id=$2 WHERE id=$1`, shadow.ID, other.ID)
	require.NoError(t, err)
	updated, err := repo.SetAccountCandyCredentialErrorIfMatch(ctx, snapshot)
	require.NoError(t, err)
	require.False(t, updated, "reassigned shadow must not disable its old owner")
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET parent_account_id=$2 WHERE id=$1`, shadow.ID, owner.ID)
	require.NoError(t, err)
	var applied atomic.Int32
	var workers sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ok, err := repo.SetAccountCandyCredentialErrorIfMatch(ctx, snapshot)
			if ok {
				applied.Add(1)
			}
			failures <- err
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, applied.Load(), "concurrent model failures must write only once")
	current, err := repo.GetByID(ctx, owner.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusError, current.Status)
	current, err = repo.GetByID(ctx, shadow.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusActive, current.Status, "credential failure belongs to the owner")
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox WHERE account_id=$1 AND event_type=$2`, owner.ID, service.SchedulerOutboxEventAccountChanged).Scan(&count))
	require.Equal(t, 1, count)
	// Refresh holds the owner row while the old 401 is in flight. Whichever
	// statement starts first, the old identity cannot overwrite the new token.
	refresh, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer refresh.Rollback()
	_, err = refresh.ExecContext(ctx, `UPDATE accounts SET status='active',schedulable=true,credentials='{"access_token":"fresh"}'::jsonb WHERE id=$1`, owner.ID)
	require.NoError(t, err)
	result := make(chan bool, 1)
	writeErr := make(chan error, 1)
	go func() {
		ok, err := repo.SetAccountCandyCredentialErrorIfMatch(ctx, snapshot)
		result <- ok
		writeErr <- err
	}()
	require.NoError(t, refresh.Commit())
	require.False(t, <-result)
	require.NoError(t, <-writeErr)
	current, err = repo.GetByID(ctx, owner.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusActive, current.Status)
	require.Equal(t, "fresh", current.GetCredential("access_token"))
}

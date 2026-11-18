package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newKiloSourceHealthTestRepository(t *testing.T) (*accountRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return newAccountRepositoryWithSQL(client, db, nil), mock
}

func expectKiloSourcePairPreflight(mock sqlmock.Sqlmock, paired bool) {
	mock.ExpectQuery(regexp.QuoteMeta(kiloSourcePairExistsSQL)).WithArgs(int64(122)).
		WillReturnRows(sqlmock.NewRows([]string{"paired"}).AddRow(paired))
}

func expectKiloSourceHealthLocks(mock sqlmock.Sqlmock, gateways ...int64) {
	expectKiloSourcePairPreflight(mock, true)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).WithArgs(kiloSourceBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
	rows := sqlmock.NewRows([]string{"account_id"})
	for _, id := range gateways {
		rows.AddRow(id)
	}
	mock.ExpectQuery(regexp.QuoteMeta(kiloSourcePreparedLockSQL)).WithArgs(int64(122)).WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).WithArgs(accountProxyAssignmentLock).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestKiloSourceHealthRejectedTokenCASOwnsGatewayIsolation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		matched  bool
		writeErr bool
	}{
		{name: "current token", matched: true},
		{name: "late old token"},
		{name: "gateway outbox failure rolls back source", matched: true, writeErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, mock := newKiloSourceHealthTestRepository(t)
			expectKiloSourceHealthLocks(mock, 124)
			var affected int64
			if tc.matched {
				affected = 1
			}
			mock.ExpectExec(`(?s)WITH guard_lock AS MATERIALIZED .*a.credentials->>'access_token'=\$2.*INSERT INTO scheduler_outbox`).
				WithArgs(int64(122), "synthetic-used-token", sqlmock.AnyArg(), service.OpenAIAccessTokenDigest("synthetic-used-token"), true, sqlmock.AnyArg(), "upstream_401", nil, nil, service.SchedulerOutboxEventAccountChanged).
				WillReturnResult(sqlmock.NewResult(0, affected))
			if tc.matched {
				write := mock.ExpectQuery(regexp.QuoteMeta(kiloSourceIsolationSQL)).WithArgs(int64(122), "{124}", service.SchedulerOutboxEventAccountChanged)
				if tc.writeErr {
					write.WillReturnError(errors.New("synthetic gateway outbox failure"))
					mock.ExpectRollback()
				} else {
					write.WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(124))
					mock.ExpectCommit()
				}
			} else {
				mock.ExpectCommit()
			}
			changed, err := repo.MarkOpenAIAuthRejected(context.Background(), 122, "synthetic-used-token", true, time.Now().Add(time.Minute))
			if tc.writeErr {
				require.Error(t, err)
				require.False(t, changed)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.matched, changed)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiloSourceHealthSetErrorUsesSameTransaction(t *testing.T) {
	repo, mock := newKiloSourceHealthTestRepository(t)
	expectKiloSourceHealthLocks(mock, 124)
	mock.ExpectExec(`(?s)UPDATE accounts.*schedulable = CASE WHEN platform = \$4 AND type = \$5 THEN schedulable ELSE FALSE END`).
		WithArgs(int64(122), service.StatusError, "Workspace deactivated (402): synthetic", service.PlatformOpenAI, service.AccountTypeOAuth).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WithArgs(service.SchedulerOutboxEventAccountChanged, int64(122), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta(kiloSourceIsolationSQL)).WithArgs(int64(122), "{124}", service.SchedulerOutboxEventAccountChanged).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(124))
	mock.ExpectCommit()
	require.NoError(t, repo.SetError(context.Background(), 122, "Workspace deactivated (402): synthetic"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloSourceHealthOuterTransactionDoesNotWaitBackwards(t *testing.T) {
	repo, mock := newKiloSourceHealthTestRepository(t)
	mock.ExpectBegin()
	tx, err := repo.client.Tx(context.Background())
	require.NoError(t, err)
	ctx := dbent.NewTxContext(context.Background(), tx)
	expectKiloSourcePairPreflight(mock, true)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_xact_lock($1)")).WithArgs(kiloSourceBatchLock).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	require.ErrorContains(t, repo.SetError(ctx, 122, "Workspace deactivated (402): synthetic"), "ownership is busy")
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet(), "the caller retains transaction ownership and no source or gateway update happened")
}

func TestKiloSourceHealthNoPairKeepsOriginalMutationWithoutGlobalLocks(t *testing.T) {
	for _, mode := range []string{"auth current token", "auth stale token", "account error"} {
		t.Run(mode, func(t *testing.T) {
			repo, mock := newKiloSourceHealthTestRepository(t)
			expectKiloSourcePairPreflight(mock, false)
			if mode == "account error" {
				mock.ExpectExec(`(?s)UPDATE accounts.*schedulable = CASE WHEN platform = \$4 AND type = \$5 THEN schedulable ELSE FALSE END`).
					WithArgs(int64(122), service.StatusError, "synthetic ordinary error", service.PlatformOpenAI, service.AccountTypeOAuth).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
					WithArgs(service.SchedulerOutboxEventAccountChanged, int64(122), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
				require.NoError(t, repo.SetError(context.Background(), 122, "synthetic ordinary error"))
			} else {
				var affected int64 = 1
				if mode == "auth stale token" {
					affected = 0
				}
				mock.ExpectExec(`(?s)WITH guard_lock AS MATERIALIZED .*FOR UPDATE\s+\), rejected.*a.credentials->>'access_token'=\$2.*INSERT INTO scheduler_outbox`).
					WillReturnResult(sqlmock.NewResult(0, affected))
				changed, err := repo.MarkOpenAIAuthRejected(context.Background(), 122, "synthetic-used-token", true, time.Now().Add(time.Minute))
				require.NoError(t, err)
				require.Equal(t, affected > 0, changed)
			}
			require.NoError(t, mock.ExpectationsWereMet(), "no Begin, Kilo advisory lock, gateway mutation or paired outbox is allowed")
		})
	}
}

func TestKiloSourceHealthBoundPairIsRecheckedAfterOwnership(t *testing.T) {
	repo, mock := newKiloSourceHealthTestRepository(t)
	expectKiloSourceHealthLocks(mock)
	mock.ExpectExec(`(?s)WITH guard_lock AS MATERIALIZED .*INSERT INTO scheduler_outbox`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	changed, err := repo.MarkOpenAIAuthRejected(context.Background(), 122, "synthetic-used-token", true, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, mock.ExpectationsWereMet(), "a pair removed after preflight cannot receive a stale gateway block")
}

func TestKiloSourceHealthOuterAuthGuardConflictRejectsBeforeWrites(t *testing.T) {
	for _, paired := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary source", true: "bound source"}[paired], func(t *testing.T) {
			repo, mock := newKiloSourceHealthTestRepository(t)
			mock.ExpectBegin()
			tx, err := repo.client.Tx(context.Background())
			require.NoError(t, err)
			ctx := dbent.NewTxContext(context.Background(), tx)
			expectKiloSourcePairPreflight(mock, paired)
			if paired {
				mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_xact_lock($1)")).WithArgs(kiloSourceBatchLock).
					WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
				mock.ExpectQuery(regexp.QuoteMeta(kiloSourcePreparedLockSQL + " NOWAIT")).WithArgs(int64(122)).
					WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(124))
				mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_xact_lock($1)")).WithArgs(accountProxyAssignmentLock).
					WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
			}
			mock.ExpectExec(`(?s)WITH guard_lock AS MATERIALIZED .*FOR UPDATE NOWAIT\s+\), rejected`).
				WillReturnError(errors.New("synthetic guard row NOWAIT conflict"))
			changed, err := repo.MarkOpenAIAuthRejected(ctx, 122, "synthetic-used-token", true, time.Now().Add(time.Minute))
			require.ErrorContains(t, err, "NOWAIT conflict")
			require.False(t, changed)
			mock.ExpectRollback()
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet(), "guard conflict cannot publish source or gateway changes")
		})
	}
}

func TestKiloSourceHealthTransactionClientWithoutContextAlsoUsesNowait(t *testing.T) {
	repo, mock := newKiloSourceHealthTestRepository(t)
	mock.ExpectBegin()
	tx, err := repo.client.Tx(context.Background())
	require.NoError(t, err)
	scoped := newAccountRepositoryWithSQL(tx.Client(), tx.Client(), nil)
	expectKiloSourcePairPreflight(mock, false)
	mock.ExpectExec(`(?s)WITH guard_lock AS MATERIALIZED .*FOR UPDATE NOWAIT\s+\), rejected`).
		WillReturnError(errors.New("synthetic guard row NOWAIT conflict"))
	changed, err := scoped.MarkOpenAIAuthRejected(context.Background(), 122, "synthetic-used-token", true, time.Now().Add(time.Minute))
	require.ErrorContains(t, err, "NOWAIT conflict")
	require.False(t, changed)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloSourceHealthFreshRoutingProjection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sourceID   sql.NullInt64
		reciprocal bool
		healthy    bool
		readErr    error
		blocked    bool
	}{
		{name: "healthy quota only source", sourceID: sql.NullInt64{Int64: 122, Valid: true}, reciprocal: true, healthy: true},
		{name: "current token rejected", sourceID: sql.NullInt64{Int64: 122, Valid: true}, reciprocal: true, blocked: true},
		{name: "source error", sourceID: sql.NullInt64{Int64: 122, Valid: true}, reciprocal: true, blocked: true},
		{name: "source removed", blocked: true},
		{name: "pair changed", sourceID: sql.NullInt64{Int64: 122, Valid: true}, healthy: true, blocked: true},
		{name: "pair unavailable", readErr: sql.ErrNoRows, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, mock := newKiloSourceHealthTestRepository(t)
			mock.ExpectQuery(regexp.QuoteMeta("SELECT id,platform,type,status,schedulable,parent_account_id,credentials,extra FROM accounts WHERE id=$1 AND deleted_at IS NULL")).WithArgs(int64(124)).
				WillReturnRows(sqlmock.NewRows([]string{"id", "platform", "type", "status", "schedulable", "parent_account_id", "credentials", "extra"}).
					AddRow(124, "openai", "apikey", "active", true, nil, `{"api_key":"synthetic-key"}`, `{"kilo_managed":true,"kilo_prepared_id":2,"kilo_onboarding_source_id":122}`))
			read := mock.ExpectQuery(regexp.QuoteMeta(kiloSourceSendHealthSQL)).WithArgs(int64(124), int64(2))
			if tc.readErr != nil {
				read.WillReturnError(tc.readErr)
			} else {
				var id any
				if tc.sourceID.Valid {
					id = tc.sourceID.Int64
				}
				read.WillReturnRows(sqlmock.NewRows([]string{"source_id", "reciprocal", "healthy"}).AddRow(id, tc.reciprocal, tc.healthy))
			}
			account, err := repo.GetKiloInferenceAccount(context.Background(), 124)
			require.NoError(t, err)
			require.Equal(t, !tc.blocked, account.Schedulable)
			if tc.blocked {
				require.Equal(t, service.StatusError, account.Status)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiloSourceHealthSQLKeepsRecoveryAndReleaseBoundaries(t *testing.T) {
	require.Contains(t, kiloSourceIsolationSQL, "g.id=ANY($2::bigint[])")
	require.Contains(t, kiloSourceIsolationSQL, "s.extra->>'kilo_source_gateway_id'=p.account_id::text")
	require.Contains(t, kiloSourceIsolationSQL, "s.extra->>'kilo_source_prepared_id'=p.id::text")
	require.Contains(t, kiloSourceIsolationSQL, "status='disabled'")
	require.Contains(t, kiloSourceIsolationSQL, "'kilo_source:'")
	require.NotContains(t, kiloSourceIsolationSQL, "kilo_release:")
	require.NotContains(t, kiloSourceIsolationSQL, "auto_release_statuses")
	require.Contains(t, kiloSourceStatusSQL, "sha256(convert_to(s.credentials->>'access_token','UTF8'))")
	require.NotContains(t, kiloSourceSendHealthSQL, "s.schedulable")
}

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestKiloModelScopeLocksAllOwnersBeforeCompareAndPublish(t *testing.T) {
	for _, changed := range []int64{0, 1} {
		t.Run(map[int64]string{0: "source_changed_rejects_publication", 1: "unchanged_publishes_scope_and_outbox"}[changed], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := &accountRepository{client: client, sql: db}
			gateway := &service.Account{ID: 124, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "synthetic-key", "kilo_organization_id": ""},
				Extra:       map[string]any{"kilo_managed": true, "kilo_prepared_id": int64(2)}}
			source := &service.Account{ID: 122, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Credentials: map[string]any{"email": "source@example.test", "chatgpt_account_id": "original-workspace"}}
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(int64(70450001)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT id FROM pk_kilo_prepared_accounts WHERE id=\\$1 FOR UPDATE").WithArgs(2).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(accountProxyAssignmentLock).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT id FROM accounts WHERE id IN \\(\\$1,\\$2\\).*ORDER BY id FOR UPDATE").WithArgs(int64(122), int64(124)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(122).AddRow(124))
			// A new statement rechecks both credential generations and the pair
			// after their locks. Zero rows means neither scope nor outbox commits.
			mock.ExpectExec("WITH updated AS \\(.*UPDATE accounts g.*g.credentials=\\$4::jsonb.*g.extra=\\$5::jsonb.*s.credentials=\\$8::jsonb.*INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(0, changed))
			if changed == 0 {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			applied, err := repo.UpdateKiloModelScopeIfUnchanged(context.Background(), gateway, source, []string{"openai/gpt-one", "openai/gpt-two"}, time.Now())
			require.NoError(t, err)
			require.Equal(t, changed == 1, applied)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

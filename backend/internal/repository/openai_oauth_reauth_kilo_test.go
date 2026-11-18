package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIReauthKiloBusyCreationRollsBackBeforeInsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &openAIOAuthReauthRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(openAIReauthKiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("(?s)SELECT EXISTS.*pk_kilo_jobs.*job_kind='onboard_source'.*stage <> 'quota_oauth'").WithArgs(int64(42), "codex").WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(true))
	mock.ExpectRollback()
	task, err := repo.CreateTask(context.Background(), 42, "expected")
	require.Nil(t, task)
	require.Equal(t, "OPENAI_REAUTH_KILO_BUSY", infraerrors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIReauthKiloQuotaCreationCommitsAfterSharedLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &openAIOAuthReauthRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(openAIReauthKiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("(?s)SELECT EXISTS.*pk_kilo_jobs.*stage <> 'quota_oauth'").WithArgs(int64(42), "codex").WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(false))
	expectKiloReauthLegacyQueueAvailable(mock)
	mock.ExpectQuery("SELECT account_id FROM account_token_guard_v2_accounts").WithArgs(int64(42)).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("INSERT INTO openai_oauth_reauth_tasks").WithArgs(int64(42), "queued", "queued", "expected").WillReturnRows(kiloReauthTaskRows())
	mock.ExpectCommit()
	task, err := repo.CreateTask(context.Background(), 42, "expected")
	require.NoError(t, err)
	require.Equal(t, int64(71), task.ID)
	require.Equal(t, "codex", task.OAuthProfile)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIReauthKiloClaimFiltersRemoteStagesWithinTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &openAIOAuthReauthRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(openAIReauthKiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("(?s)WITH expired_callbacks.*NOT EXISTS.*pk_kilo_jobs kilo.*kilo.stage <> 'quota_oauth'.*oauth_profile <> 'codex'.*openai_totp_rotations.*FOR UPDATE SKIP LOCKED").WithArgs("failed", "failed", "callback_processing", int64(1800), "queued", "running", "starting", "worker", "", sqlmock.AnyArg(), "").WillReturnRows(kiloReauthTaskRows())
	mock.ExpectCommit()
	task, err := repo.ClaimNextTaskForRuntime(context.Background(), "worker", 30*time.Minute, "", []string{service.OpenAIOAuthReauthEngineLocal}, "")
	require.NoError(t, err)
	require.Equal(t, int64(71), task.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIReauthKiloQuotaEngineOverridePrecedesGlobalSelection(t *testing.T) {
	for _, offered := range []string{service.OpenAIOAuthReauthEngineLocal, service.OpenAIOAuthReauthEngineSessionStudio} {
		t.Run(offered, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := &openAIOAuthReauthRepository{db: db}
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(openAIReauthKiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
			rows := sqlmock.NewRows([]string{"id"})
			if offered == service.OpenAIOAuthReauthEngineLocal {
				rows = kiloReauthTaskRows()
			}
			mock.ExpectQuery(`(?s)CASE WHEN oauth_profile IN.*oauth_profile='codex'.*SELECT 1 FROM accounts source.*source.extra->'kilo_quota_only'='true'::jsonb.*THEN 'local_worker' ELSE COALESCE.*\$11.*ANY\(\$10::text\[\]\)`).WithArgs("failed", "failed", "callback_processing", int64(1800), "queued", "running", "starting", "worker", "", `{"`+offered+`"}`, service.OpenAIOAuthReauthEngineSessionStudio).WillReturnRows(rows)
			mock.ExpectCommit()
			task, err := repo.ClaimNextTaskForRuntime(context.Background(), "worker", 30*time.Minute, "", []string{offered}, service.OpenAIOAuthReauthEngineSessionStudio)
			require.NoError(t, err)
			if offered == service.OpenAIOAuthReauthEngineLocal {
				require.NotNil(t, task)
			} else {
				require.Nil(t, task)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestOpenAIReauthActiveCodexReadExcludesAuxiliaryAndRotation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &openAIOAuthReauthRepository{db: db}
	mock.ExpectQuery("(?s)SELECT id, account_id.*oauth_profile='codex'.*status IN.*NOT EXISTS.*openai_totp_rotations").WithArgs(int64(42)).WillReturnRows(kiloReauthTaskRows())
	task, err := repo.GetActiveCodexTask(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, int64(71), task.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIReauthKiloAuxiliaryCreationSharesLockAndRejectsQuotaPhase(t *testing.T) {
	for _, profile := range []string{"excel", "siwc"} {
		for _, busy := range []bool{false, true} {
			name := profile + "/ordinary-account"
			if busy {
				name = profile + "/onboarding-even-quota-phase"
			}
			t.Run(name, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				defer db.Close()
				repo := &openAIOAuthReauthRepository{db: db}
				create := repo.CreateExcelTask
				if profile == "siwc" {
					create = repo.CreateSIWCTask
				}
				mock.ExpectBegin()
				mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(openAIReauthKiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(`(?s)SELECT EXISTS.*job_kind='onboard_source'.*stage <> 'quota_oauth' OR \$2 <> 'codex'`).WithArgs(int64(42), profile).WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(busy))
				if busy {
					mock.ExpectRollback()
				} else {
					expectKiloReauthLegacyQueueAvailable(mock)
					mock.ExpectQuery("INSERT INTO openai_oauth_reauth_tasks").WithArgs(int64(42), "expected").WillReturnRows(kiloReauthTaskRowsForProfile(profile))
					mock.ExpectCommit()
				}
				task, err := create(context.Background(), 42, "expected")
				if busy {
					require.Nil(t, task)
					require.Equal(t, "OPENAI_REAUTH_KILO_BUSY", infraerrors.Reason(err))
				} else {
					require.NoError(t, err)
					require.Equal(t, profile, task.OAuthProfile)
				}
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestOpenAIReauthKiloAutomaticCreationLocksBeforeBudget(t *testing.T) {
	for _, busy := range []bool{false, true} {
		name := "legacy-budget-gate-preserved"
		if busy {
			name = "remote-onboarding-conflict-before-budget"
		}
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := &accountTokenGuardV2Repository{db: db}
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(openAIReauthKiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(`(?s)SELECT EXISTS.*stage <> 'quota_oauth' OR \$2 <> 'codex'`).WithArgs(int64(42), "codex").WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(busy))
			if !busy {
				expectKiloReauthLegacyQueueAvailable(mock)
				mock.ExpectQuery("(?s)SELECT.*account_token_guard_v2_accounts.*FOR UPDATE").WithArgs(int64(42), "owner").WillReturnError(sql.ErrNoRows)
			}
			mock.ExpectRollback()
			task, err := repo.CreateAutomaticRelogin(context.Background(), 42, "expected", "owner", service.AccountTokenGuardV2Rules{}, time.Now())
			require.Nil(t, task)
			if busy {
				require.Equal(t, "OPENAI_REAUTH_KILO_BUSY", infraerrors.Reason(err))
			} else {
				require.Equal(t, "TOKEN_GUARD_V2_BUDGET_BUSY", infraerrors.Reason(err))
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Kilo ownership precedes the existing PK authorization-queue fence.
func expectKiloReauthLegacyQueueAvailable(mock sqlmock.Sqlmock) {
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("(?s)SELECT EXISTS.*pk_authorization_jobs").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(false))
}

func kiloReauthTaskRows() *sqlmock.Rows {
	return kiloReauthTaskRowsForProfile("codex")
}

func kiloReauthTaskRowsForProfile(profile string) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{"id", "account_id", "status", "stage", "worker_id", "session_id", "hash", "error", "attempt", "created_at", "updated_at", "finished_at", "profile"}).AddRow(71, 42, "queued", "queued", "", "", "expected", "", 0, now, now, nil, profile)
}

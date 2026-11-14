package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestOpenAIOAuthReauthEnginePersistsInCorrectSQLColumn(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewOpenAIOAuthReauthRepository(db)
	mock.ExpectExec("INSERT INTO openai_oauth_reauth_configs").WithArgs(int64(42), "user@example.com", "password_totp", "session_studio", "account", nil, "enc-password", "enc-totp", "", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpsertConfig(context.Background(), &service.OpenAIOAuthReauthStoredConfig{AccountID: 42, LoginEmail: "user@example.com", CredentialMode: "password_totp", Engine: "session_studio", ProxySource: "account", PasswordCiphertext: "enc-password", TOTPSecretCiphertext: "enc-totp"}))
	mock.ExpectQuery("SELECT account_id, login_email, credential_mode, engine, proxy_source, proxy_id").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"account_id", "email", "mode", "engine", "proxy_source", "proxy_id", "password", "totp", "otp", "updated_at"}).AddRow(42, "user@example.com", "password_totp", "session_studio", "account", nil, "enc-password", "enc-totp", nil, time.Now()))
	cfg, err := repo.GetConfig(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "session_studio", cfg.Engine)
	require.Equal(t, "enc-totp", cfg.TOTPSecretCiphertext)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIOAuthReauthEngineLegacyClaimFiltersBeforeLocking(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewOpenAIOAuthReauthRepository(db)
	mock.ExpectQuery("(?s)WITH expired_callbacks.*engine.*ANY.*FOR UPDATE SKIP LOCKED").WithArgs("failed", "failed", "callback_processing", int64(1800), "queued", "running", "starting", "old-worker", "", `{"local_worker"}`, "").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	claim, err := repo.ClaimNextTask(context.Background(), "old-worker", 30*time.Minute)
	require.NoError(t, err)
	require.Nil(t, claim)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTOTPRotationAutomaticEligibilityIsTransactional(t *testing.T) {
	for _, tc := range []struct {
		name          string
		manual        bool
		activeRelogin bool
		accountBusy   bool
		guardBusy     bool
		missing       bool
		monitored     bool
		eligible      bool
		queryError    bool
		cooldown      bool
		configChanged bool
	}{
		{name: "pause observed after scheduling", monitored: true},
		{name: "active callback rejected before account lock", activeRelogin: true},
		{name: "busy account skipped without waiting", accountBusy: true},
		{name: "busy guard skipped without waiting", guardBusy: true},
		{name: "missing account", missing: true},
		{name: "qualification read fails closed", monitored: true, queryError: true},
		{name: "unmanaged account remains eligible", eligible: true},
		{name: "enabled managed account remains eligible", monitored: true, eligible: true},
		{name: "existing automatic cooldown still applies", monitored: true, eligible: true, cooldown: true},
		{name: "changed configuration still conflicts", monitored: true, eligible: true, configChanged: true},
		{name: "manual rotation bypasses automatic controls", manual: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected, query string) error {
				if expected != "automatic eligibility" {
					return sqlmock.QueryMatcherRegexp.Match(expected, query)
				}
				query = strings.Join(strings.Fields(query), " ")
				for _, predicate := range []string{
					"a.id=$1 AND a.deleted_at IS NULL",
					"a.status<>'disabled' AND a.schedulable",
					"guard.account_id=a.id AND (NOT guard.enabled OR NOT guard.auto_relogin_enabled)",
					"st.account_id=a.id AND st.status='banned' AND st.credential_version=md5(a.credentials::text)",
					"email=lower(trim(COALESCE(NULLIF(a.credentials->>'email',''),NULLIF(a.extra->>'email_address',''),NULLIF(a.extra->>'email',''),'')))",
					"status IN ('banned','completed') OR settings->>'authorization_completed'='true') ORDER BY id DESC LIMIT 1),'')<>'banned'",
				} {
					require.Contains(t, query, predicate)
				}
				return nil
			})))
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			version := time.Now().UTC()
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT EXISTS").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(false))
			mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 1))
			if !tc.manual {
				mock.ExpectQuery("SELECT EXISTS.*FROM openai_oauth_reauth_tasks task.*NOT EXISTS.*openai_totp_rotations").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(tc.activeRelogin))
			}
			if !tc.manual && !tc.activeRelogin {
				accounts := sqlmock.NewRows([]string{"id"})
				if !tc.missing {
					accounts.AddRow(42)
				}
				accountLock := mock.ExpectQuery("SELECT id FROM accounts.*FOR UPDATE NOWAIT").WithArgs(int64(42))
				if tc.accountBusy {
					accountLock.WillReturnError(&pq.Error{Code: "55P03"})
				} else {
					accountLock.WillReturnRows(accounts)
				}
				if !tc.missing && !tc.accountBusy {
					guard := sqlmock.NewRows([]string{"account_id"})
					if tc.monitored {
						guard.AddRow(42)
					}
					guardLock := mock.ExpectQuery("SELECT account_id FROM account_token_guard_v2_accounts.*FOR UPDATE NOWAIT").WithArgs(int64(42))
					if tc.guardBusy {
						guardLock.WillReturnError(&pq.Error{Code: "55P03"})
					} else {
						guardLock.WillReturnRows(guard)
					}
				}
				if !tc.missing && !tc.accountBusy && !tc.guardBusy {
					check := mock.ExpectQuery("automatic eligibility").WithArgs(int64(42))
					if tc.queryError {
						check.WillReturnError(errors.New("qualification unavailable"))
					} else {
						check.WillReturnRows(sqlmock.NewRows([]string{"eligible"}).AddRow(tc.eligible))
					}
				}
			}
			qualified := tc.manual || (!tc.activeRelogin && !tc.accountBusy && !tc.guardBusy && !tc.missing && tc.eligible && !tc.queryError)
			if qualified {
				current := version
				if tc.configChanged {
					current = version.Add(time.Second)
				}
				mock.ExpectQuery("SELECT updated_at FROM openai_oauth_reauth_configs.*FOR UPDATE").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(current))
				if !tc.manual && !tc.configChanged {
					mock.ExpectQuery("SELECT EXISTS.*SELECT 1 FROM openai_totp_rotations").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"blocked"}).AddRow(tc.cooldown))
				}
			}
			created := qualified && !tc.configChanged && !tc.cooldown
			if created {
				mock.ExpectQuery("INSERT INTO openai_oauth_reauth_tasks").WithArgs(int64(42), "snapshot").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
				mock.ExpectExec("INSERT INTO openai_totp_rotations").WithArgs(int64(7), int64(42), version).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				mock.ExpectExec("UPDATE openai_totp_rotations SET state='uncertain'").WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT task_id, account_id, state, action").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"task_id", "account_id", "state", "action", "worker_id", "candidate_ciphertext", "config_updated_at", "error_code", "updated_at"}).AddRow(7, 42, "queued", "rotate", "", "", version, "", version))
			} else {
				mock.ExpectRollback()
			}
			repo := &openAIOAuthReauthRepository{db: db}
			var task *service.OpenAITOTPRotation
			if tc.manual {
				task, err = repo.CreateTOTPRotation(t.Context(), 42, "snapshot", version)
			} else {
				task, err = repo.CreateTOTPRotationOn429(t.Context(), 42, "snapshot", version)
			}
			if tc.activeRelogin || tc.queryError || tc.configChanged {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if created {
				require.NotNil(t, task)
				require.Equal(t, int64(7), task.TaskID)
			} else {
				require.Nil(t, task)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

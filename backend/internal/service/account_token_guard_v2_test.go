package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestAccountTokenGuardV2BatchParsing(t *testing.T) {
	rows, err := parseTokenGuardV2Batch("\n User@Example.com ---- password \r\nuser@example.com----second----bad\nempty@example.com----\nempty@example.com----replacement\nwrong@example.com----password----123456\ninvalid----password\nbad-line\nvalid@example.com----pass----jbswy3dpehpk3pxp")
	require.NoError(t, err)
	require.Len(t, rows, 8)
	require.Equal(t, 2, rows[0].item.Line)
	require.Equal(t, "user@example.com", rows[0].secret.Email)
	require.Equal(t, " password ", rows[0].secret.Password)
	require.Empty(t, rows[0].secret.TwoFA)
	require.Equal(t, "duplicate_email", rows[1].item.Status)
	require.Equal(t, "invalid_password", rows[2].item.Status)
	require.Equal(t, "duplicate_email", rows[3].item.Status)
	require.Equal(t, "invalid_twofa", rows[4].item.Status)
	require.Equal(t, "invalid_email", rows[5].item.Status)
	require.Equal(t, "invalid_format", rows[6].item.Status)
	require.Equal(t, "JBSWY3DPEHPK3PXP", rows[7].secret.TwoFA)
	for _, index := range []int{1, 2, 3, 4, 5, 6} {
		require.Equal(t, AuthorizationSecret{}, rows[index].secret)
	}
	_, err = parseTokenGuardV2Batch(strings.Repeat("x@example.com----password\n", 201))
	require.Error(t, err)
	_, err = parseTokenGuardV2Batch(strings.Repeat("x", 2<<20+1))
	require.Error(t, err)
	_, err = parseTokenGuardV2Batch("\n  \r\n")
	require.Error(t, err)
}

type tokenGuardV2HistoryTestEncryptor struct{}

func (tokenGuardV2HistoryTestEncryptor) Encrypt(value string) (string, error) {
	return "history:" + value, nil
}
func (tokenGuardV2HistoryTestEncryptor) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "history:") {
		return "", errors.New("private cipher diagnostic")
	}
	return strings.TrimPrefix(value, "history:"), nil
}

func tokenGuardV2EnrollmentTestService(t *testing.T) (*AccountTokenGuardV2Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	reauth, reader, _, _, _, _ := newReauthTestService("acct-1")
	svc := NewAccountTokenGuardV2Service(&accountTokenGuardV2TestRepo{}, nil, reader, accountTokenGuardV2TestProber{}, reauth)
	svc.ConfigureEnrollment(db, tokenGuardV2HistoryTestEncryptor{})
	return svc, mock
}

func tokenGuardV2ExpectEnrollmentAccount(mock sqlmock.Sqlmock, busy bool, paused ...bool) {
	isPaused := len(paused) > 0 && paused[0]
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .* FROM accounts a WHERE a.id").WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"email", "supported", "paused"}).AddRow("user@example.com", true, isPaused))
	mock.ExpectQuery("SELECT COUNT").WithArgs("user@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT EXISTS.*pk_authorization_jobs.*OR EXISTS.*openai_oauth_reauth_tasks").WithArgs(int64(42), "user@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(busy))
}

func tokenGuardV2ConfigTestRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"login_email", "credential_mode", "engine", "proxy_source", "proxy_id", "password", "totp", "otp", "updated_at"})
}

func TestAccountTokenGuardV2EnrollmentReencryptsHistory(t *testing.T) {
	svc, mock := tokenGuardV2EnrollmentTestService(t)
	tokenGuardV2ExpectEnrollmentAccount(mock, false)
	mock.ExpectQuery("SELECT login_email.*FOR UPDATE").WithArgs(int64(42)).WillReturnRows(tokenGuardV2ConfigTestRows())
	mock.ExpectQuery("SELECT secret_encrypted FROM").WithArgs("user@example.com").WillReturnRows(sqlmock.NewRows([]string{"secret"}).AddRow(
		`history:{"email":"user@example.com","password":"historical-password","twofa":"JBSWY3DPEHPK3PXP"}`))
	mock.ExpectExec("INSERT INTO openai_oauth_reauth_configs.*ON CONFLICT.*DO NOTHING").WithArgs(
		int64(42), "user@example.com", OpenAIOAuthReauthModePasswordTOTP, OpenAIOAuthReauthEngineLocal, "random_managed", nil,
		"encrypted:historical-password", "encrypted:JBSWY3DPEHPK3PXP", "").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO account_token_guard_v2_accounts.*ON CONFLICT.*DO NOTHING").WithArgs(int64(42), nil, nil).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	id, status, err := svc.enrollAccount(context.Background(), 42, nil, nil)
	require.NoError(t, err)
	require.Equal(t, int64(42), id)
	require.Equal(t, "added", status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountTokenGuardV2EnrollmentPreservesManualConfigAndSwitches(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		password string
		totp     string
		otp      string
	}{
		{"password", OpenAIOAuthReauthModePasswordTOTP, "manual-password", "manual-totp", ""},
		{"cleared totp", OpenAIOAuthReauthModePasswordTOTP, "manual-password", "", ""},
		{"cleared password", OpenAIOAuthReauthModePasswordTOTP, "", "manual-totp", ""},
		{"mailbox", OpenAIOAuthReauthModeEmailOTPURL, "", "", "manual-mailbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, mock := tokenGuardV2EnrollmentTestService(t)
			tokenGuardV2ExpectEnrollmentAccount(mock, false)
			mock.ExpectQuery("SELECT login_email.*FOR UPDATE").WithArgs(int64(42)).WillReturnRows(tokenGuardV2ConfigTestRows().AddRow(
				"user@example.com", tc.mode, OpenAIOAuthReauthEngineLocal, "account", nil, tc.password, tc.totp, tc.otp, time.Now()))
			// No history read, configuration write, or switch UPDATE is allowed.
			mock.ExpectExec("INSERT INTO account_token_guard_v2_accounts.*ON CONFLICT.*DO NOTHING").WithArgs(int64(42), nil, nil).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectCommit()
			_, status, err := svc.enrollAccount(context.Background(), 42, nil, nil)
			require.NoError(t, err)
			if tc.password == "" && tc.otp == "" {
				require.Equal(t, "missing_credentials", status)
			} else {
				require.Equal(t, "unchanged", status)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountTokenGuardV2EnrollmentExplicitUpdateUsesCAS(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		t.Run(fmt.Sprintf("rows_%d", affected), func(t *testing.T) {
			svc, mock := tokenGuardV2EnrollmentTestService(t)
			mock.ExpectBegin()
			tx, err := svc.enrollment.db.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			now := time.Now()
			existing := &OpenAIOAuthReauthStoredConfig{AccountID: 42, LoginEmail: "user@example.com", CredentialMode: OpenAIOAuthReauthModePasswordTOTP,
				Engine: OpenAIOAuthReauthEngineLocal, ProxySource: "account", PasswordCiphertext: "manual-password", TOTPSecretCiphertext: "manual-totp", UpdatedAt: now}
			mock.ExpectExec(`UPDATE openai_oauth_reauth_configs.*WHERE account_id=\$1 AND updated_at=\$10`).WithArgs(
				int64(42), "user@example.com", OpenAIOAuthReauthModePasswordTOTP, OpenAIOAuthReauthEngineLocal, "account", nil,
				"encrypted:new-password", "manual-totp", "", now).WillReturnResult(sqlmock.NewResult(0, affected))
			mock.ExpectRollback()
			changed, err := svc.writeEnrollmentConfig(context.Background(), tx, 42,
				&AuthorizationSecret{Email: "user@example.com", Password: "new-password"}, existing, &AccountTokenGuardV2BatchInput{})
			if affected == 1 {
				require.NoError(t, err)
				require.True(t, changed)
			} else {
				require.Equal(t, "account_changed", tokenGuardV2EnrollmentError(err))
			}
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountTokenGuardV2EnrollmentBusyAndMissingMaterials(t *testing.T) {
	t.Run("active task", func(t *testing.T) {
		svc, mock := tokenGuardV2EnrollmentTestService(t)
		tokenGuardV2ExpectEnrollmentAccount(mock, true)
		mock.ExpectRollback()
		_, status, err := svc.enrollAccount(context.Background(), 42, nil, nil)
		require.NoError(t, err)
		require.Equal(t, "busy", status)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("no history still visible", func(t *testing.T) {
		svc, mock := tokenGuardV2EnrollmentTestService(t)
		tokenGuardV2ExpectEnrollmentAccount(mock, false)
		mock.ExpectQuery("SELECT login_email.*FOR UPDATE").WithArgs(int64(42)).WillReturnRows(tokenGuardV2ConfigTestRows())
		mock.ExpectQuery("SELECT secret_encrypted FROM").WithArgs("user@example.com").WillReturnRows(sqlmock.NewRows([]string{"secret"}))
		mock.ExpectExec("INSERT INTO account_token_guard_v2_accounts").WithArgs(int64(42), nil, nil).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		_, status, err := svc.enrollAccount(context.Background(), 42, nil, nil)
		require.NoError(t, err)
		require.Equal(t, "missing_credentials", status)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAccountTokenGuardV2EnrollmentRejectsWrongHistoryIdentity(t *testing.T) {
	svc, _ := tokenGuardV2EnrollmentTestService(t)
	_, err := svc.decodeEnrollmentHistory("user@example.com", `history:{"email":"other@example.com","password":"private-password"}`)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-password")
	require.NotContains(t, err.Error(), "other@example.com")
	_, err = svc.decodeEnrollmentHistory("user@example.com", "wrong-key-ciphertext")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private cipher diagnostic")
}

func TestAccountTokenGuardV2BatchRejectsUnsafeMatchesWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rows   *sqlmock.Rows
		status string
	}{
		{"missing", sqlmock.NewRows([]string{"id", "supported"}), "not_found"},
		{"duplicate", sqlmock.NewRows([]string{"id", "supported"}).AddRow(42, true).AddRow(43, true), "ambiguous_email"},
		{"unsupported", sqlmock.NewRows([]string{"id", "supported"}).AddRow(42, false), "unsupported_account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, mock := tokenGuardV2EnrollmentTestService(t)
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT a.id,.*FOR UPDATE OF a").WithArgs("user@example.com").WillReturnRows(tc.rows)
			mock.ExpectRollback()
			input := &AccountTokenGuardV2BatchInput{Accounts: "user@example.com----private-batch-password"}
			result, err := svc.BatchAddAccounts(context.Background(), input)
			require.NoError(t, err)
			require.Empty(t, input.Accounts)
			require.Len(t, result.Items, 1)
			require.Equal(t, tc.status, result.Items[0].Status)
			require.Equal(t, 1, result.Failed)
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "private-batch-password")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountTokenGuardV2EnrollmentNewConfigConflictDoesNotOverwrite(t *testing.T) {
	svc, mock := tokenGuardV2EnrollmentTestService(t)
	mock.ExpectBegin()
	tx, err := svc.enrollment.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	mock.ExpectExec("INSERT INTO openai_oauth_reauth_configs.*ON CONFLICT.*DO NOTHING").WithArgs(
		int64(42), "user@example.com", OpenAIOAuthReauthModePasswordTOTP, OpenAIOAuthReauthEngineLocal, "random_managed", nil, "encrypted:new-password", "", "").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	changed, err := svc.writeEnrollmentConfig(context.Background(), tx, 42, &AuthorizationSecret{Email: "user@example.com", Password: "new-password"}, nil, nil)
	require.False(t, changed)
	require.Equal(t, "account_changed", tokenGuardV2EnrollmentError(err))
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountTokenGuardV2SyncCursorAndAutoEnrollmentDefault(t *testing.T) {
	svc, mock := tokenGuardV2EnrollmentTestService(t)
	// Old installations never start enrolling accounts merely on upgrade.
	require.NoError(t, svc.runAutoEnrollment(context.Background()))
	_, err := svc.SyncAccountsAfter(context.Background(), -1)
	require.Error(t, err)
	mock.ExpectQuery("SELECT a.id,.*ORDER BY a.id LIMIT").WithArgs(int64(41), tokenGuardV2BatchLimit).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email"}))
	result, err := svc.SyncAccountsAfter(context.Background(), 41)
	require.NoError(t, err)
	require.Empty(t, result.Items)
	require.False(t, result.HasMore)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountTokenGuardV2EnrollmentPreservesDisabledAndBannedAccounts(t *testing.T) {
	for _, inserted := range []int64{0, 1} {
		t.Run(fmt.Sprintf("inserted_%d", inserted), func(t *testing.T) {
			svc, mock := tokenGuardV2EnrollmentTestService(t)
			tokenGuardV2ExpectEnrollmentAccount(mock, false, true)
			mock.ExpectQuery("SELECT login_email.*FOR UPDATE").WithArgs(int64(42)).WillReturnRows(tokenGuardV2ConfigTestRows())
			mock.ExpectQuery("SELECT secret_encrypted FROM").WithArgs("user@example.com").WillReturnRows(sqlmock.NewRows([]string{"secret"}))
			// Paused defaults apply only on INSERT. A pre-existing row is never
			// updated, so its switches and exhausted budget are preserved.
			mock.ExpectExec("INSERT INTO account_token_guard_v2_accounts.*ON CONFLICT.*DO NOTHING").
				WithArgs(int64(42), false, false).WillReturnResult(sqlmock.NewResult(0, inserted))
			mock.ExpectCommit()
			_, status, err := svc.enrollAccount(context.Background(), 42, nil, nil)
			require.NoError(t, err)
			require.Equal(t, "missing_credentials", status)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountTokenGuardV2CannotRemoveWhileAutoEnrollmentActive(t *testing.T) {
	repo := &accountTokenGuardV2TestRepo{record: &AccountTokenGuardV2Record{AccountID: 42, AutoReloginExhausted: true}}
	settings := &accountTokenGuardV2TestSettings{raw: `{"auto_enroll_enabled":true}`}
	svc := NewAccountTokenGuardV2Service(repo, settings, nil, nil, nil)
	err := svc.RemoveAccount(context.Background(), 42)
	require.Equal(t, "TOKEN_GUARD_V2_AUTO_ENROLL_ACTIVE", infraerrors.Reason(err))
	require.NotNil(t, repo.record)
	require.True(t, repo.record.AutoReloginExhausted)
	settings.raw = `{"auto_enroll_enabled":false}`
	require.NoError(t, svc.RemoveAccount(context.Background(), 42))
	require.Nil(t, repo.record)
}

func TestAccountTokenGuardV2DisableThenRemoveStopsStaleAutomaticBatch(t *testing.T) {
	svc, mock := tokenGuardV2EnrollmentTestService(t)
	// The outer worker snapshot still says enabled. The authoritative setting
	// has already been disabled before the administrator removes membership.
	svc.settings = &accountTokenGuardV2TestSettings{raw: `{"auto_enroll_enabled":true}`}
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT value FROM settings WHERE key=\$1`).WithArgs(accountTokenGuardV2RulesSettingKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"auto_enroll_enabled":false}`))
	mock.ExpectExec(`DELETE FROM account_token_guard_v2_accounts WHERE account_id=\$1`).WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, svc.RemoveAccount(context.Background(), 42))

	// The account remains a candidate after its monitoring row was removed.
	// No account/config lookup or INSERT may follow the per-row disabled check.
	mock.ExpectQuery("SELECT a.id,.*ORDER BY a.id LIMIT").WithArgs(int64(0), tokenGuardV2BatchLimit).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email"}).AddRow(42, "user@example.com"))
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT value FROM settings WHERE key=\$1`).WithArgs(accountTokenGuardV2RulesSettingKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"auto_enroll_enabled":false}`))
	mock.ExpectRollback()
	require.NoError(t, svc.runAutoEnrollment(context.Background()))
	require.Zero(t, svc.enrollment.afterID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountTokenGuardV2TransactionalRemovalUsesPersistedRules(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rows       *sqlmock.Rows
		wantReason string
	}{
		{"enabled", sqlmock.NewRows([]string{"value"}).AddRow(`{"auto_enroll_enabled":true}`), "TOKEN_GUARD_V2_AUTO_ENROLL_ACTIVE"},
		{"malformed", sqlmock.NewRows([]string{"value"}).AddRow(`{"auto_enroll_enabled":"bad"}`), "TOKEN_GUARD_V2_RULES_READ_FAILED"},
		{"missing defaults disabled", sqlmock.NewRows([]string{"value"}), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, mock := tokenGuardV2EnrollmentTestService(t)
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(`SELECT value FROM settings WHERE key=\$1`).WithArgs(accountTokenGuardV2RulesSettingKey).WillReturnRows(tc.rows)
			if tc.wantReason == "" {
				// Deleting already-absent membership remains a successful no-op.
				mock.ExpectExec(`DELETE FROM account_token_guard_v2_accounts WHERE account_id=\$1`).WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err := svc.RemoveAccount(context.Background(), 42)
			if tc.wantReason == "" {
				require.NoError(t, err)
			} else {
				require.Equal(t, tc.wantReason, infraerrors.Reason(err))
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountTokenGuardV2AutomaticEnrollmentChecksPersistedSwitchBeforeAccount(t *testing.T) {
	svc, mock := tokenGuardV2EnrollmentTestService(t)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT value FROM settings WHERE key=\$1`).WithArgs(accountTokenGuardV2RulesSettingKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"auto_enroll_enabled":true}`))
	mock.ExpectQuery("SELECT .* FROM accounts a WHERE a.id").WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"email", "supported", "paused"}).AddRow("user@example.com", false, false))
	mock.ExpectRollback()
	_, status, err := svc.enrollAccountWithMode(context.Background(), 42, nil, nil, true)
	require.NoError(t, err)
	require.Equal(t, "unsupported_account", status)
	require.NoError(t, mock.ExpectationsWereMet())
}

type accountTokenGuardV2TestRepo struct {
	mu          sync.Mutex
	createTask  func(context.Context, int64, string) (*OpenAIOAuthReauthTaskRecord, error)
	outcomes    []AccountReloginOutcome
	record      *AccountTokenGuardV2Record
	claimBusy   bool
	completion  *AccountTokenGuardV2ProbeCompletion
	rescheduled bool
	pruneErr    error
	pruneOrphan bool
	claimed     bool
}

func (r *accountTokenGuardV2TestRepo) PruneDeletedAccounts(context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pruneErr != nil {
		return 0, r.pruneErr
	}
	if r.pruneOrphan && r.record != nil {
		r.record = nil
		return 1, nil
	}
	return 0, nil
}

func (r *accountTokenGuardV2TestRepo) UpsertAccount(_ context.Context, accountID int64, enabled, autoRelogin bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.record == nil {
		r.record = &AccountTokenGuardV2Record{AccountID: accountID, NextProbeAt: time.Now()}
	}
	r.record.Enabled = enabled
	r.record.AutoReloginEnabled = autoRelogin
	return nil
}
func (r *accountTokenGuardV2TestRepo) DeleteAccount(context.Context, int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record = nil
	return nil
}
func (r *accountTokenGuardV2TestRepo) GetAccount(context.Context, int64) (*AccountTokenGuardV2Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.record == nil {
		return nil, nil
	}
	copy := *r.record
	return &copy, nil
}
func (r *accountTokenGuardV2TestRepo) ListAccounts(context.Context) ([]AccountTokenGuardV2Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.record == nil {
		return nil, nil
	}
	return []AccountTokenGuardV2Record{*r.record}, nil
}
func (r *accountTokenGuardV2TestRepo) ClaimDue(_ context.Context, owner string, duration time.Duration, _ int, dueBefore time.Time) ([]AccountTokenGuardV2Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimed = true
	if r.record == nil || r.claimBusy || !r.record.Enabled || r.record.NextProbeAt.After(dueBefore) || (r.record.LeaseUntil != nil && r.record.LeaseUntil.After(time.Now())) {
		return nil, nil
	}
	until := time.Now().Add(duration)
	r.record.LeaseOwner, r.record.LeaseUntil = owner, &until
	return []AccountTokenGuardV2Record{*r.record}, nil
}
func (r *accountTokenGuardV2TestRepo) ClaimAccount(_ context.Context, _ int64, owner string, duration time.Duration) (*AccountTokenGuardV2Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.record == nil || r.claimBusy {
		return nil, nil
	}
	until := time.Now().Add(duration)
	r.record.LeaseOwner, r.record.LeaseUntil = owner, &until
	copy := *r.record
	return &copy, nil
}
func (r *accountTokenGuardV2TestRepo) ReleaseProbe(_ context.Context, _ int64, owner string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.record != nil && r.record.LeaseOwner == owner {
		r.record.LeaseOwner, r.record.LeaseUntil = "", nil
	}
	return nil
}
func (r *accountTokenGuardV2TestRepo) CompleteProbe(_ context.Context, _ int64, _ string, result AccountTokenGuardV2ProbeCompletion) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completion = &result
	if r.record != nil {
		r.record.ProbeState = result.ProbeState
		r.record.ProbeDetail = result.ProbeDetail
		r.record.FailStreak = result.FailStreak
		r.record.LastProbeAt = &result.LastProbeAt
		r.record.LastReauthAt = result.LastReauthAt
		r.record.NextProbeAt = result.NextProbeAt
		r.record.CooldownUntil = result.CooldownUntil
		r.record.BlockedReason = result.BlockedReason
		r.record.LeaseOwner, r.record.LeaseUntil = "", nil
	}
	return nil
}
func (r *accountTokenGuardV2TestRepo) MarkReauthRequested(context.Context, int64, time.Time, time.Time) error {
	return nil
}
func (r *accountTokenGuardV2TestRepo) RescheduleEnabled(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rescheduled = true
	return nil
}

type accountTokenGuardV2TestSettings struct{ raw string }

func (s *accountTokenGuardV2TestSettings) GetValue(_ context.Context, key string) (string, error) {
	if s.raw == "" || key == openAIOAuthReauthRuntimeSettingsKey {
		return "", ErrSettingNotFound
	}
	return s.raw, nil
}
func (s *accountTokenGuardV2TestSettings) Set(_ context.Context, _ string, value string) error {
	s.raw = value
	return nil
}

type accountTokenGuardV2TestProber struct{ err error }

type accountTokenGuardV2BlockingRepo struct {
	accountTokenGuardV2TestRepo
	started      chan struct{}
	finished     chan struct{}
	once         sync.Once
	finishedOnce sync.Once
}

func (r *accountTokenGuardV2BlockingRepo) ClaimDue(ctx context.Context, _ string, _ time.Duration, _ int, _ time.Time) ([]AccountTokenGuardV2Record, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	r.finishedOnce.Do(func() { close(r.finished) })
	return nil, ctx.Err()
}

func TestAccountTokenGuardV2StopCancelsCycle(t *testing.T) {
	reauth, reader, _, _, _, _ := newReauthTestService("acct-1")
	repo := &accountTokenGuardV2BlockingRepo{started: make(chan struct{}), finished: make(chan struct{})}
	svc := NewAccountTokenGuardV2Service(repo, nil, reader, accountTokenGuardV2TestProber{}, reauth)
	svc.Start()
	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("cycle did not start")
	}
	svc.Stop()
	select {
	case <-repo.finished:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel the in-flight cycle")
	}
	svc.Start()
	svc.Stop()
}

func (p accountTokenGuardV2TestProber) FetchOpenAIModelsListFresh(context.Context, *Account) (*OpenAIModelsResponse, error) {
	return nil, p.err
}

func TestAccountTokenGuardV2Explicit403PolicyDoesNotQueueRelogin(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"session_blocked_by_cyber_policy","message":"synthetic-private"}}`,
		`{"error":{"code":"cyber_policy","message":"synthetic-private"}}`,
		`{"error":{"type":"content_policy_violation","message":"synthetic-private"}}`,
		`{"error":{"code":"upstream_error","type":"upstream_error","message":"failed to WebSocket dial: expected handshake response status code 101 but got 403"}}`,
	} {
		for _, exhausted := range []bool{false, true} {
			t.Run(fmt.Sprintf("exhausted=%t/%s", exhausted, body[:min(len(body), 65)]), func(t *testing.T) {
				reauth, reader, reauthRepo, updater, _, _ := newReauthTestService("acct-1")
				require.NoError(t, reauthRepo.UpsertConfig(context.Background(), reauthEmailConfig(42, "user@example.test", "encrypted:https://mail.example.test/code")))
				cooldown := time.Now().Add(-time.Hour)
				failures := 0
				if exhausted {
					failures = 3
				}
				guardRepo := &accountTokenGuardV2TestRepo{record: &AccountTokenGuardV2Record{
					AccountID: 42, Enabled: true, AutoReloginEnabled: true, FailStreak: 390,
					ReloginFailStreak: failures, AutoReloginExhausted: exhausted, CooldownUntil: &cooldown,
				}}
				guardRepo.createTask = reauthRepo.CreateTask
				svc := NewAccountTokenGuardV2Service(guardRepo, nil, reader, accountTokenGuardV2TestProber{err: fmt.Errorf("wrapped: %w", &codexModelsManifestUpstreamError{
					err: errors.New("unauthorized synthetic-private"), statusCode: http.StatusForbidden, body: []byte(body),
				})}, reauth)
				require.NoError(t, svc.runProbe(context.Background(), *guardRepo.record))
				require.NotNil(t, guardRepo.completion)
				require.Equal(t, AccountTokenGuardV2ProbeTransient, guardRepo.completion.ProbeState)
				require.Equal(t, accountTokenGuardV2PolicyBlocked, guardRepo.completion.BlockedReason)
				require.NotContains(t, guardRepo.completion.ProbeDetail, "synthetic-private")
				require.Equal(t, 390, guardRepo.completion.FailStreak)
				require.Equal(t, &cooldown, guardRepo.completion.CooldownUntil)
				require.Zero(t, reauthRepo.createCalls)
				require.False(t, updater.applied)
				require.Equal(t, failures, guardRepo.record.ReloginFailStreak)
				require.Equal(t, exhausted, guardRepo.record.AutoReloginExhausted)
			})
		}
	}
}

func TestAccountTokenGuardV2PolicyClassifierDoesNotTrustMessagesOrEchoes(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"account_deactivated","message":"session_blocked_by_cyber_policy"}}`,
		`{"error":{"code":"account_deactivated"},"input":{"error":{"code":"cyber_policy"}}}`,
		`{"error":{"code":"account_deactivated","message":"synthetic-private"}}`,
	} {
		svc := &AccountTokenGuardV2Service{prober: accountTokenGuardV2TestProber{err: &codexModelsManifestUpstreamError{
			err: errors.New("synthetic-private"), statusCode: http.StatusForbidden, body: []byte(body),
		}}}
		state, detail := svc.probeAccount(context.Background(), &Account{Platform: PlatformOpenAI, Credentials: map[string]any{"access_token": "synthetic"}})
		require.Equal(t, AccountTokenGuardV2ProbeTransient, state)
		require.Equal(t, credentialOperationDetail("account_deactivated"), detail)
		require.Equal(t, "account_deactivated", probeOperationFailure(state, detail, nil).Code)
	}
}

func TestAccountTokenGuardV2SecondAuthFailureQueuesRelogin(t *testing.T) {
	reauth, reader, reauthRepo, _, _, _ := newReauthTestService("acct-1")
	require.NoError(t, reauthRepo.UpsertConfig(context.Background(), reauthEmailConfig(42, "user@example.com", "encrypted:https://mail.example.com/code")))
	guardRepo := &accountTokenGuardV2TestRepo{record: &AccountTokenGuardV2Record{
		AccountID: 42, Enabled: true, AutoReloginEnabled: true, FailStreak: 1, NextProbeAt: time.Now(),
	}}
	guardRepo.createTask = reauthRepo.CreateTask
	svc := NewAccountTokenGuardV2Service(guardRepo, nil, reader, accountTokenGuardV2TestProber{err: &codexModelsManifestUpstreamError{
		err: errors.New("unauthorized"), statusCode: http.StatusUnauthorized,
	}}, reauth)
	record, err := guardRepo.GetAccount(context.Background(), 42)
	require.NoError(t, err)

	require.NoError(t, svc.runProbe(context.Background(), *record))

	require.NotNil(t, guardRepo.completion)
	require.Equal(t, AccountTokenGuardV2ProbeAuth, guardRepo.completion.ProbeState)
	require.Equal(t, 2, guardRepo.completion.FailStreak)
	require.NotNil(t, guardRepo.completion.LastReauthAt)
	require.NotNil(t, guardRepo.completion.CooldownUntil)
	require.Contains(t, guardRepo.completion.ProbeDetail, "automatic re-login queued")
	require.Equal(t, 1, reauthRepo.createCalls)
	require.NotNil(t, reauthRepo.task)
}

func TestAccountTokenGuardV2ActiveReloginTaskIsNotDuplicated(t *testing.T) {
	reauth, reader, reauthRepo, _, _, _ := newReauthTestService("acct-1")
	require.NoError(t, reauthRepo.UpsertConfig(context.Background(), reauthEmailConfig(42, "user@example.com", "encrypted:https://mail.example.com/code")))
	existing := &OpenAIOAuthReauthTaskRecord{ID: 99, AccountID: 42, Status: OpenAIOAuthReauthStatusRunning, Stage: OpenAIOAuthReauthStageStarting}
	reauthRepo.task = existing
	reauthRepo.createErr = infraerrors.Conflict("OPENAI_REAUTH_TASK_ACTIVE", "task already active")
	guardRepo := &accountTokenGuardV2TestRepo{record: &AccountTokenGuardV2Record{
		AccountID: 42, Enabled: true, AutoReloginEnabled: true, FailStreak: 1, NextProbeAt: time.Now(),
	}}
	guardRepo.createTask = reauthRepo.CreateTask
	svc := NewAccountTokenGuardV2Service(guardRepo, nil, reader, accountTokenGuardV2TestProber{err: errors.New("invalid token")}, reauth)
	record, err := guardRepo.GetAccount(context.Background(), 42)
	require.NoError(t, err)

	require.NoError(t, svc.runProbe(context.Background(), *record))

	require.Same(t, existing, reauthRepo.task)
	require.Equal(t, 1, reauthRepo.createCalls)
	require.Contains(t, guardRepo.completion.ProbeDetail, "re-login task already active")
	require.Nil(t, guardRepo.completion.LastReauthAt)
}

func TestAccountTokenGuardV2ProbeNowDistinguishesMissingAndBusy(t *testing.T) {
	reauth, reader, _, _, _, _ := newReauthTestService("acct-1")
	prober := accountTokenGuardV2TestProber{}

	missing := NewAccountTokenGuardV2Service(&accountTokenGuardV2TestRepo{}, nil, reader, prober, reauth)
	_, err := missing.ProbeNow(context.Background(), 42)
	require.Equal(t, http.StatusNotFound, infraerrors.Code(err))

	busyRepo := &accountTokenGuardV2TestRepo{
		record:    &AccountTokenGuardV2Record{AccountID: 42, NextProbeAt: time.Now()},
		claimBusy: true,
	}
	busy := NewAccountTokenGuardV2Service(busyRepo, nil, reader, prober, reauth)
	_, err = busy.ProbeNow(context.Background(), 42)
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
}

func TestAccountTokenGuardV2RulesDefaultAndPersistence(t *testing.T) {
	repo := &accountTokenGuardV2TestRepo{}
	settings := &accountTokenGuardV2TestSettings{}
	svc := NewAccountTokenGuardV2Service(repo, settings, nil, nil, nil)

	rules, err := svc.GetRules(context.Background())
	require.NoError(t, err)
	require.Equal(t, defaultAccountTokenGuardV2Rules(), rules)

	want := AccountTokenGuardV2Rules{
		ProbeIntervalSeconds: 900, RetryIntervalSeconds: 60,
		ReloginCooldownSeconds: 7200, FailStreakThreshold: 3,
	}
	want = NormalizeReloginBudgetRules(want)
	saved, err := svc.SaveRules(context.Background(), want)
	require.NoError(t, err)
	require.Equal(t, want, saved)
	require.True(t, repo.rescheduled)

	var persisted AccountTokenGuardV2Rules
	require.NoError(t, json.Unmarshal([]byte(settings.raw), &persisted))
	require.Equal(t, want, persisted)
	reread, err := svc.GetRules(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, reread)
}

func TestAccountTokenGuardV2RulesRejectInvalidRange(t *testing.T) {
	repo := &accountTokenGuardV2TestRepo{}
	settings := &accountTokenGuardV2TestSettings{}
	svc := NewAccountTokenGuardV2Service(repo, settings, nil, nil, nil)

	_, err := svc.SaveRules(context.Background(), AccountTokenGuardV2Rules{
		ProbeIntervalSeconds: 59, RetryIntervalSeconds: 60,
		ReloginCooldownSeconds: 1800, FailStreakThreshold: 2,
	})
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Empty(t, settings.raw)
	require.False(t, repo.rescheduled)
}

func TestAccountTokenGuardV2InvalidSavedRulesDoNotQueueRelogin(t *testing.T) {
	for _, test := range []struct{ name, raw, wantError string }{
		{"invalid JSON", "invalid-json", "invalid character"},
		{"invalid interval", `{"probe_interval_seconds":59,"retry_interval_seconds":60,"relogin_cooldown_seconds":1800,"fail_streak_threshold":2}`, "probe_interval_seconds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reauth, reader, reauthRepo, _, _, _ := newReauthTestService("acct-1")
			next := time.Now().Add(-time.Minute)
			until := time.Now().Add(time.Minute)
			record := AccountTokenGuardV2Record{
				AccountID: 42, Enabled: true, AutoReloginEnabled: true, FailStreak: 2,
				NextProbeAt: next, LeaseOwner: "invalid-rules-claim", LeaseUntil: &until,
			}
			repo := &accountTokenGuardV2TestRepo{record: &record}
			settings := &accountTokenGuardV2TestSettings{raw: test.raw}
			svc := NewAccountTokenGuardV2Service(repo, settings, reader, accountTokenGuardV2TestProber{err: errors.New("invalid token")}, reauth)
			t.Cleanup(svc.cancel)

			require.ErrorContains(t, svc.runProbe(context.Background(), record), test.wantError)

			require.Zero(t, reauthRepo.createCalls)
			require.Nil(t, repo.completion)
			require.Equal(t, 2, repo.record.FailStreak)
			require.Equal(t, next, repo.record.NextProbeAt)
			require.Nil(t, repo.record.LastProbeAt)
			require.Empty(t, repo.record.LeaseOwner)
			require.Nil(t, repo.record.LeaseUntil)
		})
	}
}

func TestAccountTokenGuardV2CustomRulesDriveProbe(t *testing.T) {
	reauth, reader, reauthRepo, _, _, _ := newReauthTestService("acct-1")
	require.NoError(t, reauthRepo.UpsertConfig(context.Background(), reauthEmailConfig(42, "user@example.com", "encrypted:https://mail.example.com/code")))
	guardRepo := &accountTokenGuardV2TestRepo{record: &AccountTokenGuardV2Record{
		AccountID: 42, Enabled: true, AutoReloginEnabled: true, NextProbeAt: time.Now(),
	}}
	guardRepo.createTask = reauthRepo.CreateTask
	settings := &accountTokenGuardV2TestSettings{}
	svc := NewAccountTokenGuardV2Service(guardRepo, settings, reader, accountTokenGuardV2TestProber{err: errors.New("invalid token")}, reauth)
	_, err := svc.SaveRules(context.Background(), AccountTokenGuardV2Rules{
		ProbeIntervalSeconds: 600, RetryIntervalSeconds: 45,
		ReloginCooldownSeconds: 7200, FailStreakThreshold: 1,
	})
	require.NoError(t, err)
	record, err := guardRepo.GetAccount(context.Background(), 42)
	require.NoError(t, err)

	require.NoError(t, svc.runProbe(context.Background(), *record))

	require.Equal(t, 1, reauthRepo.createCalls)
	require.WithinDuration(t, guardRepo.completion.LastProbeAt.Add(45*time.Second), guardRepo.completion.NextProbeAt, time.Second)
	require.NotNil(t, guardRepo.completion.CooldownUntil)
	require.WithinDuration(t, guardRepo.completion.LastProbeAt.Add(2*time.Hour), *guardRepo.completion.CooldownUntil, time.Second)
}

func TestAccountTokenGuardV2RunDuePrunesBeforeClaim(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			repo := &accountTokenGuardV2TestRepo{
				record:      &AccountTokenGuardV2Record{AccountID: 42, Enabled: enabled, NextProbeAt: time.Now().Add(time.Hour)},
				pruneOrphan: true,
			}
			// No account reader/prober: orphaned records must never reach probing.
			svc := NewAccountTokenGuardV2Service(repo, nil, nil, nil, nil)
			defer svc.cancel()
			n, err := svc.RunDue(context.Background())
			require.NoError(t, err)
			require.Zero(t, n)
			require.Nil(t, repo.record)
			require.True(t, repo.claimed)
			n, err = svc.RunDue(context.Background())
			require.NoError(t, err)
			require.Zero(t, n, "cleanup must be idempotent")
		})
	}
}

func TestAccountTokenGuardV2RunDuePruneFailurePreservesRecords(t *testing.T) {
	repo := &accountTokenGuardV2TestRepo{
		record:   &AccountTokenGuardV2Record{AccountID: 42},
		pruneErr: errors.New("database unavailable"),
	}
	svc := NewAccountTokenGuardV2Service(repo, nil, nil, nil, nil)
	defer svc.cancel()
	n, err := svc.RunDue(context.Background())
	require.ErrorIs(t, err, repo.pruneErr)
	require.Zero(t, n)
	require.NotNil(t, repo.record)
	require.False(t, repo.claimed)
}

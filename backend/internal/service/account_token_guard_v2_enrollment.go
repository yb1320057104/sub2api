package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/lib/pq"
)

const tokenGuardV2BatchLimit = 200
const tokenGuardV2AutoEnrollmentDisabled = "auto_enrollment_disabled"

type AccountTokenGuardV2BatchInput struct {
	Accounts           string `json:"accounts"`
	Engine             string `json:"engine,omitempty"`
	ProxySource        string `json:"proxy_source,omitempty"`
	ProxyID            *int64 `json:"proxy_id,omitempty"`
	Enabled            *bool  `json:"enabled,omitempty"`
	AutoReloginEnabled *bool  `json:"auto_relogin_enabled,omitempty"`
}

type AccountTokenGuardV2BatchItem struct {
	Line      int    `json:"line"`
	Email     string `json:"email,omitempty"`
	AccountID int64  `json:"account_id,omitempty"`
	Status    string `json:"status"`
}

type AccountTokenGuardV2BatchResult struct {
	Items       []AccountTokenGuardV2BatchItem `json:"items"`
	Succeeded   int                            `json:"succeeded"`
	Skipped     int                            `json:"skipped"`
	Failed      int                            `json:"failed"`
	HasMore     bool                           `json:"has_more,omitempty"`
	NextAfterID int64                          `json:"next_after_id,omitempty"`
}

func (r *AccountTokenGuardV2BatchResult) append(item AccountTokenGuardV2BatchItem) {
	r.Items = append(r.Items, item)
	switch item.Status {
	case "added", "updated":
		r.Succeeded++
	case "busy", "unchanged", "missing_credentials", "duplicate_email":
		r.Skipped++
	default:
		r.Failed++
	}
}

type tokenGuardV2BatchRow struct {
	item   AccountTokenGuardV2BatchItem
	secret AuthorizationSecret
}

type accountTokenGuardV2Enrollment struct {
	db               *sql.DB
	historyEncryptor SecretEncryptor
	mu               sync.Mutex
	afterID          int64
}

// ConfigureEnrollment is called before Start. The history and credential
// operations stores intentionally use their own encryption keys.
func (s *AccountTokenGuardV2Service) ConfigureEnrollment(db *sql.DB, historyEncryptor SecretEncryptor) {
	s.enrollment = &accountTokenGuardV2Enrollment{db: db, historyEncryptor: historyEncryptor}
}

func parseTokenGuardV2Batch(raw string) ([]tokenGuardV2BatchRow, error) {
	if len(raw) > 2<<20 {
		return nil, infraerrors.BadRequest("TOKEN_GUARD_V2_BATCH_LIMIT", "每批最多200行，内容不得超过2 MiB")
	}
	rows := make([]tokenGuardV2BatchRow, 0)
	seen := make(map[string]bool)
	for i, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(rows) >= tokenGuardV2BatchLimit {
			return nil, infraerrors.BadRequest("TOKEN_GUARD_V2_BATCH_LIMIT", "每批最多200行，内容不得超过2 MiB")
		}
		row := tokenGuardV2BatchRow{item: AccountTokenGuardV2BatchItem{Line: i + 1}}
		parts := strings.Split(line, "----")
		if len(parts) < 2 || len(parts) > 3 {
			row.item.Status = "invalid_format"
			rows = append(rows, row)
			continue
		}
		email := strings.ToLower(strings.TrimSpace(parts[0]))
		address, err := mail.ParseAddress(email)
		if err != nil || address.Address != email || len(email) > 254 {
			row.item.Status = "invalid_email"
			rows = append(rows, row)
			continue
		}
		row.item.Email = email
		switch {
		case seen[email]:
			row.item.Status = "duplicate_email"
		case parts[1] == "" || len(parts[1]) > 1024:
			row.item.Status = "invalid_password"
		default:
			row.secret = AuthorizationSecret{Email: email, Password: parts[1]}
			if len(parts) == 3 {
				row.secret.TwoFA = parts[2]
			}
			if validateAuthorizationSecret(&row.secret, false) != nil {
				row.item.Status = "invalid_twofa"
			}
		}
		seen[email] = true
		if row.item.Status != "" {
			row.secret = AuthorizationSecret{}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, infraerrors.BadRequest("TOKEN_GUARD_V2_BATCH_EMPTY", "请填写至少一行邮箱和密码，可附加2FA密钥")
	}
	return rows, nil
}

func (s *AccountTokenGuardV2Service) enrollmentReady() error {
	if s == nil || s.enrollment == nil || s.enrollment.db == nil || s.reauth == nil {
		return infraerrors.ServiceUnavailable("TOKEN_GUARD_V2_ENROLLMENT_UNAVAILABLE", "credential enrollment is unavailable")
	}
	return nil
}

func (s *AccountTokenGuardV2Service) BatchAddAccounts(ctx context.Context, input *AccountTokenGuardV2BatchInput) (*AccountTokenGuardV2BatchResult, error) {
	if input == nil {
		return nil, infraerrors.BadRequest("TOKEN_GUARD_V2_BATCH_EMPTY", "请填写至少一行邮箱和密码")
	}
	rows, err := parseTokenGuardV2Batch(input.Accounts)
	input.Accounts = ""
	if err != nil {
		return nil, err
	}
	defer func() {
		for i := range rows {
			rows[i].secret = AuthorizationSecret{}
		}
	}()
	if err := s.enrollmentReady(); err != nil {
		return nil, err
	}
	if err := s.reauth.ensureDurableEncryption(); err != nil {
		return nil, err
	}
	result := &AccountTokenGuardV2BatchResult{Items: make([]AccountTokenGuardV2BatchItem, 0, len(rows))}
	for i := range rows {
		row := &rows[i]
		if row.item.Status == "" {
			if ctx.Err() != nil {
				row.item.Status = "cancelled"
			} else {
				rowCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				row.item.AccountID, row.item.Status, err = s.enrollAccount(rowCtx, 0, &row.secret, input)
				cancel()
				if err != nil {
					row.item.Status = tokenGuardV2EnrollmentError(err)
				}
			}
		}
		row.secret = AuthorizationSecret{}
		result.append(row.item)
	}
	return result, nil
}

// SyncAccounts copies history only when no login configuration exists. Existing
// configuration, including intentionally cleared fields, is never supplemented.
// Pagination is by account ID so a large installation can be processed without
// holding an open result set while writing rows on the same connection pool.
func (s *AccountTokenGuardV2Service) SyncAccounts(ctx context.Context) (*AccountTokenGuardV2BatchResult, error) {
	return s.SyncAccountsAfter(ctx, 0)
}

func (s *AccountTokenGuardV2Service) SyncAccountsAfter(ctx context.Context, after int64) (*AccountTokenGuardV2BatchResult, error) {
	if err := s.enrollmentReady(); err != nil {
		return nil, err
	}
	if after < 0 {
		return nil, infraerrors.BadRequest("TOKEN_GUARD_V2_SYNC_CURSOR_INVALID", "invalid enrollment cursor")
	}
	ctx, cancel := context.WithTimeout(ctx, 80*time.Second)
	defer cancel()
	result := &AccountTokenGuardV2BatchResult{Items: make([]AccountTokenGuardV2BatchItem, 0)}
	for {
		items, err := s.enrollmentCandidates(ctx, after)
		if err != nil {
			return nil, infraerrors.ServiceUnavailable("TOKEN_GUARD_V2_SYNC_FAILED", "failed to list eligible accounts")
		}
		for _, item := range items {
			item.Line = len(result.Items) + 1
			if ctx.Err() != nil {
				result.HasMore, result.NextAfterID = true, after
				return result, nil
			} else {
				rowCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				_, item.Status, err = s.enrollAccount(rowCtx, item.AccountID, nil, nil)
				cancel()
				if err != nil {
					item.Status = tokenGuardV2EnrollmentError(err)
				}
			}
			result.append(item)
			if ctx.Err() != nil && item.Status == "interrupted" {
				result.HasMore, result.NextAfterID = true, after
				return result, nil
			}
			after = item.AccountID
		}
		if len(items) < tokenGuardV2BatchLimit {
			return result, nil
		}
		if len(result.Items) >= 1000 || ctx.Err() != nil {
			result.HasMore, result.NextAfterID = true, after
			return result, nil
		}
	}
}

func (s *AccountTokenGuardV2Service) enrollmentCandidates(ctx context.Context, after int64) ([]AccountTokenGuardV2BatchItem, error) {
	rows, err := s.enrollment.db.QueryContext(ctx, `SELECT a.id,`+authorizationEmailSQL+` FROM accounts a
		WHERE a.id>$1 AND a.deleted_at IS NULL AND a.platform='openai' AND `+authorizationSupportedSQL+`
		ORDER BY a.id LIMIT $2`, after, tokenGuardV2BatchLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]AccountTokenGuardV2BatchItem, 0)
	for rows.Next() {
		var item AccountTokenGuardV2BatchItem
		if err := rows.Scan(&item.AccountID, &item.Email); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *AccountTokenGuardV2Service) runAutoEnrollment(ctx context.Context) error {
	if s.enrollment == nil || s.enrollment.db == nil {
		return nil
	}
	rules, err := s.GetRules(ctx)
	if err != nil || !rules.AutoEnrollEnabled {
		return err
	}
	if !s.enrollment.mu.TryLock() {
		return nil
	}
	defer s.enrollment.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	items, err := s.enrollmentCandidates(ctx, s.enrollment.afterID)
	if err != nil {
		return errors.New("failed to list credential enrollment candidates")
	}
	result := &AccountTokenGuardV2BatchResult{}
	defer func() {
		if result.Failed > 0 {
			slog.Warn("account_token_guard_v2_enrollment_rows_failed", "failed", result.Failed, "succeeded", result.Succeeded, "skipped", result.Skipped)
		}
	}()
	for _, item := range items {
		if ctx.Err() != nil {
			return nil
		}
		// Failures remain eligible on the next pass; never log database or cipher
		// errors, which can include private row values.
		_, item.Status, err = s.enrollAccountWithMode(ctx, item.AccountID, nil, nil, true)
		if err == nil && item.Status == tokenGuardV2AutoEnrollmentDisabled {
			return nil
		}
		if err != nil {
			item.Status = tokenGuardV2EnrollmentError(err)
		}
		result.append(item)
		s.enrollment.afterID = item.AccountID
	}
	if len(items) < tokenGuardV2BatchLimit {
		s.enrollment.afterID = 0
	}
	return nil
}

// The shared history lock fences both authorization queues. Account, current
// configuration, and membership are checked and changed in one transaction, so
// manual edits and active legacy jobs cannot be overwritten between steps.
func (s *AccountTokenGuardV2Service) enrollAccount(ctx context.Context, accountID int64, supplied *AuthorizationSecret, input *AccountTokenGuardV2BatchInput) (int64, string, error) {
	return s.enrollAccountWithMode(ctx, accountID, supplied, input, false)
}

func (s *AccountTokenGuardV2Service) enrollAccountWithMode(ctx context.Context, accountID int64, supplied *AuthorizationSecret, input *AccountTokenGuardV2BatchInput, automatic bool) (int64, string, error) {
	tx, err := s.enrollment.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return accountID, "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAuthorizationHistory(ctx, tx); err != nil {
		return accountID, "", err
	}
	if automatic {
		enabled, err := tokenGuardV2AutoEnrollmentEnabledTx(ctx, tx)
		if err != nil {
			return accountID, "", err
		}
		if !enabled {
			return accountID, tokenGuardV2AutoEnrollmentDisabled, nil
		}
	}
	var email string
	var paused bool
	if supplied != nil {
		email = supplied.Email
		accountID, err = authorizationSupplementAccount(ctx, tx, email)
	} else {
		var supported bool
		err = tx.QueryRowContext(ctx, `SELECT `+authorizationEmailSQL+`,`+authorizationSupportedSQL+`,
			(a.status='disabled' OR NOT a.schedulable
			OR EXISTS(SELECT 1 FROM pk_authorization_state st WHERE st.account_id=a.id AND st.status='banned' AND st.credential_version=md5(a.credentials::text))
			OR COALESCE((SELECT status FROM pk_authorization_jobs WHERE email=`+authorizationEmailSQL+`
			AND (status IN ('banned','completed') OR settings->>'authorization_completed'='true') ORDER BY id DESC LIMIT 1),'')='banned')
			FROM accounts a WHERE a.id=$1 AND a.deleted_at IS NULL AND a.platform='openai' FOR UPDATE OF a`, accountID).Scan(&email, &supported, &paused)
		if err == nil && !supported {
			return accountID, "unsupported_account", nil
		}
		// Copying by email is unsafe when more than one account has that email.
		if err == nil && email != "" {
			var count int
			err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts a WHERE a.deleted_at IS NULL
				AND a.platform='openai' AND `+authorizationEmailSQL+`=$1`, email).Scan(&count)
			if err == nil && count != 1 {
				return accountID, "ambiguous_email", nil
			}
		}
	}
	if err != nil {
		return accountID, "", err
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_authorization_jobs
		WHERE status IN ('queued','running') AND (account_id=$1 OR lower(trim(email))=$2))
		OR EXISTS(SELECT 1 FROM openai_oauth_reauth_tasks WHERE account_id=$1 AND status IN ('queued','running','callback_processing'))`, accountID, email).Scan(&busy)
	if err != nil || busy {
		return accountID, "busy", err
	}
	stored, err := tokenGuardV2ConfigTx(ctx, tx, accountID)
	if err != nil {
		return accountID, "", err
	}
	var configured bool
	if stored != nil {
		configured = (stored.CredentialMode == OpenAIOAuthReauthModePasswordTOTP && stored.PasswordCiphertext != "") ||
			(stored.CredentialMode == OpenAIOAuthReauthModeEmailOTPURL && stored.OTPURLCiphertext != "")
	}
	secret := supplied
	if supplied == nil && email != "" && stored == nil {
		var encrypted string
		err := tx.QueryRowContext(ctx, `SELECT secret_encrypted FROM (`+authorizationCurrentHistorySQL+`) h WHERE h.email=$1`, email).Scan(&encrypted)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return accountID, "", err
		}
		if err == nil {
			secret, err = s.decodeEnrollmentHistory(email, encrypted)
			if err != nil {
				return accountID, "", err
			}
			defer func() { *secret = AuthorizationSecret{} }()
		}
	}
	changed := false
	if secret != nil {
		changed, err = s.writeEnrollmentConfig(ctx, tx, accountID, secret, stored, input)
		if err != nil {
			return accountID, "", err
		}
		configured = configured || changed
	}
	var enabled, autoRelogin *bool
	if input != nil {
		enabled, autoRelogin = input.Enabled, input.AutoReloginEnabled
	} else if paused {
		disabled := false
		enabled, autoRelogin = &disabled, &disabled
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO account_token_guard_v2_accounts
		(account_id,enabled,auto_relogin_enabled,next_probe_at,last_counted_task_id)
		VALUES($1,COALESCE($2,true),COALESCE($3,true),NOW(),COALESCE((SELECT MAX(task.id) FROM openai_oauth_reauth_tasks task
			WHERE task.account_id=$1 AND task.oauth_profile='codex' AND task.status IN ('failed','succeeded')
			AND NOT EXISTS(SELECT 1 FROM openai_totp_rotations rotation WHERE rotation.task_id=task.id)),0))
		ON CONFLICT(account_id) DO NOTHING`, accountID, enabled, autoRelogin)
	if err != nil {
		return accountID, "", err
	}
	added, err := result.RowsAffected()
	if err != nil {
		return accountID, "", err
	}
	if added == 0 && input != nil && (enabled != nil || autoRelogin != nil) {
		_, err = tx.ExecContext(ctx, `UPDATE account_token_guard_v2_accounts SET
			next_probe_at=CASE WHEN $2=true AND NOT enabled THEN LEAST(next_probe_at,NOW()) ELSE next_probe_at END,
			enabled=COALESCE($2,enabled),auto_relogin_enabled=COALESCE($3,auto_relogin_enabled),updated_at=NOW()
			WHERE account_id=$1`, accountID, enabled, autoRelogin)
		if err != nil {
			return accountID, "", err
		}
		changed = true
	}
	if err := tx.Commit(); err != nil {
		return accountID, "", err
	}
	switch {
	case !configured:
		return accountID, "missing_credentials", nil
	case added > 0:
		return accountID, "added", nil
	case changed:
		return accountID, "updated", nil
	default:
		return accountID, "unchanged", nil
	}
}

// Read only after acquiring the shared history lock. No settings row lock is
// required: after a disable has committed, any earlier automatic transaction
// finishes before Remove can take the history lock, and every later one reads
// the disabled value. SaveRules commits its settings write before rescheduling
// accounts, so it never holds a settings lock while waiting for this lock.
// Both callers use READ COMMITTED so the setting read gets a fresh snapshot
// after any wait for the history lock, independent of database defaults.
func tokenGuardV2AutoEnrollmentEnabledTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, accountTokenGuardV2RulesSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var rules struct {
		AutoEnrollEnabled bool `json:"auto_enroll_enabled"`
	}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &rules); err != nil {
			return false, errors.New("automatic enrollment setting is invalid")
		}
	}
	return rules.AutoEnrollEnabled, nil
}

func (s *AccountTokenGuardV2Service) removeEnrolledAccount(ctx context.Context, accountID int64) error {
	failure := func() error {
		return infraerrors.New(http.StatusInternalServerError, "TOKEN_GUARD_V2_DELETE_FAILED", "failed to remove monitored account")
	}
	tx, err := s.enrollment.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return failure()
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAuthorizationHistory(ctx, tx); err != nil {
		return failure()
	}
	enabled, err := tokenGuardV2AutoEnrollmentEnabledTx(ctx, tx)
	if err != nil {
		return infraerrors.ServiceUnavailable("TOKEN_GUARD_V2_RULES_READ_FAILED", "failed to read automatic enrollment settings")
	}
	if enabled {
		return infraerrors.Conflict("TOKEN_GUARD_V2_AUTO_ENROLL_ACTIVE", "自动纳入已开启，请暂停该账号，或先关闭自动纳入再移除")
	}
	// Preserve DeleteAccount semantics: removing an absent row succeeds, and
	// stored login material is retained independently of monitoring membership.
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_token_guard_v2_accounts WHERE account_id=$1`, accountID); err != nil {
		return failure()
	}
	if err := tx.Commit(); err != nil {
		return failure()
	}
	return nil
}

func tokenGuardV2ConfigTx(ctx context.Context, tx *sql.Tx, accountID int64) (*OpenAIOAuthReauthStoredConfig, error) {
	var c OpenAIOAuthReauthStoredConfig
	var proxyID sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT login_email,credential_mode,engine,proxy_source,proxy_id,
		COALESCE(password_ciphertext,''),COALESCE(totp_secret_ciphertext,''),COALESCE(otp_url_ciphertext,''),updated_at
		FROM openai_oauth_reauth_configs WHERE account_id=$1 FOR UPDATE`, accountID).Scan(
		&c.LoginEmail, &c.CredentialMode, &c.Engine, &c.ProxySource, &proxyID,
		&c.PasswordCiphertext, &c.TOTPSecretCiphertext, &c.OTPURLCiphertext, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.AccountID = accountID
	if proxyID.Valid {
		c.ProxyID = &proxyID.Int64
	}
	return &c, nil
}

func (s *AccountTokenGuardV2Service) decodeEnrollmentHistory(email, encrypted string) (*AuthorizationSecret, error) {
	if s.enrollment.historyEncryptor == nil {
		return nil, errors.New("authorization history encryption is unavailable")
	}
	raw, err := s.enrollment.historyEncryptor.Decrypt(encrypted)
	if err != nil {
		return nil, errors.New("authorization history could not be decrypted")
	}
	var secret AuthorizationSecret
	if json.Unmarshal([]byte(raw), &secret) != nil || secret.Email != email || validateAuthorizationSecret(&secret, false) != nil {
		return nil, errors.New("authorization history is invalid")
	}
	return &secret, nil
}

func (s *AccountTokenGuardV2Service) writeEnrollmentConfig(ctx context.Context, tx *sql.Tx, accountID int64, secret *AuthorizationSecret, existing *OpenAIOAuthReauthStoredConfig, input *AccountTokenGuardV2BatchInput) (bool, error) {
	if err := s.reauth.ensureDurableEncryption(); err != nil {
		return false, err
	}
	c := OpenAIOAuthReauthStoredConfig{AccountID: accountID, LoginEmail: secret.Email,
		CredentialMode: OpenAIOAuthReauthModePasswordTOTP, Engine: OpenAIOAuthReauthEngineLocal, ProxySource: "random_managed"}
	if existing != nil {
		c = *existing
	}
	if input == nil && existing != nil {
		return false, nil
	}
	if input != nil {
		c.LoginEmail, c.CredentialMode, c.OTPURLCiphertext = secret.Email, OpenAIOAuthReauthModePasswordTOTP, ""
		if input.Engine != "" {
			c.Engine = input.Engine
		}
		if input.ProxySource != "" || input.ProxyID != nil {
			c.ProxySource, c.ProxyID = input.ProxySource, input.ProxyID
		}
	}
	var err error
	c.ProxySource, err = normalizeReauthProxySource(c.ProxySource, c.ProxyID)
	if err != nil {
		return false, err
	}
	runtime, err := s.reauth.GetRuntimeSettings(ctx)
	if err != nil {
		return false, err
	}
	if existing == nil || input != nil {
		c.Engine = effectiveReauthEngine(c.CredentialMode, c.Engine, runtime.Engine)
	}
	if c.Engine != OpenAIOAuthReauthEngineLocal && c.Engine != OpenAIOAuthReauthEngineSessionStudio {
		return false, errors.New("invalid re-login engine")
	}
	if c.Engine == OpenAIOAuthReauthEngineSessionStudio {
		if _, _, err := s.reauth.sessionStudioConfig(ctx); err != nil {
			return false, err
		}
	}
	if c.Engine == OpenAIOAuthReauthEngineLocal && c.ProxySource == OpenAIOAuthReauthProxySourceManagedProxy {
		if _, err := s.reauth.reauthProxyURL(ctx, c.ProxyID); err != nil {
			return false, err
		}
	}
	if c.Engine == OpenAIOAuthReauthEngineLocal && c.ProxySource == OpenAIOAuthReauthProxySourceMihomo && s.reauth.acquireMihomoProxy == nil {
		return false, errors.New("re-login proxy is unavailable")
	}
	changed := input != nil || existing == nil
	if input != nil || c.PasswordCiphertext == "" {
		c.PasswordCiphertext, err = s.reauth.encryptor.Encrypt(secret.Password)
		if err != nil {
			return false, err
		}
		changed = true
	}
	if secret.TwoFA != "" && (input != nil || c.TOTPSecretCiphertext == "") {
		c.TOTPSecretCiphertext, err = s.reauth.encryptor.Encrypt(secret.TwoFA)
		if err != nil {
			return false, err
		}
		changed = true
	}
	if c.LoginEmail == "" {
		c.LoginEmail, changed = secret.Email, true
	}
	if !changed {
		return false, nil
	}
	if existing == nil {
		result, err := tx.ExecContext(ctx, `INSERT INTO openai_oauth_reauth_configs
			(account_id,login_email,credential_mode,engine,proxy_source,proxy_id,password_ciphertext,totp_secret_ciphertext,otp_url_ciphertext)
			VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),NULLIF($9,'')) ON CONFLICT(account_id) DO NOTHING`,
			accountID, c.LoginEmail, c.CredentialMode, c.Engine, c.ProxySource, c.ProxyID, c.PasswordCiphertext, c.TOTPSecretCiphertext, c.OTPURLCiphertext)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if n == 0 {
			return false, infraerrors.Conflict("OPENAI_REAUTH_CONFIG_CHANGED", "login configuration changed")
		}
	} else {
		result, err := tx.ExecContext(ctx, `UPDATE openai_oauth_reauth_configs SET login_email=$2,credential_mode=$3,engine=$4,
			proxy_source=$5,proxy_id=$6,password_ciphertext=NULLIF($7,''),totp_secret_ciphertext=NULLIF($8,''),
			otp_url_ciphertext=NULLIF($9,''),updated_at=NOW() WHERE account_id=$1 AND updated_at=$10`,
			accountID, c.LoginEmail, c.CredentialMode, c.Engine, c.ProxySource, c.ProxyID, c.PasswordCiphertext, c.TOTPSecretCiphertext, c.OTPURLCiphertext, existing.UpdatedAt)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if n == 0 {
			return false, infraerrors.Conflict("OPENAI_REAUTH_CONFIG_CHANGED", "login configuration changed")
		}
	}
	return true, nil
}

func tokenGuardV2EnrollmentError(err error) string {
	if errors.Is(err, sql.ErrNoRows) {
		return "not_found"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "interrupted"
	}
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && pgErr.Code == "55000" {
		return "busy"
	}
	switch infraerrors.Reason(err) {
	case "AUTHORIZATION_DUPLICATE_ACCOUNT":
		return "ambiguous_email"
	case "AUTHORIZATION_ACCOUNT_UNSUPPORTED":
		return "unsupported_account"
	case "AUTHORIZATION_EMAIL_CHANGED", "OPENAI_REAUTH_CONFIG_CHANGED":
		return "account_changed"
	default:
		return "failed"
	}
}

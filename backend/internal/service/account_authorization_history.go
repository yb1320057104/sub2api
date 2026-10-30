package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// History is one current record per email. Legacy submission rows become
// visible only after their task ends; deleted rows prevent older revisions
// from reappearing. Pending rows only satisfy the existing job foreign key.
const authorizationCurrentHistorySQL = `SELECT latest.* FROM (
	SELECT DISTINCT ON(h.email) h.* FROM pk_authorization_history h
	WHERE h.source<>'pending' AND (h.source<>'submission'
		OR NOT EXISTS(SELECT 1 FROM pk_authorization_jobs j WHERE j.email=h.email AND j.history_id=h.id)
		OR EXISTS(SELECT 1 FROM pk_authorization_jobs j WHERE j.email=h.email AND j.history_id=h.id
			AND j.status NOT IN ('queued','running') AND (j.status<>'cancelled' OR j.settings->>'authorization_completed'='true')))
	ORDER BY h.email,h.id DESC
) latest WHERE latest.source<>'deleted' AND latest.secret_encrypted<>''`

const authorizationHistoryRevisionSQL = `md5(secret_encrypted||source||(extract(epoch FROM created_at))::text)`

type authorizationCredentialSnapshot struct {
	Encrypted string `json:"credential_snapshot_encrypted"`
	Revision  string `json:"credential_history_revision"`
	HasTwoFA  bool   `json:"credential_has_twofa"`
}

type AuthorizationCredentialUpdate struct {
	Password string `json:"password"`
	TwoFA    string `json:"twofa"`
	Revision string `json:"revision"`
}

// Serializes only short credential transactions, never browser/model requests.
// All mutators acquire this before job/account/history locks.
func lockAuthorizationHistory(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(70360004)`)
	return err
}

func authorizationHistorySlot(ctx context.Context, tx *sql.Tx, email string) (int64, string, error) {
	var id int64
	var revision string
	err := tx.QueryRowContext(ctx, `SELECT id,`+authorizationHistoryRevisionSQL+` FROM pk_authorization_history WHERE email=$1 ORDER BY id DESC LIMIT 1 FOR UPDATE`, email).Scan(&id, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `INSERT INTO pk_authorization_history(email,secret_encrypted,has_twofa,source) VALUES($1,'',false,'pending') RETURNING id,`+authorizationHistoryRevisionSQL, email).Scan(&id, &revision)
	}
	return id, revision, err
}

// Freeze pre-upgrade jobs before an administrator updates their referenced
// history. Newly submitted and scheduled jobs already contain an encrypted copy.
func pinLegacyAuthorizationCredentials(ctx context.Context, tx *sql.Tx, email string) error {
	_, err := tx.ExecContext(ctx, `UPDATE pk_authorization_jobs j SET settings=j.settings||jsonb_build_object(
		'credential_snapshot_encrypted',h.secret_encrypted,'credential_has_twofa',h.has_twofa,
		'credential_history_revision',md5(h.secret_encrypted||h.source||(extract(epoch FROM h.created_at))::text))
		FROM pk_authorization_history h WHERE j.email=h.email AND j.history_id=h.id AND j.email=$1
		AND j.status IN ('queued','running') AND NOT(j.settings ? 'credential_snapshot_encrypted')`, email)
	return err
}

func (s *AccountAuthorizationService) decodeAuthorizationSecret(email, encrypted string) (*AuthorizationSecret, error) {
	raw, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, errors.New("凭据无法解密，请检查持久化加密密钥")
	}
	var secret AuthorizationSecret
	if json.Unmarshal([]byte(raw), &secret) != nil || secret.Email != email {
		return nil, errors.New("凭据记录校验失败")
	}
	return &secret, nil
}

func (s *AccountAuthorizationService) authorizationJobSecret(ctx context.Context, job *authorizationJob) (*AuthorizationSecret, error) {
	var encrypted string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(j.settings->>'credential_snapshot_encrypted',h.secret_encrypted)
		FROM pk_authorization_jobs j JOIN pk_authorization_history h ON h.id=j.history_id
		WHERE j.id=$1 AND j.status='running' AND j.owner_token=$2`, job.id, job.token).Scan(&encrypted)
	if err != nil {
		return nil, err
	}
	return s.decodeAuthorizationSecret(job.email, encrypted)
}

// Archive after the entire task settles, including confirmed bans. An edit or
// deletion since submission takes precedence over the finishing job. Removing
// the encrypted snapshot makes retries idempotent and avoids retained secrets.
func (s *AccountAuthorizationService) archiveAuthorizationCredentials(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockAuthorizationHistory(ctx, tx); err != nil {
		return err
	}
	if err = archiveAuthorizationCredentialsTx(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func archiveAuthorizationCredentialsTx(ctx context.Context, tx *sql.Tx, id int64) error {
	var err error
	var email, source, status string
	var historyID int64
	var authorized bool
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT email,history_id,source,status,COALESCE(settings->>'authorization_completed','false')='true',
		jsonb_build_object('credential_snapshot_encrypted',settings->'credential_snapshot_encrypted',
		'credential_history_revision',settings->'credential_history_revision','credential_has_twofa',settings->'credential_has_twofa')
		FROM pk_authorization_jobs WHERE id=$1 AND status NOT IN ('queued','running')
		AND settings ? 'credential_snapshot_encrypted' FOR UPDATE`, id).Scan(&email, &historyID, &source, &status, &authorized, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var snapshot authorizationCredentialSnapshot
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	if source == "manual" && (status != "cancelled" || authorized) && snapshot.Encrypted != "" {
		_, err = tx.ExecContext(ctx, `UPDATE pk_authorization_history SET secret_encrypted=$3,has_twofa=$4,source='submission',created_at=NOW()
			WHERE id=$1 AND email=$2 AND `+authorizationHistoryRevisionSQL+`=$5
			AND id=(SELECT max(id) FROM pk_authorization_history WHERE email=$2)`, historyID, email, snapshot.Encrypted, snapshot.HasTwoFA, snapshot.Revision)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_authorization_jobs SET settings=settings-'credential_snapshot_encrypted'-'credential_history_revision'-'credential_has_twofa' WHERE id=$1`, id); err != nil {
		return err
	}
	return nil
}

func (s *AccountAuthorizationService) recoverAuthorizationHistory(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM pk_authorization_jobs WHERE status NOT IN ('queued','running') AND settings ? 'credential_snapshot_encrypted' ORDER BY id LIMIT 1000`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.archiveAuthorizationCredentials(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *AccountAuthorizationService) UpdateAuthorizationCredential(ctx context.Context, id int64, input *AuthorizationCredentialUpdate) error {
	if err := s.requireAuthorizationEncryption(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockAuthorizationHistory(ctx, tx); err != nil {
		return err
	}
	var email, revision string
	err = tx.QueryRowContext(ctx, `SELECT email,`+authorizationHistoryRevisionSQL+` FROM (`+authorizationCurrentHistorySQL+`) h WHERE id=$1`, id).Scan(&email, &revision)
	if err != nil {
		return err
	}
	if input.Revision == "" || input.Revision != revision {
		return infraerrors.Conflict("AUTHORIZATION_HISTORY_CHANGED", "凭据已被更新，请刷新列表后重新编辑")
	}
	secret := &AuthorizationSecret{Email: email, Password: input.Password, TwoFA: input.TwoFA}
	if err = validateAuthorizationSecret(secret, false); err != nil {
		return err
	}
	if err = pinLegacyAuthorizationCredentials(ctx, tx, email); err != nil {
		return err
	}
	currentID, _, err := authorizationHistorySlot(ctx, tx, email)
	if err != nil {
		return err
	}
	data, err := json.Marshal(secret)
	if err != nil {
		return err
	}
	encrypted, err := s.encryptor.Encrypt(string(data))
	if err != nil {
		return errors.New("凭据加密失败")
	}
	_, err = tx.ExecContext(ctx, `UPDATE pk_authorization_history SET secret_encrypted=$2,has_twofa=$3,source='updated',created_at=NOW() WHERE id=$1`, currentID, encrypted, secret.TwoFA != "")
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *AccountAuthorizationService) DeleteAuthorizationCredential(ctx context.Context, id int64, expectedRevision string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockAuthorizationHistory(ctx, tx); err != nil {
		return err
	}
	var email, revision string
	err = tx.QueryRowContext(ctx, `SELECT email,`+authorizationHistoryRevisionSQL+` FROM (`+authorizationCurrentHistorySQL+`) h WHERE id=$1`, id).Scan(&email, &revision)
	if err != nil {
		return err
	}
	if expectedRevision == "" || revision != expectedRevision {
		return infraerrors.Conflict("AUTHORIZATION_HISTORY_CHANGED", "凭据已被更新，请刷新列表后重试")
	}
	// Keep job/radar evidence and foreign keys, but erase every stored password
	// and TOTP revision for this email. Cancellation prevents in-flight commits.
	_, err = tx.ExecContext(ctx, `UPDATE pk_authorization_jobs SET
		status=CASE WHEN status IN ('queued','running') THEN 'cancelled' ELSE status END,
		stage=CASE WHEN status IN ('queued','running') THEN 'cancelled' ELSE stage END,
		message=CASE WHEN status IN ('queued','running') THEN '凭据已删除，任务已取消' ELSE message END,
		finished_at=CASE WHEN status IN ('queued','running') THEN NOW() ELSE finished_at END,
		settings=settings-'credential_snapshot_encrypted'-'credential_history_revision'-'credential_has_twofa'
		WHERE email=$1`, email)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE pk_authorization_history SET secret_encrypted='',has_twofa=false,source='deleted',created_at=NOW() WHERE email=$1`, email)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Finish earlier settled jobs before a new job snapshots this email. Otherwise
// a just-completed job could change the revision after the next job is queued.
func settleAuthorizationHistoryTx(ctx context.Context, tx *sql.Tx, email string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM pk_authorization_jobs WHERE ($1='' OR email=$1) AND status NOT IN ('queued','running') AND settings ? 'credential_snapshot_encrypted' ORDER BY id`, email)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = archiveAuthorizationCredentialsTx(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const rotationSelect = `SELECT task_id, account_id, state, action, worker_id, candidate_ciphertext,
 config_updated_at, error_code, updated_at FROM openai_totp_rotations `

func scanRotation(row openAIOAuthReauthRow) (*service.OpenAITOTPRotation, error) {
	var r service.OpenAITOTPRotation
	err := row.Scan(&r.TaskID, &r.AccountID, &r.State, &r.Action, &r.WorkerID, &r.CandidateCiphertext,
		&r.ConfigUpdatedAt, &r.ErrorCode, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}

func (r *openAIOAuthReauthRepository) GetTOTPRotation(ctx context.Context, accountID int64) (*service.OpenAITOTPRotation, error) {
	if err := r.expireTOTPRotations(ctx, accountID); err != nil {
		return nil, err
	}
	return scanRotation(r.db.QueryRowContext(ctx, rotationSelect+`WHERE account_id=$1 ORDER BY task_id DESC LIMIT 1`, accountID))
}

func (r *openAIOAuthReauthRepository) CreateTOTPRotation(ctx context.Context, accountID int64, hash string, version time.Time) (*service.OpenAITOTPRotation, error) {
	return r.createTOTPRotation(ctx, accountID, hash, version, false)
}

func (r *openAIOAuthReauthRepository) CreateTOTPRotationOn429(ctx context.Context, accountID int64, hash string, version time.Time) (*service.OpenAITOTPRotation, error) {
	return r.createTOTPRotation(ctx, accountID, hash, version, true)
}

func (r *openAIOAuthReauthRepository) createTOTPRotation(ctx context.Context, accountID int64, hash string, version time.Time, automatic bool) (*service.OpenAITOTPRotation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = service.LockCredentialAuthorizationQueue(ctx, tx, accountID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "SET LOCAL synchronous_commit = on"); err != nil {
		return nil, err
	}
	if automatic {
		eligible, eligibilityErr := lockAutomaticTOTPRotationEligibility(ctx, tx, accountID)
		if eligibilityErr != nil || !eligible {
			return nil, eligibilityErr
		}
	}
	var current time.Time
	if err = tx.QueryRowContext(ctx, `SELECT updated_at FROM openai_oauth_reauth_configs WHERE account_id=$1 FOR UPDATE`, accountID).Scan(&current); err != nil {
		return nil, err
	}
	if !current.Equal(version) {
		return nil, infraerrors.Conflict("TOTP_CONFIG_CHANGED", "Login configuration changed; reload before retrying")
	}
	if automatic {
		// The config row lock serializes this check with creation on every API
		// replica. Never replace an unresolved task, or rotate repeatedly on a
		// stream of 429s, including immediately after a manual rotation.
		var blocked bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM openai_totp_rotations WHERE account_id=$1
 AND (state NOT IN ('succeeded','failed') OR updated_at >= NOW() - INTERVAL '1 hour'))`, accountID).Scan(&blocked)
		if err != nil || blocked {
			return nil, err
		}
	}
	var taskID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO openai_oauth_reauth_tasks
 (account_id,status,stage,expected_credentials_hash) VALUES ($1,'callback_processing','applying_credentials',$2) RETURNING id`, accountID, hash).Scan(&taskID)
	if err != nil {
		var p *pq.Error
		if errors.As(err, &p) && p.Code == "23505" {
			return nil, infraerrors.Conflict("TOTP_ACCOUNT_BUSY", "A login or 2FA task is already active")
		}
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO openai_totp_rotations (task_id,account_id,config_updated_at,previous_ciphertext,password_ciphertext)
 SELECT $1,account_id,$3,totp_secret_ciphertext,password_ciphertext FROM openai_oauth_reauth_configs WHERE account_id=$2`, taskID, accountID, version)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetTOTPRotation(ctx, accountID)
}

func lockAutomaticTOTPRotationEligibility(ctx context.Context, tx *sql.Tx, accountID int64) (bool, error) {
	// Callbacks lock task before account, while budget reconciliation locks
	// guard before task. Reject active logins before locking account to avoid
	// introducing the reverse account -> guard -> task dependency. The shared
	// history lock prevents a new login from being queued after this check.
	var busy bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM openai_oauth_reauth_tasks task WHERE task.account_id=$1
 AND task.status IN ('queued','running','callback_processing')
 AND NOT EXISTS(SELECT 1 FROM openai_totp_rotations rotation WHERE rotation.task_id=task.id))`, accountID).Scan(&busy)
	if err != nil {
		return false, err
	}
	if busy {
		return false, infraerrors.Conflict("TOTP_ACCOUNT_BUSY", "A login or 2FA task is already active")
	}
	// Recheck automatic controls under account/guard locks before the existing
	// config/rotation locks. A busy row skips this automatic attempt rather than
	// waiting while holding another lock. Manual rotation keeps its semantics.
	var lockedID int64
	var lockErr *pq.Error
	err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE NOWAIT`, accountID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) || (errors.As(err, &lockErr) && lockErr.Code == "55P03") {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = tx.QueryRowContext(ctx, `SELECT account_id FROM account_token_guard_v2_accounts WHERE account_id=$1 FOR UPDATE NOWAIT`, accountID).Scan(&lockedID)
	if errors.As(err, &lockErr) && lockErr.Code == "55P03" {
		return false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM accounts a WHERE a.id=$1 AND a.deleted_at IS NULL
 AND a.status<>'disabled' AND a.schedulable
 AND NOT EXISTS(SELECT 1 FROM account_token_guard_v2_accounts guard WHERE guard.account_id=a.id AND (NOT guard.enabled OR NOT guard.auto_relogin_enabled))
 AND NOT EXISTS(SELECT 1 FROM pk_authorization_state st WHERE st.account_id=a.id AND st.status='banned' AND st.credential_version=md5(a.credentials::text))
 AND COALESCE((SELECT status FROM pk_authorization_jobs WHERE email=lower(trim(COALESCE(NULLIF(a.credentials->>'email',''),NULLIF(a.extra->>'email_address',''),NULLIF(a.extra->>'email',''),'')))
   AND (status IN ('banned','completed') OR settings->>'authorization_completed'='true') ORDER BY id DESC LIMIT 1),'')<>'banned')`, accountID).Scan(&eligible)
	return eligible, err
}

func (r *openAIOAuthReauthRepository) ClaimTOTPRotation(ctx context.Context, worker string) (*service.OpenAITOTPRotation, error) {
	if err := r.expireTOTPRotations(ctx, 0); err != nil {
		return nil, err
	}
	return scanRotation(r.db.QueryRowContext(ctx, `WITH next AS (SELECT task_id FROM openai_totp_rotations
 WHERE state='queued' ORDER BY task_id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE openai_totp_rotations r SET state='running',worker_id=$1,updated_at=NOW(),error_code=''
 FROM next WHERE r.task_id=next.task_id
 RETURNING r.task_id,r.account_id,r.state,r.action,r.worker_id,r.candidate_ciphertext,r.config_updated_at,r.error_code,r.updated_at`, worker))
}

func (r *openAIOAuthReauthRepository) TransitionTOTPRotation(ctx context.Context, taskID int64, worker, from, to, candidate string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "SET LOCAL synchronous_commit = on"); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE openai_totp_rotations SET state=$4,
 candidate_ciphertext=CASE WHEN $5='' THEN candidate_ciphertext ELSE $5 END,updated_at=NOW()
 WHERE task_id=$1 AND worker_id=$2 AND state=$3
 AND (($3='running' AND (($4='enrolling' AND action='rotate') OR ($4='verifying' AND action IN ('verify_new','verify_old'))))
      OR ($3<>'running' AND action='rotate'))`, taskID, worker, from, to, candidate)
	if err = requireOpenAIOAuthReauthTaskUpdate(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *openAIOAuthReauthRepository) FinishTOTPRotation(ctx context.Context, taskID int64, worker string, success bool, code string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "SET LOCAL synchronous_commit = on"); err != nil {
		return err
	}
	// Lock order matches creation and configuration updates (config, then rotation).
	var version time.Time
	err = tx.QueryRowContext(ctx, `SELECT c.updated_at FROM openai_oauth_reauth_configs c JOIN openai_totp_rotations r ON r.account_id=c.account_id WHERE r.task_id=$1 FOR UPDATE OF c`, taskID).Scan(&version)
	if err != nil {
		return err
	}
	rotation, err := scanRotation(tx.QueryRowContext(ctx, rotationSelect+`WHERE task_id=$1 FOR UPDATE`, taskID))
	if err != nil {
		return err
	}
	if rotation == nil || rotation.WorkerID != worker {
		return sql.ErrNoRows
	}
	if rotation.State == "succeeded" && success {
		return nil
	}
	if rotation.State == "failed" || rotation.State == "succeeded" || rotation.State == "uncertain" {
		return sql.ErrNoRows
	}
	state := "uncertain"
	if success {
		if rotation.State != "verifying" || !version.Equal(rotation.ConfigUpdatedAt) {
			return sql.ErrNoRows
		}
		if rotation.Action != "verify_old" && rotation.CandidateCiphertext == "" {
			return sql.ErrNoRows
		}
		state = "succeeded"
	} else if rotation.State == "running" && rotation.Action == "rotate" {
		state = "failed"
	}
	_, err = tx.ExecContext(ctx, `UPDATE openai_totp_rotations SET state=$2,error_code=$3,updated_at=NOW() WHERE task_id=$1`, taskID, state, code)
	if err != nil {
		return err
	}
	if success && rotation.Action != "verify_old" {
		_, err = tx.ExecContext(ctx, `UPDATE openai_oauth_reauth_configs SET totp_secret_ciphertext=$2,updated_at=NOW() WHERE account_id=$1`, rotation.AccountID, rotation.CandidateCiphertext)
		if err != nil {
			return err
		}
	}
	if state == "succeeded" || state == "failed" {
		_, err = tx.ExecContext(ctx, `UPDATE openai_oauth_reauth_tasks SET status=$2,stage=$2,error_message=NULLIF($3,''),finished_at=NOW(),updated_at=NOW() WHERE id=$1`, taskID, state, code)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *openAIOAuthReauthRepository) RetryTOTPRotation(ctx context.Context, accountID, taskID int64, action string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE openai_totp_rotations SET state='queued',action=$3,worker_id='',error_code='',updated_at=NOW()
 WHERE account_id=$1 AND task_id=$2 AND state='uncertain' AND ($3='verify_old' OR candidate_ciphertext<>'')`, accountID, taskID, action)
	return requireOpenAIOAuthReauthTaskUpdate(result, err)
}

// RecoverTOTPCandidate restores a locally journaled enrollment after a lost API acknowledgement.
// It cannot overwrite another candidate or revive a terminal task.
func (r *openAIOAuthReauthRepository) RecoverTOTPCandidate(ctx context.Context, taskID int64, candidate string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE openai_totp_rotations SET candidate_ciphertext=$2,error_code='candidate_recovered',updated_at=NOW()
 WHERE task_id=$1 AND state='uncertain' AND candidate_ciphertext=''`, taskID, candidate)
	return requireOpenAIOAuthReauthTaskUpdate(result, err)
}

// Status reads also expire interrupted tasks, so recovery exports work with all workers offline.
func (r *openAIOAuthReauthRepository) expireTOTPRotations(ctx context.Context, accountID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE openai_totp_rotations SET state='uncertain',worker_id='',error_code='worker_expired',updated_at=NOW()
 WHERE ($1::bigint=0 OR account_id=$1) AND state IN ('running','enrolling','prepared','activating','verifying')
 AND updated_at < NOW()-INTERVAL '15 minutes'`, accountID)
	return err
}

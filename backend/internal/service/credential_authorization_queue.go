package service

import (
	"context"
	"database/sql"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// LockCredentialAuthorizationQueue shares the legacy history lock before any
// queue/config/account row locks. It spans only task creation, never login I/O.
// Both authorization engines must acquire it before checking the other queue.
func LockCredentialAuthorizationQueue(ctx context.Context, tx *sql.Tx, accountID int64) error {
	if err := lockAuthorizationHistory(ctx, tx); err != nil {
		return err
	}
	var busy bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM pk_authorization_jobs j
		WHERE j.status IN ('queued','running') AND
		(j.account_id=$1 OR j.email IN (SELECT `+authorizationEmailSQL+` FROM accounts a WHERE a.id=$1)))`, accountID).Scan(&busy)
	if err != nil {
		return err
	}
	if busy {
		return infraerrors.Conflict("OPENAI_REAUTH_TASK_ALREADY_ACTIVE", "An account authorization task is already active; wait for it to finish")
	}
	return nil
}

// The caller already holds the shared history lock. Match by email as well as
// ID so a first-import job cannot race a managed login for the same identity.
func ensureNoManagedAuthorization(ctx context.Context, tx *sql.Tx, email string, accountID *int64) error {
	var busy bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM openai_oauth_reauth_tasks t JOIN accounts a ON a.id=t.account_id
		WHERE t.status IN ('queued','running','callback_processing')
		AND (a.id=$1 OR `+authorizationEmailSQL+`=$2))`, accountID, email).Scan(&busy)
	if err != nil {
		return err
	}
	if busy {
		return infraerrors.Conflict("AUTHORIZATION_RUNNING", "该账号已有凭证运营授权任务，请等待任务完成")
	}
	return nil
}

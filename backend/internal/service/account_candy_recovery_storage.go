package service

import (
	"context"
	"slices"
)

// Recheck configuration, account identity, groups and the original lease before
// every request. No transaction remains open across network work.
func (s *AccountCandyTestService) checkCandyRecoveryAttempt(ctx context.Context, result *AccountCandyTestResult, cfg *AccountCandyTestConfig, message string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := accountCandyConfigTx(ctx, tx)
	if err != nil {
		return err
	}
	if current.generation != cfg.generation {
		return context.Canceled
	}
	var owned bool
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(active_result_id=$2 AND lease_until>NOW(),false) FROM pk_account_candy_state WHERE account_id=$1 FOR SHARE`, result.AccountID, result.ID).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return context.Canceled
	}
	identity, err := accountCandyIdentity(ctx, tx, result.AccountID)
	if err != nil {
		return err
	}
	if identity != result.identity {
		return context.Canceled
	}
	var status string
	var schedulable, notExpired bool
	if err = tx.QueryRowContext(ctx, `SELECT status,schedulable,(expires_at IS NULL OR expires_at>NOW() OR auto_pause_on_expired=false) FROM accounts WHERE id=$1 AND deleted_at IS NULL`, result.AccountID).Scan(&status, &schedulable, &notExpired); err != nil {
		return err
	}
	if result.probeAccount == nil || status != result.probeAccount.Status || schedulable != result.probeAccount.Schedulable || (result.Source == "scheduled" && !notExpired) {
		return context.Canceled
	}
	groups, err := accountCandyGroups(ctx, tx, result.AccountID)
	if err != nil {
		return err
	}
	if !slices.Equal(groups, result.OriginalGroupIDs) {
		return context.Canceled
	}
	if message != "" {
		updated, err := tx.ExecContext(ctx, `UPDATE pk_account_candy_results SET error_message=$2 WHERE id=$1 AND status='running'`, result.ID, message)
		if err != nil {
			return err
		}
		n, err := updated.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return context.Canceled
		}
	}
	return tx.Commit()
}

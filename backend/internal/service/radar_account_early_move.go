package service

import (
	"context"
	"database/sql"
)

// The failed model, verdict, group replacement and outbox commit together.
// Claim/configuration locks and the original account/group snapshot still apply.
func (s *AccountCandyTestService) completeFailedRadarModel(ctx context.Context, run *RadarRun, cfg *RadarConfig, model *RadarModelResult) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, accountCandyLockID); err != nil {
		return err
	}
	current, err := radarConfigTx(ctx, tx)
	if err != nil {
		return err
	}
	if current.Generation != cfg.Generation {
		return context.Canceled
	}
	var owned bool
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(active_run_id=$2 AND lease_token=$3 AND lease_until>NOW(),false) FROM pk_radar_account_state WHERE account_id=$1 FOR UPDATE`, run.AccountID, run.ID, run.leaseToken).Scan(&owned)
	if err != nil {
		return err
	}
	if !owned {
		return context.Canceled
	}
	identity, err := accountCandyIdentity(ctx, tx, run.AccountID)
	if err != nil {
		return err
	}
	if identity != run.identity {
		return context.Canceled
	}
	var moved *int64
	var moveError, status string
	err = tx.QueryRowContext(ctx, `SELECT moved_to_group_id,move_error,status FROM pk_radar_account_runs WHERE id=$1 FOR UPDATE`, run.ID).Scan(&moved, &moveError, &status)
	if err != nil {
		return err
	}
	if status != "running" || model.Status != "completed" || model.Passed == nil || *model.Passed {
		return context.Canceled
	}
	if err = writeRadarModelWith(ctx, tx, run, model); err != nil {
		return err
	}
	if moved == nil && cfg.TargetGroupID != nil {
		moved, moveError, err = s.moveAccount(ctx, tx, &run.AccountCandyTestResult, *cfg.TargetGroupID)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_radar_account_runs SET verdict='fail',moved_to_group_id=$2,move_error=$3 WHERE id=$1 AND status='running'`, run.ID, moved, moveError); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_radar_account_state SET verdict='fail' WHERE account_id=$1 AND active_run_id=$2 AND lease_token=$3`, run.AccountID, run.ID, run.leaseToken); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

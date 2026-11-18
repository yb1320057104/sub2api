package service

import (
	"context"
	"database/sql"
	"errors"
)

// Cancellation revokes the old execution first. A cancelled binding cannot be
// returned to the idle pool until its remote task stopped and its grant was
// explicitly disconnected by a separately fenced queue operation.
func (s *KiloService) cleanupCancelledKiloBinding(ctx context.Context, jobID int64) error {
	var p kiloPoolBinding
	err := s.db.QueryRowContext(ctx, `SELECT p.id,p.account_id,p.source_account_id,p.binding_revision FROM pk_kilo_prepared_accounts p JOIN pk_kilo_jobs j ON j.prepared_id=p.id WHERE j.id=$1 AND j.status='cancelled' AND p.source_account_id=j.source_account_id AND p.binding_state='blocked' AND p.binding_error='cancelled_binding_requires_remote_cleanup' AND p.identity_status='active' AND p.binding_job_id IS NULL`, jobID).Scan(&p.ID, &p.GatewayID, &p.SourceID, &p.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.queueKiloUnbind(ctx, p, "cancelled_binding_cleanup", "")
}

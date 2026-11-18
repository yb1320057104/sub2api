package service

import (
	"context"
	"database/sql"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// ReleasePrepared disables local dispatch first. It never disconnects the
// user's remote BYOK subscription, and refuses to free an IP with a live lease.
func (s *KiloService) ReleasePrepared(ctx context.Context, id int64) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	txctx := dbent.NewTxContext(ctx, tx)
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var accountID *int64
	if err = kiloScan(ctx, tx.Client(), `SELECT account_id FROM pk_kilo_prepared_accounts WHERE id=$1 FOR UPDATE`, []any{id}, &accountID); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='cancelled',stage='cancelled',lease_token=NULL,lease_until=NULL,revision=revision+1,finished_at=NOW(),updated_at=NOW() WHERE prepared_id=$1 AND status IN ('queued','running','awaiting_authorization')`, id); err != nil {
		return err
	}
	if accountID != nil {
		a, readErr := s.accounts.GetByID(txctx, *accountID)
		if readErr != nil {
			return readErr
		}
		if a.Extra[KiloManagedExtraKey] != true {
			return kiloConflict("账号已变更，不能释放其他账号的出口")
		}
		a.Schedulable = false
		a.Status = StatusDisabled
		if err = s.accounts.Update(txctx, a); err != nil {
			return err
		}
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status='disabled',last_error='kilo_release:manual',updated_at=NOW() WHERE id=$1`, id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if accountID == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status='ready',source_account_id=NULL,binding_job_id=NULL,last_error='',updated_at=NOW() WHERE id=$1 AND status='disabled'`, id)
		return err
	}
	return s.releaseRequestExits(ctx, id, false)
}

// A crashed browser must not keep authorization codes or temporary IP ownership
// forever. Unconfirmed live request leases remain protected by the egress owner.
func (s *KiloService) expireDevices(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `UPDATE pk_kilo_device_sessions SET status='expired',code_encrypted='',token_encrypted='',updated_at=NOW() WHERE status='pending' AND expires_at<=NOW() RETURNING account_id`)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		s.releaseInactiveKiloEgress(ctx, id)
	}
	return nil
}

func (s *KiloService) preparedAccountID(ctx context.Context, id int64) (int64, error) {
	var accountID sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT account_id FROM pk_kilo_prepared_accounts WHERE id=$1`, id).Scan(&accountID); err != nil {
		return 0, err
	}
	if !accountID.Valid {
		return 0, kiloBad("KILO_ACCOUNT_NOT_CREATED", "预备账号尚未分配网关身份")
	}
	return accountID.Int64, nil
}

func (s *KiloService) checkJobOwned(ctx context.Context, job *kiloClaim) error {
	var valid bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_kilo_jobs WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>NOW())`, job.ID, job.Lease).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return kiloConflict("任务已取消或执行租约已失效")
	}
	return nil
}

func (s *KiloService) awaitQuotaReset(ctx context.Context, job *kiloClaim, reset *time.Time) error {
	next := time.Now().Add(5 * time.Minute)
	if reset != nil && reset.After(time.Now()) {
		next = *reset
	}
	result, err := s.db.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='awaiting_authorization',stage='quota_reset',last_error='官方订阅额度暂不可用，等待重置后自动检查',next_attempt_at=$3,lease_token=NULL,lease_until=NULL,updated_at=NOW() WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>NOW()`, job.ID, job.Lease, next)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return kiloConflict("任务已取消或执行租约已失效")
	}
	return nil
}

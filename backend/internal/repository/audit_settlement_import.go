package repository

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) RegisterAuditSettlementImport(ctx context.Context, in service.AuditSettlementImportInput) error {
	if r.db == nil {
		return errors.New("audit settlement database unavailable")
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO audit_settlement_imports
		(usage_id,usage_created_at,account_id,user_id,api_key_id,group_id,upstream_identity,receipt_id)
		SELECT id,created_at,account_id,user_id,api_key_id,group_id,$7,$8::uuid FROM usage_logs
		WHERE id=$1 AND created_at=$2 AND account_id=$3 AND user_id=$4 AND api_key_id=$5 AND group_id IS NOT DISTINCT FROM $6::bigint
		AND (request_type<>4 OR audit_settlement_imported_event)
		ON CONFLICT DO NOTHING`, in.UsageID, in.UsageCreatedAt, in.AccountID, in.UserID, in.APIKeyID, in.GroupID, in.UpstreamIdentity, in.ReceiptID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 1 {
		return err
	}
	// A replay of this exact identity is harmless. Reusing a receipt for a
	// different local request must not silently produce a false zero receipt.
	var matches bool
	err = r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_settlement_imports
		WHERE usage_id=$1 AND usage_created_at=$2 AND account_id=$3 AND user_id=$4 AND api_key_id=$5
		AND upstream_identity=$6 AND receipt_id=$7::uuid)`, in.UsageID, in.UsageCreatedAt, in.AccountID, in.UserID, in.APIKeyID, in.UpstreamIdentity, in.ReceiptID).Scan(&matches)
	if err != nil {
		return err
	}
	if !matches {
		return errors.New("audit settlement identity conflict")
	}
	return nil
}

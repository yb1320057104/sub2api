package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.KiloInferenceAccountReader = (*accountRepository)(nil)
var _ service.KiloGatewaySourceReader = (*accountRepository)(nil)

// Once Kilo owns inference, a stale form or ordinary extra replacement cannot
// turn the retained OAuth source into a generator, even after explicit release.
func preserveKiloQuotaOnlyExtra(incoming, current map[string]any) map[string]any {
	if !(&service.Account{Extra: current}).IsKiloQuotaOnly() {
		return incoming
	}
	if incoming == nil {
		incoming = make(map[string]any)
	}
	incoming["kilo_quota_only"] = true
	return incoming
}

func preserveKiloQuotaOnlySQL(expression string) string {
	return "CASE WHEN COALESCE(extra->>'kilo_quota_only','false')='true' OR COALESCE(extra->>'kilo_source_prepared_id','0')<>'0' OR COALESCE(extra->>'kilo_source_gateway_id','0')<>'0' THEN (" + expression + ") || '{\"kilo_quota_only\":true}'::jsonb ELSE (" + expression + ") END"
}

// GetKiloInferenceAccount is a fresh, narrow routing read for actual sends. It
// avoids proxy/group hydration and never consults a scheduler snapshot or TTL.
func (r *accountRepository) GetKiloInferenceAccount(ctx context.Context, accountID int64) (*service.Account, error) {
	if r == nil || r.sql == nil {
		return nil, errors.New("account routing reader is unavailable")
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT id,platform,type,status,schedulable,parent_account_id,credentials,extra FROM accounts WHERE id=$1 AND deleted_at IS NULL`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, sql.ErrNoRows
	}
	account := &service.Account{}
	var parent sql.NullInt64
	var credentials, extra []byte
	if err := rows.Scan(&account.ID, &account.Platform, &account.Type, &account.Status, &account.Schedulable, &parent, &credentials, &extra); err != nil {
		return nil, err
	}
	if parent.Valid {
		account.ParentAccountID = &parent.Int64
	}
	if len(credentials) > 0 {
		if err := json.Unmarshal(credentials, &account.Credentials); err != nil {
			return nil, err
		}
	}
	if len(extra) > 0 {
		if err := json.Unmarshal(extra, &account.Extra); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// Source quarantine must also stop sends selected from an older node's cache.
	if err := r.projectKiloSourceHealth(ctx, account); err != nil {
		return nil, err
	}
	return account, nil
}

// GetKiloSourceForGateway only accepts a persisted, still-reciprocal pair.
// Disabled gateways retain that pair for read-only quota display while draining.
func (r *accountRepository) GetKiloSourceForGateway(ctx context.Context, gatewayID, preparedID int64) (*service.Account, error) {
	if r == nil || r.sql == nil || gatewayID <= 0 || preparedID <= 0 {
		return nil, errors.New("Kilo source pairing reader is unavailable")
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT p.source_account_id FROM pk_kilo_prepared_accounts p JOIN accounts g ON g.id=p.account_id AND g.deleted_at IS NULL JOIN accounts s ON s.id=p.source_account_id AND s.deleted_at IS NULL WHERE p.id=$1 AND p.account_id=$2 AND p.status IN ('bound','disabled') AND g.platform='openai' AND g.type='apikey' AND g.extra->>'kilo_managed'='true' AND g.extra->>'kilo_prepared_id'=p.id::text AND s.platform='openai' AND s.type='oauth' AND s.parent_account_id IS NULL AND s.extra->>'kilo_source_gateway_id'=p.account_id::text AND s.extra->>'kilo_source_prepared_id'=p.id::text`, preparedID, gatewayID)
	if err != nil {
		return nil, err
	}
	var sourceID int64
	if !rows.Next() {
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		return nil, sql.ErrNoRows
	}
	err = rows.Scan(&sourceID)
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	return r.GetByID(ctx, sourceID)
}

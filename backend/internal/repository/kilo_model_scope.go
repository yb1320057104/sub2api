package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.KiloModelScopeRepository = (*accountRepository)(nil)

// UpdateKiloModelScopeIfUnchanged publishes the authenticated model set and
// scheduler invalidation atomically. The same prepared pair and credential
// generations must still own this discovery when the write takes place.
func (r *accountRepository) UpdateKiloModelScopeIfUnchanged(ctx context.Context, expected, source *service.Account, models []string, observed time.Time) (bool, error) {
	if r == nil || r.client == nil || r.sql == nil || expected == nil || source == nil || expected.ID == source.ID || len(models) == 0 {
		return false, errors.New("Kilo model scope persistence is unavailable")
	}
	if scoped := r.accountTransactionScope(ctx); scoped != r {
		return scoped.UpdateKiloModelScopeIfUnchanged(ctx, expected, source, models, observed)
	}
	mapping := make(map[string]string, len(models))
	for _, model := range models {
		mapping[model] = model
	}
	keyHash := sha256.Sum256([]byte(expected.GetCredential("api_key")))
	extra := map[string]any{
		"kilo_subscription_models":             models,
		"kilo_subscription_scope_source":       service.KiloAuthenticatedModelScope,
		"kilo_subscription_scope_verified_at":  observed.UTC().Format(time.RFC3339),
		"kilo_subscription_scope_key":          hex.EncodeToString(keyHash[:]),
		"kilo_subscription_scope_organization": expected.GetCredential(service.KiloGatewayOrganizationIDCredential),
	}
	// Scope belongs to the bound GPT identity/workspace, not just the Key.
	extra["kilo_subscription_scope_source_identity"] = service.KiloSubscriptionSourceIdentity(source)
	values := []any{mapping, extra, normalizeJSONMap(expected.Credentials), normalizeJSONMap(expected.Extra), normalizeJSONMap(source.Credentials), normalizeJSONMap(source.Extra)}
	encoded := make([]string, len(values))
	for index, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			return false, err
		}
		encoded[index] = string(raw)
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	txctx := dbent.NewTxContext(ctx, tx)
	preparedID := service.ParseExtraInt(expected.Extra["kilo_prepared_id"])
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(70450001)); err != nil {
		return false, err
	}
	var lockedPrepared int64
	if err = scanSingleRow(ctx, tx.Client(), `SELECT id FROM pk_kilo_prepared_accounts WHERE id=$1 FOR UPDATE`, []any{preparedID}, &lockedPrepared); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err = r.LockAccountMutation(txctx); err != nil {
		return false, err
	}
	// Source credentials can also change through independent refresh writes.
	// Lock both physical owners before a new statement compares their values;
	// a gateway-only UPDATE snapshot cannot fence a concurrent source edit.
	rows, err := tx.Client().QueryContext(ctx, `SELECT id FROM accounts WHERE id IN ($1,$2) AND deleted_at IS NULL ORDER BY id FOR UPDATE`, source.ID, expected.ID)
	if err != nil {
		return false, err
	}
	locked := 0
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		locked++
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil || locked != 2 {
		return false, err
	}
	result, err := tx.Client().ExecContext(ctx, `WITH updated AS (
 UPDATE accounts g SET
 credentials=jsonb_set(COALESCE(g.credentials,'{}'::jsonb),'{model_mapping}',$2::jsonb,true),
 extra=COALESCE(g.extra,'{}'::jsonb)||$3::jsonb,updated_at=NOW()
 WHERE g.id=$1 AND g.deleted_at IS NULL AND g.platform='openai' AND g.type='apikey'
 AND g.credentials=$4::jsonb AND g.extra=$5::jsonb
 AND EXISTS(SELECT 1 FROM pk_kilo_prepared_accounts p JOIN accounts s ON s.id=p.source_account_id
 WHERE p.account_id=g.id AND p.id=$6 AND p.status IN ('bound','disabled')
 AND g.extra->>'kilo_managed'='true' AND g.extra->>'kilo_prepared_id'=p.id::text
 AND s.id=$7 AND s.deleted_at IS NULL AND s.platform='openai' AND s.type='oauth' AND s.parent_account_id IS NULL
 AND s.extra->>'kilo_source_gateway_id'=g.id::text AND s.extra->>'kilo_source_prepared_id'=p.id::text
 AND s.credentials=$8::jsonb AND s.extra=$10::jsonb AND s.status='active'
 AND NOT `+kiloSourceRecoveryPendingSQL+`)
 RETURNING g.id)
 INSERT INTO scheduler_outbox(event_type,account_id,group_id,payload)
 SELECT $9,updated.id,NULL,NULL FROM updated`, expected.ID, encoded[0], encoded[1], encoded[2], encoded[3],
		preparedID, source.ID, encoded[4], service.SchedulerOutboxEventAccountChanged, encoded[5])
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, expected.ID)
	return true, nil
}

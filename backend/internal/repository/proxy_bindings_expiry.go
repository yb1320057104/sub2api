package repository

import (
	"context"
	"database/sql"
	"errors"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Apply only the configured expiry action. Removing one direct-fallback member
// leaves other configured proxies in use; direct egress is reached only when the
// explicit fallback removes the final member. Capacity exhaustion keeps the
// expired member, so the router can use other members or fail closed.
func applyMultiProxyExpiry(ctx context.Context, exec sqlExecutor, expired int64, target *int64) ([]int64, error) {
	rows, err := exec.QueryContext(ctx, `SELECT a.id,a.proxy_id,ARRAY(SELECT b.proxy_id FROM pk_account_proxy_bindings b WHERE b.account_id=a.id ORDER BY b.position),f.proxy_ids
FROM accounts a LEFT JOIN pk_account_proxy_fallback_sets f ON f.account_id=a.id
WHERE a.deleted_at IS NULL AND a.parent_account_id IS NULL AND EXISTS(SELECT 1 FROM pk_account_proxy_bindings b WHERE b.account_id=a.id AND b.proxy_id=$1) ORDER BY a.id FOR UPDATE OF a`, expired)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id             int64
		primary        sql.NullInt64
		current, saved pq.Int64Array
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.primary, &c.current, &c.saved); err != nil {
			break
		}
		candidates = append(candidates, c)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	var changed []int64
	for _, c := range candidates {
		if target != nil {
			var usable bool
			err = scanSingleRow(ctx, exec, `SELECT EXISTS(SELECT 1 FROM proxies p WHERE p.id=$1 AND p.deleted_at IS NULL AND p.status='active' AND (p.expires_at IS NULL OR p.expires_at>NOW()) AND (EXISTS(SELECT 1 FROM pk_effective_account_proxy_bindings WHERE account_id=$2 AND proxy_id=p.id) OR COALESCE((SELECT max_accounts FROM pk_proxy_limits WHERE proxy_id=p.id),0)=0 OR (SELECT COUNT(*) FROM pk_effective_account_proxy_bindings b JOIN accounts a ON a.id=b.account_id AND a.parent_account_id IS NULL WHERE b.proxy_id=p.id)<(SELECT max_accounts FROM pk_proxy_limits WHERE proxy_id=p.id)))`, []any{*target, c.id}, &usable)
			if err != nil {
				return nil, err
			}
			if !usable {
				continue
			}
		}
		var next []int64
		seen := map[int64]bool{}
		for _, id := range c.current {
			if id == expired {
				if target == nil {
					continue
				}
				id = *target
			}
			if !seen[id] {
				seen[id] = true
				next = append(next, id)
			}
		}
		if len(c.saved) == 0 {
			c.saved = c.current
		}
		var primary any
		if len(next) > 0 {
			primary = next[0]
		}
		if _, err = exec.ExecContext(ctx, `DELETE FROM pk_account_proxy_bindings WHERE account_id=$1`, c.id); err != nil {
			return nil, err
		}
		if _, err = exec.ExecContext(ctx, `UPDATE accounts SET proxy_id=$2,proxy_fallback_origin_id=COALESCE(proxy_fallback_origin_id,$3),extra=extra-'upstream_billing_probe'-'ollama_cloud_usage_session'-'ollama_cloud_usage_snapshot'-'opencode_go_usage_snapshot',updated_at=NOW() WHERE id=$1`, c.id, primary, c.saved[0]); err != nil {
			return nil, err
		}
		for pos, id := range next {
			if _, err = exec.ExecContext(ctx, `INSERT INTO pk_account_proxy_bindings(account_id,proxy_id,position) VALUES($1,$2,$3)`, c.id, id, pos); err != nil {
				return nil, err
			}
		}
		if _, err = exec.ExecContext(ctx, `INSERT INTO pk_account_proxy_fallback_sets(account_id,proxy_ids) VALUES($1,$2) ON CONFLICT(account_id) DO UPDATE SET proxy_ids=EXCLUDED.proxy_ids`, c.id, pq.Array(c.saved)); err != nil {
			return nil, err
		}
		shadowRows, updateErr := exec.QueryContext(ctx, `UPDATE accounts SET proxy_id=$2,proxy_fallback_origin_id=$3,updated_at=NOW() WHERE parent_account_id=$1 AND deleted_at IS NULL RETURNING id`, c.id, primary, c.saved[0])
		if updateErr != nil {
			return nil, updateErr
		}
		for shadowRows.Next() {
			var shadowID int64
			if err = shadowRows.Scan(&shadowID); err != nil {
				break
			}
			changed = append(changed, shadowID)
		}
		if err == nil {
			err = shadowRows.Err()
		}
		_ = shadowRows.Close()
		if err != nil {
			return nil, err
		}
		changed = append(changed, c.id)
	}
	if len(changed) > 0 {
		if err = enqueueProxyProbeAccountChanges(ctx, exec, changed); err != nil {
			return nil, err
		}
	}
	return changed, nil
}

func (r *accountRepository) tryRevertMultiProxyFallback(ctx context.Context, accountID int64) (bool, error) {
	// A cheap existence query keeps the established legacy-only transaction path.
	var exists bool
	if err := scanSingleRow(ctx, r.accountTransactionScope(ctx).sql, `SELECT EXISTS(SELECT 1 FROM pk_account_proxy_fallback_sets WHERE account_id=$1)`, []any{accountID}, &exists); err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	txctx := ctx
	client := r.client
	var ownedTx *dbent.Tx
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	} else {
		tx, err := r.client.Tx(ctx)
		if err != nil {
			return true, err
		}
		ownedTx, client = tx, tx.Client()
		txctx = dbent.NewTxContext(ctx, tx)
		defer func() { _ = tx.Rollback() }()
	}
	var err error
	if err = lockProxyBindingAccounts(txctx, client, []int64{accountID}); err != nil {
		return true, err
	}
	var saved pq.Int64Array
	if err = scanSingleRow(txctx, client, `SELECT proxy_ids FROM pk_account_proxy_fallback_sets WHERE account_id=$1`, []any{accountID}, &saved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, service.ErrAccountNotInFallback
		}
		return true, err
	}
	var items []service.AccountProxyBindingInput
	for _, id := range saved {
		item := service.AccountProxyBindingInput{ProxyID: id}
		if err = scanSingleRow(txctx, client, `SELECT COALESCE((SELECT max_concurrency FROM pk_proxy_limits WHERE proxy_id=$1),0),COALESCE((SELECT max_accounts FROM pk_proxy_limits WHERE proxy_id=$1),0)`, []any{id}, &item.MaxConcurrency, &item.MaxAccounts); err != nil {
			return true, err
		}
		items = append(items, item)
	}
	if err = r.ReplaceAccountProxyBindings(txctx, []int64{accountID}, items); err != nil {
		return true, err
	}
	if ownedTx != nil {
		if err = ownedTx.Commit(); err != nil {
			return true, err
		}
		r.syncSchedulerAccountSnapshot(ctx, accountID)
	}
	return true, nil
}

// Legacy and multi-account fallback share the same capacity lock. Process roots
// individually so a full backup keeps remaining accounts on their failed proxy
// instead of aborting the entire expiry sweep or exceeding the configured cap.
func applyLegacyProxyExpiry(ctx context.Context, exec sqlExecutor, expired int64, target *int64) ([]int64, error) {
	rows, err := exec.QueryContext(ctx, `SELECT id FROM accounts WHERE proxy_id=$1 AND deleted_at IS NULL AND parent_account_id IS NULL AND NOT EXISTS(SELECT 1 FROM pk_account_proxy_bindings b WHERE b.account_id=accounts.id) ORDER BY id FOR UPDATE`, expired)
	if err != nil {
		return nil, err
	}
	var roots []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		roots = append(roots, id)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	var changed []int64
	for _, id := range roots {
		if target != nil {
			var usable bool
			err = scanSingleRow(ctx, exec, `SELECT EXISTS(SELECT 1 FROM proxies p WHERE p.id=$1 AND p.deleted_at IS NULL AND p.status='active' AND (p.expires_at IS NULL OR p.expires_at>NOW()) AND (COALESCE((SELECT max_accounts FROM pk_proxy_limits WHERE proxy_id=p.id),0)=0 OR (SELECT COUNT(*) FROM pk_effective_account_proxy_bindings b JOIN accounts a ON a.id=b.account_id AND a.parent_account_id IS NULL WHERE b.proxy_id=p.id)<(SELECT max_accounts FROM pk_proxy_limits WHERE proxy_id=p.id)))`, []any{*target}, &usable)
			if err != nil {
				return nil, err
			}
			if !usable {
				continue
			}
		}
		var destination any
		if target != nil {
			destination = *target
		}
		changedRows, err := exec.QueryContext(ctx, `UPDATE accounts SET proxy_id=$2,proxy_fallback_origin_id=COALESCE(proxy_fallback_origin_id,$3),extra=CASE WHEN type='apikey' THEN extra-'upstream_billing_probe' ELSE extra END,updated_at=NOW() WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL RETURNING id`, id, destination, expired)
		if err != nil {
			return nil, err
		}
		for changedRows.Next() {
			var changedID int64
			if err = changedRows.Scan(&changedID); err != nil {
				break
			}
			changed = append(changed, changedID)
		}
		if err == nil {
			err = changedRows.Err()
		}
		_ = changedRows.Close()
		if err != nil {
			return nil, err
		}
	}
	return changed, nil
}

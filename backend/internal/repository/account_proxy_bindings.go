package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const accountProxyAssignmentLock int64 = 70440001

func (r *accountRepository) GetAccountProxyBindings(ctx context.Context, accountID int64) ([]service.AccountProxyBinding, error) {
	items, err := r.loadAccountProxyBindings(ctx, []int64{accountID})
	if err != nil {
		return nil, err
	}
	if len(items[accountID]) == 0 {
		var exists bool
		if err := scanSingleRow(ctx, r.accountTransactionScope(ctx).sql, `SELECT EXISTS(SELECT 1 FROM accounts a JOIN accounts owner ON owner.id=COALESCE(a.parent_account_id,a.id) AND owner.deleted_at IS NULL WHERE a.id=$1 AND a.deleted_at IS NULL)`, []any{accountID}, &exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, service.ErrAccountNotFound
		}
	}
	return items[accountID], nil
}

// Keep expired/inactive proxies in this result. Runtime selection must distinguish
// exhausted configured egress from an explicitly direct account.
func (r *accountRepository) loadAccountProxyBindings(ctx context.Context, ids []int64) (map[int64][]service.AccountProxyBinding, error) {
	result := make(map[int64][]service.AccountProxyBinding)
	if len(ids) == 0 {
		return result, nil
	}
	scoped := r.accountTransactionScope(ctx)
	rows, err := scoped.sql.QueryContext(ctx, `SELECT b.account_id,p.id,p.name,p.protocol,p.host,p.port,COALESCE(p.username,''),COALESCE(p.password,''),CASE WHEN p.deleted_at IS NULL THEN p.status ELSE 'deleted' END,p.expires_at,COALESCE(l.max_concurrency,0),COALESCE(l.max_accounts,0)
FROM pk_effective_account_proxy_bindings b JOIN proxies p ON p.id=b.proxy_id
LEFT JOIN pk_proxy_limits l ON l.proxy_id=p.id WHERE b.account_id=ANY($1) ORDER BY b.account_id,b.position,p.id`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var accountID int64
		var p service.Proxy
		var b service.AccountProxyBinding
		if err = rows.Scan(&accountID, &p.ID, &p.Name, &p.Protocol, &p.Host, &p.Port, &p.Username, &p.Password, &p.Status, &p.ExpiresAt, &b.MaxConcurrency, &b.MaxAccounts); err != nil {
			return nil, err
		}
		p.MaxConcurrency, p.MaxAccounts = b.MaxConcurrency, b.MaxAccounts
		b.ProxyID, b.Proxy = p.ID, &p
		result[accountID] = append(result[accountID], b)
	}
	return result, rows.Err()
}

func (r *accountRepository) hydrateAccountProxyBindings(ctx context.Context, accounts []*service.Account) error {
	ids := make([]int64, 0, len(accounts))
	for _, a := range accounts {
		if a != nil {
			ids = append(ids, a.ID)
		}
	}
	bindings, err := r.loadAccountProxyBindings(ctx, ids)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if a != nil {
			a.ProxyBindings = bindings[a.ID]
		}
	}
	return nil
}

func normalizeProxyAccountIDs(ids []int64) ([]int64, error) {
	if len(ids) > 5000 {
		return nil, infraerrors.BadRequest("PROXY_ASSIGNMENT_LIMIT", "一次最多处理5000个账号")
	}
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, infraerrors.BadRequest("PROXY_ACCOUNT_INVALID", "账号编号无效")
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func lockProxyBindingAccounts(ctx context.Context, client *dbent.Client, ids []int64) error {
	if _, err := client.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", accountProxyAssignmentLock); err != nil {
		return err
	}
	rows, err := client.QueryContext(ctx, `SELECT id,parent_account_id FROM accounts WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	found := 0
	for rows.Next() {
		var id int64
		var parent sql.NullInt64
		if err = rows.Scan(&id, &parent); err != nil {
			return err
		}
		if parent.Valid {
			return infraerrors.BadRequest("SPARK_SHADOW_PROXY_INHERITED", "影子账号继承母账号的代理，请编辑母账号")
		}
		found++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if found != len(ids) {
		return service.ErrAccountNotFound
	}
	return nil
}

func (r *accountRepository) ReplaceAccountProxyBindings(ctx context.Context, ids []int64, items []service.AccountProxyBindingInput) error {
	if err := service.ValidateAccountProxyBindings(items); err != nil {
		return err
	}
	ids, err := normalizeProxyAccountIDs(ids)
	if err != nil || len(ids) == 0 {
		return err
	}
	if dbent.TxFromContext(ctx) == nil {
		tx, err := r.client.Tx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err = r.ReplaceAccountProxyBindings(dbent.NewTxContext(ctx, tx), ids, items); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		r.syncSchedulerAccountSnapshots(ctx, ids)
		return nil
	}
	client := dbent.TxFromContext(ctx).Client()
	if err = lockProxyBindingAccounts(ctx, client, ids); err != nil {
		return err
	}
	proxyIDs := make([]int64, 0, len(items))
	for _, item := range items {
		proxyIDs = append(proxyIDs, item.ProxyID)
	}
	if len(proxyIDs) > 0 {
		rows, err := client.QueryContext(ctx, `SELECT id FROM proxies WHERE id=ANY($1) AND deleted_at IS NULL AND status='active' AND (expires_at IS NULL OR expires_at>NOW()) ORDER BY id FOR UPDATE`, pq.Array(proxyIDs))
		if err != nil {
			return err
		}
		found := 0
		for rows.Next() {
			found++
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		if found != len(proxyIDs) {
			return infraerrors.BadRequest("PROXY_UNAVAILABLE", "所选代理不存在、已停用或已到期")
		}
	}
	assignments := make(map[int64][]service.AccountProxyBindingInput, len(ids))
	for _, id := range ids {
		assignments[id] = items
	}
	return persistProxyAssignments(ctx, client, ids, assignments)
}

func persistProxyAssignments(ctx context.Context, client *dbent.Client, ids []int64, assignments map[int64][]service.AccountProxyBindingInput) error {
	// Validate the final plan before writes. This also gives a clear error when a
	// global limit is lowered below bindings held by accounts outside this edit.
	desired := map[int64]int{}
	limits := map[int64]service.AccountProxyBindingInput{}
	for _, id := range ids {
		for _, item := range assignments[id] {
			if old, ok := limits[item.ProxyID]; ok && (old.MaxAccounts != item.MaxAccounts || old.MaxConcurrency != item.MaxConcurrency) {
				return infraerrors.BadRequest("PROXY_LIMIT_CONFLICT", "同一个代理的全局限制不一致")
			}
			limits[item.ProxyID] = item
			desired[item.ProxyID]++
		}
	}
	for proxyID, item := range limits {
		if item.MaxAccounts == 0 {
			continue
		}
		var outside int
		if err := scanSingleRow(ctx, client, `SELECT COUNT(*) FROM pk_effective_account_proxy_bindings b JOIN accounts a ON a.id=b.account_id AND a.parent_account_id IS NULL WHERE b.proxy_id=$1 AND NOT(a.id=ANY($2))`, []any{proxyID, pq.Array(ids)}, &outside); err != nil {
			return err
		}
		if used := outside + desired[proxyID]; used > item.MaxAccounts {
			return infraerrors.BadRequest("PROXY_ACCOUNT_CAPACITY", fmt.Sprintf("代理 #%d 将绑定 %d 个账号，超过设置的 %d 个；本次未保存", proxyID, used, item.MaxAccounts))
		}
	}
	if _, err := client.ExecContext(ctx, `DELETE FROM pk_account_proxy_fallback_sets WHERE account_id=ANY($1)`, pq.Array(ids)); err != nil {
		return err
	}
	// Release selected roots inside this transaction before redistributing, so
	// swapping two full proxies cannot fail because of an intermediate assignment.
	if _, err := client.ExecContext(ctx, `DELETE FROM pk_account_proxy_bindings WHERE account_id=ANY($1)`, pq.Array(ids)); err != nil {
		return err
	}
	if _, err := client.ExecContext(ctx, `UPDATE accounts SET proxy_id=NULL,proxy_fallback_origin_id=NULL,extra=extra-'upstream_billing_probe'-'ollama_cloud_usage_session'-'ollama_cloud_usage_snapshot'-'opencode_go_usage_snapshot',updated_at=NOW() WHERE id=ANY($1)`, pq.Array(ids)); err != nil {
		return err
	}
	touched := map[int64]bool{}
	for _, id := range ids {
		items := assignments[id]
		for _, item := range items {
			if touched[item.ProxyID] {
				continue
			}
			if _, err := client.ExecContext(ctx, `INSERT INTO pk_proxy_limits(proxy_id,max_concurrency,max_accounts) VALUES($1,$2,$3) ON CONFLICT(proxy_id) DO UPDATE SET max_concurrency=EXCLUDED.max_concurrency,max_accounts=EXCLUDED.max_accounts,updated_at=NOW()`, item.ProxyID, item.MaxConcurrency, item.MaxAccounts); err != nil {
				return err
			}
			touched[item.ProxyID] = true
		}
		if len(items) > 0 {
			if _, err := client.ExecContext(ctx, `UPDATE accounts SET proxy_id=$2 WHERE id=$1`, id, items[0].ProxyID); err != nil {
				return err
			}
		}
		for pos, item := range items {
			if _, err := client.ExecContext(ctx, `INSERT INTO pk_account_proxy_bindings(account_id,proxy_id,position) VALUES($1,$2,$3)`, id, item.ProxyID, pos); err != nil {
				return err
			}
		}
	}
	// Global limits also constrain other accounts that already use these proxies.
	for proxyID := range touched {
		rows, err := client.QueryContext(ctx, `SELECT l.max_accounts,COUNT(a.id) FROM pk_proxy_limits l LEFT JOIN pk_effective_account_proxy_bindings b ON b.proxy_id=l.proxy_id LEFT JOIN accounts a ON a.id=b.account_id AND a.parent_account_id IS NULL WHERE l.proxy_id=$1 GROUP BY l.max_accounts`, proxyID)
		if err != nil {
			return err
		}
		var limit, used int
		ok := rows.Next()
		if ok {
			err = rows.Scan(&limit, &used)
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("proxy capacity row missing")
		}
		if limit > 0 && used > limit {
			return infraerrors.BadRequest("PROXY_ACCOUNT_CAPACITY", fmt.Sprintf("代理 #%d 已绑定 %d 个账号，超过设置的 %d 个；本次未保存", proxyID, used, limit))
		}
	}
	// Legacy background work and old binaries still use the first proxy. Shadows
	// inherit all bindings through the view and preserve their legacy primary too.
	if _, err := client.ExecContext(ctx, `UPDATE accounts child SET proxy_id=parent.proxy_id,proxy_fallback_origin_id=NULL,updated_at=NOW() FROM accounts parent WHERE child.parent_account_id=parent.id AND parent.id=ANY($1) AND child.deleted_at IS NULL`, pq.Array(ids)); err != nil {
		return err
	}
	rows, err := client.QueryContext(ctx, `SELECT id FROM accounts WHERE deleted_at IS NULL AND (id=ANY($1) OR parent_account_id=ANY($1) OR id IN(SELECT account_id FROM pk_effective_account_proxy_bindings WHERE proxy_id=ANY($2)))`, pq.Array(ids), pq.Array(proxyIDSet(touched)))
	if err != nil {
		return err
	}
	var changed []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		changed = append(changed, id)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	return enqueueProxyProbeAccountChanges(ctx, client, changed)
}

func proxyIDSet(ids map[int64]bool) []int64 {
	out := make([]int64, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (r *accountRepository) AssignAccountProxies(ctx context.Context, input service.AccountProxyAssignmentInput) (*service.AccountProxyAssignmentResult, error) {
	ids, err := normalizeProxyAccountIDs(input.AccountIDs)
	if err != nil {
		return nil, err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	txctx := dbent.NewTxContext(ctx, tx)
	if err = lockProxyBindingAccounts(txctx, client, ids); err != nil {
		return nil, err
	}
	rows, err := client.QueryContext(txctx, `SELECT id FROM proxies WHERE deleted_at IS NULL AND status='active' AND (expires_at IS NULL OR expires_at>NOW()) ORDER BY id FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	var proxyIDs []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		proxyIDs = append(proxyIDs, id)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if len(proxyIDs) < input.ProxiesPerAccount {
		return nil, infraerrors.BadRequest("PROXY_POOL_CAPACITY", "有效代理数量不足，本次未分配")
	}
	rows, err = client.QueryContext(txctx, `SELECT b.proxy_id,COUNT(*) FROM pk_effective_account_proxy_bindings b JOIN accounts a ON a.id=b.account_id AND a.parent_account_id IS NULL WHERE NOT(a.id=ANY($1)) GROUP BY b.proxy_id`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	counts := map[int64]int{}
	for rows.Next() {
		var id int64
		var count int
		if err = rows.Scan(&id, &count); err != nil {
			break
		}
		counts[id] = count
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	existing, err := r.loadAccountProxyBindings(txctx, ids)
	if err != nil {
		return nil, err
	}
	assignments, used, err := planAccountProxyAssignments(ids, proxyIDs, counts, existing, input)
	if err != nil {
		return nil, err
	}
	if err = persistProxyAssignments(txctx, client, ids, assignments); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	r.syncSchedulerAccountSnapshots(ctx, ids)
	return &service.AccountProxyAssignmentResult{AccountCount: len(ids), BindingCount: len(ids) * input.ProxiesPerAccount, ProxyCount: len(used)}, nil
}

// A valid complete existing plan is retained on repeat submission. Otherwise
// choose the largest remaining capacities first; preserving partial assignments
// greedily can falsely exhaust a pool that has a valid bipartite allocation.
func planAccountProxyAssignments(ids, proxyIDs []int64, outside map[int64]int, existing map[int64][]service.AccountProxyBinding, input service.AccountProxyAssignmentInput) (map[int64][]service.AccountProxyBindingInput, map[int64]bool, error) {
	available := map[int64]bool{}
	for _, id := range proxyIDs {
		available[id] = true
	}
	assignments := map[int64][]service.AccountProxyBindingInput{}
	used := map[int64]bool{}
	counts := map[int64]int{}
	for id, n := range outside {
		counts[id] = n
	}
	valid := true
	for _, id := range ids {
		if len(existing[id]) != input.ProxiesPerAccount {
			valid = false
			break
		}
		for _, b := range existing[id] {
			if !available[b.ProxyID] {
				valid = false
				break
			}
			counts[b.ProxyID]++
			if counts[b.ProxyID] > input.MaxAccountsPerProxy {
				valid = false
				break
			}
			used[b.ProxyID] = true
			assignments[id] = append(assignments[id], service.AccountProxyBindingInput{ProxyID: b.ProxyID, MaxAccounts: input.MaxAccountsPerProxy, MaxConcurrency: input.ProxyConcurrency})
		}
		if !valid {
			break
		}
	}
	if valid {
		return assignments, used, nil
	}
	assignments = map[int64][]service.AccountProxyBindingInput{}
	used = map[int64]bool{}
	counts = map[int64]int{}
	for id, n := range outside {
		counts[id] = n
	}
	for _, accountID := range ids {
		chosen := map[int64]bool{}
		for len(chosen) < input.ProxiesPerAccount {
			var best int64
			for _, id := range proxyIDs {
				if chosen[id] || counts[id] >= input.MaxAccountsPerProxy {
					continue
				}
				if best == 0 || counts[id] < counts[best] || (counts[id] == counts[best] && id < best) {
					best = id
				}
			}
			if best == 0 {
				return nil, nil, infraerrors.BadRequest("PROXY_POOL_CAPACITY", fmt.Sprintf("代理容量不足，账号 #%d 还缺 %d 个代理；本次未保存", accountID, input.ProxiesPerAccount-len(chosen)))
			}
			chosen[best] = true
			counts[best]++
			used[best] = true
			assignments[accountID] = append(assignments[accountID], service.AccountProxyBindingInput{ProxyID: best, MaxAccounts: input.MaxAccountsPerProxy, MaxConcurrency: input.ProxyConcurrency})
		}
	}
	return assignments, used, nil
}

// Compile-time guard: retain the optional capabilities without widening every
// AccountRepository mock used by unrelated gateway tests.
var _ service.AccountProxyBindingRepository = (*accountRepository)(nil)

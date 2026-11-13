package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func requireScopedProxyGroupGrant(ctx context.Context) error {
	p := adminaccess.FromContext(ctx)
	if p == nil || p.KeyID == "" || !slices.Contains(p.Policy.GlobalPermissions, "proxy-groups.assign") {
		return infraerrors.Forbidden("SCOPED_PROXY_GROUP_FORBIDDEN", "IP分组分配未授权")
	}
	return nil
}

func validateExpectedProxyGroup(input service.AccountProxyGroupAssignmentInput, items []service.AccountProxyBindingInput) error {
	if len(input.ExpectedProxyIDs) < 1 || len(input.ExpectedProxyIDs) > 100 {
		return infraerrors.BadRequest("SCOPED_PROXY_GROUP_EXPECTED_REQUIRED", "请提供原IP分组成员列表")
	}
	expected := slices.Clone(input.ExpectedProxyIDs)
	slices.Sort(expected)
	for i, id := range expected {
		if id <= 0 || (i > 0 && expected[i-1] == id) {
			return infraerrors.BadRequest("SCOPED_PROXY_GROUP_EXPECTED_INVALID", "原IP分组成员列表无效")
		}
	}
	current := make([]int64, len(items))
	for i, item := range items {
		current[i] = item.ProxyID
	}
	slices.Sort(current)
	if !slices.Equal(expected, current) {
		return infraerrors.Conflict("SCOPED_PROXY_GROUP_CHANGED", "IP分组成员已变化，请重新核对")
	}
	return nil
}

// The caller holds the proxy assignment advisory lock and selected proxy rows.
// Account rows and group edges are then locked in stable order before checking
// authorization; no write may use a handler's earlier scope snapshot.
func authorizeScopedProxyGroupAccounts(ctx context.Context, exec sqlExecutor, ids []int64, changesConcurrency bool) error {
	p := adminaccess.FromContext(ctx)
	if p == nil {
		return nil
	}
	if err := requireScopedProxyGroupGrant(ctx); err != nil {
		return err
	}
	rows, err := exec.QueryContext(ctx, `SELECT id,type,platform,parent_account_id FROM accounts WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	accounts := make(map[int64]adminaccess.Account, len(ids))
	shadow := false
	for rows.Next() {
		var a adminaccess.Account
		var parent sql.NullInt64
		if err = rows.Scan(&a.ID, &a.Type, &a.Platform, &parent); err != nil {
			break
		}
		shadow = shadow || parent.Valid
		accounts[a.ID] = a
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	if len(accounts) != len(ids) {
		return service.ErrAccountNotFound
	}
	rows, err = exec.QueryContext(ctx, `SELECT account_id,group_id FROM account_groups WHERE account_id=ANY($1) ORDER BY account_id,group_id FOR UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, group int64
		if err = rows.Scan(&id, &group); err != nil {
			break
		}
		a := accounts[id]
		a.GroupIDs = append(a.GroupIDs, group)
		accounts[id] = a
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !p.Policy.AllowsProxyGroupAssignment(accounts[id], changesConcurrency) {
			return service.ErrAccountNotFound
		}
	}
	// A parent row lock prevents a concurrent child FK insertion. Existing
	// children inherit egress, so preserve the scoped writer's full-admin boundary.
	rows, err = exec.QueryContext(ctx, `SELECT deleted_at IS NULL FROM accounts WHERE parent_account_id=ANY($1) ORDER BY id FOR UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	var hasShadows bool
	for rows.Next() {
		var active bool
		if err = rows.Scan(&active); err != nil {
			break
		}
		hasShadows = hasShadows || active
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	if shadow || hasShadows {
		return infraerrors.Forbidden("SCOPED_PROXY_GROUP_SHADOW", "关联影子账号请由完整管理员配置")
	}
	return nil
}

func (r *accountRepository) ValidateScopedProxyGroupAssignment(ctx context.Context, input service.AccountProxyGroupAssignmentInput) error {
	if err := requireScopedProxyGroupGrant(ctx); err != nil {
		return err
	}
	if err := service.ValidateProxyGroupAccountConcurrency(input.AccountConcurrency, true); err != nil {
		return err
	}
	ids, err := normalizeProxyAccountIDs(input.AccountIDs)
	if err != nil {
		return err
	}
	if len(ids) == 0 || input.ProxyGroupID <= 0 || input.ProxyConcurrency < 1 || input.ProxyConcurrency > 10000 {
		return infraerrors.BadRequest("PROXY_GROUP_ASSIGNMENT_INVALID", "IP分组分配参数无效")
	}
	return proxyGroupTransaction(ctx, r.client, func(ctx context.Context, c *dbent.Client) error {
		if err := proxyGroupsLock(ctx, c); err != nil {
			return err
		}
		items, err := proxyGroupAssignmentItems(ctx, c, input.ProxyGroupID, input.ProxyConcurrency)
		if err != nil {
			return err
		}
		if err := validateExpectedProxyGroup(input, items); err != nil {
			return err
		}
		return authorizeScopedProxyGroupAccounts(ctx, c, ids, input.AccountConcurrency != nil)
	})
}

// Scoped assignment receipts are durable operation evidence, NOT cache entries.
// Their reserved scope and infinity expiry exclude generic TTL/reclaim cleanup.
// Binding writes and receipt commit together, so a lost response or later admin
// edit never turns an old operation into a fresh global-capacity mutation.
func (r *accountRepository) AssignScopedAccountProxyGroup(ctx context.Context, input service.AccountProxyGroupAssignmentInput, key string) (*service.ScopedProxyGroupAssignmentResult, error) {
	if err := requireScopedProxyGroupGrant(ctx); err != nil {
		return nil, err
	}
	key, err := service.NormalizeIdempotencyKey(key)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, service.ErrIdempotencyKeyRequired
	}
	if err := service.ValidateProxyGroupAccountConcurrency(input.AccountConcurrency, true); err != nil {
		return nil, err
	}
	ids, err := normalizeProxyAccountIDs(input.AccountIDs)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 || input.ProxyGroupID <= 0 || input.ProxyConcurrency < 1 || input.ProxyConcurrency > 10000 {
		return nil, infraerrors.BadRequest("PROXY_GROUP_ASSIGNMENT_INVALID", "IP分组分配参数无效")
	}
	input.AccountIDs = ids
	input.ExpectedProxyIDs = slices.Clone(input.ExpectedProxyIDs)
	slices.Sort(input.ExpectedProxyIDs)
	p := adminaccess.FromContext(ctx)
	scope := "pk.scoped-proxy-assignment.v1:" + service.HashIdempotencyKey(p.KeyID)
	keyHash := service.HashIdempotencyKey(key)
	fingerprint, err := service.BuildIdempotencyFingerprint("POST", "/api/v1/admin/accounts/assign-proxy-group", p.KeyID, input)
	if err != nil {
		return nil, err
	}
	result := &service.ScopedProxyGroupAssignmentResult{}
	err = proxyGroupTransaction(ctx, r.client, func(ctx context.Context, c *dbent.Client) error {
		if err := proxyGroupsLock(ctx, c); err != nil {
			return err
		}
		items, err := proxyGroupAssignmentItems(ctx, c, input.ProxyGroupID, input.ProxyConcurrency)
		if err != nil {
			return err
		}
		if err = validateExpectedProxyGroup(input, items); err != nil {
			return err
		}
		if err = authorizeScopedProxyGroupAccounts(ctx, c, ids, input.AccountConcurrency != nil); err != nil {
			return err
		}
		rows, err := c.QueryContext(ctx, `SELECT request_fingerprint,response_body FROM idempotency_records WHERE scope=$1 AND idempotency_key_hash=$2 FOR UPDATE`, scope, keyHash)
		if err != nil {
			return err
		}
		var storedFingerprint, body string
		found := rows.Next()
		if found {
			err = rows.Scan(&storedFingerprint, &body)
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return err
		}
		if found {
			if storedFingerprint != fingerprint {
				return service.ErrIdempotencyKeyConflict
			}
			if err := json.Unmarshal([]byte(body), &result.AccountProxyAssignmentResult); err != nil {
				return fmt.Errorf("invalid scoped assignment receipt")
			}
			result.Replayed = true
			return nil
		}
		if err = assignWholeProxyGroup(ctx, c, ids, items, input.AccountConcurrency); err != nil {
			return err
		}
		result.AccountCount, result.ProxyCount, result.BindingCount = len(ids), len(items), len(ids)*len(items)
		encoded, err := json.Marshal(result.AccountProxyAssignmentResult)
		if err != nil {
			return err
		}
		_, err = c.ExecContext(ctx, `INSERT INTO idempotency_records(scope,idempotency_key_hash,request_fingerprint,status,response_status,response_body,expires_at) VALUES($1,$2,$3,'succeeded',200,$4,'infinity'::timestamptz)`, scope, keyHash, fingerprint, string(encoded))
		return err
	})
	if err != nil {
		return nil, err
	}
	if !result.Replayed && dbent.TxFromContext(ctx) == nil {
		r.syncSchedulerAccountSnapshots(ctx, ids)
	}
	return result, nil
}

var _ service.ScopedProxyGroupAssignmentValidator = (*accountRepository)(nil)
var _ service.ScopedProxyGroupAssignmentService = (*accountRepository)(nil)

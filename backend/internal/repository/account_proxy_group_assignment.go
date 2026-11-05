package repository

import (
	"context"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *accountRepository) GetGroupProxyPriorityPolicy(ctx context.Context, id int64) (*service.GroupProxyPriorityPolicy, error) {
	return readGroupProxyPriorityPolicy(ctx, r.accountTransactionScope(ctx).sql, id)
}

// The common allocator preserves global account capacity, including bindings
// outside this batch. An absent limit row retains its established unlimited value.
func proxyGroupAssignmentItems(ctx context.Context, exec sqlExecutor, groupID int64, concurrency int) ([]service.AccountProxyBindingInput, error) {
	var exists bool
	if err := scanSingleRow(ctx, exec, `SELECT EXISTS(SELECT 1 FROM pk_proxy_groups WHERE id=$1)`, []any{groupID}, &exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, proxyGroupNotFound()
	}
	rows, err := exec.QueryContext(ctx, `SELECT p.id,p.status='active' AND (p.expires_at IS NULL OR p.expires_at>NOW()),COALESCE(l.max_accounts,0),COALESCE(l.max_concurrency,0)
FROM pk_proxy_group_members m JOIN proxies p ON p.id=m.proxy_id AND p.deleted_at IS NULL
LEFT JOIN pk_proxy_limits l ON l.proxy_id=p.id WHERE m.proxy_group_id=$1 ORDER BY p.id FOR UPDATE OF p`, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []service.AccountProxyBindingInput{}
	for rows.Next() {
		var item service.AccountProxyBindingInput
		var active bool
		if err = rows.Scan(&item.ProxyID, &active, &item.MaxAccounts, &item.MaxConcurrency); err != nil {
			return nil, err
		}
		if !active {
			return nil, infraerrors.BadRequest("PROXY_GROUP_MEMBER_UNAVAILABLE", fmt.Sprintf("IP分组中的代理 #%d 已停用或到期，本次未保存", item.ProxyID))
		}
		if concurrency > 0 {
			item.MaxConcurrency = concurrency
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, infraerrors.BadRequest("PROXY_GROUP_EMPTY", "IP分组内没有代理，本次未保存")
	}
	if len(items) > 100 {
		return nil, infraerrors.BadRequest("PROXY_GROUP_TOO_LARGE", "IP分组超过100个代理，无法完整分配给账号")
	}
	return items, nil
}

func assignWholeProxyGroup(ctx context.Context, c *dbent.Client, ids []int64, items []service.AccountProxyBindingInput, concurrency *int) error {
	if err := lockProxyBindingAccounts(ctx, c, ids); err != nil {
		return err
	}
	assignments := make(map[int64][]service.AccountProxyBindingInput, len(ids))
	for _, id := range ids {
		assignments[id] = items
	}
	if err := persistProxyAssignments(ctx, c, ids, assignments); err != nil {
		return err
	}
	if concurrency != nil {
		// Binding persistence emits the account outbox events in this same
		// transaction. Subscribers can only observe the complete configuration.
		_, err := c.ExecContext(ctx, `UPDATE accounts SET concurrency=$2,updated_at=NOW() WHERE id=ANY($1) AND deleted_at IS NULL`, pq.Array(ids), *concurrency)
		return err
	}
	return nil
}

func (r *accountRepository) AssignAccountProxyGroup(ctx context.Context, input service.AccountProxyGroupAssignmentInput) (*service.AccountProxyAssignmentResult, error) {
	if err := service.ValidateProxyGroupAccountConcurrency(input.AccountConcurrency, true); err != nil {
		return nil, err
	}
	ids, err := normalizeProxyAccountIDs(input.AccountIDs)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, infraerrors.BadRequest("PROXY_GROUP_SELECTION_INVALID", "请选择账号")
	}
	if input.ProxyGroupID <= 0 || input.ProxyConcurrency < 1 || input.ProxyConcurrency > 10000 {
		return nil, infraerrors.BadRequest("PROXY_GROUP_ASSIGNMENT_INVALID", "请选择IP分组，每IP并发须为1至10000")
	}
	result := &service.AccountProxyAssignmentResult{}
	err = proxyGroupTransaction(ctx, r.client, func(ctx context.Context, c *dbent.Client) error {
		if err := proxyGroupsLock(ctx, c); err != nil {
			return err
		}
		items, err := proxyGroupAssignmentItems(ctx, c, input.ProxyGroupID, input.ProxyConcurrency)
		if err != nil {
			return err
		}
		if err = assignWholeProxyGroup(ctx, c, ids, items, input.AccountConcurrency); err != nil {
			return err
		}
		result.AccountCount, result.ProxyCount, result.BindingCount = len(ids), len(items), len(ids)*len(items)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if dbent.TxFromContext(ctx) == nil {
		r.syncSchedulerAccountSnapshots(ctx, ids)
	}
	return result, nil
}

func lockProxyPolicyGroups(ctx context.Context, exec sqlExecutor, ids []int64) error {
	rows, err := exec.QueryContext(ctx, `SELECT id FROM groups WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	found := 0
	for rows.Next() {
		found++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if found != len(ids) {
		return service.ErrGroupNotFound
	}
	return nil
}

func proxyPolicyAccountRoots(ctx context.Context, exec sqlExecutor, groupIDs []int64) ([]int64, error) {
	rows, err := exec.QueryContext(ctx, `SELECT a.id,a.parent_account_id,EXISTS(SELECT 1 FROM account_groups parent_group JOIN accounts parent ON parent.id=parent_group.account_id AND parent.deleted_at IS NULL WHERE parent_group.account_id=a.parent_account_id AND parent_group.group_id=ANY($1))
FROM account_groups ag JOIN accounts a ON a.id=ag.account_id AND a.deleted_at IS NULL
WHERE ag.group_id=ANY($1) ORDER BY a.id,ag.group_id FOR SHARE OF ag,a`, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []int64{}
	for rows.Next() {
		var id int64
		var parent *int64
		var parentSelected bool
		if err = rows.Scan(&id, &parent, &parentSelected); err != nil {
			return nil, err
		}
		if parent != nil {
			if !parentSelected {
				return nil, infraerrors.BadRequest("PROXY_GROUP_SHADOW_PARENT_OUTSIDE", fmt.Sprintf("影子账号 #%d 的母账号不在所选业务分组内，请同时选择母账号所在分组；本次未保存", id))
			}
			continue
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	ids = uniqueProxyGroupIDs(ids)
	if len(ids) > 5000 {
		return nil, infraerrors.BadRequest("PROXY_ASSIGNMENT_LIMIT", "所选分组超过5000个母账号，请缩小范围")
	}
	return ids, nil
}

func (r *accountRepository) UpdateGroupProxyPriorityPolicies(ctx context.Context, input service.GroupProxyPriorityUpdateInput) (*service.GroupProxyPriorityUpdateResult, error) {
	if err := service.ValidateProxyGroupAccountConcurrency(input.AccountConcurrency, input.AssignProxies); err != nil {
		return nil, err
	}
	ids := uniqueProxyGroupIDs(input.GroupIDs)
	if len(ids) == 0 || len(ids) > 500 {
		return nil, infraerrors.BadRequest("PROXY_GROUP_SELECTION_INVALID", "请选择1至500个业务分组")
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, service.ErrGroupNotFound
		}
	}
	if input.AssignProxies && (input.ProxyGroupID == nil || input.ProxyConcurrency < 1 || input.ProxyConcurrency > 10000) {
		return nil, infraerrors.BadRequest("PROXY_GROUP_ASSIGNMENT_INVALID", "请选择IP分组，每IP并发须为1至10000")
	}
	result := &service.GroupProxyPriorityUpdateResult{GroupCount: len(ids)}
	var changedAccounts []int64
	err := proxyGroupTransaction(ctx, r.client, func(ctx context.Context, c *dbent.Client) error {
		if err := proxyGroupsLock(ctx, c); err != nil {
			return err
		}
		if err := lockProxyPolicyGroups(ctx, c, ids); err != nil {
			return err
		}
		policies := make([]*service.GroupProxyPriorityPolicy, 0, len(ids))
		validated := map[int64][]service.AccountProxyBindingInput{}
		for _, id := range ids {
			p, err := readGroupProxyPriorityPolicy(ctx, c, id)
			if err != nil {
				return err
			}
			if input.ProxyGroupIDSet || input.ProxyGroupID != nil {
				p.ProxyGroupID = input.ProxyGroupID
			}
			p.StrictPriority = input.StrictPriority
			if p.StrictPriority && p.ProxyGroupID == nil {
				return infraerrors.BadRequest("PROXY_GROUP_REQUIRED", "开启严格优先级前请选择IP分组")
			}
			if p.ProxyGroupID != nil {
				if *p.ProxyGroupID <= 0 {
					return proxyGroupNotFound()
				}
				if p.StrictPriority || input.AssignProxies {
					if _, ok := validated[*p.ProxyGroupID]; !ok {
						items, err := proxyGroupAssignmentItems(ctx, c, *p.ProxyGroupID, 0)
						if err != nil {
							return err
						}
						validated[*p.ProxyGroupID] = items
					}
				} else {
					var exists bool
					if err := scanSingleRow(ctx, c, `SELECT EXISTS(SELECT 1 FROM pk_proxy_groups WHERE id=$1)`, []any{*p.ProxyGroupID}, &exists); err != nil {
						return err
					}
					if !exists {
						return proxyGroupNotFound()
					}
				}
			}
			policies = append(policies, p)
		}
		if input.AssignProxies {
			roots, err := proxyPolicyAccountRoots(ctx, c, ids)
			if err != nil {
				return err
			}
			items := validated[*input.ProxyGroupID]
			for i := range items {
				items[i].MaxConcurrency = input.ProxyConcurrency
			}
			if len(roots) > 0 {
				if err = assignWholeProxyGroup(ctx, c, roots, items, input.AccountConcurrency); err != nil {
					return err
				}
			} else {
				// A group without accounts may be prepared, but must not silently
				// mutate concurrency for unrelated accounts sharing these proxies.
				return infraerrors.BadRequest("PROXY_GROUP_NO_ACCOUNTS", "所选业务分组没有可配置的母账号，本次未保存")
			}
			changedAccounts = roots
			result.AccountCount, result.ProxyCount, result.BindingCount = len(roots), len(items), len(roots)*len(items)
		}
		for _, p := range policies {
			if _, err := c.ExecContext(ctx, `INSERT INTO pk_group_proxy_priority(group_id,strict_priority,proxy_group_id) VALUES($1,$2,$3) ON CONFLICT(group_id) DO UPDATE SET strict_priority=EXCLUDED.strict_priority,proxy_group_id=EXCLUDED.proxy_group_id,updated_at=NOW()`, p.GroupID, p.StrictPriority, p.ProxyGroupID); err != nil {
				return err
			}
			if err := enqueueSchedulerOutbox(ctx, c, service.SchedulerOutboxEventGroupChanged, nil, &p.GroupID, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(changedAccounts) > 0 && dbent.TxFromContext(ctx) == nil {
		r.syncSchedulerAccountSnapshots(ctx, changedAccounts)
	}
	return result, nil
}

var _ service.AccountProxyGroupRepository = (*accountRepository)(nil)
var _ service.GroupProxyPriorityPolicyReader = (*accountRepository)(nil)

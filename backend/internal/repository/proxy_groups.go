package repository

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func proxyGroupTransaction(ctx context.Context, client *dbent.Client, fn func(context.Context, *dbent.Client) error) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, tx.Client())
	}
	tx, err := client.Tx(ctx)
	if errors.Is(err, dbent.ErrTxStarted) {
		return fn(ctx, client)
	}
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = fn(dbent.NewTxContext(ctx, tx), tx.Client()); err != nil {
		return err
	}
	return tx.Commit()
}

func proxyGroupsLock(ctx context.Context, exec sqlExecutor) error {
	_, err := exec.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", accountProxyAssignmentLock)
	return err
}

func uniqueProxyGroupIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func listProxyGroups(ctx context.Context, exec sqlExecutor, id int64) ([]service.ProxyGroup, error) {
	rows, err := exec.QueryContext(ctx, `SELECT g.id,g.name,g.description,g.created_at,g.updated_at,
COALESCE(array_agg(p.id ORDER BY p.id) FILTER(WHERE p.id IS NOT NULL),'{}'::bigint[]),
COUNT(p.id),COUNT(p.id) FILTER(WHERE p.status='active' AND (p.expires_at IS NULL OR p.expires_at>NOW()))
FROM pk_proxy_groups g LEFT JOIN pk_proxy_group_members m ON m.proxy_group_id=g.id
LEFT JOIN proxies p ON p.id=m.proxy_id AND p.deleted_at IS NULL WHERE ($1::bigint=0 OR g.id=$1)
GROUP BY g.id ORDER BY g.id`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.ProxyGroup{}
	for rows.Next() {
		var g service.ProxyGroup
		var ids pq.Int64Array
		if err = rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt, &ids, &g.ProxyCount, &g.ActiveProxyCount); err != nil {
			return nil, err
		}
		g.ProxyIDs = []int64(ids)
		out = append(out, g)
	}
	return out, rows.Err()
}
func (r *proxyRepository) ListProxyGroups(ctx context.Context) ([]service.ProxyGroup, error) {
	return listProxyGroups(ctx, r.sql, 0)
}
func (r *proxyRepository) SaveProxyGroup(ctx context.Context, id int64, input service.ProxyGroupInput) (*service.ProxyGroup, error) {
	if err := service.ValidateProxyGroupInput(&input); err != nil {
		return nil, err
	}
	var result *service.ProxyGroup
	err := proxyGroupTransaction(ctx, r.client, func(ctx context.Context, c *dbent.Client) error {
		if err := proxyGroupsLock(ctx, c); err != nil {
			return err
		}
		if id == 0 {
			if err := scanSingleRow(ctx, c, `INSERT INTO pk_proxy_groups(name,description) VALUES($1,$2) RETURNING id`, []any{input.Name, input.Description}, &id); err != nil {
				return proxyGroupWriteError(err)
			}
		} else {
			res, err := c.ExecContext(ctx, `UPDATE pk_proxy_groups SET name=$2,description=$3,updated_at=NOW() WHERE id=$1`, id, input.Name, input.Description)
			if err != nil {
				return proxyGroupWriteError(err)
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return proxyGroupNotFound()
			}
		}
		groups, err := listProxyGroups(ctx, c, id)
		if err != nil {
			return err
		}
		result = &groups[0]
		return nil
	})
	return result, err
}
func proxyGroupWriteError(err error) error {
	var pg *pq.Error
	if errors.As(err, &pg) && pg.Code == "23505" {
		return infraerrors.Conflict("PROXY_GROUP_EXISTS", "IP分组名称已存在")
	}
	return err
}
func proxyGroupNotFound() error {
	return infraerrors.NotFound("PROXY_GROUP_NOT_FOUND", "IP分组不存在")
}
func rejectActiveProxyGroupMutation(ctx context.Context, exec sqlExecutor, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	var used bool
	if err := scanSingleRow(ctx, exec, `SELECT EXISTS(SELECT 1 FROM pk_group_proxy_priority p JOIN groups g ON g.id=p.group_id AND g.deleted_at IS NULL WHERE p.strict_priority AND p.proxy_group_id=ANY($1))`, []any{pq.Array(ids)}, &used); err != nil {
		return err
	}
	if used {
		return infraerrors.Conflict("PROXY_GROUP_STRICT_IN_USE", "IP分组正被严格优先级策略使用，请先关闭相关业务分组的开关再调整成员或删除")
	}
	return nil
}
func (r *proxyRepository) DeleteProxyGroup(ctx context.Context, id int64) error {
	return proxyGroupTransaction(ctx, r.client, func(ctx context.Context, c *dbent.Client) error {
		if err := proxyGroupsLock(ctx, c); err != nil {
			return err
		}
		if err := rejectActiveProxyGroupMutation(ctx, c, []int64{id}); err != nil {
			return err
		}
		var proxyIDs pq.Int64Array
		if err := scanSingleRow(ctx, c, `SELECT COALESCE(array_agg(proxy_id),'{}'::bigint[]) FROM pk_proxy_group_members WHERE proxy_group_id=$1`, []any{id}, &proxyIDs); err != nil {
			return err
		}
		// Soft-deleted business groups no longer have a routing policy.
		if _, err := c.ExecContext(ctx, `DELETE FROM pk_group_proxy_priority p USING groups g WHERE p.group_id=g.id AND g.deleted_at IS NOT NULL AND p.proxy_group_id=$1`, id); err != nil {
			return err
		}
		res, err := c.ExecContext(ctx, `DELETE FROM pk_proxy_groups WHERE id=$1`, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return proxyGroupNotFound()
		}
		return enqueueChangedProxyGroupAccounts(ctx, c, []int64(proxyIDs))
	})
}
func (r *proxyRepository) SetProxyGroupMembers(ctx context.Context, input service.ProxyGroupMembersInput) (int, error) {
	ids := uniqueProxyGroupIDs(input.ProxyIDs)
	err := proxyGroupTransaction(ctx, r.client, func(ctx context.Context, c *dbent.Client) error {
		if err := proxyGroupsLock(ctx, c); err != nil {
			return err
		}
		if input.ProxyGroupID != nil {
			var exists bool
			if err := scanSingleRow(ctx, c, `SELECT EXISTS(SELECT 1 FROM pk_proxy_groups WHERE id=$1)`, []any{*input.ProxyGroupID}, &exists); err != nil {
				return err
			}
			if !exists {
				return proxyGroupNotFound()
			}
		}
		rows, err := c.QueryContext(ctx, `SELECT p.id,m.proxy_group_id FROM proxies p LEFT JOIN pk_proxy_group_members m ON m.proxy_id=p.id WHERE p.id=ANY($1) AND p.deleted_at IS NULL ORDER BY p.id FOR UPDATE OF p`, pq.Array(ids))
		if err != nil {
			return err
		}
		found := 0
		changed := []int64{}
		affected := []int64{}
		for rows.Next() {
			var id int64
			var group sql.NullInt64
			if err = rows.Scan(&id, &group); err != nil {
				break
			}
			found++
			same := (!group.Valid && input.ProxyGroupID == nil) || (group.Valid && input.ProxyGroupID != nil && group.Int64 == *input.ProxyGroupID)
			if !same {
				changed = append(changed, id)
				if group.Valid {
					affected = append(affected, group.Int64)
				}
			}
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return err
		}
		if found != len(ids) {
			return service.ErrProxyNotFound
		}
		if len(changed) == 0 {
			return nil
		}
		if input.ProxyGroupID != nil {
			affected = append(affected, *input.ProxyGroupID)
		}
		if err = rejectActiveProxyGroupMutation(ctx, c, affected); err != nil {
			return err
		}
		if input.ProxyGroupID == nil {
			_, err = c.ExecContext(ctx, `DELETE FROM pk_proxy_group_members WHERE proxy_id=ANY($1)`, pq.Array(changed))
		} else {
			_, err = c.ExecContext(ctx, `INSERT INTO pk_proxy_group_members(proxy_id,proxy_group_id) SELECT unnest($1::bigint[]),$2 ON CONFLICT(proxy_id) DO UPDATE SET proxy_group_id=EXCLUDED.proxy_group_id`, pq.Array(changed), *input.ProxyGroupID)
		}
		if err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, `UPDATE pk_proxy_groups SET updated_at=NOW() WHERE id=ANY($1)`, pq.Array(affected)); err != nil {
			return err
		}
		return enqueueChangedProxyGroupAccounts(ctx, c, changed)
	})
	return len(ids), err
}
func enqueueChangedProxyGroupAccounts(ctx context.Context, exec sqlExecutor, proxyIDs []int64) error {
	rows, err := exec.QueryContext(ctx, `SELECT DISTINCT account_id FROM pk_effective_account_proxy_bindings WHERE proxy_id=ANY($1) ORDER BY account_id`, pq.Array(proxyIDs))
	if err != nil {
		return err
	}
	var ids []int64
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
	return enqueueProxyProbeAccountChanges(ctx, exec, ids)
}
func (r *proxyRepository) GetProxyGroupMemberships(ctx context.Context, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT proxy_id,proxy_group_id FROM pk_proxy_group_members WHERE proxy_id=ANY($1)`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, gid int64
		if err = rows.Scan(&id, &gid); err != nil {
			return nil, err
		}
		out[id] = gid
	}
	return out, rows.Err()
}
func applyProxyGroupFilter(ctx context.Context, q *dbent.ProxyQuery) *dbent.ProxyQuery {
	id, ok := service.ProxyGroupFilterFromContext(ctx)
	if !ok {
		return q
	}
	return q.Where(func(s *entsql.Selector) {
		m := entsql.Table("pk_proxy_group_members")
		sub := entsql.Select(m.C("proxy_id")).From(m)
		if id > 0 {
			sub.Where(entsql.EQ(m.C("proxy_group_id"), id))
			s.Where(entsql.In(s.C("id"), sub))
		} else {
			s.Where(entsql.NotIn(s.C("id"), sub))
		}
	})
}

func (r *groupRepository) ListGroupProxyPriorityPolicies(ctx context.Context) ([]service.GroupProxyPriorityPolicy, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT p.group_id,p.strict_priority,p.proxy_group_id FROM pk_group_proxy_priority p JOIN groups g ON g.id=p.group_id AND g.deleted_at IS NULL ORDER BY p.group_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.GroupProxyPriorityPolicy{}
	for rows.Next() {
		var p service.GroupProxyPriorityPolicy
		if err = rows.Scan(&p.GroupID, &p.StrictPriority, &p.ProxyGroupID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *groupRepository) GetGroupProxyPriorityPolicy(ctx context.Context, id int64) (*service.GroupProxyPriorityPolicy, error) {
	exec := r.sql
	if tx := dbent.TxFromContext(ctx); tx != nil {
		exec = tx.Client()
	}
	return readGroupProxyPriorityPolicy(ctx, exec, id)
}
func readGroupProxyPriorityPolicy(ctx context.Context, exec sqlExecutor, id int64) (*service.GroupProxyPriorityPolicy, error) {
	p := &service.GroupProxyPriorityPolicy{GroupID: id}
	var ids pq.Int64Array
	err := scanSingleRow(ctx, exec, `SELECT COALESCE(p.strict_priority,FALSE),p.proxy_group_id,
COALESCE((SELECT array_agg(m.proxy_id ORDER BY m.proxy_id) FROM pk_proxy_group_members m JOIN proxies proxy ON proxy.id=m.proxy_id AND proxy.deleted_at IS NULL WHERE m.proxy_group_id=p.proxy_group_id),'{}'::bigint[])
FROM groups g LEFT JOIN pk_group_proxy_priority p ON p.group_id=g.id WHERE g.id=$1 AND g.deleted_at IS NULL`, []any{id}, &p.StrictPriority, &p.ProxyGroupID, &ids)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGroupNotFound
	}
	p.ProxyIDs = []int64(ids)
	return p, err
}

var _ service.ProxyGroupRepository = (*proxyRepository)(nil)
var _ service.GroupProxyPolicyRepository = (*groupRepository)(nil)

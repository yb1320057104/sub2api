//go:build integration

package repository

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScopedProxyGroupDurableReplayAndCurrentAuthority(t *testing.T) {
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	client := tx.Client()
	proxies := newProxyRepositoryWithSQL(client, client)
	accounts := newAccountRepositoryWithSQL(client, client, nil)
	p1 := mustCreateProxy(t, client, &service.Proxy{Name: "scoped-first"})
	p2 := mustCreateProxy(t, client, &service.Proxy{Name: "scoped-second"})
	pg, err := proxies.SaveProxyGroup(ctx, 0, service.ProxyGroupInput{Name: "scoped-pool"})
	require.NoError(t, err)
	_, err = proxies.SetProxyGroupMembers(ctx, service.ProxyGroupMembersInput{ProxyIDs: []int64{p1.ID, p2.ID}, ProxyGroupID: &pg.ID})
	require.NoError(t, err)
	a := mustCreateAccount(t, client, &service.Account{Name: "scoped-account", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})
	p := &adminaccess.Principal{KeyID: "scoped-integration-key", Policy: adminaccess.Policy{GlobalPermissions: []string{"proxy-groups.assign"}, Rules: []adminaccess.Rule{{Permissions: []string{"accounts.read", "accounts.network", "accounts.schedule"}, IncludeUngrouped: true, AccountTypes: []string{"oauth"}, Platforms: []string{"openai"}}}}}
	scoped := adminaccess.WithPrincipal(ctx, p)
	concurrency := 100
	input := service.AccountProxyGroupAssignmentInput{AccountIDs: []int64{a.ID}, ProxyGroupID: pg.ID, ProxyConcurrency: 10, AccountConcurrency: &concurrency, ExpectedProxyIDs: []int64{p2.ID, p1.ID}}
	result, err := accounts.AssignScopedAccountProxyGroup(scoped, input, "stable-operation")
	require.NoError(t, err)
	require.False(t, result.Replayed)
	require.Equal(t, 2, result.BindingCount)
	var durable bool
	require.NoError(t, scanSingleRow(ctx, client, `SELECT expires_at='infinity'::timestamptz FROM idempotency_records WHERE scope=$1 AND idempotency_key_hash=$2`, []any{"pk.scoped-proxy-assignment.v1:" + service.HashIdempotencyKey(p.KeyID), service.HashIdempotencyKey("stable-operation")}, &durable))
	require.True(t, durable)
	// A lost response followed by a later administrator edit must not repeat the
	// old global mutation, even though there is no generic TTL cache to consult.
	_, err = client.ExecContext(ctx, `UPDATE pk_proxy_limits SET max_concurrency=7 WHERE proxy_id=$1`, p1.ID)
	require.NoError(t, err)
	result, err = accounts.AssignScopedAccountProxyGroup(scoped, input, "stable-operation")
	require.NoError(t, err)
	require.True(t, result.Replayed)
	bindings, err := accounts.GetAccountProxyBindings(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 7, bindings[0].MaxConcurrency)
	// Same creator, different key: it owns a separate durable operation.
	other := *p
	other.KeyID = "scoped-integration-other"
	result, err = accounts.AssignScopedAccountProxyGroup(adminaccess.WithPrincipal(ctx, &other), input, "stable-operation")
	require.NoError(t, err)
	require.False(t, result.Replayed)
	narrow := *p
	narrow.Policy.Rules = []adminaccess.Rule{{Permissions: []string{"accounts.read"}, IncludeUngrouped: true, AccountTypes: []string{"oauth"}, Platforms: []string{"openai"}}}
	_, err = accounts.AssignScopedAccountProxyGroup(adminaccess.WithPrincipal(ctx, &narrow), input, "stable-operation")
	require.ErrorIs(t, err, service.ErrAccountNotFound, "replay must recheck narrowed policy")
	changed := input
	changed.ProxyConcurrency = 11
	_, err = accounts.AssignScopedAccountProxyGroup(scoped, changed, "stable-operation")
	require.ErrorIs(t, err, service.ErrIdempotencyKeyConflict)
	_, err = proxies.SetProxyGroupMembers(ctx, service.ProxyGroupMembersInput{ProxyIDs: []int64{p2.ID}, ProxyGroupID: nil})
	require.NoError(t, err)
	_, err = accounts.AssignScopedAccountProxyGroup(scoped, input, "stable-operation")
	require.ErrorContains(t, err, "成员已变化")
	bindings, err = accounts.GetAccountProxyBindings(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 2, "membership mismatch cannot partially replace bindings")
}

func TestPK51ProxyGroupsAssignmentAndStrictPolicy(t *testing.T) {
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	client := tx.Client()
	proxies := newProxyRepositoryWithSQL(client, client)
	accounts := newAccountRepositoryWithSQL(client, client, nil)
	groups := newGroupRepositoryWithSQL(client, client)
	p1 := mustCreateProxy(t, client, &service.Proxy{Name: "pk51-first"})
	p2 := mustCreateProxy(t, client, &service.Proxy{Name: "pk51-second"})
	unassigned := mustCreateProxy(t, client, &service.Proxy{Name: "pk51-unassigned"})
	pg, err := proxies.SaveProxyGroup(ctx, 0, service.ProxyGroupInput{Name: "pk51-static", Description: "static"})
	require.NoError(t, err)
	n, err := proxies.SetProxyGroupMembers(ctx, service.ProxyGroupMembersInput{ProxyIDs: []int64{p1.ID, p2.ID}, ProxyGroupID: &pg.ID})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	all, err := proxies.ListProxyGroups(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, 2, all[0].ActiveProxyCount)
	filtered, total, err := proxies.ListWithFilters(service.WithProxyGroupFilter(ctx, pg.ID), pagination.PaginationParams{Page: 1, PageSize: 1}, "", "", "")
	require.NoError(t, err)
	require.EqualValues(t, 2, total.Total)
	require.Len(t, filtered, 1)
	other, _, err := proxies.ListWithFilters(service.WithProxyGroupFilter(ctx, 0), pagination.PaginationParams{Page: 1, PageSize: 20}, "", "", "")
	require.NoError(t, err)
	require.Len(t, other, 1)
	require.Equal(t, unassigned.ID, other[0].ID)
	a := mustCreateAccount(t, client, &service.Account{Name: "pk51-a", Platform: service.PlatformOpenAI})
	b := mustCreateAccount(t, client, &service.Account{Name: "pk51-b", Platform: service.PlatformOpenAI})
	_, err = client.ExecContext(ctx, `INSERT INTO pk_proxy_limits(proxy_id,max_accounts,max_concurrency) VALUES($1,2,3)`, p1.ID)
	require.NoError(t, err)
	result, err := accounts.AssignAccountProxyGroup(ctx, service.AccountProxyGroupAssignmentInput{AccountIDs: []int64{a.ID, b.ID}, ProxyGroupID: pg.ID, ProxyConcurrency: 10})
	require.NoError(t, err)
	require.Equal(t, 4, result.BindingCount)
	bindings, err := accounts.GetAccountProxyBindings(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 2)
	require.Equal(t, pg.ID, bindings[0].ProxyGroupID)
	require.Zero(t, bindings[0].MaxAccounts, "group assignment saves an unlimited candidate account pool")
	require.Zero(t, bindings[1].MaxAccounts)
	require.Equal(t, 10, bindings[1].MaxConcurrency)
	g, err := client.Group.Create().SetName("pk51-business").SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$3,1),($2,$3,2)`, a.ID, b.ID, g.ID)
	require.NoError(t, err)
	saved, err := accounts.UpdateGroupProxyPriorityPolicies(ctx, service.GroupProxyPriorityUpdateInput{GroupIDs: []int64{g.ID}, StrictPriority: true, ProxyGroupID: &pg.ID, AssignProxies: true, ProxyConcurrency: 10})
	require.NoError(t, err)
	require.Equal(t, 2, saved.AccountCount)
	peers, err := accounts.ListByGroup(ctx, g.ID)
	require.NoError(t, err)
	require.Len(t, peers, 2)
	for _, peer := range peers {
		require.Len(t, peer.ProxyBindings, 2)
		for _, binding := range peer.ProxyBindings {
			require.Equal(t, pg.ID, binding.ProxyGroupID)
		}
	}
	policy, err := groups.GetGroupProxyPriorityPolicy(ctx, g.ID)
	require.NoError(t, err)
	require.True(t, policy.StrictPriority)
	require.Equal(t, []int64{p1.ID, p2.ID}, policy.ProxyIDs)
	_, err = client.ExecContext(ctx, "SAVEPOINT pk51_member_guard")
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `DELETE FROM pk_proxy_group_members WHERE proxy_id=$1`, p1.ID)
	require.ErrorContains(t, err, "严格优先级")
	_, err = client.ExecContext(ctx, "ROLLBACK TO SAVEPOINT pk51_member_guard")
	require.NoError(t, err)
	_, err = proxies.SetProxyGroupMembers(ctx, service.ProxyGroupMembersInput{ProxyIDs: []int64{p1.ID}})
	require.ErrorContains(t, err, "先关闭")
	require.ErrorContains(t, proxies.DeleteProxyGroup(ctx, pg.ID), "先关闭")
	_, err = accounts.UpdateGroupProxyPriorityPolicies(ctx, service.GroupProxyPriorityUpdateInput{GroupIDs: []int64{g.ID}, StrictPriority: false})
	require.NoError(t, err)
	policy, err = groups.GetGroupProxyPriorityPolicy(ctx, g.ID)
	require.NoError(t, err)
	require.False(t, policy.StrictPriority)
	require.Equal(t, pg.ID, *policy.ProxyGroupID)
	require.NoError(t, proxies.DeleteProxyGroup(ctx, pg.ID))
	policy, err = groups.GetGroupProxyPriorityPolicy(ctx, g.ID)
	require.NoError(t, err)
	require.Nil(t, policy.ProxyGroupID)
	bindings, err = accounts.GetAccountProxyBindings(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 2)
	require.Zero(t, bindings[0].ProxyGroupID)
}

func TestPK51ProxyGroupsRejectCapacityAndOutsideShadowAtomically(t *testing.T) {
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	client := tx.Client()
	proxies := newProxyRepositoryWithSQL(client, client)
	accounts := newAccountRepositoryWithSQL(client, client, nil)
	p := mustCreateProxy(t, client, &service.Proxy{Name: "pk51-capacity"})
	pg, err := proxies.SaveProxyGroup(ctx, 0, service.ProxyGroupInput{Name: "pk51-limited"})
	require.NoError(t, err)
	_, err = proxies.SetProxyGroupMembers(ctx, service.ProxyGroupMembersInput{ProxyIDs: []int64{p.ID}, ProxyGroupID: &pg.ID})
	require.NoError(t, err)
	a := mustCreateAccount(t, client, &service.Account{Name: "pk51-parent", Platform: service.PlatformOpenAI})
	b := mustCreateAccount(t, client, &service.Account{Name: "pk51-other", Platform: service.PlatformOpenAI})
	shadow := mustCreateAccount(t, client, &service.Account{Name: "pk51-shadow", Platform: service.PlatformOpenAI, ParentAccountID: &a.ID, QuotaDimension: "spark"})
	_, err = client.ExecContext(ctx, `INSERT INTO pk_proxy_limits(proxy_id,max_accounts,max_concurrency) VALUES($1,1,3)`, p.ID)
	require.NoError(t, err)
	err = accounts.ReplaceAccountProxyBindings(ctx, []int64{a.ID, b.ID}, []service.AccountProxyBindingInput{{ProxyID: p.ID, MaxAccounts: 1, MaxConcurrency: 10}})
	require.ErrorContains(t, err, "超过设置的 1")
	bindings, err := accounts.GetAccountProxyBindings(ctx, a.ID)
	require.NoError(t, err)
	require.Empty(t, bindings)
	var max int
	require.NoError(t, scanSingleRow(ctx, client, `SELECT max_concurrency FROM pk_proxy_limits WHERE proxy_id=$1`, []any{p.ID}, &max))
	require.Equal(t, 3, max)
	_, err = accounts.AssignAccountProxyGroup(ctx, service.AccountProxyGroupAssignmentInput{AccountIDs: []int64{a.ID, b.ID}, ProxyGroupID: pg.ID, ProxyConcurrency: 10})
	require.NoError(t, err, "group candidates are not constrained by the old manual account limit")
	bindings, err = accounts.GetAccountProxyBindings(ctx, shadow.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	require.Equal(t, p.ID, bindings[0].ProxyID)
	require.Zero(t, bindings[0].MaxAccounts)
	g, err := client.Group.Create().SetName("pk51-shadow-business").SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,1)`, shadow.ID, g.ID)
	require.NoError(t, err)
	_, err = accounts.UpdateGroupProxyPriorityPolicies(ctx, service.GroupProxyPriorityUpdateInput{GroupIDs: []int64{g.ID}, StrictPriority: true, ProxyGroupID: &pg.ID, AssignProxies: true, ProxyConcurrency: 10})
	require.ErrorContains(t, err, "母账号不在所选")
	policy, err := accounts.GetGroupProxyPriorityPolicy(ctx, g.ID)
	require.NoError(t, err)
	require.False(t, policy.StrictPriority)
	require.Nil(t, policy.ProxyGroupID)
}

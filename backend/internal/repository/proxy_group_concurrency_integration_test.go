//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPK52ProxyAccountConcurrencyAssignment(t *testing.T) {
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	client := tx.Client()
	proxies := newProxyRepositoryWithSQL(client, client)
	accounts := newAccountRepositoryWithSQL(client, client, nil)
	p := mustCreateProxy(t, client, &service.Proxy{Name: "pk52-capacity"})
	pg, err := proxies.SaveProxyGroup(ctx, 0, service.ProxyGroupInput{Name: "pk52-capacity"})
	require.NoError(t, err)
	_, err = proxies.SetProxyGroupMembers(ctx, service.ProxyGroupMembersInput{ProxyIDs: []int64{p.ID}, ProxyGroupID: &pg.ID})
	require.NoError(t, err)
	a := mustCreateAccount(t, client, &service.Account{Name: "pk52-first", Platform: service.PlatformOpenAI, Concurrency: 11})
	b := mustCreateAccount(t, client, &service.Account{Name: "pk52-second", Platform: service.PlatformOpenAI, Concurrency: 22})
	readLimit := func(id int64) int {
		t.Helper()
		var value int
		require.NoError(t, scanSingleRow(ctx, client, `SELECT concurrency FROM accounts WHERE id=$1`, []any{id}, &value))
		return value
	}
	limit := 100
	_, err = accounts.AssignAccountProxyGroup(ctx, service.AccountProxyGroupAssignmentInput{AccountIDs: []int64{a.ID, b.ID}, ProxyGroupID: pg.ID, ProxyConcurrency: 10, AccountConcurrency: &limit})
	require.NoError(t, err)
	require.Equal(t, 100, readLimit(a.ID))
	require.Equal(t, 100, readLimit(b.ID))
	_, err = accounts.AssignAccountProxyGroup(ctx, service.AccountProxyGroupAssignmentInput{AccountIDs: []int64{a.ID, b.ID}, ProxyGroupID: pg.ID, ProxyConcurrency: 20})
	require.NoError(t, err)
	require.Equal(t, 100, readLimit(a.ID), "old clients preserve the saved account limit")
	g, err := client.Group.Create().SetName("pk52-business").SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$3,1),($2,$3,2)`, a.ID, b.ID, g.ID)
	require.NoError(t, err)
	limit = 200
	_, err = accounts.UpdateGroupProxyPriorityPolicies(ctx, service.GroupProxyPriorityUpdateInput{GroupIDs: []int64{g.ID}, StrictPriority: true, ProxyGroupID: &pg.ID, AssignProxies: true, ProxyConcurrency: 10, AccountConcurrency: &limit})
	require.NoError(t, err)
	require.Equal(t, 200, readLimit(a.ID))
	require.Equal(t, 200, readLimit(b.ID))
	_, err = accounts.UpdateGroupProxyPriorityPolicies(ctx, service.GroupProxyPriorityUpdateInput{GroupIDs: []int64{g.ID}, StrictPriority: false})
	require.NoError(t, err)
	require.Equal(t, 200, readLimit(a.ID), "switching the policy alone must not alter account limits")
	bindings, err := accounts.GetAccountProxyBindings(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	require.Equal(t, 10, bindings[0].MaxConcurrency)
	var payload []byte
	require.NoError(t, scanSingleRow(ctx, client, `SELECT payload FROM scheduler_outbox WHERE event_type=$1 ORDER BY id DESC LIMIT 1`, []any{service.SchedulerOutboxEventAccountBulkChanged}, &payload))
	var event struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	require.NoError(t, json.Unmarshal(payload, &event))
	require.Contains(t, event.AccountIDs, a.ID)
	require.Contains(t, event.AccountIDs, b.ID)
}

func TestPK52ProxyAccountConcurrencyCapacityRollback(t *testing.T) {
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	client := tx.Client()
	proxies := newProxyRepositoryWithSQL(client, client)
	accounts := newAccountRepositoryWithSQL(client, client, nil)
	p := mustCreateProxy(t, client, &service.Proxy{Name: "pk52-limited"})
	pg, err := proxies.SaveProxyGroup(ctx, 0, service.ProxyGroupInput{Name: "pk52-limited"})
	require.NoError(t, err)
	_, err = proxies.SetProxyGroupMembers(ctx, service.ProxyGroupMembersInput{ProxyIDs: []int64{p.ID}, ProxyGroupID: &pg.ID})
	require.NoError(t, err)
	a := mustCreateAccount(t, client, &service.Account{Name: "pk52-original-a", Platform: service.PlatformOpenAI, Concurrency: 11})
	b := mustCreateAccount(t, client, &service.Account{Name: "pk52-original-b", Platform: service.PlatformOpenAI, Concurrency: 22})
	g, err := client.Group.Create().SetName("pk52-rejected-business").SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$3,1),($2,$3,2)`, a.ID, b.ID, g.ID)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `INSERT INTO pk_proxy_limits(proxy_id,max_accounts,max_concurrency) VALUES($1,1,3)`, p.ID)
	require.NoError(t, err)
	limit := 100
	_, err = accounts.UpdateGroupProxyPriorityPolicies(ctx, service.GroupProxyPriorityUpdateInput{GroupIDs: []int64{g.ID}, StrictPriority: true, ProxyGroupID: &pg.ID, AssignProxies: true, ProxyConcurrency: 10, AccountConcurrency: &limit})
	require.ErrorContains(t, err, "超过设置的 1")
	for id, want := range map[int64]int{a.ID: 11, b.ID: 22} {
		var got int
		require.NoError(t, scanSingleRow(ctx, client, `SELECT concurrency FROM accounts WHERE id=$1`, []any{id}, &got))
		require.Equal(t, want, got)
		bindings, err := accounts.GetAccountProxyBindings(ctx, id)
		require.NoError(t, err)
		require.Empty(t, bindings)
	}
	policy, err := accounts.GetGroupProxyPriorityPolicy(ctx, g.ID)
	require.NoError(t, err)
	require.False(t, policy.StrictPriority)
	require.Nil(t, policy.ProxyGroupID)
	var proxyLimit int
	require.NoError(t, scanSingleRow(ctx, client, `SELECT max_concurrency FROM pk_proxy_limits WHERE proxy_id=$1`, []any{p.ID}, &proxyLimit))
	require.Equal(t, 3, proxyLimit)
}

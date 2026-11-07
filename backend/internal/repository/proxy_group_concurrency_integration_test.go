//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
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
	// Reject the later account-concurrency write, after candidate bindings and
	// proxy limits were updated. The caller-owned transaction must roll it all
	// back; the non-nested transaction rollback path is also covered by sqlmock.
	_, err = client.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE accounts ADD CONSTRAINT pk53_assignment_rollback CHECK(id NOT IN (%d,%d) OR concurrency < 100)`, a.ID, b.ID))
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `SAVEPOINT pk53_assignment_before`)
	require.NoError(t, err)
	limit := 100
	_, err = accounts.UpdateGroupProxyPriorityPolicies(ctx, service.GroupProxyPriorityUpdateInput{GroupIDs: []int64{g.ID}, StrictPriority: true, ProxyGroupID: &pg.ID, AssignProxies: true, ProxyConcurrency: 10, AccountConcurrency: &limit})
	require.ErrorContains(t, err, "pk53_assignment_rollback")
	_, err = client.ExecContext(ctx, `ROLLBACK TO SAVEPOINT pk53_assignment_before`)
	require.NoError(t, err)
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
	var proxyLimit, maxAccounts int
	require.NoError(t, scanSingleRow(ctx, client, `SELECT max_concurrency,max_accounts FROM pk_proxy_limits WHERE proxy_id=$1`, []any{p.ID}, &proxyLimit, &maxAccounts))
	require.Equal(t, 3, proxyLimit)
	require.Equal(t, 1, maxAccounts)
}

func TestPK53ProxyGroupCandidatesIgnoreOldBindingCount(t *testing.T) {
	for _, poolSize := range []int{10, 30} {
		for _, viaPolicy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d IPs %s", poolSize, map[bool]string{false: "account group assignment", true: "business group assignment"}[viaPolicy]), func(t *testing.T) {
				tx := testEntTx(t)
				ctx := dbent.NewTxContext(context.Background(), tx)
				client := tx.Client()
				proxies := newProxyRepositoryWithSQL(client, client)
				accounts := newAccountRepositoryWithSQL(client, client, nil)
				var proxyIDs []int64
				for i := 0; i < poolSize; i++ {
					p := mustCreateProxy(t, client, &service.Proxy{Name: fmt.Sprintf("pk53-old-three-%d", i)})
					proxyIDs = append(proxyIDs, p.ID)
					_, err := client.ExecContext(ctx, `INSERT INTO pk_proxy_limits(proxy_id,max_accounts,max_concurrency) VALUES($1,3,7)`, p.ID)
					require.NoError(t, err)
				}
				firstProxyID := proxyIDs[0]
				outside := mustCreateAccount(t, client, &service.Account{Name: "pk53-outside", Platform: service.PlatformOpenAI, ProxyID: &firstProxyID})
				pg, err := proxies.SaveProxyGroup(ctx, 0, service.ProxyGroupInput{Name: "pk53-candidates"})
				require.NoError(t, err)
				_, err = proxies.SetProxyGroupMembers(ctx, service.ProxyGroupMembersInput{ProxyIDs: proxyIDs, ProxyGroupID: &pg.ID})
				require.NoError(t, err)
				g, err := client.Group.Create().SetName("pk53-candidate-business").SetPlatform(service.PlatformOpenAI).Save(ctx)
				require.NoError(t, err)
				var ids []int64
				priorities := []int{4, 1, 1, 7, 2}
				for i, priority := range priorities {
					a := mustCreateAccount(t, client, &service.Account{Name: fmt.Sprintf("pk53-candidate-%d", i), Platform: service.PlatformOpenAI, Concurrency: 11, Priority: priority})
					ids = append(ids, a.ID)
					_, err = client.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,$3)`, a.ID, g.ID, priority)
					require.NoError(t, err)
				}
				shadow := mustCreateAccount(t, client, &service.Account{Name: "pk53-candidate-shadow", Platform: service.PlatformOpenAI, ParentAccountID: &ids[0], QuotaDimension: "spark"})
				_, err = client.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,1)`, shadow.ID, g.ID)
				require.NoError(t, err)
				concurrency := 100
				if viaPolicy {
					result, err := accounts.UpdateGroupProxyPriorityPolicies(ctx, service.GroupProxyPriorityUpdateInput{GroupIDs: []int64{g.ID}, StrictPriority: true, ProxyGroupID: &pg.ID, AssignProxies: true, ProxyConcurrency: 10, AccountConcurrency: &concurrency})
					require.NoError(t, err)
					require.Equal(t, 5, result.AccountCount, "shadows inherit without a second candidate account slot")
					require.Equal(t, 5*poolSize, result.BindingCount)
				} else {
					result, err := accounts.AssignAccountProxyGroup(ctx, service.AccountProxyGroupAssignmentInput{AccountIDs: ids, ProxyGroupID: pg.ID, ProxyConcurrency: 10, AccountConcurrency: &concurrency})
					require.NoError(t, err)
					require.Equal(t, 5, result.AccountCount)
					require.Equal(t, 5*poolSize, result.BindingCount)
				}
				for i, id := range ids {
					bindings, err := accounts.GetAccountProxyBindings(ctx, id)
					require.NoError(t, err)
					require.Len(t, bindings, poolSize)
					for _, binding := range bindings {
						require.Zero(t, binding.MaxAccounts)
						require.Equal(t, 10, binding.MaxConcurrency)
					}
					var priority, limit int
					require.NoError(t, scanSingleRow(ctx, client, `SELECT priority,concurrency FROM accounts WHERE id=$1`, []any{id}, &priority, &limit))
					require.Equal(t, priorities[i], priority, "strict ordering does not rewrite global account priority")
					require.Equal(t, 100, limit)
				}
				for _, id := range []int64{outside.ID, shadow.ID} {
					bindings, err := accounts.GetAccountProxyBindings(ctx, id)
					require.NoError(t, err)
					if id == shadow.ID {
						require.Len(t, bindings, poolSize)
					} else {
						require.Len(t, bindings, 1)
					}
					require.Equal(t, firstProxyID, bindings[0].ProxyID)
					require.Zero(t, bindings[0].MaxAccounts)
					require.Equal(t, 10, bindings[0].MaxConcurrency, "actual IP concurrency remains global")
				}
				// Explicit ordinary/manual allocation still enforces its requested
				// account limit, including outside bindings; this path is unchanged.
				err = accounts.ReplaceAccountProxyBindings(ctx, ids, []service.AccountProxyBindingInput{{ProxyID: firstProxyID, MaxAccounts: 3, MaxConcurrency: 20}})
				require.ErrorContains(t, err, "超过设置的 3")
				var maximum, limit int
				require.NoError(t, scanSingleRow(ctx, client, `SELECT max_accounts,max_concurrency FROM pk_proxy_limits WHERE proxy_id=$1`, []any{firstProxyID}, &maximum, &limit))
				require.Zero(t, maximum)
				require.Equal(t, 10, limit, "rejected manual edit must not change the saved global IP limit")
			})
		}
	}
}

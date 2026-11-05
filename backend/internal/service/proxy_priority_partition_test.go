//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func pk52PartitionRepo(count, concurrency, proxyLimit int) *proxyPriorityAccountRepo {
	r := newProxyPriorityRepo()
	third := r.accounts[1]
	third.ID = 38103
	r.accounts = append(r.accounts, third)
	r.policy.ProxyIDs = nil
	var bindings []AccountProxyBinding
	for id := int64(1); id <= int64(count); id++ {
		r.policy.ProxyIDs = append(r.policy.ProxyIDs, id)
		bindings = append(bindings, AccountProxyBinding{ProxyID: id, MaxConcurrency: proxyLimit,
			Proxy: &Proxy{ID: id, Protocol: "http", Host: "127.0.0.1", Port: 8080, Status: StatusActive}})
	}
	for i := range r.accounts {
		r.accounts[i].Concurrency = concurrency
		r.accounts[i].Priority = i + 1
		r.accounts[i].ProxyBindings = append([]AccountProxyBinding(nil), bindings...)
	}
	return r
}

func TestPK52ProxyPriorityPartitionsByCapacity(t *testing.T) {
	for _, tc := range []struct {
		count, concurrency, limit int
		want                      []int
	}{
		{10, 100, 10, []int{10, 0, 0}}, {20, 100, 10, []int{10, 10, 0}},
		{30, 100, 10, []int{10, 10, 10}}, {25, 100, 10, []int{10, 10, 5}},
		{10, 25, 10, []int{2, 2, 2}}, {10, 5, 10, []int{1, 1, 1}},
		{10, 0, 10, []int{1, 1, 1}}, {20, 100, 0, []int{20, 0, 0}},
	} {
		t.Run(fmt.Sprintf("ips%d_account%d_ip%d", tc.count, tc.concurrency, tc.limit), func(t *testing.T) {
			r := pk52PartitionRepo(tc.count, tc.concurrency, tc.limit)
			gate, _, err := buildProxyPriorityGate(context.Background(), r, 9, func(context.Context, *Account) bool { return true })
			require.NoError(t, err)
			seen := map[int64]bool{}
			for i, a := range r.accounts {
				require.Len(t, gate.subsets[a.ID], tc.want[i])
				for _, id := range gate.subsets[a.ID] {
					require.False(t, seen[id])
					seen[id] = true
				}
			}
		})
	}
}

func TestPK52ProxyPriorityPartitionsIgnoreExclusionAndFailover(t *testing.T) {
	r := pk52PartitionRepo(20, 100, 10)
	group := int64(9)
	ctx, excluded, err := prepareProxyPriority(WithAccountProxyRouting(context.Background()), r, &group, map[int64]struct{}{r.accounts[0].ID: {}}, func(context.Context, *Account) bool { return true })
	require.NoError(t, err)
	gate := ctx.Value(proxyPriorityGateKey{}).(*proxyPriorityGate)
	require.Len(t, gate.subsets[r.accounts[0].ID], 10, "request exclusions cannot donate a healthy account's partition")
	require.True(t, proxyPriorityAllowed(ctx, r.accounts[1].ID))
	require.False(t, proxyPriorityAllowed(ctx, r.accounts[2].ID))
	_, blocked := excluded[r.accounts[2].ID]
	require.True(t, blocked)
	r.accounts[0].Schedulable = false
	fresh, err := gate.validate(context.Background())
	require.NoError(t, err)
	require.Empty(t, fresh.subsets[r.accounts[0].ID])
	require.Equal(t, int64(1), fresh.subsets[r.accounts[1].ID][0])
	require.Equal(t, int64(11), fresh.subsets[r.accounts[2].ID][0])
	// Equal priorities still produce the same partition regardless of input order.
	r.accounts[0].Schedulable = true
	r.accounts[0].Priority, r.accounts[1].Priority, r.accounts[2].Priority = 1, 1, 1
	r.accounts[0], r.accounts[2] = r.accounts[2], r.accounts[0]
	fresh, _, err = buildProxyPriorityGate(context.Background(), r, 9, func(context.Context, *Account) bool { return true })
	require.NoError(t, err)
	require.Equal(t, int64(1), fresh.subsets[38101][0])
	require.Equal(t, int64(11), fresh.subsets[38102][0])
}

type pk52PartitionCache struct {
	proxyPriorityCacheStub
	resources []int64
	lease     string
	strict    bool
}

func (c *pk52PartitionCache) AcquireAccountProxy(_ context.Context, _ int64, _ int, lease string, _ int64, resources []int64, strict bool) (bool, error) {
	c.resources = append([]int64(nil), resources...)
	c.lease, c.strict = lease, strict
	return true, nil
}

func TestPK52ProxyPriorityRouterReservesOnlyAssignedSubset(t *testing.T) {
	r := pk52PartitionRepo(25, 100, 10)
	group := int64(9)
	for _, id := range []int64{38101, 38102, 38103} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			ctx := WithAccountProxyRouting(context.Background())
			_, _, err := prepareProxyPriority(ctx, r, &group, nil, func(context.Context, *Account) bool { return true })
			require.NoError(t, err)
			cache := &pk52PartitionCache{}
			route, err := NewAccountProxyRouter(r, cache).Acquire(ctx, id, "", 0)
			require.NoError(t, err)
			defer route.Close()
			gate := ctx.Value(accountProxyContextKey{}).(*accountProxyRequest).priority
			require.Equal(t, gate.subsets[id], cache.resources)
			require.Contains(t, cache.resources, route.ProxyID)
			account, ids, strict, valid := ParseAccountProxyLeaseID(route.LeaseID)
			require.True(t, valid)
			require.True(t, strict)
			require.Equal(t, id, account)
			require.Equal(t, cache.resources, ids)
		})
	}
	t.Run("ordinary_only_actual_proxy", func(t *testing.T) {
		r.policy.StrictPriority = false
		ctx := WithAccountProxyRouting(context.Background())
		cache := &pk52PartitionCache{}
		route, err := NewAccountProxyRouter(r, cache).Acquire(ctx, 38101, "", 17)
		require.NoError(t, err)
		defer route.Close()
		require.False(t, cache.strict)
		require.Equal(t, []int64{17}, cache.resources)
	})
}

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func proxyPriorityFixture(t *testing.T) (*proxyConcurrencyCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return &proxyConcurrencyCache{client: client}, server
}

func TestProxyPriorityReservationsDrainAndCapacity(t *testing.T) {
	c, _ := proxyPriorityFixture(t)
	ctx := context.Background()
	ids := []int64{1, 2}
	a := service.NewAccountProxyLeaseID(11, ids, true, "a")
	b := service.NewAccountProxyLeaseID(11, ids, true, "b")
	standby := service.NewAccountProxyLeaseID(22, ids, true, "c")
	ok, err := c.AcquireAccountProxy(ctx, 1, 1, a, 11, ids, true)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireAccountProxy(ctx, 1, 1, b, 11, ids, true)
	require.NoError(t, err)
	require.False(t, ok, "per-IP global capacity")
	ok, err = c.AcquireAccountProxy(ctx, 2, 1, b, 11, ids, true)
	require.NoError(t, err)
	require.True(t, ok, "same owner uses remaining IP")
	ok, err = c.AcquireAccountProxy(ctx, 2, 10, standby, 22, ids, true)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, c.ReleaseProxy(ctx, 1, a))
	ok, err = c.AcquireAccountProxy(ctx, 1, 10, standby, 22, ids, true)
	require.NoError(t, err)
	require.False(t, ok, "all original owner leases must drain")
	require.NoError(t, c.ReleaseProxy(ctx, 2, b))
	ok, err = c.AcquireAccountProxy(ctx, 1, 10, standby, 22, ids, true)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestProxyPriorityOverlapsAndDisabledPolicy(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprint(strict), func(t *testing.T) {
			c, _ := proxyPriorityFixture(t)
			ctx := context.Background()
			original := []int64{1, 2}
			overlap := []int64{2, 3}
			isolated := []int64{4}
			first := service.NewAccountProxyLeaseID(11, original, true, "first")
			ok, err := c.AcquireAccountProxy(ctx, 1, 10, first, 11, original, true)
			require.NoError(t, err)
			require.True(t, ok)
			second := service.NewAccountProxyLeaseID(22, overlap, strict, "second")
			ok, err = c.AcquireAccountProxy(ctx, 3, 10, second, 22, overlap, strict)
			require.NoError(t, err)
			require.False(t, ok, "different selected IP cannot evade overlapping reservation")
			third := service.NewAccountProxyLeaseID(33, isolated, strict, "third")
			ok, err = c.AcquireAccountProxy(ctx, 4, 10, third, 33, isolated, strict)
			require.NoError(t, err)
			require.True(t, ok)
			require.NoError(t, c.ReleaseProxy(ctx, 1, first))
			ok, err = c.AcquireAccountProxy(ctx, 3, 10, second, 22, overlap, strict)
			require.NoError(t, err)
			require.True(t, ok)
		})
	}
}

func TestProxyPriorityEnablingWaitsForOrdinaryAndLegacyLeases(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			c, _ := proxyPriorityFixture(t)
			ctx := context.Background()
			ids := []int64{1, 2}
			old := "old"
			var ok bool
			var err error
			if legacy {
				ok, err = c.AcquireProxy(ctx, 1, 10, old)
			} else {
				old = service.NewAccountProxyLeaseID(22, ids, false, old)
				ok, err = c.AcquireAccountProxy(ctx, 1, 10, old, 22, ids, false)
			}
			require.NoError(t, err)
			require.True(t, ok)
			newLease := service.NewAccountProxyLeaseID(11, ids, true, "new")
			ok, err = c.AcquireAccountProxy(ctx, 2, 10, newLease, 11, ids, true)
			require.NoError(t, err)
			require.False(t, ok)
			require.NoError(t, c.ReleaseProxy(ctx, 1, old))
			ok, err = c.AcquireAccountProxy(ctx, 2, 10, newLease, 11, ids, true)
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = c.AcquireProxy(ctx, 1, 10, "legacy-next")
			require.NoError(t, err)
			require.False(t, ok)
		})
	}
}

func TestProxyPriorityOrdinaryRequestsPreserveParallelism(t *testing.T) {
	c, _ := proxyPriorityFixture(t)
	ctx := context.Background()
	ids := []int64{1, 2}
	for _, account := range []int64{11, 22} {
		lease := service.NewAccountProxyLeaseID(account, ids, false, fmt.Sprint(account))
		ok, err := c.AcquireAccountProxy(ctx, 1, 10, lease, account, ids, false)
		require.NoError(t, err)
		require.True(t, ok)
	}
}

func TestProxyPriorityPersistentRefreshOriginalResourcesAndExpiry(t *testing.T) {
	c, server := proxyPriorityFixture(t)
	ctx := context.Background()
	ids := []int64{1, 2}
	lease := service.NewAccountProxyLeaseID(11, ids, true, "live")
	ok, err := c.AcquireAccountProxy(ctx, 1, 10, lease, 11, ids, true)
	require.NoError(t, err)
	require.True(t, ok)
	server.SetTime(time.Now().Add(20 * time.Second))
	ok, err = c.RefreshProxy(ctx, 1, lease)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, server.Exists(proxyReservationKey(2)))
	server.Del(proxyReservationKey(2))
	ok, err = c.RefreshProxy(ctx, 1, lease)
	require.NoError(t, err)
	require.False(t, ok, "missing reservation must not resurrect")
	require.False(t, server.Exists(proxyReservationKey(2)))
	require.NoError(t, c.ReleaseProxy(ctx, 1, lease))
	require.NoError(t, c.ReleaseProxy(ctx, 1, lease))
	server.FastForward(2 * time.Minute)
	ok, err = c.RefreshProxy(ctx, 1, lease)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestProxyPriorityConcurrentInstancesHaveOneOwner(t *testing.T) {
	c, _ := proxyPriorityFixture(t)
	ctx := context.Background()
	ids := []int64{1, 2}
	var wg sync.WaitGroup
	owners := make(chan int64, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			account := int64(i%2 + 1)
			other := &proxyConcurrencyCache{client: c.client}
			lease := service.NewAccountProxyLeaseID(account, ids, true, fmt.Sprint(i))
			ok, err := other.AcquireAccountProxy(ctx, int64(i%2+1), 100, lease, account, ids, true)
			if err != nil {
				t.Error(err)
			}
			if ok {
				owners <- account
			}
		}(i)
	}
	wg.Wait()
	close(owners)
	var winner int64
	count := 0
	for owner := range owners {
		count++
		if winner == 0 {
			winner = owner
		}
		require.Equal(t, winner, owner)
	}
	require.Positive(t, count)
}

//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestPK51ProxyPriorityRedisLeaseLifecycle(t *testing.T) {
	client := testRedis(t)
	c := &proxyConcurrencyCache{client: client}
	ctx := context.Background()
	ids := []int64{51, 52}
	first := service.NewAccountProxyLeaseID(1, ids, true, "first")
	second := service.NewAccountProxyLeaseID(1, ids, true, "second")
	next := service.NewAccountProxyLeaseID(2, ids, true, "next")
	ok, err := c.AcquireAccountProxy(ctx, 51, 1, first, 1, ids, true)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireAccountProxy(ctx, 51, 1, second, 1, ids, true)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = c.AcquireAccountProxy(ctx, 52, 1, second, 1, ids, true)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, c.ReleaseProxy(ctx, 51, first))
	ok, err = c.AcquireAccountProxy(ctx, 51, 1, next, 2, ids, true)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = c.RefreshProxy(ctx, 52, second)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, c.ReleaseProxy(ctx, 52, second))
	ok, err = c.AcquireAccountProxy(ctx, 51, 1, next, 2, ids, true)
	require.NoError(t, err)
	require.True(t, ok)
	// Age actual Redis lease scores, keeping the same production expiry script.
	now, err := client.Time(ctx).Result()
	require.NoError(t, err)
	for _, key := range []string{proxyLeaseKey(51), proxyReservationKey(51), proxyReservationKey(52)} {
		require.NoError(t, client.ZAdd(ctx, key, redis.Z{Score: float64(now.Unix() - proxyLeaseSeconds - 1), Member: next}).Err())
	}
	ok, err = c.RefreshProxy(ctx, 51, next)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = c.AcquireAccountProxy(ctx, 51, 1, first, 1, ids, true)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestPK51ProxyPriorityRedisOldLeaseAndOverlappingPools(t *testing.T) {
	client := testRedis(t)
	c := &proxyConcurrencyCache{client: client}
	ctx := context.Background()
	ids := []int64{61, 62}
	overlap := []int64{62, 63}
	ok, err := c.AcquireProxy(ctx, 61, 10, "legacy")
	require.NoError(t, err)
	require.True(t, ok)
	first := service.NewAccountProxyLeaseID(1, ids, true, "first")
	ok, err = c.AcquireAccountProxy(ctx, 62, 10, first, 1, ids, true)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, c.ReleaseProxy(ctx, 61, "legacy"))
	ok, err = c.AcquireAccountProxy(ctx, 61, 10, first, 1, ids, true)
	require.NoError(t, err)
	require.True(t, ok)
	for _, strict := range []bool{false, true} {
		lease := service.NewAccountProxyLeaseID(2, overlap, strict, fmt.Sprint(strict))
		ok, err = c.AcquireAccountProxy(ctx, 63, 10, lease, 2, overlap, strict)
		require.NoError(t, err)
		require.False(t, ok, "disabled or different business group cannot bypass physical reservation")
	}
	require.NoError(t, c.ReleaseProxy(ctx, 61, first))
	ordinary := service.NewAccountProxyLeaseID(2, overlap, false, "ordinary")
	ok, err = c.AcquireAccountProxy(ctx, 63, 10, ordinary, 2, overlap, false)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireAccountProxy(ctx, 61, 10, first, 1, ids, true)
	require.NoError(t, err)
	require.False(t, ok, "enabling strict waits for ordinary overlap")
}

func TestPK51ProxyPriorityRedisCompetingInstances(t *testing.T) {
	client := testRedis(t)
	caches := []*proxyConcurrencyCache{{client: client}, {client: client}}
	ctx := context.Background()
	ids := []int64{71, 72}
	var wg sync.WaitGroup
	owners := make(chan int64, 32)
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			account := int64(i%2 + 1)
			lease := service.NewAccountProxyLeaseID(account, ids, true, fmt.Sprint(i))
			ok, err := caches[i%2].AcquireAccountProxy(ctx, 71+int64(i%2), 100, lease, account, ids, true)
			if err != nil {
				t.Error(err)
			}
			if ok {
				owners <- account
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(owners)
	winner := int64(0)
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

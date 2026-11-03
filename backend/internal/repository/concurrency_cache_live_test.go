package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type routingBindingFixture map[int64][]service.AccountProxyBinding

func (f routingBindingFixture) GetAccountProxyBindings(_ context.Context, id int64) ([]service.AccountProxyBinding, error) {
	return f[id], nil
}

func TestAccountProxyPersistentLiveLeaseSurvivesRequestAndControllerHandoffs(t *testing.T) {
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	reader := routingBindingFixture{11: {{ProxyID: 1, MaxConcurrency: 1, Proxy: &service.Proxy{ID: 1, Status: service.StatusActive, Protocol: "http", Host: "live.invalid", Port: 80}}}}
	router := service.NewAccountProxyRouter(reader, NewProxyConcurrencyCache(client))
	ctx, cancel := context.WithCancel(service.WithAccountProxyRouting(t.Context()))
	created, err := router.AcquirePersistent(ctx, 11, "", 0, time.Minute)
	require.NoError(t, err)
	t.Cleanup(created.Close)
	require.NotEmpty(t, created.LeaseID)
	cancel()
	require.NoError(t, created.Context.Err(), "HTTP cancellation must not close a successfully created call")
	require.True(t, created.Handoff())
	<-created.Context.Done()
	created.Close() // A former request owner may no longer release a transferred slot.
	for i := 0; i < 3; i++ {
		controller, err := router.ResumePersistent(t.Context(), 11, 1, created.LeaseID)
		require.NoError(t, err)
		controller.Close()
		counts, err := router.GetProxyConcurrencyBatch(t.Context(), []int64{1})
		require.NoError(t, err)
		require.Equal(t, 1, counts[1], "handoff must keep exactly one slot")
	}
	_, err = router.Acquire(service.WithAccountProxyRouting(t.Context()), 11, "", 0)
	require.ErrorIs(t, err, service.ErrProxyCapacity)
	router.ReleasePersistent(t.Context(), 1, created.LeaseID)
	_, err = router.ResumePersistent(t.Context(), 11, 1, created.LeaseID)
	require.ErrorIs(t, err, service.ErrProxyLeaseUnavailable)
	counts, err := router.GetProxyConcurrencyBatch(t.Context(), []int64{1})
	require.NoError(t, err)
	require.Zero(t, counts[1], "continuation may not resurrect a released slot")
}

func TestAccountProxyPersistentLiveLeaseUnbindAndAbandonedHandoffFailClosed(t *testing.T) {
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	reader := routingBindingFixture{11: {{ProxyID: 1, MaxConcurrency: 1, Proxy: &service.Proxy{ID: 1, Status: service.StatusActive, Protocol: "http", Host: "live.invalid", Port: 80}}}}
	router := service.NewAccountProxyRouter(reader, NewProxyConcurrencyCache(client))
	held, err := router.AcquirePersistent(service.WithAccountProxyRouting(t.Context()), 11, "", 0, time.Hour)
	require.NoError(t, err)
	require.True(t, held.Handoff())
	reader[11] = nil
	_, err = router.ResumePersistent(t.Context(), 11, 1, held.LeaseID)
	require.ErrorIs(t, err, service.ErrProxyUnavailable, "an unbound live proxy cannot silently become direct")
	// No keeper remains after persistence; a process that never resumes the
	// stored call cannot keep its proxy busy beyond the Redis lease timeout.
	rdb.FastForward(time.Minute + time.Second)
	counts, err := router.GetProxyConcurrencyBatch(t.Context(), []int64{1})
	require.NoError(t, err)
	require.Zero(t, counts[1])
}

func TestAccountProxyRoutingGlobalCapacityAndRequestAffinity(t *testing.T) {
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	bindings := []service.AccountProxyBinding{
		{ProxyID: 1, MaxConcurrency: 1, Proxy: &service.Proxy{ID: 1, Status: service.StatusActive, Protocol: "http", Host: "first.example", Port: 80}},
		{ProxyID: 2, MaxConcurrency: 1, Proxy: &service.Proxy{ID: 2, Status: service.StatusActive, Protocol: "http", Host: "second.example", Port: 80}},
	}
	reader := routingBindingFixture{11: bindings, 12: bindings}
	router := service.NewAccountProxyRouter(reader, NewProxyConcurrencyCache(client))
	ctx := service.WithAccountProxyRouting(context.Background())
	first, err := router.Acquire(ctx, 11, "must-not-be-used", 0)
	require.NoError(t, err)
	defer first.Close()
	other, err := router.Acquire(service.WithAccountProxyRouting(context.Background()), 12, "", 0)
	require.NoError(t, err)
	defer other.Close()
	require.NotEqual(t, first.ProxyID, other.ProxyID)
	_, err = router.Acquire(service.WithAccountProxyRouting(context.Background()), 11, "", 0)
	require.ErrorIs(t, err, service.ErrProxyCapacity)
	counts, err := router.GetProxyConcurrencyBatch(context.Background(), []int64{1, 2, 1})
	require.NoError(t, err)
	require.Equal(t, map[int64]int{1: 1, 2: 1}, counts)
	chosen := first.ProxyID
	first.Close()
	retry, err := router.Acquire(ctx, 11, "", 0)
	require.NoError(t, err)
	defer retry.Close()
	require.Equal(t, chosen, retry.ProxyID)
	_, err = router.Acquire(ctx, 11, "", other.ProxyID)
	require.ErrorIs(t, err, service.ErrProxyUnavailable)
}

func TestProxyConcurrencyAtomicLimitAcrossInstances(t *testing.T) {
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewProxyConcurrencyCache(client)
	var wg sync.WaitGroup
	var mu sync.Mutex
	acquired := 0
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ok, err := cache.AcquireProxy(context.Background(), 1, 3, time.Now().String()+string(rune(n)))
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				mu.Lock()
				acquired++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, 3, acquired)
	rdb.FastForward(61 * time.Second)
	ok, err := cache.RefreshProxy(context.Background(), 1, "lost-owner")
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = cache.AcquireProxy(context.Background(), 1, 3, "new-owner")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, cache.ReleaseProxy(context.Background(), 1, "new-owner"))
}

func TestAccountProxyRoutingCancellationAndUnavailableBindings(t *testing.T) {
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	reader := routingBindingFixture{1: {{ProxyID: 1, MaxConcurrency: 1, Proxy: &service.Proxy{ID: 1, Status: service.StatusActive, Protocol: "http", Host: "proxy.example", Port: 80}}}, 2: {{ProxyID: 2, Proxy: &service.Proxy{ID: 2, Status: "disabled"}}}}
	router := service.NewAccountProxyRouter(reader, NewProxyConcurrencyCache(client))
	ctx, cancel := context.WithCancel(service.WithAccountProxyRouting(context.Background()))
	route, err := router.Acquire(ctx, 1, "", 0)
	require.NoError(t, err)
	defer route.Close()
	cancel()
	require.Eventually(t, func() bool {
		counts, e := router.GetProxyConcurrencyBatch(context.Background(), []int64{1})
		return e == nil && counts[1] == 0
	}, time.Second, 10*time.Millisecond)
	require.ErrorIs(t, route.Context.Err(), context.Canceled)
	_, err = router.Acquire(service.WithAccountProxyRouting(context.Background()), 2, "", 0)
	require.ErrorIs(t, err, service.ErrProxyUnavailable)
	plain, err := router.Acquire(context.Background(), 2, "explicit-probe", 0)
	require.NoError(t, err)
	require.Equal(t, "explicit-probe", plain.URL)
	direct, err := router.Acquire(service.WithAccountProxyRouting(context.Background()), 3, "stale-proxy", 0)
	require.NoError(t, err)
	require.Empty(t, direct.URL)
	broken := service.NewAccountProxyRouter(reader, nil)
	_, err = broken.Acquire(service.WithAccountProxyRouting(context.Background()), 1, "", 0)
	require.True(t, errors.Is(err, service.ErrProxyLeaseUnavailable))
	_, err = router.Acquire(service.WithAccountProxyRouting(context.Background()), 3, "", 1)
	require.ErrorIs(t, err, service.ErrProxyUnavailable, "a pinned connection must not fall back to direct after its binding is removed")
}

func TestLiveLeaseReplacesRegularSlotsAndCountsTowardLimits(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	live, ok := regular.(service.LiveConcurrencyCache)
	require.True(t, ok)
	ctx := context.Background()

	accountAcquired, err := regular.AcquireAccountSlot(ctx, 10, 1, "regular-account")
	require.NoError(t, err)
	require.True(t, accountAcquired)
	userAcquired, err := regular.AcquireUserSlot(ctx, 20, 1, "regular-user")
	require.NoError(t, err)
	require.True(t, userAcquired)

	acquired, err := live.AcquireLiveLease(ctx, 10, 1, 20, 1, 30, "live-lease", true)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, regular.ReleaseAccountSlot(ctx, 10, "regular-account"))
	require.NoError(t, regular.ReleaseUserSlot(ctx, 20, "regular-user"))

	accountCount, err := regular.GetAccountConcurrency(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, accountCount)
	userCount, err := regular.GetUserConcurrency(ctx, 20)
	require.NoError(t, err)
	require.Equal(t, 1, userCount)
	accountAcquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-blocked")
	require.NoError(t, err)
	require.False(t, accountAcquired)

	refreshed, err := live.RefreshLiveLease(ctx, 10, 20, 30, "live-lease")
	require.NoError(t, err)
	require.True(t, refreshed)
	require.NoError(t, live.ReleaseLiveLease(ctx, 10, 20, 30, "live-lease"))
	accountAcquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-allowed")
	require.NoError(t, err)
	require.True(t, accountAcquired)
}

func TestLiveLeaseExpiresWithoutRefresh(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	live, ok := regular.(service.LiveConcurrencyCache)
	require.True(t, ok)
	ctx := context.Background()

	acquired, err := live.AcquireLiveLease(ctx, 10, 1, 20, 1, 30, "expired-live", false)
	require.NoError(t, err)
	require.True(t, acquired)

	redisServer.FastForward(61 * time.Second)
	acquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-after-expiry")
	require.NoError(t, err)
	require.True(t, acquired)
	refreshed, err := live.RefreshLiveLease(ctx, 10, 20, 30, "expired-live")
	require.NoError(t, err)
	require.False(t, refreshed)
}

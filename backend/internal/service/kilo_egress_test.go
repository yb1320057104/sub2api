package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type kiloRouterGuardFixture struct {
	acquire func(context.Context, int64, int64, string) (*KiloEgressPermit, error)
}

func (g kiloRouterGuardFixture) Acquire(ctx context.Context, accountID, proxyID int64, proxyURL string) (*KiloEgressPermit, error) {
	return g.acquire(ctx, accountID, proxyID, proxyURL)
}

type kiloRouterReaderFixture struct {
	bindings []AccountProxyBinding
}

func (r kiloRouterReaderFixture) GetAccountProxyBindings(context.Context, int64) ([]AccountProxyBinding, error) {
	return r.bindings, nil
}

type kiloRouterCacheFixture struct{ releases atomic.Int32 }

func (*kiloRouterCacheFixture) AcquireProxy(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (*kiloRouterCacheFixture) RefreshProxy(context.Context, int64, string) (bool, error) {
	return true, nil
}
func (c *kiloRouterCacheFixture) ReleaseProxy(context.Context, int64, string) error {
	c.releases.Add(1)
	return nil
}
func (*kiloRouterCacheFixture) GetProxyConcurrencyBatch(context.Context, []int64) (map[int64]int, error) {
	return nil, nil
}

func TestKiloEgressRouterGuardsFallbackDirectAndBorrowed(t *testing.T) {
	for _, mode := range []string{"fallback_without_routing_flag", "direct_without_routing_flag", "no_bound_proxy", "borrowed"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			guard := kiloRouterGuardFixture{acquire: func(context.Context, int64, int64, string) (*KiloEgressPermit, error) {
				calls++
				return nil, ErrKiloEgressUnavailable
			}}
			router := NewAccountProxyRouter(kiloRouterReaderFixture{}, &kiloRouterCacheFixture{}, guard)
			ctx := context.Background()
			fallback := ""
			switch mode {
			case "fallback_without_routing_flag":
				fallback = "http://proxy.invalid:8000"
			case "no_bound_proxy":
				ctx = WithAccountProxyRouting(ctx)
			case "borrowed":
				ctx = withBorrowedAccountProxyRoute(ctx, 7, &AccountProxyRoute{Context: ctx, URL: "http://proxy.invalid:8000", ProxyID: 11})
			}
			route, err := router.Acquire(ctx, 7, fallback, 0)
			require.Nil(t, route)
			require.ErrorIs(t, err, ErrProxyUnavailable)
			require.ErrorIs(t, err, ErrKiloEgressUnavailable)
			require.Equal(t, 1, calls)
		})
	}
}

func TestKiloEgressRouterRetainsPermitUntilClose(t *testing.T) {
	var release atomic.Int32
	var cancelPermit context.CancelFunc
	guard := kiloRouterGuardFixture{acquire: func(ctx context.Context, accountID, proxyID int64, proxyURL string) (*KiloEgressPermit, error) {
		require.Equal(t, int64(7), accountID)
		require.Equal(t, int64(11), proxyID)
		require.Equal(t, "http://proxy.invalid:8000", proxyURL)
		permitCtx, cancel := context.WithCancel(ctx)
		cancelPermit = cancel
		return NewKiloEgressPermit(permitCtx, func() { cancel(); release.Add(1) }), nil
	}}
	reader := kiloRouterReaderFixture{bindings: []AccountProxyBinding{{ProxyID: 11, MaxConcurrency: 10, Proxy: &Proxy{ID: 11, Protocol: "http", Host: "proxy.invalid", Port: 8000, Status: StatusActive}}}}
	cache := &kiloRouterCacheFixture{}
	ctx, cancel := context.WithCancel(WithAccountProxyRouting(context.Background()))
	defer cancel()
	router := NewAccountProxyRouter(reader, cache, guard)
	route, err := router.Acquire(ctx, 7, "", 0)
	require.NoError(t, err)
	require.Zero(t, release.Load())
	require.False(t, route.Handoff())
	// A lost keeper stops upstream immediately without pretending Body.Close.
	cancelPermit()
	require.ErrorIs(t, route.Context.Err(), context.Canceled)
	require.Zero(t, release.Load())
	route.Close()
	route.Close()
	require.Equal(t, int32(1), release.Load())
	require.Equal(t, int32(1), cache.releases.Load())
}

func TestKiloEgressRouterRejectedRouteReturnsProxySlot(t *testing.T) {
	guard := kiloRouterGuardFixture{acquire: func(context.Context, int64, int64, string) (*KiloEgressPermit, error) {
		return nil, ErrKiloEgressConflict
	}}
	reader := kiloRouterReaderFixture{bindings: []AccountProxyBinding{{ProxyID: 11, Proxy: &Proxy{ID: 11, Protocol: "http", Host: "proxy.invalid", Port: 8000, Status: StatusActive}}}}
	cache := &kiloRouterCacheFixture{}
	ctx := WithAccountProxyRouting(context.Background())
	route, err := NewAccountProxyRouter(reader, cache, guard).Acquire(ctx, 7, "", 0)
	require.Nil(t, route)
	require.ErrorIs(t, err, ErrKiloEgressConflict)
	require.Equal(t, int32(1), cache.releases.Load())
	require.Zero(t, selectedAccountProxyID(ctx, 7))
}

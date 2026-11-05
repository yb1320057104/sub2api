//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPK52ProxyPriorityRedisPartitionsAndDrain(t *testing.T) {
	c := &proxyConcurrencyCache{client: testRedis(t)}
	ctx := context.Background()
	firstIDs, secondIDs := []int64{521, 522}, []int64{523, 524}
	first := service.NewAccountProxyLeaseID(1, firstIDs, true, "first")
	second := service.NewAccountProxyLeaseID(2, secondIDs, true, "second")
	ok, err := c.AcquireAccountProxy(ctx, 521, 10, first, 1, firstIDs, true)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireAccountProxy(ctx, 523, 10, second, 2, secondIDs, true)
	require.NoError(t, err)
	require.True(t, ok, "disjoint accounts can run simultaneously")
	newOwner := service.NewAccountProxyLeaseID(3, firstIDs, true, "takeover")
	ok, err = c.AcquireAccountProxy(ctx, 522, 10, newOwner, 3, firstIDs, true)
	require.NoError(t, err)
	require.False(t, ok, "unavailable prior account must drain across its complete partition")
	require.NoError(t, c.ReleaseProxy(ctx, 521, first))
	ok, err = c.AcquireAccountProxy(ctx, 522, 10, newOwner, 3, firstIDs, true)
	require.NoError(t, err)
	require.True(t, ok)
	// An old pk51 lease covered the full pool. New partition owners still wait.
	require.NoError(t, c.ReleaseProxy(ctx, 522, newOwner))
	require.NoError(t, c.ReleaseProxy(ctx, 523, second))
	all := []int64{521, 522, 523, 524}
	legacy := service.NewAccountProxyLeaseID(1, all, true, "pk51")
	ok, err = c.AcquireAccountProxy(ctx, 521, 10, legacy, 1, all, true)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireAccountProxy(ctx, 523, 10, second, 2, secondIDs, true)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, c.ReleaseProxy(ctx, 521, legacy))
	ok, err = c.AcquireAccountProxy(ctx, 523, 10, second, 2, secondIDs, true)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestPK52ProxyPriorityRedisOrdinaryUsesPhysicalProxy(t *testing.T) {
	c := &proxyConcurrencyCache{client: testRedis(t)}
	ctx := context.Background()
	ordinaryIDs := []int64{531}
	ordinary := service.NewAccountProxyLeaseID(1, ordinaryIDs, false, "ordinary")
	ok, err := c.AcquireAccountProxy(ctx, 531, 10, ordinary, 1, ordinaryIDs, false)
	require.NoError(t, err)
	require.True(t, ok)
	disjointIDs := []int64{532, 533}
	disjoint := service.NewAccountProxyLeaseID(2, disjointIDs, true, "strict")
	ok, err = c.AcquireAccountProxy(ctx, 532, 10, disjoint, 2, disjointIDs, true)
	require.NoError(t, err)
	require.True(t, ok)
	overlapIDs := []int64{531, 534}
	overlap := service.NewAccountProxyLeaseID(3, overlapIDs, true, "overlap")
	ok, err = c.AcquireAccountProxy(ctx, 534, 10, overlap, 3, overlapIDs, true)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, c.ReleaseProxy(ctx, 531, ordinary))
	ok, err = c.AcquireAccountProxy(ctx, 534, 10, overlap, 3, overlapIDs, true)
	require.NoError(t, err)
	require.True(t, ok)
}

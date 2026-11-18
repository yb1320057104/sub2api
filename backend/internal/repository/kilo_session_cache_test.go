package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestKiloWireSessionSharedIdentityAndExpiry(t *testing.T) {
	server := miniredis.RunT(t)
	firstClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer firstClient.Close()
	peerClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer peerClient.Close()
	first, peer := &gatewayCache{rdb: firstClient}, &gatewayCache{rdb: peerClient}
	ctx := context.Background()
	require.NoError(t, first.SetKiloGatewayResponseSession(ctx, 10, 20, "resp_shared", "ses_first", time.Hour))
	got, err := peer.GetKiloGatewayResponseSession(ctx, 10, 20, "resp_shared")
	require.NoError(t, err)
	require.Equal(t, "ses_first", got)
	for _, scope := range [][2]int64{{11, 20}, {10, 21}} {
		got, err = peer.GetKiloGatewayResponseSession(ctx, scope[0], scope[1], "resp_shared")
		require.NoError(t, err)
		require.Empty(t, got)
	}
	require.Error(t, peer.SetKiloGatewayResponseSession(ctx, 10, 20, "resp_shared", "ses_other", time.Hour))
	require.NoError(t, peer.SetKiloGatewayResponseSession(ctx, 10, 20, "resp_shared", "ses_first", time.Hour))
	server.FastForward(time.Hour + time.Second)
	got, err = first.GetKiloGatewayResponseSession(ctx, 10, 20, "resp_shared")
	require.NoError(t, err)
	require.Empty(t, got)
	require.Error(t, first.SetKiloGatewayResponseSession(ctx, 0, 20, "resp", "ses", time.Hour))
	require.Error(t, first.SetKiloGatewayResponseSession(ctx, 10, 20, "resp", "bad\nsession", time.Hour))
}

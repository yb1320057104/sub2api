//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestKiloWireSessionRedisIndependentInstances(t *testing.T) {
	ctx := context.Background()
	prefix := "kilo-wire-contract:" + uuid.NewString() + ":"
	rdb := redis.NewClient(integrationRedis.Options())
	defer rdb.Close()
	rdb.AddHook(prefixHook{prefix: prefix})
	peerClient := redis.NewClient(integrationRedis.Options())
	defer peerClient.Close()
	peerClient.AddHook(prefixHook{prefix: prefix})
	writer := NewGatewayCache(rdb).(service.KiloGatewaySessionStore)
	reader := NewGatewayCache(peerClient).(service.KiloGatewaySessionStore)
	cacheKey, err := kiloGatewayWireSessionKey(91, 92, "resp_shared_redis")
	require.NoError(t, err)
	concurrentKey, err := kiloGatewayWireSessionKey(91, 92, "resp_concurrent_redis")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, integrationRedis.Del(context.Background(), prefix+cacheKey, prefix+concurrentKey).Err())
	})
	require.NoError(t, writer.SetKiloGatewayResponseSession(ctx, 91, 92, "resp_shared_redis", "ses_stable", time.Minute))
	got, err := reader.GetKiloGatewayResponseSession(ctx, 91, 92, "resp_shared_redis")
	require.NoError(t, err)
	require.Equal(t, "ses_stable", got)
	ttl, err := rdb.PTTL(ctx, cacheKey).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, time.Duration(0))
	require.LessOrEqual(t, ttl, time.Minute)
	for _, scope := range [][2]int64{{90, 92}, {91, 93}} {
		got, err = reader.GetKiloGatewayResponseSession(ctx, scope[0], scope[1], "resp_shared_redis")
		require.NoError(t, err)
		require.Empty(t, got)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, session := range []string{"ses_winner_a", "ses_winner_b"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			results <- reader.SetKiloGatewayResponseSession(ctx, 91, 92, "resp_concurrent_redis", value, time.Minute)
		}(session)
	}
	wg.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			conflicts++
		}
	}
	require.Equal(t, 1, winners)
	require.Equal(t, 1, conflicts)
	require.NoError(t, rdb.Del(ctx, cacheKey).Err())
	got, err = reader.GetKiloGatewayResponseSession(ctx, 91, 92, "resp_shared_redis")
	require.NoError(t, err)
	require.Empty(t, got)
}

package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const proxyLeaseSeconds = 60

var proxyLeaseAcquire = redis.NewScript(`
redis.replicate_commands()
local now=tonumber(redis.call('TIME')[1])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now-tonumber(ARGV[3]))
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now-tonumber(ARGV[3]))
for _,member in ipairs(redis.call('ZRANGE',KEYS[2],0,-1)) do
 if string.match(member,'^p2:%d+:1:') then return 0 end
end
local count=redis.call('ZCARD', KEYS[1])
if tonumber(ARGV[1])>0 and count>=tonumber(ARGV[1]) then return 0 end
redis.call('ZADD', KEYS[1], now, ARGV[2])
redis.call('EXPIRE', KEYS[1], ARGV[3])
return 1
`)
var proxyLeaseRefresh = redis.NewScript(`
redis.replicate_commands()
local now=tonumber(redis.call('TIME')[1])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now-tonumber(ARGV[2]))
if redis.call('ZSCORE', KEYS[1], ARGV[1])==false then return 0 end
redis.call('ZADD', KEYS[1], now, ARGV[1])
redis.call('EXPIRE', KEYS[1], ARGV[2])
return 1
`)
var proxyLeaseCount = redis.NewScript(`
redis.replicate_commands()
local now=tonumber(redis.call('TIME')[1])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now-tonumber(ARGV[1]))
return redis.call('ZCARD', KEYS[1])
`)

type proxyConcurrencyCache struct{ client *redis.Client }

func NewProxyConcurrencyCache(client *redis.Client) service.ProxyConcurrencyCache {
	return &proxyConcurrencyCache{client: client}
}
func proxyLeaseKey(id int64) string { return fmt.Sprintf("concurrency:proxy:%d", id) }
func (c *proxyConcurrencyCache) AcquireProxy(ctx context.Context, id int64, limit int, lease string) (bool, error) {
	result, err := proxyLeaseAcquire.Run(ctx, c.client, []string{proxyLeaseKey(id), proxyReservationKey(id)}, limit, lease, proxyLeaseSeconds).Int()
	return result == 1, err
}
func (c *proxyConcurrencyCache) RefreshProxy(ctx context.Context, id int64, lease string) (bool, error) {
	if keys, managed := proxyLeaseManagedKeys(id, lease); managed {
		result, err := accountProxyLeaseRefresh.Run(ctx, c.client, keys, lease, proxyLeaseSeconds).Int()
		return result == 1, err
	}
	result, err := proxyLeaseRefresh.Run(ctx, c.client, []string{proxyLeaseKey(id)}, lease, proxyLeaseSeconds).Int()
	return result == 1, err
}
func (c *proxyConcurrencyCache) ReleaseProxy(ctx context.Context, id int64, lease string) error {
	if keys, managed := proxyLeaseManagedKeys(id, lease); managed {
		return accountProxyLeaseRelease.Run(ctx, c.client, keys, lease).Err()
	}
	return c.client.ZRem(ctx, proxyLeaseKey(id), lease).Err()
}
func (c *proxyConcurrencyCache) GetProxyConcurrencyBatch(ctx context.Context, ids []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(ids))
	pipe := c.client.Pipeline()
	commands := make(map[int64]*redis.Cmd, len(ids))
	for _, id := range ids {
		if id <= 0 || commands[id] != nil {
			continue
		}
		// EVAL avoids a pipeline NOSCRIPT race after Redis restart.
		commands[id] = pipe.Eval(ctx, proxyLeaseCountSource, []string{proxyLeaseKey(id)}, proxyLeaseSeconds)
	}
	if len(commands) == 0 {
		return result, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	for id, cmd := range commands {
		count, err := cmd.Int()
		if err != nil {
			return nil, err
		}
		result[id] = count
	}
	return result, nil
}

const proxyLeaseCountSource = `redis.replicate_commands(); local now=tonumber(redis.call('TIME')[1]); redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now-tonumber(ARGV[1])); return redis.call('ZCARD',KEYS[1])`

package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// All proxy reservations use one Lua transaction. Reservation keys are indexed
// by physical proxy ID, never group ID. Ordinary requests coexist as before,
// while either side being strict requires the same account across the entire
// original set. This is also the drain barrier when health or policy changes.
var accountProxyLeaseAcquire = redis.NewScript(`
redis.replicate_commands()
local now=tonumber(redis.call('TIME')[1])
local ttl=tonumber(ARGV[3])
local strict=ARGV[5]=='1'
local account=ARGV[4]
local function cleanup(key) redis.call('ZREMRANGEBYSCORE',key,'-inf',now-ttl) end
local function conflicts(member, incomingStrict)
 local a,m=string.match(member,'^p2:(%d+):([01]):')
 if not a then return incomingStrict end
 return a~=account and (incomingStrict or m=='1')
end
cleanup(KEYS[1])
if tonumber(ARGV[1])>0 and redis.call('ZCARD',KEYS[1])>=tonumber(ARGV[1]) then return 0 end
for i=2,#KEYS,2 do
 cleanup(KEYS[i]); cleanup(KEYS[i+1])
 for _,member in ipairs(redis.call('ZRANGE',KEYS[i],0,-1)) do
  if conflicts(member,strict) then return 0 end
 end
 -- Old p1 leases have no reservation metadata. Enabling strict mode waits
 -- for those real in-flight requests, rather than treating them as idle.
 if strict then
  for _,member in ipairs(redis.call('ZRANGE',KEYS[i+1],0,-1)) do
   if conflicts(member,true) then return 0 end
  end
 end
end
redis.call('ZADD',KEYS[1],now,ARGV[2]); redis.call('EXPIRE',KEYS[1],ttl)
for i=2,#KEYS,2 do redis.call('ZADD',KEYS[i],now,ARGV[2]); redis.call('EXPIRE',KEYS[i],ttl) end
return 1
`)

var accountProxyLeaseRefresh = redis.NewScript(`
redis.replicate_commands()
local now=tonumber(redis.call('TIME')[1]); local ttl=tonumber(ARGV[2])
for _,key in ipairs(KEYS) do
 redis.call('ZREMRANGEBYSCORE',key,'-inf',now-ttl)
 if redis.call('ZSCORE',key,ARGV[1])==false then return 0 end
end
for _,key in ipairs(KEYS) do redis.call('ZADD',key,now,ARGV[1]); redis.call('EXPIRE',key,ttl) end
return 1
`)

var accountProxyLeaseRelease = redis.NewScript(`
for _,key in ipairs(KEYS) do redis.call('ZREM',key,ARGV[1]) end
return 1
`)

func proxyReservationKey(id int64) string { return fmt.Sprintf("concurrency:proxy-owner:%d", id) }

func (c *proxyConcurrencyCache) AcquireAccountProxy(ctx context.Context, proxyID int64, limit int, lease string, accountID int64, resources []int64, strict bool) (bool, error) {
	parsedAccount, parsedResources, parsedStrict, ok := service.ParseAccountProxyLeaseID(lease)
	if !ok || parsedAccount != accountID || parsedStrict != strict || len(parsedResources) != len(resources) {
		return false, fmt.Errorf("invalid proxy reservation")
	}
	keys := []string{proxyLeaseKey(proxyID)}
	containsSelected := false
	for i, id := range parsedResources {
		if resources[i] != id {
			return false, fmt.Errorf("invalid proxy reservation resources")
		}
		if id == proxyID {
			containsSelected = true
		}
		keys = append(keys, proxyReservationKey(id), proxyLeaseKey(id))
	}
	if !containsSelected {
		return false, fmt.Errorf("proxy outside reservation")
	}
	mode := 0
	if strict {
		mode = 1
	}
	result, err := accountProxyLeaseAcquire.Run(ctx, c.client, keys, limit, lease, proxyLeaseSeconds, accountID, mode).Int()
	return result == 1, err
}

func proxyLeaseManagedKeys(proxyID int64, lease string) ([]string, bool) {
	_, resources, _, ok := service.ParseAccountProxyLeaseID(lease)
	if !ok {
		return nil, false
	}
	keys := []string{proxyLeaseKey(proxyID)}
	for _, id := range resources {
		keys = append(keys, proxyReservationKey(id))
	}
	return keys, true
}

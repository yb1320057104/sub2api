package upstreamstats

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const sharedPrefix = "upstreamstats:{responses}:"
const sharedCapacity = 65536
const sharedBatchSize = 256

// RedisStore keeps the same bounded attempt evidence as Store, with a shared
// global limit and account indexes. The response path only enqueues an event.
type RedisStore struct {
	client          *redis.Client
	nodeID          string
	seq             atomic.Uint64
	queue           chan sample
	stop            chan struct{}
	done            chan struct{}
	mu              sync.RWMutex
	closed          bool
	gap             atomic.Int64
	started         time.Time
	capacity        int
	closeOnce       sync.Once
	clientCloseOnce sync.Once
	closeErr        error
	lastWriteErr    error
	runCtx          context.Context
	cancel          context.CancelFunc
}

func NewRedis(client *redis.Client) *RedisStore {
	if client == nil {
		return nil
	}
	opts := *client.Options()
	opts.PushNotificationProcessor = nil
	if opts.MaintNotificationsConfig != nil {
		maintenance := *opts.MaintNotificationsConfig
		opts.MaintNotificationsConfig = &maintenance
	}
	opts.ContextTimeoutEnabled = true
	opts.DialTimeout = 500 * time.Millisecond
	opts.ReadTimeout = time.Second
	opts.WriteTimeout = time.Second
	opts.PoolTimeout = 500 * time.Millisecond
	opts.MaxRetries = -1
	opts.PoolSize = 4
	opts.MinIdleConns = 0
	s := &RedisStore{client: redis.NewClient(&opts), nodeID: uuid.NewString(), queue: make(chan sample, 4096), stop: make(chan struct{}), done: make(chan struct{}), started: time.Now(), capacity: sharedCapacity}
	s.runCtx, s.cancel = context.WithCancel(context.Background())
	go s.run()
	return s
}

func (s *RedisStore) markGap(at time.Time) {
	value := at.UnixMicro() + 1
	for old := s.gap.Load(); value > old; old = s.gap.Load() {
		if s.gap.CompareAndSwap(old, value) {
			return
		}
	}
}

func (s *RedisStore) Record(account int64, status int, at time.Time) {
	if s == nil || account <= 0 || status < 200 || status > 599 {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.queue <- sample{account: account, status: status, at: at}:
	default:
		s.markGap(at)
	}
}

// Every member contains account/status/nanosecond timestamp and a process-unique
// sequence. Retrying a batch uses the same members, so Redis cannot double count.
// A single script keeps the global bound and per-account indexes consistent.
var writeSharedResponses = redis.NewScript(`
local prefix = ARGV[1]
local ttl = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local observed = tonumber(redis.call('GET', KEYS[2])) or tonumber(ARGV[4])
local gap = tonumber(ARGV[5])
if gap > observed then observed = gap end
for i = 6, #ARGV, 2 do
  local member = ARGV[i]
  local score = tonumber(ARGV[i+1])
  if redis.call('ZADD', KEYS[1], 'NX', score, member) == 1 then
    local account = string.match(member, '^([^|]+)|')
    local key = prefix .. 'account:' .. account
    redis.call('ZADD', key, score, member)
    redis.call('EXPIRE', key, ttl)
  end
end
local excess = redis.call('ZCARD', KEYS[1]) - limit
if excess > 0 then
  local removed = redis.call('ZRANGE', KEYS[1], 0, excess-1, 'WITHSCORES')
  for i = 1, #removed, 2 do
    local member = removed[i]
    local account = string.match(member, '^([^|]+)|')
    redis.call('ZREM', prefix .. 'account:' .. account, member)
    redis.call('ZREM', KEYS[1], member)
    local after = tonumber(removed[i+1])+1
    if after > observed then observed = after end
  end
end
redis.call('SET', KEYS[2], string.format('%.0f', observed), 'EX', ttl)
redis.call('EXPIRE', KEYS[1], ttl)
return 1`)

func (s *RedisStore) write(ctx context.Context, batch []sample) error {
	gap := s.gap.Load()
	args := []any{sharedPrefix, int((retention + time.Hour) / time.Second), s.capacity, s.started.UnixMicro(), gap}
	for _, event := range batch {
		member := fmt.Sprintf("%d|%d|%d|%s|%d", event.account, event.status, event.at.UnixNano(), s.nodeID, s.seq.Add(1))
		args = append(args, member, event.at.UnixMicro())
	}
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		err = writeSharedResponses.Run(ctx, s.client, []string{sharedPrefix + "all", sharedPrefix + "observed"}, args...).Err()
		if err == nil || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		for _, event := range batch {
			s.markGap(event.at)
		}
	} else {
		s.gap.CompareAndSwap(gap, 0)
	}
	return err
}

func (s *RedisStore) run() {
	defer close(s.done)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	batch := make([]sample, 0, sharedBatchSize)
	flush := func() {
		if len(batch) == 0 && s.gap.Load() == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(s.runCtx, 2*time.Second)
		s.lastWriteErr = s.write(ctx, batch)
		cancel()
		batch = batch[:0]
	}
	for {
		select {
		case event := <-s.queue:
			batch = append(batch, event)
			if len(batch) == sharedBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.stop:
			for {
				select {
				case event := <-s.queue:
					batch = append(batch, event)
					if len(batch) == sharedBatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (s *RedisStore) Snapshot(ctx context.Context, ids []int64, since, now time.Time) (map[int64]Evidence, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	pipe := s.client.Pipeline()
	observedCmd := pipe.Get(ctx, sharedPrefix+"observed")
	commands := make(map[int64]*redis.StringSliceCmd, len(ids))
	cutoff := now.Add(-retention)
	start := since
	if cutoff.After(start) {
		start = cutoff
	}
	for _, id := range ids {
		if _, exists := commands[id]; !exists {
			commands[id] = pipe.ZRangeByScore(ctx, sharedPrefix+"account:"+strconv.FormatInt(id, 10), &redis.ZRangeBy{Min: strconv.FormatInt(start.UnixMicro(), 10), Max: strconv.FormatInt(now.UnixMicro(), 10)})
		}
	}
	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	observedValue, _ := observedCmd.Int64()
	observed := time.UnixMicro(observedValue)
	if observedValue == 0 || cutoff.After(observed) {
		observed = cutoff
	}
	out := make(map[int64]Evidence, len(ids))
	for id, command := range commands {
		members, err := command.Result()
		if err != nil {
			return nil, err
		}
		// Async writers and independent replica lifetimes cannot prove a fully
		// observed cluster window. Partial real samples remain valid evidence.
		e := Evidence{Source: "shared_http_responses", ObservedSince: observed, WindowComplete: false}
		for _, member := range members {
			parts := strings.SplitN(member, "|", 5)
			if len(parts) != 5 {
				continue
			}
			stamp, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil {
				continue
			}
			at := time.Unix(0, stamp)
			if at.Before(start) || at.After(now) {
				continue
			}
			status, err := strconv.Atoi(parts[1])
			if err != nil {
				continue
			}
			e.Total++
			if status >= 400 {
				e.Failures++
			}
		}
		if e.Total > 0 {
			percent := 100 * float64(e.Failures) / float64(e.Total)
			e.Percent = &percent
		}
		out[id] = e
	}
	return out, nil
}

func (s *RedisStore) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() { s.mu.Lock(); s.closed = true; close(s.stop); s.mu.Unlock() })
	select {
	case <-s.done:
		s.cancel()
		s.clientCloseOnce.Do(func() { s.closeErr = errors.Join(s.lastWriteErr, s.client.Close()) })
		return s.closeErr
	case <-ctx.Done():
		s.cancel()
		<-s.done
		s.clientCloseOnce.Do(func() { s.closeErr = errors.Join(s.lastWriteErr, s.client.Close()) })
		return ctx.Err()
	}
}

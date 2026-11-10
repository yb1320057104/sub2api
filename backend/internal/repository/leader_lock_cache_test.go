//go:build unit

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/alicebob/miniredis/v2"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGroupAntiDistillationSettingCompareAndSwap(t *testing.T) {
	const key = "group_anti_distillation:7"
	const next = `{"enabled":true,"pause_minutes":9,"user_allowlist_ids":[]}`
	previous := `{"enabled":false,"pause_minutes":5,"user_allowlist_ids":[]}`
	for _, rows := range []int64{0, 1} {
		t.Run(fmt.Sprintf("update affected %d", rows), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			mock.ExpectExec(`UPDATE "settings" SET "value" = \$1, "updated_at" = \$2 WHERE "settings"\."key" = \$3 AND "settings"\."value" = \$4`).
				WithArgs(next, sqlmock.AnyArg(), key, previous).WillReturnResult(sqlmock.NewResult(0, rows))
			ok, err := (&settingRepository{client: client}).CompareAndSwap(context.Background(), key, &previous, next)
			require.NoError(t, err)
			require.Equal(t, rows == 1, ok, "a changed or deleted row must not be recreated or overwritten")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	for _, tc := range []struct {
		name              string
		err               error
		wantOK, wantError bool
	}{
		{"new key", nil, true, false},
		{"concurrent insert", &pq.Error{Code: "23505"}, false, false},
		{"storage failure", errors.New("storage offline"), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			query := mock.ExpectQuery(`INSERT INTO "settings" \("key", "value", "updated_at"\) VALUES \(\$1, \$2, \$3\) RETURNING "id"`).
				WithArgs(key, next, sqlmock.AnyArg())
			if tc.err != nil {
				query.WillReturnError(tc.err)
			} else {
				query.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
			}
			ok, err := (&settingRepository{client: client}).CompareAndSwap(context.Background(), key, nil, next)
			require.Equal(t, tc.wantOK, ok)
			if tc.wantError {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func newLeaderLockTestCache(t *testing.T) (*leaderLockCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &leaderLockCache{rdb: rdb}, mr
}

func TestLeaderLockCache_AcquireContendedRelease(t *testing.T) {
	cache, _ := newLeaderLockTestCache(t)
	ctx := context.Background()
	const key = "dashboard:aggregation:leader"

	ok, err := cache.TryAcquireLeaderLock(ctx, key, "A", time.Minute)
	require.NoError(t, err)
	require.True(t, ok, "first owner should acquire")

	ok, err = cache.TryAcquireLeaderLock(ctx, key, "B", time.Minute)
	require.NoError(t, err)
	require.False(t, ok, "peer must be locked out while held")

	require.NoError(t, cache.ReleaseLeaderLock(ctx, key, "A"))

	ok, err = cache.TryAcquireLeaderLock(ctx, key, "B", time.Minute)
	require.NoError(t, err)
	require.True(t, ok, "peer should acquire after release")
}

// A stale owner whose lock expired and was re-acquired by a peer must not delete
// the peer's lock when its late release fires (compare-and-delete by owner).
func TestLeaderLockCache_ReleaseIsCompareAndDelete(t *testing.T) {
	cache, _ := newLeaderLockTestCache(t)
	ctx := context.Background()
	const key = "payment:order:expiry:leader"

	ok, err := cache.TryAcquireLeaderLock(ctx, key, "A", time.Minute)
	require.NoError(t, err)
	require.True(t, ok)

	// Simulate A's lock expiring and peer B taking ownership.
	require.NoError(t, cache.rdb.Set(ctx, leaderLockKeyPrefix+key, "B", time.Minute).Err())

	// A's stale release must be a no-op against B's lock.
	require.NoError(t, cache.ReleaseLeaderLock(ctx, key, "A"))

	val, err := cache.rdb.Get(ctx, leaderLockKeyPrefix+key).Result()
	require.NoError(t, err)
	require.Equal(t, "B", val, "stale owner must not delete the new owner's lock")
}

func TestLeaderLockCache_TTLExpires(t *testing.T) {
	cache, mr := newLeaderLockTestCache(t)
	ctx := context.Background()
	const key = "subscription:expiry:reminder:leader"

	ok, err := cache.TryAcquireLeaderLock(ctx, key, "A", time.Minute)
	require.NoError(t, err)
	require.True(t, ok)

	mr.FastForward(2 * time.Minute)

	ok, err = cache.TryAcquireLeaderLock(ctx, key, "B", time.Minute)
	require.NoError(t, err)
	require.True(t, ok, "lock should be re-acquirable after the TTL expires")
}

func newGroupAntiDistillationTestCache(t *testing.T) (*groupAntiDistillationCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	server.SetTime(time.Unix(1_800_000_000, 0))
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache, ok := NewGroupAntiDistillationCache(client).(*groupAntiDistillationCache)
	require.True(t, ok)
	return cache, server
}

func TestGroupAntiDistillationCacheTwentySamplesEighteenZeros(t *testing.T) {
	cache, server := newGroupAntiDistillationTestCache(t)
	ctx := context.Background()
	for i := 0; i < 19; i++ {
		triggered, _, err := cache.Observe(ctx, 7, fmt.Sprint(i), i >= 2, 5*time.Minute, time.Time{})
		require.NoError(t, err)
		require.False(t, triggered, "19 valid samples cannot trigger")
	}
	remaining, err := cache.PauseRemaining(ctx, 7)
	require.NoError(t, err)
	require.Zero(t, remaining)
	triggered, until, err := cache.Observe(ctx, 7, "last", true, 10*time.Minute, time.Time{})
	require.NoError(t, err)
	require.True(t, triggered)
	require.Equal(t, time.Unix(1_800_000_000, 0).Add(10*time.Minute), until)
	remaining, err = cache.PauseRemaining(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, 10*time.Minute, remaining, "the triggering observation determines the duration")
	remaining, err = cache.PauseRemaining(ctx, 8)
	require.NoError(t, err)
	require.Zero(t, remaining, "users are isolated")
	require.False(t, server.Exists(groupAntiDistillationRedisKeys(7)[0]), "trigger consumes the sample window")
}

func TestGroupAntiDistillationCacheWindowSlidesAndDeduplicates(t *testing.T) {
	cache, _ := newGroupAntiDistillationTestCache(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		triggered, _, err := cache.Observe(ctx, 7, fmt.Sprint(i), i >= 3, 5*time.Minute, time.Time{})
		require.NoError(t, err)
		require.False(t, triggered, "17 of 20 zeros cannot trigger")
	}
	// Replaying an earlier positive result as zero must not replace its sample.
	triggered, _, err := cache.Observe(ctx, 7, "0", true, 5*time.Minute, time.Time{})
	require.NoError(t, err)
	require.False(t, triggered)
	keys := groupAntiDistillationRedisKeys(7)
	require.EqualValues(t, 20, cache.rdb.LLen(ctx, keys[0]).Val())
	triggered, _, err = cache.Observe(ctx, 7, "20", true, 5*time.Minute, time.Time{})
	require.NoError(t, err)
	require.True(t, triggered, "the oldest positive slides out, leaving 18 of 20 zeros")
}

func TestGroupAntiDistillationCachePauseNotRenewedAndOldInflightIgnored(t *testing.T) {
	cache, server := newGroupAntiDistillationTestCache(t)
	ctx := context.Background()
	var until time.Time
	for i := 0; i < 20; i++ {
		_, observedUntil, err := cache.Observe(ctx, 7, fmt.Sprint(i), true, time.Minute, time.Time{})
		require.NoError(t, err)
		until = observedUntil
	}
	keys := groupAntiDistillationRedisKeys(7)
	firstTTL := server.TTL(keys[2])
	for i := 0; i < 30; i++ {
		triggered, _, err := cache.Observe(ctx, 7, fmt.Sprint(20+i), true, 10*time.Minute, time.Time{})
		require.NoError(t, err)
		require.False(t, triggered)
	}
	require.Equal(t, firstTTL, server.TTL(keys[2]), "an active pause never renews")
	require.False(t, server.Exists(keys[0]), "in-flight completions do not accumulate during a pause")
	server.FastForward(time.Minute + time.Second)
	server.SetTime(until.Add(time.Second))
	remaining, err := cache.PauseRemaining(ctx, 7)
	require.NoError(t, err)
	require.Zero(t, remaining)
	for i := 0; i < 30; i++ {
		triggered, _, err := cache.Observe(ctx, 7, fmt.Sprint(100+i), true, time.Minute, until.Add(-time.Millisecond))
		require.NoError(t, err)
		require.False(t, triggered)
	}
	require.False(t, server.Exists(keys[0]), "late completions from before the pause ended remain excluded")
	triggered, _, err := cache.Observe(ctx, 7, "new-request", true, time.Minute, until.Add(time.Millisecond))
	require.NoError(t, err)
	require.False(t, triggered)
	require.EqualValues(t, 1, cache.rdb.LLen(ctx, keys[0]).Val(), "recovery starts a fresh window")
}

func TestGroupAntiDistillationCacheConcurrentTriggerIsAtomic(t *testing.T) {
	cache, _ := newGroupAntiDistillationTestCache(t)
	ctx := context.Background()
	var triggers atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			triggered, _, err := cache.Observe(ctx, 7, fmt.Sprint(i), true, 5*time.Minute, time.Time{})
			if err != nil {
				t.Error(err)
				return
			}
			if triggered {
				triggers.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.EqualValues(t, 1, triggers.Load(), "exactly one atomic trigger owns notification eligibility")
}

func TestGroupAntiDistillationCacheBoundsStateAndHashesRequestIDs(t *testing.T) {
	cache, server := newGroupAntiDistillationTestCache(t)
	ctx := context.Background()
	keys := groupAntiDistillationRedisKeys(7)
	for _, key := range keys {
		require.Contains(t, key, "{7}", "all Lua keys must share a Redis cluster hash slot")
	}
	const requestID = "private-request-reference"
	triggered, _, err := cache.Observe(ctx, 7, requestID, false, time.Minute, time.Time{})
	require.NoError(t, err)
	require.False(t, triggered)
	digest := sha256.Sum256([]byte(requestID))
	members, err := cache.rdb.ZRange(ctx, keys[1], 0, -1).Result()
	require.NoError(t, err)
	require.Equal(t, []string{hex.EncodeToString(digest[:])}, members)
	for i := 0; i < 300; i++ {
		_, _, err := cache.Observe(ctx, 7, fmt.Sprint(i), false, time.Minute, time.Time{})
		require.NoError(t, err)
	}
	require.EqualValues(t, 20, cache.rdb.LLen(ctx, keys[0]).Val())
	require.EqualValues(t, 256, cache.rdb.ZCard(ctx, keys[1]).Val())
	require.Equal(t, 24*time.Hour, server.TTL(keys[0]))
	require.Equal(t, 24*time.Hour, server.TTL(keys[1]))
	server.FastForward(24*time.Hour + time.Second)
	require.False(t, server.Exists(keys[0]))
	require.False(t, server.Exists(keys[1]))
}

func TestGroupAntiDistillationCacheFailuresNeverEstablishPause(t *testing.T) {
	cache, server := newGroupAntiDistillationTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.rdb.Close())
	remaining, err := cache.PauseRemaining(ctx, 7)
	require.Error(t, err)
	require.Zero(t, remaining)
	triggered, until, err := cache.Observe(ctx, 7, "request", true, time.Minute, time.Time{})
	require.Error(t, err)
	require.False(t, triggered)
	require.True(t, until.IsZero())
	require.Empty(t, server.Keys())
	unavailable := NewGroupAntiDistillationCache(nil)
	remaining, err = unavailable.PauseRemaining(ctx, 7)
	require.Error(t, err)
	require.Zero(t, remaining)
	triggered, until, err = unavailable.Observe(ctx, 7, "request", true, time.Minute, time.Time{})
	require.Error(t, err)
	require.False(t, triggered)
	require.True(t, until.IsZero())
}

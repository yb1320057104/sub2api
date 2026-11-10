package upstreamstats

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestResponseWindow(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	s := NewStore(8, now.Add(-2*time.Hour))
	since := now.Add(-time.Hour)
	if got := s.Snapshot([]int64{1}, since, now)[1]; got.Percent != nil || got.Total != 0 {
		t.Fatalf("unknown: %+v", got)
	}
	s.Record(1, 200, since.Add(-time.Nanosecond))
	s.Record(1, 503, since)
	s.Record(1, 200, now)
	s.Record(1, 500, now.Add(time.Nanosecond))
	got := s.Snapshot([]int64{1, 2}, since, now)
	if got[1].Percent == nil || *got[1].Percent != 50 || got[1].Failures != 1 || got[1].Total != 2 || !got[1].WindowComplete {
		t.Fatalf("failed retry then success must remain two attempts: %+v", got[1])
	}
	if got[2].Percent != nil {
		t.Fatal("unseen account must be unknown")
	}
	s.Record(2, 200, now)
	if got := s.Snapshot([]int64{2}, since, now)[2]; got.Percent == nil || *got.Percent != 0 {
		t.Fatalf("measured success: %+v", got)
	}
}

func TestResponseConcurrentBatch(t *testing.T) {
	now := time.Now()
	s := NewStore(1000, now.Add(-time.Hour))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				s.Record(1, 500, now)
				s.Snapshot([]int64{1, 2}, now.Add(-time.Minute), now)
			}
		})
	}
	wg.Wait()
	got := s.Snapshot([]int64{1, 2}, now.Add(-time.Minute), now)
	if got[1].Total != 800 || got[1].Failures != 800 || got[2].Percent != nil {
		t.Fatalf("concurrent evidence: %+v", got)
	}
}

func TestResponseWindowRestartRetentionAndRefresh(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	s := NewStore(2, now.Add(-time.Minute))
	s.Record(1, 500, now.Add(-time.Second))
	s.Record(1, 200, now)
	s.Record(1, 200, now)
	got := s.Snapshot([]int64{1}, now.Add(-time.Hour), now)[1]
	if got.WindowComplete || !got.ObservedSince.After(now.Add(-time.Second)) || got.Total != 2 {
		t.Fatalf("retention must disclose incomplete history: %+v", got)
	}
	if got := s.Snapshot([]int64{1}, now.Add(time.Minute), now.Add(2*time.Minute))[1]; got.Percent != nil {
		t.Fatal("refresh must expire samples")
	}
	if got := NewStore(2, now).Snapshot([]int64{1}, now.Add(-time.Hour), now)[1]; got.Percent != nil || got.WindowComplete {
		t.Fatal("restart must be unknown")
	}
}

func TestResponseLongWindowAndCapacityValidity(t *testing.T) {
	now := time.Now()
	s := NewStore(8, now.Add(-48*time.Hour))
	s.Record(1, 503, now.Add(-90*time.Minute))
	s.Record(1, 200, now)
	got := s.Snapshot([]int64{1}, now.Add(-120*time.Minute), now)[1]
	if got.Total != 2 || got.Percent == nil || *got.Percent != 50 || !got.WindowComplete {
		t.Fatalf("120-minute window must retain evidence beyond 60m: %+v", got)
	}
	s.Record(1, 500, now.Add(-25*time.Hour))
	got = s.Snapshot([]int64{1}, now.Add(-24*time.Hour), now)[1]
	if got.Total != 2 || !got.WindowComplete {
		t.Fatalf("maximum supported window excludes expired evidence: %+v", got)
	}
	if s.Snapshot([]int64{1}, now.Add(-25*time.Hour), now)[1].WindowComplete {
		t.Fatal("unsupported history must not be complete")
	}
	bounded := NewStore(1, now.Add(-48*time.Hour))
	bounded.Record(1, 500, now.Add(-90*time.Minute))
	bounded.Record(1, 200, now)
	if bounded.Snapshot([]int64{1}, now.Add(-120*time.Minute), now)[1].WindowComplete {
		t.Fatal("eviction must invalidate affected windows")
	}
}

func TestSharedResponseReplicasExactWindowAndDrain(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	first, second, reader := NewRedis(client), NewRedis(client), NewRedis(client)
	t.Cleanup(func() {
		_ = first.Close(context.Background())
		_ = second.Close(context.Background())
		_ = reader.Close(context.Background())
	})
	now := time.Now().Truncate(time.Microsecond)
	since := now.Add(-time.Minute)
	first.Record(7, 503, since)
	first.Record(7, 500, since.Add(-time.Nanosecond))
	second.Record(7, 200, now)
	second.Record(7, 500, now.Add(time.Nanosecond))
	second.Record(8, 200, now)
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("close must be idempotent: %v", err)
	}
	data, err := reader.Snapshot(context.Background(), []int64{7, 8, 9}, since, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := data[7]; got.Total != 2 || got.Failures != 1 || got.Percent == nil || *got.Percent != 50 || got.Source != "shared_http_responses" || got.WindowComplete {
		t.Fatalf("reader must combine both replica attempts, exact inclusive boundaries and partial coverage: %+v", got)
	}
	if got := data[8]; got.Total != 1 || got.Percent == nil || *got.Percent != 0 {
		t.Fatalf("observed zero: %+v", got)
	}
	if got := data[9]; got.Total != 0 || got.Percent != nil {
		t.Fatalf("missing samples remain unknown: %+v", got)
	}
}

func TestSharedResponseRetryDedupAndGlobalBound(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	now := time.Now().Truncate(time.Microsecond)
	store := &RedisStore{client: client, nodeID: "test", capacity: 3, started: now.Add(-time.Hour)}
	ctx := context.Background()
	if err := store.write(ctx, []sample{{7, 503, now.Add(-time.Second)}, {8, 200, now}, {7, 200, now}, {9, 500, now}}); err != nil {
		t.Fatal(err)
	}
	members, err := client.ZRangeWithScores(ctx, sharedPrefix+"all", 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 3 {
		t.Fatalf("shared global capacity not enforced: %d", len(members))
	}
	// Replaying the exact acknowledged batch models a lost response followed
	// by retry; a native 503 then 200 remain distinct unique attempts.
	args := []any{sharedPrefix, 90000, 3, store.started.UnixMicro(), 0}
	for _, member := range members {
		args = append(args, member.Member, member.Score)
	}
	if err := writeSharedResponses.Run(ctx, client, []string{sharedPrefix + "all", sharedPrefix + "observed"}, args...).Err(); err != nil {
		t.Fatal(err)
	}
	data, err := store.Snapshot(ctx, []int64{7, 8, 9}, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	total := int64(0)
	for _, got := range data {
		total += got.Total
		if got.WindowComplete || !got.ObservedSince.After(now.Add(-time.Second)) {
			t.Fatalf("eviction must mark incomplete: %+v", got)
		}
	}
	if total != 3 || data[7].Total != 1 || data[7].Failures != 0 {
		t.Fatalf("duplicate replay or dangling account index: %+v", data)
	}
	server.FastForward(26 * time.Hour)
	data, err = store.Snapshot(ctx, []int64{7}, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	if data[7].Percent != nil {
		t.Fatalf("expired shared evidence must be unknown: %+v", data[7])
	}
}

func TestSharedResponseRecordNeverWaitsForStorage(t *testing.T) {
	store := &RedisStore{queue: make(chan sample, 1)}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := 0; i < 10000; i++ {
			store.Record(7, 503, time.Now())
		}
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("full evidence queue blocked the response path")
	}
	if len(store.queue) != 1 || store.gap.Load() == 0 {
		t.Fatal("overflow must remain bounded and disclose a gap")
	}
}

type unavailableSharedEvidence struct{}

func (unavailableSharedEvidence) Record(int64, int, time.Time) {}
func (unavailableSharedEvidence) Snapshot(context.Context, []int64, time.Time, time.Time) (map[int64]Evidence, error) {
	return nil, errors.New("redis unavailable")
}
func (unavailableSharedEvidence) Close(context.Context) error { return nil }

func TestSharedResponseFailureNeverFallsBackToLocalEvidence(t *testing.T) {
	previous := Default
	Default = NewStore(4, time.Now().Add(-time.Hour))
	t.Cleanup(func() { ConfigureShared(nil); Default = previous })
	now := time.Now()
	Default.Record(7, 200, now)
	ConfigureShared(unavailableSharedEvidence{})
	got := Snapshot(context.Background(), []int64{7}, now.Add(-time.Minute), now)[7]
	if got.Percent != nil || got.Total != 0 || got.Source != "shared_http_responses_unavailable" || got.WindowComplete {
		t.Fatalf("shared failure must be unknown, without local fallback: %+v", got)
	}
}

func TestSharedResponseCloseReportsUnavailableStorage(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	writer := NewRedis(client)
	server.Close()
	writer.Record(7, 503, time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(ctx); err == nil {
		t.Fatal("shutdown must report an uncommitted final batch")
	}
	select {
	case <-writer.done:
	default:
		t.Fatal("shutdown must stop the writer before returning")
	}
	writer.Record(7, 200, time.Now())
	if len(writer.queue) != 0 {
		t.Fatal("closed writer must reject new samples")
	}
}

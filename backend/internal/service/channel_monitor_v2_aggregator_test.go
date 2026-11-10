//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2MaxChunkForDepth(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	// Within last day → tightest ceiling (2h).
	require.Equal(t, channelMonitorV2MaxChunkNear1d, channelMonitorV2MaxChunkForDepth(now, now.Add(-2*time.Hour)))
	// Between 1d and 7d → 4h.
	require.Equal(t, channelMonitorV2MaxChunkNear7d, channelMonitorV2MaxChunkForDepth(now, now.Add(-2*24*time.Hour)))
	// Older than 7d → 6h (never 24h default).
	require.Equal(t, channelMonitorV2MaxChunkFar, channelMonitorV2MaxChunkForDepth(now, now.Add(-10*24*time.Hour)))
	require.Less(t, channelMonitorV2MaxChunkFar, 24*time.Hour)
	require.Equal(t, time.Hour, channelMonitorV2BackfillChunkInit)
	require.Equal(t, 15*time.Minute, channelMonitorV2MinBackfillChunk)
}

func TestChannelMonitorV2AggregatorAdaptiveChunk(t *testing.T) {
	s := NewChannelMonitorV2Aggregator(nil, nil, nil)
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	cursor := now.Add(-3 * time.Hour)

	// Failure shrinks chunk and sets backoff floor.
	s.backfillChunk = 2 * time.Hour
	s.recordBackfillFailure(now, cursor)
	require.Equal(t, time.Hour, s.backfillChunk)
	require.Equal(t, time.Minute, s.nextWaitFloor)
	require.Equal(t, 1, s.backfillFailures)

	// Repeated failure halves again and raises floor.
	s.recordBackfillFailure(now, cursor)
	require.Equal(t, 30*time.Minute, s.backfillChunk)
	require.Equal(t, 2*time.Minute, s.nextWaitFloor)

	// Fast success grows within depth ceiling and clears backoff.
	s.recordBackfillSuccess(cursor.Add(-30*time.Minute), 5*time.Second, now)
	require.Equal(t, 0, s.backfillFailures)
	require.Equal(t, time.Duration(0), s.nextWaitFloor)
	require.Greater(t, s.backfillChunk, 30*time.Minute)
	require.LessOrEqual(t, s.backfillChunk, channelMonitorV2MaxChunkForDepth(now, cursor.Add(-30*time.Minute)))
}

type channelMonitorV2RecoveryRange struct{ start, end time.Time }

type channelMonitorV2RecoveryRepo struct {
	ChannelMonitorV2Repository
	watermark ChannelMonitorV2AggregationWatermark
	ranges    []channelMonitorV2RecoveryRange
	failNext  bool
}

func (r *channelMonitorV2RecoveryRepo) GetAggregationWatermark(context.Context) (*ChannelMonitorV2AggregationWatermark, error) {
	watermark := r.watermark
	return &watermark, nil
}

func (r *channelMonitorV2RecoveryRepo) RecomputeRange(_ context.Context, start, end time.Time) error {
	r.ranges = append(r.ranges, channelMonitorV2RecoveryRange{start, end})
	if r.failNext {
		r.failNext = false
		return errors.New("aggregation transaction rolled back")
	}
	// Match the repository transaction: only a committed recompute can advance
	// data_through, and a recent rebuild cannot reset the oldest backfill cursor.
	if end.After(r.watermark.DataThrough) {
		r.watermark.DataThrough = end
	}
	if r.watermark.BackfillCursor.IsZero() || start.Before(r.watermark.BackfillCursor) {
		r.watermark.BackfillCursor = start
	}
	r.watermark.HasData = true
	return nil
}

func TestChannelMonitorV2AggregatorResumesOutageAcrossRestarts(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)
	previous := now.Add(-90 * time.Minute)
	history := now.Add(-30 * 24 * time.Hour)
	repo := &channelMonitorV2RecoveryRepo{watermark: ChannelMonitorV2AggregationWatermark{
		DataThrough: previous, BackfillCursor: history, HasData: true,
	}}

	NewChannelMonitorV2Aggregator(repo, nil, nil).runOnceAt(now)
	require.Len(t, repo.ranges, 1, "old history waits until the recent gap is filled")
	first := repo.ranges[0]
	require.Equal(t, previous.Add(-channelMonitorV2RecentOverlap), first.start)
	require.Equal(t, first.start.Add(time.Hour), first.end)
	require.Equal(t, first.end, repo.watermark.DataThrough)
	require.Equal(t, history, repo.watermark.BackfillCursor)

	// A second process must continue the committed partial recovery, rather
	// than trusting the original outage endpoint or skipping to now minus 10m.
	NewChannelMonitorV2Aggregator(repo, nil, nil).runOnceAt(now)
	require.Len(t, repo.ranges, 3)
	second := repo.ranges[1]
	require.Equal(t, first.end.Add(-channelMonitorV2RecentOverlap), second.start)
	require.Equal(t, now, second.end)
	require.False(t, second.start.After(first.end), "recovery has no unaggregated gap")
	require.Equal(t, history, repo.ranges[2].end, "historical work resumes only after catch-up")
	require.Equal(t, now, repo.watermark.DataThrough)
}

func TestChannelMonitorV2AggregatorRetriesFailedRecoveryWithoutAdvancing(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)
	previous := now.Add(-90 * time.Minute)
	repo := &channelMonitorV2RecoveryRepo{watermark: ChannelMonitorV2AggregationWatermark{
		DataThrough: previous, BackfillCursor: now.Add(-30 * 24 * time.Hour), HasData: true,
	}, failNext: true}
	svc := NewChannelMonitorV2Aggregator(repo, nil, nil)
	svc.runOnceAt(now)
	require.Len(t, repo.ranges, 1)
	require.Equal(t, previous, repo.watermark.DataThrough)

	svc.runOnceAt(now)
	require.Len(t, repo.ranges, 2)
	require.Equal(t, repo.ranges[0], repo.ranges[1], "failed minutes must be retried")
	require.Equal(t, repo.ranges[1].end, repo.watermark.DataThrough)
}

func TestChannelMonitorV2AggregatorHealthyTickKeepsShortOverlap(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)
	repo := &channelMonitorV2RecoveryRepo{watermark: ChannelMonitorV2AggregationWatermark{
		DataThrough: now.Add(-2 * time.Minute), BackfillCursor: now.Add(-30 * 24 * time.Hour), HasData: true,
	}}
	svc := NewChannelMonitorV2Aggregator(repo, nil, nil)
	svc.runOnceAt(now)
	require.Len(t, repo.ranges, 2)
	require.Equal(t, channelMonitorV2RecoveryRange{now.Add(-10 * time.Minute), now}, repo.ranges[0])

	// Refresh the durable endpoint each tick even when the historical cursor is
	// already loaded: a peer may have committed while this instance was idle.
	next := now.Add(2 * time.Hour)
	repo.watermark.DataThrough = next.Add(-time.Minute)
	svc.runOnceAt(next)
	require.Equal(t, channelMonitorV2RecoveryRange{next.Add(-10 * time.Minute), next}, repo.ranges[2])
}

func TestChannelMonitorV2AggregatorRecoveryClampsToRetention(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)
	repo := &channelMonitorV2RecoveryRepo{watermark: ChannelMonitorV2AggregationWatermark{
		DataThrough: now.Add(-100 * 24 * time.Hour), BackfillCursor: now.Add(-110 * 24 * time.Hour), HasData: true,
	}}
	NewChannelMonitorV2Aggregator(repo, nil, nil).runOnceAt(now)
	require.Len(t, repo.ranges, 1)
	require.Equal(t, now.Add(-channelMonitorV2RetentionMax), repo.ranges[0].start)
	require.Equal(t, repo.ranges[0].start.Truncate(24*time.Hour).Add(24*time.Hour), repo.ranges[0].end)
	NewChannelMonitorV2Aggregator(repo, nil, nil).runOnceAt(now)
	require.Equal(t, repo.ranges[0].end, repo.ranges[1].start)
	require.Equal(t, 24*time.Hour, repo.ranges[1].end.Sub(repo.ranges[1].start))
}

func TestChannelMonitorV2AggregatorRecoveryRetainedRollupDays(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)
	previous := time.Date(2026, 11, 4, 12, 34, 0, 0, time.UTC)
	repo := &channelMonitorV2RecoveryRepo{watermark: ChannelMonitorV2AggregationWatermark{
		DataThrough: previous, BackfillCursor: now.Add(-100 * 24 * time.Hour), HasData: true,
	}, failNext: true}
	NewChannelMonitorV2Aggregator(repo, nil, nil).runOnceAt(now)
	require.Equal(t, previous, repo.watermark.DataThrough, "failed day must not advance")
	firstDay := previous.Truncate(24 * time.Hour)
	for i := 0; i < 4; i++ {
		NewChannelMonitorV2Aggregator(repo, nil, nil).runOnceAt(now)
		got := repo.ranges[i+1]
		wantStart := firstDay.Add(time.Duration(i) * 24 * time.Hour)
		require.Equal(t, channelMonitorV2RecoveryRange{wantStart, wantStart.Add(24 * time.Hour)}, got)
		require.True(t, got.end.After(previous), "midnight restart must make progress")
		previous = got.end
	}
	require.Equal(t, repo.ranges[0], repo.ranges[1], "restart retries the same failed day")
	// Nov 7 begins before the user-fact cutoff, so it was fully rebuilt even
	// though its endpoint may be newer than the cutoff. Returning to hourly
	// recovery must not overwrite that day's rollups from a ten-minute tail.
	NewChannelMonitorV2Aggregator(repo, nil, nil).runOnceAt(now)
	last := repo.ranges[len(repo.ranges)-1]
	require.Equal(t, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC), last.start)
	require.Equal(t, last.start.Add(time.Hour), last.end)
}

func TestChannelMonitorV2AggregatorRecoveryWindowBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)
	for _, age := range []time.Duration{3*24*time.Hour - time.Minute, 3 * 24 * time.Hour, 3*24*time.Hour + time.Minute, 7*24*time.Hour - time.Minute, 7*24*time.Hour + time.Minute} {
		d := now.Add(-age)
		start, end := channelMonitorV2RecoveryWindow(now.In(time.FixedZone("+08", 8*60*60)), d)
		require.Equal(t, d.Truncate(24*time.Hour), start)
		require.Equal(t, start.Add(24*time.Hour), end)
		require.True(t, end.After(d))
	}
	midnight := now.Truncate(24 * time.Hour)
	d := midnight.Add(-ChannelMonitorV2MinFactRetention)
	start, end := channelMonitorV2RecoveryWindow(midnight, d)
	require.Equal(t, d, start, "equal-cutoff midnight must not overlap the pruned prior day")
	require.Equal(t, d.Add(time.Hour), end)
	start, end = channelMonitorV2RecoveryWindow(now, time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC))
	require.Equal(t, time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC), start)
	require.Equal(t, start.Add(24*time.Hour), end)
}

func TestChannelMonitorV2AggregatorEmptyWatermarkKeepsBootstrap(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)
	repo := &channelMonitorV2RecoveryRepo{}
	svc := NewChannelMonitorV2Aggregator(repo, nil, nil)
	svc.runOnceAt(now)
	require.Equal(t, []channelMonitorV2RecoveryRange{{now.Add(-2 * time.Hour), now}}, repo.ranges)
	require.Equal(t, now, repo.watermark.DataThrough)
	svc.runOnceAt(now.Add(time.Minute))
	require.Equal(t, channelMonitorV2RecoveryRange{now.Add(-9 * time.Minute), now.Add(time.Minute)}, repo.ranges[1])
}

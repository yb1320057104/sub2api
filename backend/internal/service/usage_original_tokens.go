package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"

// originalUsageSnapshot copies disjoint upstream token buckets before any billing
// conversion. Only counters are retained; prompts and responses are never stored.
func originalUsageSnapshot(tokens UsageTokens) *usagestats.OriginalTokenUsage {
	return &usagestats.OriginalTokenUsage{
		InputTokens:         int64(max(tokens.InputTokens, 0)),
		OutputTokens:        int64(max(tokens.OutputTokens, 0)),
		CacheCreationTokens: int64(max(tokens.CacheCreationTokens, 0)),
		CacheReadTokens:     int64(max(tokens.CacheReadTokens, 0)),
	}
}

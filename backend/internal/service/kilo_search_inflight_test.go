package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestKiloSearchInflightReservesModelessAndModelBatchesAtSettlementPrice(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	groupID := int64(75)
	price := 0.025
	key := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, RateMultiplier: 2, WebSearchPricePerCall: &price}}
	deps := inflightEstimateDeps{billing: billing, userGroupRate: func(context.Context, int64, int64, float64) float64 { return 3 }}
	for _, model := range []string{"", "gpt-6.1-sol"} {
		for _, calls := range []int{1, 4} {
			cost, ok := deps.estimate(context.Background(), key, InflightEstimateRequest{Model: model, Kind: InflightEstimateWebSearch, SearchCalls: calls})
			require.True(t, ok)
			require.InDelta(t, billing.CalculateWebSearchCost(calls, &price, 3).ActualCost, cost, 1e-12)
		}
	}
	price = 0
	cost, ok := deps.estimate(context.Background(), key, InflightEstimateRequest{Kind: InflightEstimateWebSearch, SearchCalls: 4})
	require.True(t, ok)
	require.Zero(t, cost, "explicit free search is known zero pricing")
	_, ok = deps.estimate(context.Background(), key, InflightEstimateRequest{Kind: InflightEstimateWebSearch})
	require.False(t, ok, "no actual query count must not be treated as a priced search")
}

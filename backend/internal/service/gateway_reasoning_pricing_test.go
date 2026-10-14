//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecordUsage_ReasoningPricingUsesForwardedEffort(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			subRepo := &openAIRecordUsageSubRepoStub{}
			groupID := int64(77)
			inputPrice, outputPrice := 0.001, 0.002
			requested, forwarded := "max", "high"
			model := "gpt-5.4"
			if platform == PlatformAnthropic {
				model = "claude-fable-5-1"
			}
			apiKey := &APIKey{
				ID: 10, GroupID: &groupID,
				Group: &Group{
					ID: groupID, Platform: platform, Status: StatusActive, Hydrated: true, RateMultiplier: 0.5,
					ModelPricing: []ChannelModelPricing{{
						Models: []string{model}, BillingMode: BillingModeToken,
						InputPrice: &inputPrice, OutputPrice: &outputPrice,
						ReasoningEffortMultipliers: map[string]float64{"high": 1.5, "max": 3},
					}},
				},
			}
			account := &Account{ID: 30, Platform: platform, Type: AccountTypeAPIKey}
			user := &User{ID: 20}
			if platform == PlatformAnthropic {
				svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)
				svc.resolver = NewModelPricingResolver(nil, svc.billingService)
				require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{
					Result: &ForwardResult{
						RequestID: "reasoning_pricing", Model: model, Duration: time.Second,
						Usage:           ClaudeUsage{InputTokens: 100, OutputTokens: 50},
						ReasoningEffort: &forwarded, RequestedReasoningEffort: &requested,
					},
					APIKey: apiKey, User: user, Account: account,
				}))
			} else {
				svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
				svc.resolver = NewModelPricingResolver(nil, svc.billingService)
				require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{
						RequestID: "reasoning_pricing", Model: model, Duration: time.Second,
						Usage:           OpenAIUsage{InputTokens: 100, OutputTokens: 50},
						ReasoningEffort: &forwarded, RequestedReasoningEffort: &requested,
					},
					APIKey: apiKey, User: user, Account: account,
				}))
			}
			require.NotNil(t, usageRepo.lastLog)
			require.InDelta(t, 0.3, usageRepo.lastLog.TotalCost, 1e-12)
			require.InDelta(t, 0.15, usageRepo.lastLog.ActualCost, 1e-12)
			require.Equal(t, forwarded, *usageRepo.lastLog.ReasoningEffort)
			require.Equal(t, requested, *usageRepo.lastLog.RequestedReasoningEffort)
			require.InDelta(t, 0.15, userRepo.lastAmount, 1e-12)
		})
	}
}

func TestPKKiloRecordUsageBillsRequestedEffort(t *testing.T) {
	for _, mode := range []BillingMode{BillingModeToken, BillingModePerRequest} {
		for _, billingPath := range []struct {
			name         string
			subscription bool
			atomic       bool
		}{
			{"balance", false, false}, {"subscription", true, false},
			{"atomic_balance", false, true}, {"atomic_subscription", true, true},
		} {
			for _, tc := range []struct {
				name       string
				requested  string
				forwarded  string
				multiplier float64
			}{
				{"ultra", "ultra", "max", 4},
				{"persistent", "persistent", "max", 5},
				{"policy_downgrade", "ultra", "high", 4},
				{"max", "max", "max", 3},
				{"unconfigured", "low", "low", 1},
				{"missing_requested", "", "max", 3},
			} {
				name := string(mode) + "/" + billingPath.name + "/" + tc.name
				t.Run(name, func(t *testing.T) {
					usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
					userRepo := &openAIRecordUsageUserRepoStub{}
					subRepo := &openAIRecordUsageSubRepoStub{}
					quota := &openAIRecordUsageAPIKeyQuotaStub{}
					svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
					billingRepo := &openAIRecordUsageBillingRepoStub{}
					if billingPath.atomic {
						svc.usageBillingRepo = billingRepo
					}
					groupID := int64(77)
					model := "kilo-user-model"
					pricing := ChannelModelPricing{
						Models: []string{model}, BillingMode: mode,
						InputPrice: testPtrFloat64(0.001), OutputPrice: testPtrFloat64(0.002),
						CacheReadPrice: testPtrFloat64(0.0001), PerRequestPrice: testPtrFloat64(0.2),
						ReasoningEffortMultipliers: map[string]float64{"high": 1.5, "max": 3, "ultra": 4, "persistent": 5},
					}
					cache := newEmptyChannelCache()
					cache.channelByGroupID[groupID] = &Channel{ID: 1, Status: StatusActive,
						AccountStatsPricingRules: []AccountStatsPricingRule{{AccountIDs: []int64{30}, Pricing: []ChannelModelPricing{pricing}}},
					}
					cache.groupPlatform[groupID] = PlatformOpenAI
					cache.loadedAt = time.Now()
					svc.channelService = &ChannelService{}
					svc.channelService.cache.Store(cache)
					svc.resolver = NewModelPricingResolver(svc.channelService, svc.billingService)
					apiKey := &APIKey{ID: 10, GroupID: &groupID, Quota: 10,
						Group: &Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
							RateMultiplier: 0.5, ModelPricing: []ChannelModelPricing{pricing}},
					}
					var subscription *UserSubscription
					if billingPath.subscription {
						apiKey.Group.SubscriptionType = SubscriptionTypeSubscription
						subscription = &UserSubscription{ID: 40}
					}
					account := &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
						Extra: map[string]any{AccountExtraKiloBridgeEnabled: true, AccountExtraCacheConversionEnabled: true, AccountExtraCacheConversionMultiplier: 0.5},
					}
					result := &OpenAIForwardResult{RequestID: "kilo_" + name, Model: model, UpstreamModel: model,
						Usage:           OpenAIUsage{InputTokens: 130, OutputTokens: 50, CacheCreationInputTokens: 20, CacheReadInputTokens: 10},
						ReasoningEffort: &tc.forwarded,
					}
					if tc.requested != "" {
						result.RequestedReasoningEffort = &tc.requested
					}
					require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
						Result: result, APIKey: apiKey, User: &User{ID: 20}, Account: account,
						Subscription: subscription, APIKeyService: quota,
					}))
					base := 0.202 // 100 input + 50 output + 20 converted cache-read tokens.
					if mode == BillingModePerRequest {
						base = 0.2
					}
					wantTotal, wantActual := base*tc.multiplier, base*tc.multiplier*0.5
					require.NotNil(t, usageRepo.lastLog)
					log := usageRepo.lastLog
					require.InDelta(t, wantTotal, log.TotalCost, 1e-12)
					require.InDelta(t, wantActual, log.ActualCost, 1e-12)
					require.Equal(t, tc.forwarded, *log.ReasoningEffort)
					wantRequested := tc.requested
					if wantRequested == "" {
						wantRequested = tc.forwarded
					}
					require.Equal(t, wantRequested, *log.RequestedReasoningEffort)
					require.Equal(t, tc.forwarded, *result.ReasoningEffort, "pricing must not mutate forwarding evidence")
					require.Equal(t, 100, log.InputTokens)
					require.Equal(t, 20, log.CacheReadTokens)
					require.Zero(t, log.CacheCreationTokens)
					if billingPath.atomic {
						require.Equal(t, 1, billingRepo.calls)
						require.InDelta(t, wantActual, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
						require.Equal(t, tc.forwarded, billingRepo.lastCmd.ReasoningEffort)
						if billingPath.subscription {
							require.Zero(t, billingRepo.lastCmd.BalanceCost)
							require.InDelta(t, wantActual, billingRepo.lastCmd.SubscriptionCost, 1e-12)
						} else {
							require.InDelta(t, wantActual, billingRepo.lastCmd.BalanceCost, 1e-12)
							require.Zero(t, billingRepo.lastCmd.SubscriptionCost)
						}
						require.Zero(t, userRepo.deductCalls)
						require.Zero(t, subRepo.incrementCalls)
					} else {
						require.InDelta(t, wantActual, quota.lastAmount, 1e-12)
						require.Equal(t, 1, quota.quotaCalls)
					}
					if !billingPath.atomic && billingPath.subscription {
						require.Zero(t, userRepo.deductCalls)
						require.Equal(t, 1, subRepo.incrementCalls)
						require.InDelta(t, wantActual, subRepo.lastAmount, 1e-12)
					} else if !billingPath.atomic {
						require.Equal(t, 1, userRepo.deductCalls)
						require.InDelta(t, wantActual, userRepo.lastAmount, 1e-12)
					}
					// Separate account cost rules still use raw usage and the actual
					// upstream effort; customer pricing does not rewrite provider cost.
					wantAccountCost := tryCustomRules(cache.channelByGroupID[groupID], account.ID, groupID,
						PlatformOpenAI, model, UsageTokens{InputTokens: 100, OutputTokens: 50, CacheCreationTokens: 20, CacheReadTokens: 10}, 1, tc.forwarded)
					require.NotNil(t, wantAccountCost)
					require.NotNil(t, log.AccountStatsCost)
					require.InDelta(t, *wantAccountCost, *log.AccountStatsCost, 1e-12)
				})
			}
		}
	}
}

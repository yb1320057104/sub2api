//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAISubscriptionLongContextExempt(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, tt := range []struct {
		name    string
		account *Account
		result  *OpenAIForwardResult
		want    bool
	}{
		{name: "空账号", result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}},
		{name: "空结果", account: oauth},
		{name: "订阅", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}, want: true},
		{name: "别名", account: oauth, result: &OpenAIForwardResult{Model: "openai/GPT_6.1_SOL-max"}, want: true},
		{name: "压缩", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6.1-sol-openai-compact"}, want: true},
		{name: "映射到目标", account: oauth, result: &OpenAIForwardResult{Model: "custom", UpstreamModel: "gpt-6.1-sol"}, want: true},
		{name: "映射离开目标", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6.1-sol", UpstreamModel: "gpt-6-sol"}},
		{name: "仅计费标签相同", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6-sol", BillingModel: "gpt-6.1-sol"}},
		{name: "仅响应标签相同", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6-sol", UpstreamResponseModel: "gpt-6.1-sol"}},
		{name: "其他模型", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6-astra"}},
		{name: "相似名称", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6.1-sol-preview"}},
		{name: "API账号", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}},
		{name: "其他平台", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}},
		{name: "订阅访问令牌", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken}, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openAISubscriptionLongContextExempt(tt.account, tt.result))
		})
	}
}

// 同时覆盖远程目录更新、静态兜底、旧计费入口和分组入口，校验真实扣款与账号成本。
func TestOpenAIGatewayServiceRecordUsage_GPT61SolSubscriptionLongContext(t *testing.T) {
	for _, catalog := range []bool{false, true} {
		for _, groupMode := range []string{"无分组", "分组关闭", "分组开启"} {
			for _, accountEnabled := range []bool{false, true} {
				for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
					for _, tier := range []string{"", "priority"} {
						name := fmt.Sprintf("目录=%t/%s/账号开关=%t/%s/%s", catalog, groupMode, accountEnabled, accountType, tier)
						t.Run(name, func(t *testing.T) {
							usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
							userRepo := &openAIRecordUsageUserRepoStub{}
							svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
							if catalog {
								svc.billingService = NewBillingService(svc.cfg, newStubPricingServiceFromJSON(t, `{
									"gpt-6.1-sol": {"litellm_provider": "openai", "mode": "chat",
									"input_cost_per_token": 2e-6, "output_cost_per_token": 10e-6,
									"cache_read_input_token_cost": 0.1e-6, "cache_creation_input_token_cost": 2.5e-6,
									"input_cost_per_token_above_272k_tokens": 4e-6,
									"output_cost_per_token_above_272k_tokens": 15e-6}
								}`))
							}
							apiKey := &APIKey{ID: 1}
							if groupMode != "无分组" {
								apiKey = openAIRecordUsageAPIKeyWithGroup(svc, 1, groupMode == "分组开启")
								apiKey.GroupID = i64p(1)
								apiKey.Group.RateMultiplier = 1.1
								svc.channelService = newTestChannelServiceForStats(t, &Channel{ID: 1, Status: StatusActive}, 1, PlatformOpenAI)
							}
							account := &Account{ID: 3, Platform: PlatformOpenAI, Type: accountType,
								Extra: map[string]any{openAILongContextBillingEnabledKey: accountEnabled}}
							err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
								Result: &OpenAIForwardResult{
									RequestID: name, Model: "gpt-6.1-sol", ServiceTier: &tier, Duration: time.Second,
									// 普通输入恰为 500k，由缓存使总上下文越过新阈值。
									Usage: OpenAIUsage{InputTokens: 600000, OutputTokens: 2000, CacheReadInputTokens: 80000, CacheCreationInputTokens: 20000},
								},
								APIKey: apiKey, User: &User{ID: 2}, Account: account,
							})
							require.NoError(t, err)
							wantLong := accountType == AccountTypeAPIKey && (accountEnabled || groupMode == "分组开启")
							inputFactor, outputFactor, tierFactor := 1.0, 1.0, 1.0
							if wantLong {
								inputFactor, outputFactor = 2, 1.5
							}
							if tier == "priority" {
								tierFactor = 2
							}
							log := usageRepo.lastLog
							require.NotNil(t, log)
							require.Equal(t, wantLong, log.LongContextBillingApplied)
							require.InDelta(t, 500000*2e-6*inputFactor*tierFactor, log.InputCost, 1e-12)
							require.InDelta(t, 80000*0.1e-6*inputFactor*tierFactor, log.CacheReadCost, 1e-12)
							require.InDelta(t, 20000*2.5e-6*inputFactor*tierFactor, log.CacheCreationCost, 1e-12)
							require.InDelta(t, 2000*10e-6*outputFactor*tierFactor, log.OutputCost, 1e-12)
							wantTotal := (1.058*inputFactor + 0.02*outputFactor) * tierFactor
							require.InDelta(t, wantTotal, log.TotalCost, 1e-12)
							require.InDelta(t, wantTotal*1.1, log.ActualCost, 1e-12)
							require.Equal(t, 1, userRepo.deductCalls)
							require.InDelta(t, wantTotal*1.1, userRepo.lastAmount, 1e-12)
							if groupMode != "无分组" {
								wantStats := 1.078 * tierFactor
								if accountType == AccountTypeAPIKey && accountEnabled {
									wantStats = (1.058*2 + 0.02*1.5) * tierFactor
								}
								require.NotNil(t, log.AccountStatsCost)
								require.InDelta(t, wantStats, *log.AccountStatsCost, 1e-12)
							}
						})
					}
				}
			}
		}
	}
}

func TestOpenAIGatewayServiceRecordUsage_GPT61SolUsesCredentialAndUpstreamModel(t *testing.T) {
	for _, tt := range []struct {
		name        string
		model       string
		upstream    string
		parentType  string
		inputTokens int
		wantTotal   float64
		wantLong    bool
	}{
		{name: "客户别名映射到目标", model: "custom", upstream: "gpt-6.1-sol", parentType: AccountTypeOAuth, inputTokens: 600000, wantTotal: 1.22},
		// 价卡优先请求模型；豁免看发送模型。600k 超过 Sol 6.1 价卡阈值，
		// 发送到旧模型不能冒领豁免；300k 则虽不豁免，仍未超过售价阈值。
		{name: "请求目标映射到旧模型", model: "gpt-6.1-sol", upstream: "gpt-6-sol", parentType: AccountTypeOAuth, inputTokens: 600000, wantTotal: 2.43, wantLong: true},
		{name: "请求目标映射到旧模型但未超过计费阈值", model: "gpt-6.1-sol", upstream: "gpt-6-sol", parentType: AccountTypeOAuth, inputTokens: 300000, wantTotal: 0.62},
		{name: "其他订阅模型", model: "gpt-6-sol", parentType: AccountTypeOAuth, inputTokens: 300000, wantTotal: 1.23, wantLong: true},
		{name: "订阅母账号", model: "gpt-6.1-sol", parentType: AccountTypeOAuth, inputTokens: 600000, wantTotal: 1.22},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			parentID := int64(4)
			svc.accountRepo = &openAIRecordUsageAccountRepoStub{account: &Account{
				ID: parentID, Platform: PlatformOpenAI, Type: tt.parentType,
				Extra: map[string]any{openAILongContextBillingEnabledKey: true},
			}}
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: tt.name, Model: tt.model, UpstreamModel: tt.upstream,
					Usage: OpenAIUsage{InputTokens: tt.inputTokens, OutputTokens: 2000}, Duration: time.Second},
				APIKey: openAIRecordUsageAPIKeyWithGroup(svc, 1, true), User: &User{ID: 2},
				Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID, QuotaDimension: QuotaDimensionSpark},
			})
			require.NoError(t, err)
			require.Equal(t, tt.wantLong, usageRepo.lastLog.LongContextBillingApplied)
			require.InDelta(t, tt.wantTotal, usageRepo.lastLog.TotalCost, 1e-12)
		})
	}
}

func TestCalculateCostUnified_ModelLongContextExemptionPreservesCustomIntervals(t *testing.T) {
	service := NewBillingService(nil, nil)
	for _, withResolver := range []bool{false, true} {
		input := CostInput{Model: "gpt-6.1-sol", Tokens: UsageTokens{InputTokens: 600000}, RateMultiplier: 1,
			LongContextBillingEnabled: boolPtr(true), DisableModelLongContextPricing: true}
		if withResolver {
			input.Ctx = context.Background()
			input.Resolver = NewModelPricingResolver(nil, service)
		}
		cost, err := service.CalculateCostUnified(input)
		require.NoError(t, err)
		require.InDelta(t, 1.2, cost.TotalCost, 1e-12)
		require.False(t, cost.LongContextBillingApplied)
		if withResolver {
			input.Resolved = &ResolvedPricing{
				BasePricing:               &ModelPricing{InputPricePerToken: 2e-6},
				Intervals:                 []PricingInterval{{MinTokens: 272000, InputMultiplier: pricingMultiplier(3)}},
				longContextPricingEnabled: true,
			}
			cost, err = service.CalculateCostUnified(input)
			require.NoError(t, err)
			require.InDelta(t, 3.6, cost.TotalCost, 1e-12, "管理员显式区间价格应保留")
			input.DisableModelLongContextPricing = false
			cost, err = service.CalculateCostUnified(input)
			require.NoError(t, err)
			require.InDelta(t, 3.6, cost.TotalCost, 1e-12, "显式区间优先于默认长上下文价卡")
		}
	}
}

func TestOpenAIGatewayServiceRecordUsage_GPT61SolLongContextBoundary(t *testing.T) {
	for _, totalInput := range []int{256001, 272000, 272001, 499999, 500000, 500001} {
		for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
			t.Run(fmt.Sprintf("%s/%d", accountType, totalInput), func(t *testing.T) {
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
				err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{RequestID: "boundary", Model: "gpt-6.1-sol", Duration: time.Second,
						Usage: OpenAIUsage{InputTokens: totalInput, OutputTokens: 1000, CacheReadInputTokens: 80000, CacheCreationInputTokens: 20000}},
					APIKey: openAIRecordUsageAPIKeyWithGroup(svc, 1, true), User: &User{ID: 2},
					Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: accountType,
						Extra: map[string]any{openAILongContextBillingEnabledKey: true}},
				})
				require.NoError(t, err)
				wantLong := accountType == AccountTypeAPIKey && totalInput > 500000
				wantInput, wantOutput, wantCache := float64(totalInput-100000)*2e-6, 0.01, 0.058
				if wantLong {
					wantInput *= 2
					wantOutput *= 1.5
					wantCache *= 2
				}
				require.Equal(t, wantLong, usageRepo.lastLog.LongContextBillingApplied)
				require.InDelta(t, wantInput, usageRepo.lastLog.InputCost, 1e-12)
				require.InDelta(t, wantOutput, usageRepo.lastLog.OutputCost, 1e-12)
				require.InDelta(t, wantCache, usageRepo.lastLog.CacheReadCost+usageRepo.lastLog.CacheCreationCost, 1e-12)
				require.InDelta(t, wantInput+wantOutput+wantCache, usageRepo.lastLog.TotalCost, 1e-12)
			})
		}
	}
}

func TestOpenAIGatewayServiceRecordUsage_GPT61SolExemptionSurvivesRepricing(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, totalInput := range []int{500000, 500001, 600000} {
			t.Run(fmt.Sprintf("%s/%d", accountType, totalInput), func(t *testing.T) {
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				userRepo := &openAIRecordUsageUserRepoStub{}
				svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
				apiKey := openAIRecordUsageAPIKeyWithGroup(svc, 1, true)
				apiKey.GroupID = i64p(1)
				apiKey.Group.Platform = PlatformOpenAI
				apiKey.Group.RateMultiplier = 1.1
				apiKey.Group.FreeOpenAIFast = true
				svc.channelService = newTestChannelServiceForStats(t, &Channel{ID: 1, Status: StatusActive}, 1, PlatformOpenAI)
				tier := "priority"
				err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{
						RequestID: "repricing", Model: "gpt-6-sol", UpstreamModel: "gpt-6.1-sol", BillingModel: "gpt-6-sol",
						UpstreamResponseModel: "gpt-6.1-sol", ServiceTier: &tier, Duration: time.Second,
						Usage: OpenAIUsage{InputTokens: totalInput, OutputTokens: 2000, CacheReadInputTokens: 80000},
					},
					APIKey: apiKey, User: &User{ID: 2},
					Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: accountType,
						Extra: map[string]any{openAILongContextBillingEnabledKey: true}},
					ChannelUsageFields: ChannelUsageFields{BillingModelSource: BillingModelSourceResponse},
				})
				require.NoError(t, err)
				// 响应模型改用新阈值和较低缓存价；免费 Fast 重算仍保留 API 边界及订阅豁免。
				wantLong := accountType == AccountTypeAPIKey && totalInput > 500000
				inputFactor, outputFactor := 1.0, 1.0
				if wantLong {
					inputFactor, outputFactor = 2, 1.5
				}
				standardCost := (float64(totalInput-80000)*2e-6+80000*0.1e-6)*inputFactor + 2000*10e-6*outputFactor
				require.InDelta(t, standardCost*2, usageRepo.lastLog.TotalCost, 1e-12)
				require.InDelta(t, standardCost*1.1, usageRepo.lastLog.ActualCost, 1e-12)
				require.InDelta(t, standardCost*1.1, userRepo.lastAmount, 1e-12)
				require.Equal(t, wantLong, usageRepo.lastLog.LongContextBillingApplied)
				require.NotNil(t, usageRepo.lastLog.AccountStatsCost)
				require.InDelta(t, standardCost*2, *usageRepo.lastLog.AccountStatsCost, 1e-12)
			})
		}
	}
}

func TestOpenAIGPT61SolLongContextExempt(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	apiKey := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, tt := range []struct {
		name    string
		account *Account
		result  *OpenAIForwardResult
		tokens  UsageTokens
		want    bool
	}{
		{name: "空账号", result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}},
		{name: "空结果", account: oauth},
		{name: "订阅", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}, want: true},
		{name: "别名", account: oauth, result: &OpenAIForwardResult{Model: "openai/GPT_6.1_SOL-max"}, want: true},
		{name: "压缩", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6.1-sol-openai-compact"}, want: true},
		{name: "映射到目标", account: oauth, result: &OpenAIForwardResult{Model: "custom", UpstreamModel: "gpt-6.1-sol"}, want: true},
		{name: "映射离开目标", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6.1-sol", UpstreamModel: "gpt-6-sol"}},
		{name: "仅计费标签相同", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6-sol", BillingModel: "gpt-6.1-sol"}},
		{name: "仅响应标签相同", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6-sol", UpstreamResponseModel: "gpt-6.1-sol"}},
		{name: "其他模型", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6-astra"}},
		{name: "相似名称", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6.1-sol-preview"}},
		{name: "订阅刚好 500k", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}, tokens: UsageTokens{InputTokens: 500_000}, want: true},
		{name: "订阅超过 500k", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}, tokens: UsageTokens{InputTokens: 500_001}, want: true},
		{name: "订阅缓存越过 500k", account: oauth, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}, tokens: UsageTokens{InputTokens: 400_000, CacheReadTokens: 100_000, CacheCreationTokens: 1}, want: true},
		{name: "订阅访问令牌刚好 500k", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken}, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}, tokens: UsageTokens{InputTokens: 500_000}, want: true},
		{name: "订阅访问令牌超过 500k", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken}, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}, tokens: UsageTokens{InputTokens: 500_001}, want: true},
		{name: "API Key", account: apiKey, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}},
		{name: "API Key 别名", account: apiKey, result: &OpenAIForwardResult{Model: "openai/GPT_6.1_SOL-max"}},
		{name: "API Key 压缩", account: apiKey, result: &OpenAIForwardResult{Model: "gpt-6.1-sol-openai-compact"}},
		{name: "API Key 映射到目标", account: apiKey, result: &OpenAIForwardResult{Model: "custom", UpstreamModel: "gpt-6.1-sol"}},
		{name: "API Key 超过 500k 仍不豁免", account: apiKey, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}, tokens: UsageTokens{InputTokens: 500_001}},
		{name: "API Key 映射离开目标", account: apiKey, result: &OpenAIForwardResult{Model: "gpt-6.1-sol", UpstreamModel: "gpt-6-sol"}},
		{name: "API Key 仅计费标签", account: apiKey, result: &OpenAIForwardResult{Model: "gpt-6-sol", BillingModel: "gpt-6.1-sol"}},
		{name: "API Key 仅响应标签", account: apiKey, result: &OpenAIForwardResult{Model: "gpt-6-sol", UpstreamResponseModel: "gpt-6.1-sol"}},
		{name: "API Key 其他模型", account: apiKey, result: &OpenAIForwardResult{Model: "gpt-6-sol"}},
		{name: "API Key Astra", account: apiKey, result: &OpenAIForwardResult{Model: "gpt-6-astra"}},
		{name: "API Key 相似名称", account: apiKey, result: &OpenAIForwardResult{Model: "gpt-6.1-sol-preview"}},
		{name: "其他平台", account: &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}},
		{name: "其他平台 API Key", account: &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey}, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}},
		{name: "订阅访问令牌", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken}, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}, want: true},
		{name: "上游透传不豁免", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeUpstream}, result: &OpenAIForwardResult{Model: "gpt-6.1-sol"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openAIGPT61SolLongContextExempt(tt.account, tt.result, tt.tokens))
		})
	}
}

// 同时覆盖远程目录更新、静态兜底、旧计费入口和分组入口，校验真实扣款与账号成本。

func TestOpenAIGatewayServiceRecordUsage_GPT61SolSubscriptionCacheKeepsExemption(t *testing.T) {
	for _, tt := range []struct {
		name     string
		usage    OpenAIUsage
		wantLong bool
	}{
		{name: "缓存使总输入刚好 500k", usage: OpenAIUsage{InputTokens: 500000, CacheReadInputTokens: 200000, OutputTokens: 1000}},
		{name: "缓存使总输入超过 500k", usage: OpenAIUsage{InputTokens: 500001, CacheReadInputTokens: 100000, CacheCreationInputTokens: 1, OutputTokens: 1000}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: tt.name, Model: "gpt-6.1-sol", Duration: time.Second, Usage: tt.usage},
				APIKey: openAIRecordUsageAPIKeyWithGroup(svc, 1, true), User: &User{ID: 2},
				Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
					Extra: map[string]any{openAILongContextBillingEnabledKey: true}},
			})
			require.NoError(t, err)
			require.Equal(t, tt.wantLong, usageRepo.lastLog.LongContextBillingApplied)
		})
	}
}

func TestOpenAIGatewayServiceRecordUsage_GPT61SolSubscriptionAbove500kMatchesAccountStats(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	apiKey := openAIRecordUsageAPIKeyWithGroup(svc, 1, true)
	apiKey.GroupID = i64p(1)
	apiKey.Group.RateMultiplier = 1.1
	svc.channelService = newTestChannelServiceForStats(t, &Channel{ID: 1, Status: StatusActive}, 1, PlatformOpenAI)
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "sub-500k", Model: "gpt-6.1-sol", Duration: time.Second,
			Usage: OpenAIUsage{InputTokens: 500001, OutputTokens: 2000}},
		APIKey: apiKey, User: &User{ID: 2},
		Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeSetupToken,
			Extra: map[string]any{openAILongContextBillingEnabledKey: true}},
	})
	require.NoError(t, err)
	const base = 500001*2e-6 + 2000*10e-6
	require.False(t, usageRepo.lastLog.LongContextBillingApplied)
	require.InDelta(t, base, usageRepo.lastLog.TotalCost, 1e-9)
	require.InDelta(t, base*1.1, usageRepo.lastLog.ActualCost, 1e-9)
	require.InDelta(t, base*1.1, userRepo.lastAmount, 1e-9)
	require.NotNil(t, usageRepo.lastLog.AccountStatsCost)
	require.InDelta(t, base, *usageRepo.lastLog.AccountStatsCost, 1e-9)
}

func TestOpenAIGatewayServiceRecordUsage_APIKeyGPT61SolUsesPKLongContextThreshold(t *testing.T) {
	for _, tt := range []struct {
		name      string
		model     string
		upstream  string
		wantLong  bool
		wantTotal float64
	}{
		{name: "API Key Sol", model: "gpt-6.1-sol", wantLong: false, wantTotal: 0.62},
		{name: "API Key 别名映射到 Sol", model: "custom", upstream: "gpt-6.1-sol", wantLong: false, wantTotal: 0.62},
		{name: "API Key Sol 售价仍未超过自身门槛", model: "gpt-6.1-sol", upstream: "gpt-6-sol", wantLong: false, wantTotal: 0.62},
		{name: "API Key 非 Sol", model: "gpt-6-sol", wantLong: true, wantTotal: 1.23},
		{name: "API Key Astra", model: "gpt-6-astra", wantLong: true, wantTotal: 6.15},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: tt.name, Model: tt.model, UpstreamModel: tt.upstream,
					Usage: OpenAIUsage{InputTokens: 300000, OutputTokens: 2000}, Duration: time.Second},
				APIKey: openAIRecordUsageAPIKeyWithGroup(svc, 1, true), User: &User{ID: 2},
				Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Extra: map[string]any{openAILongContextBillingEnabledKey: true}},
			})
			require.NoError(t, err)
			require.Equal(t, tt.wantLong, usageRepo.lastLog.LongContextBillingApplied)
			require.InDelta(t, tt.wantTotal, usageRepo.lastLog.TotalCost, 1e-12)
		})
	}
}

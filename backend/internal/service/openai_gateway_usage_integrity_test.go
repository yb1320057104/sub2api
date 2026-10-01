//go:build unit

package service

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cacheconversion"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequiresBillableGrokChatUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		account *Account
		models  []string
		want    bool
	}{
		{
			name:    "grok platform",
			account: &Account{Platform: PlatformGrok},
			models:  []string{"alias"},
			want:    true,
		},
		{
			name:    "OpenAI-compatible requested Grok model",
			account: &Account{Platform: PlatformOpenAI},
			models:  []string{"grok-4.5"},
			want:    true,
		},
		{
			name:    "OpenAI-compatible mapped Grok model",
			account: &Account{Platform: PlatformOpenAI},
			models:  []string{"alias", "grok-4.5"},
			want:    true,
		},
		{
			name:    "xAI-qualified Grok model",
			account: &Account{Platform: PlatformOpenAI},
			models:  []string{"xai/grok-4.5"},
			want:    true,
		},
		{
			name:    "ordinary OpenAI model",
			account: &Account{Platform: PlatformOpenAI},
			models:  []string{"gpt-5.4"},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, requiresBillableGrokChatUsage(tt.account, tt.models...))
		})
	}
}

func TestPKCacheConversionSettings(t *testing.T) {
	defaults, err := normalizeOpenAICacheConversionExtra(PlatformOpenAI, nil)
	require.NoError(t, err)
	require.Equal(t, false, defaults[AccountExtraCacheConversionEnabled])
	require.Equal(t, float64(1), defaults[AccountExtraCacheConversionMultiplier])

	account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{
		AccountExtraCacheConversionEnabled:    true,
		AccountExtraCacheConversionMultiplier: float64(0),
	}}
	update := map[string]any{"unrelated_setting": "preserved"}
	normalized, err := normalizeOpenAICacheConversionUpdateExtra(account, update)
	require.NoError(t, err)
	require.Equal(t, true, normalized[AccountExtraCacheConversionEnabled])
	require.Equal(t, float64(0), normalized[AccountExtraCacheConversionMultiplier])
	require.Equal(t, "preserved", normalized["unrelated_setting"])
	require.Equal(t, map[string]any{"unrelated_setting": "preserved"}, update)
	require.True(t, account.IsCacheConversionEnabled())
	require.Zero(t, account.GetCacheConversionMultiplier())

	for _, invalid := range []any{-1.0, math.NaN(), math.Inf(1), "0.5", nil} {
		require.Error(t, ValidateOpenAICacheConversionExtra(PlatformOpenAI, map[string]any{
			AccountExtraCacheConversionMultiplier: invalid,
		}))
	}
	require.Error(t, ValidateOpenAICacheConversionExtra(PlatformOpenAI, map[string]any{
		AccountExtraCacheConversionEnabled: "true",
	}))
	require.Error(t, ValidateOpenAICacheConversionExtra(PlatformAnthropic, account.Extra))
}

func TestPKCacheConversionDisabledPreservesOriginal(t *testing.T) {
	raw := UsageTokens{InputTokens: 11, OutputTokens: 13, CacheCreationTokens: 5, CacheReadTokens: 7}
	body := []byte(`{ "usage": {"input_tokens":23,"output_tokens":13,"cache_creation_input_tokens":5,"cache_read_input_tokens":7}, "text":"keep 原文" }`)
	sse := ": keepalive\nevent: response.completed\ndata: " + string(body) + "\n\ndata: [DONE]\n\n"
	for _, account := range []*Account{
		nil,
		{Platform: PlatformOpenAI},
		{Platform: PlatformOpenAI, Extra: map[string]any{AccountExtraCacheConversionEnabled: false, AccountExtraCacheConversionMultiplier: 0.5}},
		{Platform: PlatformAnthropic, Extra: map[string]any{AccountExtraCacheConversionEnabled: true, AccountExtraCacheConversionMultiplier: 0.5}},
	} {
		converted, err := convertOpenAICacheCreationToRead(account, raw)
		require.NoError(t, err)
		require.Equal(t, raw, converted)
		convertedJSON, err := RewriteOpenAICacheConversionJSON(account, body)
		require.NoError(t, err)
		require.Equal(t, body, convertedJSON)
		convertedSSE, err := rewriteCacheConvertedSSE(account, sse, cacheconversion.OpenAI)
		require.NoError(t, err)
		require.Equal(t, sse, convertedSSE)
	}
}

func TestPKCacheConversionBillingTokens(t *testing.T) {
	raw := UsageTokens{
		InputTokens: 11, OutputTokens: 13, CacheCreationTokens: 5, CacheReadTokens: 7,
		CacheCreation5mTokens: 2, CacheCreation1hTokens: 3,
	}
	for _, tt := range []struct {
		name       string
		multiplier float64
		wantRead   int
	}{
		{name: "zero keeps existing reads", multiplier: 0, wantRead: 7},
		{name: "half rounds creation down", multiplier: 0.5, wantRead: 9},
		{name: "one adds all creation", multiplier: 1, wantRead: 12},
	} {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{
				AccountExtraCacheConversionEnabled:    true,
				AccountExtraCacheConversionMultiplier: tt.multiplier,
			}}
			converted, err := convertOpenAICacheCreationToRead(account, raw)
			require.NoError(t, err)
			require.Equal(t, 11, converted.InputTokens)
			require.Equal(t, 13, converted.OutputTokens)
			require.Equal(t, tt.wantRead, converted.CacheReadTokens)
			require.Zero(t, converted.CacheCreationTokens)
			require.Zero(t, converted.CacheCreation5mTokens)
			require.Zero(t, converted.CacheCreation1hTokens)
			require.NotNil(t, converted.OriginalContextTokens)
			require.Equal(t, 23, converted.InputContextTokens())
			require.Equal(t, 5, raw.CacheCreationTokens)
			require.Equal(t, 7, raw.CacheReadTokens)
			require.Nil(t, raw.OriginalContextTokens)
		})
	}
}

func TestPKCacheConversionJSONKeepsPayloadAndOriginalUsage(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{
		AccountExtraCacheConversionEnabled:    true,
		AccountExtraCacheConversionMultiplier: 0.5,
	}}
	const original = `{"usage":{"input_tokens":23,"output_tokens":13,"total_tokens":36,"cache_creation_input_tokens":5,"input_tokens_details":{"cache_write_tokens":5,"cached_tokens":7,"provider_extra":"keep"},"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":3},"provider_usage":{"value":99}},"output":[{"text":"usage.cache_creation_input_tokens=999 原文"},{"tool_result":{"usage":{"cache_creation_input_tokens":999}}}],"provider_extension":{"usage":{"cache_creation_input_tokens":999}}}`
	const expected = `{"usage":{"input_tokens":20,"output_tokens":13,"total_tokens":33,"cache_creation_input_tokens":0,"input_tokens_details":{"cache_write_tokens":0,"cached_tokens":9,"provider_extra":"keep"},"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"provider_usage":{"value":99}},"output":[{"text":"usage.cache_creation_input_tokens=999 原文"},{"tool_result":{"usage":{"cache_creation_input_tokens":999}}}],"provider_extension":{"usage":{"cache_creation_input_tokens":999}}}`
	raw := json.RawMessage(original)
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	writer.Header().Set("Content-Length", "9999")

	err := writeCacheConvertedJSON(ctx, account, raw, cacheconversion.OpenAI)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, writer.Code)
	require.Empty(t, writer.Header().Get("Content-Length"))
	require.JSONEq(t, expected, writer.Body.String())
	require.Equal(t, original, string(raw))
}

func TestPKCacheConversionSSEPreservesTextAndFraming(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{
		AccountExtraCacheConversionEnabled:    true,
		AccountExtraCacheConversionMultiplier: 0.5,
	}}
	const prefix = ": keepalive\nevent: content.delta\nid: event-1\ndata: {\"delta\":\"cache_creation_input_tokens=5 原文\",\"tool_result\":{\"usage\":{\"cache_creation_input_tokens\":999}}}\n\n"
	const suffix = "\n\ndata: [DONE]\n\n"
	for _, tt := range []struct {
		name     string
		protocol cacheconversion.Protocol
		original string
		expected string
	}{
		{
			name:     "OpenAI total input includes cache buckets",
			protocol: cacheconversion.OpenAI,
			original: `{"response":{"usage":{"input_tokens":23,"output_tokens":13,"total_tokens":36,"input_tokens_details":{"cache_write_tokens":5,"cached_tokens":7},"provider_note":"keep"}}}`,
			expected: `{"response":{"usage":{"input_tokens":20,"output_tokens":13,"total_tokens":33,"input_tokens_details":{"cache_write_tokens":0,"cached_tokens":9},"provider_note":"keep"}}}`,
		},
		{
			name:     "Anthropic ordinary input stays unchanged",
			protocol: cacheconversion.Anthropic,
			original: `{"usage":{"input_tokens":11,"output_tokens":13,"cache_creation_input_tokens":5,"cache_read_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":3},"provider_note":"keep"}}`,
			expected: `{"usage":{"input_tokens":11,"output_tokens":13,"cache_creation_input_tokens":0,"cache_read_input_tokens":9,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"provider_note":"keep"}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original := prefix + "event: usage\ndata: " + tt.original + suffix
			expected := prefix + "event: usage\ndata: " + tt.expected + suffix
			converted, err := rewriteCacheConvertedSSE(account, original, tt.protocol)
			require.NoError(t, err)
			require.Equal(t, expected, converted)
		})
	}
}

type pkCacheConversionInflightCache struct {
	*memInflightCache
	rateLimitCosts chan float64
}

func (c *pkCacheConversionInflightCache) UpdateAPIKeyRateLimitUsage(_ context.Context, _ int64, cost float64) error {
	c.rateLimitCosts <- cost
	return nil
}

func TestPKCacheConversionInflightBillingPreservesOriginalContext(t *testing.T) {
	const (
		model             = "gpt-6.1-sol"
		startingBalance   = 100.0
		customerRate      = 1.7
		accountRate       = 1.3
		cacheCreation     = 200001
		originalCacheRead = 7000
		outputTokens      = 13
	)
	for _, tier := range []struct {
		name             string
		inputTokens      int
		inputMultiplier  float64
		outputMultiplier float64
		longContext      bool
	}{
		{name: "at threshold", inputTokens: 272000, inputMultiplier: 1, outputMultiplier: 1},
		{name: "above threshold", inputTokens: 272001, inputMultiplier: 2, outputMultiplier: 1.5, longContext: true},
	} {
		t.Run(tier.name, func(t *testing.T) {
			for _, conversion := range []struct {
				name       string
				multiplier float64
				wantRead   int
			}{
				{name: "zero", multiplier: 0, wantRead: 7000},
				{name: "half floors odd creation", multiplier: 0.5, wantRead: 107000},
				{name: "large", multiplier: 30, wantRead: 6007030},
			} {
				t.Run(conversion.name, func(t *testing.T) {
					cache := &pkCacheConversionInflightCache{
						memInflightCache: newMemInflightCache(startingBalance),
						rateLimitCosts:   make(chan float64, 2),
					}
					billingCache := newInflightSvc(t, cache, 60)
					usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
					billingRepo := &openAIRecordUsageBillingRepoStub{}
					svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, nil, nil, nil)
					svc.cfg.Default.RateMultiplier = customerRate
					svc.cfg.Billing.InflightReservation = billingCache.cfg.Billing.InflightReservation
					svc.billingCacheService = billingCache
					svc.resolver = NewModelPricingResolver(nil, svc.billingService)
					user := &User{ID: 1, Balance: startingBalance}
					apiKey := &APIKey{ID: 2, User: user, Quota: 100, RateLimit5h: 100}
					accountMultiplier := accountRate
					account := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: &accountMultiplier, Extra: map[string]any{
						AccountExtraCacheConversionEnabled:    true,
						AccountExtraCacheConversionMultiplier: conversion.multiplier,
						openAILongContextBillingEnabledKey:    true,
						"quota_limit":                         100.0,
					}}
					raw := OpenAIUsage{InputTokens: tier.inputTokens, OutputTokens: outputTokens, CacheCreationInputTokens: cacheCreation, CacheReadInputTokens: originalCacheRead}
					result := &OpenAIForwardResult{RequestID: "pk-cache-inflight", Model: model, Usage: raw}
					ctx := context.Background()

					// Admission has no selected account or upstream cache usage. Its estimate
					// must remain separate from the converted amount ultimately charged.
					estimate, priced := svc.EstimateInflightReservation(ctx, apiKey, InflightEstimateRequest{
						Model: model, BodyBytes: tier.inputTokens * 4, MaxTokens: outputTokens, Kind: InflightEstimateToken,
					})
					require.True(t, priced)
					require.InDelta(t, (float64(tier.inputTokens)*2e-6+outputTokens*10e-6)*customerRate, estimate, 1e-12)
					reservation, err := billingCache.ReserveInflight(ctx, user, nil, nil, estimate)
					require.NoError(t, err)
					require.NotNil(t, reservation)
					t.Cleanup(reservation.Release)
					taskDone := InflightReservationFromContext(WithInflightReservation(ctx, reservation)).Acquire()
					reservation.HandlerDone()
					require.Equal(t, 1, cache.count(), "handler return must not release a pending usage task")
					_, err = billingCache.ReserveInflight(ctx, user, nil, nil, startingBalance)
					require.ErrorIs(t, err, ErrInsufficientBalance)

					// Hold the detached task after RecordUsage so the test observes the
					// actual cache deduction before the final reservation reference exits.
					billed := make(chan error, 1)
					finishTask := make(chan struct{})
					finished := make(chan struct{})
					go func() {
						defer close(finished)
						defer taskDone()
						billed <- svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
							Result: result, APIKey: apiKey, User: user, Account: account, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
						})
						<-finishTask
					}()
					t.Cleanup(func() {
						select {
						case <-finishTask:
						default:
							close(finishTask)
						}
						<-finished
					})
					require.NoError(t, <-billed)

					ordinaryInput := tier.inputTokens - cacheCreation - originalCacheRead
					wantInputCost := float64(ordinaryInput) * 2e-6 * tier.inputMultiplier
					wantReadCost := float64(conversion.wantRead) * 0.1e-6 * tier.inputMultiplier
					wantOutputCost := outputTokens * 10e-6 * tier.outputMultiplier
					wantTotalCost := wantInputCost + wantReadCost + wantOutputCost
					wantActualCost := wantTotalCost * customerRate
					require.Equal(t, raw, result.Usage, "downstream conversion must not overwrite upstream billing evidence")
					require.Equal(t, 1, usageRepo.calls)
					log := usageRepo.lastLog
					require.NotNil(t, log)
					require.Equal(t, ordinaryInput, log.InputTokens)
					require.Equal(t, conversion.wantRead, log.CacheReadTokens)
					require.Zero(t, log.CacheCreationTokens)
					require.Zero(t, log.CacheCreationCost)
					require.InDelta(t, wantInputCost, log.InputCost, 1e-12)
					require.InDelta(t, wantReadCost, log.CacheReadCost, 1e-12)
					require.InDelta(t, wantOutputCost, log.OutputCost, 1e-12)
					require.InDelta(t, wantTotalCost, log.TotalCost, 1e-12)
					require.InDelta(t, wantActualCost, log.ActualCost, 1e-12)
					require.Equal(t, tier.longContext, log.LongContextBillingApplied, "tier must use the original 272000/272001 context even when conversion crosses the threshold")
					require.Equal(t, 1, billingRepo.calls)
					require.NotNil(t, billingRepo.lastCmd)
					require.InDelta(t, wantActualCost, billingRepo.lastCmd.BalanceCost, 1e-12)
					require.InDelta(t, wantActualCost, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
					require.InDelta(t, wantActualCost, billingRepo.lastCmd.APIKeyRateLimitCost, 1e-12)
					require.InDelta(t, wantTotalCost*accountRate, billingRepo.lastCmd.AccountQuotaCost, 1e-12)
					require.Zero(t, billingRepo.lastCmd.SubscriptionCost)
					require.Equal(t, int32(1), cache.deducts.Load())
					balance, err := cache.GetUserBalance(ctx, user.ID)
					require.NoError(t, err)
					require.InDelta(t, startingBalance-wantActualCost, balance, 1e-12)
					require.Equal(t, 1, cache.count(), "converted actual cost must land before releasing the reservation")

					close(finishTask)
					<-finished
					taskDone()
					reservation.HandlerDone()
					require.Zero(t, cache.count())
					require.Equal(t, int32(1), cache.releaseCt.Load(), "task and handler cleanup must release exactly once")
					balance, err = cache.GetUserBalance(ctx, user.ID)
					require.NoError(t, err)
					require.InDelta(t, startingBalance-wantActualCost, balance, 1e-12, "releasing an estimate must neither charge it nor refund it")
					billingCache.Stop()
					require.Len(t, cache.rateLimitCosts, 1)
					require.InDelta(t, wantActualCost, <-cache.rateLimitCosts, 1e-12)
				})
			}
		})
	}
}

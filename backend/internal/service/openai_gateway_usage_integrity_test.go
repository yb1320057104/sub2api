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
	"github.com/Wei-Shaw/sub2api/internal/pkg/usageoutput"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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
	pkCacheConversionInflightBilling(t, 1)
}

func TestPKHiddenBillingCacheConversionAndLongContext(t *testing.T) {
	pkCacheConversionInflightBilling(t, 2)
}

func pkCacheConversionInflightBilling(t *testing.T, hidden float64) {
	t.Helper()
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
						AccountExtraHiddenRateMultiplier:      hidden,
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
					if hidden != 1 {
						wantActualCost = QuantizeUsageBillingAmount(wantActualCost * hidden)
					}
					require.Equal(t, raw, result.Usage, "downstream conversion must not overwrite upstream billing evidence")
					require.Equal(t, 1, usageRepo.calls)
					log := usageRepo.lastLog
					require.NotNil(t, log)
					require.Equal(t, ordinaryInput, log.InputTokens)
					require.NotNil(t, log.OriginalUsage)
					require.Equal(t, int64(ordinaryInput), log.OriginalUsage.InputTokens)
					require.Equal(t, int64(originalCacheRead), log.OriginalUsage.CacheReadTokens)
					require.Equal(t, int64(cacheCreation), log.OriginalUsage.CacheCreationTokens)
					require.Equal(t, int64(outputTokens), log.OriginalUsage.OutputTokens)
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

func TestPKKiloBridgeSettings(t *testing.T) {
	defaults, err := normalizeKiloBridgeExtra(PlatformOpenAI, nil, nil)
	require.NoError(t, err)
	require.Equal(t, false, defaults[AccountExtraKiloBridgeEnabled])
	for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
		current := map[string]any{AccountExtraKiloBridgeEnabled: true}
		input := map[string]any{"unrelated": "keep"}
		extra, err := normalizeKiloBridgeExtra(PlatformOpenAI, input, current)
		require.NoError(t, err)
		require.True(t, (&Account{Platform: PlatformOpenAI, Type: kind, Extra: extra}).IsKiloBridgeEnabled())
		require.Equal(t, map[string]any{"unrelated": "keep"}, input)
		extra, err = normalizeKiloBridgeExtra(PlatformOpenAI, map[string]any{AccountExtraKiloBridgeEnabled: false}, current)
		require.NoError(t, err)
		require.Equal(t, false, extra[AccountExtraKiloBridgeEnabled])
	}
	for _, invalid := range []any{nil, "true", 1, 0.0} {
		require.Error(t, ValidateKiloBridgeExtra(PlatformOpenAI, map[string]any{AccountExtraKiloBridgeEnabled: invalid}))
	}
	for _, enabled := range []bool{false, true} {
		extra := map[string]any{AccountExtraKiloBridgeEnabled: enabled}
		require.Error(t, ValidateKiloBridgeExtra(PlatformAnthropic, extra))
		require.False(t, (&Account{Platform: PlatformAnthropic, Extra: extra}).IsKiloBridgeEnabled())
	}
}

func TestPKHiddenBillingOutputMultipliers(t *testing.T) {
	t.Parallel()
	const source = `{ "usage": {"input_tokens":100,"output_tokens":7,"total_tokens":107,"input_tokens_details":{"cached_tokens":0}}, "id":107 }`
	for _, tc := range []struct {
		name       string
		multiplier float64
		input      int64
		output     int64
		total      int64
	}{
		{name: "zero", multiplier: 0, input: 0, output: 0, total: 0},
		{name: "one", multiplier: 1, input: 100, output: 7, total: 107},
		{name: "two", multiplier: 2, input: 200, output: 14, total: 214},
		{name: "half", multiplier: 0.5, input: 50, output: 3, total: 53},
		{name: "decimal floor", multiplier: 0.29, input: 29, output: 2, total: 31},
		{name: "six decimals", multiplier: 0.000001, input: 0, output: 0, total: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			original := []byte(source)
			out, err := usageoutput.ScaleJSON(original, tc.multiplier, usageoutput.Options{Protocol: usageoutput.OpenAI})
			require.NoError(t, err)
			require.Equal(t, source, string(original))
			require.Equal(t, tc.input, gjson.GetBytes(out, "usage.input_tokens").Int())
			require.Equal(t, tc.output, gjson.GetBytes(out, "usage.output_tokens").Int())
			require.Equal(t, tc.total, gjson.GetBytes(out, "usage.total_tokens").Int())
			require.True(t, gjson.GetBytes(out, "usage.input_tokens_details.cached_tokens").Exists())
			require.Zero(t, gjson.GetBytes(out, "usage.input_tokens_details.cached_tokens").Int())
			require.Equal(t, int64(107), gjson.GetBytes(out, "id").Int())
			if tc.multiplier == 1 {
				require.Equal(t, source, string(out), "default must preserve exact wire bytes")
			} else {
				out[0] = '['
				require.Equal(t, source, string(original), "rewritten output must own its buffer")
			}
		})
	}
}

func TestPKHiddenBillingOutputProtocols(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		protocol usageoutput.Protocol
		body     string
		want     string
	}{
		{
			name:     "OpenAI aliases reasoning and residual",
			protocol: usageoutput.OpenAI,
			body:     `{"usage":{"input_tokens":23,"prompt_tokens":23,"output_tokens":13,"completion_tokens":13,"total_tokens":39,"cache_creation_input_tokens":5,"cache_read_input_tokens":7,"input_tokens_details":{"cached_tokens":7,"cache_write_tokens":5,"text_tokens":11,"audio_tokens":3,"image_tokens":9},"output_tokens_details":{"reasoning_tokens":7,"text_tokens":5,"audio_tokens":1,"accepted_prediction_tokens":3,"rejected_prediction_tokens":1},"cost":0.9,"duration":12,"provider_extension":{"tokens":99}}}`,
			want:     `{"usage":{"input_tokens":11,"prompt_tokens":11,"output_tokens":6,"completion_tokens":6,"total_tokens":18,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"input_tokens_details":{"cached_tokens":3,"cache_write_tokens":2,"text_tokens":5,"audio_tokens":1,"image_tokens":4},"output_tokens_details":{"reasoning_tokens":3,"text_tokens":2,"audio_tokens":0,"accepted_prediction_tokens":1,"rejected_prediction_tokens":0},"cost":0.9,"duration":12,"provider_extension":{"tokens":99}}}`,
		},
		{
			name:     "Anthropic cache buckets and detail aliases",
			protocol: usageoutput.Anthropic,
			body:     `{"message":{"usage":{"input_tokens":11,"output_tokens":13,"total_tokens":39,"cache_creation_input_tokens":5,"cache_read_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":3},"cache_creation_5m_tokens":2,"cache_creation_1h_tokens":3,"server_tool_use":{"web_search_requests":7}}}}`,
			want:     `{"message":{"usage":{"input_tokens":5,"output_tokens":6,"total_tokens":17,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":1},"cache_creation_5m_tokens":1,"cache_creation_1h_tokens":1,"server_tool_use":{"web_search_requests":7}}}}`,
		},
		{
			name:     "Gemini modality arrays cache subset and residual",
			protocol: usageoutput.Gemini,
			body:     `{"response":{"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":7,"cachedContentTokenCount":9,"thoughtsTokenCount":3,"toolUsePromptTokenCount":5,"totalTokenCount":29,"promptTokensDetails":[{"modality":"TEXT","tokenCount":5},{"modality":"AUDIO","tokenCount":6}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":3},{"modality":"IMAGE","tokenCount":4}],"cacheTokensDetails":[{"modality":"TEXT","tokenCount":9}],"toolUsePromptTokensDetails":[{"modality":"TEXT","tokenCount":5}],"trafficType":"ON_DEMAND"}}}`,
			want:     `{"response":{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"cachedContentTokenCount":4,"thoughtsTokenCount":1,"toolUsePromptTokenCount":2,"totalTokenCount":12,"promptTokensDetails":[{"modality":"TEXT","tokenCount":2},{"modality":"AUDIO","tokenCount":3}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":1},{"modality":"IMAGE","tokenCount":2}],"cacheTokensDetails":[{"modality":"TEXT","tokenCount":4}],"toolUsePromptTokensDetails":[{"modality":"TEXT","tokenCount":2}],"trafficType":"ON_DEMAND"}}}`,
		},
		{
			name:     "Realtime singular detail containers",
			protocol: usageoutput.OpenAI,
			body:     `{"response":{"usage":{"input_tokens":11,"output_tokens":3,"total_tokens":14,"input_token_details":{"cached_tokens":7,"text_tokens":9,"audio_tokens":1,"image_tokens":1,"cached_tokens_details":{"text_tokens":3,"audio_tokens":3,"image_tokens":1,"duration":3}},"output_token_details":{"text_tokens":1,"audio_tokens":2}}}}`,
			want:     `{"response":{"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6,"input_token_details":{"cached_tokens":3,"text_tokens":4,"audio_tokens":0,"image_tokens":0,"cached_tokens_details":{"text_tokens":1,"audio_tokens":1,"image_tokens":0,"duration":3}},"output_token_details":{"text_tokens":0,"audio_tokens":1}}}}`,
		},
		{
			name:     "OpenAI image wire counters remain token subsets",
			protocol: usageoutput.OpenAI,
			body:     `{"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18,"image_input_tokens":5,"image_cache_read_tokens":3,"image_output_tokens":7,"images":1,"max_tokens":20}}`,
			want:     `{"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8,"image_input_tokens":2,"image_cache_read_tokens":1,"image_output_tokens":3,"images":1,"max_tokens":20}}`,
		},
		{
			name:     "Anthropic compatible hit and miss aliases",
			protocol: usageoutput.Anthropic,
			body:     `{"usage":{"input_tokens":5,"output_tokens":7,"cache_read_input_tokens":9,"prompt_tokens":14,"prompt_cache_hit_tokens":9,"prompt_cache_miss_tokens":5,"image_output_tokens":3}}`,
			want:     `{"usage":{"input_tokens":2,"output_tokens":3,"cache_read_input_tokens":4,"prompt_tokens":7,"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":2,"image_output_tokens":1}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := []byte(tc.body)
			out, err := usageoutput.ScaleJSON(body, 0.5, usageoutput.Options{Protocol: tc.protocol})
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(out))
			require.Equal(t, tc.body, string(body))
		})
	}
}

func TestPKHiddenBillingOutputPreservesPayloadBoundaries(t *testing.T) {
	t.Parallel()
	const body = `{ "usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}, "text":"usage input_tokens=17", "output":[{"usage":{"input_tokens":17}},{"type":"function_call","arguments":{"usage":{"input_tokens":19}}}], "tools":[{"parameters":{"usage":{"input_tokens":23}}}], "provider_extension":{"usage":{"input_tokens":29}}, "created_at":31, "calls":37, "cost":41, "duration":43 }`
	const want = `{ "usage":{"input_tokens":10,"output_tokens":6,"total_tokens":16}, "text":"usage input_tokens=17", "output":[{"usage":{"input_tokens":17}},{"type":"function_call","arguments":{"usage":{"input_tokens":19}}}], "tools":[{"parameters":{"usage":{"input_tokens":23}}}], "provider_extension":{"usage":{"input_tokens":29}}, "created_at":31, "calls":37, "cost":41, "duration":43 }`
	out, err := usageoutput.ScaleJSON([]byte(body), 2, usageoutput.Options{Protocol: usageoutput.OpenAI})
	require.NoError(t, err)
	require.Equal(t, want, string(out), "only recognized counts may change, including whitespace outside those counts")
	require.False(t, gjson.GetBytes(out, "hidden_rate_multiplier").Exists())
	require.False(t, gjson.GetBytes(out, "usage.hidden_rate_multiplier").Exists())

	for _, unchanged := range []string{
		`{"output":[{"usage":{"input_tokens":5}}],"tools":[{"parameters":{"usage":{"input_tokens":7}}}]}`,
		`{"usage":{"server_tool_use":{"web_search_requests":3},"cost":1.2},"extension":1e1000000}`,
		`{"input_tokens":5,"output_tokens":3,"total_tokens":8}`,
		`{"usage":null,"id":"partial"}`,
		` [DONE] `,
		"\n\t",
	} {
		out, err = usageoutput.ScaleJSON([]byte(unchanged), 2, usageoutput.Options{Protocol: usageoutput.OpenAI})
		require.NoError(t, err)
		require.Equal(t, unchanged, string(out))
	}
}

func TestPKHiddenBillingOutputEnvelopesAndPartialUsage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		body string
		want string
	}{
		{`{"usage":{"output_tokens":3}}`, `{"usage":{"output_tokens":1}}`},
		{`{"usage":{"input_tokens":0,"prompt_tokens":5,"output_tokens":0,"completion_tokens":3,"total_tokens":8}}`, `{"usage":{"input_tokens":0,"prompt_tokens":2,"output_tokens":0,"completion_tokens":1,"total_tokens":4}}`},
		{`{"data":{"response":{"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}}}`, `{"data":{"response":{"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}}}`},
		{`{"data":{"message":{"usage":{"output_tokens":3}}}}`, `{"data":{"message":{"usage":{"output_tokens":1}}}}`},
		{`{"usage":{"input_tokens":null,"output_tokens":3,"total_tokens":9,"input_tokens_details":{"cached_tokens":null},"output_tokens_details":null}}`, `{"usage":{"input_tokens":null,"output_tokens":1,"total_tokens":4,"input_tokens_details":{"cached_tokens":null},"output_tokens_details":null}}`},
		{`{"usage":{"input_tokens":5,"output_tokens":null,"total_tokens":null}}`, `{"usage":{"input_tokens":2,"output_tokens":null,"total_tokens":null}}`},
		{`[{"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}},{"data":{"usage":{"output_tokens":5}}},{"output":[{"usage":{"input_tokens":7}}]}]`, `[{"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}},{"data":{"usage":{"output_tokens":2}}},{"output":[{"usage":{"input_tokens":7}}]}]`},
	} {
		out, err := usageoutput.ScaleJSON([]byte(tc.body), 0.5, usageoutput.Options{Protocol: usageoutput.OpenAI})
		require.NoError(t, err)
		require.JSONEq(t, tc.want, string(out))
	}
	const snake = `{"data":{"usage_metadata":{"prompt_token_count":5,"candidates_token_count":3,"thoughts_token_count":1,"cached_content_token_count":3,"total_token_count":9,"prompt_tokens_details":[{"modality":"TEXT","token_count":5}]}}}`
	out, err := usageoutput.ScaleJSON([]byte(snake), 2, usageoutput.Options{Protocol: usageoutput.Gemini})
	require.NoError(t, err)
	require.JSONEq(t, `{"data":{"usage_metadata":{"prompt_token_count":10,"candidates_token_count":6,"thoughts_token_count":2,"cached_content_token_count":6,"total_token_count":18,"prompt_tokens_details":[{"modality":"TEXT","token_count":10}]}}}`, string(out))
	const unknown = `{"usageMetadata":{"promptTokenCount":null,"candidatesTokenCount":3,"thoughtsTokenCount":null,"totalTokenCount":15,"promptTokensDetails":[{"modality":"TEXT","tokenCount":null}]}}`
	out, err = usageoutput.ScaleJSON([]byte(unknown), 0.5, usageoutput.Options{Protocol: usageoutput.Gemini})
	require.NoError(t, err)
	require.JSONEq(t, `{"usageMetadata":{"promptTokenCount":null,"candidatesTokenCount":1,"thoughtsTokenCount":null,"totalTokenCount":7,"promptTokensDetails":[{"modality":"TEXT","tokenCount":null}]}}`, string(out))
	out, err = usageoutput.ScaleJSON([]byte(`{"usage":{"input_tokens":null,"output_tokens":3}}`), 0, usageoutput.Options{Protocol: usageoutput.Anthropic})
	require.NoError(t, err)
	require.JSONEq(t, `{"usage":{"input_tokens":null,"output_tokens":0}}`, string(out))
}

func TestPKHiddenBillingOutputBareCountsAndImageUsage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		body     string
		want     string
		protocol usageoutput.Protocol
	}{
		{`{"input_tokens":5}`, `{"input_tokens":2}`, usageoutput.Anthropic},
		{`{"totalTokens":5}`, `{"totalTokens":2}`, usageoutput.Gemini},
		{`{"total_tokens":5}`, `{"total_tokens":2}`, usageoutput.OpenAI},
	} {
		out, err := usageoutput.ScaleJSON([]byte(tc.body), 0.5, usageoutput.Options{Protocol: tc.protocol})
		require.NoError(t, err)
		require.Equal(t, tc.body, string(out))
		out, err = usageoutput.ScaleJSON([]byte(tc.body), 0.5, usageoutput.Options{Protocol: tc.protocol, AllowBare: true})
		require.NoError(t, err)
		require.JSONEq(t, tc.want, string(out))
	}
	const body = `{"response":{"tool_usage":{"image_gen":{"images":1,"input_tokens":5,"output_tokens":3,"total_tokens":8,"usage":{"input_tokens":7,"output_tokens":5,"total_tokens":12,"input_tokens_details":{"text_tokens":3,"image_tokens":4}}}},"output":[{"type":"image_generation_call","result":"unchanged","usage":{"input_tokens":99}}]}}`
	out, err := usageoutput.ScaleJSON([]byte(body), 0.5, usageoutput.Options{Protocol: usageoutput.OpenAI})
	require.NoError(t, err)
	require.JSONEq(t, `{"response":{"tool_usage":{"image_gen":{"images":1,"input_tokens":2,"output_tokens":1,"total_tokens":3,"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5,"input_tokens_details":{"text_tokens":1,"image_tokens":2}}}},"output":[{"type":"image_generation_call","result":"unchanged","usage":{"input_tokens":99}}]}}`, string(out))
}

func TestPKHiddenBillingOutputTotalsRespectOriginalRelationships(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		protocol usageoutput.Protocol
		body     string
		total    int64
	}{
		{"OpenAI reasoning included", usageoutput.OpenAI, `{"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8,"output_tokens_details":{"reasoning_tokens":3}}}`, 3},
		{"OpenAI small nonstandard total", usageoutput.OpenAI, `{"usage":{"input_tokens":7,"output_tokens":5,"total_tokens":3}}`, 1},
		{"Anthropic total excludes cache", usageoutput.Anthropic, `{"usage":{"input_tokens":5,"output_tokens":3,"cache_creation_input_tokens":11,"cache_read_input_tokens":13,"total_tokens":8}}`, 3},
		{"Anthropic only creation details", usageoutput.Anthropic, `{"usage":{"input_tokens":5,"output_tokens":3,"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":3},"total_tokens":14}}`, 5},
		{"Gemini thoughts are candidate subset", usageoutput.Gemini, `{"usage":{"promptTokenCount":5,"candidatesTokenCount":3,"thoughtsTokenCount":1,"cachedContentTokenCount":3,"totalTokenCount":8}}`, 3},
		{"Gemini tool prompt included separately", usageoutput.Gemini, `{"usage":{"promptTokenCount":5,"candidatesTokenCount":3,"thoughtsTokenCount":1,"toolUsePromptTokenCount":3,"totalTokenCount":11}}`, 4},
		{"Gemini residual still counted", usageoutput.Gemini, `{"usage":{"promptTokenCount":5,"candidatesTokenCount":3,"thoughtsTokenCount":3,"totalTokenCount":14}}`, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := usageoutput.ScaleJSON([]byte(tc.body), 0.5, usageoutput.Options{Protocol: tc.protocol})
			require.NoError(t, err)
			path := "usage.total_tokens"
			if tc.protocol == usageoutput.Gemini {
				path = "usage.totalTokenCount"
			}
			require.Equal(t, tc.total, gjson.GetBytes(out, path).Int())
		})
	}
}

func TestPKHiddenBillingOutputRejectsInvalidAndOverflow(t *testing.T) {
	t.Parallel()
	for _, multiplier := range []float64{-1, 1000.000001, math.NaN(), math.Inf(1), math.Inf(-1), 0.0000001} {
		out, err := usageoutput.ScaleJSON([]byte(`{"usage":{"input_tokens":1}}`), multiplier, usageoutput.Options{Protocol: usageoutput.OpenAI})
		require.Error(t, err)
		require.Nil(t, out)
	}
	for _, body := range []string{
		`{"usage":{"input_tokens":-1}}`,
		`{"usage":{"input_tokens":1.5}}`,
		`{"usage":{"input_tokens":"12"}}`,
		`{"usage":{"input_tokens":true}}`,
		`{"usage":{"input_tokens":9223372036854775807}}`,
		`{"usage":{"input_tokens":9223372036854775808}}`,
		`{"usage":{"input_tokens":1e1000000}}`,
		`{"usage":{"input_tokens":1e-1000000}}`,
		`{"usage":{"input_tokens":1,"output_tokens_details":{"reasoning_tokens":-1}}}`,
		`{"usage":`,
	} {
		original := []byte(body)
		out, err := usageoutput.ScaleJSON(original, 2, usageoutput.Options{Protocol: usageoutput.OpenAI})
		require.Error(t, err, body)
		require.Nil(t, out, "invalid tokens must not leak an original/partially scaled payload")
		require.Equal(t, body, string(original))
	}
	out, err := usageoutput.ScaleJSON([]byte(`{"usage":{"input_tokens":4611686018427387903}}`), 2, usageoutput.Options{Protocol: usageoutput.OpenAI})
	require.NoError(t, err)
	require.Equal(t, int64(9223372036854775806), gjson.GetBytes(out, "usage.input_tokens").Int())
	out, err = usageoutput.ScaleJSON([]byte(`{"usageMetadata":{"promptTokensDetails":[{"tokenCount":-1}]}}`), 2, usageoutput.Options{Protocol: usageoutput.Gemini})
	require.Error(t, err)
	require.Nil(t, out)
}

func TestPKHiddenBillingOutputAfterCacheConversion(t *testing.T) {
	t.Parallel()
	const source = `{"usage":{"input_tokens":23,"output_tokens":13,"total_tokens":36,"cache_creation_input_tokens":5,"input_tokens_details":{"cached_tokens":7,"cache_write_tokens":5}}}`
	original := []byte(source)
	converted, err := cacheconversion.RewriteJSON(original, cacheconversion.Config{Enabled: true, Multiplier: 0.5}, cacheconversion.OpenAI)
	require.NoError(t, err)
	require.Equal(t, int64(20), gjson.GetBytes(converted, "usage.input_tokens").Int())
	require.Equal(t, int64(9), gjson.GetBytes(converted, "usage.input_tokens_details.cached_tokens").Int())
	convertedSnapshot := string(converted)
	out, err := usageoutput.ScaleJSON(converted, 2, usageoutput.Options{Protocol: usageoutput.OpenAI})
	require.NoError(t, err)
	require.JSONEq(t, `{"usage":{"input_tokens":40,"output_tokens":26,"total_tokens":66,"cache_creation_input_tokens":0,"input_tokens_details":{"cached_tokens":18,"cache_write_tokens":0}}}`, string(out))
	require.Equal(t, source, string(original))
	require.Equal(t, convertedSnapshot, string(converted), "billing still observes only the original cache conversion")
}

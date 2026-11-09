//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cacheconversion"
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

func TestPKCacheConversionDeepSeekReadAlias(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{
		AccountExtraCacheConversionEnabled:    true,
		AccountExtraCacheConversionMultiplier: 0.5,
	}}
	for _, tt := range []struct {
		name     string
		extra    string
		wantRead int
	}{
		{name: "DeepSeek alias", wantRead: 7},
		{name: "canonical nested zero wins", extra: `,"prompt_tokens_details":{"cached_tokens":0}`, wantRead: 0},
		{name: "existing root alias wins", extra: `,"cache_read_input_tokens":3`, wantRead: 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"usage":{"prompt_tokens":23,"completion_tokens":13,"total_tokens":36,"cache_creation_input_tokens":5,"prompt_cache_hit_tokens":7` + tt.extra + `},"text":"keep 原文"}`)
			originalUsage := gjson.GetBytes(body, "usage")
			require.Equal(t, tt.wantRead, openAICacheReadTokensFromUsage(originalUsage))
			converted, err := RewriteOpenAICacheConversionJSON(account, body)
			require.NoError(t, err)
			require.EqualValues(t, tt.wantRead+2, gjson.GetBytes(converted, "usage.prompt_cache_hit_tokens").Int())
			require.EqualValues(t, tt.wantRead+2, gjson.GetBytes(converted, "usage.prompt_tokens_details.cached_tokens").Int())
			require.EqualValues(t, 0, gjson.GetBytes(converted, "usage.cache_creation_input_tokens").Int())
			require.EqualValues(t, 20, gjson.GetBytes(converted, "usage.prompt_tokens").Int())
			require.EqualValues(t, 33, gjson.GetBytes(converted, "usage.total_tokens").Int())
			require.Equal(t, "keep 原文", gjson.GetBytes(converted, "text").String())
			require.EqualValues(t, 7, gjson.GetBytes(body, "usage.prompt_cache_hit_tokens").Int(), "original billing evidence must remain unchanged")
			disabled, err := RewriteOpenAICacheConversionJSON(nil, body)
			require.NoError(t, err)
			require.Equal(t, body, disabled)
			frame := ": keepalive\nevent: response.completed\ndata: " + string(body) + "\n\ndata: [DONE]\n\n"
			sse, err := rewriteCacheConvertedSSE(account, frame, cacheconversion.OpenAI)
			require.NoError(t, err)
			require.Equal(t, ": keepalive\nevent: response.completed\ndata: "+string(converted)+"\n\ndata: [DONE]\n\n", sse)
		})
	}
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

type pkEarlyBillingCache struct {
	*pkCacheConversionInflightCache
	subscriptionCosts chan float64
}

func (c *pkEarlyBillingCache) UpdateSubscriptionUsage(_ context.Context, _, _ int64, cost float64) error {
	c.subscriptionCosts <- cost
	return nil
}

type pkEarlyBillingSnapshot struct {
	costBits         []uint64
	command          UsageBillingCommand
	accountCostBits  uint64
	balanceBits      uint64
	rateLimitBits    uint64
	subscriptionBits uint64
}

// Exercise the emitted wire through real protocol readers before recording the
// observed usage. This deliberately separates reader/accounting parity from the
// handler's decision to submit a partial result; handler regressions cover that
// decision and retries. No provider endpoint or live model is used here.
func TestPKEarlyHeartbeat_WireUsageAndBillingParity(t *testing.T) {
	const model = "gpt-5.4"
	const startingBalance = 100.0
	const customerRate = 1.7
	const accountRate = 1.3
	raw := OpenAIUsage{InputTokens: 1200, OutputTokens: 13, CacheCreationInputTokens: 200, CacheReadInputTokens: 100}
	pricingAt := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	readers := []struct {
		name     string
		protocol string
		run      func(*OpenAIGatewayService, *gin.Context, *http.Response, *Account) (*OpenAIUsage, error)
	}{
		{"responses", "responses", func(s *OpenAIGatewayService, c *gin.Context, r *http.Response, a *Account) (*OpenAIUsage, error) {
			result, err := s.handleStreamingResponse(c.Request.Context(), r, c, a, time.Now(), model, model)
			if result == nil {
				return nil, err
			}
			return result.usage, err
		}},
		{"responses_passthrough", "responses", func(s *OpenAIGatewayService, c *gin.Context, r *http.Response, a *Account) (*OpenAIUsage, error) {
			result, err := s.handleStreamingResponsePassthrough(c.Request.Context(), r, c, a, time.Now(), model, model)
			if result == nil {
				return nil, err
			}
			return result.usage, err
		}},
		{"chat_raw", "chat", func(s *OpenAIGatewayService, c *gin.Context, r *http.Response, a *Account) (*OpenAIUsage, error) {
			result, err := s.streamRawChatCompletions(c, r, a, model, model, model, nil, nil, time.Now(), 0)
			if result == nil {
				return nil, err
			}
			return &result.Usage, err
		}},
		{"messages_native", "messages", func(s *OpenAIGatewayService, c *gin.Context, r *http.Response, a *Account) (*OpenAIUsage, error) {
			result, err := s.handleNativeAnthropicStreamingResponse(c.Request.Context(), r, c, a, model, model, model, nil, time.Now())
			if result == nil {
				return nil, err
			}
			return &result.Usage, err
		}},
	}
	for _, reader := range readers {
		for _, truncated := range []bool{false, true} {
			for _, subscriptionBilling := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/without_completion_suffix=%t/subscription=%t", reader.name, truncated, subscriptionBilling), func(t *testing.T) {
					var baseline *pkEarlyBillingSnapshot
					for _, mode := range []string{"disabled", StreamEarlyHeartbeatModeComment, StreamEarlyHeartbeatModeDownstreamPing, StreamEarlyHeartbeatModeSpace} {
						t.Run(mode, func(t *testing.T) {
							wire, timingCtx := pkEarlyBillingWire(t, reader.protocol, mode, truncated)
							accountMultiplier := accountRate
							account := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: &accountMultiplier, Extra: map[string]any{
								AccountExtraCacheConversionEnabled: false, AccountExtraCacheConversionMultiplier: 999.0,
								AccountExtraKiloBridgeEnabled: false, "quota_limit": 100.0,
							}}
							readerCtx, _ := newCompactBridgeTestContext(t, false)
							readerCtx.Request = httptest.NewRequest(http.MethodPost, pkEarlyBillingPath(reader.protocol), nil)
							response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": []string{"pk-early-billing"}}, Body: io.NopCloser(strings.NewReader(wire))}
							observed, readErr := reader.run(newCompactBridgeTestService(), readerCtx, response, account)
							if truncated && reader.protocol != "chat" {
								require.Error(t, readErr, "a placeholder must not turn an incomplete upstream stream into success")
							} else {
								// Raw Chat's upstream contract treats a usage object as a terminal
								// signal even without finish_reason/[DONE]; preserve that policy.
								require.NoError(t, readErr)
							}
							require.NotNil(t, observed)
							// Protection metadata describes terminal evidence independently of
							// the original counters. Native Anthropic does not infer that evidence.
							expectedUsage := raw
							expectedUsage.CacheReadKnown = !truncated && reader.protocol != "messages"
							require.Equal(t, expectedUsage, *observed, "only original provider usage may reach accounting, including progressive + terminal usage")

							cache := &pkEarlyBillingCache{
								pkCacheConversionInflightCache: &pkCacheConversionInflightCache{memInflightCache: newMemInflightCache(startingBalance), rateLimitCosts: make(chan float64, 2)},
								subscriptionCosts:              make(chan float64, 2),
							}
							billingCache := newInflightSvc(t, cache, 60)
							usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
							billingRepo := &openAIRecordUsageBillingRepoStub{}
							svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, nil, nil, nil)
							svc.billingCacheService = billingCache
							svc.cfg.Default.RateMultiplier = customerRate
							svc.cfg.Billing.InflightReservation = billingCache.cfg.Billing.InflightReservation
							// Fix the price card and pricing instant independently of live model metadata.
							svc.billingService.fallbackPrices[model] = &ModelPricing{
								InputPricePerToken: 2e-6, OutputPricePerToken: 10e-6,
								CacheCreationPricePerToken: 3e-6, CacheReadPricePerToken: 0.1e-6,
							}
							group := &Group{ID: 4, Platform: PlatformOpenAI, RateMultiplier: customerRate}
							user := &User{ID: 1, Balance: startingBalance}
							key := &APIKey{ID: 2, User: user, GroupID: &group.ID, Group: group, Quota: 100, RateLimit5h: 100}
							var subscription *UserSubscription
							if subscriptionBilling {
								group.SubscriptionType = SubscriptionTypeSubscription
								subscription = &UserSubscription{ID: 5}
							}
							upstreamTTFT := 21920
							result := &OpenAIForwardResult{RequestID: "pk-early-billing", Model: model, UpstreamModel: model, Usage: *observed, Stream: true, FirstTokenMs: &upstreamTTFT}
							require.NoError(t, svc.RecordUsage(timingCtx, &OpenAIRecordUsageInput{
								Result: result, APIKey: key, User: user, Account: account, Subscription: subscription,
								APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}, PricingAt: pricingAt,
							}))
							billingCache.Stop() // Wait for the real cache-update queue before observing it.
							require.Equal(t, 1, usageRepo.calls)
							require.Equal(t, 1, billingRepo.calls)
							log, command := usageRepo.lastLog, billingRepo.lastCmd
							require.NotNil(t, log)
							require.NotNil(t, command)
							require.Equal(t, &upstreamTTFT, log.FirstTokenMs)
							if mode == "disabled" {
								require.Equal(t, log.FirstTokenMs, log.FirstResponseMs, "disabled uses the original upstream TTFT")
							} else {
								require.Equal(t, FirstResponseMsFromContext(timingCtx), log.FirstResponseMs)
								require.NotNil(t, log.FirstResponseMs)
							}
							require.Equal(t, expectedUsage, result.Usage, "accounting must preserve raw counters and protection evidence")
							require.Equal(t, 900, log.InputTokens)
							require.Equal(t, 200, log.CacheCreationTokens)
							require.Equal(t, 100, log.CacheReadTokens)
							require.InDelta(t, 900*2e-6+13*10e-6+200*3e-6+100*0.1e-6, log.TotalCost, 1e-15)
							require.InDelta(t, (900*2e-6+13*10e-6+200*3e-6+100*0.1e-6)*customerRate, log.ActualCost, 1e-15)
							require.NotNil(t, log.AccountRateMultiplier)
							require.Equal(t, accountRate, *log.AccountRateMultiplier)
							require.Nil(t, log.AccountStatsCost, "without channel pricing, account statistics use TotalCost times AccountRateMultiplier")
							require.Equal(t, QuantizeUsageBillingAmount(log.ActualCost), command.APIKeyQuotaCost)
							require.Equal(t, command.APIKeyQuotaCost, command.APIKeyRateLimitCost)
							require.Equal(t, QuantizeUsageBillingAmount(log.TotalCost*accountRate), command.AccountQuotaCost)
							require.Len(t, cache.rateLimitCosts, 1)
							balance, err := cache.GetUserBalance(context.Background(), user.ID)
							require.NoError(t, err)
							snapshot := pkEarlyBillingSnapshot{
								command: *command, accountCostBits: math.Float64bits(log.TotalCost * *log.AccountRateMultiplier),
								balanceBits: math.Float64bits(balance), rateLimitBits: math.Float64bits(<-cache.rateLimitCosts),
							}
							for _, cost := range []float64{log.InputCost, log.OutputCost, log.CacheCreationCost, log.CacheReadCost, log.TotalCost, log.ActualCost} {
								snapshot.costBits = append(snapshot.costBits, math.Float64bits(cost))
							}
							if subscriptionBilling {
								require.Zero(t, command.BalanceCost)
								require.Equal(t, command.APIKeyQuotaCost, command.SubscriptionCost)
								require.Equal(t, startingBalance, balance)
								require.Zero(t, cache.deducts.Load())
								require.Len(t, cache.subscriptionCosts, 1)
								snapshot.subscriptionBits = math.Float64bits(<-cache.subscriptionCosts)
								require.Equal(t, math.Float64bits(log.ActualCost), snapshot.subscriptionBits)
							} else {
								require.Zero(t, command.SubscriptionCost)
								require.Equal(t, command.APIKeyQuotaCost, command.BalanceCost)
								require.Equal(t, int32(1), cache.deducts.Load())
								require.Equal(t, startingBalance-log.ActualCost, balance)
								require.Empty(t, cache.subscriptionCosts)
							}
							require.Equal(t, math.Float64bits(log.ActualCost), snapshot.rateLimitBits)
							if baseline == nil {
								baseline = &snapshot
							} else {
								require.Equal(t, *baseline, snapshot, "heartbeat mode must not change any float bit, settlement field, dedup fingerprint or cache deduction")
							}
						})
					}
				})
			}
		}
	}
}

func pkEarlyBillingWire(t *testing.T, protocol, mode string, truncated bool) (string, context.Context) {
	t.Helper()
	c, rec := newCompactBridgeTestContext(t, false)
	c.Request = httptest.NewRequest(http.MethodPost, pkEarlyBillingPath(protocol), nil)
	c.Header("Content-Type", "text/event-stream")
	stop := startEarlySSEHeartbeat(c, time.Now(), protocol, StreamEarlyHeartbeatSettings{
		Enabled: mode != "disabled", Mode: mode, MinDelayMS: 30000, MaxDelayMS: 30000,
	}, earlySpaceRequest{model: "gpt-5.4"})
	t.Cleanup(stop)
	if mode != "disabled" {
		firePKEarlyHeartbeat(t, earlySSEWriterFromContext(c))
	}
	if mode == StreamEarlyHeartbeatModeComment || (mode == StreamEarlyHeartbeatModeSpace && protocol == "messages") {
		require.Equal(t, ": keepalive\n\n", rec.Body.String(), "comment and Messages-space fallback must actually emit a heartbeat")
	} else if mode == StreamEarlyHeartbeatModeDownstreamPing {
		want := "data: {\"type\":\"ping\"}\n\n"
		if protocol == "messages" {
			want = "event: ping\n" + want
		}
		require.Equal(t, want, rec.Body.String(), "ping must actually be emitted before model output")
	}
	usage := `{"input_tokens":1200,"output_tokens":13,"total_tokens":1213,"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":200}}`
	var frames []string
	switch protocol {
	case "chat":
		chatUsage := `{"prompt_tokens":1200,"completion_tokens":13,"total_tokens":1213,"prompt_tokens_details":{"cached_tokens":100,"cache_write_tokens":200}}`
		frames = []string{"data: {\"id\":\"chat_real\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}],\"usage\":" + chatUsage + "}\n\n"}
		if !truncated {
			frames = append(frames, "data: {\"id\":\"chat_real\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":"+chatUsage+"}\n\n", "data: [DONE]\n\n")
		}
	case "messages":
		frames = []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_real\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"gpt-5.4\",\"content\":[],\"usage\":{\"input_tokens\":900,\"output_tokens\":0,\"cache_creation_input_tokens\":200,\"cache_read_input_tokens\":100}}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
			// The upstream Anthropic parser observes output usage on message_delta,
			// not message_start. Supply the same measured output before either EOF
			// or message_stop; a missing stop must still be reported as incomplete.
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":null},\"usage\":{\"output_tokens\":13}}\n\n",
		}
		if !truncated {
			frames = append(frames, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":13}}\n\n", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		}
	default:
		item := `{"id":"msg_real","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}`
		frames = []string{
			"event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_real\",\"model\":\"gpt-5.4\",\"created_at\":17,\"output\":[]}}\n\n",
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"id\":\"msg_real\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"in_progress\",\"content\":[]}}\n\n",
			"event: response.content_part.added\ndata: {\"type\":\"response.content_part.added\",\"sequence_number\":2,\"item_id\":\"msg_real\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\",\"annotations\":[]}}\n\n",
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":3,\"item_id\":\"msg_real\",\"output_index\":0,\"content_index\":0,\"delta\":\"hello\",\"usage\":" + usage + "}\n\n",
		}
		if !truncated {
			frames = append(frames, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"sequence_number\":4,\"output_index\":0,\"item\":"+item+"}\n\n", "event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":5,\"response\":{\"id\":\"resp_real\",\"status\":\"completed\",\"model\":\"gpt-5.4\",\"output\":["+item+"],\"usage\":"+usage+"}}\n\n")
		}
	}
	for _, frame := range frames {
		_, err := c.Writer.WriteString(frame)
		require.NoError(t, err)
	}
	stop()
	if mode == StreamEarlyHeartbeatModeSpace && protocol != "messages" {
		foundSpace := false
		forEachOpenAISSEFrame(rec.Body.String(), func(_ string, data []byte) {
			v := gjson.ParseBytes(data)
			if v.Get("delta").String() == " " || v.Get("choices.0.delta.content").String() == " " {
				foundSpace = true
				require.False(t, v.Get("usage").Exists(), "placeholder output must not invent usage")
			}
		})
		require.True(t, foundSpace, "space case must exercise an actually emitted placeholder")
	}
	return rec.Body.String(), CopyFirstResponseTiming(c.Request.Context(), context.Background())
}

func pkEarlyBillingPath(protocol string) string {
	if protocol == "chat" {
		return "/v1/chat/completions"
	}
	return "/v1/" + protocol
}

func TestOpenAIUsageProtectionExcludesRequestMediaWithoutTokenBreakdown(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ws         bool
		converted  bool
	}{
		{"responses image", `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.invalid/image.png"}]}]}`, false, false},
		{"responses tool output image", `{"input":[{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_image","file_id":"file_example"}]}]}`, false, false},
		{"chat image conversion", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.invalid/image.png"}}]},{"role":"user","content":"describe it"}]}`, false, true},
		{"messages image conversion", `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`, false, true},
		{"chat audio input", `{"messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"AA==","format":"wav"}}]}]}`, false, false},
		{"audio output", `{"messages":[{"role":"user","content":"hello"}],"modalities":["text","audio"],"audio":{"voice":"alloy","format":"wav"}}`, false, false},
		{"video input", `{"messages":[{"role":"user","content":[{"type":"video_url","video_url":{"url":"https://example.invalid/video.mp4"}}]}]}`, false, false},
		{"websocket flat image", `{"type":"response.create","input":[{"role":"user","content":[{"type":"input_image","file_id":"file_example"}]}]}`, true, false},
		{"websocket wrapped image", `{"type":"response.create","response":{"input":[{"role":"user","content":[{"type":"input_image","file_id":"file_example"}]}]}}`, true, false},
		{"responses opaque previous turn", `{"previous_response_id":"resp_example","input":"describe the previous picture"}`, false, false},
		{"responses opaque conversation", `{"conversation":"conv_example","input":"continue"}`, false, false},
		{"responses opaque conversation object", `{"conversation":{"id":"conv_example"},"input":"continue"}`, false, false},
		{"chat opaque conversation", `{"conversation_id":"conv_example","messages":[{"role":"user","content":"continue"}]}`, false, true},
		{"opaque thread", `{"thread_id":"thread_example","input":"continue"}`, false, false},
		{"websocket flat opaque previous turn", `{"type":"response.create","previous_response_id":"resp_example","input":"continue"}`, true, false},
		{"websocket wrapped opaque previous turn", `{"type":"response.create","response":{"previous_response_id":"resp_example","input":"continue"}}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			protection, redis := antiDistillationTestService(t)
			ctx := context.Background()
			require.NoError(t, protection.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5}))
			svc := &OpenAIGatewayService{groupAntiDistillation: protection}
			key := &APIKey{UserID: 7, Group: &Group{ID: 1, Platform: PlatformOpenAI}}
			account := &Account{Platform: PlatformOpenAI}
			// A valid upstream counter may omit image/audio breakdowns entirely.
			usageJSON := []byte(`{"input_tokens":2000,"output_tokens":20,"input_tokens_details":{"cached_tokens":0}}`)
			usage, ok := extractOpenAIUsageFromJSONBytes(append(append([]byte(`{"usage":`), usageJSON...), '}'))
			require.True(t, ok)
			require.True(t, usage.CacheReadKnown)
			if tc.converted {
				var typed apicompat.ResponsesUsage
				require.NoError(t, json.Unmarshal(usageJSON, &typed))
				usage = copyOpenAIUsageFromResponsesUsage(&typed, usage)
			}
			for i := 0; i < 20; i++ {
				result := &OpenAIForwardResult{Usage: usage, OpenAIWSMode: tc.ws, UpstreamTerminalEvent: "response.completed"}
				require.True(t, openAIGroupAntiDistillationUsageEligible(result), "usage alone cannot identify the media request")
				svc.ObserveGroupAntiDistillationUsage(ctx, key, account, result, time.Now(), []byte(tc.body))
			}
			require.Empty(t, redis.Keys(), "media or unknown history must not create protection samples or pause the user")
		})
	}
}

func TestOpenAIUsageProtectionStillSamplesTextWithMediaNamesInStringsOrTools(t *testing.T) {
	for _, body := range []string{
		`{"input":"explain the input_image and audio fields"}`,
		`{"messages":[{"role":"user","content":[{"type":"text","text":"input_image"}]}],"tools":[{"type":"function","function":{"name":"example","parameters":{"type":"object","properties":{"image_url":{"type":"string"}}}}}]}`,
		`{"type":"response.create","response":{"input":[{"type":"function_call_output","call_id":"call_1","output":"{\"type\":\"input_image\"}"}]}}`,
		`{"previous_response_id":null,"conversation":null,"session_id":"session_example","prompt_cache_key":"cache_example","input":"hello"}`,
	} {
		t.Run(body, func(t *testing.T) {
			protection, cache := antiDistillationTestService(t)
			ctx := context.Background()
			require.NoError(t, protection.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5}))
			svc := &OpenAIGatewayService{groupAntiDistillation: protection}
			key := &APIKey{UserID: 7, Group: &Group{ID: 1, Platform: PlatformOpenAI}}
			for i := 0; i < 20; i++ {
				result := &OpenAIForwardResult{Usage: OpenAIUsage{InputTokens: 2000, CacheReadKnown: true}}
				svc.ObserveGroupAntiDistillationUsage(ctx, key, &Account{Platform: PlatformOpenAI}, result, time.Now(), []byte(body))
			}
			require.Len(t, cache.Observations(), 20, "text requests still reach the protection cache; atomic 20/18 enforcement is covered by repository adapter tests")
		})
	}
}

func TestOpenAIUsageProtectionRequiresExplicitValidCacheCounter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, usage string
		known       bool
		tokens      int
	}{
		{"missing", `{"input_tokens":2000}`, false, 0},
		{"zero", `{"input_tokens":2000,"input_tokens_details":{"cached_tokens":0}}`, true, 0},
		{"positive", `{"input_tokens":2000,"prompt_tokens_details":{"cached_tokens":1280}}`, true, 1280},
		{"null", `{"input_tokens":2000,"input_tokens_details":{"cached_tokens":null}}`, false, 0},
		{"string", `{"input_tokens":2000,"input_tokens_details":{"cached_tokens":"0"}}`, false, 0},
		{"negative", `{"input_tokens":2000,"input_tokens_details":{"cached_tokens":-1}}`, false, 0},
		{"fractional", `{"input_tokens":2000,"input_tokens_details":{"cached_tokens":0.5}}`, false, 0},
		{"canonical zero wins", `{"input_tokens":2000,"input_tokens_details":{"cached_tokens":0},"cache_read_input_tokens":1280}`, true, 0},
		{"legacy explicit zero", `{"input_tokens":2000,"cache_read_input_tokens":0}`, true, 0},
		{"conflicting aliases", `{"input_tokens":2000,"cache_read_input_tokens":0,"cached_tokens":1280}`, false, 1280},
		{"legacy matching aliases", `{"input_tokens":2000,"cache_read_input_tokens":0,"cached_tokens":0}`, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage, ok := extractOpenAIUsageFromJSONBytes([]byte(`{"usage":` + tc.usage + `}`))
			require.True(t, ok)
			require.Equal(t, tc.known, usage.CacheReadKnown)
			require.Equal(t, tc.tokens, usage.CacheReadInputTokens)
		})
	}
}

func TestOpenAIUsageProtectionOnlyUsesSuccessfulTerminalEvidence(t *testing.T) {
	t.Parallel()
	svc := &OpenAIGatewayService{}
	for _, tc := range []struct {
		name, event, payload string
		known                bool
	}{
		{"completed zero", "response.completed", `{"response":{"usage":{"input_tokens":2000,"input_tokens_details":{"cached_tokens":0}}}}`, true},
		{"completed missing cache", "response.completed", `{"response":{"usage":{"input_tokens":2000}}}`, false},
		{"terminal missing usage", "response.completed", `{"response":{"id":"resp_1"}}`, false},
		{"failed", "response.failed", `{"response":{"usage":{"input_tokens":2000,"input_tokens_details":{"cached_tokens":0}}}}`, false},
		{"incomplete", "response.incomplete", `{"response":{"usage":{"input_tokens":2000,"input_tokens_details":{"cached_tokens":0}}}}`, false},
		{"progressive", "response.in_progress", `{"type":"response.in_progress","usage":{"input_tokens":2000,"input_tokens_details":{"cached_tokens":0}}}`, false},
		{"conflicting status", "response.completed", `{"response":{"status":"incomplete","usage":{"input_tokens":2000,"input_tokens_details":{"cached_tokens":0}}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := OpenAIUsage{InputTokens: 2000, CacheReadInputTokens: 500, CacheReadKnown: true}
			svc.parseSSEUsageBytesWithType([]byte(tc.payload), tc.event, &usage)
			require.Equal(t, tc.known, usage.CacheReadKnown)
		})
	}
}

func TestOpenAIUsageProtectionMetadataDoesNotLeakToClients(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(OpenAIUsage{CacheReadKnown: true, InputTokens: 2000})
	require.NoError(t, err)
	require.NotContains(t, string(data), "Known")
	require.NotContains(t, string(data), "known")
}

func TestOpenAIUsageProtectionExcludesPartialAndNonTextRequests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*OpenAIForwardResult)
	}{
		{"unknown cache", func(r *OpenAIForwardResult) { r.Usage.CacheReadKnown = false }},
		{"missing usage", func(r *OpenAIForwardResult) { r.Usage = OpenAIUsage{} }},
		{"disconnect", func(r *OpenAIForwardResult) { r.ClientDisconnect = true }},
		{"failed websocket", func(r *OpenAIForwardResult) { r.OpenAIWSMode = true; r.UpstreamTerminalEvent = "response.failed" }},
		{"image", func(r *OpenAIForwardResult) { r.ImageCount = 1 }},
		{"image input", func(r *OpenAIForwardResult) { r.Usage.ImageInputTokens = 1 }},
		{"video", func(r *OpenAIForwardResult) { r.VideoCount = 1 }},
		{"zero input", func(r *OpenAIForwardResult) { r.Usage.InputTokens = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &OpenAIForwardResult{Usage: OpenAIUsage{InputTokens: 2000, CacheReadKnown: true}}
			require.True(t, openAIGroupAntiDistillationUsageEligible(r))
			tc.change(r)
			require.False(t, openAIGroupAntiDistillationUsageEligible(r))
		})
	}
}

func TestOpenAIUsageProtectionTypedConversionDoesNotMakeImageRequestsText(t *testing.T) {
	t.Parallel()
	for _, details := range []string{
		`"input_tokens_details":{"cached_tokens":0,"image_tokens":100}`,
		`"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"image_tokens":100}`,
	} {
		raw := []byte(`{"input_tokens":2000,"output_tokens":100,` + details + `}`)
		observed, ok := extractOpenAIUsageFromJSONBytes(append(append([]byte(`{"usage":`), raw...), '}'))
		require.True(t, ok)
		require.True(t, observed.CacheReadKnown)
		var typed apicompat.ResponsesUsage
		require.NoError(t, json.Unmarshal(raw, &typed))
		converted := copyOpenAIUsageFromResponsesUsage(&typed, observed)
		require.False(t, converted.CacheReadKnown, "typed conversion must not turn known image usage into a text protection sample")
		require.False(t, openAIGroupAntiDistillationUsageEligible(&OpenAIForwardResult{Usage: converted}))
	}
}

func TestOpenAIUsageProtectionCCFallbackRequiresCompletedGeneration(t *testing.T) {
	for _, protocol := range []string{"responses", "messages"} {
		for _, tc := range []struct {
			name, delta, finish, suffix string
			known                       bool
		}{
			{"completed", `"content":"ok"`, "stop", "data: [DONE]\n\n", true},
			{"missing upstream terminal", `"content":"ok"`, "stop", "", false},
			{"reasoning failure", `"reasoning_content":"unfinished"`, "length", "data: [DONE]\n\n", false},
			{"incomplete text", `"content":"partial"`, "length", "data: [DONE]\n\n", false},
		} {
			if protocol == "messages" && (tc.name == "reasoning failure" || tc.name == "incomplete text") {
				continue
			}
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				body := `data: {"choices":[{"index":0,"delta":{` + tc.delta + `},"finish_reason":"` + tc.finish + `"}]}` + "\n\n" +
					`data: {"choices":[],"usage":{"prompt_tokens":2000,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":0}}}` + "\n\n" + tc.suffix
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
				var result *OpenAIForwardResult
				var err error
				if protocol == "responses" {
					result, err = svc.streamChatCompletionsAsResponses(c, resp, "gpt-5.4", nil, nil, false, nil, "gpt-5.4", "gpt-5.4", nil, nil, time.Now(), forceChatResponsesFallbackAccount())
				} else {
					result, err = svc.streamChatCompletionsAsAnthropic(c, resp, "gpt-5.4", "gpt-5.4", "gpt-5.4", nil, nil, time.Now(), forceChatResponsesFallbackAccount())
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, tc.known, result.Usage.CacheReadKnown)
			})
		}
	}
}

func TestOpenAIUsageProtectionNativeSSETerminalCompatibility(t *testing.T) {
	for _, tc := range []struct{ name, eventLine, terminal string }{
		{"legacy done payload", "", `{"type":"response.done","response":{"status":"completed","usage":{"input_tokens":2000,"output_tokens":20,"input_tokens_details":{"cached_tokens":0}}}}`},
		{"completed event field", "event: response.completed\n", `{"response":{"status":"completed","usage":{"input_tokens":2000,"output_tokens":20,"input_tokens_details":{"cached_tokens":0}}}}`},
		{"done event field", "event: response.done\n", `{"response":{"status":"completed","usage":{"input_tokens":2000,"output_tokens":20,"input_tokens_details":{"cached_tokens":0}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			body := "data: " + `{"type":"response.output_text.delta","delta":"ok"}` + "\n\n" + tc.eventLine + "data: " + tc.terminal + "\n\n"
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
			result, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, time.Now(), "gpt-5.4", "gpt-5.4")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, result.usage)
			require.True(t, result.usage.CacheReadKnown, "a successful terminal must retain explicit cache statistics without needing a DONE sentinel")
			require.True(t, openAIGroupAntiDistillationUsageEligible(&OpenAIForwardResult{Usage: *result.usage}))
		})
	}
}

func TestOpenAIUsageProtectionNativeSSERetainsProgressiveMediaEvidence(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	terminal := `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2000,"output_tokens":20,"input_tokens_details":{"cached_tokens":0}}}}`
	body := "data: " + `{"type":"response.in_progress","usage":{"input_tokens":2000,"input_tokens_details":{"cached_tokens":0,"image_tokens":100}}}` + "\n\n" +
		"data: " + `{"type":"response.output_text.delta","delta":"ok"}` + "\n\n" + "data: " + terminal + "\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	result, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, time.Now(), "gpt-5.4", "gpt-5.4")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.usage.cacheReadMediaObserved)
	require.False(t, result.usage.CacheReadKnown)
	require.Zero(t, result.usage.ImageInputTokens, "protection must not alter terminal billing token counters")
	require.False(t, openAIGroupAntiDistillationUsageEligible(&OpenAIForwardResult{Usage: *result.usage}))
	// Buffered conversion may read repeated terminal frames. They must not erase
	// media evidence retained from an earlier progressive frame either.
	buffered := svc.parseSSEUsageFromBody(body + "data: " + terminal + "\n\n")
	require.True(t, buffered.cacheReadMediaObserved)
	require.False(t, buffered.CacheReadKnown)
	var typed apicompat.ResponsesStreamEvent
	require.NoError(t, json.Unmarshal([]byte(terminal), &typed))
	converted := copyOpenAIUsageFromResponsesUsage(typed.Response.Usage, *buffered)
	require.True(t, converted.cacheReadMediaObserved)
	require.False(t, openAIGroupAntiDistillationUsageEligible(&OpenAIForwardResult{Usage: converted}))
}

func TestOpenAIUsageProtectionWSPoolRetainsProgressiveMediaEvidence(t *testing.T) {
	usage := &OpenAIUsage{}
	parseOpenAIWSResponseUsageFromCompletedEvent([]byte(`{"type":"response.in_progress","usage":{"input_tokens":2000,"input_tokens_details":{"cached_tokens":0,"image_tokens":100}}}`), usage)
	parseOpenAIWSResponseUsageFromCompletedEvent([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":2000,"output_tokens":20,"input_tokens_details":{"cached_tokens":0}}}}`), usage)
	require.True(t, usage.cacheReadMediaObserved)
	require.False(t, usage.CacheReadKnown)
	require.Zero(t, usage.ImageInputTokens)
}

func TestOpenAIUsageProtectionWaitsForSuccessfulDelivery(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed or drained", true: "delivered"}[delivered], func(t *testing.T) {
			protection, cache := antiDistillationTestService(t)
			require.NoError(t, protection.SaveConfig(context.Background(), 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5}))
			svc := &OpenAIGatewayService{groupAntiDistillation: protection}
			key := &APIKey{UserID: 7, Group: &Group{ID: 1, Platform: PlatformOpenAI}}
			gate := &openAIGroupAntiDistillationDelivery{}
			result := &OpenAIForwardResult{Usage: OpenAIUsage{InputTokens: 2000, CacheReadKnown: true}, OpenAIWSMode: true, UpstreamTerminalEvent: "response.completed", antiDistillationDelivery: gate}
			startedAt := time.Now()
			svc.ObserveGroupAntiDistillationUsage(context.Background(), key, &Account{Platform: PlatformOpenAI}, result, startedAt, []byte(`{"input":"hello"}`))
			require.Empty(t, cache.Observations(), "settling usage does not prove client delivery")
			// The deferred sample must not retain mutable request or billing data.
			key.UserID = 99
			key.Group.ID = 99
			result.Usage.CacheReadInputTokens = 1280
			gate.complete(delivered)
			gate.complete(true)
			gate.register(func() { t.Error("a delivery must sample only once") })
			observations := cache.Observations()
			if !delivered {
				require.Empty(t, observations)
				return
			}
			require.Len(t, observations, 1)
			require.Equal(t, int64(7), observations[0].userID)
			require.True(t, observations[0].zero)
			require.Equal(t, startedAt, observations[0].startedAt)
		})
	}
}

func TestOpenAIUsageProtectionDeliveryRegistrationRace(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		for i := 0; i < 20; i++ {
			gate := &openAIGroupAntiDistillationDelivery{}
			var calls atomic.Int32
			var workers sync.WaitGroup
			for j := 0; j < 4; j++ {
				workers.Add(2)
				go func() { defer workers.Done(); gate.register(func() { calls.Add(1) }) }()
				go func() { defer workers.Done(); gate.complete(delivered) }()
			}
			workers.Wait()
			want := int32(0)
			if delivered {
				want = 1
			}
			require.Equal(t, want, calls.Load())
		}
	}
}

func TestOpenAIUsageProtectionPassthroughSamplesAfterTerminalDelivery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controlCtx, cancelControl := context.WithCancelCause(context.Background())
	defer cancelControl(context.Canceled)
	protection, cache := antiDistillationTestService(t)
	require.NoError(t, protection.SaveConfig(context.Background(), 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5}))
	upstream := newStagedPassthroughConn()
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_protection","model":"gpt-5.1","usage":{"input_tokens":2000,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}}`)
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	svc.SetGroupAntiDistillationService(protection)
	account := passthroughLifecycleAccount()
	key := &APIKey{UserID: 7, Group: &Group{ID: 1, Platform: PlatformOpenAI}}
	beforeDelivery := make(chan int, 1)
	hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, turnErr error) {
		if result == nil || turnErr != nil {
			return
		}
		svc.ObserveGroupAntiDistillationUsage(context.Background(), key, account, result, time.Now(), []byte(`{"input":"hello"}`))
		beforeDelivery <- len(cache.Observations())
	}}
	server, _ := startPassthroughHookRecordingServer(t, controlCtx, svc, account, hooks)
	defer server.Close()
	clientConn := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = clientConn.CloseNow() }()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	_, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	select {
	case count := <-beforeDelivery:
		require.Zero(t, count, "usage settlement callback must precede sampling")
	case <-time.After(time.Second):
		t.Fatal("upstream completion callback missing")
	}
	require.Eventually(t, func() bool { return len(cache.Observations()) == 1 }, time.Second, time.Millisecond)
}

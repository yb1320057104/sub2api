package service

import (
	"encoding/json"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cacheconversion"
)

const (
	AccountExtraCacheConversionEnabled    = "cache_conversion_enabled"
	AccountExtraCacheConversionMultiplier = "cache_conversion_multiplier"
)

// ParseCacheConversionMultiplier accepts finite, nonnegative numeric settings.
// Zero is an explicit multiplier, not the default for an omitted setting.
func ParseCacheConversionMultiplier(value any) (float64, bool) {
	var multiplier float64
	switch v := value.(type) {
	case float64:
		multiplier = v
	case float32:
		multiplier = float64(v)
	case int:
		multiplier = float64(v)
	case int64:
		multiplier = float64(v)
	case int32:
		multiplier = float64(v)
	case int16:
		multiplier = float64(v)
	case int8:
		multiplier = float64(v)
	case uint:
		multiplier = float64(v)
	case uint64:
		multiplier = float64(v)
	case uint32:
		multiplier = float64(v)
	case uint16:
		multiplier = float64(v)
	case uint8:
		multiplier = float64(v)
	case json.Number:
		var err error
		multiplier, err = v.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return multiplier, multiplier >= 0 && !math.IsNaN(multiplier) && !math.IsInf(multiplier, 0)
}

func (a *Account) IsCacheConversionEnabled() bool {
	if a == nil || a.Platform != PlatformOpenAI || a.Extra == nil {
		return false
	}
	enabled, _ := a.Extra[AccountExtraCacheConversionEnabled].(bool)
	return enabled
}

func (a *Account) GetCacheConversionMultiplier() float64 {
	if a != nil && a.Extra != nil {
		if multiplier, ok := ParseCacheConversionMultiplier(a.Extra[AccountExtraCacheConversionMultiplier]); ok {
			return multiplier
		}
	}
	return 1
}

// OpenAICacheConversionConfig snapshots the selected account's conversion
// settings. Other platforms always produce a disabled configuration.
func OpenAICacheConversionConfig(account *Account) cacheconversion.Config {
	if !account.IsCacheConversionEnabled() {
		return cacheconversion.Config{}
	}
	return cacheconversion.Config{Enabled: true, Multiplier: account.GetCacheConversionMultiplier()}
}

func RewriteOpenAICacheConversionJSON(account *Account, body []byte) ([]byte, error) {
	return cacheconversion.RewriteJSON(body, OpenAICacheConversionConfig(account), cacheconversion.OpenAI)
}

func RewriteAnthropicCacheConversionJSON(account *Account, body []byte) ([]byte, error) {
	return cacheconversion.RewriteJSON(body, OpenAICacheConversionConfig(account), cacheconversion.Anthropic)
}

// convertOpenAICacheCreationToRead receives disjoint token buckets, after the
// original cache counts have been subtracted from OpenAI's total input. It
// returns a billing-only copy; client response rewriting uses the same arithmetic
// separately, after the original upstream usage has been collected.
func convertOpenAICacheCreationToRead(account *Account, tokens UsageTokens) (UsageTokens, error) {
	if !account.IsCacheConversionEnabled() {
		return tokens, nil
	}

	multiplier := account.GetCacheConversionMultiplier()
	cacheRead, err := cacheconversion.ConvertReadTokens(tokens.CacheCreationTokens, tokens.CacheReadTokens, multiplier)
	if err != nil {
		return tokens, err
	}

	originalContextTokens := tokens.InputContextTokens()
	tokens.OriginalContextTokens = &originalContextTokens
	tokens.CacheReadTokens = cacheRead
	tokens.CacheCreationTokens = 0
	tokens.CacheCreation5mTokens = 0
	tokens.CacheCreation1hTokens = 0
	return tokens, nil
}

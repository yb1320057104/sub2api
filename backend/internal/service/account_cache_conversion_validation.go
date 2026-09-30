package service

import (
	"maps"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func hasOpenAICacheConversionExtra(extra map[string]any) bool {
	_, hasEnabled := extra[AccountExtraCacheConversionEnabled]
	_, hasMultiplier := extra[AccountExtraCacheConversionMultiplier]
	return hasEnabled || hasMultiplier
}

// ValidateOpenAICacheConversionExtra validates explicitly supplied settings.
// Missing settings retain the default behavior: conversion disabled, multiplier 1.
func ValidateOpenAICacheConversionExtra(platform string, extra map[string]any) error {
	if !hasOpenAICacheConversionExtra(extra) {
		return nil
	}
	if platform != PlatformOpenAI {
		return infraerrors.BadRequest("OPENAI_CACHE_CONVERSION_UNSUPPORTED", "cache conversion is only supported for OpenAI accounts")
	}
	if raw, exists := extra[AccountExtraCacheConversionEnabled]; exists {
		if _, ok := raw.(bool); !ok {
			return infraerrors.BadRequest("OPENAI_CACHE_CONVERSION_INVALID", "cache_conversion_enabled must be a boolean")
		}
	}
	if raw, exists := extra[AccountExtraCacheConversionMultiplier]; exists {
		if _, ok := ParseCacheConversionMultiplier(raw); !ok {
			return infraerrors.BadRequest("OPENAI_CACHE_CONVERSION_INVALID", "cache_conversion_multiplier must be a finite non-negative number")
		}
	}
	return nil
}

func normalizeOpenAICacheConversionExtra(platform string, extra map[string]any) (map[string]any, error) {
	if err := ValidateOpenAICacheConversionExtra(platform, extra); err != nil {
		return nil, err
	}
	if platform != PlatformOpenAI {
		return extra, nil
	}
	normalized := maps.Clone(extra)
	if normalized == nil {
		normalized = make(map[string]any, 2)
	}
	if _, exists := normalized[AccountExtraCacheConversionEnabled]; !exists {
		normalized[AccountExtraCacheConversionEnabled] = false
	}
	if raw, exists := normalized[AccountExtraCacheConversionMultiplier]; exists {
		// Use a consistent JSON number representation and preserve a valid zero.
		normalized[AccountExtraCacheConversionMultiplier], _ = ParseCacheConversionMultiplier(raw)
	} else {
		normalized[AccountExtraCacheConversionMultiplier] = float64(1)
	}
	return normalized, nil
}

func normalizeOpenAICacheConversionUpdateExtra(account *Account, normalized map[string]any) (map[string]any, error) {
	if account.Platform != PlatformOpenAI {
		if err := ValidateOpenAICacheConversionExtra(account.Platform, normalized); err != nil {
			return nil, err
		}
		return normalized, nil
	}
	normalized = maps.Clone(normalized)
	if normalized == nil {
		normalized = make(map[string]any, 2)
	}
	// Full extra replacement also serves partial edits and re-authorization.
	// Preserve settings omitted by clients that do not know about this feature.
	for _, key := range []string{AccountExtraCacheConversionEnabled, AccountExtraCacheConversionMultiplier} {
		if _, provided := normalized[key]; !provided {
			if current, exists := account.Extra[key]; exists {
				normalized[key] = current
			}
		}
	}
	return normalizeOpenAICacheConversionExtra(account.Platform, normalized)
}

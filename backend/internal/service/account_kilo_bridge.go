package service

import (
	"maps"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const AccountExtraKiloBridgeEnabled = "kilo_bridge_enabled"

func hasKiloBridgeExtra(extra map[string]any) bool {
	_, exists := extra[AccountExtraKiloBridgeEnabled]
	return exists
}

// IsKiloBridgeEnabled only affects OpenAI accounts. Transport and model
// mapping remain the responsibility of the existing account configuration.
func (a *Account) IsKiloBridgeEnabled() bool {
	if a == nil || a.Platform != PlatformOpenAI {
		return false
	}
	enabled, _ := a.Extra[AccountExtraKiloBridgeEnabled].(bool)
	return enabled
}

func ValidateKiloBridgeExtra(platform string, extra map[string]any) error {
	raw, exists := extra[AccountExtraKiloBridgeEnabled]
	if !exists {
		return nil
	}
	_, ok := raw.(bool)
	if !ok {
		return infraerrors.BadRequest("KILO_BRIDGE_INVALID", "kilo_bridge_enabled must be a boolean")
	}
	if platform != PlatformOpenAI {
		return infraerrors.BadRequest("KILO_BRIDGE_UNSUPPORTED", "Kilo bridge is only supported for OpenAI accounts")
	}
	return nil
}

func normalizeKiloBridgeExtra(platform string, extra, current map[string]any) (map[string]any, error) {
	normalized := maps.Clone(extra)
	if platform == PlatformOpenAI {
		if normalized == nil {
			normalized = make(map[string]any)
		}
		if !hasKiloBridgeExtra(normalized) {
			if saved, exists := current[AccountExtraKiloBridgeEnabled]; exists {
				normalized[AccountExtraKiloBridgeEnabled] = saved
			} else {
				normalized[AccountExtraKiloBridgeEnabled] = false
			}
		}
	}
	if err := ValidateKiloBridgeExtra(platform, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// Package usageoutput scales downstream token counts independently of the
// upstream usage retained by the gateway for accounting and statistics.
package usageoutput

import (
	"bytes"
	"fmt"
	"math"
	"strconv"

	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type Protocol string

const (
	OpenAI    Protocol = "openai"
	Anthropic Protocol = "anthropic"
	Gemini    Protocol = "gemini"
)

type Options struct {
	Protocol  Protocol
	AllowBare bool
}

var usagePaths = []string{
	"usage", "response.usage", "message.usage",
	"data.usage", "data.response.usage", "data.message.usage",
}

var geminiUsagePaths = []string{
	"usageMetadata", "usage_metadata", "response.usageMetadata", "response.usage_metadata",
	"data.usageMetadata", "data.usage_metadata", "data.response.usageMetadata", "data.response.usage_metadata",
}

// Image-tool usage has appeared both directly under image_gen and inside its
// usage member. No other tool payload or generated content is traversed.
var imageUsagePaths = []string{
	"tool_usage.image_gen", "tool_usage.image_gen.usage",
	"response.tool_usage.image_gen", "response.tool_usage.image_gen.usage",
	"data.tool_usage.image_gen", "data.tool_usage.image_gen.usage",
	"data.response.tool_usage.image_gen", "data.response.tool_usage.image_gen.usage",
}

var scalarTokenFields = []string{
	"input_tokens", "output_tokens", "prompt_tokens", "completion_tokens", "total_tokens",
	"cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens", "cache_write_tokens",
	"cache_read_input_tokens", "cache_read_tokens", "cached_tokens",
	"prompt_cache_hit_tokens", "prompt_cache_miss_tokens",
	"image_input_tokens", "image_cache_read_tokens", "image_output_tokens",
	"cache_creation_5m_tokens", "cache_creation_1h_tokens",
	"cache_creation_5m_input_tokens", "cache_creation_1h_input_tokens",
	"reasoning_tokens", "text_tokens", "audio_tokens", "image_tokens",
	"accepted_prediction_tokens", "rejected_prediction_tokens",
	"promptTokenCount", "candidatesTokenCount", "cachedContentTokenCount", "totalTokenCount",
	"thoughtsTokenCount", "toolUsePromptTokenCount", "inputTokenCount", "outputTokenCount", "totalTokens",
	"prompt_token_count", "candidates_token_count", "cached_content_token_count", "total_token_count",
	"thoughts_token_count", "tool_use_prompt_token_count", "input_token_count", "output_token_count", "total_tokens_count",
}

var detailContainers = []string{
	"input_tokens_details", "output_tokens_details", "prompt_tokens_details", "completion_tokens_details",
	"input_token_details", "output_token_details", "prompt_token_details", "completion_token_details",
}

var detailTokenFields = []string{
	"reasoning_tokens", "text_tokens", "audio_tokens", "image_tokens", "cached_tokens",
	"accepted_prediction_tokens", "rejected_prediction_tokens",
	"cache_creation_tokens", "cache_write_tokens", "cache_read_tokens",
	"cache_creation_input_tokens", "cache_write_input_tokens", "cache_read_input_tokens",
	"ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens",
}

var creationDetailFields = []string{
	"cache_creation.ephemeral_5m_input_tokens", "cache_creation.ephemeral_1h_input_tokens",
}

var geminiDetailContainers = []string{
	"promptTokensDetails", "candidatesTokensDetails", "cacheTokensDetails", "toolUsePromptTokensDetails",
	"thoughtsTokensDetails", "prompt_tokens_details", "candidates_tokens_details", "cache_tokens_details",
	"tool_use_prompt_tokens_details", "thoughts_tokens_details",
}

type tokenValue struct {
	original decimal.Decimal
	scaled   decimal.Decimal
}

type tokenPatch struct {
	path  string
	value int64
}

// ScaleJSON accepts one response, a response array, or one SSE/WS JSON payload.
// It changes only explicitly recognized token fields in response envelopes.
// Callers must invoke it after protocol/cache conversion and must not use its
// result to calculate their own bill or original-usage statistics. No input
// bytes are mutated; an unchanged response, including multiplier 1, is returned
// byte-for-byte. An error returns nil, never a partially scaled payload.
func ScaleJSON(body []byte, multiplier float64, options Options) ([]byte, error) {
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier < 0 || multiplier > 1000 {
		return nil, fmt.Errorf("usage output multiplier must be between 0 and 1000")
	}
	factor := decimal.NewFromFloat(multiplier)
	if !factor.Equal(factor.Truncate(6)) {
		return nil, fmt.Errorf("usage output multiplier must have at most six decimal places")
	}
	if options.Protocol != OpenAI && options.Protocol != Anthropic && options.Protocol != Gemini {
		return nil, fmt.Errorf("unsupported usage output protocol %q", options.Protocol)
	}
	if multiplier == 1 {
		return body, nil
	}
	if trimmed := bytes.TrimSpace(body); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[DONE]")) {
		return body, nil
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("usage output response is not valid JSON")
	}
	root := gjson.ParseBytes(body)
	patches := make([]tokenPatch, 0)
	if root.IsArray() {
		for i, response := range root.Array() {
			if err := collectResponsePatches(response, strconv.Itoa(i), factor, options, &patches); err != nil {
				return nil, err
			}
		}
	} else if err := collectResponsePatches(root, "", factor, options, &patches); err != nil {
		return nil, err
	}
	if len(patches) == 0 {
		return body, nil
	}
	out := bytes.Clone(body)
	for _, patch := range patches {
		var err error
		out, err = sjson.SetBytes(out, patch.path, patch.value)
		if err != nil {
			return nil, fmt.Errorf("replace usage output %s: %w", patch.path, err)
		}
	}
	return out, nil
}

func collectResponsePatches(response gjson.Result, prefix string, factor decimal.Decimal, options Options, patches *[]tokenPatch) error {
	if !response.IsObject() {
		return nil
	}
	paths := usagePaths
	switch options.Protocol {
	case Gemini:
		paths = append(append([]string{}, paths...), geminiUsagePaths...)
	case OpenAI:
		paths = append(append([]string{}, paths...), imageUsagePaths...)
	}
	foundUsage := false
	for _, path := range paths {
		usage := response.Get(path)
		if !usage.IsObject() {
			continue
		}
		foundUsage = true
		if err := collectUsagePatches(usage, joinPath(prefix, path), factor, options.Protocol, patches); err != nil {
			return err
		}
	}
	if options.AllowBare && !foundUsage {
		return collectUsagePatches(response, prefix, factor, options.Protocol, patches)
	}
	return nil
}

func collectUsagePatches(usage gjson.Result, prefix string, factor decimal.Decimal, protocol Protocol, patches *[]tokenPatch) error {
	values := make(map[string]tokenValue)
	paths := make([]string, 0, len(scalarTokenFields))
	add := func(path string) error {
		value := usage.Get(path)
		// Optional provider counters can explicitly be unknown. Preserve null
		// rather than turning it into zero or rejecting an otherwise valid usage.
		if !value.Exists() || value.Type == gjson.Null {
			return nil
		}
		if _, found := values[path]; found {
			return nil
		}
		original, err := parseTokens(value)
		if err != nil {
			return fmt.Errorf("invalid usage output %s: %w", joinPath(prefix, path), err)
		}
		scaled := original.Mul(factor).Floor()
		if scaled.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
			return fmt.Errorf("usage output %s exceeds int64 token range", joinPath(prefix, path))
		}
		values[path] = tokenValue{original: original, scaled: scaled}
		paths = append(paths, path)
		return nil
	}
	for _, path := range scalarTokenFields {
		if err := add(path); err != nil {
			return err
		}
	}
	for _, container := range detailContainers {
		if !usage.Get(container).IsObject() {
			continue
		}
		for _, field := range detailTokenFields {
			if err := add(container + "." + field); err != nil {
				return err
			}
		}
		// Realtime usage breaks cached input down once more by modality.
		for _, cached := range []string{"cached_tokens_details", "cached_token_details"} {
			for _, field := range []string{"text_tokens", "audio_tokens", "image_tokens"} {
				if err := add(container + "." + cached + "." + field); err != nil {
					return err
				}
			}
		}
	}
	for _, path := range creationDetailFields {
		if err := add(path); err != nil {
			return err
		}
	}
	if protocol == Gemini {
		for _, container := range geminiDetailContainers {
			if details := usage.Get(container); details.IsArray() {
				for i, detail := range details.Array() {
					if !detail.IsObject() {
						continue
					}
					for _, field := range []string{"tokenCount", "token_count"} {
						if err := add(container + "." + strconv.Itoa(i) + "." + field); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	reconcileTotals(values, factor, protocol)
	for _, path := range paths {
		value := values[path]
		if !value.original.Equal(value.scaled) {
			*patches = append(*patches, tokenPatch{path: joinPath(prefix, path), value: value.scaled.IntPart()})
		}
	}
	return nil
}

func parseTokens(value gjson.Result) (decimal.Decimal, error) {
	if value.Type != gjson.Number || len(value.Raw) > 128 {
		return decimal.Zero, fmt.Errorf("token count must be a nonnegative integer")
	}
	number, err := decimal.NewFromString(value.Raw)
	// Bound exponents before rescaling: malicious scientific notation must not
	// cause a huge allocation while checking an otherwise tiny JSON payload.
	if err != nil || number.Exponent() > 19 || number.Exponent() < -128 {
		return decimal.Zero, fmt.Errorf("token count exceeds integer range")
	}
	if number.IsNegative() || !number.Equal(number.Truncate(0)) || number.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return decimal.Zero, fmt.Errorf("token count must be a nonnegative int64 integer")
	}
	return number, nil
}

func reconcileTotals(values map[string]tokenValue, factor decimal.Decimal, protocol Protocol) {
	input := firstToken(values, "input_tokens", "prompt_tokens", "inputTokenCount", "input_token_count")
	output := firstToken(values, "output_tokens", "completion_tokens", "outputTokenCount", "output_token_count")
	base := addTokens(input, output)
	var optional []tokenValue
	switch protocol {
	case Gemini:
		base = addTokens(
			firstToken(values, "promptTokenCount", "prompt_token_count", "input_tokens", "prompt_tokens", "inputTokenCount", "input_token_count"),
			firstToken(values, "candidatesTokenCount", "candidates_token_count", "output_tokens", "completion_tokens", "outputTokenCount", "output_token_count"),
		)
		// cachedContentTokenCount is already part of promptTokenCount. Most
		// providers report thoughts separately; some include them in candidates.
		optional = []tokenValue{
			firstToken(values, "thoughtsTokenCount", "thoughts_token_count"),
			firstToken(values, "toolUsePromptTokenCount", "tool_use_prompt_token_count"),
		}
	case Anthropic:
		creation := firstToken(values, "cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens", "cache_write_tokens")
		if !hasToken(values, "cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens", "cache_write_tokens") {
			creation = addTokens(firstToken(values, creationDetailFields[0]), firstToken(values, creationDetailFields[1]))
		}
		optional = []tokenValue{creation, firstToken(values, "cache_read_input_tokens", "cache_read_tokens", "cached_tokens")}
	}
	for _, path := range []string{"total_tokens", "totalTokenCount", "total_token_count", "totalTokens", "total_tokens_count"} {
		total, exists := values[path]
		if !exists || base.original.GreaterThan(total.original) {
			// Do not enlarge malformed/nonstandard totals that were already
			// smaller than their reported input+output; preserve their relation.
			continue
		}
		selected := base
		// Choose the largest additive combination consistent with the original
		// total. This prevents double-counting cached/think tokens in providers
		// whose optional fields are subsets rather than separate buckets.
		for mask := 1; mask < 1<<len(optional); mask++ {
			candidate := base
			for i, item := range optional {
				if mask&(1<<i) != 0 {
					candidate = addTokens(candidate, item)
				}
			}
			if candidate.original.LessThanOrEqual(total.original) && candidate.original.GreaterThan(selected.original) {
				selected = candidate
			}
		}
		residual := total.original.Sub(selected.original).Mul(factor).Floor()
		total.scaled = selected.scaled.Add(residual)
		values[path] = total
	}
}

func firstToken(values map[string]tokenValue, paths ...string) tokenValue {
	for _, path := range paths {
		if value, exists := values[path]; exists {
			return value
		}
	}
	return tokenValue{original: decimal.Zero, scaled: decimal.Zero}
}

func hasToken(values map[string]tokenValue, paths ...string) bool {
	for _, path := range paths {
		if _, exists := values[path]; exists {
			return true
		}
	}
	return false
}

func addTokens(left, right tokenValue) tokenValue {
	return tokenValue{original: left.original.Add(right.original), scaled: left.scaled.Add(right.scaled)}
}

func joinPath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	return prefix + "." + path
}

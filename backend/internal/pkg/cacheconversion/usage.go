// Package cacheconversion rewrites client-visible usage without changing the
// upstream usage retained for billing. Callers must pass the output protocol.
package cacheconversion

import (
	"bytes"
	"fmt"
	"math"

	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type Config struct {
	Enabled    bool
	Multiplier float64
}

type Protocol string

const (
	OpenAI    Protocol = "openai"
	Anthropic Protocol = "anthropic"
)

var usagePaths = []string{
	"usage", "response.usage", "message.usage",
	"data.usage", "data.response.usage", "data.message.usage",
}

// The ordering of canonical paths matches OpenAI's billing usage parser. An
// explicitly present nested zero takes precedence over positive root aliases.
var nestedCreationPaths = []string{
	"input_tokens_details.cache_write_tokens",
	"prompt_tokens_details.cache_write_tokens",
	"input_tokens_details.cache_creation_tokens",
	"prompt_tokens_details.cache_creation_tokens",
}

var rootCreationPaths = []string{
	"cache_write_tokens", "cache_creation_input_tokens",
	"cache_write_input_tokens", "cache_creation_tokens",
}

var creationDetailPaths = []string{
	"cache_creation.ephemeral_5m_input_tokens",
	"cache_creation.ephemeral_1h_input_tokens",
}

var nestedReadPaths = []string{
	"input_tokens_details.cached_tokens", "prompt_tokens_details.cached_tokens",
}

var rootReadPaths = []string{
	"cache_read_input_tokens", "cache_read_tokens", "cached_tokens",
}

// ConvertReadTokens uses decimal floor so values such as 100 * 0.29 yield 29,
// and rejects overflow of the PostgreSQL integer used by usage_logs.
func ConvertReadTokens(creation, read int, multiplier float64) (int, error) {
	if multiplier < 0 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		return 0, fmt.Errorf("cache conversion multiplier must be finite and nonnegative")
	}
	created := decimal.NewFromInt(int64(max(creation, 0)))
	converted := created.Mul(decimal.NewFromFloat(multiplier)).Floor()
	cacheRead := decimal.NewFromInt(int64(read)).Add(converted)
	if cacheRead.IsNegative() || cacheRead.GreaterThan(decimal.NewFromInt(math.MaxInt32)) {
		return 0, fmt.Errorf("cache conversion exceeds token storage range: cache_read=%d cache_creation=%d multiplier=%g maximum=%d",
			read, creation, multiplier, math.MaxInt32)
	}
	return int(cacheRead.IntPart()), nil
}

// RewriteJSON accepts a response JSON, one SSE data payload, or a bare usage
// object. Only known response envelopes are visited; tool results and generated
// content are never traversed. Disabled conversion returns the original bytes.
// On error it returns nil: callers must not send the original payload instead.
func RewriteJSON(body []byte, cfg Config, protocol Protocol) ([]byte, error) {
	if !cfg.Enabled {
		return body, nil
	}
	if data := bytes.TrimSpace(body); len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return body, nil
	}
	if err := validate(cfg, protocol); err != nil {
		return nil, err
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("cache conversion response is not valid JSON")
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return body, nil
	}

	out := body
	foundUsage := false
	for _, path := range usagePaths {
		usage := root.Get(path)
		if !usage.IsObject() {
			continue
		}
		foundUsage = true
		raw := []byte(usage.Raw)
		rewritten, err := RewriteUsageJSON(raw, cfg, protocol)
		if err != nil {
			return nil, fmt.Errorf("rewrite %s: %w", path, err)
		}
		if bytes.Equal(raw, rewritten) {
			continue
		}
		out, err = sjson.SetRawBytes(out, path, rewritten)
		if err != nil {
			return nil, fmt.Errorf("replace %s: %w", path, err)
		}
	}
	if !foundUsage && isUsageObject(root) {
		return RewriteUsageJSON(body, cfg, protocol)
	}
	return out, nil
}

// RewriteUsageJSON rewrites a bare usage object. OpenAI input/prompt counts
// include both cache buckets; Anthropic input counts exclude them. This choice
// must come from the outgoing protocol, never from cache field names.
func RewriteUsageJSON(body []byte, cfg Config, protocol Protocol) ([]byte, error) {
	if !cfg.Enabled {
		return body, nil
	}
	if err := validate(cfg, protocol); err != nil {
		return nil, err
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("cache conversion usage is not valid JSON")
	}
	usage := gjson.ParseBytes(body)
	if !usage.IsObject() {
		return nil, fmt.Errorf("cache conversion usage must be an object")
	}
	if !hasCreationFields(usage) {
		return body, nil
	}

	creation, err := creationTokens(usage, protocol)
	if err != nil {
		return nil, err
	}
	read := readTokens(usage, protocol)
	convertedRead, err := ConvertReadTokens(creation, read, cfg.Multiplier)
	if err != nil {
		return nil, err
	}
	out := bytes.Clone(body)
	set := func(path string, value int64) error {
		var err error
		out, err = sjson.SetBytes(out, path, value)
		return err
	}
	for _, paths := range [][]string{nestedCreationPaths, rootCreationPaths, creationDetailPaths} {
		for _, path := range paths {
			if usage.Get(path).Exists() {
				if err := set(path, 0); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, paths := range [][]string{nestedReadPaths, rootReadPaths} {
		for _, path := range paths {
			if usage.Get(path).Exists() {
				if err := set(path, int64(convertedRead)); err != nil {
					return nil, err
				}
			}
		}
	}
	// Always expose a canonical read count, including when the upstream only
	// reported creation and had no read field to update.
	readPath := "cache_read_input_tokens"
	if protocol == OpenAI {
		readPath = "input_tokens_details.cached_tokens"
		if !usage.Get("input_tokens").Exists() && (usage.Get("prompt_tokens").Exists() || usage.Get("prompt_tokens_details").Exists()) {
			readPath = "prompt_tokens_details.cached_tokens"
		}
	}
	if err := set(readPath, int64(convertedRead)); err != nil {
		return nil, err
	}

	// Adjust total_tokens by the input change. This preserves other reported
	// output components, such as providers that report reasoning separately.
	inputDelta := decimal.NewFromInt(int64(convertedRead)).Sub(decimal.NewFromInt(int64(read))).Sub(decimal.NewFromInt(int64(creation)))
	if protocol == OpenAI {
		input := max(int(usage.Get("input_tokens").Int()), 0)
		if input == 0 {
			input = max(int(usage.Get("prompt_tokens").Int()), 0)
		}
		ordinaryInput := subtractNonnegative(subtractNonnegative(input, creation), read)
		convertedInput, err := checkedTokenSum(ordinaryInput, convertedRead)
		if err != nil {
			return nil, err
		}
		hasInput := false
		for _, path := range []string{"input_tokens", "prompt_tokens"} {
			if usage.Get(path).Exists() {
				hasInput = true
				if err := set(path, convertedInput); err != nil {
					return nil, err
				}
			}
		}
		if hasInput {
			inputDelta = decimal.NewFromInt(convertedInput).Sub(decimal.NewFromInt(int64(input)))
		}
	}
	if total := usage.Get("total_tokens"); total.Exists() {
		convertedTotal := decimal.NewFromInt(max(total.Int(), 0)).Add(inputDelta)
		if convertedTotal.IsNegative() {
			convertedTotal = decimal.Zero
		}
		if convertedTotal.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
			return nil, fmt.Errorf("cache conversion total_tokens exceeds integer range")
		}
		if err := set("total_tokens", convertedTotal.IntPart()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func validate(cfg Config, protocol Protocol) error {
	if protocol != OpenAI && protocol != Anthropic {
		return fmt.Errorf("unsupported cache conversion protocol %q", protocol)
	}
	if cfg.Multiplier < 0 || math.IsNaN(cfg.Multiplier) || math.IsInf(cfg.Multiplier, 0) {
		return fmt.Errorf("cache conversion multiplier must be finite and nonnegative")
	}
	return nil
}

func hasCreationFields(usage gjson.Result) bool {
	for _, paths := range [][]string{nestedCreationPaths, rootCreationPaths, creationDetailPaths} {
		for _, path := range paths {
			if usage.Get(path).Exists() {
				return true
			}
		}
	}
	return false
}

func isUsageObject(value gjson.Result) bool {
	for _, path := range []string{"input_tokens", "prompt_tokens", "output_tokens", "completion_tokens", "total_tokens"} {
		if value.Get(path).Exists() {
			return true
		}
	}
	return hasCreationFields(value)
}

func creationTokens(usage gjson.Result, protocol Protocol) (int, error) {
	if protocol == Anthropic {
		if value := usage.Get("cache_creation_input_tokens").Int(); value > 0 {
			return int(value), nil
		}
		// Anthropic may provide only the 5m/1h detail object.
		detail := decimal.NewFromInt(max(usage.Get(creationDetailPaths[0]).Int(), 0)).Add(
			decimal.NewFromInt(max(usage.Get(creationDetailPaths[1]).Int(), 0)))
		if detail.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
			return 0, fmt.Errorf("cache creation detail tokens exceed integer range")
		}
		if detail.IsPositive() {
			return int(detail.IntPart()), nil
		}
	}
	for _, path := range nestedCreationPaths {
		if value := usage.Get(path); value.Exists() {
			return max(int(value.Int()), 0), nil
		}
	}
	return firstPositive(usage, rootCreationPaths), nil
}

func readTokens(usage gjson.Result, protocol Protocol) int {
	if protocol == Anthropic {
		if value := firstPositive(usage, rootReadPaths); value > 0 {
			return value
		}
	}
	for _, path := range nestedReadPaths {
		if value := usage.Get(path); value.Exists() {
			return max(int(value.Int()), 0)
		}
	}
	return firstPositive(usage, rootReadPaths)
}

func firstPositive(usage gjson.Result, paths []string) int {
	for _, path := range paths {
		if value := usage.Get(path).Int(); value > 0 {
			return int(value)
		}
	}
	return 0
}

func subtractNonnegative(value, subtract int) int {
	if subtract >= value {
		return 0
	}
	return value - subtract
}

func checkedTokenSum(left, right int) (int64, error) {
	sum := decimal.NewFromInt(int64(left)).Add(decimal.NewFromInt(int64(right)))
	if sum.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, fmt.Errorf("cache conversion input tokens exceed integer range")
	}
	return sum.IntPart(), nil
}

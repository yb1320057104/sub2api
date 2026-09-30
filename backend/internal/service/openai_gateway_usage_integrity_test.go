//go:build unit

package service

import (
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

package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAISSEReadPumpNormalizesMultilineUsageBeforeClientConversion(t *testing.T) {
	input := "id: usage-1\nevent: response.completed\ndata: {\ndata: \"type\":\"response.completed\",\n" +
		"data: \"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":5,\"cache_creation_input_tokens\":40,\"input_tokens_details\":{\"cached_tokens\":20}}}}\n\n"
	pump := newOpenAISSEReadPump(io.NopCloser(strings.NewReader(input)), 4096, true)
	defer pump.Close()
	account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{
		AccountExtraCacheConversionEnabled: true, AccountExtraCacheConversionMultiplier: 2,
	}}
	var lines []string
	var usagePayloads int
	for pump.Next(context.Background(), time.Second, nil, nil) {
		line := pump.Text()
		lines = append(lines, line)
		if payload, ok := extractOpenAISSEDataLine(line); ok {
			usagePayloads++
			require.True(t, gjson.Valid(payload), "downstream rewrites must receive the complete JSON document")
			converted, err := RewriteOpenAICacheConversionJSON(account, []byte(payload))
			require.NoError(t, err)
			require.Equal(t, int64(100), gjson.GetBytes(converted, "response.usage.input_tokens_details.cached_tokens").Int())
			require.Equal(t, int64(0), gjson.GetBytes(converted, "response.usage.cache_creation_input_tokens").Int())
			require.Equal(t, int64(20), gjson.Get(payload, "response.usage.input_tokens_details.cached_tokens").Int(), "original billing/protection evidence stays unconverted")
		}
	}
	require.NoError(t, pump.Err())
	require.Equal(t, 1, usagePayloads)
	require.Equal(t, "id: usage-1", lines[0])
	require.Equal(t, "event: response.completed", lines[1])
	require.Empty(t, lines[len(lines)-1], "the event boundary must remain intact")
}

func TestOpenAISSEReadPumpSilentHeartbeatAndJoin(t *testing.T) {
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	pump := newOpenAISSEReadPump(reader, 1024)
	defer pump.Close()
	beats := time.NewTicker(5 * time.Millisecond)
	defer beats.Stop()
	count := 0
	start := time.Now()
	require.False(t, pump.Next(context.Background(), 50*time.Millisecond, beats.C, func() { count++ }))
	require.ErrorIs(t, pump.Err(), errOpenAISSEIdle)
	require.Positive(t, count)
	require.Less(t, time.Since(start), time.Second)
	pump.Close()
	pump.Close()
	select {
	case <-pump.done:
	default:
		t.Fatal("reader not joined")
	}
}

func TestOpenAISSEReadPumpFragmentedDocumentIsActivity(t *testing.T) {
	reader, writer := io.Pipe()
	pump := newOpenAISSEReadPump(reader, 4096)
	defer pump.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = writer.Close() }()
		for _, piece := range []string{`data: {"type":`, `"response.output_text.delta",`, `"delta":`, `"fragmented"}`, "\n\n"} {
			if _, err := io.WriteString(writer, piece); err != nil {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	}()
	defer func() { pump.Close(); <-done }()
	require.True(t, pump.Next(context.Background(), 100*time.Millisecond, nil, nil))
	require.Contains(t, pump.Text(), "fragmented")
}

func TestOpenAISSEReadPumpCancelAndBoundedQueue(t *testing.T) {
	for _, input := range []string{"", strings.Repeat("data: {}\n\n", 10000)} {
		t.Run("cancel", func(t *testing.T) {
			reader, writer := io.Pipe()
			pump := newOpenAISSEReadPump(reader, 1024)
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() { _ = writer.Close() }()
				if input != "" {
					_, _ = io.WriteString(writer, input)
				} else {
					<-pump.stop
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			for pump.Next(ctx, 0, nil, nil) {
			}
			require.True(t, errors.Is(pump.Err(), context.Canceled))
			pump.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("blocked scanner or producer leaked")
			}
		})
	}
}

func TestOpenAISSEReadPumpFirstOutputDeadlineNotExtendedByComments(t *testing.T) {
	reader, writer := io.Pipe()
	pump := newOpenAISSEReadPump(reader, 1024)
	pump.firstOutputDeadline = time.Now().Add(90 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = writer.Close() }()
		for {
			if _, err := io.WriteString(writer, ": upstream heartbeat\n\n"); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	defer func() { pump.Close(); <-done }()
	start := time.Now()
	for pump.Next(context.Background(), time.Second, nil, nil) {
	}
	require.ErrorIs(t, pump.Err(), errOpenAISSEFirstOutput)
	require.Less(t, time.Since(start), time.Second)
}

func TestOpenAISSEReadPumpIncompleteUsageIsNotAnUpstreamTerminal(t *testing.T) {
	result := &OpenAIForwardResult{Usage: OpenAIUsage{OutputTokens: 7}, streamReadIncomplete: true}
	require.False(t, result.SucceededForScheduling())
	require.Empty(t, result.UpstreamTerminalEvent, "local timeout must not invent an upstream terminal")
	require.Equal(t, 7, result.Usage.OutputTokens)
}

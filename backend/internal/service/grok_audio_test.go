package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildGrokVoiceURL_UsesAPIDefaultForCLIProxyBase(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"base_url": xai.DefaultCLIBaseURL,
		},
	}
	url, err := buildGrokVoiceURL(account, nil, "tts")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/tts", url)

	url, err = buildGrokVoiceURL(account, nil, "realtime")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/realtime", url)
}

func TestBuildGrokVoiceURL_EmptyBaseFallsBackToAPI(t *testing.T) {
	account := &Account{
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{},
	}
	url, err := buildGrokVoiceURL(account, nil, "stt")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/stt", url)
}

func TestBuildGrokVoiceURL_RequiresEndpoint(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}
	_, err := buildGrokVoiceURL(account, nil, "  ")
	require.Error(t, err)
}

func TestBuildGrokVoiceURL_EncodesCustomVoicePathSegments(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}
	got, err := buildGrokVoiceURL(account, nil, "custom-voices/nlbqfwie/audio")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/custom-voices/nlbqfwie/audio", got)

	_, err = buildGrokVoiceURL(account, nil, "custom-voices/../audio")
	require.Error(t, err)
}

func TestForwardGrokVoice_RejectsNonGrok(t *testing.T) {
	svc := &OpenAIGatewayService{}
	_, err := svc.ForwardGrokVoice(context.Background(), nil, &Account{Platform: PlatformOpenAI}, "tts", []byte(`{}`), "application/json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported")
}

func TestAwaitGrokRealtimeAudioObservedReadsFlagAfterRelayExits(t *testing.T) {
	errCh := make(chan error, 1)
	var observed atomic.Bool
	go func() {
		observed.Store(true)
		errCh <- io.EOF
	}()
	got, err := awaitGrokRealtimeAudioObserved(errCh, &observed)
	require.ErrorIs(t, err, io.EOF)
	require.True(t, got, "audioObserved must be read after the relay returns, not before <-errCh")
}

func TestGrokRealtimeEventHasAudio(t *testing.T) {
	require.False(t, grokRealtimeEventHasAudio([]byte(`{"type":"session.created"}`)))
	require.False(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.audio_transcript.delta","delta":"hi"}`)))
	require.False(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.audio.delta","delta":""}`)))
	require.True(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.audio.delta","delta":"abc"}`)))
	require.True(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.output_audio.delta","audio":"abc"}`)))
}

func TestForwardGrokVoice_RejectsUnknownEndpoint(t *testing.T) {
	svc := &OpenAIGatewayService{}
	_, err := svc.ForwardGrokVoice(context.Background(), nil, &Account{Platform: PlatformGrok}, "unknown", []byte(`{}`), "application/json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported")
}

type hiddenBillingGrokRealtimeConn struct {
	*stagedPassthroughConn
}

func (c *hiddenBillingGrokRealtimeConn) WriteJSON(ctx context.Context, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.WriteFrame(ctx, coderws.MessageText, body)
}

func TestPKHiddenBillingGrokRealtimeOutputSnapshotAndClientPassthrough(t *testing.T) {
	for _, tc := range []struct {
		name       string
		multiplier *float64
		wantInput  int64
	}{
		{name: "legacy default", wantInput: 100},
		{name: "zero", multiplier: ptrFloat64WS(0), wantInput: 0},
		{name: "double snapshot", multiplier: ptrFloat64WS(2), wantInput: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &hiddenBillingGrokRealtimeConn{stagedPassthroughConn: newStagedPassthroughConn()}
			defer func() { _ = fake.Close() }()
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.Enabled = false
			svc := &OpenAIGatewayService{
				cfg:                       cfg,
				openaiWSPassthroughDialer: &openAIWSSingleConnDialer{conn: fake},
			}
			upstream := &GrokRealtimeUpstream{conn: fake}
			if tc.multiplier != nil {
				account := &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey,
					Extra: map[string]any{AccountExtraHiddenRateMultiplier: *tc.multiplier}}
				var err error
				upstream, err = svc.OpenGrokRealtime(context.Background(), account, "test", "grok-voice-latest")
				require.NoError(t, err)
				// An open session uses its account snapshot, not a later setting.
				account.Extra[AccountExtraHiddenRateMultiplier] = 9.0
			}
			type relayResult struct {
				audioObserved bool
				err           error
			}
			results := make(chan relayResult, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				client, err := coderws.Accept(w, r, nil)
				if err != nil {
					results <- relayResult{err: err}
					return
				}
				defer func() { _ = client.CloseNow() }()
				observed, err := svc.ProxyGrokRealtimeConn(r.Context(), nil, client, upstream)
				results <- relayResult{audioObserved: observed, err: err}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			request := []byte(`{"type":"input_audio_buffer.append","audio":"untouched-audio","usage":{"input_tokens":100}}`)
			require.NoError(t, client.Write(ctx, coderws.MessageBinary, request))
			forwarded := requirePassthroughUpstreamWrite(t, fake.stagedPassthroughConn, 3*time.Second)
			require.JSONEq(t, string(request), string(forwarded), "client-to-upstream tokens and audio must not be scaled")
			fake.Send(`{"type":"response.done","response":{"id":"resp_voice","usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300,"input_token_details":{"audio_tokens":100},"output_token_details":{"audio_tokens":200}}},"audio":"untouched-output"}`)
			kind, payload, err := client.Read(ctx)
			require.NoError(t, err)
			require.Equal(t, coderws.MessageText, kind)
			require.Equal(t, tc.wantInput, gjson.GetBytes(payload, "response.usage.input_tokens").Int())
			require.Equal(t, 2*tc.wantInput, gjson.GetBytes(payload, "response.usage.output_tokens").Int())
			require.Equal(t, 3*tc.wantInput, gjson.GetBytes(payload, "response.usage.total_tokens").Int())
			require.Equal(t, tc.wantInput, gjson.GetBytes(payload, "response.usage.input_token_details.audio_tokens").Int())
			require.Equal(t, 2*tc.wantInput, gjson.GetBytes(payload, "response.usage.output_token_details.audio_tokens").Int())
			require.Equal(t, "untouched-output", gjson.GetBytes(payload, "audio").String())
			require.NotContains(t, string(payload), "hidden_rate_multiplier")
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case result := <-results:
				require.True(t, result.audioObserved)
				require.Equal(t, coderws.StatusNormalClosure, coderws.CloseStatus(result.err))
			case <-ctx.Done():
				t.Fatal("realtime relay did not finish")
			}
		})
	}
}

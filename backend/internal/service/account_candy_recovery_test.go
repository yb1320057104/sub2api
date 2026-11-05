//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const pk52CandyItem = `{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"<final_answer>21</final_answer>"}]}`

func pk52CandyTerminal(answer string) string {
	return `data: {"type":"response.completed","response":{"status":"completed","output":[` + strings.ReplaceAll(pk52CandyItem, "21", answer) + "]}}\n\n"
}

func TestPK52CandyCompletedItemRecovery(t *testing.T) {
	done := `data: {"type":"response.output_item.done","output_index":0,"item":` + pk52CandyItem + "}\n\n"
	empty := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"done and successful empty terminal", done + empty, true},
		{"header only type", "event: response.output_item.done\n" + strings.Replace(done, `"type":"response.output_item.done",`, "", 1) + "event: response.completed\n" + strings.Replace(empty, `"type":"response.completed",`, "", 1), true},
		{"repeated identical done", done + done + empty, true},
		{"terminal agrees", done + pk52CandyTerminal("21"), true},
		{"no terminal", done, false},
		{"delta cannot complete", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n" + empty, false},
		{"index gap", strings.Replace(done, `"output_index":0`, `"output_index":1`, 1) + empty, false},
		{"missing index", strings.Replace(done, `"output_index":0,`, "", 1) + empty, false},
		{"negative index", strings.Replace(done, `"output_index":0`, `"output_index":-1`, 1) + empty, false},
		{"duplicate conflict", done + strings.ReplaceAll(done, "21", "22") + empty, false},
		{"terminal conflict", done + pk52CandyTerminal("22"), false},
		{"header conflict", "event: response.failed\n" + pk52CandyTerminal("21"), false},
		{"incomplete terminal", done + strings.Replace(empty, `"status":"completed"`, `"status":"incomplete"`, 1), false},
		{"incomplete item", strings.Replace(done, `"status":"completed"`, `"status":"in_progress"`, 1) + empty, false},
		{"tool item", strings.Replace(done, `"type":"message"`, `"type":"function_call"`, 1) + empty, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, err := readAccountCandyReply(newJSONResponse(http.StatusOK, tc.body), false)
			if tc.ok {
				require.NoError(t, err)
				require.Equal(t, "<final_answer>21</final_answer>", text)
			} else {
				require.Error(t, err)
				require.Empty(t, text)
			}
		})
	}
}

func TestPK52CandyCompleteTailNotReplayed(t *testing.T) {
	for _, answer := range []string{"21", "22"} {
		t.Run(answer, func(t *testing.T) {
			response := newJSONResponse(http.StatusOK, "")
			response.Body = io.NopCloser(io.MultiReader(strings.NewReader(strings.TrimSpace(pk52CandyTerminal(answer))), iotest.ErrReader(io.ErrUnexpectedEOF)))
			text, err := readAccountCandyReply(response, false)
			require.NoError(t, err)
			require.Equal(t, "<final_answer>"+answer+"</final_answer>", text)
		})
	}
	response := newJSONResponse(http.StatusOK, "")
	response.Body = io.NopCloser(io.MultiReader(strings.NewReader(`data: {"type":"response.failed","response":{"error":{"message":"Invalid model"}}}`), iotest.ErrReader(io.ErrUnexpectedEOF)))
	_, err := readAccountCandyReply(response, false)
	require.ErrorContains(t, err, "Invalid model")
	require.False(t, accountCandyResponseCanRecover(err))
}

func TestPK52CandyRecoveryClassification(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		recover    bool
	}{
		{"progress clean EOF", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n", true},
		{"unframed DONE", "data: [DONE]\n\n", false},
		{"incomplete business", "data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n", false},
		{"failed business", "data: {\"type\":\"response.failed\"}\n\n", false},
		{"malformed", "data: {bad}\n\n", false},
		{"empty final", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readAccountCandyReply(newJSONResponse(http.StatusOK, tc.body), false)
			require.Error(t, err)
			require.Equal(t, tc.recover, accountCandyResponseCanRecover(err))
		})
	}
}

func TestPK52CandyRecoveryBoundAndRevalidation(t *testing.T) {
	for _, tc := range []struct {
		name             string
		failures         int
		cancelAfterFirst bool
		want             int
	}{
		{"one retry", 1, false, 2}, {"exhausted", 10, false, 3}, {"revalidation cancels", 1, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recovery := withAccountCandyRecovery(context.Background(), nil)
			calls := 0
			text, err := runAccountCandyResponseRecovery(ctx, nil, func(context.Context) (string, error) {
				calls++
				if tc.cancelAfterFirst && calls > 1 {
					return "", context.Canceled
				}
				if calls <= tc.failures {
					return "", &accountCandyProbeError{httpStatus: 200, message: "断流", recoverable: true}
				}
				return "<final_answer>22</final_answer>", nil
			})
			require.Equal(t, tc.want, calls)
			if tc.failures == 1 && !tc.cancelAfterFirst {
				require.NoError(t, err)
				require.Contains(t, text, "22")
			} else {
				require.Error(t, err)
				require.Empty(t, text)
			}
			require.Contains(t, recovery.message(), fmt.Sprintf("本轮已请求%d次", tc.want))
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx, recovery := withAccountCandyRecovery(ctx, nil)
	calls := 0
	_, err := runAccountCandyResponseRecovery(ctx, nil, func(context.Context) (string, error) {
		calls++
		cancel()
		return "", &accountCandyProbeError{httpStatus: 200, recoverable: true}
	})
	require.Error(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, recovery.attempts)
}

func TestPK52CandyProxyRecoveryKeepsRouteAndCompletedWrongAnswer(t *testing.T) {
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		newJSONResponse(200, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n"),
		newJSONResponse(200, pk52CandyTerminal("22")),
	}}
	tests := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
	account := &Account{ID: 5252, Platform: PlatformOpenAI, Type: AccountTypeUpstream, Concurrency: 2, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://upstream.example.com"}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, recovery := withAccountCandyRecovery(ctx, nil)
	checked := 0
	text, err := runAccountCandyWithProxyRetry(ctx, nil, tests, account, account, defaultAccountCandyConfig(), func(_ context.Context, route *Account, routeAttempt int) error {
		checked++
		require.Nil(t, route.ProxyID)
		require.Equal(t, 1, routeAttempt)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "<final_answer>22</final_answer>", text)
	require.Equal(t, 2, checked)
	require.Equal(t, 2, recovery.attempts)
}

func TestPK52CandyRetryRevalidationStopsBeforeRequest(t *testing.T) {
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(200, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n")}}
	tests := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
	account := &Account{ID: 5253, Platform: PlatformOpenAI, Type: AccountTypeUpstream, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://upstream.example.com"}}
	ctx, recovery := withAccountCandyRecovery(context.Background(), nil)
	checked := 0
	_, err := runAccountCandyWithProxyRetry(ctx, nil, tests, account, account, defaultAccountCandyConfig(), func(context.Context, *Account, int) error {
		checked++
		if checked > 1 {
			return context.Canceled
		}
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, 1, recovery.attempts)
	_, err = runAccountCandyResponseRecovery(context.Background(), func(context.Context) error { return context.Canceled }, func(context.Context) (string, error) { t.Fatal("must not send"); return "", nil })
	require.ErrorIs(t, err, context.Canceled)
}

func TestPK52CandyProgressFailureDoesNotCountUnsentRequest(t *testing.T) {
	ctx, recovery := withAccountCandyRecovery(context.Background(), func(context.Context, string) error { return context.Canceled })
	recovery.attempts, recovery.last = 1, "断流"
	require.ErrorIs(t, recovery.begin(ctx), context.Canceled)
	require.Equal(t, 1, recovery.attempts)
}

func TestPK52CandyBusinessFailuresNeverReplay(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(status, `{"error":{"message":"rejected"}}`)}}
			tests := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
			account := &Account{ID: 5254, Platform: PlatformOpenAI, Type: AccountTypeUpstream, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://upstream.example.com"}}
			_, err := runAccountCandyWithProxyRetry(context.Background(), nil, tests, account, account, defaultAccountCandyConfig(), nil)
			require.Error(t, err)
			require.Len(t, upstream.requests, 1)
		})
	}
}

func TestPK52CandyUnsentRadarFailureDoesNotConsumeResponseBudget(t *testing.T) {
	ctx, _ := withAccountCandyRecovery(context.Background(), nil)
	ctx = context.WithValue(ctx, accountCandyProxyRetryKey{}, true)
	ctx = context.WithValue(ctx, radarCandyProxyRetryKey{}, true)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid", strings.NewReader("{}"))
	require.NoError(t, err)
	calls := 0
	_, err = doAccountCandyTLSHandshakeRetry(req, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: io.ErrUnexpectedEOF}
	})
	var unsent *accountCandyRetryableNetworkError
	require.ErrorAs(t, err, &unsent)
	require.Equal(t, 1, calls, "proxy recovery owns retries; no nested send retry")
}

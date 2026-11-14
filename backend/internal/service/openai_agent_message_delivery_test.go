package service

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pk64PendingBatch struct {
	issue715BatchOwners
	read func(context.Context, []string) ([]string, error)
}

func (c *pk64PendingBatch) GetOpenAIAgentRecoveries(ctx context.Context, keys []string) ([]string, error) {
	return c.read(ctx, keys)
}

func TestPK64BatchedPendingCannotBecomeUnprovenOwnership(t *testing.T) {
	for _, outcome := range []string{"complete", "missing", "failed", "conflict", "error", "short_batch", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			route := encodeOpenAIAgentRecoveryBinding(openAIAgentRecoveryBinding{accountID: 1})
			keys := [][32]byte{{1}, {2}}
			calls := 0
			cache := &pk64PendingBatch{read: func(_ context.Context, lookup []string) ([]string, error) {
				calls++
				if calls == 1 {
					require.Equal(t, encodeOpenAIAgentRecoveryKeys(keys), lookup)
					if outcome == "cancel" {
						cancel()
					}
					return []string{route, "pending:fixture:" + route}, nil
				}
				require.Equal(t, encodeOpenAIAgentRecoveryKeys(keys[1:]), lookup, "only unresolved fences require another network read")
				switch outcome {
				case "missing":
					return []string{""}, nil
				case "failed", "conflict":
					return []string{outcome}, nil
				case "error":
					return nil, errors.New("fixture store failure")
				case "short_batch":
					return nil, nil
				default:
					return []string{route}, nil
				}
			}}
			svc := &OpenAIGatewayService{}
			bindings, found, err := svc.awaitOpenAIAgentRecoveries(ctx, cache, &svc.agentMessageRecovery, keys)
			switch outcome {
			case "complete":
				require.NoError(t, err)
				require.Equal(t, []bool{true, true}, found)
				require.Equal(t, int64(1), bindings[1].accountID)
			case "conflict":
				require.NoError(t, err)
				require.True(t, bindings[1].conflicted, "caller must still reject the conflicting owner")
			default:
				var recovery *OpenAIAgentRecoveryError
				require.ErrorAs(t, err, &recovery)
				reason := "owner_unavailable"
				if outcome == "error" || outcome == "short_batch" {
					reason = "store_unavailable"
				} else if outcome == "cancel" {
					reason = "delivery_pending"
				}
				require.Equal(t, reason, recovery.Reason)
			}
		})
	}
}

func issue715OutputAttempt(t *testing.T, svc *OpenAIGatewayService, parent context.Context) (context.Context, *gin.Context, func(bool)) {
	t.Helper()
	c, _ := newTurnStateTestContext(t, 715, "fixture-session")
	c.Set("api_key", &APIKey{ID: 715, GroupID: int64Ptr(715)})
	ctx := svc.WithOpenAIAgentMessageRecovery(parent, c, []byte(`{"input":[]}`), "fixture-session", "gpt-6-astra")
	c.Request = c.Request.WithContext(ctx)
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	req, err := http.NewRequest(http.MethodPost, "https://native.example/v1/responses", bytes.NewBufferString(`{"input":[]}`))
	require.NoError(t, err)
	finish, err := svc.prepareOpenAIAgentRecoveryAttempt(ctx, account, req)
	require.NoError(t, err)
	return ctx, c, finish
}

func TestIssue715OutputPendingIsNeverACompatibilityGrant(t *testing.T) {
	for _, outcome := range []string{"commit", "abort", "cancel", "early_only"} {
		t.Run(outcome, func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx, c, finish := issue715OutputAttempt(t, svc, parent)
			item := `{"type":"agent_message","encrypted_content":"fixture-output"}`
			require.NoError(t, observeOpenAIAgentRecoveryOutput(c, []byte(`{"type":"response.output_item.added","item":`+item+`}`), true))
			r := openAIAgentRecoveryFromContext(ctx)
			require.Len(t, r.delivery.keys, 1, "early complete opaque item must be fenced before exposure")
			var key [32]byte
			for k := range r.delivery.keys {
				key = k
			}
			short, stop := context.WithTimeout(context.Background(), time.Millisecond)
			_, _, err := svc.awaitOpenAIAgentRecovery(short, nil, &svc.agentMessageRecovery, key)
			stop()
			require.Error(t, err, "pending must wait or fail, never authorize dispatch")
			if outcome != "early_only" {
				require.NoError(t, observeOpenAIAgentRecoveryOutput(c, []byte(`{"status":"completed","output":[`+item+`]}`), false))
			}
			if outcome == "cancel" {
				cancel()
			}
			finish(outcome != "abort")
			finish(false) // deferred cleanup is idempotent
			binding, found, err := svc.awaitOpenAIAgentRecovery(context.Background(), nil, &svc.agentMessageRecovery, key)
			if outcome == "commit" {
				require.NoError(t, err)
				require.True(t, found)
				require.Empty(t, binding.pending)
				require.False(t, binding.failed)
			} else {
				require.Error(t, err, "failure and early-only output must not become first-seen or a completed owner")
			}
		})
	}
}

func TestIssue715PendingLookupHonorsCancellationAndMissingFence(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	ctx, c, finish := issue715OutputAttempt(t, svc, context.Background())
	defer finish(false)
	require.NoError(t, observeOpenAIAgentRecoveryOutput(c, []byte(`{"status":"completed","output":[{"type":"agent_message","encrypted_content":"fixture-output"}]}`), false))
	var key [32]byte
	for k := range openAIAgentRecoveryFromContext(ctx).delivery.keys {
		key = k
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := svc.awaitOpenAIAgentRecovery(canceled, nil, &svc.agentMessageRecovery, key)
	require.Error(t, err)
}

func TestIssue715ObservedPendingCannotDowngradeToMiss(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	var key [32]byte
	key[0] = 71
	cache := &issue715PendingThenMissing{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, found, err := svc.awaitOpenAIAgentRecovery(ctx, cache, &svc.agentMessageRecovery, key)
	require.Error(t, err)
	require.False(t, found, "an observed producer fence cannot become a first-seen task")
	require.Equal(t, 2, cache.reads)
}

type issue715PendingThenMissing struct{ reads int }

func (c *issue715PendingThenMissing) GetOpenAIAgentRecovery(context.Context, string) (string, error) {
	c.reads++
	if c.reads == 1 {
		b := openAIAgentRecoveryBinding{accountID: 1}
		return "pending:fixture:" + encodeOpenAIAgentRecoveryBinding(b), nil
	}
	return "", nil
}
func (c *issue715PendingThenMissing) PublishOpenAIAgentRecovery(context.Context, string, string, time.Duration) error {
	return nil
}

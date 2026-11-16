package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func issue715WSManager(t *testing.T, svc *OpenAIGatewayService, parent context.Context) (*openAIWSAgentRecovery, *gin.Context, *Account) {
	t.Helper()
	c, _ := newTurnStateTestContext(t, 715, "fixture-session")
	c.Set("api_key", &APIKey{ID: 715, GroupID: int64Ptr(715)})
	ctx := svc.WithOpenAIWSAgentRecovery(parent, c, []byte(`{"model":"gpt-6-astra","input":[]}`), "fixture-session", "gpt-6-astra")
	return newOpenAIWSAgentRecovery(svc, ctx, c), c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
}

func issue715WSBegin(t *testing.T, manager *openAIWSAgentRecovery, account *Account, source string, mode string) error {
	t.Helper()
	return manager.beginSocket(account, []byte(source), []byte(source), "wss://native.example/v1/responses", http.Header{"Authorization": {"Bearer fixture"}}, mode)
}

func TestPK70WSDisabledAccountLockRetainsReplayProtection(t *testing.T) {
	for _, mode := range []string{"ctx_pool", "passthrough", "http_bridge"} {
		for _, inputOpaque := range []bool{false, true} {
			name := mode + "/generated_output"
			if inputOpaque {
				name = mode + "/opaque_input"
			}
			t.Run(name, func(t *testing.T) {
				cache := &issue715BatchOwners{}
				producer := &OpenAIGatewayService{cache: cache, cfg: &config.Config{}}
				issue715LearnRoute(t, producer, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, issue715OpaqueBody)
				counts := []int{cache.reads, cache.publishes, cache.reserves, cache.finishes, cache.refreshes}
				before := make(map[string]string, len(cache.values))
				for key, value := range cache.values {
					before[key] = value
				}
				cache.batchError = errors.New("fixture owner store unavailable")
				cfg := &config.Config{}
				cfg.Gateway.DisableEncryptedAccountLock = true
				svc := &OpenAIGatewayService{cache: cache, cfg: cfg}
				manager, _, account := issue715WSManager(t, svc, context.Background())
				require.NotNil(t, manager)
				defer manager.abort()
				account.ID = 2
				source := `{"model":"gpt-6-astra","input":[]}`
				if inputOpaque {
					source = issue715OpaqueBody
				}
				payload := []byte(source)
				if mode == "http_bridge" {
					req, err := http.NewRequest(http.MethodPost, "https://native.example/v1/responses", bytes.NewReader(payload))
					require.NoError(t, err)
					require.NoError(t, manager.begin(account, payload, payload, req, mode))
				} else {
					require.NoError(t, issue715WSBegin(t, manager, account, source, mode))
				}
				require.Equal(t, source, string(payload), "the switch must leave forwarded history intact")
				require.True(t, manager.request.accountLockDisabled)
				require.Nil(t, manager.request.delivery)
				require.Nil(t, manager.request.owner)
				require.Equal(t, !inputOpaque, OpenAIWSAgentRecoveryCanRetry(manager.ctx))
				item := gjson.Get(issue715OpaqueBody, "input.0").Raw
				frame := []byte(`{"type":"response.output_item.done","item":` + item + `}`)
				writes := 0
				require.NoError(t, manager.write(frame, func() error {
					writes++
					require.False(t, OpenAIWSAgentRecoveryCanRetry(manager.ctx), "opaque output must block replay before any downstream bytes are exposed")
					return nil
				}))
				require.Equal(t, `{"type":"response.output_item.done","item":`+item+`}`, string(frame))
				require.NoError(t, manager.write([]byte(`{"type":"response.completed","response":{"id":"resp_disabled_ws","status":"completed","output":[`+item+`]}}`), func() error {
					writes++
					return nil
				}))
				require.Equal(t, 2, writes)
				// A new turn can select another permitted account without consulting
				// or republishing the old encrypted owner.
				account.ID = 3
				require.NoError(t, issue715WSBegin(t, manager, account, issue715OpaqueBody, mode))
				require.False(t, OpenAIWSAgentRecoveryCanRetry(manager.ctx))
				require.Empty(t, manager.known)
				writeErr := errors.New("fixture downstream write failed")
				require.ErrorIs(t, manager.write(frame, func() error { return writeErr }), writeErr)
				manager.abort()
				require.Equal(t, counts, []int{cache.reads, cache.publishes, cache.reserves, cache.finishes, cache.refreshes})
				require.Equal(t, before, cache.values)
				require.Empty(t, svc.agentMessageRecovery.bindings)
			})
		}
	}
}

func TestPK70WSDisabledAccountLockSkipsWireOwnershipValidation(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.DisableEncryptedAccountLock = true
	manager, _, account := issue715WSManager(t, &OpenAIGatewayService{cfg: cfg}, context.Background())
	defer manager.abort()
	req, err := http.NewRequest(http.MethodPost, "https://native.example/v1/responses", bytes.NewBufferString(issue715OpaqueBody))
	require.NoError(t, err)
	reads := 0
	req.GetBody = func() (io.ReadCloser, error) {
		reads++
		return nil, errors.New("fixture owner fingerprint unavailable")
	}
	require.NoError(t, manager.begin(account, []byte(issue715OpaqueBody), []byte(issue715OpaqueBody), req, "http_bridge"))
	require.Zero(t, reads, "disabled ownership must not reject a normal send because a compatibility fingerprint cannot be built")
	require.False(t, OpenAIWSAgentRecoveryCanRetry(manager.ctx))
}

func TestIssue715WSActualDeliveryFence(t *testing.T) {
	item := gjson.Get(issue715OpaqueBody, "input.0").Raw
	for _, outcome := range []string{"commit", "error", "cancel", "incomplete", "early_only", "eof", "items_then_terminal"} {
		t.Run(outcome, func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			manager, _, account := issue715WSManager(t, svc, parent)
			defer manager.abort()
			require.NoError(t, issue715WSBegin(t, manager, account, `{"model":"gpt-6-astra","input":[]}`, "ctx_pool"))
			terminal := `{"type":"response.completed","response":{"id":"resp_fixture_ws715","status":"completed","output":[` + item + `]}}`
			if outcome == "incomplete" {
				terminal = strings.ReplaceAll(strings.ReplaceAll(terminal, "response.completed", "response.incomplete"), `"status":"completed"`, `"status":"incomplete"`)
			}
			if outcome == "early_only" || outcome == "eof" || outcome == "items_then_terminal" {
				terminal = `{"type":"response.output_item.done","item":` + item + `}`
			}
			writes := 0
			err := manager.write([]byte(terminal), func() error {
				writes++
				require.Len(t, manager.request.delivery.keys, 1, "real write must observe a prior exposure reservation")
				for key := range manager.request.delivery.keys {
					short, stop := context.WithTimeout(context.Background(), time.Millisecond)
					_, _, err := svc.awaitOpenAIAgentRecovery(short, nil, &svc.agentMessageRecovery, key)
					stop()
					require.Error(t, err, "write in progress is never a successful grant")
				}
				if outcome == "cancel" {
					cancel()
				}
				if outcome == "error" {
					return errors.New("fixture socket write failed")
				}
				return nil
			})
			if outcome == "error" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if outcome == "items_then_terminal" {
				require.NoError(t, manager.write([]byte(`{"type":"response.completed","response":{"id":"resp_fixture_ws715","status":"completed","output":[]}}`), func() error { writes++; return nil }))
			}
			manager.abort() // also covers upstream EOF before a successful terminal
			for key := range manager.request.delivery.keys {
				binding, found, err := svc.awaitOpenAIAgentRecovery(context.Background(), nil, &svc.agentMessageRecovery, key)
				if outcome == "commit" || outcome == "items_then_terminal" {
					require.NoError(t, err)
					require.True(t, found)
					require.Empty(t, binding.pending)
					owner, err := svc.getOpenAIWSStateStore().GetResponseAccount(context.Background(), 715, "resp_fixture_ws715")
					require.NoError(t, err)
					require.Equal(t, int64(1), owner)
				} else {
					require.Error(t, err, "failed delivery must retain a rejection fence")
				}
			}
			require.Positive(t, writes)
			require.False(t, OpenAIWSAgentRecoveryCanRetry(manager.ctx), "an exposed task cannot be automatically replayed")
		})
	}
}

func TestIssue715WSFinalSendRechecksBinding(t *testing.T) {
	for _, mutation := range []string{"same", "account", "credentials", "adapter", "previous", "lost", "expired"} {
		t.Run(mutation, func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			manager, _, account := issue715WSManager(t, svc, context.Background())
			defer manager.abort()
			require.NoError(t, issue715WSBegin(t, manager, account, `{"model":"gpt-6-astra","input":[]}`, "ctx_pool"))
			item := gjson.Get(issue715OpaqueBody, "input.0").Raw
			require.NoError(t, manager.write([]byte(`{"type":"response.completed","response":{"id":"resp_ws715","status":"completed","output":[`+item+`]}}`), func() error { return nil }))
			body, mode := issue715OpaqueBody, "ctx_pool"
			switch mutation {
			case "account":
				account.ID = 2
			case "credentials":
				account.Credentials = map[string]any{"api_key": "fixture-rotated"}
			case "adapter":
				mode = "passthrough"
			case "previous":
				body = strings.Replace(body, `"input":`, `"previous_response_id":"resp_other_account","input":`, 1)
				require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(context.Background(), 715, "resp_other_account", 2, time.Minute))
			case "lost", "expired":
				svc.agentMessageRecovery.mu.Lock()
				for key, binding := range svc.agentMessageRecovery.bindings {
					if mutation == "lost" {
						delete(svc.agentMessageRecovery.bindings, key)
					} else {
						binding.expiresAt = time.Now().Add(-time.Second)
						svc.agentMessageRecovery.bindings[key] = binding
					}
				}
				svc.agentMessageRecovery.mu.Unlock()
			}
			err := issue715WSBegin(t, manager, account, body, mode)
			if mutation == "same" {
				require.NoError(t, err)
			} else {
				require.Error(t, err, "changed binding must fail before any generation write")
			}
		})
	}
}

func TestIssue715WSBridgeValidatesBuiltPayload(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	manager, _, account := issue715WSManager(t, svc, context.Background())
	defer manager.abort()
	pure := `{"model":"gpt-6-astra","input":[]}`
	newRequest := func(body string) *http.Request {
		req, err := http.NewRequest(http.MethodPost, "https://native.example/v1/responses", bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer fixture")
		return req
	}
	require.NoError(t, manager.begin(account, []byte(pure), []byte(pure), newRequest(pure), "http_bridge"))
	item := gjson.Get(issue715OpaqueBody, "input.0").Raw
	require.NoError(t, manager.write([]byte(`{"type":"response.completed","response":{"id":"resp_built715","status":"completed","output":[`+item+`]}}`), func() error { return nil }))
	// The bridge's pre-builder payload is intact; the actual request builder
	// has changed an item field. A real provider must never receive this change
	// under an ownership grant for the original client representation.
	built := strings.Replace(issue715OpaqueBody, "fixture envelope", "fixture rewritten envelope", 1)
	err := manager.begin(account, []byte(issue715OpaqueBody), []byte(issue715OpaqueBody), newRequest(built), "http_bridge")
	require.Error(t, err, "bridge must validate actual built HTTP body before granting opaque task admission")
}

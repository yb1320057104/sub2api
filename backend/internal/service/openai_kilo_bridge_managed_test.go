package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func managedKiloTestAccount() *Account {
	return &Account{ID: 41001, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "synthetic-kilo-key", "base_url": KiloGatewayBaseURL,
			KiloGatewayMachineIDCredential: "2cb88f6e-2f43-4d16-8f28-c8f92c6c245f", KiloGatewayOrganizationIDCredential: "org-synthetic"},
		Extra: map[string]any{AccountExtraKiloBridgeEnabled: true, KiloGatewayManagedExtra: true, KiloGatewayProfileExtra: KiloGatewayCLIProfile},
	}
}

func requireNoKiloMachineIDHeader(t *testing.T, headers http.Header) {
	t.Helper()
	for name := range headers {
		require.False(t, strings.EqualFold(name, "X-KILOCODE-MACHINEID"), "retired Kilo machine header %q must not be sent", name)
	}
}

func TestKiloManagedAgentHistoryPreservesContent(t *testing.T) {
	account := managedKiloTestAccount()
	body := []byte(`{"model":"gpt-6-astra.Exact","nonce":1844674407370955161601,"input":[{"type":"agent_message","id":"agent_original","author":"planner","recipient":"worker","sequence":90071992547409931234,"content":[{"type":"input_text","text":"result"},{"type":"encrypted_content","encrypted_content":"gAAAA+opaque/密文==","format_version":90071992547409931235},{"type":"encrypted_content","encrypted_content":"second-cipher"}]},{"type":"function_call","id":"fc_original","call_id":"call_original","namespace":"collaboration","name":"spawn_agent","arguments":"{\"n\":90071992547409931236,\"cipher\":\"opaque\"}"}]}`)
	converted, err := translateKiloResponsesRequest(account, body)
	require.NoError(t, err)
	require.Equal(t, "openai/gpt-6-astra.Exact", gjson.GetBytes(converted, "model").String())
	require.Equal(t, "1844674407370955161601", gjson.GetBytes(converted, "nonce").Raw)
	items := gjson.GetBytes(converted, "input").Array()
	require.Len(t, items, 6)
	for i, ciphertext := range []string{"gAAAA+opaque/密文==", "second-cipher"} {
		call, result := items[i*2], items[i*2+1]
		require.Equal(t, "custom_tool_call", call.Get("type").String())
		require.Equal(t, "collaboration", call.Get("namespace").String())
		require.Equal(t, "send_message", call.Get("name").String())
		require.NotEmpty(t, call.Get("call_id").String())
		require.Equal(t, call.Get("call_id").String(), result.Get("call_id").String())
		require.Equal(t, "custom_tool_call_output", result.Get("type").String())
		arguments := call.Get("input").String()
		require.Equal(t, "worker", gjson.Get(arguments, "target").String())
		require.Equal(t, ciphertext, gjson.Get(arguments, "message").String())
		output := result.Get("output.0.text").String()
		require.Equal(t, "planner", gjson.Get(output, "author").String())
		require.Equal(t, "worker", gjson.Get(output, "recipient").String())
		require.Equal(t, int64(i+1), gjson.Get(output, "content_index").Int())
	}
	require.Equal(t, "90071992547409931235", gjson.Get(items[1].Get("output.0.text").String(), "content_metadata.format_version").Raw)
	routing := strings.TrimPrefix(items[4].Get("content.0.text").String(), "Received agent communication:\n")
	require.Equal(t, "agent_original", gjson.Get(routing, "id").String())
	require.Equal(t, "90071992547409931234", gjson.Get(routing, "sequence").Raw)
	require.Equal(t, "result", items[4].Get("content.1.text").String())
	require.Contains(t, items[4].Get("content.2.text").String(), items[0].Get("call_id").String())
	require.Contains(t, items[4].Get("content.3.text").String(), items[2].Get("call_id").String())
	require.Equal(t, "call_original", items[5].Get("call_id").String())
	require.Equal(t, gjson.GetBytes(body, "input.1.arguments").String(), items[5].Get("input").String())
	require.False(t, gjson.GetBytes(converted, "tools").Exists(), "received history must not register tools")
	repeated, err := translateKiloResponsesRequest(account, body)
	require.NoError(t, err)
	require.Equal(t, converted, repeated, "history call IDs must be deterministic")
	replayed, err := translateKiloResponsesRequest(account, converted)
	require.NoError(t, err)
	require.Equal(t, converted, replayed, "translation must be idempotent")
}

func TestKiloManagedAgentHistoryRejectsUnknownStructures(t *testing.T) {
	for _, item := range []string{
		`{"type":"agent_message","author":"a","recipient":"b","content":"opaque"}`,
		`{"type":"agent_message","author":"a","recipient":"b","content":[{"type":"input_image","image_url":"unhandled"}]}`,
		`{"type":"agent_message","author":"a","recipient":"b","content":[{"type":"encrypted_content","encrypted_content":23}]}`,
		`{"type":"agent_message","author":"a","recipient":"b","content":[{"type":"input_text","text":23}]}`,
		`{"type":"agent_message","content":[{"type":"input_text","text":"missing route"}]}`,
		`{"type":"agent_message","author":"a","recipient":23,"content":[]}`,
	} {
		converted, err := translateKiloResponsesRequest(managedKiloTestAccount(), []byte(`{"model":"exact","input":[`+item+`]}`))
		require.Error(t, err)
		require.Nil(t, converted)
		require.NotContains(t, err.Error(), "opaque", "errors must not echo opaque content")
	}
	legacy := managedKiloTestAccount()
	delete(legacy.Extra, KiloGatewayManagedExtra)
	plain := []byte(`{"model":"Legacy/Exact","input":[{"type":"agent_message","content":[{"type":"input_text","text":"legacy plain"}]}]}`)
	converted, err := translateKiloResponsesRequest(legacy, plain)
	require.NoError(t, err)
	require.Equal(t, "Legacy/Exact", gjson.GetBytes(converted, "model").String())
	require.Equal(t, "legacy plain", gjson.GetBytes(converted, "input.0.content.0.text").String())
}

func TestKiloManagedRequestIdentityAcrossBuilders(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%v", passthrough), func(t *testing.T) {
			account := managedKiloTestAccount()
			machineID := account.GetCredential(KiloGatewayMachineIDCredential)
			account.Credentials["header_override_enabled"] = true
			account.Credentials["header_overrides"] = map[string]any{
				"x-kilocode-machineid":      "account-forged-machine",
				"X-KILOCODE-MACHINEID":      "account-forged-uppercase",
				"X-KiloCode-OrganizationId": "account-forged-org",
			}
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{ForceCodexCLI: true}}}
			body := []byte(`{"model":"Exact.Version","prompt_cache_key":"shared-prompt","input":"hello"}`)
			build := func(c *gin.Context) *http.Request {
				var request *http.Request
				var err error
				if passthrough {
					request, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "synthetic-kilo-key")
				} else {
					request, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "synthetic-kilo-key", false, "", false)
				}
				require.NoError(t, err)
				return request
			}
			newContext := func() *gin.Context {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
				c.Request.Header.Set("User-Agent", "downstream-forged")
				c.Request.Header.Set("X-KiloCode-MachineId", "forged-machine")
				c.Request.Header["x-kilocode-machineid"] = []string{"client-forged-lowercase"}
				c.Request.Header["X-kIlOcOdE-mAcHiNeId"] = []string{"client-forged-mixed-case"}
				c.Request.Header.Set("X-KiloCode-OrganizationId", "forged-org")
				c.Request.Header.Set("X-Session-Id", "client-session")
				return c
			}
			request := build(newContext())
			require.Equal(t, KiloGatewayBaseURL+"/responses", request.URL.String())
			require.Equal(t, "Bearer synthetic-kilo-key", request.Header.Get("Authorization"))
			require.Equal(t, "Kilo-Code/"+KiloGatewayCLIVersion+" ai-sdk/provider-utils/4.0.40 runtime/bun/1.4.4", request.UserAgent())
			require.Equal(t, "Kilo CLI "+KiloGatewayCLIVersion, request.Header.Get("X-KiloCode-EditorName"))
			require.Equal(t, "cli", request.Header.Get("X-KiloCode-Feature"))
			require.Equal(t, "code", request.Header.Get("X-KiloCode-Mode"))
			requireNoKiloMachineIDHeader(t, request.Header)
			require.Equal(t, machineID, account.GetCredential(KiloGatewayMachineIDCredential), "forwarding must retain the stored machine ID")
			require.Equal(t, "org-synthetic", request.Header.Get("X-KiloCode-OrganizationId"))
			session := request.Header.Get("X-Session-Id")
			require.NotEmpty(t, session)
			require.NotEqual(t, "client-session", session)
			require.Equal(t, session, request.Header.Get("X-KiloCode-TaskId"))
			require.Equal(t, session, request.Header.Get("X-Session-Affinity"))
			require.Equal(t, session, build(newContext()).Header.Get("X-Session-Id"))
			wire, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			require.Equal(t, "openai/Exact.Version", gjson.GetBytes(wire, "model").String())
			account.Credentials[KiloGatewayOrganizationIDCredential] = ""
			require.Empty(t, build(newContext()).Header.Get("X-KiloCode-OrganizationId"))
			for _, storedID := range []string{"", "legacy-non-uuid-id"} {
				account.Credentials[KiloGatewayMachineIDCredential] = storedID
				request = build(newContext())
				requireNoKiloMachineIDHeader(t, request.Header)
				require.Equal(t, storedID, account.GetCredential(KiloGatewayMachineIDCredential))
				require.Equal(t, session, request.Header.Get("X-Session-Id"))
			}
			delete(account.Credentials, KiloGatewayMachineIDCredential)
			request = build(newContext())
			requireNoKiloMachineIDHeader(t, request.Header)
			require.NotContains(t, account.Credentials, KiloGatewayMachineIDCredential, "forwarding must not generate a replacement machine ID")
		})
	}
}

func TestKiloManagedIdentityStripsMachineIDAndRetainsBearer(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, KiloGatewayBaseURL+"/responses", nil)
	request.Header["x-kilocode-machineid"] = []string{"forged"}
	request.Header["X-KILOCODE-MACHINEID"] = []string{"other-forged"}
	request.Header["X-kIlOcOdE-mAcHiNeId"] = []string{"mixed-case-forged"}
	request.Header.Set("X-KiloCode-MachineId", "canonical-forged")
	request.Header["x-kilocode-organizationid"] = []string{"forged-org"}
	request.Header.Set("Authorization", "Bearer synthetic")
	account := managedKiloTestAccount()
	require.NoError(t, applyKiloGatewayIdentity(request, nil, account, []byte(`{"model":"openai/exact"}`), "cache"))
	require.Equal(t, "Bearer synthetic", request.Header.Get("Authorization"))
	requireNoKiloMachineIDHeader(t, request.Header)
	require.Equal(t, "2cb88f6e-2f43-4d16-8f28-c8f92c6c245f", account.GetCredential(KiloGatewayMachineIDCredential))
	session := request.Header.Get("X-Session-Id")
	require.NotEmpty(t, session)
	require.Equal(t, session, request.Header.Get("X-KiloCode-TaskId"))
	require.Equal(t, session, request.Header.Get("X-Session-Affinity"))
	require.Equal(t, "org-synthetic", request.Header.Get("X-KiloCode-OrganizationId"))
	legacy := managedKiloTestAccount()
	delete(legacy.Extra, KiloGatewayManagedExtra)
	request.Header.Set("User-Agent", "legacy-identity")
	request.Header.Set("X-KiloCode-MachineId", "legacy-machine")
	request.Header["x-kilocode-machineid"] = []string{"legacy-lowercase-machine"}
	legacyHeaders := request.Header.Clone()
	require.NoError(t, applyKiloGatewayIdentity(request, nil, legacy, []byte(`{}`), ""))
	require.Equal(t, legacyHeaders, request.Header, "legacy headers must remain untouched")
}

func TestKiloManagedContinuationRetainsRandomSession(t *testing.T) {
	account := managedKiloTestAccount()
	account.ID = 41002
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	identity, err := resolveKiloGatewaySession(c, account, []byte(`{"model":"openai/exact"}`), "")
	require.NoError(t, err)
	_, err = uuid.Parse(identity.sessionID)
	require.NoError(t, err)
	result := &OpenAIForwardResult{ResponseID: "resp_synthetic_continuation", Model: "exact", Usage: OpenAIUsage{InputTokens: 7, OutputTokens: 3}}
	stampKiloBridgeUsage(c.Request.Context(), account, nil, result)
	require.Equal(t, "openai/exact", result.UpstreamModel)
	key := kiloGatewayResponseSessionKey(account.ID, 0, result.ResponseID)
	t.Cleanup(func() {
		kiloGatewaySessions.Lock()
		delete(kiloGatewaySessions.bindings, key)
		kiloGatewaySessions.Unlock()
	})
	followup, err := resolveKiloGatewaySession(nil, account, []byte(`{"model":"openai/exact","previous_response_id":"resp_synthetic_continuation"}`), "")
	require.NoError(t, err)
	require.Equal(t, identity.sessionID, followup.sessionID)
	_, err = resolveKiloGatewaySession(nil, account, []byte(`{"model":"openai/exact","previous_response_id":"resp_unknown"}`), "")
	require.ErrorContains(t, err, "continuation session is unavailable")
	seeded, err := resolveKiloGatewaySession(nil, account, []byte(`{"previous_response_id":"resp_unknown","prompt_cache_key":"original-stable-seed"}`), "")
	require.NoError(t, err)
	repeat, err := resolveKiloGatewaySession(nil, account, []byte(`{"prompt_cache_key":"original-stable-seed"}`), "")
	require.NoError(t, err)
	require.Equal(t, repeat.sessionID, seeded.sessionID)
}

type kiloGatewaySharedSessionTestData struct {
	sessions    map[string]string
	reads       int
	writes      int
	writeCtx    context.Context
	writeCtxErr error
	readErr     error
	writeErr    error
}

type kiloGatewaySharedSessionTestCache struct {
	GatewayCache
	data *kiloGatewaySharedSessionTestData
}

func (s *kiloGatewaySharedSessionTestCache) GetKiloGatewayResponseSession(ctx context.Context, accountID, apiKeyID int64, responseID string) (string, error) {
	s.data.reads++
	return s.data.sessions[kiloGatewayResponseSessionKey(accountID, apiKeyID, responseID)], s.data.readErr
}

func (s *kiloGatewaySharedSessionTestCache) SetKiloGatewayResponseSession(ctx context.Context, accountID, apiKeyID int64, responseID, sessionID string, ttl time.Duration) error {
	s.data.writes++
	s.data.writeCtx = ctx
	s.data.writeCtxErr = ctx.Err()
	if s.data.writeErr != nil {
		return s.data.writeErr
	}
	s.data.sessions[kiloGatewayResponseSessionKey(accountID, apiKeyID, responseID)] = sessionID
	return nil
}

func TestKiloManagedSharedSessionCrossGatewayContinuation(t *testing.T) {
	account := managedKiloTestAccount()
	account.ID = 41003
	shared := &kiloGatewaySharedSessionTestData{sessions: make(map[string]string)}
	// Different GatewayCache instances stand in for workers sharing Redis.
	firstWorker := &OpenAIGatewayService{cache: &kiloGatewaySharedSessionTestCache{data: shared}}
	secondWorker := &OpenAIGatewayService{cache: &kiloGatewaySharedSessionTestCache{data: shared}}
	first, _ := gin.CreateTestContext(httptest.NewRecorder())
	requestCtx, cancel := context.WithCancel(context.Background())
	first.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestCtx)
	request := httptest.NewRequest(http.MethodPost, KiloGatewayBaseURL+"/responses", nil)
	require.NoError(t, firstWorker.applyKiloGatewayIdentity(request, first, account, []byte(`{"model":"openai/exact"}`), ""))
	require.Same(t, firstWorker.cache, kiloGatewaySessionStoreFromContext(request.Context()))
	require.Same(t, firstWorker.cache, kiloGatewaySessionStoreFromContext(first.Request.Context()))
	session := request.Header.Get("X-Session-Id")
	_, err := uuid.Parse(session)
	require.NoError(t, err)
	cancel()
	result := &OpenAIForwardResult{ResponseID: "resp_shared_no_seed", Model: "exact", Usage: OpenAIUsage{InputTokens: 7, OutputTokens: 3}}
	stampKiloBridgeUsage(first.Request.Context(), account, nil, result)
	require.Equal(t, 1, shared.writes)
	require.NotNil(t, shared.writeCtx)
	require.NoError(t, shared.writeCtxErr, "shared publication must survive client cancellation")
	require.Equal(t, "openai/exact", result.UpstreamModel)
	// Shared metadata must not also rely on the local fallback cache.
	require.Empty(t, lookupKiloGatewayResponseSession(account.ID, 0, result.ResponseID))
	second, _ := gin.CreateTestContext(httptest.NewRecorder())
	second.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	followup := httptest.NewRequest(http.MethodPost, KiloGatewayBaseURL+"/responses", nil)
	require.NoError(t, secondWorker.applyKiloGatewayIdentity(followup, second, account, []byte(`{"model":"openai/exact","previous_response_id":"resp_shared_no_seed"}`), ""))
	require.Equal(t, session, followup.Header.Get("X-Session-Id"))
	require.Equal(t, 1, shared.reads)
	wrongAccount := managedKiloTestAccount()
	wrongAccount.ID = account.ID + 1
	request = httptest.NewRequest(http.MethodPost, KiloGatewayBaseURL+"/responses", nil)
	require.ErrorContains(t, secondWorker.applyKiloGatewayIdentity(request, nil, wrongAccount, []byte(`{"previous_response_id":"resp_shared_no_seed"}`), ""), "continuation session is unavailable")
}

func TestKiloManagedSharedSessionContextInjection(t *testing.T) {
	account := managedKiloTestAccount()
	shared := &kiloGatewaySharedSessionTestData{sessions: map[string]string{
		kiloGatewayResponseSessionKey(account.ID, 0, "resp_context"): "ses_shared_context",
	}}
	store := &kiloGatewaySharedSessionTestCache{data: shared}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(WithKiloGatewaySessionStore(context.Background(), store))
	request := httptest.NewRequest(http.MethodPost, KiloGatewayBaseURL+"/responses", nil)
	require.NoError(t, (&OpenAIGatewayService{}).applyKiloGatewayIdentity(request, c, account, []byte(`{"previous_response_id":"resp_context"}`), ""))
	require.Equal(t, "ses_shared_context", request.Header.Get("X-Session-Id"))
	require.Equal(t, 1, shared.reads)
}

func TestKiloManagedSharedSessionFailureDoesNotRebind(t *testing.T) {
	account := managedKiloTestAccount()
	shared := &kiloGatewaySharedSessionTestData{sessions: make(map[string]string), readErr: errors.New("shared store unavailable")}
	svc := &OpenAIGatewayService{cache: &kiloGatewaySharedSessionTestCache{data: shared}}
	request := httptest.NewRequest(http.MethodPost, KiloGatewayBaseURL+"/responses", nil)
	err := svc.applyKiloGatewayIdentity(request, nil, account, []byte(`{"previous_response_id":"resp_shared","prompt_cache_key":"seed"}`), "")
	require.ErrorContains(t, err, "read shared Kilo continuation session")
	require.Empty(t, request.Header.Get("X-Session-Id"))
	shared.readErr = nil
	shared.writeErr = errors.New("shared write unavailable")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	require.NoError(t, svc.applyKiloGatewayIdentity(request, c, account, []byte(`{"model":"openai/exact"}`), ""))
	result := &OpenAIForwardResult{ResponseID: "resp_write_failed", Usage: OpenAIUsage{InputTokens: 5, OutputTokens: 2}}
	require.Error(t, bindKiloGatewayResponseSession(c.Request.Context(), result))
	require.Empty(t, shared.sessions)
	stampKiloBridgeUsage(c.Request.Context(), account, nil, result)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, "openai/exact", result.UpstreamModel)
	followup := httptest.NewRequest(http.MethodPost, KiloGatewayBaseURL+"/responses", nil)
	require.ErrorContains(t, svc.applyKiloGatewayIdentity(followup, nil, account, []byte(`{"previous_response_id":"resp_write_failed"}`), ""), "continuation session is unavailable")
	require.Empty(t, followup.Header.Get("X-Session-Id"))
}

func TestKiloManagedRejectedFieldDoesNotReplayPOST(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%v", passthrough), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusBadRequest,
				Header: http.Header{"Content-Type": {"application/json"}},
				Body:   io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","code":"unsupported_parameter","param":"parallel_tool_calls","message":"Unsupported parameter: parallel_tool_calls"}}`)),
			}}
			account := managedKiloTestAccount()
			account.Extra["openai_passthrough"] = passthrough
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			body := []byte(`{"model":"Exact.Model","stream":false,"input":"hello","parallel_tool_calls":true}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.Nil(t, result)
			require.Len(t, upstream.requests, 1, "provider rejection must not consume a compatibility retry budget")
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func TestKiloManagedModelMetadataScopeAndLegacy(t *testing.T) {
	body := []byte(`{"model":"openai/Exact","response":{"model":"openai/Nested"},"text":"openai/generated","tool":{"model":"openai/opaque"},"usage":{"input_tokens":90071992547409931234}}`)
	account := managedKiloTestAccount()
	converted, err := rewriteKiloBridgeResponseHTTPBody(nil, account, body)
	require.NoError(t, err)
	require.Equal(t, "Exact", gjson.GetBytes(converted, "model").String())
	require.Equal(t, "Nested", gjson.GetBytes(converted, "response.model").String())
	require.Equal(t, "openai/opaque", gjson.GetBytes(converted, "tool.model").String())
	require.Equal(t, "openai/generated", gjson.GetBytes(converted, "text").String())
	require.Equal(t, "90071992547409931234", gjson.GetBytes(converted, "usage.input_tokens").Raw)
	wire := []byte("event: response.completed\ndata: " + string(body) + "\n\ndata: [DONE]\n\n")
	streamed, err := rewriteKiloBridgeResponseHTTPBody(nil, account, wire)
	require.NoError(t, err)
	require.Contains(t, string(streamed), `"model":"Exact"`)
	require.Contains(t, string(streamed), "data: [DONE]")
	delete(account.Extra, KiloGatewayManagedExtra)
	unchanged, err := rewriteKiloBridgeResponseHTTPBody(nil, account, wire)
	require.NoError(t, err)
	require.Equal(t, wire, unchanged)
	for _, model := range []string{"openai/Exact", "provider/Exact", "Case.Exact"} {
		request, err := json.Marshal(map[string]any{"model": model, "input": "hello"})
		require.NoError(t, err)
		legacy, err := translateKiloResponsesRequest(account, request)
		require.NoError(t, err)
		require.Equal(t, model, gjson.GetBytes(legacy, "model").String())
	}
}

func TestKiloManagedForwardProtocolsKeepUsageAndModel(t *testing.T) {
	for _, protocol := range []string{"responses", "passthrough", "chat", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", protocol, stream), func(t *testing.T) {
				response := `{"id":"resp_managed_protocol","object":"response","created_at":1700000000,"status":"completed","model":"openai/Exact.Model","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":13,"output_tokens":5,"input_tokens_details":{"cached_tokens":7}}}`
				wire := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_managed_protocol\",\"model\":\"openai/Exact.Model\",\"status\":\"in_progress\"}}\n\n" +
					"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}}
				account := managedKiloTestAccount()
				delete(account.Credentials, KiloGatewayMachineIDCredential)
				account.Extra["openai_passthrough"] = protocol == "passthrough"
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}, httpUpstream: upstream}
				request := map[string]any{"model": "Exact.Model", "stream": stream, "prompt_cache_key": "synthetic-conversation"}
				path := "/v1/responses"
				if protocol == "chat" || protocol == "messages" {
					request["messages"] = []any{map[string]any{"role": "user", "content": "hello"}}
					path = "/v1/chat/completions"
					if protocol == "messages" {
						request["max_tokens"] = 1024
						path = "/v1/messages"
					}
				} else {
					request["input"] = "hello"
				}
				body, err := json.Marshal(request)
				require.NoError(t, err)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				var result *OpenAIForwardResult
				switch protocol {
				case "chat":
					result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "synthetic-conversation", "")
				case "messages":
					result, err = svc.ForwardAsAnthropic(context.Background(), c, account, body, "synthetic-conversation", "")
				default:
					result, err = svc.Forward(context.Background(), c, account, body)
				}
				require.NoError(t, err)
				require.Len(t, upstream.requests, 1, "a submitted POST must not be replayed")
				require.Equal(t, KiloGatewayBaseURL+"/responses", upstream.lastReq.URL.String())
				require.Equal(t, "openai/Exact.Model", gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "openai/Exact.Model", result.UpstreamModel)
				require.Equal(t, 13, result.Usage.InputTokens)
				require.Equal(t, 5, result.Usage.OutputTokens)
				require.Equal(t, 7, result.Usage.CacheReadInputTokens)
				require.Contains(t, recorder.Body.String(), `"model":"Exact.Model"`)
				require.NotContains(t, recorder.Body.String(), `"model":"openai/`)
				requireNoKiloMachineIDHeader(t, upstream.lastReq.Header)
				require.Equal(t, "Bearer synthetic-kilo-key", upstream.lastReq.Header.Get("Authorization"))
				require.Equal(t, "org-synthetic", upstream.lastReq.Header.Get("X-KiloCode-OrganizationId"))
				session := upstream.lastReq.Header.Get("X-Session-Id")
				require.NotEmpty(t, session)
				require.Equal(t, session, upstream.lastReq.Header.Get("X-KiloCode-TaskId"))
				require.Equal(t, session, upstream.lastReq.Header.Get("X-Session-Affinity"))
			})
		}
	}
}

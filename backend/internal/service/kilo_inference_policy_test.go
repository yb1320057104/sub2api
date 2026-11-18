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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kiloPolicyTestRepo struct {
	AccountRepository
	accounts map[int64]*Account
	pairErr  error
	readErr  error
	updates  map[int64]map[string]any
}

func (r *kiloPolicyTestRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.readErr != nil {
		return nil, r.readErr
	}
	if account := r.accounts[id]; account != nil {
		return account, nil
	}
	return nil, fmt.Errorf("synthetic account %d is missing", id)
}
func (r *kiloPolicyTestRepo) GetKiloInferenceAccount(ctx context.Context, id int64) (*Account, error) {
	return r.GetByID(ctx, id)
}
func (r *kiloPolicyTestRepo) GetKiloSourceForGateway(_ context.Context, gatewayID, preparedID int64) (*Account, error) {
	if r.pairErr != nil {
		return nil, r.pairErr
	}
	for _, account := range r.accounts {
		if account.IsOpenAIOAuth() && int64(ParseExtraInt(account.Extra["kilo_source_gateway_id"])) == gatewayID && int64(ParseExtraInt(account.Extra["kilo_source_prepared_id"])) == preparedID {
			return account, nil
		}
	}
	return nil, errors.New("synthetic pair is missing")
}
func (r *kiloPolicyTestRepo) UpdateExtra(_ context.Context, id int64, extra map[string]any) error {
	if r.updates == nil {
		r.updates = make(map[int64]map[string]any)
	}
	r.updates[id] = extra
	return nil
}

func kiloPolicyTestAccounts() (*Account, *Account, *kiloPolicyTestRepo) {
	source := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"access_token": "synthetic-source-oauth", "chatgpt_account_id": "synthetic-chatgpt-account", "email": "source@example.invalid"},
		Extra:       map[string]any{"kilo_quota_only": true, "kilo_source_gateway_id": int64(42), "kilo_source_prepared_id": int64(7)}}
	gateway := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "synthetic-kilo-key", "base_url": KiloGatewayBaseURL},
		Extra:       map[string]any{KiloManagedExtraKey: true, AccountExtraKiloBridgeEnabled: true, KiloGatewayProfileExtra: KiloGatewayCLIProfile, "kilo_prepared_id": int64(7)}}
	return source, gateway, &kiloPolicyTestRepo{accounts: map[int64]*Account{41: source, 42: gateway}}
}

func kiloPolicyRequest(t *testing.T, method, endpoint, bearer string) *http.Request {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, endpoint, strings.NewReader(`{"model":"gpt-test","input":"hello"}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+bearer)
	return request
}

func TestKiloInferencePolicyFailClosed(t *testing.T) {
	source, gateway, repo := kiloPolicyTestAccounts()
	request := kiloPolicyRequest(t, http.MethodPost, KiloGatewayBaseURL+"/responses", gateway.GetCredential("api_key"))
	require.NoError(t, ValidateKiloInferenceRequest(request, gateway))
	require.NoError(t, GuardKiloInferenceRequest(context.Background(), repo, request, gateway, gateway.ID))
	require.False(t, source.IsSchedulable())
	require.False(t, source.IsCredentialUsableForShadow())
	require.False(t, source.IsOpenAIResponsesWebSocketV2Enabled())
	for _, extra := range []map[string]any{{"kilo_quota_only": true}, {"kilo_source_gateway_id": 42}, {"kilo_source_prepared_id": 7}, {"kilo_source_gateway_id": "broken"}, {"kilo_quota_only": "true"}} {
		source.Extra = extra
		require.True(t, source.IsKiloQuotaOnly())
		require.Error(t, GuardKiloInferenceRequest(context.Background(), repo, kiloPolicyRequest(t, http.MethodPost, chatgptCodexURL, "synthetic-source-oauth"), source, source.ID))
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		require.NoError(t, GuardKiloInferenceRequest(context.Background(), repo, kiloPolicyRequest(t, method, chatGPTUsageURL, "synthetic-source-oauth"), source, source.ID), "official metadata reads remain available")
	}
	ws := kiloPolicyRequest(t, http.MethodGet, chatgptCodexURL, "synthetic-source-oauth")
	ws.Header.Set("Upgrade", "websocket")
	require.Error(t, GuardKiloInferenceRequest(context.Background(), repo, ws, source, source.ID), "a GET WebSocket handshake is inference, not a metadata read")
	for _, endpoint := range []string{chatgptCodexURL, "https://api.openai.com/v1/responses", "https://ai-gateway.kilo.ai.evil.invalid/api/v1/responses", "http://ai-gateway.kilo.ai/api/v1/responses", KiloGatewayBaseURL + "/chat/completions", KiloGatewayBaseURL + "/responses?redirect=official"} {
		require.Error(t, ValidateKiloInferenceRequest(kiloPolicyRequest(t, http.MethodPost, endpoint, gateway.GetCredential("api_key")), gateway))
	}
	badHost := request.Clone(context.Background())
	badHost.Host = "chatgpt.com"
	require.Error(t, ValidateKiloInferenceRequest(badHost, gateway))
	badBearer := kiloPolicyRequest(t, http.MethodPost, KiloGatewayBaseURL+"/responses", "synthetic-source-oauth")
	require.Error(t, ValidateKiloInferenceRequest(badBearer, gateway))
	badBearer = request.Clone(context.Background())
	badBearer.Header["authorization"] = []string{"Bearer synthetic-source-oauth"}
	require.Error(t, ValidateKiloInferenceRequest(badBearer, gateway), "case-variant duplicate auth cannot bypass")
	gateway.Credentials["access_token"] = gateway.GetCredential("api_key")
	require.Error(t, ValidateKiloInferenceAccount(gateway), "an OAuth bearer copied to api_key is not a Kilo key")
	delete(gateway.Credentials, "access_token")
	gateway.Type = AccountTypeOAuth
	require.Error(t, ValidateKiloInferenceAccount(gateway))
	gateway.Type = AccountTypeAPIKey
	gateway.Credentials["base_url"] = "https://api.openai.com/v1"
	require.Error(t, ValidateKiloInferenceAccount(gateway))
	gateway.Credentials["base_url"] = KiloGatewayBaseURL
	gateway.Schedulable = false
	require.Error(t, GuardKiloInferenceRequest(context.Background(), repo, request, nil, gateway.ID))
}

func TestKiloInferencePolicyCatchesLateBindingAndShadow(t *testing.T) {
	source, gateway, repo := kiloPolicyTestAccounts()
	stale := *source
	stale.Extra = nil
	request := kiloPolicyRequest(t, http.MethodPost, chatgptCodexURL, "synthetic-source-oauth")
	require.Error(t, GuardKiloInferenceRequest(context.Background(), repo, request, &stale, source.ID), "a snapshot selected before onboarding cannot send OAuth")
	parentID := source.ID
	shadow := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID, Status: StatusActive}
	repo.accounts[shadow.ID] = shadow
	require.Error(t, GuardKiloInferenceRequest(context.Background(), repo, request, shadow, shadow.ID))
	repo.readErr = errors.New("synthetic read failure")
	require.Error(t, GuardKiloInferenceRequest(context.Background(), repo, kiloPolicyRequest(t, http.MethodPost, KiloGatewayBaseURL+"/responses", gateway.GetCredential("api_key")), gateway, gateway.ID))
}

type kiloPolicyUpstream struct {
	requests []*http.Request
	bodies   [][]byte
	reply    string
}

func (u *kiloPolicyUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	u.requests = append(u.requests, request.Clone(request.Context()))
	u.bodies = append(u.bodies, body)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(u.reply)), Request: request}, nil
}
func (u *kiloPolicyUpstream) DoWithTLS(request *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(request, proxy, id, concurrency)
}

func kiloPolicyReply(text string) string {
	payload, _ := json.Marshal(map[string]any{"id": "resp_synthetic", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text}}}}})
	return "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"in_progress\"}}\n\n" + "data: {\"type\":\"response.output_text.delta\",\"delta\":" + jsonString(text) + "}\n\n" + "data: {\"type\":\"response.completed\",\"response\":" + string(payload) + "}\n\n"
}

func TestKiloInferencePolicySourceAdminAndCandyUseGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, candy := range []bool{false, true} {
		t.Run(fmt.Sprint(candy), func(t *testing.T) {
			source, gateway, repo := kiloPolicyTestAccounts()
			upstream := &kiloPolicyUpstream{reply: kiloPolicyReply("<final_answer>1</final_answer>")}
			service := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}}
			if candy {
				text, err := service.runAccountCandyProbe(context.Background(), source, source, &AccountCandyTestConfig{Model: "gpt-test", Effort: "low"})
				require.NoError(t, err)
				require.Contains(t, text, "<final_answer>1</final_answer>")
			} else {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
				require.NoError(t, service.testAccountConnectionForAccount(c, source, "gpt-test", "hello", AccountTestModeDefault, AccountTestOptions{}))
			}
			require.Len(t, upstream.requests, 1)
			require.Equal(t, KiloGatewayBaseURL+"/responses", upstream.requests[0].URL.String())
			require.Equal(t, "Bearer "+gateway.GetCredential("api_key"), upstream.requests[0].Header.Get("Authorization"))
			require.NotContains(t, fmt.Sprint(upstream.requests[0].Header), "synthetic-source-oauth")
			require.True(t, HTTPUpstreamRedirectsDisabled(upstream.requests[0].Context()))
			gateway.Schedulable = false
			_, err := resolveKiloTestAccount(context.Background(), repo, source)
			require.Error(t, err)
			require.Len(t, upstream.requests, 1, "unavailable Kilo never falls back to official")
		})
	}
	pending := &Account{ID: 51, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"kilo_quota_only": true, "kilo_onboarding_job_id": 9}}
	_, err := resolveKiloTestAccount(context.Background(), nil, pending)
	require.Error(t, err, "pending onboarding has no gateway and cannot generate")
}

func TestKiloInferencePolicyBuildersAndSenderRejectOfficial(t *testing.T) {
	_, gateway, repo := kiloPolicyTestAccounts()
	upstream := &kiloPolicyUpstream{reply: kiloPolicyReply("hello")}
	service := &OpenAIGatewayService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}}
	body := []byte(`{"model":"gpt-test","input":"hello","stream":true}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	for _, passthrough := range []bool{false, true} {
		var request *http.Request
		var err error
		if passthrough {
			request, err = service.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, gateway, body, gateway.GetCredential("api_key"))
		} else {
			request, err = service.buildUpstreamRequest(context.Background(), c, gateway, body, gateway.GetCredential("api_key"), true, "", false)
		}
		require.NoError(t, err)
		require.Equal(t, KiloGatewayBaseURL+"/responses", request.URL.String())
		request.URL.Scheme = "https"
		request.URL.Host = "api.openai.com"
		request.URL.Path = "/v1/responses"
		_, err = service.doOpenAIUpstream(request, "", gateway)
		require.Error(t, err)
	}
	require.Empty(t, upstream.requests)
}

func TestKiloInferencePolicySubscriptionScopeCannotExpand(t *testing.T) {
	_, gateway, repo := kiloPolicyTestAccounts()
	gateway.Extra["kilo_onboarding_source_id"] = int64(41)
	gateway.Extra["kilo_subscription_models"] = []string{"openai/gpt-test"}
	gateway.Extra["kilo_subscription_scope_source"] = "administrator_verified_exact_models"
	gateway.Extra["kilo_subscription_scope_verified_at"] = "2026-11-14T12:00:00Z"
	gateway.Extra["openai_passthrough"] = true
	gateway.Credentials["model_mapping"] = map[string]any{"openai/gpt-test": "openai/gpt-test"}
	require.NoError(t, ValidateKiloInferenceAccount(gateway))
	require.True(t, gateway.IsModelSupported("openai/gpt-test"))
	require.True(t, gateway.IsModelSupported("gpt-test"), "the existing client prefix adapter stays in the same exact grant")
	require.False(t, gateway.IsModelSupported("openai/paid-model"))
	gateway.Credentials["model_mapping"] = map[string]any{"*": "openai/paid-model"}
	require.False(t, gateway.IsModelSupported("openai/gpt-test"), "passthrough cannot bypass an empty or wildcard mapping")
	gateway.Credentials["model_mapping"] = map[string]any{"openai/gpt-test": "openai/gpt-test", "openai/paid-model": "openai/paid-model"}
	require.False(t, gateway.IsModelSupported("openai/paid-model"), "mapping edits cannot exceed the verified scope")
	request := kiloPolicyRequest(t, http.MethodPost, KiloGatewayBaseURL+"/responses", gateway.GetCredential("api_key"))
	require.NoError(t, ValidateKiloInferenceRequest(request, gateway))
	request = kiloPolicyRequest(t, http.MethodPost, KiloGatewayBaseURL+"/responses", gateway.GetCredential("api_key"))
	request.Body = io.NopCloser(strings.NewReader(`{"model":"openai/paid-model","input":"hi"}`))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"model":"openai/paid-model","input":"hi"}`)), nil
	}
	require.Error(t, ValidateKiloInferenceRequest(request, gateway), "the final wire model remains guarded after mapping")
	catalogBody := []byte(`{"object":"list","data":[{"id":"openai/gpt-test"},{"id":"openai/paid-model"},{"id":"anthropic/claude-paid"}]}`)
	projected, err := projectAccountModelsBody(catalogBody, gateway, nil, false)
	require.NoError(t, err)
	require.Contains(t, string(projected), "openai/gpt-test")
	require.NotContains(t, string(projected), "paid-model")
	require.NotContains(t, string(projected), "anthropic/")
	service := &AccountTestService{accountRepo: repo}
	for _, automatic := range []bool{false, true} {
		catalog, err := service.SyncUpstreamModelCatalogWithOptions(context.Background(), gateway, UpstreamModelSyncOptions{Apply: true, Automatic: automatic})
		require.NoError(t, err)
		require.Equal(t, "kilo_subscription_scope", catalog.Skipped)
	}
	gateway.Extra["kilo_subscription_models"] = []string{}
	require.Error(t, ValidateKiloInferenceAccount(gateway))
	require.False(t, gateway.IsModelSupported("openai/gpt-test"))
	delete(gateway.Extra, "kilo_subscription_models")
	delete(gateway.Extra, "kilo_subscription_scope_source")
	delete(gateway.Extra, "kilo_subscription_scope_verified_at")
	require.Error(t, ValidateKiloInferenceAccount(gateway), "new onboarding cannot delete its proof to become legacy")
	delete(gateway.Extra, "kilo_onboarding_source_id")
	require.NoError(t, ValidateKiloInferenceAccount(gateway), "older manual managed gateways without scope retain compatibility")
}

func TestKiloInferencePolicyQuotaReadsUseOfficialSourceOnly(t *testing.T) {
	source, gateway, repo := kiloPolicyTestAccounts()
	var methods, paths, bearers []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		bearers = append(bearers, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "rate-limit-reset-credits") {
			_, _ = io.WriteString(w, `{"available_count":0,"credits":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"account_id":"synthetic-chatgpt-account","email":"source@example.invalid","plan_type":"pro","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":34,"limit_window_seconds":604800,"reset_after_seconds":7200}}}`)
	}))
	defer srv.Close()
	provider := NewOpenAITokenProvider(repo, &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(source): "synthetic-source-oauth"}}, nil)
	quota := NewOpenAIQuotaService(repo, nil, provider, newQuotaRedirectingFactory(srv), nil)
	usageService := &AccountUsageService{accountRepo: repo, openAIQuotaService: quota, cache: NewUsageCache()}
	for _, account := range []*Account{source, gateway} {
		usage, err := usageService.GetUsageForAccount(context.Background(), account, true)
		require.NoError(t, err)
		require.NotNil(t, usage.FiveHour)
		require.NotNil(t, usage.SevenDay)
		require.Equal(t, float64(12), usage.FiveHour.Utilization)
		require.Equal(t, float64(34), usage.SevenDay.Utilization)
	}
	_, err := quota.QueryUsage(context.Background(), gateway.ID)
	require.NoError(t, err)
	require.NotEmpty(t, methods)
	for i, method := range methods {
		require.Equal(t, http.MethodGet, method)
		require.Contains(t, paths[i], "/backend-api/wham/")
		require.Equal(t, "Bearer synthetic-source-oauth", bearers[i])
	}
	require.NotContains(t, repo.updates, gateway.ID, "official scheduling snapshots belong to source, not the Kilo gateway")
	_, err = usageService.probeOpenAICodexSnapshot(context.Background(), source)
	require.Error(t, err, "a direct call cannot revive the model POST usage probe")
}

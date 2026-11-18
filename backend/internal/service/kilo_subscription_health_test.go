package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type kiloBoundModelsReader struct {
	body  string
	err   error
	calls int
}

func (r *kiloBoundModelsReader) FetchOpenAIModelsListFresh(context.Context, *Account) (*OpenAIModelsResponse, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return &OpenAIModelsResponse{Body: []byte(r.body)}, nil
}

type kiloBoundRemoteModels struct {
	KiloRemote
	models []string
	calls  int
}

func (r *kiloBoundRemoteModels) Models(context.Context, string, string, string) ([]string, error) {
	r.calls++
	return r.models, nil
}

func TestKiloBoundGPTModelsRejectGenericBYOKAndRevokedSource(t *testing.T) {
	source, _, _ := kiloPolicyTestAccounts()
	official := &kiloBoundModelsReader{body: `{"data":[{"id":"gpt-6.1-sol"},{"id":"gpt-6.2-sol"},{"id":"gpt-6.2-astra"},{"id":"gpt-4o"}]}`}
	remote := &kiloBoundRemoteModels{models: []string{"openai/gpt-6.1-sol", "openai/gpt-6.2-sol", "openai/gpt-6.2-astra", "openai/gpt-6-luna", "openai/gpt-4o", "kilo-auto/free"}}
	svc := &KiloService{modelSource: official, remote: remote}
	models, err := svc.discoverKiloModels(context.Background(), source, "synthetic-key", "", "", nil)
	require.NoError(t, err)
	require.Equal(t, []string{"openai/gpt-6.1-sol", "openai/gpt-6.2-astra", "openai/gpt-6.2-sol"}, models)
	_, err = svc.discoverKiloModels(context.Background(), source, "synthetic-key", "", "", []string{"openai/gpt-6-luna"})
	require.Error(t, err, "channel support and generic BYOK cannot grant a model missing from the bound GPT catalog")
	official.err = errors.New("synthetic 401")
	before := remote.calls
	_, err = svc.discoverKiloModels(context.Background(), source, "synthetic-key", "", "", nil)
	require.Error(t, err)
	require.Equal(t, before, remote.calls, "an invalid GPT must not fall back to the Kilo catalog")
}

func TestKiloBoundGPTBlocksSendWhenSourceAuthorizationFails(t *testing.T) {
	source, gateway, repo := kiloPolicyTestAccounts()
	gateway.Extra["kilo_onboarding_source_id"] = source.ID
	gateway.Credentials["model_mapping"] = map[string]string{"openai/gpt-test": "openai/gpt-test"}
	for name, value := range kiloCatalogScopeExtra(gateway, []string{"openai/gpt-test"}, time.Now(), source) {
		gateway.Extra[name] = value
	}
	source.Schedulable = false
	request := kiloPolicyRequest(t, http.MethodPost, KiloGatewayBaseURL+"/responses", gateway.GetCredential("api_key"))
	require.NoError(t, GuardKiloInferenceRequest(context.Background(), repo, request, gateway, gateway.ID), "quota-only scheduling is not authentication health")
	source.Extra[OpenAIAuthRecoveryKey] = map[string]any{"access_token_digest": OpenAIAccessTokenDigest(source.GetOpenAIAccessToken()), "requires_relogin": true}
	require.Error(t, GuardKiloInferenceRequest(context.Background(), repo, request, gateway, gateway.ID))
	_, err := resolveKiloTestAccount(context.Background(), repo, gateway)
	require.Error(t, err)
	delete(source.Extra, OpenAIAuthRecoveryKey)
	source.Status = StatusError
	require.Error(t, GuardKiloInferenceRequest(context.Background(), repo, request, gateway, gateway.ID))
}

func TestKiloQuotaAuthObserverReportsActualTokenOnly(t *testing.T) {
	source, _, repo := kiloPolicyTestAccounts()
	called := 0
	svc := &OpenAIQuotaService{accountRepo: repo, upstreamErrorHandler: func(ctx context.Context, a *Account, status int, _ http.Header, _ []byte) bool {
		called++
		used, ok := ctx.Value(openAIUsedAccessTokenKey{}).(openAIUsedAccessToken)
		require.True(t, ok)
		require.Equal(t, source.ID, used.accountID)
		require.Equal(t, "synthetic-source-oauth", used.token)
		return true
	}}
	for _, tc := range []struct {
		status int
		body   string
		want   int
	}{
		{401, `{"error":{"code":"token_revoked"}}`, 1},
		{402, `{"detail":{"code":"deactivated_workspace"}}`, 2},
		{402, `{"error":{"title":"Paid Model - Credits","balance":0}}`, 2},
		{502, `{}`, 2},
	} {
		svc.observeUpstreamAuthFailure(context.Background(), source.ID, "synthetic-source-oauth", "synthetic-chatgpt-account", tc.status, nil, []byte(tc.body))
		require.Equal(t, tc.want, called)
	}
	source.Credentials["access_token"] = "new-token"
	svc.observeUpstreamAuthFailure(context.Background(), source.ID, "synthetic-source-oauth", "synthetic-chatgpt-account", 401, nil, []byte(`{"error":{"code":"token_revoked"}}`))
	require.Equal(t, 2, called, "a delayed old-token error must not report a new credential generation")
}

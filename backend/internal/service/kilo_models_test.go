package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKiloAuthenticatedCatalogRequiresLiteralUserBYOK(t *testing.T) {
	body := []byte(`{"data":[{"id":"openai/gpt-one","hasUserByokAvailable":true},{"id":"openai/gpt-two","hasUserByokAvailable":true},{"id":"openai/paid","hasUserByokAvailable":false},{"id":"openai/missing"},{"id":"~openai/alias","hasUserByokAvailable":true},{"id":"anthropic/other","hasUserByokAvailable":true}]}`)
	models, err := parseKiloAuthenticatedCatalog(body)
	require.NoError(t, err)
	require.Equal(t, []string{"openai/gpt-one", "openai/gpt-two"}, models)
	for _, rejected := range []string{`{"data":[{"id":"openai/public"}]}`, `{"data":[{"id":"openai/string","hasUserByokAvailable":"true"}]}`, `{"data":[{"id":"openai/*","hasUserByokAvailable":true}]}`, `{"data":[{"id":"openai/gpt-one","hasUserByokAvailable":true},{"id":"openai/gpt-one","hasUserByokAvailable":true}]}`} {
		_, err := parseKiloAuthenticatedCatalog([]byte(rejected))
		require.Error(t, err)
	}
	all, err := selectKiloCatalogModels(nil, models)
	require.NoError(t, err)
	require.Equal(t, models, all)
	subset, err := selectKiloCatalogModels([]string{"openai/gpt-two"}, models)
	require.NoError(t, err)
	require.Equal(t, []string{"openai/gpt-two"}, subset)
	_, err = selectKiloCatalogModels([]string{"openai/paid"}, models)
	require.Error(t, err)
}

func TestKiloRemoteAuthenticatedCatalogUsesExactKeyAndOrganization(t *testing.T) {
	for _, organization := range []string{"", "aa52a13e-1af9-4b3c-977b-66eae64d4b8d"} {
		t.Run(organization, func(t *testing.T) {
			calls := 0
			remote := kiloRemoteTestClient(t, func(req *http.Request) (*http.Response, error) {
				calls++
				path := "/api/v1/models"
				if organization != "" {
					path = "/api/v1/organizations/" + organization + "/models"
				}
				require.Equal(t, "ai-gateway.kilo.ai", req.URL.Host)
				require.Equal(t, path, req.URL.Path)
				require.Equal(t, http.MethodGet, req.Method)
				require.Equal(t, "Bearer fake-kilo-key", req.Header.Get("Authorization"))
				require.Equal(t, organization, req.Header.Get("X-KILOCODE-ORGANIZATIONID"))
				return kiloRemoteTestResponse(200, `{"data":[{"id":"openai/gpt-one","hasUserByokAvailable":true}]}`), nil
			})
			models, err := remote.Models(context.Background(), "fake-kilo-key", organization, "socks5://selected-proxy.invalid:1080")
			require.NoError(t, err)
			require.Equal(t, []string{"openai/gpt-one"}, models)
			require.Equal(t, 1, calls)
		})
	}
	calls := 0
	remote := kiloRemoteTestClient(t, func(req *http.Request) (*http.Response, error) {
		calls++
		return kiloRemoteTestResponse(http.StatusUnauthorized, `{}`), nil
	})
	_, err := remote.Models(context.Background(), "fake-kilo-key", "", "socks5://selected-proxy.invalid:1080")
	require.Error(t, err)
	require.Equal(t, 1, calls, "anonymous fallback must never become authenticated BYOK evidence")
}

func TestKiloAuthenticatedScopeBindsKeyAndSpace(t *testing.T) {
	account := &Account{ID: 124, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "gateway-test-key", "base_url": KiloGatewayBaseURL, "kilo_organization_id": "", "model_mapping": map[string]string{"openai/gpt-one": "openai/gpt-one", "openai/gpt-two": "openai/gpt-two"}},
		Extra:       map[string]any{KiloManagedExtraKey: true, AccountExtraKiloBridgeEnabled: true, KiloGatewayProfileExtra: KiloGatewayCLIProfile, "kilo_prepared_id": int64(2)}}
	for name, value := range kiloCatalogScopeExtra(account, []string{"openai/gpt-one", "openai/gpt-two"}, time.Now()) {
		account.Extra[name] = value
	}
	require.NoError(t, ValidateKiloInferenceAccount(account))
	require.True(t, account.kiloSubscriptionAllowsRequestModel("openai/gpt-two"))
	require.False(t, account.kiloSubscriptionAllowsRequestModel("openai/paid"))
	account.Credentials["api_key"] = "rotated-gateway-key"
	require.Error(t, ValidateKiloInferenceAccount(account))
	account.Credentials["api_key"] = "gateway-test-key"
	account.Credentials["kilo_organization_id"] = "different-space"
	require.Error(t, ValidateKiloInferenceAccount(account))
	account.Credentials["kilo_organization_id"] = ""
	request, err := http.NewRequest(http.MethodPost, KiloGatewayBaseURL+"/responses", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer source-oauth-token")
	require.Error(t, ValidateKiloInferenceRequest(request, account))
}

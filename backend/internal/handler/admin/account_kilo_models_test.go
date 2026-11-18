package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKiloAccountModelPickerNeverFallsBackFromInvalidSubscription(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"obsolete_byok_scope", "disabled_gateway", "discovery_unavailable", "quota_source"} {
		t.Run(mode, func(t *testing.T) {
			account := &service.Account{ID: 124, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
				Credentials: map[string]any{"api_key": "synthetic-gateway-key", "base_url": service.KiloGatewayBaseURL, "model_mapping": map[string]string{"openai/gpt-4o": "openai/gpt-4o"}},
				Extra:       map[string]any{service.KiloManagedExtraKey: true, service.AccountExtraKiloBridgeEnabled: true, service.KiloGatewayProfileExtra: service.KiloGatewayCLIProfile, "kilo_prepared_id": 2, "kilo_onboarding_source_id": 122, "kilo_subscription_models": []string{"openai/gpt-4o"}, "kilo_subscription_scope_source": "administrator_verified_exact_models", "kilo_subscription_scope_verified_at": time.Now().UTC().Format(time.RFC3339)}}
			expected := http.StatusConflict
			switch mode {
			case "obsolete_byok_scope":
				account.Extra["kilo_subscription_scope_source"] = "kilo_authenticated_byok_catalog"
			case "disabled_gateway":
				account.Status = service.StatusError
				account.Schedulable = false
			case "discovery_unavailable":
				expected = http.StatusServiceUnavailable
			case "quota_source":
				account.Type = service.AccountTypeOAuth
				account.Extra = map[string]any{"kilo_quota_only": true}
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/accounts/124/models", nil)
			(&AccountHandler{}).respondAvailableModels(c, account)
			require.Equal(t, expected, rec.Code, rec.Body.String())
			require.NotContains(t, rec.Body.String(), "gpt-4o", "a persisted mapping cannot become an entitlement fallback")
			require.NotContains(t, rec.Body.String(), `"data":[`)
		})
	}
}

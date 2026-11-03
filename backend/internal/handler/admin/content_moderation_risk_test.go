package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type moderationRiskHandlerSettings struct {
	service.SettingRepository
	value string
}

func (settings *moderationRiskHandlerSettings) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyContentModerationConfig {
		return settings.value, nil
	}
	return "", service.ErrSettingNotFound
}

func (settings *moderationRiskHandlerSettings) Set(_ context.Context, _ string, value string) error {
	settings.value = value
	return nil
}

func TestContentModerationRiskHandlerOmittedNullAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &moderationRiskHandlerSettings{value: `{}`}
	svc := service.NewContentModerationService(settings, nil, nil, nil, nil, nil, nil, nil)
	h := NewContentModerationHandler(svc)
	router := gin.New()
	router.PUT("/config", h.UpdateConfig)
	put := func(body string, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, want, response.Code, response.Body.String())
	}
	put(`{"risk_policy":{"low_threshold":20,"medium_threshold":40,"high_threshold":60,"low_action":"allow","medium_action":"charge_allow","high_action":"block"},"keyword_scores":{" Flow ":0}}`, 200)
	var cfg service.ContentModerationConfig
	require.NoError(t, json.Unmarshal([]byte(settings.value), &cfg))
	require.NotNil(t, cfg.RiskPolicy)
	require.Zero(t, cfg.KeywordScores["flow"])
	put(`{"record_non_hits":true}`, 200)
	require.NoError(t, json.Unmarshal([]byte(settings.value), &cfg))
	require.NotNil(t, cfg.RiskPolicy, "omitted risk_policy must not disable it")
	put(`{"risk_policy":{}}`, 400)
	put(`{"keyword_scores":{"flow":101}}`, 400)
	put(`{"keyword_scores":{"flow":0.5}}`, 400)
	put(`{"risk_policy":null}`, 200)
	require.NoError(t, json.Unmarshal([]byte(settings.value), &cfg))
	require.Nil(t, cfg.RiskPolicy)
}

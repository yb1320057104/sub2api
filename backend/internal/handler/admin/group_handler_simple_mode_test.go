package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newSimpleModeGroupRouter(svc *stubAdminService) *gin.Engine {
	h := NewGroupHandlerWithConfig(svc, nil, nil, &config.Config{RunMode: config.RunModeSimple})
	r := gin.New()
	r.GET("/groups", h.List)
	r.GET("/groups/all", h.GetAll)
	r.GET("/groups/live-capability", h.GetLiveCapability)
	r.GET("/groups/usage-summary", h.GetUsageSummary)
	r.GET("/groups/capacity-summary", h.GetCapacitySummary)
	r.PUT("/groups/sort-order", h.UpdateSortOrder)
	r.GET("/groups/:id", h.GetByID)
	r.GET("/groups/:id/stats", h.GetStats)
	r.GET("/groups/:id/api-keys", h.GetGroupAPIKeys)
	r.GET("/groups/:id/model-allowlist-candidates", h.GetGroupModelAllowlistCandidates)
	r.POST("/groups", h.Create)
	r.PUT("/groups/:id", h.Update)
	r.POST("/groups/:id/duplicate", h.Duplicate)
	r.GET("/groups/:id/composite-routes", h.ListCompositeRoutes)
	r.POST("/groups/:id/composite-routes", h.CreateCompositeRoute)
	r.POST("/groups/:id/composite-routes/preview", h.PreviewCompositeRoute)
	r.PUT("/groups/:id/composite-routes/:route_id", h.UpdateCompositeRoute)
	r.DELETE("/groups/:id/composite-routes/:route_id", h.DeleteCompositeRoute)
	r.GET("/groups/:id/rate-multipliers", h.GetGroupRateMultipliers)
	r.PUT("/groups/:id/rate-multipliers", h.BatchSetGroupRateMultipliers)
	r.DELETE("/groups/:id/rate-multipliers", h.ClearGroupRateMultipliers)
	r.PUT("/groups/:id/rpm-overrides", h.BatchSetGroupRPMOverrides)
	r.DELETE("/groups/:id/rpm-overrides", h.ClearGroupRPMOverrides)
	r.DELETE("/groups/:id", h.Delete)
	return r
}

func TestGroupHandlerSimpleModeSanitizesCommercialFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	r := newSimpleModeGroupRouter(svc)

	create := `{"name":"simple","description":"basic grouping","platform":"anthropic","rate_multiplier":7,"is_exclusive":true,"subscription_type":"subscription","daily_limit_usd":10,"long_context_pricing_enabled":true,"model_pricing":[{"model":"claude","input_price":1}],"allow_image_generation":true,"allow_batch_image_generation":true,"video_price_720p":2,"web_search_price_per_call":3,"audio_realtime_price_per_min":4,"rpm_limit":99}`
	req := httptest.NewRequest(http.MethodPost, "/groups", bytes.NewBufferString(create))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code)
	require.Len(t, svc.createdGroups, 1)
	created := svc.createdGroups[0]
	require.Equal(t, "basic grouping", created.Description)
	require.Equal(t, 1.0, created.RateMultiplier)
	require.False(t, created.IsExclusive)
	require.Equal(t, service.SubscriptionTypeStandard, created.SubscriptionType)
	require.Nil(t, created.DailyLimitUSD)
	require.False(t, created.AllowImageGeneration)
	require.False(t, created.LongContextPricingEnabled)
	require.Empty(t, created.ModelPricing)
	require.False(t, created.AllowBatchImageGeneration)
	require.Nil(t, created.VideoPrice720P)
	require.Nil(t, created.WebSearchPricePerCall)
	require.Nil(t, created.AudioRealtimePricePerMin)
	require.Zero(t, created.RPMLimit)

	update := `{"name":"renamed","description":"still basic","rate_multiplier":9,"is_exclusive":true,"subscription_type":"subscription","daily_limit_usd":12,"long_context_pricing_enabled":true,"model_pricing":[{"model":"claude","input_price":1}],"allow_image_generation":true,"allow_batch_image_generation":true,"video_price_720p":2,"web_search_price_per_call":3,"audio_realtime_price_per_min":4,"status":"inactive","rpm_limit":123}`
	req = httptest.NewRequest(http.MethodPut, "/groups/2", bytes.NewBufferString(update))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code)
	require.Len(t, svc.updatedGroups, 1)
	updated := svc.updatedGroups[0]
	require.NotNil(t, updated.Description)
	require.Equal(t, "still basic", *updated.Description)
	require.Nil(t, updated.RateMultiplier)
	require.Nil(t, updated.IsExclusive)
	require.Empty(t, updated.SubscriptionType)
	require.Nil(t, updated.DailyLimitUSD)
	require.Nil(t, updated.AllowImageGeneration)
	require.Nil(t, updated.LongContextPricingEnabled)
	require.Nil(t, updated.ModelPricing)
	require.Nil(t, updated.AllowBatchImageGeneration)
	require.Nil(t, updated.VideoPrice720P)
	require.Nil(t, updated.WebSearchPricePerCall)
	require.Nil(t, updated.AudioRealtimePricePerMin)
	require.Empty(t, updated.Status)
	require.Nil(t, updated.RPMLimit)
}

func TestGroupHandlerSimpleModeRejectsCompositeBeforeService(t *testing.T) {
	svc := newStubAdminService()
	r := newSimpleModeGroupRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/groups", bytes.NewBufferString(`{"name":"composite","platform":"composite"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusBadRequest, res.Code)
	require.Empty(t, svc.createdGroups)
}

func TestGroupHandlerSimpleModeDeleteRejectsNonEmptyGroupBeforeCascade(t *testing.T) {
	svc := newStubAdminService()
	svc.deleteGroupIfEmptyErr = service.ErrGroupNotEmpty
	r := newSimpleModeGroupRouter(svc)
	res := httptest.NewRecorder()

	r.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "/groups/7", nil))

	require.Equal(t, http.StatusConflict, res.Code)
	require.Equal(t, []int64{7}, svc.guardedDeletedGroupIDs)
	require.Empty(t, svc.deletedGroupIDs)
}

func TestGroupHandlerSimpleModeDeletePermitsEmptyGroup(t *testing.T) {
	svc := newStubAdminService()
	r := newSimpleModeGroupRouter(svc)
	res := httptest.NewRecorder()

	r.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "/groups/8", nil))

	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, []int64{8}, svc.guardedDeletedGroupIDs)
	require.Empty(t, svc.deletedGroupIDs)
}

func TestGroupHandlerSimpleModeIgnoresExclusiveFilter(t *testing.T) {
	svc := newStubAdminService()
	r := newSimpleModeGroupRouter(svc)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/groups?is_exclusive=true", nil))
	require.Equal(t, http.StatusOK, res.Code)
	require.Nil(t, svc.lastListGroupsIsExclusive)
}

func float64PtrForSimpleModeTest(value float64) *float64 { return &value }

func TestGroupHandlerSimpleModeResponseUsesFieldAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	svc.groups = []service.Group{{
		ID: 1, Name: "basic", Description: "allowed", Platform: service.PlatformAnthropic,
		AccountCount: 3, ActiveAccountCount: 2, RateLimitedAccountCount: 1,
		Status: service.StatusActive, RateMultiplier: 9, RPMLimit: 42,
		LongContextPricingEnabled: true,
		ModelPricing:              []service.ChannelModelPricing{{Models: []string{"claude"}}},
		AllowBatchImageGeneration: true, VideoPrice720P: float64PtrForSimpleModeTest(2),
		WebSearchPricePerCall: float64PtrForSimpleModeTest(3), AudioRealtimePricePerMin: float64PtrForSimpleModeTest(4),
		ModelRouting: map[string][]int64{"claude": {2}},
	}}
	r := newSimpleModeGroupRouter(svc)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/groups", nil))
	require.Equal(t, http.StatusOK, res.Code)

	var payload struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 1)
	item := payload.Data.Items[0]
	require.Equal(t, "basic", item["name"])
	require.Equal(t, "allowed", item["description"])
	require.Equal(t, "anthropic", item["platform"])
	require.Equal(t, float64(3), item["account_count"])
	require.ElementsMatch(t, []string{
		"id", "name", "description", "platform", "status", "account_count",
		"active_account_count", "rate_limited_account_count", "sort_order", "created_at", "updated_at",
	}, mapKeys(item))
	for _, forbidden := range []string{
		"rate_multiplier", "rpm_limit", "long_context_pricing_enabled", "model_pricing",
		"allow_batch_image_generation", "video_price_720p", "web_search_price_per_call",
		"audio_realtime_price_per_min", "model_routing",
	} {
		_, exposed := item[forbidden]
		require.Falsef(t, exposed, "simple mode exposed %s", forbidden)
	}
}

func TestGroupHandlerSimpleModeListsOnlyBindableGroups(t *testing.T) {
	svc := newStubAdminService()
	svc.groups = []service.Group{{ID: 1, Name: "basic", Platform: service.PlatformAnthropic}, {ID: 2, Name: "composite", Platform: service.PlatformComposite}}
	r := newSimpleModeGroupRouter(svc)
	for _, path := range []string{"/groups", "/groups/all"} {
		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, res.Code)
		require.Contains(t, res.Body.String(), `"name":"basic"`)
		require.NotContains(t, res.Body.String(), `"name":"composite"`)
	}
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestGroupHandlerSimpleModeAllReadAndWriteResponsesUseFieldAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	svc.groups = []service.Group{{
		ID: 1, Name: "basic", Description: "allowed", Platform: service.PlatformAnthropic, AccountCount: 3,
		Status: service.StatusActive, RateMultiplier: 9, RPMLimit: 42,
		LongContextPricingEnabled: true,
		ModelPricing:              []service.ChannelModelPricing{{Models: []string{"claude"}}},
	}}
	r := newSimpleModeGroupRouter(svc)

	assertNoAdvancedFields := func(t *testing.T, item map[string]any) {
		t.Helper()
		for _, forbidden := range []string{
			"rate_multiplier", "rpm_limit", "long_context_pricing_enabled", "model_pricing",
			"allow_batch_image_generation", "video_price_720p", "web_search_price_per_call",
			"audio_realtime_price_per_min", "model_routing",
		} {
			_, exposed := item[forbidden]
			require.Falsef(t, exposed, "simple mode exposed %s", forbidden)
		}
	}

	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   string
		list   bool
	}{
		{name: "get all", method: http.MethodGet, path: "/groups/all", list: true},
		{name: "get by id", method: http.MethodGet, path: "/groups/1"},
		{name: "create", method: http.MethodPost, path: "/groups", body: `{"name":"created","platform":"anthropic","model_pricing":[{"models":["claude"]}]}`},
		{name: "update", method: http.MethodPut, path: "/groups/1", body: `{"name":"updated","model_pricing":[{"models":["claude"]}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(res, req)
			require.Equal(t, http.StatusOK, res.Code)

			var payload struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &payload))
			if tt.list {
				var items []map[string]any
				require.NoError(t, json.Unmarshal(payload.Data, &items))
				require.Len(t, items, 1)
				assertNoAdvancedFields(t, items[0])
				return
			}
			var item map[string]any
			require.NoError(t, json.Unmarshal(payload.Data, &item))
			assertNoAdvancedFields(t, item)
		})
	}
}

func TestGroupHandlerSimpleModeBlocksAdvancedOperations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/groups/1/duplicate", ""},
		{http.MethodGet, "/groups/1/model-allowlist-candidates", ""},
		{http.MethodGet, "/groups/1/composite-routes", ""},
		{http.MethodPost, "/groups/1/composite-routes", `{"public_model":"x","target_platform":"openai"}`},
		{http.MethodPost, "/groups/1/composite-routes/preview", `{"model":"x"}`},
		{http.MethodPut, "/groups/1/composite-routes/2", `{"public_model":"x","target_platform":"openai"}`},
		{http.MethodDelete, "/groups/1/composite-routes/2", ""},
		{http.MethodGet, "/groups/1/rate-multipliers", ""},
		{http.MethodPut, "/groups/1/rate-multipliers", `{"entries":[]}`},
		{http.MethodDelete, "/groups/1/rate-multipliers", ""},
		{http.MethodPut, "/groups/1/rpm-overrides", `{"entries":[]}`},
		{http.MethodDelete, "/groups/1/rpm-overrides", ""},
		{http.MethodGet, "/groups/1/stats", ""},
		{http.MethodGet, "/groups/1/api-keys", ""},
		{http.MethodGet, "/groups/live-capability", ""},
		{http.MethodGet, "/groups/usage-summary", ""},
		{http.MethodGet, "/groups/capacity-summary", ""},
		{http.MethodPut, "/groups/sort-order", `{"updates":[{"id":1,"sort_order":1}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			svc := newStubAdminService()
			r := newSimpleModeGroupRouter(svc)
			res := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(res, req)
			require.Equal(t, http.StatusForbidden, res.Code)
			require.Zero(t, svc.advancedGroupOperationCalls)
		})
	}
}

type groupProtectionSettings struct {
	service.SettingRepository
	mu     sync.Mutex
	values map[string]string
}

func (s *groupProtectionSettings) GetValue(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

func (s *groupProtectionSettings) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *groupProtectionSettings) CompareAndSwap(_ context.Context, key string, expected *string, value string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.values[key]
	if expected == nil && exists || expected != nil && (!exists || current != *expected) {
		return false, nil
	}
	s.values[key] = value
	return true, nil
}

type groupProtectionGroups struct{ service.GroupRepository }

func (groupProtectionGroups) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	if id != 7 {
		return nil, service.ErrGroupNotFound
	}
	return &service.Group{ID: id, Platform: service.PlatformOpenAI}, nil
}

type groupProtectionUsers struct {
	service.UserRepository
	users map[int64]*service.User
	err   error
}

func (r *groupProtectionUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	if r.err != nil {
		return nil, r.err
	}
	if user := r.users[id]; user != nil {
		return user, nil
	}
	return nil, service.ErrUserNotFound
}

func TestGroupAntiDistillationIndependentSettingsAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &groupProtectionSettings{values: map[string]string{}}
	protection := service.NewGroupAntiDistillationService(settings, groupProtectionGroups{}, nil, nil, nil)
	h := ProvideGroupHandlerWithProtection(newStubAdminService(), nil, nil, &config.Config{}, protection)
	r := gin.New()
	r.GET("/groups/:id/anti-distillation", h.GetAntiDistillation)
	r.PUT("/groups/:id/anti-distillation", h.SaveAntiDistillation)
	res := serveGroupProtectionRequest(r, "GET", "/groups/7/anti-distillation", "")
	require.Equal(t, 200, res.Code)
	require.Contains(t, res.Body.String(), `"enabled":false`)
	require.Contains(t, res.Body.String(), `"pause_minutes":5`)
	require.Contains(t, res.Body.String(), `"user_allowlist_ids":[]`)
	res = serveGroupProtectionRequest(r, "PUT", "/groups/7/anti-distillation", `{"enabled":true,"pause_minutes":10}`)
	require.Equal(t, 200, res.Code)
	res = serveGroupProtectionRequest(r, "GET", "/groups/7/anti-distillation", "")
	require.Contains(t, res.Body.String(), `"enabled":true`)
	require.Contains(t, res.Body.String(), `"pause_minutes":10`)
	for _, body := range []string{`{}`, `null`, `{"enabled":false}`, `{"pause_minutes":5}`, `{"enabled":null,"pause_minutes":5}`, `{"enabled":true,"pause_minutes":0}`, `{"enabled":true,"pause_minutes":1441}`} {
		res = serveGroupProtectionRequest(r, "PUT", "/groups/7/anti-distillation", body)
		require.Equal(t, 400, res.Code, body)
	}
	res = serveGroupProtectionRequest(r, "GET", "/groups/8/anti-distillation", "")
	require.Equal(t, 404, res.Code)
}

func TestGroupAntiDistillationAllowlistSettingsAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &groupProtectionSettings{values: map[string]string{}}
	deletedAt := time.Now()
	users := &groupProtectionUsers{users: map[int64]*service.User{7: {ID: 7}, 8: {ID: 8}, 9: {ID: 9, DeletedAt: &deletedAt}}}
	protection := service.NewGroupAntiDistillationService(settings, groupProtectionGroups{}, nil, users, nil)
	h := ProvideGroupHandlerWithProtection(newStubAdminService(), nil, nil, &config.Config{}, protection)
	r := gin.New()
	r.GET("/groups/:id/anti-distillation", h.GetAntiDistillation)
	r.PUT("/groups/:id/anti-distillation", h.SaveAntiDistillation)
	const path = "/groups/7/anti-distillation"
	res := serveGroupProtectionRequest(r, http.MethodPut, path, `{"enabled":true,"pause_minutes":10,"user_allowlist_ids":[8,7,8]}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Contains(t, res.Body.String(), `"user_allowlist_ids":[7,8]`)
	res = serveGroupProtectionRequest(r, http.MethodGet, path, "")
	require.Equal(t, http.StatusOK, res.Code)
	require.Contains(t, res.Body.String(), `"user_allowlist_ids":[7,8]`)
	res = serveGroupProtectionRequest(r, http.MethodPut, path, `{"enabled":false,"pause_minutes":15}`)
	require.Equal(t, http.StatusOK, res.Code)
	require.Contains(t, res.Body.String(), `"user_allowlist_ids":[7,8]`, "older clients must preserve the list when changing other fields")
	stored := settings.values["group_anti_distillation:7"]
	for _, value := range []string{"null", "7", `"7"`, "{}", "[null]", `["7"]`, "[true]", "[7.5]", "[0]", "[-1]", "[9223372036854775808]", "[404]", "[9]", "[" + strings.Repeat("7,", 500) + "7]"} {
		t.Run(value[:min(len(value), 40)], func(t *testing.T) {
			body := `{"enabled":true,"pause_minutes":5,"user_allowlist_ids":` + value + `}`
			res := serveGroupProtectionRequest(r, http.MethodPut, path, body)
			require.Equal(t, http.StatusBadRequest, res.Code, res.Body.String())
			if value == "[9]" || value == "[404]" {
				require.Contains(t, res.Body.String(), "#"+strings.Trim(value, "[]"), "identify the missing or deleted user so administrators can remove it")
			}
			require.Equal(t, stored, settings.values["group_anti_distillation:7"], "invalid allowlists must not alter any setting")
		})
	}
	users.err = errors.New("user repository offline")
	res = serveGroupProtectionRequest(r, http.MethodPut, path, `{"enabled":true,"pause_minutes":5,"user_allowlist_ids":[7]}`)
	require.Equal(t, http.StatusInternalServerError, res.Code)
	require.Equal(t, stored, settings.values["group_anti_distillation:7"])
	res = serveGroupProtectionRequest(r, http.MethodPut, path, `{"enabled":false,"pause_minutes":15,"user_allowlist_ids":[]}`)
	require.Equal(t, http.StatusOK, res.Code)
	require.Contains(t, res.Body.String(), `"user_allowlist_ids":[]`, "explicit empty must clear without querying users")
	res = serveGroupProtectionRequest(r, http.MethodGet, path, "")
	require.Equal(t, http.StatusOK, res.Code)
	require.Contains(t, res.Body.String(), `"user_allowlist_ids":[]`)
}

type groupProtectionConflictingSettings struct{ *groupProtectionSettings }

func (s groupProtectionConflictingSettings) CompareAndSwap(context.Context, string, *string, string) (bool, error) {
	return false, nil
}

func TestGroupAntiDistillationAllowlistOmissionAtomicSaveErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"continuous conflict", http.StatusConflict},
		{"atomic writer unavailable", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const original = `{"enabled":true,"pause_minutes":5,"user_allowlist_ids":[7]}`
			settings := &groupProtectionSettings{values: map[string]string{"group_anti_distillation:7": original}}
			var repository service.SettingRepository = groupProtectionConflictingSettings{settings}
			if tc.status == http.StatusServiceUnavailable {
				repository = struct{ service.SettingRepository }{settings}
			}
			protection := service.NewGroupAntiDistillationService(repository, groupProtectionGroups{}, nil, nil, nil)
			h := ProvideGroupHandlerWithProtection(newStubAdminService(), nil, nil, &config.Config{}, protection)
			r := gin.New()
			r.PUT("/groups/:id/anti-distillation", h.SaveAntiDistillation)
			res := serveGroupProtectionRequest(r, http.MethodPut, "/groups/7/anti-distillation", `{"enabled":false,"pause_minutes":15}`)
			require.Equal(t, tc.status, res.Code, res.Body.String())
			require.Equal(t, original, settings.values["group_anti_distillation:7"])
		})
	}
}

func serveGroupProtectionRequest(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	return res
}

func TestGroupAntiDistillationSimpleModeRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := ProvideGroupHandlerWithProtection(newStubAdminService(), nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil)
	r := gin.New()
	r.GET("/groups/:id/anti-distillation", h.GetAntiDistillation)
	r.PUT("/groups/:id/anti-distillation", h.SaveAntiDistillation)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		res := serveGroupProtectionRequest(r, method, "/groups/7/anti-distillation", `{"enabled":true,"pause_minutes":5}`)
		require.Equal(t, http.StatusForbidden, res.Code)
	}
}

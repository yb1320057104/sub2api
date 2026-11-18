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

type kiloAccountViewListStub struct {
	*stubAdminService
	views       map[int64]service.KiloAccountView
	viewCalls   [][]int64
	logicalList bool
}

func (s *kiloAccountViewListStub) ListAccounts(ctx context.Context, page, pageSize int, platform, accountType, status, search string, groupID int64, privacyMode, sortBy, sortOrder string) ([]service.Account, int64, error) {
	s.logicalList = service.IsKiloAccountList(ctx)
	return s.stubAdminService.ListAccounts(ctx, page, pageSize, platform, accountType, status, search, groupID, privacyMode, sortBy, sortOrder)
}

func (s *kiloAccountViewListStub) BulkUpdateAccounts(ctx context.Context, input *service.BulkUpdateAccountsInput) (*service.BulkUpdateAccountsResult, error) {
	s.logicalList = service.IsKiloAccountList(ctx)
	return s.stubAdminService.BulkUpdateAccounts(ctx, input)
}

func TestKiloAccountViewBulkFiltersUseLogicalContextOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &kiloAccountViewListStub{stubAdminService: newStubAdminService()}
	h := &AccountHandler{adminService: svc}
	router := gin.New()
	router.POST("/bulk", h.BulkUpdate)
	for _, test := range []struct {
		body    string
		logical bool
	}{
		{`{"filters":{"platform":"openai"},"priority":2}`, true},
		{`{"account_ids":[124],"priority":2}`, false},
		{`{"account_ids":[124],"filters":{"platform":"openai"},"priority":2}`, false},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/bulk", strings.NewReader(test.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, test.logical, svc.logicalList)
	}
}

func (s *kiloAccountViewListStub) GetKiloAccountViews(_ context.Context, ids []int64) (map[int64]service.KiloAccountView, error) {
	s.viewCalls = append(s.viewCalls, append([]int64(nil), ids...))
	return s.views, nil
}

func TestKiloAccountViewHandlerFullLiteAndDetailUseSingleBatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	preparedID := int64(2)
	svc := &kiloAccountViewListStub{stubAdminService: newStubAdminService(), views: map[int64]service.KiloAccountView{
		124: {PreparedID: &preparedID, Name: "GPT owner", Email: "owner@example.test", HasKey: true, Source: &service.KiloAccountSourceView{ID: 122, Name: "GPT owner", Email: "owner@example.test", PlanType: "pro", WorkspaceID: "workspace-1", Status: "active", QuotaOnly: true}},
		300: {Name: "Single Key", HasKey: true},
	}}
	svc.accounts = []service.Account{
		{ID: 124, Name: "gateway", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Extra: map[string]any{service.KiloManagedExtraKey: true}, Credentials: map[string]any{"api_key": "synthetic-key-secret"}},
		{ID: 300, Name: "single", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Extra: map[string]any{service.KiloManagedExtraKey: true}},
		{ID: 400, Name: "other", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey},
	}
	h := &AccountHandler{adminService: svc}
	router := gin.New()
	router.GET("/accounts", h.List)
	for _, lite := range []string{"0", "1"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/accounts?lite="+lite, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.True(t, svc.logicalList)
		require.Equal(t, []int64{124, 300}, svc.viewCalls[len(svc.viewCalls)-1])
		var payload struct {
			Data struct {
				Items []struct {
					ID   int64                    `json:"id"`
					Kilo *service.KiloAccountView `json:"kilo"`
				} `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		require.EqualValues(t, 124, payload.Data.Items[0].ID)
		require.EqualValues(t, 122, payload.Data.Items[0].Kilo.Source.ID)
		require.Nil(t, payload.Data.Items[1].Kilo.Source)
		require.Nil(t, payload.Data.Items[2].Kilo)
		require.NotContains(t, rec.Body.String(), "synthetic-")
	}
	require.Len(t, svc.viewCalls, 2)
	detail := h.buildAccountResponseWithRuntime(context.Background(), &svc.accounts[0])
	require.EqualValues(t, 122, detail.Kilo.Source.ID)
	require.Len(t, svc.viewCalls, 3)
	observer := service.WithObserverScope(context.Background(), []int64{7})
	detail = h.buildAccountResponseWithRuntime(observer, &svc.accounts[0])
	require.Nil(t, detail.Kilo)
	require.Len(t, svc.viewCalls, 3, "observer cannot request a cross-group identity projection")
}

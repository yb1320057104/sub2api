//go:build unit

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type intelligenceModelsHandlerSettings struct{ service.SettingRepository }

func (intelligenceModelsHandlerSettings) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}

func TestIntelligenceModelsHandlerAdminBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		role       string
		subject    bool
		restricted bool
		want       int
	}{
		{"anonymous", "", false, false, http.StatusUnauthorized},
		{"user", service.RoleUser, true, false, http.StatusForbidden},
		{"scoped_admin", service.RoleAdmin, true, true, http.StatusForbidden},
		{"admin", service.RoleAdmin, true, false, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			if tc.want == http.StatusOK {
				mock.ExpectQuery("WITH local_accounts").WillReturnRows(sqlmock.NewRows([]string{"model", "target", "media"}).AddRow("my-text-model", "my-text-model", false).AddRow("my-media-alias", "gpt-image-2", false))
			}
			svc := service.NewIntelligenceTestService(db, nil, intelligenceModelsHandlerSettings{}, nil, nil, nil)
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if tc.subject {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
					c.Set(string(middleware.ContextKeyUserRole), tc.role)
				}
				if tc.restricted {
					c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), &adminaccess.Principal{KeyID: "scoped"}))
				}
			})
			router.GET("/admin/intelligence-test/models", NewIntelligenceTestHandler(svc).GetModels)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/intelligence-test/models", nil))
			require.Equal(t, tc.want, recorder.Code)
			if tc.want == http.StatusOK {
				require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
				require.Contains(t, recorder.Body.String(), "my-text-model")
				require.Contains(t, recorder.Body.String(), `"excluded_models":[`)
				require.Contains(t, recorder.Body.String(), "my-media-alias")
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestIntelligenceModelsHandlerSafeFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("WITH local_accounts").WillReturnError(errors.New("postgres://secret@internal.private/database"))
	svc := service.NewIntelligenceTestService(db, nil, intelligenceModelsHandlerSettings{}, nil, nil, nil)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/admin/intelligence-test/models", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
	c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
	NewIntelligenceTestHandler(svc).GetModels(c)
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Contains(t, recorder.Body.String(), "读取智商测试模型候选失败")
	require.NotContains(t, recorder.Body.String(), "secret")
	require.NotContains(t, recorder.Body.String(), "internal.private")
	require.NoError(t, mock.ExpectationsWereMet())
}

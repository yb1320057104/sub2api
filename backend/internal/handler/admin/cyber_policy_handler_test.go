package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCyberPolicyRecordsRejectsUnauthorizedAndInvalidRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		subject bool
		role    string
		id      string
		status  int
		message string
	}{
		{"anonymous", false, "", "7", http.StatusUnauthorized, "请先登录"},
		{"role_without_subject", false, service.RoleAdmin, "7", http.StatusUnauthorized, "请先登录"},
		{"ordinary_user", true, service.RoleUser, "7", http.StatusForbidden, "仅管理员可以操作耻辱排行榜"},
		{"missing_role", true, "", "7", http.StatusForbidden, "仅管理员可以操作耻辱排行榜"},
		{"authorization_precedes_id_validation", true, service.RoleUser, "invalid", http.StatusForbidden, "仅管理员可以操作耻辱排行榜"},
		{"admin_nonnumeric_id", true, service.RoleAdmin, "invalid", http.StatusBadRequest, "用户 ID 无效"},
		{"admin_zero_id", true, service.RoleAdmin, "0", http.StatusBadRequest, "用户 ID 无效"},
		{"admin_negative_id", true, service.RoleAdmin, "-1", http.StatusBadRequest, "用户 ID 无效"},
		{"admin_overflow_id", true, service.RoleAdmin, "9223372036854775808", http.StatusBadRequest, "用户 ID 无效"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/security-policy-ranking/users/"+tc.id+"/records", nil)
			ctx.Params = gin.Params{{Key: "id", Value: tc.id}}
			if tc.subject {
				ctx.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
			}
			if tc.role != "" {
				ctx.Set(string(middleware.ContextKeyUserRole), tc.role)
			}

			// A nil service makes any accidental database access fail immediately.
			handler := &UsageHandler{}
			require.NotPanics(t, func() { handler.CyberPolicyRecords(ctx) })
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), tc.message)
			require.NotContains(t, recorder.Body.String(), `"data"`)
		})
	}
}

func TestCyberPolicyRecordsExcludedFromScopedAdminCatalog(t *testing.T) {
	// Even a scoped key with every published permission cannot access the new
	// full-admin records route through AdminAuthMiddleware's exact allowlist.
	policy := adminaccess.Policy{}
	for _, permission := range adminaccess.PermissionCatalog() {
		if permission.Scope == "global" {
			policy.GlobalPermissions = append(policy.GlobalPermissions, permission.ID)
		} else {
			policy.Rules = append(policy.Rules, adminaccess.Rule{
				Permissions: []string{permission.ID}, AllGroups: true,
				IncludeUngrouped: true, AccountTypes: []string{"oauth", "apikey"},
			})
		}
	}
	require.False(t, adminaccess.RouteAllowed(policy, http.MethodGet, "/api/v1/admin/security-policy-ranking/users/:id/records"))
}

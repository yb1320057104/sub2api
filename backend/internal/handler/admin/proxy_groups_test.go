//go:build unit

package admin

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type proxyGroupHandlerStub struct {
	service.AdminService
	service.ProxyGroupManagementService
	calls int
	input service.GroupProxyPriorityUpdateInput
}

func (s *proxyGroupHandlerStub) ListProxyGroups(context.Context) ([]service.ProxyGroup, error) {
	s.calls++
	return []service.ProxyGroup{{ID: 1, Name: "static", ProxyIDs: []int64{2}, ProxyCount: 1, ActiveProxyCount: 1}}, nil
}
func (s *proxyGroupHandlerStub) UpdateGroupProxyPriorityPolicies(_ context.Context, input service.GroupProxyPriorityUpdateInput) (*service.GroupProxyPriorityUpdateResult, error) {
	s.calls++
	s.input = input
	return &service.GroupProxyPriorityUpdateResult{GroupCount: len(input.GroupIDs)}, nil
}

func TestPK51ProxyGroupAdminBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ method, path, body string }{{"GET", "/proxy-groups", ""}, {"POST", "/proxy-groups", `{"name":"static"}`}, {"POST", "/groups/proxy-policy", `{"group_ids":[1],"strict_priority":true,"proxy_group_id":2}`}, {"POST", "/accounts/assign-proxy-group", `{"account_ids":[1],"proxy_group_id":2,"proxy_concurrency":10}`}} {
		t.Run(tc.path+tc.method, func(t *testing.T) {
			s := &proxyGroupHandlerStub{}
			p := NewProxyHandler(s)
			g := NewGroupHandler(s, nil, nil)
			a := &AccountHandler{adminService: s}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), &adminaccess.Principal{}))
			})
			r.GET("/proxy-groups", p.ListGroups)
			r.POST("/proxy-groups", p.CreateGroup)
			r.POST("/groups/proxy-policy", g.UpdateProxyPolicies)
			r.POST("/accounts/assign-proxy-group", a.AssignProxyGroup)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			require.Equal(t, 403, w.Code, w.Body.String())
			require.Zero(t, s.calls)
		})
	}
}

func TestPK51ProxyGroupListAndFilterContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &proxyGroupHandlerStub{}
	r := gin.New()
	r.GET("/proxy-groups", NewProxyHandler(s).ListGroups)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/proxy-groups", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"proxy_ids":[2]`)
	require.Contains(t, w.Body.String(), `"active_proxy_count":1`)
	for _, tc := range []struct {
		query string
		code  int
		want  int64
		set   bool
	}{{"", 200, 0, false}, {"?proxy_group_id=0", 200, 0, true}, {"?proxy_group_id=12", 200, 12, true}, {"?proxy_group_id=-1", 400, 0, false}, {"?proxy_group_id=no", 400, 0, false}} {
		t.Run(tc.query, func(t *testing.T) {
			r := gin.New()
			r.GET("/proxies", func(c *gin.Context) {
				if !applyProxyGroupRequestFilter(c) {
					return
				}
				id, ok := service.ProxyGroupFilterFromContext(c.Request.Context())
				require.Equal(t, tc.want, id)
				require.Equal(t, tc.set, ok)
				c.Status(200)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/proxies"+tc.query, nil))
			require.Equal(t, tc.code, w.Code)
		})
	}
}

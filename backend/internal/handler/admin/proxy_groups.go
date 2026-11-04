package admin

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func proxyGroupAPI(c *gin.Context, admin service.AdminService) (service.ProxyGroupManagementService, bool) {
	if adminaccess.FromContext(c.Request.Context()) != nil {
		response.Error(c, 403, "IP分组与严格优先级配置仅限完整管理员")
		return nil, false
	}
	api, ok := admin.(service.ProxyGroupManagementService)
	if !ok {
		response.Error(c, 503, "IP分组服务不可用")
	}
	return api, ok
}
func proxyGroupID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "IP分组编号无效")
		return 0, false
	}
	return id, true
}
func applyProxyGroupRequestFilter(c *gin.Context) bool {
	raw, ok := c.GetQuery("proxy_group_id")
	if !ok {
		return true
	}
	if adminaccess.FromContext(c.Request.Context()) != nil {
		response.Error(c, 403, "IP分组筛选仅限完整管理员")
		return false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		response.BadRequest(c, "IP分组筛选编号无效")
		return false
	}
	c.Request = c.Request.WithContext(service.WithProxyGroupFilter(c.Request.Context(), id))
	return true
}
func (h *ProxyHandler) ListGroups(c *gin.Context) {
	api, ok := proxyGroupAPI(c, h.adminService)
	if !ok {
		return
	}
	items, err := api.ListProxyGroups(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}
func (h *ProxyHandler) CreateGroup(c *gin.Context) {
	api, ok := proxyGroupAPI(c, h.adminService)
	if !ok {
		return
	}
	var in service.ProxyGroupInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "IP分组参数无效")
		return
	}
	executeAdminIdempotentJSON(c, "admin.proxy-groups.create", in, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) { return api.SaveProxyGroup(ctx, 0, in) })
}
func (h *ProxyHandler) UpdateGroup(c *gin.Context) {
	api, ok := proxyGroupAPI(c, h.adminService)
	if !ok {
		return
	}
	id, ok := proxyGroupID(c)
	if !ok {
		return
	}
	var in service.ProxyGroupInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "IP分组参数无效")
		return
	}
	executeAdminIdempotentJSON(c, "admin.proxy-groups.update", struct {
		ID    int64                   `json:"id"`
		Input service.ProxyGroupInput `json:"input"`
	}{id, in}, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) { return api.SaveProxyGroup(ctx, id, in) })
}
func (h *ProxyHandler) DeleteGroup(c *gin.Context) {
	api, ok := proxyGroupAPI(c, h.adminService)
	if !ok {
		return
	}
	id, ok := proxyGroupID(c)
	if !ok {
		return
	}
	executeAdminIdempotentJSON(c, "admin.proxy-groups.delete", gin.H{"id": id}, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		if err := api.DeleteProxyGroup(ctx, id); err != nil {
			return nil, err
		}
		return gin.H{"deleted": true}, nil
	})
}
func (h *ProxyHandler) SetGroupMembers(c *gin.Context) {
	api, ok := proxyGroupAPI(c, h.adminService)
	if !ok {
		return
	}
	var in service.ProxyGroupMembersInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "IP分组成员参数无效")
		return
	}
	executeAdminIdempotentJSON(c, "admin.proxies.batch-group", in, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		n, err := api.SetProxyGroupMembers(ctx, in)
		return gin.H{"proxy_count": n}, err
	})
}
func (h *GroupHandler) ListProxyPolicies(c *gin.Context) {
	if h.rejectUnsupportedSimpleModeOperation(c, "proxy_policy") {
		return
	}
	api, ok := proxyGroupAPI(c, h.adminService)
	if !ok {
		return
	}
	items, err := api.ListGroupProxyPriorityPolicies(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}
func (h *GroupHandler) UpdateProxyPolicies(c *gin.Context) {
	if h.rejectUnsupportedSimpleModeOperation(c, "proxy_policy") {
		return
	}
	api, ok := proxyGroupAPI(c, h.adminService)
	if !ok {
		return
	}
	var in service.GroupProxyPriorityUpdateInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "严格优先级参数无效")
		return
	}
	executeAdminIdempotentJSON(c, "admin.groups.proxy-policy", struct {
		service.GroupProxyPriorityUpdateInput
		ProxyGroupIDSet bool `json:"proxy_group_id_set"`
	}{in, in.ProxyGroupIDSet}, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) { return api.UpdateGroupProxyPriorityPolicies(ctx, in) })
}
func (h *AccountHandler) AssignProxyGroup(c *gin.Context) {
	api, ok := proxyGroupAPI(c, h.adminService)
	if !ok {
		return
	}
	var in service.AccountProxyGroupAssignmentInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "IP分组分配参数无效")
		return
	}
	executeAdminIdempotentJSON(c, "admin.accounts.assign-proxy-group", in, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) { return api.AssignAccountProxyGroup(ctx, in) })
}

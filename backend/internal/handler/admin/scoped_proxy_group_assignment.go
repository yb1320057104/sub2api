package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) assignScopedProxyGroup(c *gin.Context) {
	p := adminaccess.FromContext(c.Request.Context())
	if p == nil || p.KeyID == "" || !slices.Contains(p.Policy.GlobalPermissions, "proxy-groups.assign") {
		response.ErrorWithDetails(c, 403, "IP分组分配未授权", "SCOPED_PROXY_GROUP_FORBIDDEN", nil)
		return
	}
	api, ok := h.adminService.(service.ScopedProxyGroupAssignmentService)
	if !ok {
		response.Error(c, 503, "IP分组服务不可用")
		return
	}
	var in service.AccountProxyGroupAssignmentInput
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "IP分组分配参数无效")
		return
	}
	if _, err := service.NormalizeIdempotencyKey(c.GetHeader("Idempotency-Key")); err != nil || c.GetHeader("Idempotency-Key") == "" {
		response.BadRequest(c, "请提供有效的幂等请求标识")
		return
	}
	// The dedicated service commits durable evidence with the binding writes
	// and rechecks current permission/member scope even when replaying.
	result, err := api.AssignScopedAccountProxyGroup(c.Request.Context(), in, c.GetHeader("Idempotency-Key"))
	if err != nil {
		scopedProxyGroupError(c, err)
		return
	}
	if result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, result.AccountProxyAssignmentResult)
}

func scopedProxyGroupError(c *gin.Context, err error) {
	code := infraerrors.Code(err)
	_, status := infraerrors.ToHTTP(err)
	if status.Reason == "SCOPED_PROXY_GROUP_CHANGED" {
		response.ErrorWithDetails(c, 409, "IP分组成员已变化，请重新核对", status.Reason, nil)
		return
	}
	message, reason := "IP分组服务暂不可用", "SCOPED_PROXY_GROUP_UNAVAILABLE"
	switch code {
	case 400:
		message, reason = "IP分组或账号配置无效，请刷新后核对", "SCOPED_PROXY_GROUP_INVALID"
	case 403:
		message, reason = "IP分组分配未授权或账号存在继承关联", "SCOPED_PROXY_GROUP_FORBIDDEN"
	case 404:
		message, reason = "账号或IP分组不存在或不在授权范围", "SCOPED_PROXY_GROUP_NOT_FOUND"
	case 409:
		message, reason = "原分配请求正在处理或请求内容不一致", "SCOPED_PROXY_GROUP_CONFLICT"
	default:
		code = 503
	}
	response.ErrorWithDetails(c, code, message, reason, nil)
}

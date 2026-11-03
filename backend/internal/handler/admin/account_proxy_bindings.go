package admin

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ProxyConcurrencyReader interface {
	GetProxyConcurrencyBatch(context.Context, []int64) (map[int64]int, error)
}

func (h *AccountHandler) SetProxyConcurrencyReader(reader ProxyConcurrencyReader) {
	h.proxyConcurrencyReader = reader
}

func (h *AccountHandler) attachProxyConcurrency(ctx context.Context, items []AccountWithConcurrency) {
	if h.proxyConcurrencyReader == nil {
		return
	}
	seen := map[int64]bool{}
	var ids []int64
	for _, item := range items {
		if item.Account == nil {
			continue
		}
		for _, b := range item.ProxyBindings {
			if !seen[b.ProxyID] {
				seen[b.ProxyID] = true
				ids = append(ids, b.ProxyID)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	counts, err := h.proxyConcurrencyReader.GetProxyConcurrencyBatch(ctx, ids)
	if err != nil {
		return
	} // Unknown remains null; never claim a failed read means zero.
	for _, item := range items {
		if item.Account == nil {
			continue
		}
		for i := range item.ProxyBindings {
			n := counts[item.ProxyBindings[i].ProxyID]
			item.ProxyBindings[i].CurrentConcurrency = &n
		}
	}
}

func (h *AccountHandler) AssignProxies(c *gin.Context) {
	if adminaccess.FromContext(c.Request.Context()) != nil {
		response.Error(c, 403, "多代理分配仅限完整管理员")
		return
	}
	var input service.AccountProxyAssignmentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "代理分配参数无效")
		return
	}
	api, ok := h.adminService.(service.AccountProxyAssignmentService)
	if !ok {
		response.Error(c, 503, "代理分配服务不可用")
		return
	}
	result, err := executeAdminIdempotent(c, "admin.accounts.assign-proxies", input, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) { return api.AssignAccountProxies(ctx, input) })
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if result != nil && result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	response.Success(c, result.Data)
}

package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) healthService(c *gin.Context) *service.AccountHealthService {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "请先登录")
		return nil
	}
	role, ok := middleware.GetUserRoleFromContext(c)
	if !ok || role != service.RoleAdmin || adminaccess.FromContext(c.Request.Context()) != nil {
		response.Forbidden(c, "仅完整权限的管理员可以管理账号统计")
		return nil
	}
	c.Header("Cache-Control", "no-store")
	s, err := h.accountUsageService.RequireAccountHealth()
	if err != nil {
		response.ErrorFrom(c, err)
		return nil
	}
	return s
}
func (h *AccountHandler) GetAccountHealthProgress(c *gin.Context) {
	s := h.healthService(c)
	if s == nil {
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	size, _ := strconv.Atoi(c.Query("page_size"))
	if page > 1000000 {
		response.BadRequest(c, "页码过大")
		return
	}
	result, err := s.Progress(c.Request.Context(), page, size)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *AccountHandler) SaveAccountHealthConfig(c *gin.Context) {
	s := h.healthService(c)
	if s == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input service.AccountHealthConfig
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "统计配置格式错误")
		return
	}
	if err := s.SaveConfig(c.Request.Context(), &input); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	config, err := s.Config(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, config)
}

package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *UsageHandler) SetCyberPolicyService(s *service.CyberPolicyService) { h.cyberPolicy = s }

func requireCyberPolicyAdmin(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录")
		return 0, false
	}
	role, _ := middleware.GetUserRoleFromContext(c)
	if role != service.RoleAdmin {
		response.Forbidden(c, "仅管理员可以操作耻辱排行榜")
		return 0, false
	}
	return subject.UserID, true
}

func cyberPolicyUserID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "用户 ID 无效")
		return 0, false
	}
	return id, true
}

func cyberPolicyQueryWindow(c *gin.Context) service.CyberPolicyWindowInput {
	return service.CyberPolicyWindowInput{StartDate: c.Query("start_date"), EndDate: c.Query("end_date"), Timezone: c.Query("timezone")}
}

func (h *UsageHandler) CyberPolicyRanking(c *gin.Context) {
	if _, ok := requireCyberPolicyAdmin(c); !ok {
		return
	}
	page, size := response.ParsePagination(c)
	r, err := h.cyberPolicy.Ranking(c.Request.Context(), cyberPolicyQueryWindow(c), page, size)
	if !response.ErrorFrom(c, err) {
		response.Success(c, r)
	}
}

func (h *UsageHandler) CyberPolicyPublicRanking(c *gin.Context) {
	if _, ok := middleware.GetAuthSubjectFromContext(c); !ok {
		response.Unauthorized(c, "请先登录")
		return
	}
	page, size := response.ParsePagination(c)
	r, err := h.cyberPolicy.PublicRanking(c.Request.Context(), cyberPolicyQueryWindow(c), page, size)
	if !response.ErrorFrom(c, err) {
		response.Success(c, r)
	}
}

func (h *UsageHandler) CyberPolicyRecords(c *gin.Context) {
	if _, ok := requireCyberPolicyAdmin(c); !ok {
		return
	}
	id, ok := cyberPolicyUserID(c)
	if !ok {
		return
	}
	page, size := response.ParsePagination(c)
	r, err := h.cyberPolicy.Records(c.Request.Context(), id, cyberPolicyQueryWindow(c), page, size)
	if !response.ErrorFrom(c, err) {
		response.Success(c, r)
	}
}

func (h *UsageHandler) CyberPolicySettings(c *gin.Context) {
	if _, ok := requireCyberPolicyAdmin(c); !ok {
		return
	}
	r, err := h.cyberPolicy.Settings(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, r)
	}
}

func (h *UsageHandler) SaveCyberPolicySettings(c *gin.Context) {
	if _, ok := requireCyberPolicyAdmin(c); !ok {
		return
	}
	var input service.CyberPolicySettingsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "设置格式无效")
		return
	}
	r, err := h.cyberPolicy.SaveSettings(c.Request.Context(), input)
	if !response.ErrorFrom(c, err) {
		response.Success(c, r)
	}
}

func (h *UsageHandler) PreviewCyberPolicyDeduction(c *gin.Context) {
	actor, ok := requireCyberPolicyAdmin(c)
	if !ok {
		return
	}
	id, ok := cyberPolicyUserID(c)
	if !ok {
		return
	}
	var input service.CyberPolicyWindowInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "日期格式无效")
		return
	}
	r, err := h.cyberPolicy.Preview(c.Request.Context(), actor, id, input)
	if !response.ErrorFrom(c, err) {
		response.Success(c, r)
	}
}

func (h *UsageHandler) DeductCyberPolicyBalance(c *gin.Context) {
	actor, ok := requireCyberPolicyAdmin(c)
	if !ok {
		return
	}
	id, ok := cyberPolicyUserID(c)
	if !ok {
		return
	}
	var input struct {
		PreviewID string `json:"preview_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "请先确认扣除预览")
		return
	}
	r, err := h.cyberPolicy.Deduct(c.Request.Context(), actor, id, input.PreviewID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, r)
	}
}

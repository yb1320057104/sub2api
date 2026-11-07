package securityaudit

import (
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *PromptAdminHandler) SetAuditCenter(content *service.ContentModerationService, ranking *service.CyberPolicyService) {
	if h == nil || content == nil || ranking == nil {
		return
	}
	prompt, ok := h.service.(*PromptService)
	if !ok || prompt == nil {
		return
	}
	manager, ok := prompt.config.(*ConfigManager)
	if !ok || manager == nil || manager.db == nil {
		return
	}
	h.auditCenter = &AuditCenterService{prompt: manager, content: content, ranking: ranking}
}

func requireAuditCenterAdmin(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录")
		return 0, false
	}
	role, _ := middleware.GetUserRoleFromContext(c)
	if role != service.RoleAdmin {
		response.Forbidden(c, "仅管理员可以管理审计配置")
		return 0, false
	}
	return subject.UserID, true
}

func (h *PromptAdminHandler) GetAuditCenter(c *gin.Context) {
	if _, ok := requireAuditCenterAdmin(c); !ok {
		return
	}
	if h.auditCenter == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("AUDIT_CENTER_UNAVAILABLE", "审计配置服务暂不可用"))
		return
	}
	config, err := h.auditCenter.Get(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, config)
}

func (h *PromptAdminHandler) UpdateAuditCenter(c *gin.Context) {
	actor, ok := requireAuditCenterAdmin(c)
	if !ok {
		return
	}
	if h.auditCenter == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("AUDIT_CENTER_UNAVAILABLE", "审计配置服务暂不可用"))
		return
	}
	var input AuditCenterUpdate
	if err := c.ShouldBindJSON(&input); err != nil {
		setPromptAdminAudit(c, "failed", "AUDIT_CENTER_INVALID_REQUEST", nil)
		response.ErrorFrom(c, infraerrors.BadRequest("AUDIT_CENTER_INVALID_REQUEST", "审计配置请求格式无效"))
		return
	}
	config, err := h.auditCenter.Save(c.Request.Context(), input, actor)
	if err != nil {
		setPromptAdminAudit(c, "failed", infraerrors.Reason(err), nil)
		response.ErrorFrom(c, err)
		return
	}
	setPromptAdminAudit(c, "success", "", map[string]any{"source": "audit_center", "revision": config.Revision, "prompt_config_version": config.Prompt.ConfigVersion, "ranking_settings_revision": config.Ranking.SettingsRevision})
	response.Success(c, config)
}

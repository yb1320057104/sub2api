package admin

import (
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ZhongcaiImport is deliberately scoped-only. Full administrators keep the legacy API.
func (h *AccountHandler) ZhongcaiImport(c *gin.Context) {
	p := adminaccess.FromContext(c.Request.Context())
	if p == nil {
		response.ErrorWithDetails(c, 404, "此连接不使用受限导入协议", "ZHONGCAI_IMPORT_UNSUPPORTED", nil)
		return
	}
	if !adminaccess.RouteAllowed(p.Policy, c.Request.Method, c.FullPath()) {
		response.ErrorWithDetails(c, 403, "导入未授权", "ZHONGCAI_IMPORT_FORBIDDEN", nil)
		return
	}
	api, ok := h.adminService.(service.ScopedZhongcaiImports)
	if !ok {
		response.ErrorWithDetails(c, 503, "导入服务暂不可用", "ZHONGCAI_IMPORT_UNAVAILABLE", nil)
		return
	}
	if c.Request.Method == http.MethodGet && c.Param("source") == "" {
		response.Success(c, gin.H{"protocol": service.ZhongcaiImportProtocol})
		return
	}
	var result *service.ZhongcaiImportReceipt
	var err error
	if c.Request.Method == http.MethodPost {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.ZhongcaiImportMaxBody)
		var raw []byte
		raw, err = io.ReadAll(c.Request.Body)
		if err != nil {
			response.ErrorWithDetails(c, 400, "导入参数无效或请求过大", "ZHONGCAI_IMPORT_INVALID", nil)
			return
		}
		result, err = api.ScopedZhongcaiImport(c.Request.Context(), raw)
	} else {
		result, err = api.ScopedZhongcaiReceipt(c.Request.Context(), c.Param("source"), c.Param("operation"))
	}
	if err != nil {
		zhongcaiImportError(c, err)
		return
	}
	response.Success(c, result)
}

func zhongcaiImportError(c *gin.Context, err error) {
	code, reason := infraerrors.Code(err), infraerrors.Reason(err)
	message := "导入服务暂不可用，请核对原导入结果"
	switch reason {
	case "ZHONGCAI_IMPORT_INVALID":
		message = "导入参数无效"
	case "ZHONGCAI_IMPORT_FORBIDDEN":
		message = "当前密钥未获此导入操作所需权限"
	case "ZHONGCAI_IMPORT_NOT_FOUND":
		message = "未找到此导入操作"
	case "ZHONGCAI_IMPORT_ACCOUNT_UNAVAILABLE":
		message = "原导入账号已删除或不可用"
	case "ZHONGCAI_IMPORT_ACCOUNT_CHANGED":
		message = "原导入账号身份已改变，请人工核对"
	case "ZHONGCAI_IMPORT_CONFLICT":
		message = "同一导入操作的请求内容不一致"
	default:
		switch code {
		case 400:
			reason, message = "ZHONGCAI_IMPORT_REJECTED", "导入参数未通过账号或代理校验"
		case 403:
			reason, message = "ZHONGCAI_IMPORT_FORBIDDEN", "当前密钥未获此导入操作所需权限"
		case 404:
			reason, message = "ZHONGCAI_IMPORT_RESOURCE_UNAVAILABLE", "导入所需资源不可用"
		case 409:
			reason, message = "ZHONGCAI_IMPORT_REJECTED", "导入资源冲突，请核对配置"
		default:
			code, reason = 503, "ZHONGCAI_IMPORT_UNAVAILABLE"
		}
	}
	// Never propagate underlying errors: they can include credentials or database details.
	response.ErrorWithDetails(c, code, message, reason, nil)
}

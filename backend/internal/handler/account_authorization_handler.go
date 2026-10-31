package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountAuthorizationHandler struct {
	service *service.AccountAuthorizationService
}

func (h *AccountAuthorizationHandler) Supplement(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	var input service.AuthorizationSecret
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "authorization.credentials.supplement")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if !authorizationError(c, h.service.Supplement(ctx, id, &input)) {
		response.Success(c, map[string]any{"status": "credentials_completed"})
	}
}

func NewAccountAuthorizationHandler(s *service.AccountAuthorizationService) *AccountAuthorizationHandler {
	return &AccountAuthorizationHandler{service: s}
}

func authorizationAdmin(c *gin.Context) bool {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "请先登录")
		return false
	}
	role, ok := middleware.GetUserRoleFromContext(c)
	if !ok || role != service.RoleAdmin || adminaccess.FromContext(c.Request.Context()) != nil {
		response.Forbidden(c, "仅完整权限管理员可管理账号授权")
		return false
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	return true
}
func authorizationInput(c *gin.Context, value any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "请求格式错误、字段无效或内容过大")
		return false
	}
	return true
}
func authorizationError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "记录不存在")
		return true
	}
	status, _ := infraerrors.ToHTTP(err)
	if status >= 500 {
		response.InternalError(c, "操作未完成，请检查数据库、加密密钥或授权运行环境")
	} else {
		response.ErrorFrom(c, err)
	}
	return true
}
func authorizationID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		response.BadRequest(c, "记录ID无效")
		return 0, false
	}
	return id, true
}

func (h *AccountAuthorizationHandler) Config(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	cfg, err := h.service.GetConfig(ctx)
	if !authorizationError(c, err) {
		response.Success(c, cfg)
	}
}
func (h *AccountAuthorizationHandler) SaveConfig(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	var cfg service.AuthorizationConfig
	if !authorizationInput(c, &cfg) {
		return
	}
	middleware.SetAuditAction(c, "authorization.config.update")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if authorizationError(c, h.service.SaveConfig(ctx, &cfg)) {
		return
	}
	saved, err := h.service.GetConfig(ctx)
	if !authorizationError(c, err) {
		response.Success(c, saved)
	}
}
func (h *AccountAuthorizationHandler) Submit(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	var input service.AuthorizationSubmit
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "authorization.jobs.create")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	batch, err := h.service.Submit(ctx, &input)
	input.Accounts = ""
	if !authorizationError(c, err) {
		response.Accepted(c, map[string]any{"batch_id": batch})
	}
}
func (h *AccountAuthorizationHandler) List(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authorizationAdmin(c) {
			return
		}
		page, e1 := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, e2 := strconv.Atoi(c.DefaultQuery("page_size", "20"))
		if e1 != nil || e2 != nil || page < 1 || page > 1000000 || size < 1 || size > 100 || len(c.Query("search")) > 254 {
			response.BadRequest(c, "分页或搜索参数无效")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		data, err := h.service.ListAuthorization(ctx, kind, c.Query("search"), c.Query("status"), page, size)
		if !authorizationError(c, err) {
			response.Success(c, data)
		}
	}
}
func (h *AccountAuthorizationHandler) Reveal(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "authorization.history.reveal")
	secret, err := h.service.Reveal(c.Request.Context(), id)
	if !authorizationError(c, err) {
		response.Success(c, secret)
	}
}

func (h *AccountAuthorizationHandler) UpdateCredential(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	var input service.AuthorizationCredentialUpdate
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "authorization.history.update")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	err := h.service.UpdateAuthorizationCredential(ctx, id, &input)
	input.Password, input.TwoFA = "", ""
	if !authorizationError(c, err) {
		response.Success(c, map[string]any{"updated": true})
	}
}

func (h *AccountAuthorizationHandler) DeleteCredential(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "authorization.history.delete")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if !authorizationError(c, h.service.DeleteAuthorizationCredential(ctx, id, c.Query("revision"))) {
		response.Success(c, map[string]any{"deleted": true})
	}
}

func (h *AccountAuthorizationHandler) AddProxies(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	var input struct {
		Proxies string `json:"proxies"`
	}
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "authorization.proxies.create")
	if !authorizationError(c, h.service.AddProxies(c.Request.Context(), input.Proxies)) {
		response.Success(c, map[string]any{"saved": true})
	}
}
func (h *AccountAuthorizationHandler) DisableProxy(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "authorization.proxies.disable")
	if !authorizationError(c, h.service.DisableProxy(c.Request.Context(), id)) {
		response.Success(c, map[string]any{"disabled": true})
	}
}
func (h *AccountAuthorizationHandler) CheckProxy(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "authorization.proxies.check")
	if !authorizationError(c, h.service.CheckAuthorizationProxy(c.Request.Context(), id)) {
		response.Success(c, map[string]any{"checked": true})
	}
}
func (h *AccountAuthorizationHandler) Cancel(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "authorization.jobs.cancel")
	if !authorizationError(c, h.service.CancelJob(c.Request.Context(), id)) {
		response.Success(c, map[string]any{"cancelled": true})
	}
}

func (h *AccountAuthorizationHandler) DeleteProxies(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	var input struct {
		IDs []int64 `json:"ids"`
	}
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "authorization.proxies.delete_batch")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	count, err := h.service.DeleteAuthorizationProxies(ctx, input.IDs)
	if !authorizationError(c, err) {
		response.Success(c, map[string]any{"deleted": count})
	}
}

func (h *AccountAuthorizationHandler) RetryJob(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "authorization.jobs.retry")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	result, err := h.service.RetryAuthorizationJob(ctx, id)
	if !authorizationError(c, err) {
		response.Success(c, result)
	}
}
func (h *AccountAuthorizationHandler) AuthorizeNow(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "authorization.accounts.authorize_now")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	result, err := h.service.AuthorizeAccountNow(ctx, id)
	if !authorizationError(c, err) {
		response.Success(c, result)
	}
}

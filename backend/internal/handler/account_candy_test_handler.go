package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AccountCandyTestHandler manages account-specific probes. It never accepts
// credentials, an upstream URL, or an arbitrary prompt from the browser.
type AccountCandyTestHandler struct {
	service *service.AccountCandyTestService
}

func NewAccountCandyTestHandler(s *service.AccountCandyTestService) *AccountCandyTestHandler {
	return &AccountCandyTestHandler{service: s}
}

func accountCandyTestAdmin(c *gin.Context) bool {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "请先登录")
		return false
	}
	role, ok := middleware.GetUserRoleFromContext(c)
	if !ok || role != service.RoleAdmin || adminaccess.FromContext(c.Request.Context()) != nil {
		response.Forbidden(c, "仅完整权限的管理员可以管理账号糖果测试")
		return false
	}
	c.Header("Cache-Control", "no-store")
	return true
}

func decodeAccountCandyTestInput(c *gin.Context, target any, emptyAllowed bool) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if emptyAllowed && err == io.EOF {
		return true
	}
	if err != nil {
		response.BadRequest(c, "请求格式不正确或包含不支持的字段")
		return false
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		response.BadRequest(c, "仅支持一个请求对象")
		return false
	}
	return true
}

func (h *AccountCandyTestHandler) GetConfig(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	config, err := h.service.GetConfig(ctx)
	if !response.ErrorFrom(c, err) {
		response.Success(c, config)
	}
}

func (h *AccountCandyTestHandler) UpdateConfig(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	var input struct {
		Enabled         *bool           `json:"enabled"`
		IntervalMinutes *int            `json:"interval_minutes"`
		Model           *string         `json:"model"`
		Effort          *string         `json:"effort"`
		TargetGroupID   json.RawMessage `json:"target_group_id"`
	}
	if !decodeAccountCandyTestInput(c, &input, false) {
		return
	}
	if input.Enabled == nil || input.IntervalMinutes == nil || input.Model == nil || input.Effort == nil || len(input.TargetGroupID) == 0 {
		response.BadRequest(c, "请提交完整的账号糖果测试配置")
		return
	}
	var targetGroupID *int64
	if err := json.Unmarshal(input.TargetGroupID, &targetGroupID); err != nil {
		response.BadRequest(c, "目标分组必须是正整数或 null")
		return
	}
	config := &service.AccountCandyTestConfig{
		Enabled:         *input.Enabled,
		IntervalMinutes: *input.IntervalMinutes,
		Model:           *input.Model,
		Effort:          *input.Effort,
		TargetGroupID:   targetGroupID,
	}
	middleware.SetAuditAction(c, "account.candy_test.config.update")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if !response.ErrorFrom(c, h.service.UpdateConfig(ctx, config)) {
		response.Success(c, config)
	}
}

func (h *AccountCandyTestHandler) ListLatest(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	raw := strings.TrimSpace(c.Query("account_ids"))
	parts := strings.Split(raw, ",")
	if raw == "" || len(parts) > 200 {
		response.BadRequest(c, "请选择 1～200 个账号")
		return
	}
	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]bool, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "账号 ID 必须是正整数")
			return
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	results, err := h.service.ListLatest(ctx, ids)
	if !response.ErrorFrom(c, err) {
		response.Success(c, results)
	}
}

func (h *AccountCandyTestHandler) Trigger(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "请选择有效账号")
		return
	}
	var input struct{}
	if !decodeAccountCandyTestInput(c, &input, true) {
		return
	}
	middleware.SetAuditAction(c, "account.candy_test.trigger")
	middleware.SetAuditExtra(c, map[string]any{"account_id": accountID})
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	result, err := h.service.Trigger(ctx, accountID)
	if !response.ErrorFrom(c, err) {
		response.Accepted(c, result)
	}
}

func (h *AccountCandyTestHandler) GetResult(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	accountID, accountErr := strconv.ParseInt(c.Param("id"), 10, 64)
	resultID, resultErr := strconv.ParseInt(c.Param("result_id"), 10, 64)
	if accountErr != nil || resultErr != nil || accountID <= 0 || resultID <= 0 {
		response.BadRequest(c, "请选择有效测试记录")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	result, err := h.service.GetResult(ctx, accountID, resultID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}

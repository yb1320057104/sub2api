package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// IntelligenceTestHandler exposes saved results and administrator configuration.
// There is deliberately no HTTP endpoint that runs a model request.
type IntelligenceTestHandler struct {
	service *service.IntelligenceTestService
}

func NewIntelligenceTestHandler(s *service.IntelligenceTestService) *IntelligenceTestHandler {
	return &IntelligenceTestHandler{service: s}
}

func intelligenceTestAdmin(c *gin.Context) bool {
	role, ok := middleware.GetUserRoleFromContext(c)
	return ok && role == service.RoleAdmin
}

func intelligenceTestSubject(c *gin.Context, admin bool) (middleware.AuthSubject, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "请先登录")
		return middleware.AuthSubject{}, false
	}
	if admin && !intelligenceTestAdmin(c) {
		response.Forbidden(c, "仅管理员可以配置智商测试")
		return middleware.AuthSubject{}, false
	}
	c.Header("Cache-Control", "no-store")
	return subject, true
}

func (h *IntelligenceTestHandler) canRead(c *gin.Context) bool {
	if _, ok := intelligenceTestSubject(c, false); !ok {
		return false
	}
	if intelligenceTestAdmin(c) {
		return true
	}
	enabled, err := h.service.Enabled(c.Request.Context())
	if err != nil {
		response.InternalError(c, "读取智商测试状态失败")
		return false
	}
	if !enabled {
		response.NotFound(c, "智商测试未开启")
		return false
	}
	return true
}

func (h *IntelligenceTestHandler) Status(c *gin.Context) {
	if _, ok := intelligenceTestSubject(c, false); !ok {
		return
	}
	enabled, err := h.service.Enabled(c.Request.Context())
	if err != nil {
		response.InternalError(c, "读取智商测试状态失败")
		return
	}
	response.Success(c, gin.H{"enabled": enabled})
}

func (h *IntelligenceTestHandler) Groups(c *gin.Context) {
	if !h.canRead(c) {
		return
	}
	groups, err := h.service.ListGroups(c.Request.Context())
	if err != nil {
		response.InternalError(c, "读取测试分组失败")
		return
	}
	response.Success(c, groups)
}

func (h *IntelligenceTestHandler) Results(c *gin.Context) {
	if !h.canRead(c) {
		return
	}
	groupID, err := strconv.ParseInt(c.Query("group_id"), 10, 64)
	mode := c.Query("mode")
	if err != nil || groupID <= 0 || (mode != "svg" && mode != "candy") {
		response.BadRequest(c, "请选择有效分组和测试类型")
		return
	}
	results, err := h.service.ListResults(c.Request.Context(), groupID, mode)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, results)
}

func (h *IntelligenceTestHandler) GetConfig(c *gin.Context) {
	if _, ok := intelligenceTestSubject(c, true); !ok {
		return
	}
	config, err := h.service.GetConfig(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, config)
}

func (h *IntelligenceTestHandler) Artifact(c *gin.Context) {
	if !h.canRead(c) {
		return
	}
	groupID, err := strconv.ParseInt(c.Query("group_id"), 10, 64)
	resultID, idErr := strconv.ParseInt(c.Query("id"), 10, 64)
	if err != nil || idErr != nil || groupID <= 0 || resultID <= 0 {
		response.BadRequest(c, "请选择有效作品")
		return
	}
	artifact, err := h.service.GetArtifact(c.Request.Context(), groupID, resultID)
	if response.ErrorFrom(c, err) {
		return
	}
	// JSON only: model-created HTML is never served as a same-origin document.
	response.Success(c, gin.H{"artifact": artifact})
}

// Reject unknown fields, including credentials and endpoint URLs. The browser
// can only submit the explicit configuration schema, never a model request.
func decodeIntelligenceTestInput(c *gin.Context, target any, emptyAllowed bool) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if emptyAllowed && err == io.EOF {
		return true
	}
	if err != nil {
		response.BadRequest(c, "配置格式不正确或包含不支持的字段")
		return false
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		response.BadRequest(c, "仅支持一个配置对象")
		return false
	}
	return true
}

func (h *IntelligenceTestHandler) UpdateConfig(c *gin.Context) {
	if _, ok := intelligenceTestSubject(c, true); !ok {
		return
	}
	var config service.IntelligenceTestConfig
	if !decodeIntelligenceTestInput(c, &config, false) {
		return
	}
	if response.ErrorFrom(c, h.service.UpdateConfig(c.Request.Context(), &config)) {
		return
	}
	response.Success(c, config)
}

func (h *IntelligenceTestHandler) SyncKeys(c *gin.Context) {
	subject, ok := intelligenceTestSubject(c, true)
	if !ok {
		return
	}
	if !decodeIntelligenceTestInput(c, &struct{}{}, true) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	result, err := h.service.SyncKeys(ctx, subject.UserID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

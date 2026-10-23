package handler

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// Public reads use dedicated service projections. They cannot weaken the
// authenticated/admin handlers or enter the scheduler's execution path.
func (h *IntelligenceTestHandler) PublicGroups(c *gin.Context) {
	groups, err := h.service.ListPublicGroups(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, groups)
}

func (h *IntelligenceTestHandler) PublicResults(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Query("group_id"), 10, 64)
	mode := c.Query("mode")
	if err != nil || groupID <= 0 || (mode != "svg" && mode != "candy") {
		response.BadRequest(c, "请选择有效分组和测试类型")
		return
	}
	results, err := h.service.ListPublicResults(c.Request.Context(), groupID, mode)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, results)
}

func publicIntelligenceTestResultIDs(c *gin.Context) (int64, int64, bool) {
	groupID, err := strconv.ParseInt(c.Query("group_id"), 10, 64)
	resultID, idErr := strconv.ParseInt(c.Query("id"), 10, 64)
	if err != nil || idErr != nil || groupID <= 0 || resultID <= 0 {
		response.BadRequest(c, "请选择有效测试记录")
		return 0, 0, false
	}
	return groupID, resultID, true
}

func (h *IntelligenceTestHandler) PublicArtifact(c *gin.Context) {
	groupID, resultID, ok := publicIntelligenceTestResultIDs(c)
	if !ok {
		return
	}
	artifact, err := h.service.GetPublicArtifact(c.Request.Context(), groupID, resultID)
	if response.ErrorFrom(c, err) {
		return
	}
	// Model-created markup is JSON only, never a same-origin HTML document.
	response.Success(c, gin.H{"artifact": artifact})
}

func (h *IntelligenceTestHandler) PublicDetails(c *gin.Context) {
	groupID, resultID, ok := publicIntelligenceTestResultIDs(c)
	if !ok {
		return
	}
	details, err := h.service.GetPublicDetails(c.Request.Context(), groupID, resultID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, details)
}

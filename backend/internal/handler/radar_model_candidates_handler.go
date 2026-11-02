package handler

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *AccountCandyTestHandler) RadarModels(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	models, err := h.service.RadarModelCandidates(ctx)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"models": models})
	}
}

package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

var errGroupAntiDistillationPaused = errors.New("group anti-distillation paused")

// Recheck after waiting, before any upstream send. On failure the caller must
// release its acquired slot and terminate, without retrying another account.
func (h *OpenAIGatewayHandler) groupAntiDistillationAdmitted(c *gin.Context, streamStarted bool, writeError openAISlotErrorWriter) bool {
	remaining, err := middleware.RecheckGroupAntiDistillation(c.Request.Context())
	if err != nil || remaining <= 0 {
		return true
	}
	middleware.MarkGroupAntiDistillationPaused(c, remaining)
	if strings.HasSuffix(strings.TrimSuffix(c.Request.URL.Path, "/"), "/messages") {
		h.anthropicStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error", middleware.GroupAntiDistillationPausedMessage, streamStarted, middleware.GroupAntiDistillationPausedCode)
	} else if writeError != nil {
		writeError(http.StatusTooManyRequests, "rate_limit_error", middleware.GroupAntiDistillationPausedCode, middleware.GroupAntiDistillationPausedMessage)
	} else {
		h.handleStreamingAwareErrorWithCode(c, http.StatusTooManyRequests, "rate_limit_error", middleware.GroupAntiDistillationPausedCode, middleware.GroupAntiDistillationPausedMessage, streamStarted, false)
	}
	return false
}

func checkOpenAIWSGroupAntiDistillation(ctx context.Context, c *gin.Context) error {
	remaining, err := middleware.RecheckGroupAntiDistillation(ctx)
	if err != nil || remaining <= 0 {
		return nil
	}
	middleware.MarkGroupAntiDistillationPaused(c, remaining)
	// Keep the close reason within the WebSocket 123-byte limit. The business
	// marker and client-close error exclude this from upstream account failures.
	return service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, middleware.GroupAntiDistillationPausedCode, errGroupAntiDistillationPaused)
}

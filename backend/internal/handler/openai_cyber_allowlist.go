package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) cyberPolicyLogOnly(c *gin.Context, apiKey *service.APIKey) bool {
	return h != nil && c != nil && c.Request != nil && h.gatewayService.CyberPolicyLogOnly(c.Request.Context(), apiKey)
}

// Both HTTP and WebSocket admission skip existing blocks for trusted users.
func (h *OpenAIGatewayHandler) findBlockedCyberSessionForAPIKey(c *gin.Context, apiKey *service.APIKey, body []byte) string {
	if h == nil || h.gatewayService == nil || c == nil || c.Request == nil || apiKey == nil {
		return ""
	}
	identity := service.ResolveCyberSessionIdentity(apiKey.ID, c, body)
	return h.findBlockedCyberSessionForRequest(c, apiKey, identity, body)
}

// Both HTTP and WebSocket admission skip existing blocks for trusted users.
// Session identity is still resolved and observed by the caller.
// findBlockedCyberSessionForRequest also uses the content fingerprint fallback
// when the client supplied no explicit session identity, so an identical retry
// of a cyber-blocked request is rejected before account selection.
func (h *OpenAIGatewayHandler) findBlockedCyberSessionForRequest(c *gin.Context, apiKey *service.APIKey, identity service.CyberSessionIdentityResolution, body []byte) string {
	if apiKey == nil || h.cyberPolicyLogOnly(c, apiKey) {
		return ""
	}
	scopeKey := service.CyberSessionScopeKey(apiKey.ID, strings.TrimSpace(ip.GetClientIP(c)), c.GetHeader("User-Agent"))
	return h.gatewayService.FindCyberSessionBlockedForIdentityAndBodyAndScope(c.Request.Context(), apiKey.ID, identity, body, scopeKey)
}

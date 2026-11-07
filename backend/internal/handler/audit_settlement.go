package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type auditSettlementProvider interface {
	BeginAuditSettlement(context.Context, int64, int64) (context.Context, string, error)
	AuditSettlement(context.Context, int64, int64, string) (*service.AuditSettlementReceipt, error)
}

func (h *GatewayHandler) AuditSettlementNegotiation() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader(service.AuditSettlementCapabilityHeader) != service.AuditSettlementVersion ||
			!service.SupportsAuditSettlementRequest(c.Request.Method, c.Request.URL.Path) ||
			(h.cfg != nil && h.cfg.RunMode == config.RunModeSimple) {
			c.Next()
			return
		}
		provider, ok := h.cyberAuditRecorder.(auditSettlementProvider)
		if !ok {
			c.Next()
			return
		}
		key, ok := middleware2.GetAPIKeyFromContext(c)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		negotiated, id, err := provider.BeginAuditSettlement(ctx, key.UserID, key.ID)
		// Keep only the negotiated value on the original request context; admission's
		// short DB budget must not become the model request deadline.
		if err == nil {
			c.Request = c.Request.WithContext(service.CopyAuditSettlement(negotiated, c.Request.Context()))
		}
		cancel()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "code": "audit_settlement_unavailable", "message": "Billing settlement is temporarily unavailable"}})
			return
		}
		c.Header(service.AuditSettlementCapabilityHeader, service.AuditSettlementVersion)
		c.Header(service.AuditSettlementReceiptHeader, id)
		defer service.EndAuditSettlementRequest(c.Request.Context())
		c.Next()
	}
}

func (h *GatewayHandler) AuditSettlement(c *gin.Context) {
	provider, ok := h.cyberAuditRecorder.(auditSettlementProvider)
	if !ok || (h.cfg != nil && h.cfg.RunMode == config.RunModeSimple) {
		c.Status(http.StatusNotFound)
		return
	}
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	receipt, err := provider.AuditSettlement(ctx, key.UserID, key.ID, c.Param("receipt_id"))
	if errors.Is(err, service.ErrAuditSettlementNotFound) {
		c.Status(http.StatusNotFound)
		return
	}
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Header("Cache-Control", "no-store")
	if receipt.Status == "pending" {
		c.Header("Retry-After", "2")
	}
	c.JSON(http.StatusOK, receipt)
}

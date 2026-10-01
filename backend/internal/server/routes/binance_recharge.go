package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func RegisterBinanceRechargeRoutes(v1 *gin.RouterGroup, h *handler.BinanceRechargeHandler, jwtAuth middleware.JWTAuthMiddleware, adminAuth middleware.AdminAuthMiddleware, auditLog middleware.AuditLogMiddleware, settings *service.SettingService, limiter *middleware.PanelRateLimiter) {
	user := v1.Group("/binance-recharge")
	user.Use(gin.HandlerFunc(jwtAuth), middleware.BackendModeUserGuard(settings), limiter.Global())
	user.GET("/config", h.Config)
	user.POST("/orders", h.CreateOrder)
	user.GET("/orders", h.ListOrders)
	user.GET("/orders/:id", h.GetOrder)
	user.POST("/orders/:id/refresh", h.RefreshOrder)
	user.GET("/stats", h.Stats)

	admin := v1.Group("/admin/binance-recharge")
	admin.Use(gin.HandlerFunc(adminAuth), limiter.Global(), gin.HandlerFunc(auditLog), middleware.AdminComplianceGuard(settings))
	admin.GET("/accounts", h.ListAccounts)
	admin.POST("/accounts", h.CreateAccount)
	admin.PUT("/accounts/:id", h.UpdateAccount)
	admin.POST("/accounts/:id/check", h.CheckAccount)
	admin.GET("/orders", h.AdminListOrders)
	admin.GET("/orders/:id", h.AdminGetOrder)
	admin.POST("/orders/:id/refresh", h.AdminRefreshOrder)
	admin.GET("/stats", h.AdminStats)
}

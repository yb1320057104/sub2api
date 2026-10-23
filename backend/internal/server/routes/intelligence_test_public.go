package routes

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	ratelimit "github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func RegisterPublicIntelligenceTestRoutes(v1 *gin.RouterGroup, h *handler.Handlers, limiter *middleware.PanelRateLimiter, redisClient *redis.Client) {
	if h.IntelligenceTest == nil {
		return
	}
	public := v1.Group("/public/intelligence-test")
	public.Use(func(c *gin.Context) {
		// Also cover invalid queries and rate-limit replies: closing the feature
		// must take effect on the next request rather than a cached public response.
		c.Header("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	// Artwork responses are bounded but larger than ordinary panel metadata.
	// Keep an independent limit even if the configurable panel limit is disabled.
	fixedLimiter := ratelimit.NewRateLimiter(redisClient)
	public.Use(fixedLimiter.LimitWithOptions("public-intelligence-test", 240, time.Minute, ratelimit.RateLimitOptions{
		FailureMode: ratelimit.RateLimitFailClose,
	}))
	public.Use(limiter.PublicIP())
	public.GET("/groups", h.IntelligenceTest.PublicGroups)
	public.GET("/results", h.IntelligenceTest.PublicResults)
	public.GET("/artifact", h.IntelligenceTest.PublicArtifact)
	public.GET("/details", h.IntelligenceTest.PublicDetails)
}

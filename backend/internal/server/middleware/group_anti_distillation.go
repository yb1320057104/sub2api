package middleware

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const GroupAntiDistillationPausedCode = "group_anti_distillation_paused"
const GroupAntiDistillationPausedMessage = "此分组的零缓存保护已暂停新请求，请按 Retry-After 等待恢复；此信号不代表已认定蒸馏。"

type groupAntiDistillationChecker func(context.Context, *service.APIKey) (time.Duration, error)
type groupAntiDistillationAdmissionKey struct{}
type groupAntiDistillationAdmission struct {
	check groupAntiDistillationChecker
	key   *service.APIKey
}

// GroupAntiDistillation checks the effective billing group after API-key group
// selection. It does not switch groups or modify upstream account health.
func GroupAntiDistillation(check groupAntiDistillationChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := GetAPIKeyFromContext(c)
		if !ok || key == nil || key.Group == nil || check == nil || !isAntiDistillationGeneration(c) {
			c.Next()
			return
		}
		ctx := service.WithGroupAntiDistillationRequestStart(c.Request.Context(), time.Now())
		ctx = context.WithValue(ctx, groupAntiDistillationAdmissionKey{}, groupAntiDistillationAdmission{check: check, key: key})
		ctx = service.WithGroupAntiDistillationAdmission(ctx, RecheckGroupAntiDistillation)
		c.Request = c.Request.WithContext(ctx)
		// A multi-group WebSocket selects its effective group from the first
		// frame. Do not reject based on the provisional handshake group.
		if c.Request.Method == http.MethodGet && len(key.SelectedGroupIDs()) > 1 {
			c.Next()
			return
		}
		remaining, err := RecheckGroupAntiDistillation(ctx)
		if err != nil {
			slog.Warn("group anti-distillation admission unavailable", "group_id", key.Group.ID)
		}
		if err == nil && remaining > 0 {
			MarkGroupAntiDistillationPaused(c, remaining)
			body := gin.H{"error": gin.H{
				"type": "rate_limit_error", "code": GroupAntiDistillationPausedCode,
				"message": GroupAntiDistillationPausedMessage,
			}}
			if strings.HasSuffix(strings.TrimSuffix(c.Request.URL.Path, "/"), "/messages") {
				body["type"] = "error"
			}
			c.AbortWithStatusJSON(http.StatusTooManyRequests, body)
			return
		}
		c.Next()
	}
}

// RecheckGroupAntiDistillation lets queue admission and WebSocket turns retain
// all existing auth/billing checks while checking the same effective group.
// Cache errors fail open at the caller; a missing ingress checker is a no-op.
func RecheckGroupAntiDistillation(ctx context.Context) (time.Duration, error) {
	admission, _ := ctx.Value(groupAntiDistillationAdmissionKey{}).(groupAntiDistillationAdmission)
	if admission.check == nil || admission.key == nil || admission.key.Group == nil || admission.key.Group.Platform != service.PlatformOpenAI {
		return 0, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return admission.check(bounded, admission.key)
}

// WithGroupAntiDistillationAPIKey replaces only the effective group snapshot
// after first-frame routing, preserving the original request start and checker.
func WithGroupAntiDistillationAPIKey(ctx context.Context, key *service.APIKey) context.Context {
	admission, ok := ctx.Value(groupAntiDistillationAdmissionKey{}).(groupAntiDistillationAdmission)
	if !ok {
		return ctx
	}
	admission.key = key
	return context.WithValue(ctx, groupAntiDistillationAdmissionKey{}, admission)
}

func MarkGroupAntiDistillationPaused(c *gin.Context, remaining time.Duration) {
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	c.Header("Retry-After", strconv.Itoa(int(math.Ceil(remaining.Seconds()))))
}

func isAntiDistillationGeneration(c *gin.Context) bool {
	if c.Request == nil || c.Request.URL == nil {
		return false
	}
	path := strings.TrimSuffix(c.Request.URL.Path, "/")
	if c.Request.Method == http.MethodGet {
		return strings.HasSuffix(path, "/responses")
	}
	if c.Request.Method != http.MethodPost {
		return false
	}
	return strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/responses/compact") ||
		strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/messages")
}

package handler

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountCandyTestHandler) RadarEmailAlert(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	view, err := h.service.EmailAlerts().View(ctx)
	if !response.ErrorFrom(c, err) {
		response.Success(c, view)
	}
}

func (h *AccountCandyTestHandler) SaveRadarEmailAlert(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	var input struct {
		Enabled         *bool `json:"enabled"`
		IntervalMinutes *int  `json:"interval_minutes"`
		MinimumCount    *int  `json:"minimum_count"`
		Conditions      *struct {
			QuotaBelow100 *bool `json:"quota_below_100"`
			Qualified     *bool `json:"qualified"`
			Not401        *bool `json:"not_401"`
		} `json:"conditions"`
		Recipients  *[]string  `json:"recipients"`
		Revision    *int64     `json:"revision"`
		NextCheckAt *time.Time `json:"next_check_at"`
	}
	if !decodeAccountCandyTestInput(c, &input, false) {
		return
	}
	if input.Enabled == nil || input.IntervalMinutes == nil || input.MinimumCount == nil || input.Conditions == nil ||
		input.Conditions.QuotaBelow100 == nil || input.Conditions.Qualified == nil || input.Conditions.Not401 == nil || input.Recipients == nil || input.Revision == nil {
		response.BadRequest(c, "请提交完整的邮件提醒配置及当前版本")
		return
	}
	cfg := &service.RadarEmailAlertConfig{
		Enabled: *input.Enabled, IntervalMinutes: *input.IntervalMinutes, MinimumCount: *input.MinimumCount,
		Conditions: service.RadarEmailAlertConditions{QuotaBelow100: *input.Conditions.QuotaBelow100, Qualified: *input.Conditions.Qualified, Not401: *input.Conditions.Not401},
		Recipients: *input.Recipients, Revision: *input.Revision,
	}
	middleware.SetAuditAction(c, "radar.email_alert.config.update")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	view, err := h.service.EmailAlerts().SaveConfig(ctx, cfg)
	if !response.ErrorFrom(c, err) {
		response.Success(c, view)
	}
}

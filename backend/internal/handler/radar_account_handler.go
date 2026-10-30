package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountCandyTestHandler) RadarConfig(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	cfg, err := h.service.GetRadarConfig(ctx)
	if !response.ErrorFrom(c, err) {
		response.Success(c, cfg)
	}
}

func (h *AccountCandyTestHandler) SaveRadarConfig(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	var input struct {
		Enabled    *bool                `json:"enabled"`
		Interval   *int                 `json:"interval_minutes"`
		Target     json.RawMessage      `json:"target_group_id"`
		Generation string               `json:"generation"`
		Models     []service.RadarModel `json:"models"`
	}
	if !decodeAccountCandyTestInput(c, &input, false) {
		return
	}
	if input.Enabled == nil || input.Interval == nil || len(input.Target) == 0 || input.Generation == "" {
		response.BadRequest(c, "请提交完整设置并刷新配置版本")
		return
	}
	var target *int64
	if json.Unmarshal(input.Target, &target) != nil {
		response.BadRequest(c, "目标分组格式无效")
		return
	}
	cfg := &service.RadarConfig{Enabled: *input.Enabled, IntervalMinutes: *input.Interval, TargetGroupID: target, Generation: input.Generation, Models: input.Models}
	middleware.SetAuditAction(c, "radar.config.update")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if !response.ErrorFrom(c, h.service.UpdateRadarConfig(ctx, cfg)) {
		response.Success(c, cfg)
	}
}

func (h *AccountCandyTestHandler) RadarAccounts(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, sizeErr := strconv.Atoi(c.DefaultQuery("page_size", "12"))
	if err != nil || sizeErr != nil || page < 1 || page > 1000000 || size < 1 || size > 100 {
		response.BadRequest(c, "分页参数无效")
		return
	}
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 200 {
		response.BadRequest(c, "搜索词过长")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	observations, total, cfg, err := h.service.ListRadarAccounts(ctx, page, size, c.DefaultQuery("verdict", "all"), search)
	if response.ErrorFrom(c, err) {
		return
	}
	type item struct {
		Account        *dto.Account      `json:"account"`
		Run            *service.RadarRun `json:"run"`
		Verdict        string            `json:"verdict"`
		NextRunAt      *time.Time        `json:"next_run_at"`
		ScheduleReason string            `json:"schedule_reason"`
	}
	items := make([]item, 0, len(observations))
	for _, o := range observations {
		account := dto.AccountFromServiceShallow(o.Account)
		if p := o.Parent; p != nil {
			account.ParentEmail = p.GetCredential("email")
			account.ParentPlanType = p.GetCredential("plan_type")
			account.ParentSubscriptionExpiresAt = p.GetCredential("subscription_expires_at")
			account.ParentChatGPTAccountID = p.GetCredential("chatgpt_account_id")
			account.ParentPrivacyMode = p.GetExtraString("privacy_mode")
		}
		items = append(items, item{account, o.Run, o.Verdict, o.NextRunAt, o.ScheduleReason})
	}
	response.Success(c, map[string]any{"items": items, "total": total, "page": page, "page_size": size, "generation": cfg.Generation, "interval_minutes": cfg.IntervalMinutes})
}

func (h *AccountCandyTestHandler) StartRadar(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "账号ID无效")
		return
	}
	var input struct{}
	if !decodeAccountCandyTestInput(c, &input, true) {
		return
	}
	middleware.SetAuditAction(c, "radar.account.test")
	middleware.SetAuditExtra(c, map[string]any{"account_id": id})
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	run, err := h.service.StartRadar(ctx, id, "manual")
	if !response.ErrorFrom(c, err) {
		response.Accepted(c, run)
	}
}

func (h *AccountCandyTestHandler) RadarLatest(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	parts := strings.Split(c.Query("account_ids"), ",")
	if len(parts) > 200 {
		response.BadRequest(c, "一次最多读取200个账号")
		return
	}
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "账号ID无效")
			return
		}
		ids = append(ids, id)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	items, err := h.service.ListRadarLatest(ctx, ids)
	if !response.ErrorFrom(c, err) {
		response.Success(c, items)
	}
}

func (h *AccountCandyTestHandler) RadarResult(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	rid, rerr := strconv.ParseInt(c.Param("run_id"), 10, 64)
	if err != nil || rerr != nil || id <= 0 || rid <= 0 {
		response.BadRequest(c, "测试记录ID无效")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	var r *service.RadarRun
	switch c.Query("origin") {
	case "authorization":
		r, err = h.service.GetAuthorizationRadarRun(ctx, id, rid)
	case "", "radar":
		r, err = h.service.GetRadarRun(ctx, id, rid)
	default:
		response.BadRequest(c, "测试记录来源无效")
		return
	}
	if !response.ErrorFrom(c, err) {
		response.Success(c, r)
	}
}

func (h *AccountCandyTestHandler) RadarHistory(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "账号ID无效")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	items, err := h.service.RadarHistory(ctx, id)
	if !response.ErrorFrom(c, err) {
		response.Success(c, items)
	}
}

func (h *AccountCandyTestHandler) LegacyRadarWrite(c *gin.Context) {
	if !accountCandyTestAdmin(c) {
		return
	}
	c.JSON(409, map[string]any{"code": 409, "reason": "RADAR_CLIENT_UPGRADE_REQUIRED", "message": "账号测试已升级为多模型观测，请刷新页面后重试"})
}

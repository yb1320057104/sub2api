package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountTokenGuardV2Handler struct {
	service *service.AccountTokenGuardV2Service
}

func NewAccountTokenGuardV2Handler(guardService *service.AccountTokenGuardV2Service) *AccountTokenGuardV2Handler {
	return &AccountTokenGuardV2Handler{service: guardService}
}

type accountTokenGuardV2SaveRequest struct {
	AccountID          int64  `json:"account_id"`
	LoginEmail         string `json:"login_email" binding:"required"`
	CredentialMode     string `json:"credential_mode" binding:"required"`
	Engine             string `json:"engine"`
	ProxySource        string `json:"proxy_source"`
	ProxyID            *int64 `json:"proxy_id"`
	Password           string `json:"password"`
	TOTPSecret         string `json:"totp_secret"`
	OTPURL             string `json:"otp_url"`
	ClearPassword      bool   `json:"clear_password"`
	ClearTOTP          bool   `json:"clear_totp"`
	Enabled            *bool  `json:"enabled"`
	AutoReloginEnabled *bool  `json:"auto_relogin_enabled"`
}

func (r accountTokenGuardV2SaveRequest) input() service.AccountTokenGuardV2AccountInput {
	enabled := true
	autoRelogin := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	if r.AutoReloginEnabled != nil {
		autoRelogin = *r.AutoReloginEnabled
	}
	return service.AccountTokenGuardV2AccountInput{
		PreserveEnabled: r.Enabled == nil, PreserveAutoRelogin: r.AutoReloginEnabled == nil,
		LoginEmail: r.LoginEmail, CredentialMode: r.CredentialMode,
		Engine:      r.Engine,
		ProxySource: r.ProxySource, ProxyID: r.ProxyID,
		Password: r.Password, TOTPSecret: r.TOTPSecret, OTPURL: r.OTPURL,
		ClearPassword: r.ClearPassword, ClearTOTP: r.ClearTOTP,
		Enabled: enabled, AutoReloginEnabled: autoRelogin,
	}
}

func parseTokenGuardV2AccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return id, true
}

func (h *AccountTokenGuardV2Handler) List(c *gin.Context) {
	rules, err := h.service.GetRules(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	accounts, err := h.service.ListAccounts(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	runtimeSettings, err := h.service.GetRuntimeSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"auto_enroll_enabled":         rules.AutoEnrollEnabled,
		"relogin_backoff_max_seconds": rules.ReloginBackoffMaxSeconds,
		"relogin_max_failures":        rules.ReloginMaxFailures,
		"runtime_settings":            runtimeSettings,
		"accounts":                    accounts,
		"worker":                      h.service.WorkerStatus(),
		"probe_interval_seconds":      rules.ProbeIntervalSeconds,
		"retry_interval_seconds":      rules.RetryIntervalSeconds,
		"relogin_cooldown_seconds":    rules.ReloginCooldownSeconds,
		"fail_streak_threshold":       rules.FailStreakThreshold,
	})
}

func (h *AccountTokenGuardV2Handler) SaveRuntime(c *gin.Context) {
	var cfg service.OpenAIOAuthReauthRuntimeSettings
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "Invalid re-login settings")
		return
	}
	saved, err := h.service.SaveRuntimeSettings(c.Request.Context(), cfg)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}

func (h *AccountTokenGuardV2Handler) UpdateSwitches(c *gin.Context) {
	id, ok := parseTokenGuardV2AccountID(c)
	if !ok {
		return
	}
	var req struct {
		Enabled            *bool `json:"enabled"`
		AutoReloginEnabled *bool `json:"auto_relogin_enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid automation switches")
		return
	}
	saved, err := h.service.UpdateSwitches(c.Request.Context(), id, req.Enabled, req.AutoReloginEnabled)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}

func (h *AccountTokenGuardV2Handler) SaveRules(c *gin.Context) {
	var req struct {
		service.AccountTokenGuardV2Rules
		AutoEnrollEnabled *bool `json:"auto_enroll_enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	rules := req.AccountTokenGuardV2Rules
	if req.AutoEnrollEnabled != nil {
		rules.AutoEnrollEnabled = *req.AutoEnrollEnabled
	}
	// Old clients omit the two new fields. Preserve a previously configured
	// budget; initial old settings still receive the documented safe defaults.
	if req.AutoEnrollEnabled == nil || rules.ReloginMaxFailures == 0 || rules.ReloginBackoffMaxSeconds == 0 {
		current, err := h.service.GetRules(c.Request.Context())
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if rules.ReloginMaxFailures == 0 {
			rules.ReloginMaxFailures = current.ReloginMaxFailures
		}
		if req.AutoEnrollEnabled == nil {
			rules.AutoEnrollEnabled = current.AutoEnrollEnabled
		}
		if rules.ReloginBackoffMaxSeconds == 0 {
			rules.ReloginBackoffMaxSeconds = max(current.ReloginBackoffMaxSeconds, rules.ReloginCooldownSeconds)
		}
	}
	saved, err := h.service.SaveRules(c.Request.Context(), rules)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}

func (h *AccountTokenGuardV2Handler) BatchCreate(c *gin.Context) {
	if !tokenGuardV2EnrollmentAdmin(c) {
		return
	}
	// Bound the encoded body as well as the decoded 2 MiB credential payload.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 3<<20)
	var req service.AccountTokenGuardV2BatchInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid credential batch")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	result, err := h.service.BatchAddAccounts(ctx, &req)
	req.Accounts = ""
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AccountTokenGuardV2Handler) Sync(c *gin.Context) {
	if !tokenGuardV2EnrollmentAdmin(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var req struct {
		AfterID int64 `json:"after_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) || req.AfterID < 0 {
		response.BadRequest(c, "Invalid enrollment cursor")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	result, err := h.service.SyncAccountsAfter(ctx, req.AfterID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func tokenGuardV2EnrollmentAdmin(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "请先登录")
		return false
	}
	role, ok := middleware.GetUserRoleFromContext(c)
	if !ok || role != service.RoleAdmin || adminaccess.FromContext(c.Request.Context()) != nil {
		response.Forbidden(c, "仅完整权限管理员可导入或复制登录凭证")
		return false
	}
	return true
}

func (h *AccountTokenGuardV2Handler) Create(c *gin.Context) {
	var req accountTokenGuardV2SaveRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.AccountID <= 0 {
		response.BadRequest(c, "Invalid request")
		return
	}
	account, err := h.service.SaveAccount(c.Request.Context(), req.AccountID, req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, account)
}

func (h *AccountTokenGuardV2Handler) Update(c *gin.Context) {
	accountID, ok := parseTokenGuardV2AccountID(c)
	if !ok {
		return
	}
	var req accountTokenGuardV2SaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	account, err := h.service.SaveAccount(c.Request.Context(), accountID, req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, account)
}

func (h *AccountTokenGuardV2Handler) Delete(c *gin.Context) {
	accountID, ok := parseTokenGuardV2AccountID(c)
	if !ok {
		return
	}
	if err := h.service.RemoveAccount(c.Request.Context(), accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *AccountTokenGuardV2Handler) Probe(c *gin.Context) {
	accountID, ok := parseTokenGuardV2AccountID(c)
	if !ok {
		return
	}
	account, err := h.service.ProbeNow(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, account)
}

func (h *AccountTokenGuardV2Handler) Relogin(c *gin.Context) {
	accountID, ok := parseTokenGuardV2AccountID(c)
	if !ok {
		return
	}
	task, err := h.service.ReloginNow(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Accepted(c, task)
}

func (h *AccountTokenGuardV2Handler) ResumeReloginBudget(c *gin.Context) {
	id, ok := parseTokenGuardV2AccountID(c)
	if !ok {
		return
	}
	var input struct {
		Confirm bool `json:"confirm"`
	}
	if c.ShouldBindJSON(&input) != nil || !input.Confirm {
		response.BadRequest(c, "Explicit confirmation is required to restore the automatic re-login budget")
		return
	}
	account, err := h.service.ResumeReloginBudget(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, account)
}

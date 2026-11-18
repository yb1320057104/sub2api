package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type KiloHandler struct {
	service *service.KiloService
	egress  service.KiloEgressManager
}

func NewKiloHandler(s *service.KiloService, egress service.KiloEgressManager) *KiloHandler {
	return &KiloHandler{service: s, egress: egress}
}
func kiloHTTPError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, service.ErrKiloProxyOwned):
		response.Error(c, http.StatusConflict, "该代理仍由其他账号占用，请等待原账号排空释放或选择其他代理")
	case errors.Is(err, service.ErrKiloEgressConflict):
		response.Error(c, http.StatusConflict, "该真实出口IP已绑定另一个账号，请选择其他出口")
	case errors.Is(err, service.ErrKiloEgressDraining):
		response.Error(c, http.StatusConflict, "出口正在排空，请等待在途请求结束后释放或重新验证")
	case errors.Is(err, service.ErrKiloEgressCapacity):
		response.Error(c, http.StatusTooManyRequests, "出口并发已满，请稍后重试")
	case errors.Is(err, service.ErrKiloEgressUnstable):
		response.BadRequest(c, "出口IP在两次探测间变化，请使用稳定出口代理")
	case errors.Is(err, service.ErrKiloEgressUnavailable):
		response.Error(c, http.StatusServiceUnavailable, "出口未绑定或未验证，已阻止请求发送")
	default:
		return authorizationError(c, err)
	}
	return true
}
func (h *KiloHandler) Overview(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	value, err := h.service.Overview(ctx)
	if !kiloHTTPError(c, err) {
		response.Success(c, value)
	}
}
func (h *KiloHandler) Import(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	var input service.KiloPreparedImportInput
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "kilo.prepared.import")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	value, err := h.service.ImportPrepared(ctx, input)
	for i := range input.Items {
		input.Items[i].APIKey = ""
	}
	if !kiloHTTPError(c, err) {
		response.Success(c, value)
	}
}
func (h *KiloHandler) Submit(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	var input service.KiloBatchInput
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "kilo.batch.submit")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	id, err := h.service.SubmitBatch(ctx, input)
	if !kiloHTTPError(c, err) {
		response.Success(c, gin.H{"batch_id": id})
	}
}

const kiloOnboardingImportBodyKey = "kilo_onboarding_import_body"

// KiloOnboardingImportPrivacy runs before audit capture: only the import handler
// may read this credential-bearing stream. Denied and malformed requests are
// audited without copying their body into the audit entry.
func KiloOnboardingImportPrivacy() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost || !strings.HasSuffix(c.FullPath(), "/admin/kilo/onboarding/import") {
			c.Next()
			return
		}
		middleware.SetAuditAction(c, "kilo.onboarding.import")
		body := c.Request.Body
		c.Set(kiloOnboardingImportBodyKey, body)
		c.Request.Body = http.NoBody
		defer func() {
			c.Request.Body = http.NoBody
			c.Set(kiloOnboardingImportBodyKey, nil)
			if body != nil {
				_ = body.Close()
			}
		}()
		c.Next()
	}
}

func (h *KiloHandler) ImportOnboarding(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	middleware.SetAuditAction(c, "kilo.onboarding.import")
	if value, ok := c.Get(kiloOnboardingImportBodyKey); ok {
		if body, ok := value.(io.ReadCloser); ok {
			c.Request.Body = body
		}
		c.Set(kiloOnboardingImportBodyKey, nil)
	}
	defer func() { c.Request.Body = http.NoBody }()
	var input service.KiloPoolImportInput
	defer input.ClearSecrets()
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"requested_count": len(input.Items), "group_count": len(input.GroupIDs)})
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	result, err := h.service.ImportOnboarding(ctx, input)
	if !kiloHTTPError(c, err) {
		response.Success(c, result)
	}
}

func (h *KiloHandler) SubmitOnboarding(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	var input service.KiloOnboardingBatchInput
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "kilo.onboarding.submit")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	result, err := h.service.SubmitOnboardingBatch(ctx, input)
	if !kiloHTTPError(c, err) {
		response.Success(c, result)
	}
}
func (h *KiloHandler) OnboardingBatch(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	result, err := h.service.OnboardingBatch(ctx, c.Param("batch_id"))
	if !kiloHTTPError(c, err) {
		response.Success(c, result)
	}
}
func (h *KiloHandler) RefreshIdentityPolicy(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "kilo.identity.refresh")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 55*time.Second)
	defer cancel()
	result, err := h.service.RefreshIdentityPolicy(ctx, id)
	if !kiloHTTPError(c, err) {
		response.Success(c, result)
	}
}

func (h *KiloHandler) RefreshModels(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Kilo账号编号无效")
		return
	}
	middleware.SetAuditAction(c, "kilo.models.refresh")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 55*time.Second)
	defer cancel()
	result, err := h.service.RefreshModels(ctx, id)
	if !kiloHTTPError(c, err) {
		response.Success(c, result)
	}
}

func (h *KiloHandler) Candidates(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	items, err := h.service.Candidates(c.Request.Context())
	if !kiloHTTPError(c, err) {
		response.Success(c, gin.H{"items": items})
	}
}
func (h *KiloHandler) Groups(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	items, err := h.service.Groups(c.Request.Context())
	if !kiloHTTPError(c, err) {
		rows := make([]gin.H, 0, len(items))
		for _, item := range items {
			rows = append(rows, gin.H{"id": item.ID, "name": item.Name})
		}
		response.Success(c, gin.H{"items": rows})
	}
}
func (h *KiloHandler) Proxies(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	items, err := h.service.ActiveProxies(c.Request.Context())
	if !kiloHTTPError(c, err) {
		response.Success(c, gin.H{"items": items})
	}
}
func (h *KiloHandler) PreflightProxy(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "kilo.proxy.preflight")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	result, err := h.service.PreflightLoginProxy(ctx, id)
	if !kiloHTTPError(c, err) {
		response.Success(c, result)
	}
}

func (h *KiloHandler) DeleteFailedJob(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "kilo.job.delete")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 55*time.Second)
	defer cancel()
	result, err := h.service.DeleteFailedJob(ctx, id)
	if !kiloHTTPError(c, err) {
		response.Success(c, result)
	}
}

func (h *KiloHandler) DeleteFailedPrepared(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "kilo.prepared.delete")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 55*time.Second)
	defer cancel()
	result, err := h.service.DeleteFailedPrepared(ctx, id)
	if !kiloHTTPError(c, err) {
		response.Success(c, result)
	}
}

func (h *KiloHandler) SetFailedJobLoginProxy(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	var input struct {
		LoginProxyID int64 `json:"login_proxy_id"`
	}
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "kilo.job.login_proxy.update")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 55*time.Second)
	defer cancel()
	if !kiloHTTPError(c, h.service.SetFailedJobLoginProxy(ctx, id, input.LoginProxyID)) {
		response.Success(c, gin.H{"status": "saved", "job_id": id, "login_proxy_id": input.LoginProxyID})
	}
}

func (h *KiloHandler) ProxyGroups(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	items, err := h.service.ProxyGroups(c.Request.Context())
	if !kiloHTTPError(c, err) {
		response.Success(c, gin.H{"items": items})
	}
}
func (h *KiloHandler) DeviceStart(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	var input service.KiloDeviceInput
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "kilo.device.start")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	value, err := h.service.StartDevice(ctx, input)
	if !kiloHTTPError(c, err) {
		response.Success(c, value)
	}
}
func (h *KiloHandler) DevicePoll(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	value, err := h.service.PollDevice(ctx, c.Param("session"))
	if !kiloHTTPError(c, err) {
		response.Success(c, value)
	}
}
func (h *KiloHandler) DeviceCancel(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	middleware.SetAuditAction(c, "kilo.device.cancel")
	if !kiloHTTPError(c, h.service.CancelDevice(c.Request.Context(), c.Param("session"))) {
		response.Success(c, gin.H{"status": "cancelled"})
	}
}
func (h *KiloHandler) Organizations(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	items, err := h.service.Organizations(ctx, id)
	if !kiloHTTPError(c, err) {
		response.Success(c, gin.H{"items": items})
	}
}
func (h *KiloHandler) SetOrganization(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	var input struct {
		OrganizationID string `json:"organization_id"`
	}
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "kilo.organization.change")
	if !kiloHTTPError(c, h.service.SetOrganization(c.Request.Context(), id, input.OrganizationID)) {
		response.Success(c, gin.H{"status": "saved"})
	}
}
func (h *KiloHandler) SetRouting(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	var input service.KiloRoutingInput
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "kilo.routing.update")
	if !kiloHTTPError(c, h.service.SetRouting(c.Request.Context(), id, input)) {
		response.Success(c, gin.H{"status": "saved"})
	}
}

func (h *KiloHandler) UnbindPrepared(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	var input struct {
		RequestID string `json:"request_id"`
	}
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "kilo.prepared.unbind")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if !kiloHTTPError(c, h.service.UnbindPrepared(ctx, id, input.RequestID)) {
		response.Success(c, gin.H{"status": "queued"})
	}
}

func (h *KiloHandler) ReleasePrepared(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "kilo.prepared.release")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	if !kiloHTTPError(c, h.service.ReleasePrepared(ctx, id)) {
		response.Success(c, gin.H{"status": "released"})
	}
}
func (h *KiloHandler) Retry(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "kilo.job.retry")
	if !kiloHTTPError(c, h.service.RetryJob(c.Request.Context(), id)) {
		response.Success(c, gin.H{"status": "queued"})
	}
}
func (h *KiloHandler) ReconcileSubscription(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	var input struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !authorizationInput(c, &input) {
		return
	}
	if input.ExpectedRevision <= 0 {
		response.BadRequest(c, "必须提供大于零的 expected_revision")
		return
	}
	middleware.SetAuditAction(c, "kilo.job.reconcile_subscription")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if !kiloHTTPError(c, h.service.ReconcileSubscription(ctx, id, input.ExpectedRevision)) {
		response.Success(c, gin.H{"status": "queued"})
	}
}

func (h *KiloHandler) ReauthorizeSubscription(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	var input struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !authorizationInput(c, &input) {
		return
	}
	if input.ExpectedRevision <= 0 {
		response.BadRequest(c, "必须提供大于零的 expected_revision")
		return
	}
	middleware.SetAuditAction(c, "kilo.job.reauthorize_subscription")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if !kiloHTTPError(c, h.service.ReauthorizeSubscription(ctx, id, input.ExpectedRevision)) {
		response.Success(c, gin.H{"status": "queued"})
	}
}

func (h *KiloHandler) Cancel(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "kilo.job.cancel")
	if !kiloHTTPError(c, h.service.CancelJob(c.Request.Context(), id)) {
		response.Success(c, gin.H{"status": "cancelled"})
	}
}
func kiloAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("account_id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "账号编号无效")
		return 0, false
	}
	return id, true
}
func (h *KiloHandler) Egress(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := kiloAccountID(c)
	if !ok {
		return
	}
	items, err := h.egress.List(c.Request.Context(), id)
	if !kiloHTTPError(c, err) {
		response.Success(c, gin.H{"items": items})
	}
}
func (h *KiloHandler) VerifyEgress(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := kiloAccountID(c)
	if !ok {
		return
	}
	var input struct {
		ProxyID int64 `json:"proxy_id"`
	}
	if !authorizationInput(c, &input) {
		return
	}
	middleware.SetAuditAction(c, "kilo.egress.verify")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	value, err := h.service.VerifyRequestEgress(ctx, id, input.ProxyID)
	if !kiloHTTPError(c, err) {
		response.Success(c, value)
	}
}
func (h *KiloHandler) ReleaseEgress(c *gin.Context) {
	if !authorizationAdmin(c) {
		return
	}
	id, ok := authorizationID(c)
	if !ok {
		return
	}
	middleware.SetAuditAction(c, "kilo.egress.release")
	if !kiloHTTPError(c, h.service.ReleaseRequestEgress(c.Request.Context(), id)) {
		response.Success(c, gin.H{"status": "released"})
	}
}

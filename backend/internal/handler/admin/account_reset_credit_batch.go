package admin

import (
	"context"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
)

const resetCreditBatchLimit = 20

type openAIResetCreditBatchReader interface {
	QueryUsage(context.Context, int64) (*service.OpenAIQuotaUsage, error)
	CacheResetCreditsSnapshot(context.Context, int64, *service.OpenAIRateLimitResetCredits) error
	CacheCreditsSnapshot(context.Context, int64, *service.OpenAIQuotaUsage) error
}

type resetCreditBatchResult struct {
	AccountID     int64                       `json:"account_id"`
	AccountName   string                      `json:"account_name"`
	Platform      string                      `json:"platform"`
	Status        string                      `json:"status"`
	Available     *int                        `json:"available_count"`
	Total         *int                        `json:"total_count"`
	ErrorCode     string                      `json:"error_code,omitempty"`
	WarningCode   string                      `json:"warning_code,omitempty"`
	OpenAIQuota   *openAIQuotaRefreshResponse `json:"openai_quota,omitempty"`
	ClaudeCredits *service.ClaudeResetCredits `json:"claude_credits,omitempty"`
}

func (h *AccountHandler) SetResetCreditQuotaService(quota *service.OpenAIQuotaService) {
	if quota != nil {
		h.resetCreditQuota = quota
	}
}

// BatchQueryResetCredits queries the existing upstream credit readers only. It
// deliberately does not call redemption or wake the automatic redemption queue.
// POST /api/v1/admin/accounts/reset-credits/batch
func (h *AccountHandler) BatchQueryResetCredits(c *gin.Context) {
	var input struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	if c.ShouldBindJSON(&input) != nil || len(input.AccountIDs) == 0 || len(input.AccountIDs) > 200 {
		response.BadRequest(c, "account_ids must contain 1 to 20 account IDs")
		return
	}
	for _, id := range input.AccountIDs {
		if id <= 0 {
			response.BadRequest(c, "Invalid account ID")
			return
		}
	}
	ids := normalizeInt64IDList(input.AccountIDs)
	if len(ids) > resetCreditBatchLimit {
		response.BadRequest(c, "At most 20 accounts can be queried per batch")
		return
	}
	middleware.SetAuditAction(c, "accounts.reset_credits.query_batch")
	middleware.SetAuditExtra(c, map[string]any{"account_ids": ids})
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	accounts, err := h.adminService.GetAccountsByIDs(ctx, ids)
	if response.ErrorFrom(c, err) {
		return
	}
	byID := make(map[int64]*service.Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	results := make([]resetCreditBatchResult, len(ids))
	workers, workerCtx := errgroup.WithContext(ctx)
	workers.SetLimit(3)
	for index, id := range ids {
		index, id := index, id
		workers.Go(func() error {
			results[index] = h.queryResetCreditBatchAccount(workerCtx, id, byID[id])
			return nil
		})
	}
	if err := workers.Wait(); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	success, failed, skipped := 0, 0, 0
	for _, item := range results {
		switch item.Status {
		case "success":
			success++
		case "skipped":
			skipped++
		default:
			failed++
		}
	}
	response.Success(c, gin.H{"total": len(ids), "success": success, "failed": failed, "skipped": skipped, "results": results})
}

func (h *AccountHandler) queryResetCreditBatchAccount(ctx context.Context, id int64, account *service.Account) resetCreditBatchResult {
	item := resetCreditBatchResult{AccountID: id, Status: "failed"}
	if account == nil {
		item.ErrorCode = "ACCOUNT_NOT_FOUND"
		return item
	}
	item.AccountName, item.Platform = account.Name, account.Platform
	if account.Type != service.AccountTypeOAuth || (account.Platform != service.PlatformOpenAI && account.Platform != service.PlatformAnthropic) {
		item.Status, item.ErrorCode = "skipped", "RESET_CREDITS_UNSUPPORTED"
		return item
	}
	queryCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	if err := queryCtx.Err(); err != nil {
		item.ErrorCode = "RESET_CREDITS_QUERY_CANCELLED"
		return item
	}
	var err error
	if account.Platform == service.PlatformOpenAI {
		if h.resetCreditQuota == nil {
			item.ErrorCode = "RESET_CREDITS_UNAVAILABLE"
			return item
		}
		var usage *service.OpenAIQuotaUsage
		usage, err = h.resetCreditQuota.QueryUsage(queryCtx, id)
		if err == nil && usage != nil {
			quota := &openAIQuotaRefreshResponse{OpenAIQuotaUsage: *usage}
			quota.CreditsCachePersisted = h.resetCreditQuota.CacheCreditsSnapshot(queryCtx, id, usage) == nil
			quota.CachePersisted = h.resetCreditQuota.CacheResetCreditsSnapshot(queryCtx, id, usage.RateLimitResetCredits) == nil
			item.OpenAIQuota = quota
			if !quota.CachePersisted || !quota.CreditsCachePersisted {
				item.WarningCode = "RESET_CREDITS_CACHE_NOT_SAVED"
			}
			if credits := usage.RateLimitResetCredits; credits != nil {
				count := credits.AvailableCount
				item.Available, item.Total = &count, &count
			}
		}
	} else {
		if h.claudeResetCredits == nil {
			item.ErrorCode = "RESET_CREDITS_UNAVAILABLE"
			return item
		}
		item.ClaudeCredits, err = h.claudeResetCredits.Query(queryCtx, id)
		if credits := item.ClaudeCredits; err == nil && credits != nil {
			available, total := credits.AvailableCount, 0
			for _, credit := range credits.Credits {
				total += credit.ResetsLeft
			}
			item.Available, item.Total = &available, &total
		}
	}
	if err != nil {
		// Never return network URLs, upstream bodies, or credential material in
		// the batch summary. Structured reason codes suffice for per-row help.
		item.ErrorCode = infraerrors.Reason(err)
		if item.ErrorCode == "" || infraerrors.Code(err) == http.StatusInternalServerError {
			item.ErrorCode = "RESET_CREDITS_QUERY_FAILED"
		}
		return item
	}
	if item.OpenAIQuota == nil && item.ClaudeCredits == nil {
		item.ErrorCode = "RESET_CREDITS_EMPTY_RESULT"
		return item
	}
	item.Status = "success"
	return item
}

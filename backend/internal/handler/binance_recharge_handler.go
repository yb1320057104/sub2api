package handler

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type BinanceRechargeHandler struct {
	service *service.BinanceRechargeService
}

func NewBinanceRechargeHandler(s *service.BinanceRechargeService) *BinanceRechargeHandler {
	return &BinanceRechargeHandler{service: s}
}

// Zero is reserved for administrator scope in the service API, so a malformed
// authentication context must never reach user-facing queries with that value.
func requireBinanceRechargeAuth(c *gin.Context) (middleware.AuthSubject, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return middleware.AuthSubject{}, false
	}
	return subject, true
}

func (h *BinanceRechargeHandler) Config(c *gin.Context) {
	if _, ok := requireBinanceRechargeAuth(c); !ok {
		return
	}
	result, err := h.service.Config(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

func (h *BinanceRechargeHandler) CreateOrder(c *gin.Context) {
	subject, ok := requireBinanceRechargeAuth(c)
	if !ok {
		return
	}
	var input struct {
		AccountID int64 `json:"account_id"`
		Amount    int64 `json:"amount"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "充值金额必须为整数，并选择收款账户")
		return
	}
	result, err := h.service.CreateOrder(c.Request.Context(), subject.UserID, input.AccountID, input.Amount)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

func binanceRechargeID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效的记录 ID")
		return 0, false
	}
	return id, true
}

func binanceRechargeListParams(c *gin.Context, userID int64) (service.BinanceRechargeListParams, bool) {
	page, size := response.ParsePagination(c)
	if size > 100 {
		size = 100
	}
	p := service.BinanceRechargeListParams{UserID: userID, Page: page, PageSize: size, Status: c.Query("status"), Keyword: c.Query("keyword")}
	if raw := c.Query("account_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "无效的收款账户 ID")
			return p, false
		}
		p.AccountID = id
	}
	return p, true
}

func (h *BinanceRechargeHandler) listOrders(c *gin.Context, userID int64) {
	p, ok := binanceRechargeListParams(c, userID)
	if !ok {
		return
	}
	items, total, err := h.service.ListOrders(c.Request.Context(), p)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Paginated(c, items, total, p.Page, p.PageSize)
}

func (h *BinanceRechargeHandler) ListOrders(c *gin.Context) {
	subject, ok := requireBinanceRechargeAuth(c)
	if !ok {
		return
	}
	h.listOrders(c, subject.UserID)
}

func (h *BinanceRechargeHandler) AdminListOrders(c *gin.Context) { h.listOrders(c, 0) }

func (h *BinanceRechargeHandler) getOrder(c *gin.Context, userID int64, refresh bool) {
	id, ok := binanceRechargeID(c)
	if !ok {
		return
	}
	var result *service.BinanceRechargeOrder
	var err error
	if refresh {
		result, err = h.service.CheckOrder(c.Request.Context(), id, userID)
	} else {
		result, err = h.service.GetOrder(c.Request.Context(), id, userID)
	}
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

func (h *BinanceRechargeHandler) GetOrder(c *gin.Context) {
	subject, ok := requireBinanceRechargeAuth(c)
	if !ok {
		return
	}
	h.getOrder(c, subject.UserID, false)
}

func (h *BinanceRechargeHandler) RefreshOrder(c *gin.Context) {
	subject, ok := requireBinanceRechargeAuth(c)
	if !ok {
		return
	}
	h.getOrder(c, subject.UserID, true)
}

func (h *BinanceRechargeHandler) AdminGetOrder(c *gin.Context)     { h.getOrder(c, 0, false) }
func (h *BinanceRechargeHandler) AdminRefreshOrder(c *gin.Context) { h.getOrder(c, 0, true) }

func (h *BinanceRechargeHandler) stats(c *gin.Context, userID int64) {
	p, ok := binanceRechargeListParams(c, userID)
	if !ok {
		return
	}
	result, err := h.service.Stats(c.Request.Context(), p)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

func (h *BinanceRechargeHandler) Stats(c *gin.Context) {
	subject, ok := requireBinanceRechargeAuth(c)
	if !ok {
		return
	}
	h.stats(c, subject.UserID)
}

func (h *BinanceRechargeHandler) AdminStats(c *gin.Context) { h.stats(c, 0) }

func (h *BinanceRechargeHandler) ListAccounts(c *gin.Context) {
	result, err := h.service.ListAccounts(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

func (h *BinanceRechargeHandler) saveAccount(c *gin.Context, id int64) {
	var input service.BinanceRechargeAccountInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "无效的收款账户配置")
		return
	}
	result, err := h.service.SaveAccount(c.Request.Context(), id, input)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

func (h *BinanceRechargeHandler) CreateAccount(c *gin.Context) { h.saveAccount(c, 0) }

func (h *BinanceRechargeHandler) UpdateAccount(c *gin.Context) {
	id, ok := binanceRechargeID(c)
	if !ok {
		return
	}
	h.saveAccount(c, id)
}

func (h *BinanceRechargeHandler) CheckAccount(c *gin.Context) {
	id, ok := binanceRechargeID(c)
	if !ok {
		return
	}
	result, err := h.service.CheckAccount(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

func (h *BinanceRechargeHandler) Bills(c *gin.Context) {
	id, ok := binanceRechargeID(c)
	if !ok {
		return
	}
	from, to, err := service.BinanceRechargeBillWindow(c.Query("start_time"), c.Query("end_time"), time.Now())
	if response.ErrorFrom(c, err) {
		return
	}
	result, err := h.service.Bills(c.Request.Context(), id, from, to)
	if response.ErrorFrom(c, err) {
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, result)
}

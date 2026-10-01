package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const binanceOrderColumns = `o.id,o.order_no,o.user_id,u.email,o.account_id,o.account_name,o.recipient_uid,
o.requested_amount,o.amount_units,o.credited_units,o.status,o.transaction_id,o.review_reason,
o.created_at,o.expires_at,o.paid_at,o.completed_at`

func scanBinanceRechargeOrder(row binanceRowScanner) (*BinanceRechargeOrder, error) {
	var o BinanceRechargeOrder
	err := row.Scan(&o.ID, &o.OrderNo, &o.UserID, &o.UserEmail, &o.AccountID, &o.AccountName, &o.RecipientUID,
		&o.RequestedAmount, &o.amountUnits, &o.creditedUnits, &o.Status, &o.TransactionID, &o.ReviewReason,
		&o.CreatedAt, &o.ExpiresAt, &o.PaidAt, &o.CompletedAt)
	o.PayAmount = binancepay.FormatAmount(o.amountUnits)
	o.CreditedAmount = binancepay.FormatAmount(o.creditedUnits)
	// Expiry is also projected at read time, even if the poller is inactive.
	if o.Status == BinanceRechargePending && time.Now().After(o.ExpiresAt) {
		o.Status = BinanceRechargeExpired
	}
	return &o, err
}

func (s *BinanceRechargeService) GetOrder(ctx context.Context, orderID, userID int64) (*BinanceRechargeOrder, error) {
	o, err := scanBinanceRechargeOrder(s.db.QueryRowContext(ctx, `SELECT `+binanceOrderColumns+` FROM binance_recharge_orders o JOIN users u ON u.id=o.user_id WHERE o.id=$1 AND ($2::bigint=0 OR o.user_id=$2)`, orderID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("BINANCE_ORDER_NOT_FOUND", "充值订单不存在")
	}
	if err != nil {
		return nil, err
	}
	if userID > 0 {
		o.UserEmail = ""
	}
	return o, nil
}

func (s *BinanceRechargeService) CreateOrder(ctx context.Context, userID, accountID, amount int64) (*BinanceRechargeOrder, error) {
	if userID <= 0 || accountID <= 0 || amount < 1 || amount > 1000000 {
		return nil, infraerrors.BadRequest("BINANCE_AMOUNT_INVALID", "请输入 1 至 1000000 的整数充值金额，并选择收款账户")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Account first: receipt inserts acquire an account FK key-share lock before
	// crediting a user. A shared account-before-user ordering avoids deadlocks.
	a, err := scanBinanceRechargeAccount(tx.QueryRowContext(ctx, `SELECT `+binanceAccountColumns+` FROM binance_recharge_accounts a WHERE a.id=$1 FOR UPDATE OF a`, accountID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("BINANCE_ACCOUNT_NOT_FOUND", "收款账户不存在")
	}
	if err != nil {
		return nil, err
	}
	if !a.Enabled || !a.APIKeyConfigured || !a.APISecretConfigured {
		return nil, infraerrors.Forbidden("BINANCE_ACCOUNT_DISABLED", "该收款账户未启用")
	}
	// All create limits are checked while holding the user row lock. Different
	// API instances cannot bypass the pending/rate limits by creating together.
	var userStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&userStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("USER_NOT_FOUND", "用户不存在")
		}
		return nil, err
	}
	if userStatus != "active" {
		return nil, infraerrors.Forbidden("USER_INACTIVE", "用户账号已禁用")
	}
	var pending, recent, daily int
	if err := tx.QueryRowContext(ctx, `SELECT
COUNT(*) FILTER(WHERE status='PENDING' AND expires_at>NOW()),
COUNT(*) FILTER(WHERE created_at>NOW()-INTERVAL '30 seconds'),
COUNT(*) FILTER(WHERE created_at>NOW()-INTERVAL '24 hours')
FROM binance_recharge_orders WHERE user_id=$1`, userID).Scan(&pending, &recent, &daily); err != nil {
		return nil, err
	}
	if pending >= 3 {
		return nil, infraerrors.TooManyRequests("BINANCE_PENDING_LIMIT", "最多保留 3 个待付款订单，请先完成或等待到期")
	}
	if recent > 0 || daily >= 20 {
		return nil, infraerrors.TooManyRequests("BINANCE_CREATE_LIMIT", "每 30 秒可创建一个订单，每 24 小时最多 20 个")
	}
	seed, err := rand.Int(rand.Reader, big.NewInt(99999))
	if err != nil {
		return nil, err
	}
	var units int64
	// Account row locking plus the lifetime unique constraint protects allocation.
	// Previously observed unmatched deposits are also excluded from allocation.
	err = tx.QueryRowContext(ctx, `SELECT $1::bigint*100000+((n+$2::bigint)%99999)+1 AS candidate
FROM generate_series(0,99998) n
WHERE NOT EXISTS(SELECT 1 FROM binance_recharge_orders o WHERE o.recipient_uid=$3 AND o.amount_units=$1::bigint*100000+((n+$2::bigint)%99999)+1)
AND NOT EXISTS(SELECT 1 FROM binance_recharge_receipts r WHERE r.recipient_uid=$3 AND r.amount_units=$1::bigint*100000+((n+$2::bigint)%99999)+1)
LIMIT 1`, amount, seed.Int64(), a.RecipientUID).Scan(&units)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.Conflict("BINANCE_AMOUNT_EXHAUSTED", "该整数金额的识别尾数已用尽，请更换整数金额或收款账户")
	}
	if err != nil {
		return nil, err
	}
	var orderID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO binance_recharge_orders
(order_no,user_id,account_id,account_name,recipient_uid,requested_amount,amount_units,created_at,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7,date_trunc('milliseconds',clock_timestamp()),date_trunc('milliseconds',clock_timestamp())+make_interval(mins=>$8)) RETURNING id`,
		"BN-"+strings.ReplaceAll(uuid.NewString(), "-", ""), userID, accountID, a.Name, a.RecipientUID, amount, units, a.OrderTTLMinutes).Scan(&orderID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetOrder(ctx, orderID, userID)
}

func normalizeBinanceListParams(p BinanceRechargeListParams) BinanceRechargeListParams {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 20
	}
	if p.PageSize > 100 {
		p.PageSize = 100
	}
	p.Keyword = strings.TrimSpace(p.Keyword)
	if len(p.Keyword) > 200 {
		p.Keyword = p.Keyword[:200]
	}
	return p
}

func (s *BinanceRechargeService) ListOrders(ctx context.Context, p BinanceRechargeListParams) ([]BinanceRechargeOrder, int64, error) {
	p = normalizeBinanceListParams(p)
	if p.Status != "" && p.Status != BinanceRechargePending && p.Status != BinanceRechargeCompleted && p.Status != BinanceRechargeExpired && p.Status != BinanceRechargeReview {
		return nil, 0, infraerrors.BadRequest("BINANCE_STATUS_INVALID", "订单状态无效")
	}
	where := ` FROM binance_recharge_orders o JOIN users u ON u.id=o.user_id
WHERE ($1::bigint=0 OR o.user_id=$1) AND ($2::bigint=0 OR o.account_id=$2)
AND ($3::text='' OR (CASE WHEN o.status='PENDING' AND o.expires_at<NOW() THEN 'EXPIRED' ELSE o.status END)=$3)
AND ($4::text='' OR position(lower($4) in lower(o.order_no || ' ' || u.email || ' ' || o.transaction_id))>0)`
	args := []any{p.UserID, p.AccountID, p.Status, p.Keyword}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, p.PageSize, (p.Page-1)*p.PageSize)
	rows, err := s.db.QueryContext(ctx, `SELECT `+binanceOrderColumns+where+` ORDER BY o.created_at DESC,o.id DESC LIMIT $5 OFFSET $6`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]BinanceRechargeOrder, 0)
	for rows.Next() {
		o, err := scanBinanceRechargeOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		if p.UserID > 0 {
			o.UserEmail = ""
		}
		items = append(items, *o)
	}
	return items, total, rows.Err()
}

type BinanceRechargeUserStats struct {
	UserID      int64  `json:"user_id"`
	UserEmail   string `json:"user_email,omitempty"`
	TotalAmount string `json:"total_amount"`
	TotalOrders int64  `json:"total_orders"`
}

type BinanceRechargeStats struct {
	TotalAmount    string                         `json:"total_amount"`
	TotalOrders    int64                          `json:"total_orders"`
	Items          []BinanceRechargeUserStats     `json:"items"`
	Total          int64                          `json:"total"`
	Page           int                            `json:"page"`
	PageSize       int                            `json:"page_size"`
	Pages          int                            `json:"pages"`
	AccountOptions []BinanceRechargePublicAccount `json:"account_options"`
}

func (s *BinanceRechargeService) Stats(ctx context.Context, p BinanceRechargeListParams) (*BinanceRechargeStats, error) {
	p = normalizeBinanceListParams(p)
	// Format NUMERIC sums in SQL; summing many users must not overflow int64.
	where := ` FROM binance_recharge_orders o JOIN users u ON u.id=o.user_id WHERE o.status='COMPLETED' AND ($1::bigint=0 OR o.user_id=$1) AND ($2::bigint=0 OR o.account_id=$2)`
	amountSQL := `TRIM(TO_CHAR(COALESCE(SUM(o.credited_units),0)/100000::numeric,'FM999999999999999999999999999999999990.00000'))`
	r := &BinanceRechargeStats{Items: make([]BinanceRechargeUserStats, 0), AccountOptions: make([]BinanceRechargePublicAccount, 0), Page: p.Page, PageSize: p.PageSize}
	if err := s.db.QueryRowContext(ctx, `SELECT `+amountSQL+`,COUNT(*),COUNT(DISTINCT o.user_id)`+where, p.UserID, p.AccountID).Scan(&r.TotalAmount, &r.TotalOrders, &r.Total); err != nil {
		return nil, err
	}
	r.Pages = int((r.Total + int64(p.PageSize) - 1) / int64(p.PageSize))
	rows, err := s.db.QueryContext(ctx, `SELECT o.user_id,u.email,`+amountSQL+`,COUNT(*)`+where+` GROUP BY o.user_id,u.email ORDER BY SUM(o.credited_units) DESC,o.user_id LIMIT $3 OFFSET $4`, p.UserID, p.AccountID, p.PageSize, (p.Page-1)*p.PageSize)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item BinanceRechargeUserStats
		if err := rows.Scan(&item.UserID, &item.UserEmail, &item.TotalAmount, &item.TotalOrders); err != nil {
			return nil, err
		}
		if p.UserID > 0 {
			item.UserEmail = ""
		}
		r.Items = append(r.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	options, err := s.db.QueryContext(ctx, `SELECT a.id,a.name,a.recipient_uid,a.order_ttl_minutes FROM binance_recharge_accounts a WHERE EXISTS(SELECT 1 FROM binance_recharge_orders o WHERE o.account_id=a.id AND o.status='COMPLETED' AND ($1::bigint=0 OR o.user_id=$1)) ORDER BY a.id`, p.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = options.Close() }()
	for options.Next() {
		var a BinanceRechargePublicAccount
		if err := options.Scan(&a.ID, &a.Name, &a.RecipientUID, &a.OrderTTLMinutes); err != nil {
			return nil, err
		}
		r.AccountOptions = append(r.AccountOptions, a)
	}
	return r, options.Err()
}

// acceptReceipt is one PostgreSQL transaction: receipt claim, balance update,
// balance-history record, and order completion either all commit or all roll back.
// No caller-controlled amount or payment status can invoke this through HTTP.
func (s *BinanceRechargeService) acceptReceipt(ctx context.Context, a *BinanceRechargeAccount, r binancepay.Receipt) (matched, review bool, err error) {
	if r.TransactionID == "" || len(r.TransactionID) > 160 || r.AmountUnits <= 0 || r.Time.IsZero() {
		return false, false, errors.New("invalid authenticated Binance receipt")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var receiptID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO binance_recharge_receipts(account_id,recipient_uid,transaction_id,amount_units,paid_at,status)
VALUES($1,$2,$3,$4,$5,'REVIEW') ON CONFLICT(recipient_uid,transaction_id) DO NOTHING RETURNING id`, a.ID, a.RecipientUID, r.TransactionID, r.AmountUnits, r.Time).Scan(&receiptID)
	if errors.Is(err, sql.ErrNoRows) {
		var previousUnits int64
		var previousPaidAt time.Time
		if err := tx.QueryRowContext(ctx, `SELECT amount_units,paid_at FROM binance_recharge_receipts WHERE recipient_uid=$1 AND transaction_id=$2`, a.RecipientUID, r.TransactionID).Scan(&previousUnits, &previousPaidAt); err != nil {
			return false, false, err
		}
		if previousUnits != r.AmountUnits || !previousPaidAt.Equal(r.Time) {
			return false, false, errors.New("conflicting authenticated Binance transaction evidence")
		}
		// A previous response may have been lost after committing. Cache invalidation
		// remains safe to repeat; the balance is never incremented a second time.
		var userID int64
		lookupErr := tx.QueryRowContext(ctx, `SELECT o.user_id FROM binance_recharge_receipts r JOIN binance_recharge_orders o ON o.id=r.order_id WHERE r.recipient_uid=$1 AND r.transaction_id=$2 AND r.status='COMPLETED'`, a.RecipientUID, r.TransactionID).Scan(&userID)
		_ = tx.Rollback()
		if lookupErr == nil {
			s.invalidateBalance(ctx, userID)
		}
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	o, lookupErr := scanBinanceRechargeOrder(tx.QueryRowContext(ctx, `SELECT `+binanceOrderColumns+` FROM binance_recharge_orders o JOIN users u ON u.id=o.user_id WHERE o.recipient_uid=$1 AND o.amount_units=$2 FOR UPDATE OF o`, a.RecipientUID, r.AmountUnits))
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return false, false, lookupErr
	}
	if errors.Is(lookupErr, sql.ErrNoRows) {
		o = nil
	}
	// A pre-existing unrelated payment must not poison a subsequently created
	// order. Retain it for review without associating it with that newer order.
	if o != nil && r.Time.Before(o.CreatedAt) {
		o = nil
	}
	reason := binanceRechargeReceiptReviewReason(o, r)
	if reason == "" {
		var active bool
		if err := tx.QueryRowContext(ctx, `SELECT status='active' AND deleted_at IS NULL FROM users WHERE id=$1 FOR UPDATE`, o.UserID).Scan(&active); err != nil {
			return false, false, err
		}
		if !active {
			reason = "用户已禁用或删除，请人工核对"
		}
	}
	if reason != "" {
		var orderID any
		if o != nil {
			orderID = o.ID
		}
		if _, err := tx.ExecContext(ctx, `UPDATE binance_recharge_receipts SET order_id=$2,review_reason=$3 WHERE id=$1`, receiptID, orderID, reason); err != nil {
			return false, false, err
		}
		if o != nil && o.Status != BinanceRechargeCompleted {
			if _, err := tx.ExecContext(ctx, `UPDATE binance_recharge_orders SET status='REVIEW',review_reason=$2,transaction_id=$3,paid_at=$4,updated_at=NOW() WHERE id=$1`, o.ID, reason, r.TransactionID, r.Time); err != nil {
				return false, false, err
			}
		}
		if err := tx.Commit(); err != nil {
			return false, false, err
		}
		return false, true, nil
	}
	amount := binancepay.FormatAmount(r.AmountUnits)
	if _, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance+$2::numeric,total_recharged=total_recharged+$2::numeric,updated_at=NOW() WHERE id=$1`, o.UserID, amount); err != nil {
		return false, false, err
	}
	// Preserve existing user balance history and cumulative recharge queries.
	if _, err := tx.ExecContext(ctx, `INSERT INTO redeem_codes(code,type,value,status,used_by,used_at,notes) VALUES($1,'balance',$2::numeric,'used',$3,NOW(),$4)`, fmt.Sprintf("BN-%d", o.ID), amount, o.UserID, "币安 UID 自助充值 "+o.OrderNo+" / "+r.TransactionID); err != nil {
		return false, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE binance_recharge_orders SET status='COMPLETED',credited_units=amount_units,transaction_id=$2,paid_at=$3,completed_at=NOW(),review_reason='',updated_at=NOW() WHERE id=$1`, o.ID, r.TransactionID, r.Time); err != nil {
		return false, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE binance_recharge_receipts SET status='COMPLETED',order_id=$2,review_reason='' WHERE id=$1`, receiptID, o.ID); err != nil {
		return false, false, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	s.invalidateBalance(ctx, o.UserID)
	return true, false, nil
}

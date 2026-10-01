package service

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	BinanceRechargePending       = "PENDING"
	BinanceRechargeCompleted     = "COMPLETED"
	BinanceRechargeExpired       = "EXPIRED"
	BinanceRechargeReview        = "REVIEW"
	binanceRechargePollInterval  = 30 * time.Second
	binanceRechargeHistoryWindow = 24 * time.Hour
	binanceRechargePollTimeout   = 100 * time.Second
)

// BinanceRechargeClient returns only authenticated, successful incoming USDT
// transfers whose receiver UID matches the requested account. It never rounds.
type BinanceRechargeClient interface {
	History(context.Context, string, string, string, time.Time, time.Time) ([]binancepay.Receipt, error)
	Probe(context.Context, string, string) error
	CloseIdleConnections()
}

type BinanceRechargeAccount struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	RecipientUID        string     `json:"recipient_uid"`
	Enabled             bool       `json:"enabled"`
	OrderTTLMinutes     int        `json:"order_ttl_minutes"`
	APIKeyConfigured    bool       `json:"api_key_configured"`
	APISecretConfigured bool       `json:"api_secret_configured"`
	HasOrders           bool       `json:"has_orders"`
	LastCheckedAt       *time.Time `json:"last_checked_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	apiKeyEncrypted     string
	apiSecretEncrypted  string
}

type BinanceRechargeAccountInput struct {
	Name            string `json:"name"`
	RecipientUID    string `json:"recipient_uid"`
	APIKey          string `json:"api_key"`
	APISecret       string `json:"api_secret"`
	Enabled         bool   `json:"enabled"`
	OrderTTLMinutes int    `json:"order_ttl_minutes"`
}

type BinanceRechargePublicAccount struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	RecipientUID    string `json:"recipient_uid"`
	OrderTTLMinutes int    `json:"order_ttl_minutes"`
}

type BinanceRechargeConfig struct {
	Enabled             bool                           `json:"enabled"`
	MinAmount           int64                          `json:"min_amount"`
	MaxAmount           int64                          `json:"max_amount"`
	DecimalPlaces       int                            `json:"decimal_places"`
	PollIntervalSeconds int                            `json:"poll_interval_seconds"`
	Accounts            []BinanceRechargePublicAccount `json:"accounts"`
}

type BinanceRechargeOrder struct {
	ID              int64      `json:"id"`
	OrderNo         string     `json:"order_no"`
	UserID          int64      `json:"user_id"`
	UserEmail       string     `json:"user_email,omitempty"`
	AccountID       int64      `json:"account_id"`
	AccountName     string     `json:"account_name"`
	RecipientUID    string     `json:"recipient_uid"`
	RequestedAmount int64      `json:"requested_amount"`
	PayAmount       string     `json:"pay_amount"`
	CreditedAmount  string     `json:"credited_amount"`
	Status          string     `json:"status"`
	TransactionID   string     `json:"transaction_id,omitempty"`
	ReviewReason    string     `json:"review_reason,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
	PaidAt          *time.Time `json:"paid_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	amountUnits     int64
	creditedUnits   int64
}

type BinanceRechargeListParams struct {
	Page      int
	PageSize  int
	UserID    int64
	AccountID int64
	Status    string
	Keyword   string
}

type BinanceRechargeCheckResult struct {
	Matched int `json:"matched"`
	Review  int `json:"review"`
}

type BinanceRechargeService struct {
	db              *sql.DB
	encryptor       SecretEncryptor
	client          BinanceRechargeClient
	authInvalidator APIKeyAuthCacheInvalidator
	billingCache    *BillingCacheService
	mu              sync.Mutex
	started         bool
	stopped         bool
	cancel          context.CancelFunc
	wg              sync.WaitGroup
}

func NewBinanceRechargeService(db *sql.DB, encryptor SecretEncryptor, client BinanceRechargeClient, authInvalidator APIKeyAuthCacheInvalidator, billingCache *BillingCacheService) *BinanceRechargeService {
	return &BinanceRechargeService{db: db, encryptor: encryptor, client: client, authInvalidator: authInvalidator, billingCache: billingCache}
}

var binanceUIDPattern = regexp.MustCompile(`^[0-9]{1,32}$`)

func normalizeBinanceRechargeAccountInput(input BinanceRechargeAccountInput) (BinanceRechargeAccountInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.RecipientUID = strings.TrimLeft(strings.TrimSpace(input.RecipientUID), "0")
	input.APIKey = strings.TrimSpace(input.APIKey)
	input.APISecret = strings.TrimSpace(input.APISecret)
	if input.OrderTTLMinutes == 0 {
		input.OrderTTLMinutes = 30
	}
	if input.Name == "" || len([]rune(input.Name)) > 100 || !binanceUIDPattern.MatchString(input.RecipientUID) {
		return input, infraerrors.BadRequest("BINANCE_ACCOUNT_INVALID", "账户名称或币安 UID 无效")
	}
	if input.OrderTTLMinutes != 15 && input.OrderTTLMinutes != 30 {
		return input, infraerrors.BadRequest("BINANCE_TTL_INVALID", "订单有效期只能是 15 或 30 分钟")
	}
	if len(input.APIKey) > 512 || len(input.APISecret) > 512 {
		return input, infraerrors.BadRequest("BINANCE_CREDENTIAL_INVALID", "币安 API 凭据格式无效")
	}
	return input, nil
}

const binanceAccountColumns = `a.id, a.name, a.recipient_uid, a.enabled, a.order_ttl_minutes,
a.api_key_encrypted, a.api_secret_encrypted, a.last_checked_at, a.last_error, a.created_at, a.updated_at,
EXISTS(SELECT 1 FROM binance_recharge_orders o WHERE o.account_id = a.id)`

type binanceRowScanner interface{ Scan(...any) error }

func scanBinanceRechargeAccount(row binanceRowScanner) (*BinanceRechargeAccount, error) {
	var a BinanceRechargeAccount
	err := row.Scan(&a.ID, &a.Name, &a.RecipientUID, &a.Enabled, &a.OrderTTLMinutes,
		&a.apiKeyEncrypted, &a.apiSecretEncrypted, &a.LastCheckedAt, &a.LastError, &a.CreatedAt, &a.UpdatedAt, &a.HasOrders)
	a.APIKeyConfigured = a.apiKeyEncrypted != ""
	a.APISecretConfigured = a.apiSecretEncrypted != ""
	return &a, err
}

func (s *BinanceRechargeService) ListAccounts(ctx context.Context) ([]BinanceRechargeAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+binanceAccountColumns+` FROM binance_recharge_accounts a ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	accounts := make([]BinanceRechargeAccount, 0)
	for rows.Next() {
		a, err := scanBinanceRechargeAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, *a)
	}
	return accounts, rows.Err()
}

func (s *BinanceRechargeService) Config(ctx context.Context) (*BinanceRechargeConfig, error) {
	accounts, err := s.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	result := &BinanceRechargeConfig{MinAmount: 1, MaxAmount: 1000000, DecimalPlaces: 5, PollIntervalSeconds: 30, Accounts: make([]BinanceRechargePublicAccount, 0)}
	for _, a := range accounts {
		if a.Enabled && a.APIKeyConfigured && a.APISecretConfigured {
			result.Accounts = append(result.Accounts, BinanceRechargePublicAccount{ID: a.ID, Name: a.Name, RecipientUID: a.RecipientUID, OrderTTLMinutes: a.OrderTTLMinutes})
		}
	}
	result.Enabled = len(result.Accounts) > 0
	return result, nil
}

func (s *BinanceRechargeService) SaveAccount(ctx context.Context, id int64, input BinanceRechargeAccountInput) (*BinanceRechargeAccount, error) {
	input, err := normalizeBinanceRechargeAccountInput(input)
	if err != nil {
		return nil, err
	}
	if s.encryptor == nil {
		return nil, infraerrors.ServiceUnavailable("BINANCE_ENCRYPTION_UNAVAILABLE", "凭据加密服务不可用")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current BinanceRechargeAccount
	if id > 0 {
		a, err := scanBinanceRechargeAccount(tx.QueryRowContext(ctx, `SELECT `+binanceAccountColumns+` FROM binance_recharge_accounts a WHERE a.id=$1 FOR UPDATE OF a`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("BINANCE_ACCOUNT_NOT_FOUND", "收款账户不存在")
		}
		if err != nil {
			return nil, err
		}
		current = *a
		if current.RecipientUID != input.RecipientUID {
			var polling bool
			if err := tx.QueryRowContext(ctx, `SELECT poll_token<>'' AND api_locked_until>NOW() FROM binance_recharge_accounts WHERE id=$1`, id).Scan(&polling); err != nil {
				return nil, err
			}
			if polling {
				return nil, infraerrors.Conflict("BINANCE_ACCOUNT_POLLING", "查收进行中，暂时不能修改收款 UID")
			}
			// Recheck after acquiring the account row lock. The initial SELECT's
			// snapshot may predate a concurrent first order that held that lock.
			var hasHistory bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM binance_recharge_orders WHERE account_id=$1) OR EXISTS(SELECT 1 FROM binance_recharge_receipts WHERE account_id=$1)`, id).Scan(&hasHistory); err != nil {
				return nil, err
			}
			if hasHistory {
				return nil, infraerrors.Conflict("BINANCE_UID_IMMUTABLE", "已有订单或到账记录的收款 UID 不可修改，请新增账户")
			}
		}
	}
	keyEncrypted, secretEncrypted := current.apiKeyEncrypted, current.apiSecretEncrypted
	if input.APIKey != "" {
		keyEncrypted, err = s.encryptor.Encrypt(input.APIKey)
		if err != nil {
			return nil, infraerrors.ServiceUnavailable("BINANCE_ENCRYPTION_FAILED", "保存币安 API 凭据失败")
		}
	}
	if input.APISecret != "" {
		secretEncrypted, err = s.encryptor.Encrypt(input.APISecret)
		if err != nil {
			return nil, infraerrors.ServiceUnavailable("BINANCE_ENCRYPTION_FAILED", "保存币安 API 凭据失败")
		}
	}
	if input.Enabled && (keyEncrypted == "" || secretEncrypted == "") {
		return nil, infraerrors.BadRequest("BINANCE_CREDENTIAL_REQUIRED", "启用账户前必须配置 API Key 和 API Secret")
	}
	if id == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO binance_recharge_accounts (name,recipient_uid,api_key_encrypted,api_secret_encrypted,enabled,order_ttl_minutes) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, input.Name, input.RecipientUID, keyEncrypted, secretEncrypted, input.Enabled, input.OrderTTLMinutes).Scan(&id)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE binance_recharge_accounts SET name=$2,recipient_uid=$3,api_key_encrypted=$4,api_secret_encrypted=$5,enabled=$6,order_ttl_minutes=$7,updated_at=NOW() WHERE id=$1`, id, input.Name, input.RecipientUID, keyEncrypted, secretEncrypted, input.Enabled, input.OrderTTLMinutes)
	}
	if err != nil {
		if isBinanceUniqueViolation(err) {
			return nil, infraerrors.Conflict("BINANCE_UID_EXISTS", "该收款 UID 已配置")
		}
		return nil, err
	}
	a, err := scanBinanceRechargeAccount(tx.QueryRowContext(ctx, `SELECT `+binanceAccountColumns+` FROM binance_recharge_accounts a WHERE a.id=$1`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return a, nil
}

// Driver-independent PostgreSQL SQLSTATE, without leaking SQL parameters.
func isBinanceUniqueViolation(err error) bool {
	var state interface{ SQLState() string }
	return errors.As(err, &state) && state.SQLState() == "23505"
}

func (s *BinanceRechargeService) invalidateBalance(ctx context.Context, userID int64) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if s.authInvalidator != nil {
		s.authInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	if s.billingCache != nil {
		_ = s.billingCache.InvalidateUserBalance(ctx, userID)
	}
}

func binanceRechargeReceiptReviewReason(order *BinanceRechargeOrder, receipt binancepay.Receipt) string {
	if order == nil {
		return "未找到对应订单，请人工核对"
	}
	if receipt.AmountUnits != order.amountUnits {
		return "实收金额与订单不一致"
	}
	if receipt.Time.Before(order.CreatedAt) {
		return "转账时间早于订单创建时间"
	}
	if receipt.Time.After(order.ExpiresAt) {
		return "订单到期后付款，请人工核对"
	}
	if order.Status != BinanceRechargePending && order.Status != BinanceRechargeExpired {
		return "订单已结算或处于待核对状态"
	}
	return ""
}

func binanceRechargePollError(err error) string {
	var limited *binancepay.RateLimitError
	if errors.As(err, &limited) {
		return "币安 API 限流，已延后查收"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "币安查收超时，稍后自动重试"
	}
	return "币安查收失败，请检查 API 读取权限、收款 UID、凭据和网络"
}

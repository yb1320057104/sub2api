package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type binanceRechargeBillsClient interface {
	Bills(context.Context, string, string, string, time.Time, time.Time) (*binancepay.BillsPage, error)
}

type BinanceRechargeBills struct {
	*binancepay.BillsPage
	AccountID    int64     `json:"account_id"`
	AccountName  string    `json:"account_name"`
	RecipientUID string    `json:"recipient_uid"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
}

func BinanceRechargeBillWindow(start, end string, now time.Time) (time.Time, time.Time, error) {
	// The deployment may run in UTC; "today" follows the Chinese account UI.
	local := now.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	from := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	to := now
	var err error
	if strings.TrimSpace(start) != "" {
		from, err = time.Parse(time.RFC3339, strings.TrimSpace(start))
		if err != nil {
			return from, to, infraerrors.BadRequest("BINANCE_BILLS_DATE_INVALID", "请选择 89 天以内的有效账单日期区间")
		}
	}
	if strings.TrimSpace(end) != "" {
		to, err = time.Parse(time.RFC3339, strings.TrimSpace(end))
		if err != nil {
			return from, to, infraerrors.BadRequest("BINANCE_BILLS_DATE_INVALID", "请选择 89 天以内的有效账单日期区间")
		}
	}
	if to.After(now) {
		to = now
	}
	if from.UnixMilli() < 0 || !to.After(from) || to.Sub(from) > 89*24*time.Hour {
		return from, to, infraerrors.BadRequest("BINANCE_BILLS_DATE_INVALID", "请选择 89 天以内的有效账单日期区间")
	}
	return from.UTC(), to.UTC(), nil
}

// Bills reads live Pay history without invoking acceptReceipt, changing orders,
// or crediting balances. The API lease and provider cooldown are shared with
// collection, while the independent bill cooldown cannot postpone due orders.
func (s *BinanceRechargeService) Bills(ctx context.Context, accountID int64, from, to time.Time) (*BinanceRechargeBills, error) {
	if s.client == nil || s.encryptor == nil {
		return nil, infraerrors.ServiceUnavailable("BINANCE_BILLS_UNAVAILABLE", "币安账单查询服务不可用")
	}
	if from.IsZero() || from.UnixMilli() < 0 || !to.After(from) || to.Sub(from) > 89*24*time.Hour {
		return nil, infraerrors.BadRequest("BINANCE_BILLS_DATE_INVALID", "请选择 89 天以内的有效账单日期区间")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	token := uuid.NewString()
	var failures int
	err := s.db.QueryRowContext(ctx, `UPDATE binance_recharge_accounts a
SET api_locked_until=NOW()+INTERVAL '45 seconds',bills_next_query_at=NOW()+INTERVAL '60 seconds',poll_token=$2
WHERE a.id=$1 AND a.api_locked_until<=NOW() AND a.bills_next_query_at<=NOW()
AND NOT (a.next_poll_at<=NOW() AND EXISTS(SELECT 1 FROM binance_recharge_orders o WHERE o.account_id=a.id AND o.status IN ('PENDING','EXPIRED') AND o.created_at>=NOW()-INTERVAL '24 hours'))
RETURNING failure_count`, accountID, token).Scan(&failures)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, s.binanceCooldownError(ctx, accountID, true)
	}
	if err != nil {
		return nil, err
	}
	a, err := scanBinanceRechargeAccount(s.db.QueryRowContext(ctx, `SELECT `+binanceAccountColumns+` FROM binance_recharge_accounts a WHERE a.id=$1`, accountID))
	var page *binancepay.BillsPage
	if err == nil {
		var key, secret string
		key, err = s.encryptor.Decrypt(a.apiKeyEncrypted)
		if err == nil {
			secret, err = s.encryptor.Decrypt(a.apiSecretEncrypted)
		}
		if err == nil {
			var operation BinanceRechargeClient
			var finish func()
			operation, finish, err = s.accountClient(a)
			if err == nil {
				client, ok := operation.(binanceRechargeBillsClient)
				if !ok {
					err = infraerrors.ServiceUnavailable("BINANCE_BILLS_UNAVAILABLE", "币安账单查询服务不可用")
				} else {
					page, err = client.Bills(ctx, key, secret, a.RecipientUID, from, to)
				}
				finish()
			}
		}
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	var finishErr error
	if err == nil {
		// Leave next_poll_at and collection status untouched on successful reads.
		_, finishErr = s.db.ExecContext(finishCtx, `UPDATE binance_recharge_accounts SET api_locked_until=to_timestamp(0),poll_token='' WHERE id=$1 AND poll_token=$2`, accountID, token)
	} else {
		failures++
		if failures > 6 {
			failures = 6
		}
		delay := time.Duration(1<<uint(failures)) * binanceRechargePollInterval
		var limited *binancepay.RateLimitError
		providerLimited := errors.As(err, &limited)
		if providerLimited && limited.RetryAfter > delay {
			delay = limited.RetryAfter
		}
		_, finishErr = s.db.ExecContext(finishCtx, `UPDATE binance_recharge_accounts SET api_locked_until=CASE WHEN $6 THEN NOW()+make_interval(secs=>$4) ELSE to_timestamp(0) END,poll_token='',failure_count=$3,
next_poll_at=GREATEST(next_poll_at,NOW()+make_interval(secs=>$4)),bills_next_query_at=GREATEST(bills_next_query_at,NOW()+make_interval(secs=>$4)),last_error=$5
WHERE id=$1 AND poll_token=$2`, accountID, token, failures, delay.Seconds(), binanceRechargePollError(err), providerLimited)
	}
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("BINANCE_BILLS_FAILED", binanceRechargePollError(err))
	}
	if finishErr != nil {
		return nil, finishErr
	}
	if page == nil {
		return nil, infraerrors.ServiceUnavailable("BINANCE_BILLS_FAILED", "币安未返回有效账单")
	}
	return &BinanceRechargeBills{BillsPage: page, AccountID: a.ID, AccountName: a.Name, RecipientUID: a.RecipientUID, StartTime: from, EndTime: to}, nil
}

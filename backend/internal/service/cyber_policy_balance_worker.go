package service

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

type cyberBalanceInvalidator interface {
	InvalidateUserBalance(context.Context, int64) error
}

// CyberPolicyBalanceWorker delivers invalidations committed with automatic
// audit charges. A delayed second pass catches in-flight balance cache loads.
type CyberPolicyBalanceWorker struct {
	db     *sql.DB
	cache  cyberBalanceInvalidator
	owner  string
	ctx    context.Context
	cancel context.CancelFunc
	start  sync.Once
	stop   sync.Once
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

func newCyberPolicyBalanceWorker(db *sql.DB, cache cyberBalanceInvalidator) *CyberPolicyBalanceWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &CyberPolicyBalanceWorker{db: db, cache: cache, owner: uuid.NewString(), ctx: ctx, cancel: cancel}
}

func ProvideCyberPolicyBalanceWorker(db *sql.DB, cache *BillingCacheService) *CyberPolicyBalanceWorker {
	w := newCyberPolicyBalanceWorker(db, cache)
	startBackgroundAfterActivation("CyberPolicyBalanceWorker", w.Start)
	return w
}

func (w *CyberPolicyBalanceWorker) Start() {
	if w == nil || w.db == nil || w.cache == nil {
		return
	}
	w.start.Do(func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.closed {
			return
		}
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				ctx, cancel := context.WithTimeout(w.ctx, 20*time.Second)
				if err := w.processBatch(ctx); err != nil && w.ctx.Err() == nil {
					slog.Warn("cyber_policy.balance_cache_delivery_failed")
				}
				cancel()
				select {
				case <-w.ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

func (w *CyberPolicyBalanceWorker) Stop() {
	if w == nil {
		return
	}
	w.stop.Do(func() {
		w.mu.Lock()
		w.closed = true
		w.cancel()
		w.mu.Unlock()
		w.wg.Wait()
	})
}

type cyberBalanceDelivery struct {
	userID, revision int64
	stage            int
}

func (w *CyberPolicyBalanceWorker) processBatch(ctx context.Context) error {
	rows, err := w.db.QueryContext(ctx, `WITH pending AS (
		SELECT user_id FROM cyber_policy_auto_charge_outbox
		WHERE available_at<=NOW() AND (claimed_at IS NULL OR claimed_at<NOW()-INTERVAL '30 seconds')
		ORDER BY available_at,user_id LIMIT 100 FOR UPDATE SKIP LOCKED
	) UPDATE cyber_policy_auto_charge_outbox o SET claimed_at=NOW(),claimed_by=$1,attempts=o.attempts+1
	FROM pending p WHERE o.user_id=p.user_id RETURNING o.user_id,o.revision,o.stage`, w.owner)
	if err != nil {
		return err
	}
	items := []cyberBalanceDelivery{}
	for rows.Next() {
		var item cyberBalanceDelivery
		if err := rows.Scan(&item.userID, &item.revision, &item.stage); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := w.cache.InvalidateUserBalance(callCtx, item.userID)
		cancel()
		var query string
		if err != nil {
			query = `UPDATE cyber_policy_auto_charge_outbox SET claimed_at=NULL,claimed_by=NULL,
				available_at=NOW()+INTERVAL '5 seconds',last_error='balance_cache_unavailable'
				WHERE user_id=$1 AND revision=$2 AND claimed_by=$3`
		} else if item.stage == 0 {
			query = `UPDATE cyber_policy_auto_charge_outbox SET stage=1,claimed_at=NULL,claimed_by=NULL,
				available_at=NOW()+INTERVAL '30 seconds',last_error=NULL
				WHERE user_id=$1 AND revision=$2 AND claimed_by=$3`
		} else {
			query = `DELETE FROM cyber_policy_auto_charge_outbox WHERE user_id=$1 AND revision=$2 AND claimed_by=$3`
		}
		if _, err := w.db.ExecContext(ctx, query, item.userID, item.revision, w.owner); err != nil {
			return err
		}
	}
	return nil
}

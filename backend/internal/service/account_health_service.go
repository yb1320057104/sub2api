package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/lib/pq"
	"github.com/robfig/cron/v3"
)

const accountHealthLockID int64 = 81003701

type accountHealthReader interface {
	GetAccountHealthStats(context.Context, int64, time.Time, time.Time) (*usagestats.AccountHealthStats, error)
}
type AccountHealthConfig struct {
	Enabled         bool      `json:"enabled"`
	IntervalMinutes int       `json:"interval_minutes"`
	WindowHours     int       `json:"window_hours"`
	Revision        int64     `json:"revision"`
	NextCycleAt     time.Time `json:"next_cycle_at"`
}
type AccountHealthService struct {
	db                        *sql.DB
	reader                    accountHealthReader
	ctx                       context.Context
	cancel                    context.CancelFunc
	mu                        sync.Mutex
	started, stopped, running bool
	cron                      *cron.Cron
	workers                   sync.WaitGroup
}

func NewAccountHealthService(db *sql.DB, repo UsageLogRepository) *AccountHealthService {
	ctx, cancel := context.WithCancel(context.Background())
	reader, _ := repo.(accountHealthReader)
	return &AccountHealthService{db: db, reader: reader, ctx: ctx, cancel: cancel}
}
func (s *AccountUsageService) AccountHealth() *AccountHealthService { return s.healthStats }
func (s *AccountUsageService) Stop() {
	if s != nil && s.healthStats != nil {
		s.healthStats.Stop()
	}
}
func (s *AccountHealthService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped || s.db == nil || s.reader == nil {
		return
	}
	c := cron.New()
	if _, err := c.AddFunc("* * * * *", s.wake); err != nil {
		return
	}
	s.cron = c
	s.started = true
	c.Start()
}
func (s *AccountHealthService) Stop() {
	s.mu.Lock()
	s.stopped = true
	s.cancel()
	c := s.cron
	s.mu.Unlock()
	if c != nil {
		<-c.Stop().Done()
	}
	s.workers.Wait()
}
func (s *AccountHealthService) wake() {
	s.mu.Lock()
	if s.stopped || s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.workers.Add(1)
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock(); s.workers.Done() }()
	ctx, cancel := context.WithTimeout(s.ctx, 45*time.Second)
	defer cancel()
	if err := s.work(ctx); err != nil && s.ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) {
		slog.Warn("account_health_statistics_incomplete", "message", "账号统计暂未完成，下次cron继续")
	}
}
func (s *AccountHealthService) Config(ctx context.Context) (*AccountHealthConfig, error) {
	c := &AccountHealthConfig{}
	err := s.db.QueryRowContext(ctx, `SELECT enabled,interval_minutes,window_hours,revision,next_cycle_at FROM pk_account_health_config WHERE id=1`).Scan(&c.Enabled, &c.IntervalMinutes, &c.WindowHours, &c.Revision, &c.NextCycleAt)
	return c, err
}
func (s *AccountHealthService) SaveConfig(ctx context.Context, c *AccountHealthConfig) error {
	if c == nil || c.IntervalMinutes < 5 || c.IntervalMinutes > 1440 || c.WindowHours < 1 || c.WindowHours > 168 {
		return infraerrors.BadRequest("ACCOUNT_HEALTH_CONFIG", "统计周期须为5至1440分钟，统计范围须为1至168小时")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	r, err := tx.ExecContext(ctx, `UPDATE pk_account_health_config SET enabled=$1,interval_minutes=$2,window_hours=$3,revision=revision+1,next_cycle_at=date_trunc('minute',NOW())+INTERVAL '1 minute' WHERE id=1 AND revision=$4`, c.Enabled, c.IntervalMinutes, c.WindowHours, c.Revision)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return infraerrors.BadRequest("ACCOUNT_HEALTH_CONFIG_CHANGED", "配置已变化，请刷新后再保存")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_account_health_jobs SET status='cancelled',finished_at=NOW() WHERE cycle_id IN (SELECT id FROM pk_account_health_cycles WHERE status='running') AND status IN ('pending','running')`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_account_health_cycles SET status='cancelled',finished_at=NOW() WHERE status='running'`); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *AccountHealthService) Snapshots(ctx context.Context, ids []int64) (map[int64]*usagestats.AccountHealthStats, error) {
	out := map[int64]*usagestats.AccountHealthStats{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT account_id,result FROM pk_account_health_snapshots WHERE account_id=ANY($1)`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var raw []byte
		var value usagestats.AccountHealthStats
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		out[id] = &value
	}
	return out, rows.Err()
}

// One cross-process session lock owns the queue; account queries are sequential.
// Each wake is bounded to 45 seconds and at most 100 rows, not 100 concurrent SQLs.
// Large logical batches continue on the next cron tick, without creating a sixth batch.
func (s *AccountHealthService) work(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var locked bool
	if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", accountHealthLockID).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() {
		unlock, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, e := conn.ExecContext(unlock, "SELECT pg_advisory_unlock($1)", accountHealthLockID); e != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if err = s.pruneProgress(ctx, conn); err != nil {
		return err
	}
	id, revision, start, end, err := s.ensureCycle(ctx, conn)
	if err != nil || id == 0 {
		return err
	}
	// No other live worker can own a running row while we hold the session lock.
	if _, err = conn.ExecContext(ctx, `UPDATE pk_account_health_jobs SET status='pending',started_at=NULL WHERE cycle_id=$1 AND status='running'`, id); err != nil {
		return err
	}
	rows, err := conn.QueryContext(ctx, `SELECT account_id FROM pk_account_health_jobs WHERE cycle_id=$1 AND status='pending' AND scheduled_at<=NOW() AND batch_no=(SELECT MIN(batch_no) FROM pk_account_health_jobs WHERE cycle_id=$1 AND status IN ('pending','running')) ORDER BY account_id LIMIT 100`, id)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var a int64
		if err = rows.Scan(&a); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, a)
	}
	readErr := rows.Err()
	_ = rows.Close()
	if readErr != nil {
		return readErr
	}
	for _, accountID := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r, e := conn.ExecContext(ctx, `UPDATE pk_account_health_jobs j SET status='running',started_at=NOW() WHERE cycle_id=$1 AND account_id=$2 AND status='pending' AND EXISTS(SELECT 1 FROM pk_account_health_config WHERE id=1 AND enabled AND revision=$3)`, id, accountID, revision)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n == 0 {
			return nil
		}
		var exists bool
		if e = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND deleted_at IS NULL)`, accountID).Scan(&exists); e != nil {
			return e
		}
		status, message := "completed", ""
		var stats *usagestats.AccountHealthStats
		if !exists {
			status = "skipped"
		} else {
			queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			stats, e = s.reader.GetAccountHealthStats(queryCtx, accountID, start, end)
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if e != nil {
				status = "failed"
				message = "统计查询失败或超时，保留上次结果，等待下轮统计"
			}
		}
		if e = s.finishAccount(ctx, conn, id, accountID, revision, status, message, stats); e != nil {
			return e
		}
	}
	_, err = conn.ExecContext(ctx, `UPDATE pk_account_health_cycles c SET status='completed',finished_at=NOW() WHERE id=$1 AND status='running' AND NOT EXISTS(SELECT 1 FROM pk_account_health_jobs j WHERE j.cycle_id=c.id AND j.status IN ('pending','running'))`, id)
	return err
}
func (s *AccountHealthService) ensureCycle(ctx context.Context, conn *sql.Conn) (int64, int64, time.Time, time.Time, error) {
	var id, revision int64
	var start, end time.Time
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return 0, 0, start, end, err
	}
	defer func() { _ = tx.Rollback() }()
	c := &AccountHealthConfig{}
	err = tx.QueryRowContext(ctx, `SELECT enabled,interval_minutes,window_hours,revision,next_cycle_at FROM pk_account_health_config WHERE id=1 FOR UPDATE`).Scan(&c.Enabled, &c.IntervalMinutes, &c.WindowHours, &c.Revision, &c.NextCycleAt)
	if err != nil || !c.Enabled {
		return 0, 0, start, end, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id,revision,window_start,window_end FROM pk_account_health_cycles WHERE status='running' ORDER BY id DESC LIMIT 1`).Scan(&id, &revision, &start, &end)
	if err == nil {
		return id, revision, start, end, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, start, end, err
	}
	now := time.Now().Truncate(time.Minute)
	if now.Before(c.NextCycleAt) {
		return 0, 0, start, end, nil
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE deleted_at IS NULL`).Scan(&count); err != nil {
		return 0, 0, start, end, err
	}
	batches := min(5, count)
	status := "running"
	if count == 0 {
		status = "completed"
	}
	end = now
	start = now.Add(-time.Duration(c.WindowHours) * time.Hour)
	revision = c.Revision
	err = tx.QueryRowContext(ctx, `INSERT INTO pk_account_health_cycles(revision,status,started_at,window_start,window_end,interval_minutes,batch_count,total_accounts,finished_at) VALUES($1,$2,$3,$4,$3,$5,$6,$7,CASE WHEN $7=0 THEN $3 ELSE NULL END) RETURNING id`, revision, status, now, start, c.IntervalMinutes, batches, count).Scan(&id)
	if err != nil {
		return 0, 0, start, end, err
	}
	if count > 0 {
		// ntile spreads the remainder across the first groups, e.g. 301 => 61/60/60/60/60.
		_, err = tx.ExecContext(ctx, `INSERT INTO pk_account_health_jobs(cycle_id,account_id,account_name,batch_no,scheduled_at)
   SELECT $1,id,name,batch,$2::timestamptz+((batch-1)*$3::double precision/$4::integer)*INTERVAL '1 minute'
   FROM (SELECT id,name,ntile($4::integer) OVER(ORDER BY id) AS batch FROM accounts WHERE deleted_at IS NULL) a`, id, now, c.IntervalMinutes, batches)
		if err != nil {
			return 0, 0, start, end, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_account_health_config SET next_cycle_at=$1 WHERE id=1`, now.Add(time.Duration(c.IntervalMinutes)*time.Minute)); err != nil {
		return 0, 0, start, end, err
	}

	return id, revision, start, end, tx.Commit()
}
func (s *AccountHealthService) finishAccount(ctx context.Context, conn *sql.Conn, cycleID, accountID, revision int64, status, message string, stats *usagestats.AccountHealthStats) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current int64
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT revision,enabled FROM pk_account_health_config WHERE id=1 FOR SHARE`).Scan(&current, &enabled); err != nil {
		return err
	}
	if !enabled || current != revision {
		return nil
	}
	var raw any
	if stats != nil {
		data, e := json.Marshal(stats)
		if e != nil {
			return e
		}
		raw = string(data)
	}
	r, err := tx.ExecContext(ctx, `UPDATE pk_account_health_jobs SET status=$3,error_message=$4,result=$5::jsonb,finished_at=NOW() WHERE cycle_id=$1 AND account_id=$2 AND status='running'`, cycleID, accountID, status, message, raw)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return nil
	}
	if status == "completed" && stats != nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO pk_account_health_snapshots(account_id,result,computed_at) SELECT id,$2::jsonb,$3 FROM accounts WHERE id=$1 AND deleted_at IS NULL ON CONFLICT(account_id) DO UPDATE SET result=EXCLUDED.result,computed_at=EXCLUDED.computed_at`, accountID, raw, stats.ComputedAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Missing wiring must fail visibly rather than falling back to expensive raw reads.
func (s *AccountUsageService) RequireAccountHealth() (*AccountHealthService, error) {
	if s == nil || s.healthStats == nil || s.healthStats.db == nil || s.healthStats.reader == nil {
		return nil, errors.New("account health statistics unavailable")
	}
	return s.healthStats, nil
}

// Progress reads the durable queue and snapshots only; it never scans request history.
type AccountHealthBatch struct {
	BatchNo     int       `json:"batch_no"`
	Total       int       `json:"total"`
	Completed   int       `json:"completed"`
	Failed      int       `json:"failed"`
	Skipped     int       `json:"skipped"`
	Pending     int       `json:"pending"`
	Running     int       `json:"running"`
	ScheduledAt time.Time `json:"scheduled_at"`
}
type AccountHealthCycle struct {
	ID            int64                `json:"id"`
	Status        string               `json:"status"`
	StartedAt     time.Time            `json:"started_at"`
	FinishedAt    *time.Time           `json:"finished_at"`
	WindowStart   time.Time            `json:"window_start"`
	WindowEnd     time.Time            `json:"window_end"`
	BatchCount    int                  `json:"batch_count"`
	TotalAccounts int                  `json:"total_accounts"`
	Batches       []AccountHealthBatch `json:"batches"`
}
type AccountHealthJob struct {
	AccountID    int64                          `json:"account_id"`
	AccountName  string                         `json:"account_name"`
	BatchNo      int                            `json:"batch_no"`
	Status       string                         `json:"status"`
	ScheduledAt  time.Time                      `json:"scheduled_at"`
	StartedAt    *time.Time                     `json:"started_at"`
	FinishedAt   *time.Time                     `json:"finished_at"`
	ErrorMessage string                         `json:"error_message"`
	Result       *usagestats.AccountHealthStats `json:"result"`
}
type AccountHealthProgress struct {
	Config   *AccountHealthConfig `json:"config"`
	Cycle    *AccountHealthCycle  `json:"cycle"`
	Current  []AccountHealthJob   `json:"current"`
	Items    []AccountHealthJob   `json:"items"`
	Total    int                  `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
}

func (s *AccountHealthService) Progress(ctx context.Context, page, size int) (*AccountHealthProgress, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	out := &AccountHealthProgress{Config: &AccountHealthConfig{}, Current: []AccountHealthJob{}, Items: []AccountHealthJob{}, Page: page, PageSize: size}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c := out.Config
	if err = tx.QueryRowContext(ctx, `SELECT enabled,interval_minutes,window_hours,revision,next_cycle_at FROM pk_account_health_config WHERE id=1`).Scan(&c.Enabled, &c.IntervalMinutes, &c.WindowHours, &c.Revision, &c.NextCycleAt); err != nil {
		return nil, err
	}
	cycle := &AccountHealthCycle{Batches: []AccountHealthBatch{}}
	err = tx.QueryRowContext(ctx, `SELECT id,status,started_at,finished_at,window_start,window_end,batch_count,total_accounts FROM pk_account_health_cycles ORDER BY id DESC LIMIT 1`).Scan(&cycle.ID, &cycle.Status, &cycle.StartedAt, &cycle.FinishedAt, &cycle.WindowStart, &cycle.WindowEnd, &cycle.BatchCount, &cycle.TotalAccounts)
	if errors.Is(err, sql.ErrNoRows) {
		return out, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	out.Cycle = cycle
	out.Total = cycle.TotalAccounts
	rows, err := tx.QueryContext(ctx, `SELECT batch_no,COUNT(*),COUNT(*) FILTER(WHERE status='completed'),COUNT(*) FILTER(WHERE status='failed'),COUNT(*) FILTER(WHERE status IN ('skipped','cancelled')),COUNT(*) FILTER(WHERE status='pending'),COUNT(*) FILTER(WHERE status='running'),MIN(scheduled_at) FROM pk_account_health_jobs WHERE cycle_id=$1 GROUP BY batch_no ORDER BY batch_no`, cycle.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b AccountHealthBatch
		if err = rows.Scan(&b.BatchNo, &b.Total, &b.Completed, &b.Failed, &b.Skipped, &b.Pending, &b.Running, &b.ScheduledAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		cycle.Batches = append(cycle.Batches, b)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	// Include the current account even when it is outside the visible page.
	rows, err = tx.QueryContext(ctx, `SELECT j.account_id,COALESCE(a.name,j.account_name),j.batch_no FROM pk_account_health_jobs j LEFT JOIN accounts a ON a.id=j.account_id AND a.deleted_at IS NULL WHERE j.cycle_id=$1 AND j.status='running' ORDER BY j.account_id LIMIT 1`, cycle.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var j AccountHealthJob
		j.Status = "running"
		if err = rows.Scan(&j.AccountID, &j.AccountName, &j.BatchNo); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out.Current = append(out.Current, j)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT j.account_id,COALESCE(a.name,j.account_name),j.batch_no,j.status,j.scheduled_at,j.started_at,j.finished_at,j.error_message,j.result FROM pk_account_health_jobs j LEFT JOIN accounts a ON a.id=j.account_id AND a.deleted_at IS NULL WHERE j.cycle_id=$1 ORDER BY j.batch_no,j.account_id LIMIT $2 OFFSET $3`, cycle.ID, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var j AccountHealthJob
		var raw []byte
		if err = rows.Scan(&j.AccountID, &j.AccountName, &j.BatchNo, &j.Status, &j.ScheduledAt, &j.StartedAt, &j.FinishedAt, &j.ErrorMessage, &raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &j.Result); err != nil {
				_ = rows.Close()
				return nil, err
			}
		}
		out.Items = append(out.Items, j)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// Delete only a small part of one expired cycle on each cron tick; never cascade
// through a large pool in a single transaction. Latest snapshots are independent.
func (s *AccountHealthService) pruneProgress(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM pk_account_health_cycles WHERE status<>'running' AND started_at<NOW()-INTERVAL '7 days' ORDER BY id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM pk_account_health_jobs WHERE cycle_id=$1 AND account_id IN (SELECT account_id FROM pk_account_health_jobs WHERE cycle_id=$1 ORDER BY account_id LIMIT 1000)`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM pk_account_health_cycles WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM pk_account_health_jobs WHERE cycle_id=$1)`, id); err != nil {
		return err
	}
	return tx.Commit()
}

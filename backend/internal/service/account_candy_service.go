package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	accountCandySettingKey       = "pk_account_candy_test_config"
	accountCandyLockID     int64 = 81003401
	accountCandyWorkers          = 3
)

type AccountCandyTestConfig struct {
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"interval_minutes"`
	Model           string `json:"model"`
	Effort          string `json:"effort"`
	TargetGroupID   *int64 `json:"target_group_id"`
	generation      string
}

type accountCandyStoredConfig struct {
	*AccountCandyTestConfig
	Generation string `json:"_generation"`
}

type AccountCandyTestResult struct {
	identity          [32]byte
	probeAccount      *Account
	credentialAccount *Account
	ID                int64      `json:"id"`
	AccountID         int64      `json:"account_id"`
	Source            string     `json:"source"`
	Status            string     `json:"status"`
	Passed            *bool      `json:"passed"`
	Answer            *int       `json:"answer"`
	Model             string     `json:"model"`
	Effort            string     `json:"effort"`
	Prompt            string     `json:"prompt,omitempty"`
	ResponseText      string     `json:"response_text,omitempty"`
	OriginalGroupIDs  []int64    `json:"original_group_ids"`
	MovedToGroupID    *int64     `json:"moved_to_group_id"`
	ErrorMessage      string     `json:"error_message"`
	MoveError         string     `json:"move_error"`
	StartedAt         time.Time  `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	DurationMS        *int64     `json:"duration_ms"`
}

type accountCandyWorker struct {
	cancel     context.CancelFunc
	generation string
	claimed    bool
}

type AccountCandyTestService struct {
	db          *sql.DB
	accountRepo AccountRepository
	groupRepo   GroupRepository
	settingRepo SettingRepository
	testService *AccountTestService
	emailAlerts *RadarEmailAlertService
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	wake        chan struct{}
	active      map[int64]*accountCandyWorker
	workers     sync.WaitGroup
}

func NewAccountCandyTestService(db *sql.DB, accountRepo AccountRepository, groupRepo GroupRepository, settingRepo SettingRepository, testService *AccountTestService) *AccountCandyTestService {
	return &AccountCandyTestService{db: db, accountRepo: accountRepo, groupRepo: groupRepo, settingRepo: settingRepo, testService: testService, wake: make(chan struct{}, 1), active: make(map[int64]*accountCandyWorker)}
}

func defaultAccountCandyConfig() *AccountCandyTestConfig {
	return &AccountCandyTestConfig{IntervalMinutes: 60, Model: "gpt-6-astra", Effort: "medium"}
}

func decodeAccountCandyConfig(raw string) (*AccountCandyTestConfig, error) {
	cfg := defaultAccountCandyConfig()
	stored := accountCandyStoredConfig{AccountCandyTestConfig: cfg}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || stored.AccountCandyTestConfig == nil {
		return nil, errors.New("invalid account candy configuration")
	}
	cfg.generation = stored.Generation
	if err := validateAccountCandyConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func validateAccountCandyConfig(cfg *AccountCandyTestConfig) error {
	bad := func(message string) error { return infraerrors.BadRequest("INVALID_ACCOUNT_CANDY_CONFIG", message) }
	if cfg == nil {
		return bad("账号糖果测试配置不能为空")
	}
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.Effort = strings.TrimSpace(cfg.Effort)
	if !validIntelligenceTestModel(cfg.Model) || isOpenAIImageModel(cfg.Model) {
		return bad("请选择文本模型")
	}
	if !validIntelligenceTestEffort(cfg.Effort) {
		return bad("推理强度无效")
	}
	if cfg.IntervalMinutes < 1 || cfg.IntervalMinutes > 1440 {
		return bad("测试间隔必须为 1 至 1440 分钟")
	}
	if cfg.TargetGroupID != nil && *cfg.TargetGroupID <= 0 {
		return bad("目标分组无效")
	}
	if cfg.Enabled && cfg.TargetGroupID == nil {
		return bad("开启定时测试前请选择不合格账号的目标分组")
	}
	return nil
}

func (s *AccountCandyTestService) GetConfig(ctx context.Context) (*AccountCandyTestConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, accountCandySettingKey)
	if errors.Is(err, ErrSettingNotFound) {
		return defaultAccountCandyConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	return decodeAccountCandyConfig(raw)
}

func accountCandyConfigTx(ctx context.Context, tx *sql.Tx) (*AccountCandyTestConfig, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, accountCandySettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultAccountCandyConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	return decodeAccountCandyConfig(raw)
}

func (s *AccountCandyTestService) UpdateConfig(ctx context.Context, cfg *AccountCandyTestConfig) error {
	if err := validateAccountCandyConfig(cfg); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, accountCandyLockID); err != nil {
		return err
	}
	if cfg.TargetGroupID != nil {
		var platform, status string
		err = tx.QueryRowContext(ctx, `SELECT platform,status FROM groups WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, *cfg.TargetGroupID).Scan(&platform, &status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) || platform != PlatformOpenAI || status != StatusActive {
			return infraerrors.BadRequest("INVALID_ACCOUNT_CANDY_TARGET", "目标必须是有效的 OpenAI 分组")
		}
	}
	cfg.generation = uuid.NewString()
	raw, err := json.Marshal(accountCandyStoredConfig{AccountCandyTestConfig: cfg, Generation: cfg.generation})
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, accountCandySettingKey, string(raw)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_account_candy_state SET next_run_at=NOW()`); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	for _, worker := range s.active {
		if worker.claimed && worker.generation != cfg.generation {
			worker.cancel()
		}
	}
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *AccountCandyTestService) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *AccountCandyTestService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.done = make(chan struct{})
	go s.loop(s.ctx, s.done)
}

func (s *AccountCandyTestService) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	if cancel != nil {
		cancel()
	}
	s.mu.Unlock()
	// Cancel candy work before waiting for an admitted SMTP call to finish.
	// Email alerts stop independently even if candy was never started.
	if s.emailAlerts != nil {
		s.emailAlerts.Stop()
	}
	if cancel == nil {
		return
	}
	<-done
	s.mu.Lock()
	if s.done == done {
		s.cancel = nil
		s.ctx = nil
	}
	s.mu.Unlock()
}

func (s *AccountCandyTestService) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	defer s.workers.Wait()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.wake:
		}
		if ctx.Err() != nil {
			return
		}
		s.dispatchRadar(ctx)
		timer.Reset(5 * time.Second)
	}
}

func (s *AccountCandyTestService) dispatch(ctx context.Context) {
	if err := s.expireAbandoned(ctx); err != nil {
		return
	}
	cfg, err := s.GetConfig(ctx)
	s.mu.Lock()
	for _, worker := range s.active {
		if worker.claimed && (err != nil || cfg.generation != worker.generation) {
			worker.cancel()
		}
	}
	s.mu.Unlock()
	if err != nil || !cfg.Enabled {
		return
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id FROM accounts a LEFT JOIN pk_account_candy_state st ON st.account_id=a.id
		WHERE a.deleted_at IS NULL AND a.platform='openai' AND a.status='active' AND a.schedulable=true
		AND COALESCE(a.extra->'synthetic_ui_test','false'::jsonb) <> 'true'::jsonb
		AND (a.expires_at IS NULL OR a.expires_at>NOW() OR a.auto_pause_on_expired=false)
		AND (st.next_run_at IS NULL OR st.next_run_at<=NOW()) AND (st.lease_until IS NULL OR st.lease_until<=NOW())
		AND NOT EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=$1)
		ORDER BY COALESCE(st.next_run_at,a.created_at),a.id LIMIT 1000`, cfg.TargetGroupID)
	if err != nil {
		return
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			_ = rows.Close()
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return
	}
	for _, id := range ids {
		s.mu.Lock()
		full := len(s.active) >= accountCandyWorkers
		s.mu.Unlock()
		if full {
			return
		}
		_, _ = s.start(ctx, id, "scheduled")
	}
}

func (s *AccountCandyTestService) Trigger(ctx context.Context, accountID int64) (*AccountCandyTestResult, error) {
	return s.start(ctx, accountID, "manual")
}

func (s *AccountCandyTestService) start(ctx context.Context, accountID int64, source string) (*AccountCandyTestResult, error) {
	s.mu.Lock()
	if s.ctx == nil || s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, infraerrors.ServiceUnavailable("ACCOUNT_CANDY_UNAVAILABLE", "账号糖果测试服务尚未启动")
	}
	if _, ok := s.active[accountID]; ok {
		s.mu.Unlock()
		return nil, infraerrors.Conflict("ACCOUNT_CANDY_RUNNING", "该账号正在测试")
	}
	if len(s.active) >= accountCandyWorkers {
		s.mu.Unlock()
		return nil, infraerrors.Conflict("ACCOUNT_CANDY_BUSY", "账号测试繁忙，请稍后再试")
	}
	workerCtx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	worker := &accountCandyWorker{cancel: cancel}
	s.active[accountID] = worker
	s.workers.Add(1)
	s.mu.Unlock()
	cleanup := func() { cancel(); s.mu.Lock(); delete(s.active, accountID); s.mu.Unlock(); s.workers.Done() }
	claimCtx, claimCancel := context.WithCancel(ctx)
	stopClaimCancel := context.AfterFunc(workerCtx, claimCancel)
	result, cfg, err := s.claim(claimCtx, accountID, source)
	stopClaimCancel()
	claimCancel()
	if err != nil {
		cleanup()
		return nil, err
	}
	s.mu.Lock()
	worker.generation = cfg.generation
	worker.claimed = true
	s.mu.Unlock()
	go func() {
		defer cleanup()
		started := time.Now()
		latest, configErr := s.GetConfig(workerCtx)
		var text string
		var callErr error
		if configErr != nil {
			callErr = configErr
		} else if latest.generation != cfg.generation {
			callErr = context.Canceled
		} else {
			text, callErr = s.testService.runAccountCandyProbe(workerCtx, result.probeAccount, result.credentialAccount, cfg)
		}
		elapsed := time.Since(started).Milliseconds()
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer finishCancel()
		for attempt := 0; attempt < 3; attempt++ {
			if err = s.finish(finishCtx, result, cfg, text, callErr, elapsed); err == nil {
				return
			}
			select {
			case <-finishCtx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
		slog.Error("account candy result persistence failed", "account_id", accountID, "result_id", result.ID, "error", err)
	}()
	return result, nil
}

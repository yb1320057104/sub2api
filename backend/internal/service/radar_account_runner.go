package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *AccountCandyTestService) dispatchRadar(ctx context.Context) {
	cfg, err := s.GetRadarConfig(ctx)
	s.mu.Lock()
	for _, w := range s.active {
		if w.claimed && (err != nil || w.generation != cfg.Generation) {
			w.cancel()
		}
	}
	s.mu.Unlock()
	if err != nil {
		return
	}
	if s.expireRadar(ctx) != nil {
		return
	}
	if !cfg.Enabled || len(cfg.GroupRules) == 0 {
		return
	}
	rawRules, err := json.Marshal(cfg.GroupRules)
	if err != nil {
		return
	}
	// Read live account management on every scheduled dispatch. Never cache the
	// roster: manually added accounts join the next eligible round immediately.
	rows, err := s.db.QueryContext(ctx, `SELECT a.id FROM accounts a LEFT JOIN pk_radar_account_state st ON st.account_id=a.id WHERE a.deleted_at IS NULL AND a.platform='openai' AND a.status='active' AND a.schedulable=true AND COALESCE(a.extra->'synthetic_ui_test','false'::jsonb)<>'true'::jsonb AND(a.expires_at IS NULL OR a.expires_at>NOW() OR a.auto_pause_on_expired=false)AND(st.next_run_at IS NULL OR st.next_run_at<=NOW())AND(st.lease_until IS NULL OR st.lease_until<=NOW())AND EXISTS(SELECT 1 FROM account_groups ag JOIN groups g ON g.id=ag.group_id AND g.deleted_at IS NULL AND g.platform='openai' AND g.status='active' JOIN jsonb_array_elements($1::jsonb) rule ON (rule->>'source_group_id')::bigint=ag.group_id WHERE ag.account_id=a.id)ORDER BY COALESCE(st.next_run_at,a.created_at),a.id LIMIT 1000`, string(rawRules))
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
		_, _ = s.StartRadar(ctx, id, "scheduled")
	}
}

func (s *AccountCandyTestService) StartRadar(ctx context.Context, id int64, source string) (*RadarRun, error) {
	if source != "manual" && source != "scheduled" {
		return nil, infraerrors.BadRequest("RADAR_SOURCE", "触发来源无效")
	}
	if _, err := s.GetRadarConfig(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.ctx == nil || s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, infraerrors.ServiceUnavailable("RADAR_UNAVAILABLE", "账号观测服务尚未启动")
	}
	if _, ok := s.active[id]; ok {
		s.mu.Unlock()
		return nil, infraerrors.Conflict("RADAR_RUNNING", "该账号正在测试")
	}
	if len(s.active) >= accountCandyWorkers {
		s.mu.Unlock()
		return nil, infraerrors.Conflict("RADAR_BUSY", "账号测试繁忙，请稍后再试")
	}
	workerCtx, cancel := context.WithCancel(s.ctx)
	worker := &accountCandyWorker{cancel: cancel}
	s.active[id] = worker
	s.workers.Add(1)
	s.mu.Unlock()
	cleanup := func() { cancel(); s.mu.Lock(); delete(s.active, id); s.mu.Unlock(); s.workers.Done() }
	claimCtx, claimCancel := context.WithCancel(ctx)
	stop := context.AfterFunc(workerCtx, claimCancel)
	run, cfg, err := s.claimRadar(claimCtx, id, source)
	stop()
	claimCancel()
	if err != nil {
		cleanup()
		return nil, err
	}
	s.mu.Lock()
	worker.generation = cfg.Generation
	worker.claimed = true
	s.mu.Unlock()
	snapshot := *run
	snapshot.ModelResults = append([]RadarModelResult(nil), run.ModelResults...)
	go func() { defer cleanup(); s.executeRadar(workerCtx, &snapshot, cfg) }()
	return run, nil
}

func (s *AccountCandyTestService) executeRadar(parent context.Context, run *RadarRun, cfg *RadarConfig) {
	ctx, cancel := context.WithTimeout(parent, 6*time.Minute)
	defer cancel()
	started := time.Now()
	heartDone := make(chan struct{})
	heartStopped := make(chan struct{})
	go func() {
		defer close(heartStopped)
		timer := time.NewTicker(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-heartDone:
				return
			case <-ctx.Done():
				return
			case <-timer.C:
				check, c := context.WithTimeout(ctx, 10*time.Second)
				err := s.renewRadar(check, run)
				if err == nil {
					err = s.checkRadarIdentity(check, run)
				}
				c()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	completions := make(chan radarModelCompletion, len(run.ModelResults))
	var failedCommit sync.Mutex
	for _, model := range run.ModelResults {
		go func(model RadarModelResult) {
			completions <- s.executeRadarModel(ctx, run, cfg, model, &failedCommit)
		}(model)
	}
	var callErr error
	for range run.ModelResults {
		result := <-completions
		if result.err != nil && callErr == nil {
			callErr = result.err
			cancel()
		}
		model := result.model
		if model.EgressCheckedAt != nil && (run.EgressCheckedAt == nil || model.EgressCheckedAt.After(*run.EgressCheckedAt)) {
			run.EgressIP, run.EgressCheckedAt, run.EgressError = model.EgressIP, model.EgressCheckedAt, model.EgressError
		} else if run.EgressCheckedAt == nil && model.EgressError != "" {
			run.EgressError = model.EgressError
		}
	}
	if ctx.Err() != nil {
		callErr = ctx.Err()
	}
	close(heartDone)
	<-heartStopped
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer finishCancel()
	var finishErr error
	for attempt := 0; attempt < 3; attempt++ {
		finishErr = s.finishRadar(finishCtx, ctx, run, cfg, callErr, time.Since(started).Milliseconds())
		if finishErr == nil {
			return
		}
		select {
		case <-finishCtx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	slog.Error("radar result persistence failed", "account_id", run.AccountID, "run_id", run.ID, "error", finishErr)
}

// Cancel remaining requests when account credentials, model routing or proxy
// settings change. Each request continues to use its immutable claimed route.
func (s *AccountCandyTestService) checkRadarIdentity(ctx context.Context, run *RadarRun) error {
	account, err := s.accountRepo.GetByID(ctx, run.AccountID)
	if err != nil {
		return err
	}
	credential := account
	if account.IsCredentialShadow() {
		credential, err = resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return err
		}
	}
	identity, err := accountCandySnapshotIdentity(account, credential)
	if err != nil {
		return err
	}
	if identity != run.identity {
		return context.Canceled
	}
	return s.checkRadarGroupSnapshot(ctx, run)
}

package service

import (
	"context"
	"log/slog"
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
	if !cfg.Enabled {
		return
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id FROM accounts a LEFT JOIN pk_radar_account_state st ON st.account_id=a.id WHERE a.deleted_at IS NULL AND a.platform='openai' AND a.status='active' AND a.schedulable=true AND COALESCE(a.extra->'synthetic_ui_test','false'::jsonb)<>'true'::jsonb AND(a.expires_at IS NULL OR a.expires_at>NOW() OR a.auto_pause_on_expired=false)AND(st.next_run_at IS NULL OR st.next_run_at<=NOW())AND(st.lease_until IS NULL OR st.lease_until<=NOW())AND NOT EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=$1)ORDER BY COALESCE(st.next_run_at,a.created_at),a.id LIMIT 1000`, cfg.TargetGroupID)
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
	ctx, cancel := context.WithTimeout(parent, time.Duration(len(run.ModelResults))*5*time.Minute+time.Minute)
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
	var callErr error
	for i := range run.ModelResults {
		if err := ctx.Err(); err != nil {
			callErr = err
			break
		}
		if err := s.renewRadar(ctx, run); err != nil {
			callErr = err
			break
		}
		if err := s.checkRadarIdentity(ctx, run); err != nil {
			callErr = err
			break
		}
		model := &run.ModelResults[i]
		now := time.Now()
		model.StartedAt = &now
		model.Status = "running"
		if err := s.writeRadarModel(ctx, run, model); err != nil {
			callErr = err
			break
		}
		modelCtx, modelCancel := context.WithTimeout(ctx, 5*time.Minute)
		model.EgressIP, model.EgressCheckedAt, model.EgressError = probeRadarEgress(modelCtx, run.probeAccount)
		run.EgressIP, run.EgressCheckedAt, run.EgressError = model.EgressIP, model.EgressCheckedAt, model.EgressError
		if err := s.writeRadarModel(modelCtx, run, model); err != nil {
			modelCancel()
			callErr = err
			break
		}
		probeCfg := &AccountCandyTestConfig{Model: model.Model, Effort: model.Effort}
		text, err := s.testService.runAccountCandyProbe(modelCtx, run.probeAccount, run.credentialAccount, probeCfg)
		modelCancel()
		finished := time.Now()
		elapsed := finished.Sub(now).Milliseconds()
		model.FinishedAt = &finished
		model.DurationMS = &elapsed
		if err != nil {
			model.Status = "failed"
			model.ErrorMessage = accountCandySafeError(err)
		} else {
			model.Status = "completed"
			model.ResponseText = text
			model.Answer = intelligenceExtractAnswer(text)
			passed := model.Answer != nil && *model.Answer == 21
			model.Passed = &passed
		}
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 10*time.Second)
		persistErr := s.writeRadarModel(persistCtx, run, model)
		persistCancel()
		if persistErr != nil {
			callErr = persistErr
			break
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
	return nil
}

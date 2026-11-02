package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type radarModelCompletion struct {
	model RadarModelResult
	err   error
}

// Each model owns a fresh, identity-checked account object. Account model-mapping
// caches are mutable, so parallel requests must not share an Account instance.
func (s *AccountCandyTestService) radarModelAccounts(ctx context.Context, run *RadarRun) (*Account, *Account, error) {
	account, err := s.accountRepo.GetByID(ctx, run.AccountID)
	if err != nil {
		return nil, nil, err
	}
	credential := account
	if account.IsCredentialShadow() {
		credential, err = resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return nil, nil, err
		}
	}
	identity, err := accountCandySnapshotIdentity(account, credential)
	if err != nil {
		return nil, nil, err
	}
	if identity != run.identity {
		return nil, nil, context.Canceled
	}
	if err = s.checkRadarGroupSnapshot(ctx, run); err != nil {
		return nil, nil, err
	}
	return account, credential, nil
}

func (s *AccountCandyTestService) executeRadarModel(ctx context.Context, run *RadarRun, cfg *RadarConfig, model RadarModelResult, failedCommit *sync.Mutex) radarModelCompletion {
	result := radarModelCompletion{model: model}
	m := &result.model
	account, credential, err := s.radarModelAccounts(ctx, run)
	if err != nil {
		result.err = err
		return result
	}
	now := time.Now()
	m.StartedAt, m.Status = &now, "running"
	if result.err = s.writeRadarModel(ctx, run, m); result.err != nil {
		return result
	}
	modelCtx, modelCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer modelCancel()
	text, callErr := runAccountCandyWithProxyRetry(modelCtx, s.db, s.testService, account, credential, &AccountCandyTestConfig{Model: m.Model, Effort: m.Effort}, func(attemptCtx context.Context, route *Account, attempt int) error {
		if err := s.checkRadarIdentity(attemptCtx, run); err != nil {
			return err
		}
		m.egressMode = "direct"
		if route.ProxyID != nil {
			m.egressMode = "proxy"
		}
		m.ErrorMessage = ""
		if attempt > 1 {
			m.ErrorMessage = fmt.Sprintf("连接失败，已切换临时代理 #%d；正在进行第%d/%d次尝试", *route.ProxyID, attempt, accountCandyProxyAttempts)
		}
		m.EgressIP, m.EgressCheckedAt, m.EgressError = probeRadarEgress(attemptCtx, route)
		if err := s.writeRadarModel(attemptCtx, run, m); err != nil {
			return err
		}
		return s.checkRadarIdentity(attemptCtx, run)
	})
	finished := time.Now()
	elapsed := finished.Sub(now).Milliseconds()
	m.FinishedAt, m.DurationMS = &finished, &elapsed
	if callErr != nil {
		m.Status = "failed"
		m.ErrorMessage = strings.ReplaceAll(accountCandySafeError(callErr), "未转组", "此失败不作为转组依据")
	} else {
		m.Status, m.ResponseText, m.ErrorMessage = "completed", text, ""
		m.Answer = intelligenceExtractAnswer(text)
		passed := m.Answer != nil && *m.Answer == 21
		m.Passed = &passed
	}
	// Persist the first complete wrong answer and its group move atomically;
	// another model's network completion is never a prerequisite.
	// Serialize only these short commits, before opening their serializable
	// transactions; simultaneous failures must not exhaust each other's retries.
	if m.Status == "completed" && m.Passed != nil && !*m.Passed {
		failedCommit.Lock()
		defer failedCommit.Unlock()
	}
	for attempt := 0; attempt < 3; attempt++ {
		persistCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if m.Status == "completed" && m.Passed != nil && !*m.Passed {
			result.err = s.completeFailedRadarModel(persistCtx, run, cfg, m)
		} else {
			result.err = s.writeRadarModel(persistCtx, run, m)
		}
		cancel()
		if result.err == nil || ctx.Err() != nil {
			return result
		}
		select {
		case <-ctx.Done():
			return result
		case <-time.After(50 * time.Millisecond):
		}
	}
	return result
}

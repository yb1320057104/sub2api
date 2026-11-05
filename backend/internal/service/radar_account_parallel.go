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
	// All model goroutines share the claim-time baseline. A late-starting
	// model must not accept a sibling's newly recorded 401 as its initial state.
	accountState, credentialState, stateErr := radarCandyClaimStates(run)
	now := time.Now()
	m.StartedAt, m.Status = &now, "running"
	if result.err = s.writeRadarModel(ctx, run, m); result.err != nil {
		return result
	}
	modelCtx, modelCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer modelCancel()
	modelCtx, recovery := withAccountCandyRecovery(modelCtx, func(progressCtx context.Context, message string) error {
		m.ErrorMessage = message
		return s.writeRadarModel(progressCtx, run, m)
	})
	text, callErr := runAccountCandyWithProxyRetry(modelCtx, s.db, s.testService, account, credential, &AccountCandyTestConfig{Model: m.Model, Effort: m.Effort}, func(attemptCtx context.Context, route *Account, attempt int) error {
		if stateErr != nil {
			return stateErr
		}
		if err := s.checkRadarIdentity(attemptCtx, run); err != nil {
			return err
		}
		if err := s.checkRadarRequestState(attemptCtx, accountState, credentialState); err != nil {
			return err
		}
		m.egressMode = "direct"
		if route.ProxyID != nil {
			m.egressMode = "proxy"
		}
		m.ErrorMessage = ""
		if attempt > 1 {
			m.ErrorMessage = fmt.Sprintf("连接失败，已选取尚未分配账号的备用代理 #%d；正在进行第%d次尝试", *route.ProxyID, attempt)
		}
		m.EgressIP, m.EgressCheckedAt, m.EgressError = run.proxySession.observe(attemptCtx, route, attempt)
		if err := s.writeRadarModel(attemptCtx, run, m); err != nil {
			return err
		}
		if err := s.checkRadarIdentity(attemptCtx, run); err != nil {
			return err
		}
		if err := s.checkRadarRequestState(attemptCtx, accountState, credentialState); err != nil {
			return err
		}
		if m.EgressError != "" {
			return &accountCandyRetryableNetworkError{}
		}
		return nil
	}, run.proxySession)
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
	if message := recovery.message(); message != "" {
		if m.ErrorMessage != "" {
			m.ErrorMessage += "；"
		}
		m.ErrorMessage += message
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

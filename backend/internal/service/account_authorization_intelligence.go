package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type AuthorizationIntelligenceResult struct {
	MappedModel     string     `json:"mapped_model,omitempty"`
	ResponseText    string     `json:"response_text,omitempty"`
	EgressIP        string     `json:"egress_ip,omitempty"`
	EgressMode      string     `json:"egress_mode,omitempty"`
	EgressCheckedAt *time.Time `json:"egress_checked_at,omitempty"`
	EgressError     string     `json:"egress_error,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	Model           string     `json:"model"`
	Effort          string     `json:"effort"`
	Status          string     `json:"status"`
	Passed          *bool      `json:"passed"`
	Answer          *int       `json:"answer"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	DurationMS      int64      `json:"duration_ms"`
}

func authorizationIntelligenceModels(settings AuthorizationSubmit) ([]RadarModel, error) {
	models := append([]RadarModel(nil), settings.IntelligenceModels...)
	if len(models) == 0 {
		// Jobs queued before multi-model admission keep their original snapshot.
		models = []RadarModel{{Model: settings.IntelligenceModel, Effort: settings.IntelligenceEffort, Enabled: true}}
	}
	cfg := &RadarConfig{IntervalMinutes: 60, Models: models}
	if err := validateRadarConfig(cfg); err != nil {
		return nil, authorizationBrowserFailure("intelligence_failed", "任务中的测智模型配置无效，请重新提交")
	}
	enabled := make([]RadarModel, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		if model.Enabled {
			enabled = append(enabled, model)
		}
	}
	return enabled, nil
}

// An answered failure is conclusive even if another model fails technically.
// Admission passes only when every configured model completes and passes.
func authorizationIntelligenceVerdict(results []AuthorizationIntelligenceResult) (*bool, *int) {
	allPassed := len(results) > 0
	for _, result := range results {
		if result.Status != "completed" || result.Passed == nil {
			allPassed = false
			continue
		}
		if !*result.Passed {
			passed := false
			if len(results) == 1 {
				return &passed, result.Answer
			}
			return &passed, nil
		}
	}
	if allPassed {
		if len(results) == 1 {
			return &allPassed, results[0].Answer
		}
		return &allPassed, nil
	}
	return nil, nil
}

func (s *AccountAuthorizationService) saveAuthorizationIntelligence(ctx context.Context, job *authorizationJob, results []AuthorizationIntelligenceResult, retainCancelled bool) error {
	passed, answer := authorizationIntelligenceVerdict(results)
	var startedAt, finishedAt *time.Time
	complete := len(results) > 0
	for _, result := range results {
		if result.StartedAt != nil && (startedAt == nil || result.StartedAt.Before(*startedAt)) {
			startedAt = result.StartedAt
		}
		if result.FinishedAt == nil {
			complete = false
		} else if finishedAt == nil || result.FinishedAt.After(*finishedAt) {
			finishedAt = result.FinishedAt
		}
	}
	if !complete {
		finishedAt = nil
	}
	raw, err := json.Marshal(map[string]any{"intelligence_results": results, "intelligence_passed": passed, "intelligence_answer": answer, "intelligence_started_at": startedAt, "intelligence_finished_at": finishedAt})
	if err != nil {
		return err
	}
	// A cancelled job may retain late probe results, but its status, ownership
	// and account groups are never changed by this metadata-only update.
	r, err := s.db.ExecContext(ctx, `UPDATE pk_authorization_jobs SET settings=settings||$3::jsonb WHERE id=$1 AND owner_token=$2 AND (status='running' OR ($4 AND status='cancelled'))`, job.id, job.token, string(raw), retainCancelled)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return context.Canceled
	}
	return nil
}

func (s *AccountAuthorizationService) executeAuthorizationIntelligence(ctx context.Context, accountID int64, snapshot *authorizationGateSnapshot, model RadarModel) (result AuthorizationIntelligenceResult) {
	result = AuthorizationIntelligenceResult{Model: model.Model, Effort: model.Effort, Status: "failed"}
	started := time.Now()
	result.StartedAt = &started
	defer func() {
		finished := time.Now()
		result.FinishedAt = &finished
		result.DurationMS = finished.Sub(started).Milliseconds()
		if recover() != nil {
			result.Status, result.ErrorMessage = "failed", "模型测智异常中断，此错误不作为不及格依据"
			result.Passed, result.Answer = nil, nil
		}
	}()
	// Account model-mapping caches are mutable. Each concurrent probe loads
	// its own account object and checks it against the saved identity.
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		result.ErrorMessage = "无法读取测智账号，请重新检查账号状态"
		return result
	}
	if !snapshot.matches(account) {
		result.ErrorMessage = "账号凭据、分组或状态已变化，此模型未继续测试"
		return result
	}
	result.EgressMode = "direct"
	if account.ProxyID != nil {
		result.EgressMode = "proxy"
	}
	result.EgressIP, result.EgressCheckedAt, result.EgressError = probeRadarEgress(ctx, account)
	result.MappedModel = account.GetMappedModel(model.Model)
	text, err := s.tests.runAccountCandyProbe(ctx, account, account, &AccountCandyTestConfig{Model: model.Model, Effort: model.Effort})
	if err != nil {
		result.ErrorMessage = redactCandyAccountError(accountCandySafeError(err), account)
		result.ErrorMessage = strings.ReplaceAll(result.ErrorMessage, "未转组", "此错误不作为不及格依据")
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			result.Status = "cancelled"
		}
		return result
	}
	if strings.TrimSpace(text) == "" {
		result.ErrorMessage = "未取得完整的最终回复，此错误不作为不及格依据"
		return result
	}
	result.Status, result.Answer, result.ResponseText = "completed", intelligenceExtractAnswer(text), text
	passed := result.Answer != nil && *result.Answer == 21
	result.Passed = &passed
	return result
}

func (s *AccountAuthorizationService) runAuthorizationIntelligenceModels(ctx context.Context, job *authorizationJob, account *Account, snapshot *authorizationGateSnapshot, models []RadarModel, progress func(string, string) error) ([]AuthorizationIntelligenceResult, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	results := make([]AuthorizationIntelligenceResult, len(models))
	startedAt := time.Now()
	for i, model := range models {
		results[i] = AuthorizationIntelligenceResult{Model: model.Model, Effort: model.Effort, Status: "running", StartedAt: &startedAt}
	}
	if err := s.saveAuthorizationIntelligence(ctx, job, results, false); err != nil {
		return results, err
	}
	type completion struct {
		index  int
		result AuthorizationIntelligenceResult
	}
	completed := make(chan completion, len(models))
	for i, model := range models {
		go func(index int, current RadarModel) {
			completed <- completion{index: index, result: s.executeAuthorizationIntelligence(probeCtx, account.ID, snapshot, current)}
		}(i, model)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	deadline := probeCtx.Done()
	var stopErr error
	stop := func(err error) {
		if stopErr == nil {
			stopErr = err
		}
		cancel()
	}
	for count := 0; count < len(models); {
		select {
		case result := <-completed:
			results[result.index] = result.result
			count++
			if stopErr == nil {
				if err := progress("intelligence_testing", fmt.Sprintf("授权成功，正在并发糖果测智；已完成 %d/%d 个模型", count, len(models))); err != nil {
					stop(err)
				} else if err = s.saveAuthorizationIntelligence(ctx, job, results, false); err != nil {
					stop(err)
				}
			}
		case <-ticker.C:
			if stopErr == nil {
				if err := progress("", ""); err != nil {
					stop(err)
				}
			}
		case <-deadline:
			if err := ctx.Err(); err != nil {
				stop(err)
			}
			// The model budget expiring is a technical failure. Let all model
			// results settle; an already-answered failure still decides admission.
			deadline = nil
		}
	}
	if stopErr != nil {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = s.saveAuthorizationIntelligence(cleanup, job, results, true)
		return results, stopErr
	}
	return results, nil
}

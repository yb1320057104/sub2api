package service

import (
	"context"
	"encoding/json"
	"time"
)

const openAIAPIKeyFallbackStatusSetting = "openai_apikey_fallback_runtime_status"

type openAIAPIKeyFallbackSharedStatus struct {
	Revision string                     `json:"revision"`
	Status   OpenAIAPIKeyFallbackStatus `json:"status"`
}

// The activated worker publishes observations; API replicas only read them.
// The revision keeps observations from an earlier configuration out of the UI.
func (s *AccountOpsService) persistOpenAIAPIKeyFallbackStatus(ctx context.Context, revision string, status OpenAIAPIKeyFallbackStatus) error {
	if s == nil || !s.sharedJobsEnabled.Load() || s.settings == nil || status.LastCheckedAt == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(openAIAPIKeyFallbackSharedStatus{Revision: revision, Status: status})
	if err != nil {
		return err
	}
	query, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return s.settings.Set(query, openAIAPIKeyFallbackStatusSetting, string(raw))
}

// OpenAIAPIKeyFallbackStatusForAdmin does not schedule evaluations or trust an
// API process's empty local status as evidence that the worker is healthy.
func (s *AccountOpsService) OpenAIAPIKeyFallbackStatusForAdmin(ctx context.Context, cfg OAuthAutoConfig) OpenAIAPIKeyFallbackStatus {
	if !cfg.OpenAIAPIKeyFallback.Enabled {
		return OpenAIAPIKeyFallbackStatus{}
	}
	unavailable := OpenAIAPIKeyFallbackStatus{Metric: cfg.OpenAIAPIKeyFallback.Metric,
		WindowMinutes: cfg.OpenAIAPIKeyFallback.WindowMinutes, LastErrorCategory: "shared_status_unavailable"}
	if s == nil || s.settings == nil || ctx.Err() != nil {
		return unavailable
	}
	query, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	raw, err := s.settings.GetValue(query, openAIAPIKeyFallbackStatusSetting)
	if err != nil || len(raw) == 0 || len(raw) > 16384 {
		return unavailable
	}
	var shared openAIAPIKeyFallbackSharedStatus
	if json.Unmarshal([]byte(raw), &shared) != nil || shared.Status.LastCheckedAt == nil {
		return unavailable
	}
	if shared.Revision != cfg.Revision {
		unavailable.LastErrorCategory = "shared_status_stale"
		return unavailable
	}
	now := time.Now()
	if shared.Status.LastCheckedAt.Before(now.Add(-45*time.Second)) || shared.Status.LastCheckedAt.After(now.Add(5*time.Second)) {
		shared.Status.LastErrorCategory = "shared_status_stale"
	}
	return shared.Status
}

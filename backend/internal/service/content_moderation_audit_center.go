package service

import (
	"crypto/sha256"
	"sort"
	"time"
)

// AuditCenterConfigView uses the existing redacted view and resolves inherited
// exemptions from the same transaction snapshot as the other audit settings.
func (s *ContentModerationService) AuditCenterConfigView(raw, inheritedAllowlist string) (*ContentModerationConfigView, error) {
	cfg, err := parseContentModerationConfig(raw)
	if err != nil {
		return nil, err
	}
	users, err := contentModerationAllowlist(cfg, inheritedAllowlist)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(users))
	for id := range users {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	view := s.engineConfigView(cfg)
	view.UserAllowlistIDs = ids
	for _, profile := range view.EngineConfigs {
		profile.UserAllowlistIDs = append([]int64{}, ids...)
	}
	return view, nil
}

// Changing only the shared feature gate must not rewrite or revalidate unrelated
// stored engine settings. It activates their existing normalized representation.
func (s *ContentModerationService) PrepareAuditCenterStoredActivation(raw string, riskEnabled bool, inheritedAllowlist string) (func(), error) {
	cfg, err := parseContentModerationConfig(raw)
	if err != nil {
		return nil, err
	}
	return s.PrepareAuditCenterActivation(&PreparedContentModerationConfig{Config: cfg, Raw: []byte(raw)}, riskEnabled, inheritedAllowlist)
}

// PrepareAuditCenterActivation validates everything before commit. The returned
// closure only installs an in-memory snapshot, so a failed transaction cannot
// alter gateway policy and a successful commit has no fallible activation step.
func (s *ContentModerationService) PrepareAuditCenterActivation(prepared *PreparedContentModerationConfig, riskEnabled bool, inheritedAllowlist string) (func(), error) {
	users, err := contentModerationAllowlist(prepared.Config, inheritedAllowlist)
	if err != nil {
		return nil, err
	}
	snapshot := &contentModerationRuntimeSnapshot{
		allowlistedUsers: users, riskControlEnabled: riskEnabled,
		config:         prepared.Config.effectiveEngine(prepared.Config.Engine),
		keywordMatcher: newContentModerationKeywordMatcher(prepared.Config.BlockedKeywords),
		configDigest:   sha256.Sum256(prepared.Raw), loadedAt: time.Now(),
	}
	return func() {
		s.runtimeRefreshMu.Lock()
		s.runtimeSnapshot.Store(snapshot)
		s.runtimeRefreshRetryAt.Store(0)
		s.moderationProxyCache.Store(nil)
		s.runtimeRefreshMu.Unlock()
	}, nil
}

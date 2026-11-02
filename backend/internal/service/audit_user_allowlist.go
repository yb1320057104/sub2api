package service

import (
	"context"
	"errors"
	"sort"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const MaxAuditUserAllowlistIDs = 1000

func NormalizeAuditUserAllowlist(ids []int64) ([]int64, error) {
	if len(ids) > MaxAuditUserAllowlistIDs {
		return nil, infraerrors.BadRequest("AUDIT_USER_ALLOWLIST_INVALID", "豁免用户最多 1000 个")
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, infraerrors.BadRequest("AUDIT_USER_ALLOWLIST_INVALID", "豁免用户 ID 必须为正整数")
		}
	}
	return append([]int64{}, normalizeInt64IDs(ids)...), nil
}

func contentModerationAllowlist(cfg *ContentModerationConfig, legacy string) (map[int64]struct{}, error) {
	if cfg.UserAllowlistIDs == nil {
		return ParseCyberPolicyUserAllowlist(legacy)
	}
	ids, err := NormalizeAuditUserAllowlist(cfg.UserAllowlistIDs)
	if err != nil {
		return nil, err
	}
	users := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		users[id] = struct{}{}
	}
	return users, nil
}

func (s *ContentModerationService) configViewWithUserAllowlist(ctx context.Context, cfg *ContentModerationConfig) (*ContentModerationConfigView, error) {
	legacy := ""
	if cfg.UserAllowlistIDs == nil {
		value, err := s.settingRepo.GetValue(ctx, SettingKeyCyberPolicyUserAllowlist)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			return nil, err
		}
		legacy = value
	}
	users, err := contentModerationAllowlist(cfg, legacy)
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

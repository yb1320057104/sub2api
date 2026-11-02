package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuditUserAllowlistValidation(t *testing.T) {
	ids, err := NormalizeAuditUserAllowlist([]int64{9, 3, 9})
	require.NoError(t, err)
	require.Equal(t, []int64{3, 9}, ids)
	empty, err := NormalizeAuditUserAllowlist(nil)
	require.NoError(t, err)
	require.NotNil(t, empty)
	for _, invalid := range [][]int64{{0}, {-1}, make([]int64, MaxAuditUserAllowlistIDs+1)} {
		_, err := NormalizeAuditUserAllowlist(invalid)
		require.Error(t, err)
	}
}

func TestContentModerationIndependentAllowlistPreservesLegacyUntilExplicitSave(t *testing.T) {
	ctx := context.Background()
	settings := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled: "true", SettingKeyCyberPolicyUserAllowlist: "12,34",
	}}
	svc := &ContentModerationService{settingRepo: settings, repo: &contentModerationTestRepo{}, asyncQueue: make(chan contentModerationTask, 8)}
	view, err := svc.GetConfig(ctx)
	require.NoError(t, err)
	require.True(t, view.UserAllowlistInherited)
	require.Equal(t, []int64{12, 34}, view.UserAllowlistIDs)
	for _, engine := range view.EngineConfigs {
		require.Equal(t, view.UserAllowlistIDs, engine.UserAllowlistIDs)
		require.True(t, engine.UserAllowlistInherited)
	}
	_, err = svc.refreshRuntimeSnapshot(ctx)
	require.NoError(t, err)
	empty := []int64{}
	view, err = svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{UserAllowlistIDs: &empty})
	require.NoError(t, err)
	require.False(t, view.UserAllowlistInherited)
	require.Empty(t, view.UserAllowlistIDs)
	require.Empty(t, svc.runtimeSnapshot.Load().allowlistedUsers, "explicit clear must remove inherited members immediately")
	var saved ContentModerationConfig
	require.NoError(t, json.Unmarshal([]byte(settings.values[SettingKeyContentModerationConfig]), &saved))
	require.NotNil(t, saved.UserAllowlistIDs, "[] must survive storage and differ from absent/null")
	settings.values[SettingKeyCyberPolicyUserAllowlist] = "56"
	view, err = svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{})
	require.NoError(t, err)
	require.False(t, view.UserAllowlistInherited)
	require.Empty(t, view.UserAllowlistIDs, "omitting the field must preserve the independent empty list")
	ids := []int64{12, 12}
	view, err = svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{UserAllowlistIDs: &ids})
	require.NoError(t, err)
	require.Equal(t, []int64{12}, view.UserAllowlistIDs)
	require.Contains(t, svc.runtimeSnapshot.Load().allowlistedUsers, int64(12))
	require.NotContains(t, svc.runtimeSnapshot.Load().allowlistedUsers, int64(56))
	cloned := cloneContentModerationConfig(&saved)
	require.NotNil(t, cloned.UserAllowlistIDs)
}

func TestContentModerationIndependentAllowlistOverridesGlobalAndRetainsFinding(t *testing.T) {
	ctx := context.Background()
	cfg := defaultContentModerationConfig()
	cfg.Enabled, cfg.AutoBanEnabled, cfg.EmailOnHit = true, true, true
	cfg.Mode, cfg.KeywordBlockingMode = ContentModerationModePreBlock, ContentModerationKeywordModeKeywordOnly
	cfg.GroupIDs, cfg.UserAllowlistIDs, cfg.BlockedKeywords = []int64{7}, []int64{34}, []string{"blocked prompt"}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	settings := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled: "true", SettingKeyContentModerationConfig: string(raw), SettingKeyCyberPolicyUserAllowlist: "12",
	}}
	repo := &banCountArgsTestRepo{}
	svc := &ContentModerationService{settingRepo: settings, repo: repo, asyncQueue: make(chan contentModerationTask, 4)}
	for _, userID := range []int64{34, 12} {
		decision, err := svc.Check(ctx, ContentModerationCheckInput{UserID: userID, GroupID: contentModerationTestGroupID(), Protocol: ContentModerationProtocolOpenAIChat,
			Body: []byte(`{"messages":[{"role":"user","content":"blocked prompt","user_id":34}]}`)})
		require.NoError(t, err)
		require.Equal(t, userID != 34, decision.Blocked, "only the trusted caller ID and independent list can exempt the request")
		task := <-svc.asyncQueue
		require.True(t, task.log.Flagged)
		if userID == 34 {
			require.Equal(t, ContentModerationModeRiskControlLogOnly, task.log.Mode)
			svc.persistContentModerationLog(ctx, task.config, task.log, task.inputHash, task.recordHash, task.applySideEffects)
			require.Empty(t, repo.snapshotCountCalls())
		} else {
			require.NotEqual(t, ContentModerationModeRiskControlLogOnly, task.log.Mode)
		}
	}
	logs := repo.snapshotLogs()
	require.Len(t, logs, 1)
	require.False(t, logs[0].AutoBanned)
	require.False(t, logs[0].EmailSent)
}

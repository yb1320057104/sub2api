package securityaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/auditrisk"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAuditCenterSharedPatchPreservesSecretsAndExplicitEmptyValues(t *testing.T) {
	current := DefaultStorageConfig()
	current.GroupIDs, current.UserAllowlistIDs = []int64{2, 4}, []int64{9}
	current.RiskPolicy = &auditrisk.Policy{LowThreshold: 20, MediumThreshold: 40, HighThreshold: 80, LowAction: "allow", MediumAction: "block", HighAction: "charge_allow"}
	current.Endpoints = []StorageEndpoint{{ID: "main", Name: "saved", Protocol: "openai_compatible", BaseURL: "https://audit.example.test", Model: "saved-audit-model", TokenCiphertext: "enc:stored-private-token", TimeoutMS: 1000, InputLimit: 4000}}
	content, prompt, ranking := splitAuditCenterUpdate(AuditCenterUpdate{Content: &service.UpdateContentModerationConfigInput{}}, current)
	require.NotNil(t, content)
	require.Nil(t, prompt)
	require.Nil(t, ranking, "an omitted shared section must not normalize mismatched scopes")
	content, prompt, ranking = splitAuditCenterUpdate(AuditCenterUpdate{Shared: &AuditCenterShared{GroupIDs: &[]int64{}, UserAllowlistIDs: &[]int64{}, RiskPolicy: json.RawMessage("null")}}, current)
	require.Empty(t, *content.GroupIDs)
	require.Empty(t, *ranking.UserAllowlistIDs)
	require.NotNil(t, prompt.UserAllowlistIDs, "explicit empty must not inherit old exemptions")
	manager := NewConfigManager(nil, nil, nil, prefixEncryptor{}, testTotpKeyConfig())
	next, err := manager.buildNextStorage(current, *prompt, 1)
	require.NoError(t, err)
	require.Empty(t, next.GroupIDs)
	require.Empty(t, next.UserAllowlistIDs)
	require.Nil(t, next.RiskPolicy)
	require.Equal(t, "enc:stored-private-token", next.Endpoints[0].TokenCiphertext)
	require.Equal(t, current.AuditInstructions, next.AuditInstructions)
	require.Equal(t, current.LocalRules, next.LocalRules)
	require.Equal(t, []int64{2, 4}, current.GroupIDs, "preparation must not mutate the stored snapshot")
	// Updating only shared groups preserves both policies and endpoint secrets.
	_, prompt, _ = splitAuditCenterUpdate(AuditCenterUpdate{Shared: &AuditCenterShared{GroupIDs: &[]int64{7}}}, current)
	next, err = manager.buildNextStorage(current, *prompt, 1)
	require.NoError(t, err)
	require.Equal(t, current.RiskPolicy, next.RiskPolicy)
	require.Equal(t, current.Endpoints, next.Endpoints)
}

func TestAuditCenterDiagnosticsDoesNotPromiseAsyncOrLowScoreCharges(t *testing.T) {
	policy := &auditrisk.Policy{LowThreshold: 20, MediumThreshold: 40, HighThreshold: 80, LowAction: "allow", MediumAction: "allow", HighAction: "charge_allow"}
	cfg := &AuditCenterConfig{RiskControlEnabled: true,
		Content: &service.ContentModerationConfigView{Enabled: true, Mode: "pre_block", KeywordBlockingMode: "keyword_async_api", GroupIDs: []int64{4}, APIKeyConfigured: true, SampleRate: 100, RiskPolicy: policy, BlockedKeywords: []string{"example"}, KeywordScores: map[string]int{"example": 20}},
		Prompt:  PublicConfig{Enabled: true, Engine: PromptEngineLocalRules, GroupIDs: []int64{2, 4}, RiskPolicy: policy, LocalRules: DefaultLocalRules()},
		Ranking: &service.CyberPolicySettings{GroupIDs: []int64{2, 4, 5}, GroupRules: []service.CyberPolicyGroupRule{{GroupID: 2, AutoDeductEnabled: true}, {GroupID: 4, AutoDeductEnabled: true}}},
	}
	differences, groups := auditCenterDiagnostics(cfg)
	require.Contains(t, differences, "group_ids")
	require.Equal(t, []int64{2, 4, 5}, []int64{groups[0].GroupID, groups[1].GroupID, groups[2].GroupID})
	require.Contains(t, groups[0].Reasons, "scope_mismatch")
	require.Contains(t, groups[1].Reasons, "prompt_async_observation_only")
	require.Contains(t, groups[1].Reasons, "content_keyword_async_api_observation_only")
	require.Contains(t, groups[1].Reasons, "content_keyword_scores_do_not_charge")
	require.Contains(t, groups[1].Reasons, "prompt_low_allow")
	require.Equal(t, "disabled", groups[1].ChargeState, "remote observation and low-score local allow cannot debit")
	cfg.Prompt.BlockingEnabled = true
	_, groups = auditCenterDiagnostics(cfg)
	require.Equal(t, "ready", groups[1].ChargeState, "enabled default high-score local rules can reach charge_allow")
	cfg.RiskControlEnabled = false
	_, groups = auditCenterDiagnostics(cfg)
	require.Equal(t, "disabled", groups[1].ChargeState)
	require.Contains(t, groups[1].Reasons, "risk_control_disabled")
}

func auditCenterTestSnapshot() *auditCenterSnapshot {
	values := map[string]string{}
	for _, key := range auditCenterSettingKeys {
		values[key] = ""
	}
	values[SettingKeyRiskControl] = "true"
	return &auditCenterSnapshot{values: values, ranking: &service.CyberPolicySettings{Multiplier: "1.000000", AutoDisableThreshold: 10, ResetOnManualEnable: true, GroupIDs: []int64{}, UserAllowlistIDs: []int64{}, GroupRules: []service.CyberPolicyGroupRule{}, SettingsRevision: 1}}
}

func expectAuditCenterSnapshot(mock sqlmock.Sqlmock, snapshot *auditCenterSnapshot, locked bool) {
	rows := sqlmock.NewRows([]string{"key", "value"})
	for _, key := range auditCenterSettingKeys {
		rows.AddRow(key, snapshot.values[key])
	}
	query := "SELECT key,value FROM settings"
	if locked {
		query += ".*FOR UPDATE"
	}
	mock.ExpectQuery(query).WillReturnRows(rows).RowsWillBeClosed()
	r := snapshot.ranking
	raw, _ := json.Marshal(r.GroupRules)
	query = "SELECT multiplier::text,auto_disable_enabled"
	if locked {
		query += ".*FOR UPDATE"
	}
	mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"multiplier", "enabled", "threshold", "reset", "groups", "revision", "users", "rules"}).
		AddRow(r.Multiplier, r.AutoDisableEnabled, r.AutoDisableThreshold, r.ResetOnManualEnable, "{}", r.SettingsRevision, "{}", raw))
}

func TestAuditCenterAtomicSaveConflictRollbackAndActivation(t *testing.T) {
	for _, mode := range []string{"stale-before-lock", "stale-after-lock", "prompt-write-failed", "ranking-write-failed", "import-summary-failed", "commit-failed", "success"} {
		t.Run(mode, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			manager := NewConfigManager(db, nil, nil, prefixEncryptor{}, testTotpKeyConfig())
			center := &AuditCenterService{prompt: manager, content: &service.ContentModerationService{}, ranking: service.NewCyberPolicyService(db, nil, nil)}
			current := auditCenterTestSnapshot()
			input := AuditCenterUpdate{ExpectedRevision: current.revision(), Shared: &AuditCenterShared{UserAllowlistIDs: &[]int64{}}}
			disabled := false
			input.RiskControlEnabled = &disabled
			if mode == "stale-before-lock" {
				input.ExpectedRevision = "stale"
			}
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(promptAuditConfigLockKey).WillReturnResult(sqlmock.NewResult(0, 1))
			expectAuditCenterSnapshot(mock, current, false)
			if mode != "stale-before-lock" {
				for range auditCenterSettingKeys {
					mock.ExpectExec("INSERT INTO settings").WillReturnResult(sqlmock.NewResult(0, 1))
				}
				if mode == "stale-after-lock" {
					current.values[SettingKeyRiskControl] = "false"
				}
				expectAuditCenterSnapshot(mock, current, true)
				if mode != "stale-after-lock" {
					mock.ExpectExec("UPDATE settings SET value").WithArgs(SettingKeyRiskControl, "false").WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectExec("UPDATE settings SET value").WithArgs(service.SettingKeyContentModerationConfig, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
					promptWrite := mock.ExpectExec("UPDATE settings SET value").WithArgs(SettingKeyPromptAuditConfig, sqlmock.AnyArg())
					if mode == "prompt-write-failed" {
						promptWrite.WillReturnError(errors.New("prompt write failed"))
					} else {
						promptWrite.WillReturnResult(sqlmock.NewResult(0, 1))
						rankingWrite := mock.ExpectQuery("UPDATE cyber_policy_settings SET")
						if mode == "ranking-write-failed" {
							rankingWrite.WillReturnError(errors.New("ranking write failed"))
						} else {
							rankingWrite.WillReturnRows(sqlmock.NewRows([]string{"multiplier", "enabled", "threshold", "reset", "groups", "revision", "users", "rules"}).AddRow("1.000000", false, 10, true, "{}", 2, "{}", []byte("[]")))
							mock.ExpectQuery("WITH recent AS.*cyber_policy_auto_charges.*CASE WHEN status='deducted'").WillReturnRows(sqlmock.NewRows([]string{"status", "reason", "count", "amount"}).AddRow("zero_cost", "", 2, "0"))
							importRead := mock.ExpectQuery("WITH recent AS.*audit_settlement_imports.*CASE WHEN status='deducted'")
							if mode == "import-summary-failed" {
								importRead.WillReturnError(errors.New("import summary unavailable"))
							} else {
								importRead.WillReturnRows(sqlmock.NewRows([]string{"status", "reason", "count", "amount"}).AddRow("deducted", "", 1, "0.20000000"))
								mock.ExpectQuery("WITH recent AS.*cyber_policy_audit_observations").WillReturnRows(sqlmock.NewRows([]string{"source", "count"}).AddRow("prompt_observation", 1908))
							}
						}
					}
				}
			}
			switch mode {
			case "success":
				mock.ExpectCommit()
			case "commit-failed":
				mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
			default:
				mock.ExpectRollback()
			}
			result, err := center.Save(context.Background(), input, 3)
			if mode == "success" {
				require.NoError(t, err)
				require.NotEqual(t, input.ExpectedRevision, result.Revision)
				require.Equal(t, int64(2), result.Prompt.ConfigVersion)
				require.Equal(t, int64(2), result.Ranking.SettingsRevision)
				require.Equal(t, int64(2), manager.snapshot.Load().storage.ConfigVersion)
				require.False(t, result.RiskControlEnabled)
				require.False(t, manager.snapshot.Load().active.RiskControlEnabled)
				require.Equal(t, 10000, result.ChargeSummarySampleLimit)
				require.True(t, result.ChargeSummarySampled)
				require.Equal(t, "0.20000000", result.ImportSummary[0].Amount)
				require.Equal(t, int64(1908), result.ObservationSummary[0].Count)
				require.Equal(t, "0.00000000", result.ObservationSummary[0].Amount)
				require.Equal(t, "observation_only", result.ObservationSummary[0].Reason)
			} else {
				require.Error(t, err)
				require.Nil(t, result)
				require.Nil(t, manager.snapshot.Load(), "a rolled-back/failed transaction must not activate new policy")
				if strings.HasPrefix(mode, "stale-") {
					require.Equal(t, "AUDIT_CENTER_CONFIG_CONFLICT", infraerrors.Reason(err))
				}
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAuditCenterGlobalGateOnlyPreservesBothConfigsAndRankingRevision(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	manager := NewConfigManager(db, nil, nil, prefixEncryptor{}, testTotpKeyConfig())
	center := &AuditCenterService{prompt: manager, content: &service.ContentModerationService{}, ranking: service.NewCyberPolicyService(db, nil, nil)}
	current := auditCenterTestSnapshot()
	disabled := false
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	expectAuditCenterSnapshot(mock, current, false)
	for range auditCenterSettingKeys {
		mock.ExpectExec("INSERT INTO settings").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	expectAuditCenterSnapshot(mock, current, true)
	mock.ExpectExec("UPDATE settings SET value").WithArgs(SettingKeyRiskControl, "false").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("WITH recent AS.*cyber_policy_auto_charges").WillReturnRows(sqlmock.NewRows([]string{"status", "reason", "count", "amount"}))
	mock.ExpectQuery("WITH recent AS.*audit_settlement_imports").WillReturnRows(sqlmock.NewRows([]string{"status", "reason", "count", "amount"}))
	mock.ExpectQuery("WITH recent AS.*cyber_policy_audit_observations").WillReturnRows(sqlmock.NewRows([]string{"source", "count"}))
	mock.ExpectCommit()
	result, err := center.Save(context.Background(), AuditCenterUpdate{ExpectedRevision: current.revision(), RiskControlEnabled: &disabled}, 3)
	require.NoError(t, err)
	require.False(t, result.RiskControlEnabled)
	require.False(t, manager.snapshot.Load().active.RiskControlEnabled)
	require.Equal(t, int64(1), result.Prompt.ConfigVersion)
	require.Equal(t, int64(1), result.Ranking.SettingsRevision)
	require.NotEqual(t, current.revision(), result.Revision)
	require.NoError(t, mock.ExpectationsWereMet(), "global-only updates must never write the unchanged module settings")
}

func TestAuditCenterViewRedactsSecretsAndKeepsInheritedExemptions(t *testing.T) {
	snapshot := auditCenterTestSnapshot()
	snapshot.values[service.SettingKeyContentModerationConfig] = `{"api_keys":["sk-content-should-stay-private"],"group_ids":[4]}`
	snapshot.values[service.SettingKeyCyberPolicyUserAllowlist] = "7,9"
	prompt := DefaultStorageConfig()
	prompt.Endpoints = []StorageEndpoint{{ID: "main", Name: "saved", Protocol: "openai_compatible", BaseURL: "https://audit.example.test", Model: "saved-audit-model", TokenCiphertext: "enc:prompt-should-stay-private", TimeoutMS: 1000, InputLimit: 4000}}
	raw, err := json.Marshal(prompt)
	require.NoError(t, err)
	snapshot.values[SettingKeyPromptAuditConfig] = string(raw)
	center := &AuditCenterService{prompt: NewConfigManager(nil, nil, nil, prefixEncryptor{}, testTotpKeyConfig()), content: &service.ContentModerationService{}}
	result, err := center.view(snapshot)
	require.NoError(t, err)
	require.Equal(t, []int64{7, 9}, result.Content.UserAllowlistIDs)
	require.True(t, result.Content.UserAllowlistInherited)
	require.Contains(t, result.Differences, "user_allowlist_ids")
	raw, err = json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "sk-content-should-stay-private")
	require.NotContains(t, string(raw), "prompt-should-stay-private")
	require.NotContains(t, string(raw), "token_ciphertext")
	require.True(t, result.Prompt.Endpoints[0].HasToken)
}

func TestAuditCenterIdentityUnionKeepsIndependentModuleLimits(t *testing.T) {
	for _, tc := range []struct {
		kind    string
		size    int
		allowed bool
	}{
		{"groups", 4000, true}, {"groups", 4001, false}, {"users", 3000, true}, {"users", 3001, false},
	} {
		t.Run(fmt.Sprintf("%s_%d", tc.kind, tc.size), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectBegin()
			tx, err := db.Begin()
			require.NoError(t, err)
			ids := make([]int64, tc.size)
			rows := sqlmock.NewRows([]string{"id"})
			for i := range ids {
				ids[i] = int64(i + 1)
				rows.AddRow(ids[i])
			}
			if tc.allowed {
				mock.ExpectQuery("SELECT id FROM " + tc.kind).WillReturnRows(rows).RowsWillBeClosed()
			}
			mock.ExpectRollback()
			err = auditCenterLockIDs(context.Background(), tx, tc.kind, ids)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.Equal(t, "AUDIT_CENTER_IDENTITIES_INVALID", infraerrors.Reason(err))
			}
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

type prefixEncryptor struct{}

func promptTestGroupID(value int64) *int64 { return &value }

func (prefixEncryptor) Encrypt(value string) (string, error) { return "enc:" + value, nil }
func (prefixEncryptor) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "enc:") {
		return "", errors.New("cipher: message authentication failed")
	}
	return value[4:], nil
}

// testTotpKeyConfig mirrors a deployment with a fixed TOTP_ENCRYPTION_KEY so
// unit tests may persist endpoint tokens.
func testTotpKeyConfig() *config.Config {
	return &config.Config{Totp: config.TotpConfig{EncryptionKeyConfigured: true}}
}

func TestDefaultConfigIsOff(t *testing.T) {
	storage, err := ParseStorageConfig("")
	require.NoError(t, err)
	require.False(t, storage.Enabled)
	require.False(t, storage.BlockingLatestTurnOnly)
	active, err := ActiveFromStorage(storage, true, prefixEncryptor{})
	require.NoError(t, err)
	require.Equal(t, ModeOff, active.EffectiveMode())
	require.Equal(t, AllScannerIDs, storage.Scanners)
	publicJSON, err := json.Marshal(PublicFromStorage(storage, true, nil))
	require.NoError(t, err)
	require.Contains(t, string(publicJSON), `"group_ids":[]`)
	require.Contains(t, string(publicJSON), `"endpoints":[]`)
}

func TestBlockingLatestTurnOnlyConfigRoundTrip(t *testing.T) {
	manager := &ConfigManager{encryptor: prefixEncryptor{}, encryptionKeyConfigured: true}
	request := UpdateConfigRequest{
		ExpectedConfigVersion: 1, Enabled: true, BlockingEnabled: true, BlockingLatestTurnOnly: true,
		Strategy: "priority", WorkerCount: 1, QueueCapacity: 10, Scanners: []string{"pii"}, AllGroups: true,
		Endpoints: []UpdateEndpoint{{
			ID: "guard-1", Name: "Guard", Protocol: "openai_compatible", BaseURL: "http://127.0.0.1:8080",
			Model: DefaultGuardModel, TimeoutMS: 1000, InputLimit: 1000, Enabled: true,
		}},
	}
	next, err := manager.buildNextStorage(DefaultStorageConfig(), request, 9)
	require.NoError(t, err)
	require.True(t, next.BlockingLatestTurnOnly)
	require.Contains(t, changeSummary(next), `"blocking_latest_turn_only":true`)

	active, err := ActiveFromStorage(next, true, prefixEncryptor{})
	require.NoError(t, err)
	require.True(t, active.BlockingLatestTurnOnly)
	public := PublicFromStorage(next, true, nil)
	require.True(t, public.BlockingLatestTurnOnly)
}

func TestConfigRejectsBlockingWithoutAudit(t *testing.T) {
	storage := DefaultStorageConfig()
	storage.BlockingEnabled = true
	require.Error(t, validateStorageConfig(storage))
}

func TestPublicConfigNeverMarshalsToken(t *testing.T) {
	storage := DefaultStorageConfig()
	storage.Endpoints = []StorageEndpoint{{ID: "one", Name: "One", Protocol: "openai_compatible", BaseURL: "http://127.0.0.1:8080", Model: DefaultGuardModel, TokenCiphertext: "GUARD_TOKEN_CANARY_SECRET", TimeoutMS: 1000, InputLimit: 1000, Enabled: true}}
	public := PublicFromStorage(storage, true, nil)
	raw, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "GUARD_TOKEN_CANARY_SECRET")
	require.NotContains(t, string(raw), "ciphertext")
	require.True(t, public.Endpoints[0].HasToken)
}

func TestConfigRuntimeLoadErrorIsStableBoundedAndSecretFree(t *testing.T) {
	const canary = "CONFIG_LOAD_CANARY_SECRET"
	manager := &ConfigManager{clock: fixedClock{}}
	manager.recordLoadError(errors.New("decrypt failed for token " + canary + " Authorization: Bearer " + canary))
	_, _, _, message := manager.RuntimeState()
	require.Equal(t, stableErrorMessage("config_load_failed"), message)
	require.NotContains(t, message, canary)
	require.LessOrEqual(t, len([]rune(message)), 160)
}

func TestConfigManagerPublicRequiresSuccessfullyLoadedSnapshot(t *testing.T) {
	t.Run("absent persisted setting is legitimate default", func(t *testing.T) {
		manager := NewConfigManager(nil, staticSettingRepository{values: map[string]string{
			SettingKeyPromptAuditConfig: "",
			SettingKeyRiskControl:       "false",
		}}, nil, prefixEncryptor{}, testTotpKeyConfig())
		require.NoError(t, manager.Reload(context.Background()))

		public, err := manager.Public()
		require.NoError(t, err)
		require.Equal(t, int64(1), public.ConfigVersion)
		require.False(t, public.Enabled)
	})

	t.Run("unparseable persisted config is unavailable", func(t *testing.T) {
		const canary = "persisted-token-canary"
		manager := NewConfigManager(nil, staticSettingRepository{values: map[string]string{
			// Endpoint without id/name fails validation, so no trustworthy
			// snapshot can be installed from this raw value.
			SettingKeyPromptAuditConfig: `{"enabled":true,"config_version":9,"endpoints":[{"token_ciphertext":"` + canary + `"}]}`,
			SettingKeyRiskControl:       "true",
		}}, nil, prefixEncryptor{}, testTotpKeyConfig())
		require.Error(t, manager.Reload(context.Background()))

		public, err := manager.Public()
		require.Error(t, err)
		require.Empty(t, public)
		require.Equal(t, ErrorCodeConfigUnavailable, infraerrors.Reason(err))
		require.NotContains(t, err.Error(), canary)
	})

	t.Run("reload failure preserves last successfully loaded snapshot", func(t *testing.T) {
		storage := DefaultStorageConfig()
		storage.ConfigVersion = 4
		storage.ChangeSummary = "trusted snapshot"
		raw, err := json.Marshal(storage)
		require.NoError(t, err)
		repository := &switchableSettingRepository{staticSettingRepository: staticSettingRepository{values: map[string]string{
			SettingKeyPromptAuditConfig: string(raw),
			SettingKeyRiskControl:       "false",
		}}}
		manager := NewConfigManager(nil, repository, nil, prefixEncryptor{}, testTotpKeyConfig())
		require.NoError(t, manager.Reload(context.Background()))
		repository.loadErr = errors.New("settings unavailable")
		require.Error(t, manager.Reload(context.Background()))

		public, err := manager.Public()
		require.NoError(t, err)
		require.Equal(t, int64(4), public.ConfigVersion)
		require.Equal(t, "trusted snapshot", public.ChangeSummary)
	})
}

// Regression coverage for issue #4887: a persisted config whose endpoint token
// can no longer be decrypted (encryption key changed or auto-generated per
// boot) must stay visible and editable for admins instead of falling back to a
// default v1 config that makes every save fail the CAS version check.
func TestConfigManagerUndecryptableTokenKeepsConfigVisibleAndRecoverable(t *testing.T) {
	const canary = "persisted-token-canary"
	persisted := `{"enabled":true,"blocking_enabled":false,"config_version":9,"endpoints":[{"id":"g1","name":"Guard","protocol":"openai_compatible","base_url":"http://127.0.0.1:8080","model":"m","token_ciphertext":"` + canary + `","timeout_ms":1000,"input_limit":1000,"enabled":true}]}`
	manager := NewConfigManager(nil, staticSettingRepository{values: map[string]string{
		SettingKeyPromptAuditConfig: persisted,
		SettingKeyRiskControl:       "true",
	}}, nil, prefixEncryptor{}, testTotpKeyConfig())
	require.NoError(t, manager.Reload(context.Background()), "an undecryptable token must not fail the whole config load")

	public, err := manager.Public()
	require.NoError(t, err)
	require.Equal(t, int64(9), public.ConfigVersion, "admins must see the real persisted version so CAS saves can succeed")
	require.Len(t, public.Endpoints, 1)
	require.True(t, public.Endpoints[0].HasToken)
	require.Equal(t, "invalid", public.Endpoints[0].TokenStatus)
	raw, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(raw), canary)

	active, ok := manager.Active()
	require.True(t, ok)
	require.Len(t, active.Endpoints, 1)
	require.False(t, active.Endpoints[0].Enabled, "an endpoint with an undecryptable token must not be used at runtime")
	require.True(t, active.Endpoints[0].TokenInvalid)
	require.Empty(t, active.Endpoints[0].Token)
	require.Empty(t, active.EnabledEndpoints())
	require.Equal(t, []string{"g1"}, active.InvalidTokenEndpointIDs())

	expected, activeVersion, _, _ := manager.RuntimeState()
	require.Equal(t, int64(9), expected)
	require.Equal(t, int64(9), activeVersion)
}

func TestConfigManagerUndecryptableTokenStillFailsClosedForBlockingIntent(t *testing.T) {
	persisted := `{"enabled":true,"blocking_enabled":true,"group_ids":[1],"config_version":9,"endpoints":[{"id":"g1","name":"Guard","protocol":"openai_compatible","base_url":"http://127.0.0.1:8080","model":"m","token_ciphertext":"undecryptable","timeout_ms":1000,"input_limit":1000,"enabled":true}]}`
	manager := NewConfigManager(nil, staticSettingRepository{values: map[string]string{
		SettingKeyPromptAuditConfig: persisted,
		SettingKeyRiskControl:       "true",
	}}, nil, prefixEncryptor{}, testTotpKeyConfig())
	require.NoError(t, manager.Reload(context.Background()))
	require.Equal(t, ModeBlocking, manager.EffectiveMode())

	service := &PromptService{config: manager, evaluator: NewGuardEvaluator(NewOpenAICompatibleScanner(), nil, nil)}
	decision, err := service.Evaluate(context.Background(), Request{
		GroupID:  promptTestGroupID(1),
		Protocol: "openai_chat_completions",
		Body:     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	})
	require.Error(t, err, "blocking intent with no usable endpoint must not let requests pass unaudited")
	require.Nil(t, decision)
	var guardErr *GuardError
	require.ErrorAs(t, err, &guardErr)
	require.Equal(t, "audit_token_invalid", guardErr.Code)
}

func TestBuildNextStoragePreserveReplaceAndClearToken(t *testing.T) {
	manager := &ConfigManager{encryptor: prefixEncryptor{}, encryptionKeyConfigured: true}
	current := DefaultStorageConfig()
	current.Endpoints = []StorageEndpoint{{ID: "one", Name: "One", Protocol: "openai_compatible", BaseURL: "http://127.0.0.1:8080", Model: DefaultGuardModel, TokenCiphertext: "enc:old", TimeoutMS: 1000, InputLimit: 1000}}
	base := UpdateConfigRequest{ExpectedConfigVersion: 1, Strategy: "priority", WorkerCount: 1, QueueCapacity: 10, Scanners: []string{"PII"}, AllGroups: true,
		Endpoints: []UpdateEndpoint{{ID: "one", Name: "One", Protocol: "openai_compatible", BaseURL: "http://127.0.0.1:8080", TimeoutMS: 1000, InputLimit: 1000}}}
	preserved, err := manager.buildNextStorage(current, base, 9)
	require.NoError(t, err)
	require.Equal(t, "enc:old", preserved.Endpoints[0].TokenCiphertext)
	replacedReq := base
	replacedReq.Endpoints = append([]UpdateEndpoint(nil), base.Endpoints...)
	replacedReq.Endpoints[0].Token = "new"
	replaced, err := manager.buildNextStorage(current, replacedReq, 9)
	require.NoError(t, err)
	require.Equal(t, "enc:new", replaced.Endpoints[0].TokenCiphertext)
	clearedReq := base
	clearedReq.Endpoints = append([]UpdateEndpoint(nil), base.Endpoints...)
	clearedReq.Endpoints[0].ClearToken = true
	cleared, err := manager.buildNextStorage(current, clearedReq, 9)
	require.NoError(t, err)
	require.Empty(t, cleared.Endpoints[0].TokenCiphertext)
}

// Without a fixed encryption key the per-boot auto-generated key would make a
// freshly saved token undecryptable after the next restart (issue #4887), so
// saving a new token must be rejected with an actionable error. Preserving or
// clearing an existing ciphertext stays allowed so admins can still edit or
// disable the feature.
func TestBuildNextStorageRejectsNewTokenWithoutConfiguredEncryptionKey(t *testing.T) {
	manager := &ConfigManager{encryptor: prefixEncryptor{}, encryptionKeyConfigured: false}
	current := DefaultStorageConfig()
	current.Endpoints = []StorageEndpoint{{ID: "one", Name: "One", Protocol: "openai_compatible", BaseURL: "http://127.0.0.1:8080", Model: DefaultGuardModel, TokenCiphertext: "enc:old", TimeoutMS: 1000, InputLimit: 1000}}
	base := UpdateConfigRequest{ExpectedConfigVersion: 1, Strategy: "priority", WorkerCount: 1, QueueCapacity: 10, Scanners: []string{"PII"}, AllGroups: true,
		Endpoints: []UpdateEndpoint{{ID: "one", Name: "One", Protocol: "openai_compatible", BaseURL: "http://127.0.0.1:8080", TimeoutMS: 1000, InputLimit: 1000}}}

	newTokenReq := base
	newTokenReq.Endpoints = append([]UpdateEndpoint(nil), base.Endpoints...)
	newTokenReq.Endpoints[0].Token = "fresh-token"
	_, err := manager.buildNextStorage(current, newTokenReq, 9)
	require.Error(t, err)
	require.Equal(t, ErrorCodeEncryptionKeyRequired, infraerrors.Reason(err))

	preserved, err := manager.buildNextStorage(current, base, 9)
	require.NoError(t, err)
	require.Equal(t, "enc:old", preserved.Endpoints[0].TokenCiphertext)

	clearedReq := base
	clearedReq.Endpoints = append([]UpdateEndpoint(nil), base.Endpoints...)
	clearedReq.Endpoints[0].ClearToken = true
	cleared, err := manager.buildNextStorage(current, clearedReq, 9)
	require.NoError(t, err)
	require.Empty(t, cleared.Endpoints[0].TokenCiphertext)
}

func TestEffectiveModeTruthTable(t *testing.T) {
	tests := []struct {
		risk, enabled, blocking bool
		want                    Mode
	}{
		{false, false, false, ModeOff}, {false, true, true, ModeOff}, {true, false, false, ModeOff},
		{true, true, false, ModeAsync}, {true, true, true, ModeBlocking},
	}
	for _, tt := range tests {
		cfg := ActiveConfig{RiskControlEnabled: tt.risk, Enabled: tt.enabled, BlockingEnabled: tt.blocking}
		require.Equal(t, tt.want, cfg.EffectiveMode())
	}
}

func TestConfigManagerColdStartOnlyFailsClosedForExplicitBlockingIntent(t *testing.T) {
	manager := &ConfigManager{}

	manager.observeExpectedState(`{"enabled":true,"blocking_enabled":false,"config_version":42}`, true)
	require.Equal(t, int64(42), manager.expected.Load())
	require.Equal(t, ModeOff, manager.EffectiveMode(), "an async config version must not imply blocking")
	require.False(t, manager.BlockingActivationDegraded())

	manager.observeExpectedState(`{"enabled":true,"blocking_enabled":true,"config_version":43}`, false)
	require.Equal(t, ModeOff, manager.EffectiveMode(), "the global risk-control switch still gates blocking")

	manager.observeExpectedState(`{"enabled":true,"blocking_enabled":true,"config_version":44}`, true)
	require.Equal(t, ModeBlocking, manager.EffectiveMode())
	require.True(t, manager.BlockingActivationDegraded())

	manager.observeExpectedState(`{"enabled":true`, true)
	require.Equal(t, ModeBlocking, manager.EffectiveMode(), "undecodable storage must not erase the last known strict intent")
}

func TestConfigManagerStaleWeakerSnapshotFailsClosedWhenBlockingExpected(t *testing.T) {
	manager := &ConfigManager{}
	async := ActiveConfig{RiskControlEnabled: true, Enabled: true, BlockingEnabled: false, ConfigVersion: 1, GroupIDs: []int64{1}}
	manager.snapshot.Store(&activeConfigSnapshot{active: async, storage: DefaultStorageConfig(), loadedAt: fixedClock{}.Now()})
	manager.expected.Store(2)
	manager.expectedBlocking.Store(true)

	require.True(t, manager.BlockingActivationDegraded())
	require.Equal(t, ModeBlocking, manager.EffectiveMode())

	service := &PromptService{config: manager, evaluator: NewGuardEvaluator(nil, nil, nil)}
	decision, err := service.Evaluate(context.Background(), Request{GroupID: promptTestGroupID(1), Protocol: "openai_chat_completions", Body: []byte(`{"messages":[{"role":"user","content":"hi"}]}`)})
	require.Error(t, err)
	require.Nil(t, decision)
	var guardErr *GuardError
	require.ErrorAs(t, err, &guardErr)
	require.Equal(t, ErrorCodeConfigUnavailable, guardErr.Code)
}

type errorSettingRepository struct{ staticSettingRepository }

func (errorSettingRepository) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("settings unavailable")
}

type switchableSettingRepository struct {
	staticSettingRepository
	loadErr error
}

func (r *switchableSettingRepository) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	if r.loadErr != nil {
		return nil, r.loadErr
	}
	return r.staticSettingRepository.GetMultiple(ctx, keys)
}

func TestConfigManagerStartupLoadFailureDoesNotBlockWhenBlockingNotIntended(t *testing.T) {
	// Settings unavailable and no prior blocking intent: stay ModeOff so the
	// gateway remains usable and admins can still disable/configure Prompt Audit.
	manager := NewConfigManager(nil, errorSettingRepository{}, nil, prefixEncryptor{}, testTotpKeyConfig())
	err := manager.Start(context.Background())
	require.Error(t, err)
	require.True(t, manager.configUntrusted.Load())
	require.False(t, manager.BlockingActivationDegraded())
	require.Equal(t, ModeOff, manager.EffectiveMode())

	service := &PromptService{config: manager, evaluator: NewGuardEvaluator(nil, nil, nil)}
	decision, evalErr := service.Evaluate(context.Background(), Request{
		Protocol: "openai_chat_completions",
		Body:     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	})
	require.NoError(t, evalErr)
	require.NotNil(t, decision)
	require.Equal(t, DecisionAllow, decision.Kind)
	require.NoError(t, manager.Shutdown(context.Background()))
}

func TestConfigManagerStartupLoadFailureFailsClosedWhenBlockingIntended(t *testing.T) {
	manager := NewConfigManager(nil, errorSettingRepository{}, nil, prefixEncryptor{}, testTotpKeyConfig())
	// Simulate intent observed before a later load failure (e.g. decrypt error).
	manager.observeExpectedState(`{"enabled":true,"blocking_enabled":true,"config_version":3,"group_ids":[1]}`, true)
	manager.markConfigUntrusted()
	require.True(t, manager.BlockingActivationDegraded())
	require.Equal(t, ModeBlocking, manager.EffectiveMode())

	service := &PromptService{config: manager, evaluator: NewGuardEvaluator(nil, nil, nil)}
	decision, err := service.Evaluate(context.Background(), Request{
		GroupID:  promptTestGroupID(1),
		Protocol: "openai_chat_completions",
		Body:     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	})
	require.Error(t, err)
	require.Nil(t, decision)
	var guardErr *GuardError
	require.ErrorAs(t, err, &guardErr)
	require.Equal(t, ErrorCodeConfigUnavailable, guardErr.Code)
}

func TestConfigManagerUntrustedClearsOnSuccessfulDisable(t *testing.T) {
	// After a degraded fail-closed period, saving disabled config must restore ModeOff.
	manager := &ConfigManager{encryptor: prefixEncryptor{}, clock: fixedClock{}}
	manager.observeExpectedState(`{"enabled":true,"blocking_enabled":true,"config_version":5}`, true)
	manager.markConfigUntrusted()
	require.Equal(t, ModeBlocking, manager.EffectiveMode())

	// Install a trusted disabled snapshot the same way Save does after commit.
	disabled := DefaultStorageConfig()
	disabled.ConfigVersion = 6
	disabled.Enabled = false
	disabled.BlockingEnabled = false
	active, err := ActiveFromStorage(disabled, true, manager.encryptor)
	require.NoError(t, err)
	manager.expected.Store(disabled.ConfigVersion)
	manager.expectedBlocking.Store(false)
	manager.snapshot.Store(&activeConfigSnapshot{storage: disabled, active: active, loadedAt: manager.clock.Now()})
	manager.configUntrusted.Store(false)

	require.False(t, manager.BlockingActivationDegraded())
	require.Equal(t, ModeOff, manager.EffectiveMode())

	service := &PromptService{config: manager, evaluator: NewGuardEvaluator(nil, nil, nil)}
	decision, evalErr := service.Evaluate(context.Background(), Request{
		Protocol: "openai_chat_completions",
		Body:     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	})
	require.NoError(t, evalErr)
	require.Equal(t, DecisionAllow, decision.Kind)
}

func TestConfigManagerUntrustedWithoutBlockingDoesNotForceBlockingMode(t *testing.T) {
	manager := &ConfigManager{}
	manager.observeExpectedState(`{"enabled":true,"blocking_enabled":false,"config_version":2}`, true)
	manager.markConfigUntrusted()
	require.False(t, manager.expectedBlocking.Load())
	require.False(t, manager.BlockingActivationDegraded())
	require.Equal(t, ModeOff, manager.EffectiveMode(), "async intent + untrusted must not force blocking unavailable")
}

func TestParseLegacyConfigDefaultsMissingFieldsWithoutEnablingBlocking(t *testing.T) {
	storage, err := ParseStorageConfig(`{"enabled":false,"config_version":9}`)
	require.NoError(t, err)
	require.False(t, storage.BlockingEnabled)
	require.Equal(t, "priority", storage.Strategy)
	require.Equal(t, DefaultWorkerCount, storage.WorkerCount)
	require.Equal(t, DefaultQueueCapacity, storage.QueueCapacity)
	require.Equal(t, AllScannerIDs, storage.Scanners)
	require.False(t, storage.AllGroups)
	require.Empty(t, storage.GroupIDs)
	require.Equal(t, PromptEngineRemote, storage.Engine)
	require.Equal(t, DefaultLocalRules(), storage.LocalRules)
}

func TestUpdateConfigStrictBoundsAndKnownValues(t *testing.T) {
	valid := promptAuditUpdateRequest(1, 1, "")
	require.NoError(t, validateUpdateConfigRequest(valid))

	tests := []struct {
		name   string
		mutate func(*UpdateConfigRequest)
		reason string
	}{
		{name: "strategy", mutate: func(req *UpdateConfigRequest) { req.Strategy = "round_robin" }, reason: "prompt_audit_invalid_strategy"},
		{name: "worker low", mutate: func(req *UpdateConfigRequest) { req.WorkerCount = 0 }, reason: "prompt_audit_invalid_worker_count"},
		{name: "worker high", mutate: func(req *UpdateConfigRequest) { req.WorkerCount = MaxWorkerCount + 1 }, reason: "prompt_audit_invalid_worker_count"},
		{name: "capacity low", mutate: func(req *UpdateConfigRequest) { req.QueueCapacity = 0 }, reason: "prompt_audit_invalid_queue_capacity"},
		{name: "capacity high", mutate: func(req *UpdateConfigRequest) { req.QueueCapacity = MaxQueueCapacity + 1 }, reason: "prompt_audit_invalid_queue_capacity"},
		{name: "unknown scanner", mutate: func(req *UpdateConfigRequest) { req.Scanners = []string{"made_up"} }, reason: "prompt_audit_invalid_scanner"},
		{name: "group positive", mutate: func(req *UpdateConfigRequest) { req.AllGroups = false; req.GroupIDs = []int64{0} }, reason: "prompt_audit_invalid_group"},
		{name: "timeout low", mutate: func(req *UpdateConfigRequest) { req.Endpoints[0].TimeoutMS = MinTimeoutMS - 1 }, reason: "prompt_audit_invalid_timeout"},
		{name: "timeout high", mutate: func(req *UpdateConfigRequest) { req.Endpoints[0].TimeoutMS = MaxTimeoutMS + 1 }, reason: "prompt_audit_invalid_timeout"},
		{name: "input low", mutate: func(req *UpdateConfigRequest) { req.Endpoints[0].InputLimit = MinInputLimit - 1 }, reason: "prompt_audit_invalid_input_limit"},
		{name: "input high", mutate: func(req *UpdateConfigRequest) { req.Endpoints[0].InputLimit = MaxInputLimit + 1 }, reason: "prompt_audit_invalid_input_limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := valid
			req.Scanners = append([]string(nil), valid.Scanners...)
			req.GroupIDs = append([]int64(nil), valid.GroupIDs...)
			req.Endpoints = append([]UpdateEndpoint(nil), valid.Endpoints...)
			tt.mutate(&req)
			err := validateUpdateConfigRequest(req)
			require.Error(t, err)
			require.Equal(t, tt.reason, infraerrors.Reason(err))
		})
	}
}

func TestLocalPromptConfigNeedsRulesButNoNodeAndPreservesExplicitEmpty(t *testing.T) {
	manager := &ConfigManager{}
	req := UpdateConfigRequest{Engine: PromptEngineLocalRules, Enabled: true, BlockingEnabled: true,
		Strategy: "priority", WorkerCount: 1, QueueCapacity: 10, AllGroups: true, GroupIDs: []int64{9, 3, 9},
		AuditInstructions: "custom instructions", LocalRules: DefaultLocalRules()}
	cfg, err := manager.buildNextStorage(DefaultStorageConfig(), req, 7)
	require.NoError(t, err)
	require.Empty(t, cfg.Endpoints)
	require.False(t, cfg.AllGroups)
	require.Equal(t, []int64{3, 9}, cfg.GroupIDs)
	active, err := ActiveFromStorage(cfg, true, nil)
	require.NoError(t, err)
	require.True(t, active.IncludesGroup(promptTestGroupID(3)))
	require.False(t, active.IncludesGroup(promptTestGroupID(7)))
	require.False(t, active.IncludesGroup(nil))
	public := PublicFromStorage(cfg, true, nil)
	require.Equal(t, DefaultLocalRules(), public.DefaultLocalRules)
	require.Equal(t, DefaultAuditInstructions, public.DefaultAuditInstructions)
	require.Equal(t, req.AuditInstructions, public.AuditInstructions)
	public.LocalRules[0].Pattern = "changed"
	require.NotEqual(t, public.LocalRules[0].Pattern, cfg.LocalRules[0].Pattern)

	req.GroupIDs = nil
	cfg, err = manager.buildNextStorage(cfg, req, 7)
	require.NoError(t, err, "an empty selection deliberately disables audit for all groups")
	active, err = ActiveFromStorage(cfg, true, nil)
	require.NoError(t, err)
	require.False(t, active.IncludesGroup(promptTestGroupID(3)))

	req.Engine, req.Scanners = PromptEngineRemote, []string{"pii"}
	_, err = manager.buildNextStorage(cfg, req, 7)
	require.Equal(t, "prompt_audit_endpoint_required", infraerrors.Reason(err))
	req.Engine, req.LocalRules = PromptEngineLocalRules, []LocalRule{}
	_, err = manager.buildNextStorage(cfg, req, 7)
	require.Equal(t, "prompt_audit_local_rules_required", infraerrors.Reason(err))
	req.Enabled, req.BlockingEnabled, req.AuditInstructions = false, false, ""
	cfg, err = manager.buildNextStorage(cfg, req, 7)
	require.NoError(t, err)
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	reloaded, err := ParseStorageConfig(string(raw))
	require.NoError(t, err)
	require.Empty(t, reloaded.LocalRules, "deleted rules must not reappear on reload")
	require.NotNil(t, reloaded.LocalRules)
	require.Empty(t, reloaded.AuditInstructions, "an explicitly cleared editor must stay empty")

	legacy, err := ParseStorageConfig(`{"all_groups":true}`)
	require.NoError(t, err)
	require.Equal(t, PromptEngineRemote, legacy.Engine)
	require.False(t, legacy.AllGroups)
	require.Equal(t, DefaultLocalRules(), legacy.LocalRules)
	require.Equal(t, DefaultAuditInstructions, legacy.AuditInstructions)
}

func TestLocalPromptConfigRejectsInvalidPatternsWithoutDisclosure(t *testing.T) {
	base := LocalRule{ID: "test", Name: "test", Kind: "regex", Pattern: "safe-pattern", Category: "jailbreak", Action: "block", Enabled: true}
	for _, pattern := range []string{"SECRET_CANARY(?=x)", `SECRET_CANARY(a)\1`, "a*", "", strings.Repeat("中", MaxLocalPatternRunes+1)} {
		rule := base
		rule.Pattern = pattern
		_, err := compileLocalRules([]LocalRule{rule})
		require.Equal(t, "prompt_audit_local_rules_invalid", infraerrors.Reason(err))
		require.NotContains(t, err.Error(), "SECRET_CANARY")
	}
	for _, mutate := range []func(*LocalRule){
		func(r *LocalRule) { r.Name = strings.Repeat("a", 101) },
		func(r *LocalRule) { r.Kind = "script" },
		func(r *LocalRule) { r.Action = "allow" },
		func(r *LocalRule) { r.Category = "unknown" },
	} {
		rule := base
		mutate(&rule)
		_, err := compileLocalRules([]LocalRule{rule})
		require.Error(t, err)
	}
	_, err := compileLocalRules([]LocalRule{base, base})
	require.Error(t, err)
	_, err = compileLocalRules(make([]LocalRule, MaxLocalRules+1))
	require.Error(t, err)
	cfg := DefaultStorageConfig()
	cfg.AuditInstructions = strings.Repeat("中", MaxAuditInstructionRunes+1)
	require.Equal(t, "prompt_audit_instructions_invalid", infraerrors.Reason(validateStorageConfig(cfg)))
	cfg.AuditInstructions = "INSTRUCTION_SECRET_CANARY"
	cfg.LocalRules[0].Pattern = "RULE_SECRET_CANARY"
	summary := changeSummary(cfg)
	require.NotContains(t, summary, "SECRET_CANARY")
	var summaryFields map[string]any
	require.NoError(t, json.Unmarshal([]byte(summary), &summaryFields))
	require.Equal(t, float64(len(cfg.LocalRules)), summaryFields["local_rule_count"])
	clone := cloneStorageConfig(cfg)
	clone.LocalRules[0].Pattern = "mutated"
	require.Equal(t, "RULE_SECRET_CANARY", cfg.LocalRules[0].Pattern)
}

// Regression coverage for issue #5732: refreshLoop reloads every 5s, so
// config_loaded must stay a change signal instead of ~17k identical lines a
// day, while still reporting the first load, real config changes and a
// recovery from a failed reload.
func TestConfigLoadedIsLoggedOnlyWhenSomethingChanged(t *testing.T) {
	storage := DefaultStorageConfig()
	storage.ConfigVersion = 4
	raw, err := json.Marshal(storage)
	require.NoError(t, err)
	repository := &switchableSettingRepository{staticSettingRepository: staticSettingRepository{values: map[string]string{
		SettingKeyPromptAuditConfig: string(raw),
		SettingKeyRiskControl:       "false",
	}}}
	manager := NewConfigManager(nil, repository, nil, prefixEncryptor{}, testTotpKeyConfig())

	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	loadedCount := func() int { return strings.Count(output.String(), EventConfigLoaded) }

	require.NoError(t, manager.Reload(context.Background()))
	require.Equal(t, 1, loadedCount(), "the first successful load must be logged")

	require.NoError(t, manager.Reload(context.Background()))
	require.NoError(t, manager.Reload(context.Background()))
	require.Equal(t, 1, loadedCount(), "TTL refreshes of an unchanged config must stay silent")

	repository.values[SettingKeyRiskControl] = "true"
	require.NoError(t, manager.Reload(context.Background()))
	require.Equal(t, 2, loadedCount(), "flipping the global risk control gate must be logged")

	storage.ConfigVersion = 5
	raw, err = json.Marshal(storage)
	require.NoError(t, err)
	repository.values[SettingKeyPromptAuditConfig] = string(raw)
	require.NoError(t, manager.Reload(context.Background()))
	require.Equal(t, 3, loadedCount(), "a new config version must be logged")

	repository.loadErr = errors.New("settings unavailable")
	require.Error(t, manager.Reload(context.Background()))
	require.Equal(t, 3, loadedCount(), "a failed reload must not claim a load")

	repository.loadErr = nil
	require.NoError(t, manager.Reload(context.Background()))
	require.Equal(t, 4, loadedCount(), "recovering from a failed reload must be visible")
}

package securityaudit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// AuditCenterShared is an explicit patch. Omitted values preserve differences;
// empty lists clear all three scopes, and JSON null disables both risk policies.
type AuditCenterShared struct {
	GroupIDs         *[]int64        `json:"group_ids"`
	UserAllowlistIDs *[]int64        `json:"user_allowlist_ids"`
	RiskPolicy       json.RawMessage `json:"risk_policy"`
}

type AuditCenterUpdate struct {
	RiskControlEnabled *bool                                       `json:"risk_control_enabled"`
	ExpectedRevision   string                                      `json:"expected_revision"`
	Shared             *AuditCenterShared                          `json:"shared"`
	Content            *service.UpdateContentModerationConfigInput `json:"content"`
	Prompt             *UpdateConfigRequest                        `json:"prompt"`
	Ranking            *service.CyberPolicySettingsInput           `json:"ranking"`
}

type AuditCenterGroup struct {
	GroupID      int64    `json:"group_id"`
	ContentState string   `json:"content_state"`
	PromptState  string   `json:"prompt_state"`
	RankingState string   `json:"ranking_state"`
	ChargeState  string   `json:"charge_state"`
	Reasons      []string `json:"reasons"`
}

type AuditCenterChargeSummary struct {
	Source string `json:"source,omitempty"`
	Status string `json:"status"`
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
	Amount string `json:"amount"`
}

type AuditCenterConfig struct {
	ObservationSummary       []AuditCenterChargeSummary           `json:"observation_summary"`
	Revision                 string                               `json:"revision"`
	RiskControlEnabled       bool                                 `json:"risk_control_enabled"`
	Content                  *service.ContentModerationConfigView `json:"content"`
	Prompt                   PublicConfig                         `json:"prompt"`
	Ranking                  *service.CyberPolicySettings         `json:"ranking"`
	Differences              []string                             `json:"differences"`
	Groups                   []AuditCenterGroup                   `json:"groups"`
	ChargeSummary            []AuditCenterChargeSummary           `json:"charge_summary"`
	ChargeSummaryFrom        time.Time                            `json:"charge_summary_from"`
	ChargeSummaryTo          time.Time                            `json:"charge_summary_to"`
	ChargeSummarySampleLimit int                                  `json:"charge_summary_sample_limit"`
	ChargeSummarySampled     bool                                 `json:"charge_summary_sampled"`
	ImportSummary            []AuditCenterChargeSummary           `json:"import_summary"`
}

type AuditCenterService struct {
	prompt  *ConfigManager
	content *service.ContentModerationService
	ranking *service.CyberPolicyService
}

type auditCenterSnapshot struct {
	values  map[string]string
	ranking *service.CyberPolicySettings
}

var auditCenterSettingKeys = []string{
	service.SettingKeyContentModerationConfig, SettingKeyPromptAuditConfig,
	SettingKeyRiskControl, service.SettingKeyCyberPolicyUserAllowlist,
}

func (s *auditCenterSnapshot) revision() string {
	// Authoritative raw values participate, including credentials, but only the
	// one-way digest leaves the process. Map encoding has deterministic key order.
	raw, _ := json.Marshal(struct {
		Values  map[string]string            `json:"values"`
		Ranking *service.CyberPolicySettings `json:"ranking"`
	}{s.values, s.ranking})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (s *AuditCenterService) readSnapshot(ctx context.Context, tx *sql.Tx, lock bool) (*auditCenterSnapshot, error) {
	query := `SELECT key,value FROM settings WHERE key=ANY($1::text[]) ORDER BY key`
	if lock {
		query += ` FOR UPDATE`
	}
	rows, err := tx.QueryContext(ctx, query, pq.Array(auditCenterSettingKeys))
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(auditCenterSettingKeys))
	for _, key := range auditCenterSettingKeys {
		values[key] = ""
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			_ = rows.Close()
			return nil, err
		}
		values[key] = value
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	ranking, err := s.ranking.SettingsInTx(ctx, tx, lock)
	if err != nil {
		return nil, err
	}
	return &auditCenterSnapshot{values: values, ranking: ranking}, nil
}

func (s *AuditCenterService) view(snapshot *auditCenterSnapshot) (*AuditCenterConfig, error) {
	content, err := s.content.AuditCenterConfigView(snapshot.values[service.SettingKeyContentModerationConfig], snapshot.values[service.SettingKeyCyberPolicyUserAllowlist])
	if err != nil {
		return nil, err
	}
	prompt, err := ParseStorageConfig(snapshot.values[SettingKeyPromptAuditConfig])
	if err != nil {
		return nil, err
	}
	gate := snapshot.values[SettingKeyRiskControl] == "true"
	active, err := ActiveFromStorage(prompt, gate, s.prompt.encryptor)
	if err != nil {
		return nil, err
	}
	result := &AuditCenterConfig{
		Revision: snapshot.revision(), RiskControlEnabled: gate, Content: content,
		Prompt: PublicFromStorage(prompt, gate, active.InvalidTokenEndpointIDs()), Ranking: snapshot.ranking,
		Differences: []string{}, Groups: []AuditCenterGroup{}, ChargeSummary: []AuditCenterChargeSummary{}, ImportSummary: []AuditCenterChargeSummary{}, ObservationSummary: []AuditCenterChargeSummary{},
	}
	result.Differences, result.Groups = auditCenterDiagnostics(result)
	return result, nil
}

func (s *AuditCenterService) Get(ctx context.Context) (*AuditCenterConfig, error) {
	tx, err := s.prompt.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	snapshot, err := s.readSnapshot(ctx, tx, false)
	if err != nil {
		return nil, err
	}
	result, err := s.view(snapshot)
	if err != nil {
		return nil, err
	}
	if err := s.chargeSummary(ctx, tx, result); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func auditCenterConflict() error {
	return infraerrors.Conflict("AUDIT_CENTER_CONFIG_CONFLICT", "审计配置已被其他页面或管理员更新，请刷新后重试")
}

// splitAuditCenterUpdate applies only explicitly edited shared fields. It does
// not reconstruct omitted module fields from the public (credential-redacted) DTO.
func splitAuditCenterUpdate(input AuditCenterUpdate, current storageConfig) (*service.UpdateContentModerationConfigInput, *UpdateConfigRequest, *service.CyberPolicySettingsInput) {
	content, prompt, ranking := input.Content, input.Prompt, input.Ranking
	if content != nil {
		copy := *content
		content = &copy
	}
	if prompt != nil {
		copy := *prompt
		prompt = &copy
	}
	if ranking != nil {
		copy := *ranking
		ranking = &copy
	}
	shared := input.Shared
	if shared == nil {
		return content, prompt, ranking
	}
	commonLists := shared.GroupIDs != nil || shared.UserAllowlistIDs != nil
	if commonLists || len(shared.RiskPolicy) > 0 {
		if content == nil {
			content = &service.UpdateContentModerationConfigInput{}
		}
		if prompt == nil {
			restored := auditCenterPromptRequest(current)
			prompt = &restored
		}
	}
	if commonLists && ranking == nil {
		ranking = &service.CyberPolicySettingsInput{}
	}
	if shared.GroupIDs != nil {
		content.GroupIDs, ranking.GroupIDs = shared.GroupIDs, shared.GroupIDs
		prompt.GroupIDs = append([]int64{}, (*shared.GroupIDs)...)
	}
	if shared.UserAllowlistIDs != nil {
		content.UserAllowlistIDs, ranking.UserAllowlistIDs = shared.UserAllowlistIDs, shared.UserAllowlistIDs
		prompt.UserAllowlistIDs = append([]int64{}, (*shared.UserAllowlistIDs)...)
	}
	if len(shared.RiskPolicy) > 0 {
		content.RiskPolicy, prompt.RiskPolicy = shared.RiskPolicy, shared.RiskPolicy
	}
	return content, prompt, ranking
}

func auditCenterPromptRequest(current storageConfig) UpdateConfigRequest {
	req := UpdateConfigRequest{
		ExpectedConfigVersion: current.ConfigVersion, Engine: current.Engine,
		AuditInstructions: current.AuditInstructions, LocalRules: cloneLocalRules(current.LocalRules),
		Enabled: current.Enabled, FastMode: current.FastMode, BlockingEnabled: current.BlockingEnabled,
		BlockingLatestTurnOnly: current.BlockingLatestTurnOnly, StorePassEvents: current.StorePassEvents,
		Strategy: current.Strategy, WorkerCount: current.WorkerCount, QueueCapacity: current.QueueCapacity,
		Scanners: append([]string{}, current.Scanners...), GroupIDs: append([]int64{}, current.GroupIDs...),
		UserAllowlistIDs: append([]int64{}, current.UserAllowlistIDs...), Endpoints: []UpdateEndpoint{},
	}
	for _, e := range current.Endpoints {
		req.Endpoints = append(req.Endpoints, UpdateEndpoint{ID: e.ID, Name: e.Name, Protocol: e.Protocol, BaseURL: e.BaseURL, Model: e.Model, TimeoutMS: e.TimeoutMS, InputLimit: e.InputLimit, Enabled: e.Enabled})
	}
	return req
}

func (s *AuditCenterService) Save(ctx context.Context, input AuditCenterUpdate, actorID int64) (*AuditCenterConfig, error) {
	if strings.TrimSpace(input.ExpectedRevision) == "" {
		return nil, infraerrors.BadRequest("AUDIT_CENTER_REVISION_REQUIRED", "请先读取审计配置版本")
	}
	tx, err := s.prompt.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize with the existing prompt editor, retaining its token/version rules.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, promptAuditConfigLockKey); err != nil {
		return nil, err
	}
	current, err := s.readSnapshot(ctx, tx, false)
	if err != nil {
		return nil, err
	}
	if current.revision() != input.ExpectedRevision {
		return nil, auditCenterConflict()
	}
	promptStorage, err := ParseStorageConfig(current.values[SettingKeyPromptAuditConfig])
	if err != nil {
		return nil, err
	}
	contentInput, promptInput, rankingInput := splitAuditCenterUpdate(input, promptStorage)
	var preparedContent *service.PreparedContentModerationConfig
	var nextPrompt storageConfig
	var activePrompt ActiveConfig
	var activateContent func()
	var groups, users []int64
	if contentInput != nil {
		preparedContent, err = s.content.PrepareAuditCenterConfig(ctx, current.values[service.SettingKeyContentModerationConfig], *contentInput)
		if err != nil {
			return nil, err
		}
		groups = append(groups, preparedContent.Config.GroupIDs...)
		if contentInput.UserAllowlistIDs != nil {
			users = append(users, preparedContent.Config.UserAllowlistIDs...)
		}
	}
	if promptInput != nil {
		if promptInput.ExpectedConfigVersion != promptStorage.ConfigVersion {
			return nil, auditCenterConflict()
		}
		nextPrompt, err = s.prompt.buildNextStorage(promptStorage, *promptInput, actorID)
		if err != nil {
			return nil, err
		}
		nextPrompt.ConfigVersion = promptStorage.ConfigVersion + 1
		nextPrompt.UpdatedAt, nextPrompt.UpdatedBy = s.prompt.clock.Now(), actorID
		nextPrompt.ChangeSummary = changeSummary(nextPrompt)
		groups = append(groups, nextPrompt.GroupIDs...)
		if promptInput.UserAllowlistIDs != nil {
			users = append(users, nextPrompt.UserAllowlistIDs...)
		}
	}
	if rankingInput != nil {
		if rankingInput.GroupIDs != nil {
			groups = append(groups, (*rankingInput.GroupIDs)...)
		}
		if rankingInput.GroupRules != nil {
			for _, rule := range *rankingInput.GroupRules {
				groups = append(groups, rule.GroupID)
			}
		}
		if rankingInput.UserAllowlistIDs != nil {
			users = append(users, (*rankingInput.UserAllowlistIDs)...)
		}
	}
	// Follow existing deletion/save lock order: users, groups, settings. Recheck
	// the revision after acquiring locks to reject concurrent legacy-page saves.
	if err := auditCenterLockIDs(ctx, tx, "users", users); err != nil {
		return nil, err
	}
	if err := auditCenterLockIDs(ctx, tx, "groups", groups); err != nil {
		return nil, err
	}
	keys := append([]string{}, auditCenterSettingKeys...)
	sort.Strings(keys)
	for _, key := range keys {
		// Missing rows represent the same empty/default value in the revision.
		// Materialize them inside the transaction to also protect first-save races.
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO NOTHING`, key, current.values[key]); err != nil {
			return nil, err
		}
	}
	locked, err := s.readSnapshot(ctx, tx, true)
	if err != nil {
		return nil, err
	}
	if locked.revision() != input.ExpectedRevision {
		return nil, auditCenterConflict()
	}
	gate := locked.values[SettingKeyRiskControl] == "true"
	if input.RiskControlEnabled != nil {
		gate = *input.RiskControlEnabled
		locked.values[SettingKeyRiskControl] = strconv.FormatBool(gate)
		if err := auditCenterWriteSetting(ctx, tx, SettingKeyRiskControl, locked.values[SettingKeyRiskControl]); err != nil {
			return nil, err
		}
		if preparedContent == nil {
			activateContent, err = s.content.PrepareAuditCenterStoredActivation(locked.values[service.SettingKeyContentModerationConfig], gate, locked.values[service.SettingKeyCyberPolicyUserAllowlist])
			if err != nil {
				return nil, err
			}
		}
		if promptInput == nil {
			nextPrompt = promptStorage
			activePrompt, err = ActiveFromStorage(nextPrompt, gate, s.prompt.encryptor)
			if err != nil {
				return nil, err
			}
		}
	}
	if preparedContent != nil {
		activateContent, err = s.content.PrepareAuditCenterActivation(preparedContent, gate, locked.values[service.SettingKeyCyberPolicyUserAllowlist])
		if err != nil {
			return nil, err
		}
		locked.values[service.SettingKeyContentModerationConfig] = string(preparedContent.Raw)
		if err := auditCenterWriteSetting(ctx, tx, service.SettingKeyContentModerationConfig, string(preparedContent.Raw)); err != nil {
			return nil, err
		}
	}
	if promptInput != nil {
		activePrompt, err = ActiveFromStorage(nextPrompt, gate, s.prompt.encryptor)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(nextPrompt)
		if err != nil {
			return nil, err
		}
		locked.values[SettingKeyPromptAuditConfig] = string(raw)
		if err := auditCenterWriteSetting(ctx, tx, SettingKeyPromptAuditConfig, string(raw)); err != nil {
			return nil, err
		}
	}
	if rankingInput != nil {
		locked.ranking, err = s.ranking.SaveSettingsInTx(ctx, tx, *rankingInput)
		if err != nil {
			return nil, err
		}
	}
	result, err := s.view(locked)
	if err != nil {
		return nil, err
	}
	if err := s.chargeSummary(ctx, tx, result); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if activateContent != nil {
		activateContent()
	}
	if promptInput != nil || input.RiskControlEnabled != nil {
		s.installPrompt(ctx, nextPrompt, activePrompt)
	}
	return result, nil
}

func auditCenterWriteSetting(ctx context.Context, tx *sql.Tx, key, raw string) error {
	_, err := tx.ExecContext(ctx, `UPDATE settings SET value=$2,updated_at=NOW() WHERE key=$1`, key, raw)
	return err
}

func auditCenterLockIDs(ctx context.Context, tx *sql.Tx, kind string, ids []int64) error {
	ids = auditCenterIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	limit := 3000 // Three independent user exemption lists, each at most 1000.
	if kind == "groups" {
		limit = 4000
	} // Three scopes plus retained ranking rules.
	if len(ids) > limit || ids[0] <= 0 {
		return infraerrors.BadRequest("AUDIT_CENTER_IDENTITIES_INVALID", "审计分组或豁免用户 ID 无效")
	}
	query := `SELECT id FROM users WHERE id=ANY($1::bigint[]) AND deleted_at IS NULL ORDER BY id FOR SHARE`
	if kind == "groups" {
		query = `SELECT id FROM groups WHERE id=ANY($1::bigint[]) AND deleted_at IS NULL AND status='active' ORDER BY id FOR SHARE`
	}
	rows, err := tx.QueryContext(ctx, query, pq.Array(ids))
	if err != nil {
		return err
	}
	found := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		found++
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if found != len(ids) {
		return infraerrors.BadRequest("AUDIT_CENTER_IDENTITIES_UNAVAILABLE", "审计分组或豁免用户不存在、已删除或分组已停用，请刷新后重试")
	}
	return nil
}

func auditCenterIDs(ids ...[]int64) []int64 {
	unique := map[int64]bool{}
	for _, list := range ids {
		for _, id := range list {
			unique[id] = true
		}
	}
	result := make([]int64, 0, len(unique))
	for id := range unique {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (s *AuditCenterService) installPrompt(ctx context.Context, next storageConfig, active ActiveConfig) {
	m := s.prompt
	m.expected.Store(next.ConfigVersion)
	m.expectedScope.Store(&promptAuditGroupScope{version: next.ConfigVersion, groupIDs: append([]int64{}, next.GroupIDs...), userAllowlistIDs: append([]int64{}, next.UserAllowlistIDs...)})
	m.expectedBlocking.Store(active.RiskControlEnabled && next.Enabled && next.BlockingEnabled)
	previous := m.snapshot.Load()
	m.snapshot.Store(&activeConfigSnapshot{storage: cloneStorageConfig(next), active: cloneActiveConfig(active), loadedAt: m.clock.Now()})
	m.configUntrusted.Store(false)
	m.clearLoadError()
	m.logInvalidTokenEndpoints(previous, active)
	LogInfo(EventConfigUpdated, map[string]any{"config_version": next.ConfigVersion, "status": "updated", "source": "audit_center"})
	if m.redis != nil {
		if err := m.redis.Publish(ctx, ConfigInvalidationChannel, strconv.FormatInt(next.ConfigVersion, 10)).Err(); err != nil {
			LogWarn(EventConfigReloadDegraded, map[string]any{"config_version": next.ConfigVersion, "status": "degraded", "error_code": "config_invalidation_publish_failed"})
		}
	}
}

func (s *AuditCenterService) chargeSummary(ctx context.Context, tx *sql.Tx, result *AuditCenterConfig) error {
	result.ChargeSummaryTo = s.prompt.clock.Now().UTC()
	result.ChargeSummaryFrom = result.ChargeSummaryTo.Add(-24 * time.Hour)
	result.ChargeSummarySampleLimit, result.ChargeSummarySampled = 10000, true
	// Read a bounded primary-key tail; the UI explicitly labels this sample.
	// Amount means actual successful deduction, never a skipped proposed amount.
	rows, err := tx.QueryContext(ctx, `WITH recent AS (
		SELECT status,reason,amount,evaluated_at FROM cyber_policy_auto_charges ORDER BY id DESC LIMIT 10000
	) SELECT status,reason,COUNT(*),COALESCE(SUM(CASE WHEN status='deducted' THEN amount ELSE 0 END),0)::text
	FROM recent WHERE evaluated_at >= $1 AND evaluated_at < $2 GROUP BY status,reason ORDER BY status,reason`, result.ChargeSummaryFrom, result.ChargeSummaryTo)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item AuditCenterChargeSummary
		if err := rows.Scan(&item.Status, &item.Reason, &item.Count, &item.Amount); err != nil {
			return err
		}
		result.ChargeSummary = append(result.ChargeSummary, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	// Imports share the displayed time window and primary-key sample limit.
	// A missing/unavailable table is an error, never a fabricated empty result.
	importRows, err := tx.QueryContext(ctx, `WITH recent AS (
		SELECT status,reason,amount,created_at FROM audit_settlement_imports ORDER BY id DESC LIMIT 10000
	) SELECT status,reason,COUNT(*),COALESCE(SUM(CASE WHEN status='deducted' THEN amount ELSE 0 END),0)::text
	FROM recent WHERE created_at >= $1 AND created_at < $2 GROUP BY status,reason ORDER BY status,reason`, result.ChargeSummaryFrom, result.ChargeSummaryTo)
	if err != nil {
		return err
	}
	defer importRows.Close()
	for importRows.Next() {
		var item AuditCenterChargeSummary
		if err := importRows.Scan(&item.Status, &item.Reason, &item.Count, &item.Amount); err != nil {
			return err
		}
		result.ImportSummary = append(result.ImportSummary, item)
	}
	if err := importRows.Err(); err != nil {
		return err
	}
	if err := importRows.Close(); err != nil {
		return err
	}
	// Display-only findings are a separate series, never charge attempts or
	// penalty counter increments. Double-audit findings retain their own sources.
	observationRows, err := tx.QueryContext(ctx, `WITH recent AS (
		SELECT source,created_at FROM cyber_policy_audit_observations ORDER BY id DESC LIMIT 10000
	) SELECT source,COUNT(*) FROM recent WHERE created_at >= $1 AND created_at < $2 GROUP BY source ORDER BY source`, result.ChargeSummaryFrom, result.ChargeSummaryTo)
	if err != nil {
		return err
	}
	defer observationRows.Close()
	for observationRows.Next() {
		item := AuditCenterChargeSummary{Status: "skipped", Reason: "observation_only", Amount: "0.00000000"}
		if err := observationRows.Scan(&item.Source, &item.Count); err != nil {
			return err
		}
		result.ObservationSummary = append(result.ObservationSummary, item)
	}
	return observationRows.Err()
}

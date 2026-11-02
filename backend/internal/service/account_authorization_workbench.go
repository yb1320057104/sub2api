package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const authorizationWorkbenchLockID int64 = 70360006

func authorizationWorkbenchDefaults() AuthorizationSubmit {
	return AuthorizationSubmit{AuthorizationConcurrency: 3, AccountConcurrency: 20, AccountPriority: 1, AccountProxyMode: "direct", MaxAccountsPerProxy: 3, PassGroupIDs: []int64{}, GroupIDs: []int64{}}
}

func authorizationWorkbenchTx(ctx context.Context, tx *sql.Tx) (AuthorizationSubmit, error) {
	settings := authorizationWorkbenchDefaults()
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR SHARE`, authorizationWorkbenchSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	err = json.Unmarshal([]byte(raw), &settings)
	return settings, err
}

// Persist only non-sensitive workbench inputs. Models, matched source rules and
// destinations are server-generated job snapshots, never client-controlled.
func (s *AccountAuthorizationService) normalizeAuthorizationWorkbench(ctx context.Context, input *AuthorizationSubmit) (AuthorizationSubmit, error) {
	if input == nil {
		return AuthorizationSubmit{}, infraerrors.BadRequest("AUTHORIZATION_OPTIONS", "授权参数不能为空")
	}
	settings := AuthorizationSubmit{
		WorkbenchRevision:        input.WorkbenchRevision,
		AuthorizationConcurrency: input.AuthorizationConcurrency,
		AccountConcurrency:       input.AccountConcurrency,
		AccountPriority:          input.AccountPriority,
		AccountProxyMode:         input.AccountProxyMode,
		MaxAccountsPerProxy:      input.MaxAccountsPerProxy,
		IntelligenceGate:         input.IntelligenceGate,
		PassGroupIDs:             append([]int64{}, input.PassGroupIDs...),
		GroupIDs:                 append([]int64{}, input.PassGroupIDs...),
	}
	if err := validateAuthorizationOptions(settings.AuthorizationConcurrency, settings.AccountConcurrency, settings.AccountPriority, settings.PassGroupIDs); err != nil {
		return settings, err
	}
	if err := normalizeAuthorizationProxyOptions(&settings); err != nil {
		return settings, err
	}
	if err := s.admin.ValidateAccountGroupBindings(ctx, settings.PassGroupIDs); err != nil {
		return settings, err
	}
	return settings, nil
}

func saveAuthorizationWorkbenchTx(ctx context.Context, tx *sql.Tx, settings AuthorizationSubmit) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, authorizationWorkbenchLockID); err != nil {
		return err
	}
	var revision string
	err := tx.QueryRowContext(ctx, `SELECT md5(value) FROM settings WHERE key=$1 FOR UPDATE`, authorizationWorkbenchSettingKey).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if settings.WorkbenchRevision != revision {
		return infraerrors.Conflict("AUTHORIZATION_WORKBENCH_CHANGED", "授权工作台配置已变化，请刷新后保存或提交")
	}
	if len(settings.PassGroupIDs) > 0 {
		if err = validateAuthorizationGateGroups(ctx, tx, settings.PassGroupIDs, nil); err != nil {
			return err
		}
	}
	settings.WorkbenchRevision = ""
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, authorizationWorkbenchSettingKey, string(raw))
	return err
}

func (s *AccountAuthorizationService) SaveWorkbench(ctx context.Context, input *AuthorizationSubmit) error {
	settings, err := s.normalizeAuthorizationWorkbench(ctx, input)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = saveAuthorizationWorkbenchTx(ctx, tx, settings); err != nil {
		return err
	}
	return tx.Commit()
}

// A new job uses the first configured source matching the actual existing
// memberships (or the selected import groups for a new account).
func configureAuthorizationGroupGate(settings *AuthorizationSubmit, candy *RadarConfig, groups []int64, refreshModels bool) {
	if !settings.IntelligenceGate {
		return
	}
	rule := firstRadarGroupRule(candy.GroupRules, groups)
	if rule == nil {
		settings.IntelligenceGate = false
		settings.IntelligenceSourceGroupID = 0
		settings.FailGroupIDs = nil
		return
	}
	settings.IntelligenceSourceGroupID = rule.SourceGroupID
	settings.PassGroupIDs = append([]int64{}, rule.PassGroupIDs...)
	settings.FailGroupIDs = []int64{rule.FailGroupID}
	if refreshModels {
		settings.IntelligenceModels = candy.Models
		settings.IntelligenceGeneration = candy.Generation
		if len(candy.Models) > 0 {
			settings.IntelligenceModel, settings.IntelligenceEffort = candy.Models[0].Model, candy.Models[0].Effort
		}
	}
}

func authorizationSubmissionAccount(ctx context.Context, tx *sql.Tx, email string) (*int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT a.id FROM accounts a WHERE a.deleted_at IS NULL AND a.platform='openai' AND a.type='oauth' AND a.parent_account_id IS NULL AND `+authorizationEmailSQL+`=$1 ORDER BY a.id LIMIT 2`, email)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var id *int64
	for rows.Next() {
		var current int64
		if err = rows.Scan(&current); err != nil {
			return nil, err
		}
		if id != nil {
			return nil, infraerrors.Conflict("AUTHORIZATION_DUPLICATE_ACCOUNT", "存在多个同邮箱账号，请通过巡检绑定具体账号后重授权")
		}
		id = &current
	}
	return id, rows.Err()
}

func authorizationJobSourceGroups(ctx context.Context, tx *sql.Tx, accountID *int64, fallback []int64) ([]int64, error) {
	if accountID == nil {
		return append([]int64{}, fallback...), nil
	}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(ag.group_id ORDER BY ag.group_id) FILTER (WHERE g.platform='openai' AND g.status='active'),'[]'::jsonb) FROM account_groups ag JOIN groups g ON g.id=ag.group_id AND g.deleted_at IS NULL WHERE ag.account_id=$1`, *accountID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var groups []int64
	if err = json.Unmarshal(raw, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func authorizationCurrentGroupRules(ctx context.Context, query authorizationGroupQuerier) ([]RadarGroupRule, error) {
	rows, err := query.QueryContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR SHARE`, radarGroupRulesSettingKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	rules := []RadarGroupRule{}
	var raw string
	if rows.Next() {
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &rules); err != nil {
			return nil, err
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	if len(rules) == 0 {
		return rules, nil
	}
	// A deleted/disabled source cannot continue scheduling tests from stale JSON.
	sourceRows, err := query.QueryContext(ctx, `SELECT id FROM groups WHERE deleted_at IS NULL AND platform='openai' AND status='active' AND id IN (SELECT (value->>'source_group_id')::bigint FROM jsonb_array_elements($1::jsonb)) ORDER BY id FOR SHARE`, raw)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sourceRows.Close() }()
	active := map[int64]bool{}
	for sourceRows.Next() {
		var id int64
		if err = sourceRows.Scan(&id); err != nil {
			return nil, err
		}
		active[id] = true
	}
	if err = sourceRows.Err(); err != nil {
		return nil, err
	}
	filtered := make([]RadarGroupRule, 0, len(rules))
	for _, rule := range rules {
		if active[rule.SourceGroupID] {
			filtered = append(filtered, rule)
		}
	}
	return filtered, nil
}

func (s *AccountAuthorizationService) checkAuthorizationSourceRule(ctx context.Context, sourceID int64) error {
	if sourceID == 0 {
		return nil // Preserve the snapshotted semantics of pre-rule jobs.
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE((SELECT value::jsonb FROM settings WHERE key=$1),'[]'::jsonb)) rule(value) JOIN groups g ON g.id=(rule.value->>'source_group_id')::bigint AND g.deleted_at IS NULL AND g.platform='openai' AND g.status='active' WHERE g.id=$2)`, radarGroupRulesSettingKey, sourceID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return &authorizationBrowserError{stage: "intelligence_groups_missing", message: "糖果测试来源分组规则已移除或停用，本次测智已停止，账号保持当前分组与调度设置", needsInput: true}
	}
	return nil
}

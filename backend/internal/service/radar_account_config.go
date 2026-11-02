package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type RadarModel struct {
	Model   string `json:"model"`
	Effort  string `json:"effort"`
	Enabled bool   `json:"enabled"`
}

const radarGroupRulesSettingKey = "pk_radar_account_group_rules"

type RadarGroupRule struct {
	SourceGroupID int64   `json:"source_group_id"`
	PassGroupIDs  []int64 `json:"pass_group_ids"`
	FailGroupID   int64   `json:"fail_group_id"`
}

type RadarConfig struct {
	Enabled         bool             `json:"enabled"`
	IntervalMinutes int              `json:"interval_minutes"`
	TargetGroupID   *int64           `json:"target_group_id"`
	Generation      string           `json:"generation"`
	Models          []RadarModel     `json:"models"`
	GroupRules      []RadarGroupRule `json:"group_rules"`
}

type RadarModelResult struct {
	egressMode      string
	ID              int64      `json:"id"`
	Model           string     `json:"model"`
	MappedModel     string     `json:"mapped_model"`
	Effort          string     `json:"effort"`
	Status          string     `json:"status"`
	Passed          *bool      `json:"passed"`
	Answer          *int       `json:"answer"`
	ResponseText    string     `json:"response_text,omitempty"`
	ErrorMessage    string     `json:"error_message"`
	EgressIP        string     `json:"egress_ip"`
	EgressCheckedAt *time.Time `json:"egress_checked_at"`
	EgressError     string     `json:"egress_error"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	DurationMS      *int64     `json:"duration_ms"`
}

type RadarRun struct {
	AccountCandyTestResult
	StartedAtEstimated bool               `json:"started_at_estimated,omitempty"`
	Origin             string             `json:"origin"`
	Generation         string             `json:"generation"`
	Verdict            string             `json:"verdict"`
	MovedToGroupIDs    []int64            `json:"moved_to_group_ids"`
	ModelResults       []RadarModelResult `json:"model_results"`
	FailedModels       []RadarModelResult `json:"failed_models"`
	EgressIP           string             `json:"egress_ip"`
	EgressMode         string             `json:"egress_mode"`
	EgressCheckedAt    *time.Time         `json:"egress_checked_at"`
	EgressError        string             `json:"egress_error"`
	leaseToken         string
	groupRule          *RadarGroupRule
}

// Initialize new storage atomically; legacy data is only read. Deployment must
// drain older binaries before activating this scheduler (PK-019).
func (s *AccountCandyTestService) ensureRadar(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, accountCandyLockID); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_radar_account_config WHERE id=1)`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit()
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_account_candy_state WHERE active_result_id IS NOT NULL AND lease_until>NOW())`).Scan(&active); err != nil {
		return err
	}
	if active {
		return infraerrors.Conflict("RADAR_LEGACY_RUNNING", "旧账号测试尚未结束，请等待任务排空")
	}
	old, err := accountCandyConfigTx(ctx, tx)
	if err != nil {
		return err
	}
	generation := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO pk_radar_account_config(id,enabled,interval_minutes,target_group_id,generation,initialized_from_legacy) VALUES(1,$1,$2,$3,$4,true)`, old.Enabled, old.IntervalMinutes, old.TargetGroupID, generation); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pk_radar_account_models(config_id,model,effort,enabled,sort_order) VALUES(1,$1,$2,true,0)`, old.Model, old.Effort); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pk_radar_account_state(account_id,next_run_at) SELECT account_id,next_run_at FROM pk_account_candy_state ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	return tx.Commit()
}

func radarConfigTx(ctx context.Context, tx *sql.Tx) (*RadarConfig, error) {
	cfg := &RadarConfig{Models: make([]RadarModel, 0)}
	err := tx.QueryRowContext(ctx, `SELECT enabled,interval_minutes,target_group_id,generation FROM pk_radar_account_config WHERE id=1`).Scan(&cfg.Enabled, &cfg.IntervalMinutes, &cfg.TargetGroupID, &cfg.Generation)
	if err != nil {
		return nil, err
	}
	cfg.GroupRules, err = radarGroupRulesTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT model,effort,enabled FROM pk_radar_account_models WHERE config_id=1 ORDER BY sort_order`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var model RadarModel
		if err = rows.Scan(&model.Model, &model.Effort, &model.Enabled); err != nil {
			return nil, err
		}
		cfg.Models = append(cfg.Models, model)
	}
	return cfg, rows.Err()
}

// No implicit legacy fallback: a group participates only after it is explicitly
// configured. Settings and the radar generation are committed together.
func radarGroupRulesTx(ctx context.Context, tx *sql.Tx) ([]RadarGroupRule, error) {
	rules := make([]RadarGroupRule, 0)
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, radarGroupRulesSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return rules, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, err
	}
	if rules == nil {
		rules = make([]RadarGroupRule, 0)
	}
	return rules, nil
}

func firstRadarGroupRule(rules []RadarGroupRule, groupIDs []int64) *RadarGroupRule {
	groups := make(map[int64]bool, len(groupIDs))
	for _, id := range groupIDs {
		groups[id] = true
	}
	for _, rule := range rules {
		if groups[rule.SourceGroupID] {
			matched := rule
			matched.PassGroupIDs = append([]int64{}, rule.PassGroupIDs...)
			return &matched
		}
	}
	return nil
}

func (s *AccountCandyTestService) GetRadarConfig(ctx context.Context) (*RadarConfig, error) {
	if err := s.ensureRadar(ctx); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, err := radarConfigTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	return cfg, tx.Commit()
}

func validateRadarConfig(cfg *RadarConfig) error {
	bad := func(message string) error { return infraerrors.BadRequest("INVALID_RADAR_CONFIG", message) }
	if cfg == nil || len(cfg.Models) < 1 || len(cfg.Models) > 8 {
		return bad("请配置1～8个模型")
	}
	seen := map[string]bool{}
	enabled := 0
	for i := range cfg.Models {
		m := &cfg.Models[i]
		m.Model = strings.TrimSpace(m.Model)
		m.Effort = strings.TrimSpace(m.Effort)
		if !validIntelligenceTestModel(m.Model) || isOpenAIImageModel(m.Model) || !validIntelligenceTestEffort(m.Effort) {
			return bad("模型或推理强度无效")
		}
		if seen[m.Model] {
			return bad("模型不能重复")
		}
		seen[m.Model] = true
		if m.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return bad("至少启用一个模型")
	}
	if cfg.IntervalMinutes < 1 || cfg.IntervalMinutes > 1440 {
		return bad("测试间隔必须为1至1440分钟")
	}
	sources := make(map[int64]bool, len(cfg.GroupRules))
	for _, rule := range cfg.GroupRules {
		if rule.SourceGroupID <= 0 || len(rule.PassGroupIDs) == 0 || rule.FailGroupID <= 0 {
			return bad("每条规则都必须选择测试来源分组、至少一个合格分组和不合格分组")
		}
		passGroups := make(map[int64]bool, len(rule.PassGroupIDs))
		for _, id := range rule.PassGroupIDs {
			if id <= 0 || passGroups[id] {
				return bad("合格分组必须有效且不能重复")
			}
			passGroups[id] = true
		}
		if sources[rule.SourceGroupID] {
			return bad("测试来源分组不能重复")
		}
		sources[rule.SourceGroupID] = true
	}
	return nil
}

func (s *AccountCandyTestService) UpdateRadarConfig(ctx context.Context, cfg *RadarConfig) error {
	if err := validateRadarConfig(cfg); err != nil {
		return err
	}
	if err := s.ensureRadar(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, accountCandyLockID); err != nil {
		return err
	}
	current, err := radarConfigTx(ctx, tx)
	if err != nil {
		return err
	}
	if cfg.Generation == "" || current.Generation != cfg.Generation {
		return infraerrors.Conflict("RADAR_CONFIG_CHANGED", "设置已更新，请刷新后重试")
	}
	groupSet := make(map[int64]bool)
	for _, rule := range cfg.GroupRules {
		groupSet[rule.SourceGroupID], groupSet[rule.FailGroupID] = true, true
		for _, id := range rule.PassGroupIDs {
			groupSet[id] = true
		}
	}
	groupIDs := make([]int64, 0, len(groupSet))
	for id := range groupSet {
		groupIDs = append(groupIDs, id)
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	for _, id := range groupIDs {
		var platform, status string
		err = tx.QueryRowContext(ctx, `SELECT platform,status FROM groups WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, id).Scan(&platform, &status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) || platform != PlatformOpenAI || status != StatusActive {
			return infraerrors.BadRequest("INVALID_RADAR_TARGET", fmt.Sprintf("分组 %d 不存在、已停用或不是OpenAI分组", id))
		}
	}
	// Retain the historical global target only for jobs created by older builds.
	// New radar and authorization tasks use explicit group rules.
	cfg.TargetGroupID = current.TargetGroupID
	if cfg.GroupRules == nil {
		cfg.GroupRules = make([]RadarGroupRule, 0)
	}
	rawRules, err := json.Marshal(cfg.GroupRules)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, radarGroupRulesSettingKey, string(rawRules)); err != nil {
		return err
	}
	cfg.Generation = uuid.NewString()
	if _, err = tx.ExecContext(ctx, `UPDATE pk_radar_account_config SET enabled=$1,interval_minutes=$2,target_group_id=$3,generation=$4,updated_at=NOW() WHERE id=1`, cfg.Enabled, cfg.IntervalMinutes, cfg.TargetGroupID, cfg.Generation); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM pk_radar_account_models WHERE config_id=1`); err != nil {
		return err
	}
	for i, m := range cfg.Models {
		if _, err = tx.ExecContext(ctx, `INSERT INTO pk_radar_account_models(config_id,model,effort,enabled,sort_order)VALUES(1,$1,$2,$3,$4)`, m.Model, m.Effort, m.Enabled, i); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_radar_account_state SET next_run_at=NOW()`); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	for _, w := range s.active {
		if w.claimed && w.generation != cfg.Generation {
			w.cancel()
		}
	}
	s.mu.Unlock()
	s.notify()
	return nil
}

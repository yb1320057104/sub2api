package service

import (
	"context"
	"database/sql"
	"errors"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const intelligenceTestPublicGroupLimit = 2000

// Public groups contain only display metadata and the completed modes available
// to a visitor. Key availability and administrator configuration stay private.
type IntelligenceTestPublicGroup struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Platform string   `json:"platform"`
	Modes    []string `json:"modes"`
}

type IntelligenceTestPublicGroups struct {
	Modes  []string                      `json:"modes"`
	Groups []IntelligenceTestPublicGroup `json:"groups"`
}

type IntelligenceTestPublicDetails struct {
	Prompt string `json:"prompt"`
	Answer string `json:"answer"`
}

// Read the current database value on every public request, without the public
// settings cache. Each following query also matches this exact value, so a
// concurrent save cannot disclose results using an obsolete enabled mode.
func (s *IntelligenceTestService) publicIntelligenceTestConfig(ctx context.Context) (*IntelligenceTestConfig, string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, intelligenceTestSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultIntelligenceTestConfig(), "", nil
	}
	if err != nil {
		return nil, "", err
	}
	cfg, err := decodeIntelligenceTestConfig(raw)
	return cfg, raw, err
}

func (s *IntelligenceTestService) ListPublicGroups(ctx context.Context) (*IntelligenceTestPublicGroups, error) {
	result := &IntelligenceTestPublicGroups{Modes: []string{}, Groups: []IntelligenceTestPublicGroup{}}
	cfg, raw, err := s.publicIntelligenceTestConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled || (!cfg.SVG.Enabled && !cfg.Candy.Enabled) {
		return result, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT g.id,g.name,g.platform,available.svg,available.candy
		FROM pk_intelligence_test_keys k
		JOIN groups g ON g.id=k.group_id AND g.status='active' AND g.deleted_at IS NULL
		JOIN settings configuration ON configuration.key='pk_intelligence_test_config' AND configuration.value=$3
		CROSS JOIN LATERAL (
			SELECT ($1::boolean AND EXISTS (SELECT 1 FROM pk_intelligence_test_results r WHERE r.group_id=g.id AND r.mode='svg' AND r.status='completed')) AS svg,
			       ($2::boolean AND EXISTS (SELECT 1 FROM pk_intelligence_test_results r WHERE r.group_id=g.id AND r.mode='candy' AND r.status='completed')) AS candy
		) available
		WHERE available.svg OR available.candy ORDER BY g.id LIMIT $4`, cfg.SVG.Enabled, cfg.Candy.Enabled, raw, intelligenceTestPublicGroupLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	hasSVG, hasCandy := false, false
	for rows.Next() {
		var group IntelligenceTestPublicGroup
		var svg, candy bool
		if err := rows.Scan(&group.ID, &group.Name, &group.Platform, &svg, &candy); err != nil {
			return nil, err
		}
		group.Modes = []string{}
		if svg {
			group.Modes = append(group.Modes, "svg")
			hasSVG = true
		}
		if candy {
			group.Modes = append(group.Modes, "candy")
			hasCandy = true
		}
		result.Groups = append(result.Groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if hasSVG {
		result.Modes = append(result.Modes, "svg")
	}
	if hasCandy {
		result.Modes = append(result.Modes, "candy")
	}
	return result, nil
}

func (s *IntelligenceTestService) ListPublicResults(ctx context.Context, groupID int64, mode string) ([]IntelligenceTestResult, error) {
	if groupID <= 0 || (mode != "svg" && mode != "candy") {
		return nil, infraerrors.BadRequest("INVALID_INTELLIGENCE_TEST_QUERY", "分组或测试类型无效")
	}
	results := make([]IntelligenceTestResult, 0, 30)
	cfg, raw, err := s.publicIntelligenceTestConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled || (mode == "svg" && !cfg.SVG.Enabled) || (mode == "candy" && !cfg.Candy.Enabled) {
		return results, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.group_id,r.mode,r.status,r.request_model,r.request_effort,r.passed,(r.artifact<>''),r.started_at,r.finished_at,r.duration_ms,(r.error_code='empty_account_pool')
		FROM pk_intelligence_test_results r
		JOIN pk_intelligence_test_keys k ON k.group_id=r.group_id
		JOIN groups g ON g.id=r.group_id AND g.status='active' AND g.deleted_at IS NULL
		JOIN settings configuration ON configuration.key='pk_intelligence_test_config' AND configuration.value=$3
		WHERE r.group_id=$1 AND r.mode=$2 AND r.status='completed' ORDER BY r.id DESC LIMIT 30`, groupID, mode, raw)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var result IntelligenceTestResult
		if err := rows.Scan(&result.ID, &result.GroupID, &result.Mode, &result.Status, &result.Model, &result.Effort, &result.Passed, &result.HasArtifact, &result.StartedAt, &result.FinishedAt, &result.DurationMS, &result.EmptyPool); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (s *IntelligenceTestService) GetPublicArtifact(ctx context.Context, groupID, resultID int64) (string, error) {
	if groupID <= 0 || resultID <= 0 {
		return "", infraerrors.BadRequest("INVALID_INTELLIGENCE_TEST_QUERY", "分组或作品无效")
	}
	notFound := infraerrors.NotFound("INTELLIGENCE_TEST_ARTIFACT_NOT_FOUND", "作品不存在或已清理")
	cfg, raw, err := s.publicIntelligenceTestConfig(ctx)
	if err != nil {
		return "", err
	}
	if !cfg.Enabled || !cfg.SVG.Enabled {
		return "", notFound
	}
	var artifact string
	err = s.db.QueryRowContext(ctx, `SELECT r.artifact FROM pk_intelligence_test_results r
		JOIN pk_intelligence_test_keys k ON k.group_id=r.group_id
		JOIN groups g ON g.id=r.group_id AND g.status='active' AND g.deleted_at IS NULL
		JOIN settings configuration ON configuration.key='pk_intelligence_test_config' AND configuration.value=$3
		WHERE r.id=$1 AND r.group_id=$2 AND r.mode='svg' AND r.status='completed' AND r.artifact<>''`, resultID, groupID, raw).Scan(&artifact)
	if errors.Is(err, sql.ErrNoRows) {
		return "", notFound
	}
	return artifact, err
}

func (s *IntelligenceTestService) GetPublicDetails(ctx context.Context, groupID, resultID int64) (*IntelligenceTestPublicDetails, error) {
	if groupID <= 0 || resultID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_INTELLIGENCE_TEST_QUERY", "请选择有效测试记录")
	}
	notFound := infraerrors.NotFound("INTELLIGENCE_TEST_RESULT_NOT_FOUND", "测试记录不存在或已清理")
	cfg, raw, err := s.publicIntelligenceTestConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled || !cfg.Candy.Enabled {
		return nil, notFound
	}
	details := &IntelligenceTestPublicDetails{}
	// This separate projection never loads diagnostics, credentials or running
	// answers. Existing database constraints bound prompt/answer to 100000/262144 bytes.
	err = s.db.QueryRowContext(ctx, `SELECT r.prompt_snapshot,r.answer_text FROM pk_intelligence_test_results r
		JOIN pk_intelligence_test_keys k ON k.group_id=r.group_id
		JOIN groups g ON g.id=r.group_id AND g.status='active' AND g.deleted_at IS NULL
		JOIN settings configuration ON configuration.key='pk_intelligence_test_config' AND configuration.value=$3
		WHERE r.id=$1 AND r.group_id=$2 AND r.mode='candy' AND r.status='completed'`, resultID, groupID, raw).Scan(&details.Prompt, &details.Answer)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound
	}
	return details, err
}

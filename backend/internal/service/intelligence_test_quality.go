package service

import (
	"context"
	"database/sql"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const intelligenceTestQualityHistoryLimit = 30

// Quality reads project existing group tests only. They never create a test,
// select a backend account, or expose the account-observatory result structs.
type IntelligenceTestQualityGroup struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

type IntelligenceTestQualityGroups struct {
	Enabled bool                           `json:"enabled"`
	Groups  []IntelligenceTestQualityGroup `json:"groups"`
}

type IntelligenceTestQualityResult struct {
	ID         int64      `json:"id"`
	Model      *string    `json:"model"`
	Effort     *string    `json:"effort"`
	Status     string     `json:"status"`
	State      string     `json:"state"`
	Passed     *bool      `json:"passed,omitempty"`
	EmptyPool  bool       `json:"empty_pool,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	DurationMS *int64     `json:"duration_ms,omitempty"`
}

type IntelligenceTestQualityHistory struct {
	Enabled           bool                            `json:"enabled"`
	GroupID           int64                           `json:"group_id"`
	IntervalMinutes   int                             `json:"interval_minutes"`
	HistoryLimit      int                             `json:"history_limit"`
	StaleAfterSeconds int                             `json:"stale_after_seconds"`
	ObservedAt        time.Time                       `json:"observed_at"`
	Results           []IntelligenceTestQualityResult `json:"results"`
}

func (s *IntelligenceTestService) ListQualityGroups(ctx context.Context) (*IntelligenceTestQualityGroups, error) {
	view := &IntelligenceTestQualityGroups{Groups: []IntelligenceTestQualityGroup{}}
	cfg, raw, err := s.publicIntelligenceTestConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled || !cfg.Candy.Enabled {
		return view, nil
	}
	view.Enabled = true
	// Matching the exact configuration in this statement also protects a read
	// that races a configuration save. Unselected groups and key state stay private.
	rows, err := s.db.QueryContext(ctx, `SELECT g.id,g.name,g.platform
		FROM pk_intelligence_test_keys k
		JOIN groups g ON g.id=k.group_id AND g.status='active' AND g.deleted_at IS NULL
		JOIN settings configuration ON configuration.key='pk_intelligence_test_config' AND configuration.value=$1
		ORDER BY g.id LIMIT $2`, raw, intelligenceTestPublicGroupLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var group IntelligenceTestQualityGroup
		if err := rows.Scan(&group.ID, &group.Name, &group.Platform); err != nil {
			return nil, err
		}
		view.Groups = append(view.Groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return view, nil
}

func (s *IntelligenceTestService) QualityHistory(ctx context.Context, groupID int64) (*IntelligenceTestQualityHistory, error) {
	if groupID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_INTELLIGENCE_TEST_QUERY", "请选择有效测试分组")
	}
	view := &IntelligenceTestQualityHistory{GroupID: groupID, HistoryLimit: intelligenceTestQualityHistoryLimit,
		ObservedAt: time.Now().UTC(), Results: []IntelligenceTestQualityResult{}}
	cfg, raw, err := s.publicIntelligenceTestConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled || !cfg.Candy.Enabled {
		return view, nil
	}
	// The left join distinguishes an admitted group with no history from a
	// removed/unselected group or a configuration changed since the first read.
	// Explicit projection excludes answers, internal diagnostics and account data.
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.request_model,r.request_effort,r.status,r.passed,
		r.started_at,r.finished_at,r.duration_ms,COALESCE(r.error_code='empty_account_pool',false)
		FROM pk_intelligence_test_keys k
		JOIN groups g ON g.id=k.group_id AND g.status='active' AND g.deleted_at IS NULL
		JOIN settings configuration ON configuration.key='pk_intelligence_test_config' AND configuration.value=$2
		LEFT JOIN LATERAL (
			SELECT id,request_model,request_effort,status,passed,started_at,finished_at,duration_ms,error_code
			FROM pk_intelligence_test_results
			WHERE group_id=k.group_id AND mode='candy' ORDER BY id DESC LIMIT $3
		) r ON true WHERE k.group_id=$1 ORDER BY r.id DESC NULLS LAST`, groupID, raw, intelligenceTestQualityHistoryLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	admitted := false
	for rows.Next() {
		var id sql.NullInt64
		var status *string
		var startedAt *time.Time
		var result IntelligenceTestQualityResult
		if err := rows.Scan(&id, &result.Model, &result.Effort, &status, &result.Passed,
			&startedAt, &result.FinishedAt, &result.DurationMS, &result.EmptyPool); err != nil {
			return nil, err
		}
		admitted = true
		if !id.Valid {
			continue
		}
		result.ID = id.Int64
		if status != nil {
			result.Status = *status
		}
		if startedAt != nil {
			result.StartedAt = *startedAt
		}
		result.State = intelligenceTestQualityState(result)
		view.Results = append(view.Results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !admitted {
		return nil, infraerrors.NotFound("INTELLIGENCE_TEST_QUALITY_GROUP_NOT_FOUND", "检测分组不存在或未开放展示")
	}
	view.Enabled = true
	view.IntervalMinutes = cfg.Candy.IntervalMinutes
	view.StaleAfterSeconds = cfg.Candy.IntervalMinutes*60 + int(intelligenceTestTimeout.Seconds())
	view.ObservedAt = time.Now().UTC()
	return view, nil
}

func intelligenceTestQualityState(result IntelligenceTestQualityResult) string {
	if result.EmptyPool {
		return "empty_pool"
	}
	switch result.Status {
	case "running":
		return "running"
	case "failed":
		return "error"
	case "completed":
		if result.Passed != nil {
			if *result.Passed {
				return "correct"
			}
			return "incorrect"
		}
	}
	return "unknown"
}

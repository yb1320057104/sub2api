package service

import (
	"context"
	"encoding/json"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Only actual authorization candy runs qualify. Group membership alone is never
// a verdict; scheduled reauthorization qualifies only with recorded test work.
const authorizationRadarCondition = `j.account_id IS NOT NULL AND j.source IN ('manual','reauthorization')
	AND j.settings->>'intelligence_gate'='true' AND j.settings->>'authorization_completed'='true'
	AND (j.settings->'intelligence_passed' IN ('true'::jsonb,'false'::jsonb)
		OR jsonb_array_length(COALESCE(NULLIF(j.settings->'intelligence_results','null'::jsonb),'[]'::jsonb))>0)`

const authorizationRadarTimeSQL = `COALESCE((j.settings->>'intelligence_started_at')::timestamptz,j.started_at,j.created_at)`

// A set-based join keeps pagination and verdict filtering on the same source.
// Current radar runs retain priority, and newer radar tests supersede imports.
const authorizationRadarAccountJoin = ` LEFT JOIN (
	SELECT DISTINCT ON(j.account_id) j.id,j.account_id,` + authorizationRadarTimeSQL + ` AS started_at,
	NULLIF(j.settings->>'intelligence_generation','') AS generation,
	CASE j.settings->'intelligence_passed' WHEN 'false'::jsonb THEN 'fail' WHEN 'true'::jsonb THEN 'pass' ELSE 'unknown' END AS verdict
	FROM pk_authorization_jobs j WHERE ` + authorizationRadarCondition + `
	ORDER BY j.account_id,` + authorizationRadarTimeSQL + ` DESC,j.id DESC
	) ar ON ar.account_id=a.id`

const radarUseAuthorizationSQL = `st.active_run_id IS NULL AND ar.id IS NOT NULL AND (rr.id IS NULL OR ar.started_at>rr.started_at)`

const radarObservedVerdictSQL = `CASE WHEN ` + radarUseAuthorizationSQL + ` THEN
	CASE WHEN ar.generation IS NULL OR ar.generation=$2 THEN ar.verdict ELSE 'unknown' END
	ELSE CASE WHEN st.generation=$2 THEN st.verdict ELSE 'unknown' END END`

func authorizationIntelligenceResultsSQL(settings string, details bool) string {
	value := "COALESCE(NULLIF(" + settings + "->'intelligence_results','null'::jsonb),'[]'::jsonb)"
	if details {
		return value
	}
	return "COALESCE((SELECT jsonb_agg(item-'response_text' ORDER BY ordinal) FROM jsonb_array_elements(" + value + ") WITH ORDINALITY AS results(item,ordinal)),'[]'::jsonb)"
}

type authorizationRadarMetadata struct {
	Model      string                            `json:"model"`
	Effort     string                            `json:"effort"`
	Generation string                            `json:"generation"`
	Passed     *bool                             `json:"passed"`
	Answer     *int                              `json:"answer"`
	StartedAt  *time.Time                        `json:"started_at"`
	FinishedAt *time.Time                        `json:"finished_at"`
	Groups     []int64                           `json:"groups"`
	Results    []AuthorizationIntelligenceResult `json:"results"`
}

func loadAuthorizationRadarRuns(ctx context.Context, q radarQuerier, ids []int64, details bool) (map[int64]*RadarRun, error) {
	out := make(map[int64]*RadarRun)
	if len(ids) == 0 {
		return out, nil
	}
	args, marks := radarIDArgs(ids)
	// Project only test metadata, never authorization settings or credentials.
	payload := `jsonb_build_object('model',j.settings->'intelligence_model','effort',j.settings->'intelligence_effort',
		'generation',j.settings->'intelligence_generation','passed',j.settings->'intelligence_passed','answer',j.settings->'intelligence_answer',
		'started_at',j.settings->'intelligence_started_at','finished_at',j.settings->'intelligence_finished_at',
		'groups',j.settings->'final_group_ids','results',` + authorizationIntelligenceResultsSQL("j.settings", details) + ")"
	rows, err := q.QueryContext(ctx, `SELECT j.id,j.account_id,j.source,j.status,j.stage,j.message,j.created_at,j.started_at,j.finished_at,`+payload+`
		FROM pk_authorization_jobs j JOIN accounts a ON a.id=j.account_id AND a.deleted_at IS NULL
		WHERE j.id IN(`+marks+`) AND `+authorizationRadarCondition, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		r := &RadarRun{Origin: "authorization", Verdict: "unknown", ModelResults: []RadarModelResult{}, FailedModels: []RadarModelResult{}, MovedToGroupIDs: []int64{}}
		var source, status, stage, message string
		var created time.Time
		var started, finished *time.Time
		var raw []byte
		if err = rows.Scan(&r.ID, &r.AccountID, &source, &status, &stage, &message, &created, &started, &finished, &raw); err != nil {
			return nil, err
		}
		var meta authorizationRadarMetadata
		if err = json.Unmarshal(raw, &meta); err != nil {
			return nil, err
		}
		r.Source, r.Status, r.StartedAt = "manual", "failed", created
		if source == "reauthorization" {
			r.Source = "scheduled"
		}
		r.OriginalGroupIDs = []int64{}
		r.Generation, r.Passed, r.Answer = meta.Generation, meta.Passed, meta.Answer
		if started != nil {
			r.StartedAt = *started
		}
		r.StartedAtEstimated = meta.StartedAt == nil
		if meta.StartedAt != nil {
			r.StartedAt = *meta.StartedAt
		}
		r.FinishedAt = meta.FinishedAt
		if r.FinishedAt == nil && status != "running" {
			r.FinishedAt = finished
		}
		if meta.StartedAt != nil && meta.FinishedAt != nil {
			elapsed := meta.FinishedAt.Sub(*meta.StartedAt).Milliseconds()
			r.DurationMS = &elapsed
		}
		if meta.Passed != nil {
			r.Verdict = "fail"
			if *meta.Passed {
				r.Verdict = "pass"
			}
		}
		if status == "completed" && len(meta.Groups) > 0 {
			r.MovedToGroupIDs = append([]int64{}, meta.Groups...)
			r.MovedToGroupID = &meta.Groups[0]
		}
		if len(meta.Results) == 0 && meta.Passed != nil {
			// Old single-model imports retain only their recorded answer/verdict.
			meta.Results = []AuthorizationIntelligenceResult{{Model: meta.Model, Effort: meta.Effort, Status: "completed", Passed: meta.Passed, Answer: meta.Answer}}
		}
		completed, running := 0, false
		for i, result := range meta.Results {
			m := RadarModelResult{ID: int64(i + 1), Model: result.Model, MappedModel: result.MappedModel, Effort: result.Effort, Status: result.Status,
				Passed: result.Passed, Answer: result.Answer, ErrorMessage: result.ErrorMessage, EgressIP: result.EgressIP,
				EgressCheckedAt: result.EgressCheckedAt, EgressError: result.EgressError, StartedAt: result.StartedAt, FinishedAt: result.FinishedAt}
			if details {
				m.ResponseText = result.ResponseText
			}
			if result.DurationMS > 0 || result.FinishedAt != nil {
				duration := result.DurationMS
				m.DurationMS = &duration
			}
			if m.Status == "running" && status != "running" {
				m.Status = "cancelled"
				if status != "cancelled" {
					m.Status = "failed"
				}
				m.ErrorMessage = "授权任务已结束，此模型未保存完整结果"
			}
			if m.Status == "completed" {
				completed++
			}
			running = running || m.Status == "running"
			r.ModelResults = append(r.ModelResults, m)
			if m.Passed != nil && !*m.Passed && m.Status == "completed" {
				summary := m
				summary.ResponseText = ""
				r.FailedModels = append(r.FailedModels, summary)
			}
			if i == 0 {
				r.Model, r.Effort, r.Answer, r.ResponseText = m.Model, m.Effort, m.Answer, m.ResponseText
			} else {
				r.Model += " / " + m.Model
				r.Answer, r.ResponseText = nil, ""
			}
			if result.EgressMode != "" {
				r.EgressMode = result.EgressMode
			}
			if m.EgressCheckedAt != nil && (r.EgressCheckedAt == nil || m.EgressCheckedAt.After(*r.EgressCheckedAt)) {
				r.EgressIP, r.EgressCheckedAt, r.EgressError = m.EgressIP, m.EgressCheckedAt, m.EgressError
			} else if r.EgressCheckedAt == nil && m.EgressError != "" {
				r.EgressError = m.EgressError
			}
		}
		switch {
		case status == "cancelled":
			r.Status = "cancelled"
		case status == "running" || running:
			r.Status = "running"
		case completed == len(meta.Results) && completed > 0:
			r.Status = "completed"
		case completed > 0:
			r.Status = "partial_failed"
		}
		if r.Status == "failed" || r.Status == "partial_failed" || r.Status == "cancelled" {
			r.ErrorMessage = message
		}
		if meta.Passed != nil && status != "completed" && status != "running" {
			r.MoveError = message
		}
		if r.EgressMode == "" && r.FinishedAt != nil {
			r.EgressError = "此历史授权测试没有保存出口IP观测，不能从当前配置反推历史出口"
		}
		if details {
			r.Prompt = radarPrompt()
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}

func latestAuthorizationRadarIDs(ctx context.Context, q radarQuerier, accounts []int64) ([]int64, error) {
	ids := make([]int64, 0)
	if len(accounts) == 0 {
		return ids, nil
	}
	args, marks := radarIDArgs(accounts)
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT ON(j.account_id) j.id FROM pk_authorization_jobs j
		WHERE j.account_id IN(`+marks+`) AND `+authorizationRadarCondition+`
		ORDER BY j.account_id,`+authorizationRadarTimeSQL+` DESC,j.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *AccountCandyTestService) GetAuthorizationRadarRun(ctx context.Context, accountID, jobID int64) (*RadarRun, error) {
	runs, err := loadAuthorizationRadarRuns(ctx, s.db, []int64{jobID}, true)
	if err != nil {
		return nil, err
	}
	run := runs[jobID]
	if run == nil || run.AccountID != accountID {
		return nil, infraerrors.NotFound("RADAR_RESULT_NOT_FOUND", "测试记录不存在")
	}
	return run, nil
}

func authorizationRadarCanReplace(current, candidate *RadarRun) bool {
	return candidate != nil && (current == nil || (current.Status != "running" && candidate.StartedAt.After(current.StartedAt)))
}

func normalizeAuthorizationRadarGeneration(run *RadarRun, generation string) {
	// Historical authorization records have no generation. Keep their actual
	// recorded judgment, with an explicit authorization origin, without backfill.
	if run.Generation != "" && run.Generation != generation {
		run.Verdict, run.Passed = "unknown", nil
	}
}

func authorizationRadarHistoryIDs(ctx context.Context, q radarQuerier, accountID int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT j.id FROM pk_authorization_jobs j WHERE j.account_id=$1 AND `+authorizationRadarCondition+` ORDER BY `+authorizationRadarTimeSQL+` DESC,j.id DESC LIMIT 30`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

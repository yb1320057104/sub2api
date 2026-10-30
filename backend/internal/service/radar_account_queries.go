package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type radarQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const radarRunColumns = `r.id,r.account_id,r.source,r.status,r.generation,r.verdict,r.original_group_ids,r.moved_to_group_id,r.error_message,r.move_error,r.started_at,r.finished_at,r.duration_ms,COALESCE(host(r.egress_ip),''),r.egress_mode,r.egress_checked_at,r.egress_error`

func radarIDArgs(ids []int64) ([]any, string) {
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	return args, strings.Join(marks, ",")
}

func loadRadarRuns(ctx context.Context, q radarQuerier, ids []int64, details bool) (map[int64]*RadarRun, error) {
	out := make(map[int64]*RadarRun)
	if len(ids) == 0 {
		return out, nil
	}
	args, marks := radarIDArgs(ids)
	rows, err := q.QueryContext(ctx, `SELECT `+radarRunColumns+` FROM pk_radar_account_runs r JOIN accounts a ON a.id=r.account_id AND a.deleted_at IS NULL WHERE r.id IN(`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		r := &RadarRun{Origin: "radar", ModelResults: make([]RadarModelResult, 0), FailedModels: make([]RadarModelResult, 0)}
		var groups []byte
		if err = rows.Scan(&r.ID, &r.AccountID, &r.Source, &r.Status, &r.Generation, &r.Verdict, &groups, &r.MovedToGroupID, &r.ErrorMessage, &r.MoveError, &r.StartedAt, &r.FinishedAt, &r.DurationMS, &r.EgressIP, &r.EgressMode, &r.EgressCheckedAt, &r.EgressError); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(groups, &r.OriginalGroupIDs); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if r.Verdict != "unknown" {
			ok := r.Verdict == "pass"
			r.Passed = &ok
		}
		if details {
			r.Prompt = radarPrompt()
		}
		out[r.ID] = r
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	body := "''"
	if details {
		body = "m.response_text"
	}
	rows, err = q.QueryContext(ctx, `SELECT m.run_id,m.id,m.model,m.mapped_model,m.effort,m.status,m.passed,m.answer,`+body+`,m.error_message,COALESCE(host(m.egress_ip),''),m.egress_checked_at,m.egress_error,m.started_at,m.finished_at,m.duration_ms FROM pk_radar_account_model_results m WHERE m.run_id IN(`+marks+`) ORDER BY m.run_id,m.sort_order`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var m RadarModelResult
		if err = rows.Scan(&id, &m.ID, &m.Model, &m.MappedModel, &m.Effort, &m.Status, &m.Passed, &m.Answer, &m.ResponseText, &m.ErrorMessage, &m.EgressIP, &m.EgressCheckedAt, &m.EgressError, &m.StartedAt, &m.FinishedAt, &m.DurationMS); err != nil {
			return nil, err
		}
		r := out[id]
		if r == nil {
			continue
		}
		r.ModelResults = append(r.ModelResults, m)
		if m.Status == "completed" && m.Passed != nil && !*m.Passed {
			summary := m
			summary.ResponseText = ""
			r.FailedModels = append(r.FailedModels, summary)
		}
		if len(r.ModelResults) == 1 {
			r.Model = m.Model
			r.Effort = m.Effort
			r.Answer = m.Answer
			r.ResponseText = m.ResponseText
		} else {
			r.Model += " / " + m.Model
			r.Answer = nil
			r.ResponseText = ""
		}
		if m.EgressCheckedAt != nil && (r.EgressCheckedAt == nil || m.EgressCheckedAt.After(*r.EgressCheckedAt)) {
			r.EgressIP = m.EgressIP
			r.EgressCheckedAt = m.EgressCheckedAt
			r.EgressError = m.EgressError
		}
	}
	return out, rows.Err()
}

func (s *AccountCandyTestService) GetRadarRun(ctx context.Context, accountID, runID int64) (*RadarRun, error) {
	runs, err := loadRadarRuns(ctx, s.db, []int64{runID}, true)
	if err != nil {
		return nil, err
	}
	r := runs[runID]
	if r == nil || r.AccountID != accountID {
		return nil, infraerrors.NotFound("RADAR_RESULT_NOT_FOUND", "测试记录不存在")
	}
	return r, nil
}

func (s *AccountCandyTestService) ListRadarLatest(ctx context.Context, ids []int64) ([]RadarRun, error) {
	out := make([]RadarRun, 0)
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > 200 {
		return nil, infraerrors.BadRequest("RADAR_TOO_MANY_IDS", "一次最多读取200个账号")
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, infraerrors.BadRequest("RADAR_ACCOUNT_ID", "账号ID无效")
		}
	}
	cfg, err := s.GetRadarConfig(ctx)
	if err != nil {
		return nil, err
	}
	args, marks := radarIDArgs(ids)
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT ON(account_id) id FROM pk_radar_account_runs WHERE account_id IN(`+marks+`) ORDER BY account_id,id DESC`, args...)
	if err != nil {
		return nil, err
	}
	runIDs := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		runIDs = append(runIDs, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	runs, err := loadRadarRuns(ctx, s.db, runIDs, false)
	if err != nil {
		return nil, err
	}
	authorizationIDs, err := latestAuthorizationRadarIDs(ctx, s.db, ids)
	if err != nil {
		return nil, err
	}
	authorizationRuns, err := loadAuthorizationRadarRuns(ctx, s.db, authorizationIDs, false)
	if err != nil {
		return nil, err
	}
	byAccount := make(map[int64]*RadarRun, len(runs))
	for _, r := range runs {
		byAccount[r.AccountID] = r
	}
	for _, r := range authorizationRuns {
		if authorizationRadarCanReplace(byAccount[r.AccountID], r) {
			normalizeAuthorizationRadarGeneration(r, cfg.Generation)
			byAccount[r.AccountID] = r
		}
	}
	found := map[int64]bool{}
	for _, id := range ids {
		if r := byAccount[id]; r != nil && !found[id] {
			if r.Origin != "authorization" && r.Generation != cfg.Generation {
				r.Verdict = "unknown"
				r.Passed = nil
			}
			out = append(out, *r)
			found[r.AccountID] = true
		}
	}
	missing := make([]int64, 0)
	for _, id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		legacy, err := s.ListLatest(ctx, missing)
		if err != nil {
			return nil, err
		}
		for _, r := range legacy {
			if r.Status == "running" {
				r.Status = "failed"
				r.ErrorMessage = "旧任务已失效"
			}
			out = append(out, RadarRun{AccountCandyTestResult: r, Origin: "legacy", Verdict: "unknown", ModelResults: []RadarModelResult{}, FailedModels: []RadarModelResult{}})
		}
	}
	return out, nil
}

type RadarObservation struct {
	Account        *Account   `json:"-"`
	Parent         *Account   `json:"-"`
	Run            *RadarRun  `json:"run"`
	Verdict        string     `json:"verdict"`
	NextRunAt      *time.Time `json:"next_run_at"`
	ScheduleReason string     `json:"schedule_reason"`
}

func (s *AccountCandyTestService) ListRadarAccounts(ctx context.Context, page, size int, filter, search string) ([]RadarObservation, int, *RadarConfig, error) {
	if page < 1 || size < 1 || size > 100 {
		return nil, 0, nil, infraerrors.BadRequest("RADAR_PAGINATION", "分页参数无效")
	}
	if filter != "all" && filter != "pass" && filter != "fail" {
		return nil, 0, nil, infraerrors.BadRequest("RADAR_FILTER", "筛选条件无效")
	}
	if err := s.ensureRadar(ctx); err != nil {
		return nil, 0, nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, err := radarConfigTx(ctx, tx)
	if err != nil {
		return nil, 0, nil, err
	}
	where := ` FROM accounts a LEFT JOIN pk_radar_account_state st ON st.account_id=a.id
		LEFT JOIN pk_radar_account_runs rr ON rr.id=COALESCE(st.active_run_id,st.latest_run_id)` + authorizationRadarAccountJoin + `
		WHERE a.deleted_at IS NULL AND($1='all' OR (` + radarObservedVerdictSQL + `)=$1) AND($3='' OR a.name ILIKE '%'||$3||'%')`
	var total int
	if err = tx.QueryRowContext(ctx, `SELECT count(*)`+where, filter, cfg.Generation, search).Scan(&total); err != nil {
		return nil, 0, nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.id,COALESCE(st.active_run_id,st.latest_run_id),CASE WHEN `+radarUseAuthorizationSQL+` THEN ar.id END,`+radarObservedVerdictSQL+`,st.next_run_at,st.active_run_id IS NOT NULL`+where+` ORDER BY a.id DESC LIMIT $4 OFFSET $5`, filter, cfg.Generation, search, size, (page-1)*size)
	if err != nil {
		return nil, 0, nil, err
	}
	type meta struct {
		id              int64
		runID           sql.NullInt64
		authorizationID sql.NullInt64
		verdict         string
		due             *time.Time
		running         bool
	}
	entries := make([]meta, 0)
	ids, runIDs, authorizationIDs := make([]int64, 0), make([]int64, 0), make([]int64, 0)
	for rows.Next() {
		var m meta
		if err = rows.Scan(&m.id, &m.runID, &m.authorizationID, &m.verdict, &m.due, &m.running); err != nil {
			_ = rows.Close()
			return nil, 0, nil, err
		}
		entries = append(entries, m)
		ids = append(ids, m.id)
		if m.authorizationID.Valid {
			authorizationIDs = append(authorizationIDs, m.authorizationID.Int64)
		} else if m.runID.Valid {
			runIDs = append(runIDs, m.runID.Int64)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, 0, nil, err
	}
	runs, err := loadRadarRuns(ctx, tx, runIDs, false)
	if err != nil {
		return nil, 0, nil, err
	}
	authorizationRuns, err := loadAuthorizationRadarRuns(ctx, tx, authorizationIDs, false)
	if err != nil {
		return nil, 0, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, nil, err
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, ids)
	if err != nil {
		return nil, 0, nil, err
	}
	byID := map[int64]*Account{}
	parentIDs := make([]int64, 0)
	missingRunIDs := make([]int64, 0)
	for _, a := range accounts {
		byID[a.ID] = a
		if a.ParentAccountID != nil {
			parentIDs = append(parentIDs, *a.ParentAccountID)
		}
	}
	parents, err := s.accountRepo.GetByIDs(ctx, parentIDs)
	if err != nil {
		return nil, 0, nil, err
	}
	parentMap := map[int64]*Account{}
	for _, parent := range parents {
		parentMap[parent.ID] = parent
	}
	for _, entry := range entries {
		if !entry.runID.Valid && !entry.authorizationID.Valid {
			missingRunIDs = append(missingRunIDs, entry.id)
		}
	}
	legacy, err := s.ListLatest(ctx, missingRunIDs)
	if err != nil {
		return nil, 0, nil, err
	}
	legacyMap := map[int64]*RadarRun{}
	for _, result := range legacy {
		if result.Status == "running" {
			result.Status = "failed"
			result.ErrorMessage = "旧任务已失效"
		}
		copy := result
		legacyMap[result.AccountID] = &RadarRun{AccountCandyTestResult: copy, Origin: "legacy", Verdict: "unknown", ModelResults: []RadarModelResult{}, FailedModels: []RadarModelResult{}}
	}
	out := make([]RadarObservation, 0, len(entries))
	now := time.Now()
	for _, m := range entries {
		a := byID[m.id]
		if a == nil {
			continue
		}
		r := RadarObservation{Account: a, Run: runs[m.runID.Int64], Verdict: m.verdict, NextRunAt: m.due}
		if m.authorizationID.Valid {
			r.Run = authorizationRuns[m.authorizationID.Int64]
			if r.Run != nil {
				normalizeAuthorizationRadarGeneration(r.Run, cfg.Generation)
			}
		}
		if a.ParentAccountID != nil {
			r.Parent = parentMap[*a.ParentAccountID]
		}
		if r.Run == nil {
			r.Run = legacyMap[a.ID]
		}
		switch {
		case a.Platform != PlatformOpenAI || a.IsSyntheticUITest():
			r.ScheduleReason = "unsupported"
		case !cfg.Enabled:
			r.ScheduleReason = "disabled"
		case a.Status != StatusActive || !a.Schedulable:
			r.ScheduleReason = "inactive"
		case a.ExpiresAt != nil && a.AutoPauseOnExpired && a.ExpiresAt.Before(now):
			r.ScheduleReason = "expired"
		case m.running:
			r.ScheduleReason = "running"
		}
		if r.ScheduleReason == "" && cfg.TargetGroupID != nil {
			for _, g := range a.GroupIDs {
				if g == *cfg.TargetGroupID {
					r.ScheduleReason = "target_group"
					break
				}
			}
		}
		if r.ScheduleReason != "" {
			r.NextRunAt = nil
		} else if r.NextRunAt == nil {
			due := now
			r.NextRunAt = &due
		}
		out = append(out, r)
	}
	return out, total, cfg, nil
}

func (s *AccountCandyTestService) RadarHistory(ctx context.Context, id int64) ([]RadarRun, error) {
	if _, err := s.accountRepo.GetByID(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM pk_radar_account_runs WHERE account_id=$1 ORDER BY id DESC LIMIT 30`, id)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var runID int64
		if err = rows.Scan(&runID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, runID)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	runs, err := loadRadarRuns(ctx, s.db, ids, false)
	if err != nil {
		return nil, err
	}
	out := make([]RadarRun, 0, len(runs)+30)
	for _, runID := range ids {
		if r := runs[runID]; r != nil {
			out = append(out, *r)
		}
	}
	rows, err = s.db.QueryContext(ctx, `SELECT `+accountCandyResultColumns+` FROM pk_account_candy_results r WHERE r.account_id=$1 ORDER BY r.id DESC LIMIT 30`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		r, err := scanAccountCandyResult(rows, false)
		if err != nil {
			return nil, err
		}
		if r.Status == "running" {
			r.Status = "failed"
			r.ErrorMessage = "旧任务已失效"
		}
		out = append(out, RadarRun{AccountCandyTestResult: r, Origin: "legacy", Verdict: "unknown", ModelResults: []RadarModelResult{}, FailedModels: []RadarModelResult{}})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	authorizationIDs, err := authorizationRadarHistoryIDs(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	authorizationRuns, err := loadAuthorizationRadarRuns(ctx, s.db, authorizationIDs, false)
	if err != nil {
		return nil, err
	}
	for _, jobID := range authorizationIDs {
		if r := authorizationRuns[jobID]; r != nil {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

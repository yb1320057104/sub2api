package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

func radarPrompt() string {
	return intelligenceTestCandyPrompt + "\n\n请给出你的解答，并在最后单独一行输出 <final_answer>整数</final_answer>，标签中只填写你求得的最少糖果数目。"
}

func (s *AccountCandyTestService) claimRadar(ctx context.Context, id int64, source string) (*RadarRun, *RadarConfig, error) {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if account.Platform != PlatformOpenAI || account.IsSyntheticUITest() {
		return nil, nil, infraerrors.BadRequest("RADAR_UNSUPPORTED", "糖果测试仅支持真实OpenAI账号")
	}
	if _, err = accountCandyProxyURL(account); err != nil {
		return nil, nil, infraerrors.BadRequest("RADAR_PROXY_UNAVAILABLE", err.Error())
	}
	credential := account
	if account.IsCredentialShadow() {
		credential, err = resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return nil, nil, err
		}
	}
	snapshot, err := accountCandySnapshotIdentity(account, credential)
	if err != nil {
		return nil, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, accountCandyLockID); err != nil {
		return nil, nil, err
	}
	cfg, err := radarConfigTx(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	var platform, status string
	var schedulable, notExpired bool
	err = tx.QueryRowContext(ctx, `SELECT platform,status,schedulable,(expires_at IS NULL OR expires_at>NOW() OR auto_pause_on_expired=false) FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&platform, &status, &schedulable, &notExpired)
	if err != nil {
		return nil, nil, err
	}
	if platform != PlatformOpenAI {
		return nil, nil, infraerrors.Conflict("RADAR_ACCOUNT_CHANGED", "账号平台已更改")
	}
	if source == "scheduled" && (!cfg.Enabled || status != StatusActive || !schedulable || !notExpired) {
		return nil, nil, infraerrors.Conflict("RADAR_NOT_DUE", "账号当前不参与定时测试")
	}
	groups, err := accountCandyGroups(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	if source == "scheduled" && cfg.TargetGroupID != nil {
		for _, g := range groups {
			if g == *cfg.TargetGroupID {
				return nil, nil, infraerrors.Conflict("RADAR_TARGET_GROUP", "目标组账号不自动测试")
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pk_radar_account_state(account_id)VALUES($1)ON CONFLICT DO NOTHING`, id); err != nil {
		return nil, nil, err
	}
	var old sql.NullInt64
	var leased, due bool
	err = tx.QueryRowContext(ctx, `SELECT active_run_id,COALESCE(lease_until>NOW(),false),next_run_at<=NOW() FROM pk_radar_account_state WHERE account_id=$1 FOR UPDATE`, id).Scan(&old, &leased, &due)
	if err != nil {
		return nil, nil, err
	}
	if leased {
		return nil, nil, infraerrors.Conflict("RADAR_RUNNING", "该账号正在测试")
	}
	if source == "scheduled" && !due {
		return nil, nil, infraerrors.Conflict("RADAR_NOT_DUE", "账号尚未到测试时间")
	}
	if old.Valid {
		if _, err = tx.ExecContext(ctx, `UPDATE pk_radar_account_runs SET status='failed',verdict=CASE WHEN verdict='fail' THEN 'fail' ELSE 'unknown' END,error_message='任务租约已过期',finished_at=NOW() WHERE id=$1 AND status='running'`, old.Int64); err != nil {
			return nil, nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pk_radar_account_model_results SET status='failed',error_message='任务租约已过期',finished_at=NOW() WHERE run_id=$1 AND status IN('pending','running')`, old.Int64); err != nil {
			return nil, nil, err
		}
	}
	identity, err := accountCandyIdentity(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	if identity != snapshot {
		return nil, nil, infraerrors.Conflict("RADAR_ACCOUNT_CHANGED", "账号连接配置已变更，请重试")
	}
	run := &RadarRun{Origin: "radar", Generation: cfg.Generation, Verdict: "unknown", EgressMode: "direct", ModelResults: make([]RadarModelResult, 0), FailedModels: make([]RadarModelResult, 0), leaseToken: uuid.NewString()}
	run.AccountCandyTestResult = AccountCandyTestResult{AccountID: id, Source: source, Status: "running", OriginalGroupIDs: groups, Prompt: radarPrompt(), identity: identity, probeAccount: account, credentialAccount: credential}
	if account.ProxyID != nil {
		run.EgressMode = "proxy"
	}
	rawGroups, err := json.Marshal(groups)
	if err != nil {
		return nil, nil, err
	}
	rawModels, err := json.Marshal(cfg.Models)
	if err != nil {
		return nil, nil, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO pk_radar_account_runs(account_id,source,generation,models,original_group_ids,target_group_id,egress_mode)VALUES($1,$2,$3,$4::jsonb,$5::jsonb,$6,$7)RETURNING id,started_at`, id, source, cfg.Generation, string(rawModels), string(rawGroups), cfg.TargetGroupID, run.EgressMode).Scan(&run.ID, &run.StartedAt)
	if err != nil {
		return nil, nil, err
	}
	for i, m := range cfg.Models {
		if !m.Enabled {
			continue
		}
		r := RadarModelResult{Model: m.Model, Effort: m.Effort, MappedModel: account.GetMappedModel(m.Model), Status: "pending"}
		err = tx.QueryRowContext(ctx, `INSERT INTO pk_radar_account_model_results(run_id,sort_order,model,mapped_model,effort)VALUES($1,$2,$3,$4,$5)RETURNING id`, run.ID, i, m.Model, r.MappedModel, m.Effort).Scan(&r.ID)
		if err != nil {
			return nil, nil, err
		}
		run.ModelResults = append(run.ModelResults, r)
	}
	if len(run.ModelResults) == 0 {
		return nil, nil, infraerrors.BadRequest("RADAR_NO_MODELS", "没有启用的模型")
	}
	run.Model = run.ModelResults[0].Model
	run.Effort = run.ModelResults[0].Effort
	if _, err = tx.ExecContext(ctx, `UPDATE pk_radar_account_state SET active_run_id=$2,lease_token=$3,lease_until=NOW()+INTERVAL '6 minutes',verdict='unknown',generation=$4 WHERE account_id=$1`, id, run.ID, run.leaseToken, cfg.Generation); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	return run, cfg, nil
}

func (s *AccountCandyTestService) renewRadar(ctx context.Context, run *RadarRun) error {
	result, err := s.db.ExecContext(ctx, `UPDATE pk_radar_account_state SET lease_until=NOW()+INTERVAL '6 minutes' WHERE account_id=$1 AND active_run_id=$2 AND lease_token=$3 AND lease_until>NOW() AND EXISTS(SELECT 1 FROM pk_radar_account_config WHERE id=1 AND generation=$4)`, run.AccountID, run.ID, run.leaseToken, run.Generation)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return context.Canceled
	}
	return nil
}

type radarModelExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *AccountCandyTestService) writeRadarModel(ctx context.Context, run *RadarRun, m *RadarModelResult) error {
	return writeRadarModelWith(ctx, s.db, run, m)
}

func writeRadarModelWith(ctx context.Context, db radarModelExecutor, run *RadarRun, m *RadarModelResult) error {
	result, err := db.ExecContext(ctx, `UPDATE pk_radar_account_model_results SET status=$2,passed=$3,answer=$4,response_text=$5,error_message=$6,egress_ip=NULLIF($7,'')::inet,egress_checked_at=$8,egress_error=$9,started_at=$10,finished_at=$11,duration_ms=$12 WHERE id=$1 AND run_id=$13 AND EXISTS(SELECT 1 FROM pk_radar_account_state WHERE account_id=$14 AND active_run_id=$13 AND lease_token=$15 AND lease_until>NOW()) AND EXISTS(SELECT 1 FROM pk_radar_account_config WHERE id=1 AND generation=$16)`, m.ID, m.Status, m.Passed, m.Answer, m.ResponseText, m.ErrorMessage, m.EgressIP, m.EgressCheckedAt, m.EgressError, m.StartedAt, m.FinishedAt, m.DurationMS, run.ID, run.AccountID, run.leaseToken, run.Generation)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return context.Canceled
	}
	return nil
}

func (s *AccountCandyTestService) finishRadar(ctx, executionCtx context.Context, run *RadarRun, cfg *RadarConfig, callErr error, elapsed int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
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
	var owned bool
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(active_run_id=$2 AND lease_token=$3 AND lease_until>NOW(),false)FROM pk_radar_account_state WHERE account_id=$1 FOR UPDATE`, run.AccountID, run.ID, run.leaseToken).Scan(&owned)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !owned {
		return nil
	}
	if err != nil {
		return err
	}
	var previousVerdict, previousStatus, moveFailure string
	var moved *int64
	err = tx.QueryRowContext(ctx, `SELECT verdict,status,moved_to_group_id,move_error FROM pk_radar_account_runs WHERE id=$1 FOR UPDATE`, run.ID).Scan(&previousVerdict, &previousStatus, &moved, &moveFailure)
	if err != nil {
		return err
	}
	if previousStatus != "running" {
		return nil
	}
	status, verdict, failure := "completed", "unknown", ""
	if executionCtx.Err() != nil {
		callErr = executionCtx.Err()
	}
	if current.Generation != cfg.Generation || errors.Is(callErr, context.Canceled) {
		status = "cancelled"
		failure = "测试已取消或配置已更新"
	} else if callErr != nil {
		status = "failed"
		failure = "本轮执行失败，请查看逐模型结果"
	}
	identity, identityErr := accountCandyIdentity(ctx, tx, run.AccountID)
	if errors.Is(identityErr, sql.ErrNoRows) || identityErr == nil && identity != run.identity {
		status = "cancelled"
		failure = "账号或连接配置已更改，剩余测试已取消"
	} else if identityErr != nil {
		return identityErr
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_radar_account_model_results SET status='cancelled',error_message='本轮已结束，模型未完成',finished_at=NOW() WHERE run_id=$1 AND status IN('pending','running')`, run.ID); err != nil {
		return err
	}
	var total, completed, failed int
	err = tx.QueryRowContext(ctx, `SELECT count(*),count(*)FILTER(WHERE status='completed'),count(*)FILTER(WHERE status='completed' AND passed=false)FROM pk_radar_account_model_results WHERE run_id=$1`, run.ID).Scan(&total, &completed, &failed)
	if err != nil {
		return err
	}
	if status == "completed" {
		if failed > 0 {
			verdict = "fail"
		} else if total > 0 && completed == total {
			verdict = "pass"
		}
		if completed == 0 {
			status = "failed"
			failure = "所有模型均未取得完整回复"
		} else if completed < total {
			status = "partial_failed"
			failure = "部分模型请求失败，请查看逐模型结果"
		}
	}
	// A complete wrong answer and its move were already committed together.
	// Later errors/cancellation must retain that evidence and never move twice.
	if previousVerdict == "fail" {
		verdict = "fail"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_radar_account_runs SET status=$2,verdict=$3,moved_to_group_id=$4,error_message=$5,move_error=$6,finished_at=NOW(),duration_ms=$7,egress_ip=NULLIF($8,'')::inet,egress_checked_at=$9,egress_error=$10 WHERE id=$1 AND status='running'`, run.ID, status, verdict, moved, failure, moveFailure, elapsed, run.EgressIP, run.EgressCheckedAt, run.EgressError); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_radar_account_state SET active_run_id=NULL,lease_token=NULL,lease_until=NULL,latest_run_id=$2,verdict=$3,generation=$4,next_run_at=CASE WHEN $5 THEN NOW() ELSE NOW()+make_interval(mins=>$6)END WHERE account_id=$1 AND active_run_id=$2`, run.AccountID, run.ID, verdict, cfg.Generation, current.Generation != cfg.Generation, current.IntervalMinutes); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM pk_radar_account_runs WHERE account_id=$1 AND id NOT IN(SELECT id FROM pk_radar_account_runs WHERE account_id=$1 ORDER BY id DESC LIMIT 30) AND status<>'running' AND id<>$2`, run.AccountID, run.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *AccountCandyTestService) expireRadar(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, accountCandyLockID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_radar_account_model_results m SET status='failed',error_message='测试租约已过期',finished_at=NOW() FROM pk_radar_account_state st WHERE m.run_id=st.active_run_id AND st.lease_until<=NOW() AND m.status IN('pending','running')`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `WITH expired AS MATERIALIZED(SELECT account_id,active_run_id FROM pk_radar_account_state WHERE active_run_id IS NOT NULL AND lease_until<=NOW() FOR UPDATE), closed AS(UPDATE pk_radar_account_runs r SET status='failed',verdict=CASE WHEN r.verdict='fail' THEN 'fail' ELSE 'unknown' END,error_message='测试租约已过期',finished_at=NOW() FROM expired e WHERE r.id=e.active_run_id AND r.status='running' RETURNING r.id)UPDATE pk_radar_account_state st SET latest_run_id=e.active_run_id,active_run_id=NULL,lease_token=NULL,lease_until=NULL,verdict=CASE WHEN st.verdict='fail' THEN 'fail' ELSE 'unknown' END,next_run_at=NOW() FROM expired e WHERE st.account_id=e.account_id AND st.active_run_id=e.active_run_id`); err != nil {
		return err
	}
	return tx.Commit()
}

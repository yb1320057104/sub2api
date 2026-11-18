package service

import (
	"context"
	"time"
)

func (s *KiloService) isSourceOnboardingJob(ctx context.Context, id int64) (bool, error) {
	var kind string
	err := s.db.QueryRowContext(ctx, `SELECT job_kind FROM pk_kilo_jobs WHERE id=$1`, id).Scan(&kind)
	return kind == KiloJobOnboardSource || kind == KiloJobUnbindSource, err
}

func (s *KiloService) retrySourceOnboardingJob(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var preparedID, quotaTaskID *int64
	var status, stage, cipher, outcome, kind string
	var quotaReady, reused, connected bool
	if err = tx.QueryRowContext(ctx, `SELECT prepared_id,status,stage,remote_state_encrypted,remote_outcome,quota_oauth_ready,quota_task_id,job_kind,reused_identity,subscription_connected FROM pk_kilo_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&preparedID, &status, &stage, &cipher, &outcome, &quotaReady, &quotaTaskID, &kind, &reused, &connected); err != nil {
		return err
	}
	if status != "failed" && status != "awaiting_authorization" {
		return kiloConflict("仅失败或等待中的任务可重试")
	}
	state, err := s.readOnboardingState(cipher)
	if err != nil {
		return err
	}
	// Re-reading an unresolved operation is not a new authorization. Preserve its
	// exact revision and phase before blocking any attempt to replay the grant.
	resumeOperation := !state.RemoteClosed && state.OperationID != "" && outcome != "failed" && outcome != "cancelled" && outcome != "needs_action"
	if !resumeOperation && (state.ReconcileSubscription || state.ReauthorizeSubscription || kind == KiloJobOnboardSource && preparedID == nil && state.RemoteUserID != "" && (state.SourcePlanType == "plus" || state.SourcePlanType == "pro") && stage == "subscription_authorization") {
		return kiloConflict("现存个人订阅只能使用只读订阅核对入口，普通重试不会重新授权")
	}
	if preparedID != nil {
		var sourceID *int64
		if err = tx.QueryRowContext(ctx, `SELECT source_account_id FROM pk_kilo_jobs WHERE id=$1`, id).Scan(&sourceID); err != nil {
			return err
		}
		policy, policyErr := kiloReadLifecyclePolicy(ctx, tx, *preparedID)
		if policyErr != nil {
			return policyErr
		}
		if policyErr = kiloRequireLifecycleSource(policy, sourceID); policyErr != nil {
			return policyErr
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status='binding',binding_job_id=$2,last_error='',updated_at=NOW() WHERE id=$1 AND status IN ('ready','binding') AND (binding_job_id IS NULL OR binding_job_id=$2)`, *preparedID, id)
		if updateErr != nil {
			return updateErr
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return kiloConflict("预备账号已被其他任务接管")
		}
	}
	// Unknown or still-owned operations resume their exact job/revision/phase.
	// A new local lease fences the old claimant; inventing another executor
	// revision here could start a second login before the first one stops.
	if kind == KiloJobUnbindSource {
		stage = "disconnect_subscription"
	} else if !quotaReady {
		stage = "quota_oauth"
	} else if preparedID != nil && (!reused || connected) {
		stage = "credential_verified"
	} else if !quotaReady {
		stage = "quota_oauth"
	} else if state.DeviceSessionID != "" && (state.DeviceApproved || outcome == "unknown") {
		stage = "device_authorization"
	} else if resumeOperation {
		stage = kiloExecutorPhaseStage(state.Phase)
	} else {
		stage = "kilo_login"
	}
	if !quotaReady && quotaTaskID != nil && s.reauth != nil && s.reauth.repo != nil {
		quota, readErr := s.reauth.repo.GetTask(ctx, *quotaTaskID)
		if readErr != nil {
			return readErr
		}
		if quota != nil && quota.Status == OpenAIOAuthReauthStatusFailed {
			quotaTaskID = nil
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='queued',stage=$2,revision=revision+CASE WHEN $3 THEN 0 ELSE 1 END,lease_token=NULL,lease_until=NULL,error_code='',failed_stage='',next_action='',last_error='',quota_task_id=$4,next_attempt_at=NOW(),finished_at=NULL,updated_at=NOW() WHERE id=$1 AND (lease_until IS NULL OR lease_until<=NOW())`, id, stage, resumeOperation, quotaTaskID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return kiloConflict("任务仍有执行者，请稍后重试")
	}
	return tx.Commit()
}

func (s *KiloService) cancelSourceOnboardingJob(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var preparedID *int64
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT prepared_id,status FROM pk_kilo_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&preparedID, &status); err != nil {
		return err
	}
	if status == "succeeded" {
		return kiloConflict("已完成账号请使用完整退出操作")
	}
	if status == "cancelled" {
		if err = tx.Commit(); err != nil {
			return err
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return s.cancelOnboardingRemote(cleanup, id)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='cancelled',stage='cancelled',revision=revision+1,lease_token=NULL,lease_until=NULL,finished_at=NOW(),updated_at=NOW() WHERE id=$1`, id); err != nil {
		return err
	}
	if preparedID != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status=CASE WHEN source_account_id IS NULL THEN 'ready' ELSE 'disabled' END,binding_state='blocked',binding_error='cancelled_binding_requires_remote_cleanup',binding_job_id=NULL,updated_at=NOW() WHERE id=$1 AND binding_job_id=$2 AND status='binding'`, *preparedID, id); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// Revoke the durable execution before remote cleanup. The full receipt
	// survives failures, so maintenance can retry cancellation and release the
	// exact asynchronous login lease only after the remote operation stops.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err = s.cancelOnboardingRemote(cleanup, id); err != nil {
		return err
	}
	if preparedID != nil {
		_, unlock, lockErr := s.lockPreparedOperation(ctx, *preparedID)
		if lockErr != nil {
			return lockErr
		}
		unlock()
	}
	// quota-only is intentionally not removed: a cancelled activation never
	// silently restores the source's direct-to-OpenAI inference capability.
	return nil
}

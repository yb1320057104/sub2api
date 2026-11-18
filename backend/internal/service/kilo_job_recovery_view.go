package service

import (
	"context"
	"strings"
)

const (
	kiloJobRecoveryRetry       = "retry"
	kiloJobRecoveryReconcile   = "reconcile_subscription"
	kiloJobRecoveryReauthorize = "reauthorize_subscription"
)

const kiloJobProjection = `SELECT j.id,j.batch_id::text,j.prepared_id,COALESCE(p.name,''),j.source_account_id,COALESCE(a.name,''),j.account_id,j.stage,j.status,j.last_error,j.verification_url,j.user_code,j.created_at,j.finished_at,j.job_kind,j.organization_id,j.login_proxy_id,j.error_code,j.failed_stage,j.next_action,j.next_attempt_at,j.credential_ready,j.subscription_connected,j.quota_oauth_ready,COALESCE(q.error_message,''),j.revision,j.remote_state_encrypted,j.remote_outcome,j.source_identity,j.reused_identity,COALESCE(j.lease_until>NOW(),false),j.material_version
FROM pk_kilo_jobs j
LEFT JOIN pk_kilo_prepared_accounts p ON p.id=j.prepared_id
LEFT JOIN accounts a ON a.id=j.source_account_id
LEFT JOIN openai_oauth_reauth_tasks q ON q.id=j.quota_task_id AND q.account_id=j.source_account_id
  AND q.oauth_profile IN ('codex','') AND q.status='failed'
  AND j.job_kind='onboard_source' AND j.stage='quota_oauth' AND j.status='failed' AND j.error_code='KILO_QUOTA_OAUTH_FAILED' `

// Private state is read from the same row as the public revision. A view never
// closes an operation or grants authority; mutations recheck under their locks.
type kiloJobRecoverySnapshot struct {
	kiloFailedJob
	MaterialVersion string
}

func (s *KiloService) kiloJobs(ctx context.Context, where string, args ...any) ([]KiloJob, error) {
	rows, err := s.db.QueryContext(ctx, kiloJobProjection+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KiloJob{}
	snapshots := []kiloJobRecoverySnapshot{}
	for rows.Next() {
		j := KiloJob{RecoveryActions: []string{}}
		var snapshot kiloJobRecoverySnapshot
		var quotaError string
		if err = rows.Scan(&j.ID, &j.BatchID, &j.PreparedID, &j.PreparedName, &j.SourceAccountID, &j.SourceName, &j.AccountID, &j.Stage, &j.Status, &j.LastError, &j.VerificationURL, &j.UserCode, &j.CreatedAt, &j.FinishedAt, &j.JobKind, &j.OrganizationID, &j.LoginProxyID, &j.ErrorCode, &j.FailedStage, &j.NextAction, &j.NextAttemptAt, &j.CredentialReady, &j.SubscriptionConnected, &j.QuotaOAuthReady, &quotaError, &j.Revision, &snapshot.Cipher, &snapshot.Outcome, &snapshot.SourceIdentity, &snapshot.Reused, &snapshot.LiveLease, &snapshot.MaterialVersion); err != nil {
			return nil, err
		}
		snapshot.ID, snapshot.Revision = j.ID, j.Revision
		snapshot.PreparedID, snapshot.SourceID, snapshot.AccountID, snapshot.LoginProxyID = j.PreparedID, j.SourceAccountID, j.AccountID, j.LoginProxyID
		snapshot.Kind, snapshot.Status, snapshot.Stage = j.JobKind, j.Status, j.Stage
		snapshot.CredentialReady, snapshot.Connected = j.CredentialReady, j.SubscriptionConnected
		// Older jobs stored a generic error. Read their own trusted diagnostic without re-running login.
		if failure := kiloQuotaOAuthInteractionFailure(quotaError); failure != nil {
			j.ErrorCode, j.LastError, j.NextAction = failure.code, failure.message, failure.action
		}
		out, snapshots = append(out, j), append(snapshots, snapshot)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// Release the result connection before read-only eligibility queries, including
	// deployments that use a single connection for administrative reads.
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		s.populateKiloJobRecovery(ctx, &out[i], &snapshots[i])
	}
	return out, ctx.Err()
}

func kiloJobRetryAllowed(job *kiloFailedJob, state *kiloOnboardingRuntime) bool {
	if job == nil || job.LiveLease || (job.Status != "failed" && job.Status != "awaiting_authorization") {
		return false
	}
	if job.Kind != KiloJobOnboardSource && job.Kind != KiloJobUnbindSource {
		return true
	}
	if state == nil {
		return false
	}
	// Match ordinary retry's original-operation exception without permitting a
	// fresh grant for a saved personal identity or a subscription recovery.
	resume := !state.RemoteClosed && state.OperationID != "" && job.Outcome != "failed" && job.Outcome != "cancelled" && job.Outcome != "needs_action"
	return resume || !(state.ReconcileSubscription || state.ReauthorizeSubscription || job.Kind == KiloJobOnboardSource && job.PreparedID == nil && state.RemoteUserID != "" && (state.SourcePlanType == "plus" || state.SourcePlanType == "pro") && job.Stage == "subscription_authorization")
}

func kiloJobSubscriptionRecoveryActions(job *KiloJob, snapshot *kiloJobRecoverySnapshot, state *kiloOnboardingRuntime) []string {
	actions := []string{}
	if job == nil || snapshot == nil || validateKiloReconcileCandidate(&snapshot.kiloFailedJob, state, job.Revision) != nil || job.OrganizationID != "" || snapshot.MaterialVersion == "" || !job.QuotaOAuthReady {
		return actions
	}
	if job.ErrorCode != "IDENTITY_MISMATCH" && job.ErrorCode != "BYOK_ENABLE_UNVERIFIED" && !(job.ErrorCode == "SESSION_LOST" && state.ReconcileSubscription) {
		return actions
	}
	actions = append(actions, kiloJobRecoveryReconcile)
	if job.ErrorCode == "IDENTITY_MISMATCH" && !state.ReconcileSubscription && state.ConnectionSubject == "" && state.ConnectionAt.IsZero() && state.IdentityAssociation == nil && state.ConnectionObservation == nil {
		actions = append(actions, kiloJobRecoveryReauthorize)
	}
	return actions
}

func (s *KiloService) populateKiloJobRecovery(ctx context.Context, job *KiloJob, snapshot *kiloJobRecoverySnapshot) {
	if job.Status != "failed" && job.Status != "awaiting_authorization" || snapshot.LiveLease {
		return
	}
	var state *kiloOnboardingRuntime
	if job.JobKind == KiloJobOnboardSource || job.JobKind == KiloJobUnbindSource {
		if s.encryptor == nil {
			return
		}
		var err error
		state, err = s.readOnboardingState(snapshot.Cipher)
		if err != nil {
			return
		}
	}
	if kiloJobRetryAllowed(&snapshot.kiloFailedJob, state) {
		job.RecoveryActions = append(job.RecoveryActions, kiloJobRecoveryRetry)
	}
	actions := kiloJobSubscriptionRecoveryActions(job, snapshot, state)
	if len(actions) == 0 || s.client == nil || s.reauth == nil || s.accounts == nil || s.proxies == nil || !s.encryptionReady() {
		return
	}
	// Any unavailable or changed dependency fails closed for this view. No
	// external call, login, cleanup, acknowledgment or identity mutation occurs.
	if checkKiloFailedBusy(ctx, s.db, &snapshot.kiloFailedJob) != nil {
		return
	}
	var deviceExists bool
	if err := kiloScan(ctx, s.db, `SELECT EXISTS(SELECT 1 FROM pk_kilo_device_sessions WHERE account_id=$1 AND status IN ('pending','approved','saved'))`, []any{*job.AccountID}, &deviceExists); err != nil || deviceExists {
		return
	}
	claim := &kiloClaim{ID: job.ID, Revision: job.Revision, SourceID: job.SourceAccountID, SourceIdentity: snapshot.SourceIdentity, AccountID: job.AccountID, LoginProxyID: job.LoginProxyID, QuotaOAuthReady: true}
	source, err := s.accounts.GetByID(ctx, *job.SourceAccountID)
	if err != nil || s.checkOnboardingSource(ctx, claim, source) != nil || validateKiloReconcileSource(source, state) != nil {
		return
	}
	gateway, err := s.accounts.GetByID(ctx, *job.AccountID)
	if err != nil || !kiloOnboardingIdentityMatches(gateway, claim) || gateway.GetCredential("api_key") != "" || ParseExtraInt(gateway.Extra["kilo_prepared_id"]) != 0 || gateway.ProxyID != nil && *gateway.ProxyID != *job.LoginProxyID {
		return
	}
	proxy, err := s.proxies.GetByID(ctx, *job.LoginProxyID)
	if err != nil || !usableAssignedAuthProxy(proxy) || state.ProxyHash != kiloOnboardingProxyHash(proxy.URL()) {
		return
	}
	var routeOwned bool
	if err = kiloScan(ctx, s.db, `SELECT EXISTS(SELECT 1 FROM pk_kilo_egress_routes WHERE account_id=$1 AND proxy_id=$2 AND config_hash=$3 AND state=$4 AND verified_at IS NOT NULL)`, []any{*job.AccountID, *job.LoginProxyID, state.ProxyHash, KiloEgressActive}, &routeOwned); err != nil || !routeOwned {
		return
	}
	material, err := s.reauth.ReadKiloLoginMaterial(ctx, *job.SourceAccountID)
	if err != nil || material == nil || material.Version != snapshot.MaterialVersion || material.Password == "" || !strings.EqualFold(strings.TrimSpace(material.Email), strings.TrimSpace(state.SourceEmail)) {
		return
	}
	job.RecoveryActions = append(job.RecoveryActions, actions...)
}

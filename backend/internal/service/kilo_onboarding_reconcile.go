package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

const kiloSnapshotSourceClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

// Durable receipts are checked without the old operation clock. Only their
// initial acceptance may establish that an observation belongs to an operation.
func kiloCredentialIdentityMatches(state *kiloOnboardingRuntime, evidence *KiloCredentialIdentityEvidence, subject string, connectedAt time.Time) bool {
	if state == nil || evidence == nil || (state.SourcePlanType != "plus" && state.SourcePlanType != "pro") || state.SourceIssuer != "https://auth.openai.com" || state.SourceSubject == "" || state.SourceWorkspace == "" || state.RemoteUserID == "" {
		return false
	}
	fingerprint, err := hex.DecodeString(evidence.ProviderFingerprint)
	return err == nil && len(fingerprint) == 32 && len(evidence.ProviderFingerprint) == 64 &&
		evidence.Version == 1 && evidence.Method == "credential_snapshot_password_totp" &&
		evidence.KiloUserID == state.RemoteUserID && evidence.KiloClientID != "" && evidence.SourceClientID == kiloSnapshotSourceClientID && evidence.KiloClientID != evidence.SourceClientID &&
		evidence.SourceIssuer == state.SourceIssuer && evidence.SourceSubject == state.SourceSubject && evidence.SourceWorkspaceID == state.SourceWorkspace && evidence.SourcePlanType == state.SourcePlanType &&
		subject != "" && evidence.ConnectionSubject == subject && !connectedAt.IsZero() && !evidence.ProviderCreatedAt.IsZero() && !evidence.ProviderCreatedAt.After(connectedAt) && !evidence.ObservedAt.IsZero() &&
		evidence.KiloPasswordTOTPVerified && evidence.SourcePasswordTOTPVerified && evidence.SourceSnapshotSignatureVerified && evidence.SourceSignatureVerified && evidence.NonceVerified && evidence.ProviderUnchanged
}

func kiloObservationInOperation(state *kiloOnboardingRuntime, observed, now time.Time) bool {
	return state != nil && kiloOnboardingEvidenceInPhase(state, observed, now)
}

func kiloReconcileSessionFailureDetails(code string) *kiloOnboardingFailure {
	return &kiloOnboardingFailure{code: code, message: "原只读核对会话不可恢复，未重新登录或授权订阅", action: "保留原身份、固定代理和操作回执，核对执行器会话后处理", retryable: false}
}

func kiloReconcileSessionFailure(code string) error {
	return kiloReconcileSessionFailureDetails(code)
}

// This restores the recorded Kilo owner, not a prepared credential or an owner
// inferred from the source email. It intentionally has no API key dependency.
func kiloReconcileIdentity(state *kiloOnboardingRuntime) (*KiloExecutorIdentityCredential, error) {
	if state == nil || state.RemoteUserID == "" || state.SourceEmail == "" || state.IdentitySessionMaterial == "" || len(state.IdentitySessionMaterial) > 64*1024 {
		return nil, kiloOnboardingFail("IDENTITY_MISMATCH", "缺少原 Kilo 身份的加密会话材料", "保留原身份，不能以来源邮箱推断或创建新身份", false)
	}
	email := state.SourceEmail
	if state.IdentityCredential != nil && state.IdentityCredential.Email != "" {
		email = state.IdentityCredential.Email
	}
	if !strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(state.SourceEmail)) {
		return nil, kiloOnboardingFail("IDENTITY_MISMATCH", "原 Kilo 身份邮箱与接入身份快照不一致", "保留原身份材料并核对归属", false)
	}
	return &KiloExecutorIdentityCredential{UserID: state.RemoteUserID, Email: email, SessionMaterial: state.IdentitySessionMaterial, Credential: state.IdentityCredential}, nil
}

func validateKiloReconcileSource(source *Account, state *kiloOnboardingRuntime) error {
	if state == nil || (state.SourcePlanType != "plus" && state.SourcePlanType != "pro") || state.SourceSubject == "" || state.SourceIssuer != "https://auth.openai.com" || state.SourceWorkspace == "" || !strings.EqualFold(strings.TrimSpace(state.SourceEmail), kiloSourceEmail(source)) {
		return kiloOnboardingFail("IDENTITY_MISMATCH", "只读订阅核对缺少原个人来源身份快照", "保留原任务，不能更换来源或推断主体归属", false)
	}
	view := *state
	input := KiloExecutorTaskInput{Phase: "reconcile_subscription"}
	if err := setKiloExecutorExpectedIdentity(&input, source, &view); err != nil {
		return err
	}
	if input.SourceIDToken == "" || input.ExpectedIdentity.SourceSubject != state.SourceSubject || input.ExpectedIdentity.SourceIssuer != state.SourceIssuer {
		return kiloOnboardingFail("IDENTITY_MISMATCH", "来源缺少原身份的官方签发快照", "核对服务器保存的原 OAuth 身份，禁止管理员补造证明", false)
	}
	_, err := kiloReconcileIdentity(state)
	return err
}

func validateKiloReconcileCandidate(job *kiloFailedJob, state *kiloOnboardingRuntime, expectedRevision int64) error {
	if job == nil || state == nil || expectedRevision <= 0 || job.Revision != expectedRevision || state.Revision != job.Revision || job.Kind != KiloJobOnboardSource || job.Status != "failed" || job.Stage != "subscription_authorization" || job.LiveLease || job.PreparedID != nil || job.Reused || job.CredentialReady || job.Connected || job.SourceID == nil || *job.SourceID <= 0 || job.AccountID == nil || *job.AccountID <= 0 || job.LoginProxyID == nil || *job.LoginProxyID <= 0 {
		return kiloConflict("任务版本、失败阶段或身份归属不符合只读订阅核对条件")
	}
	// Cleanup must already have proved the old operation closed. Never cancel,
	// acknowledge or erase an unresolved intent just to admit a new observation.
	if !state.RemoteClosed || state.OperationID != "" || state.TaskID != "" || state.Phase != "" || state.RemoteLeaseID != "" || len(state.AckTaskIDs) != 0 || !state.DeadlineAt.IsZero() || state.SessionHandle != "" {
		return kiloConflict("原远端操作尚未闭合，请保留原操作回执等待确认")
	}
	if state.RemoteUserID == "" || state.BYOKEnabled || state.DeviceApproved || state.DeviceSessionID != "" || state.DeviceVerificationURL != "" || state.DeviceUserCode != "" || state.ConnectionOrganization != "" || state.ConnectionWorkspaceID != "" || state.ProxyHash == "" || (state.SourcePlanType != "plus" && state.SourcePlanType != "pro") {
		return kiloConflict("仅保留原个人 Kilo 身份且尚未建立设备或凭据的失败任务可核对")
	}
	_, err := kiloReconcileIdentity(state)
	return err
}

// ReconcileSubscription enqueues observation of an existing personal connection.
// Its caller supplies only a revision; all identity and proof material is loaded
// under the existing job/account/egress ownership locks.
func (s *KiloService) ReconcileSubscription(ctx context.Context, id, expectedRevision int64) error {
	return s.queueKiloSubscriptionRecovery(ctx, id, expectedRevision, false)
}

// ReauthorizeSubscription is a new administrator-authorized attempt, never a retry
// of an unresolved grant. The adapter rechecks the saved owner and disconnected
// state before login and again immediately before the new delegated grant.
func (s *KiloService) ReauthorizeSubscription(ctx context.Context, id, expectedRevision int64) error {
	return s.queueKiloSubscriptionRecovery(ctx, id, expectedRevision, true)
}

func (s *KiloService) queueKiloSubscriptionRecovery(ctx context.Context, id, expectedRevision int64, reauthorize bool) error {
	if id <= 0 || expectedRevision <= 0 {
		return kiloBad("KILO_REVISION_REQUIRED", "必须提供有效任务编号及大于零的 expected_revision")
	}
	if !s.encryptionReady() || s.client == nil || s.reauth == nil {
		return kiloOnboardingFail("LOGIN_MATERIAL_MISSING", "只读核对所需的加密存储或登录材料服务不可用", "检查现有凭据与加密配置", false)
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	job, err := readKiloFailedJob(ctx, tx.Client(), id, true)
	if err != nil {
		return err
	}
	state, err := s.readOnboardingState(job.Cipher)
	if err != nil {
		return err
	}
	if err = validateKiloReconcileCandidate(job, state, expectedRevision); err != nil {
		return err
	}
	if reauthorize && (state.ReconcileSubscription || state.ConnectionSubject != "" || !state.ConnectionAt.IsZero() || state.IdentityAssociation != nil || state.ConnectionObservation != nil) {
		return kiloConflict("已有订阅观察或授权回执，不能作为未授权失败重新执行")
	}
	var code, organization, version string
	var quotaReady bool
	if err = kiloScan(ctx, tx.Client(), `SELECT error_code,organization_id,material_version,quota_oauth_ready FROM pk_kilo_jobs WHERE id=$1`, []any{id}, &code, &organization, &version, &quotaReady); err != nil {
		return err
	}
	closedObservationSessionLost := code == "SESSION_LOST" && state.ReconcileSubscription
	if reauthorize && code != "IDENTITY_MISMATCH" {
		return kiloConflict("仅已闭合且未完成订阅的身份校验失败可显式重新授权")
	}
	if (code != "IDENTITY_MISMATCH" && code != "BYOK_ENABLE_UNVERIFIED" && !closedObservationSessionLost) || organization != "" || version == "" || !quotaReady {
		return kiloConflict("仅原个人身份核验或启用依据未确认的订阅失败任务可只读核对")
	}
	txctx := dbent.NewTxContext(ctx, tx)
	if err = s.lockKiloFailedAccounts(txctx, tx, job); err != nil {
		return err
	}
	if err = checkKiloFailedBusy(ctx, tx.Client(), job); err != nil {
		return err
	}
	var deviceExists bool
	if err = kiloScan(ctx, tx.Client(), `SELECT EXISTS(SELECT 1 FROM pk_kilo_device_sessions WHERE account_id=$1 AND status IN ('pending','approved','saved'))`, []any{*job.AccountID}, &deviceExists); err != nil {
		return err
	}
	if deviceExists {
		return kiloConflict("任务已经建立设备授权，不能进入只读订阅核对")
	}
	claim := &kiloClaim{ID: job.ID, Revision: job.Revision, SourceID: job.SourceID, SourceIdentity: job.SourceIdentity, AccountID: job.AccountID, LoginProxyID: job.LoginProxyID, QuotaOAuthReady: true}
	source, err := s.accounts.GetByID(txctx, *job.SourceID)
	if err != nil {
		return err
	}
	if err = s.checkOnboardingSource(txctx, claim, source); err != nil {
		return err
	}
	if err = validateKiloReconcileSource(source, state); err != nil {
		return err
	}
	gateway, err := s.accounts.GetByID(txctx, *job.AccountID)
	if err != nil {
		return err
	}
	if !kiloOnboardingIdentityMatches(gateway, claim) || gateway.GetCredential("api_key") != "" || ParseExtraInt(gateway.Extra["kilo_prepared_id"]) != 0 || gateway.ProxyID != nil && *gateway.ProxyID != *job.LoginProxyID {
		return kiloConflict("原网关已经取得凭据或固定身份归属已改变")
	}
	var lockedProxyID int64
	if err = kiloScan(ctx, tx.Client(), `SELECT id FROM proxies WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, []any{*job.LoginProxyID}, &lockedProxyID); err != nil {
		return err
	}
	proxy, err := s.proxies.GetByID(txctx, *job.LoginProxyID)
	if err != nil || !usableAssignedAuthProxy(proxy) || state.ProxyHash != kiloOnboardingProxyHash(proxy.URL()) {
		return kiloBad("LOGIN_PROXY_CONFLICT", "原固定登录代理或其凭据已改变，禁止切换代理或直连")
	}
	var routeOwned bool
	if err = kiloScan(ctx, tx.Client(), `SELECT EXISTS(SELECT 1 FROM pk_kilo_egress_routes WHERE account_id=$1 AND proxy_id=$2 AND config_hash=$3 AND state=$4 AND verified_at IS NOT NULL)`, []any{*job.AccountID, *job.LoginProxyID, state.ProxyHash, KiloEgressActive}, &routeOwned); err != nil {
		return err
	}
	if !routeOwned {
		return kiloConflict("原身份的已核验固定认证出口归属不完整")
	}
	material, err := s.reauth.ReadKiloLoginMaterial(txctx, *job.SourceID)
	if err != nil {
		return err
	}
	if material == nil || material.Version != version || material.Password == "" || !strings.EqualFold(strings.TrimSpace(material.Email), strings.TrimSpace(state.SourceEmail)) {
		return kiloOnboardingFail("LOGIN_MATERIAL_CHANGED", "原登录凭据版本已改变，不能继续原身份只读核对", "保留原任务和身份，核对原始登录材料", false)
	}
	// The old operation is closed and its durable identity material is verified.
	// A new observation binds its own executor; it cannot inherit an old instance.
	state.InstanceID = ""
	state.ReconcileSubscription, state.ReauthorizeSubscription = !reauthorize, reauthorize
	state.Revision, state.Failures = job.Revision+1, 0
	if reauthorize {
		state.ReauthorizationHistory = append(state.ReauthorizationHistory, kiloSubscriptionRecovery{Revision: job.Revision, ErrorCode: code, Outcome: job.Outcome, AuthorizedAt: time.Now().UTC()})
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	cipher, err := s.encryptor.Encrypt(string(raw))
	if err != nil {
		return kiloOnboardingFail("ONBOARDING_STATE_INVALID", "只读核对恢复记录加密失败", "检查现有加密配置", false)
	}
	result, err := tx.Client().ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='queued',stage='subscription_authorization',revision=$3,remote_state_encrypted=$4,remote_outcome='',credential_ready=false,subscription_connected=false,error_code='',failed_stage='',next_action='',last_error='',verification_url='',user_code='',lease_token=NULL,lease_until=NULL,next_attempt_at=NOW(),finished_at=NULL,updated_at=NOW() WHERE id=$1 AND revision=$2 AND status='failed' AND (lease_until IS NULL OR lease_until<=NOW())`, id, job.Revision, state.Revision, cipher)
	if err != nil {
		return err
	}
	if n, affectedErr := result.RowsAffected(); affectedErr != nil {
		return affectedErr
	} else if n != 1 {
		return kiloConflict("任务版本或执行租约已改变，请刷新后核对")
	}
	return tx.Commit()
}

func (s *KiloService) processReconcileSubscription(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime, source *Account, material *KiloLoginMaterial) error {
	if !state.ReconcileSubscription || state.Revision != job.Revision || job.Kind != KiloJobOnboardSource || job.Stage != "subscription_authorization" || job.OrganizationID != "" || job.PreparedID != 0 || job.ReusedIdentity || job.CredentialReady || job.SubscriptionConnected || !job.QuotaOAuthReady || state.BYOKEnabled || state.DeviceApproved || state.DeviceSessionID != "" || state.ConnectionOrganization != "" || state.ConnectionWorkspaceID != "" {
		return kiloOnboardingFail("ONBOARDING_STAGE_INVALID", "只读订阅核对的任务或凭据边界已改变", "保留原身份和回执，禁止转入授权、设备或取 Key", false)
	}
	if material == nil || job.MaterialVersion == "" || material.Version != job.MaterialVersion || material.Password == "" {
		return kiloOnboardingFail("LOGIN_MATERIAL_CHANGED", "原登录凭据版本已改变", "保留原任务并核对原始登录材料", false)
	}
	if err := validateKiloReconcileSource(source, state); err != nil {
		return err
	}
	if state.RemoteClosed && (state.OperationID != "" || state.TaskID != "") {
		return kiloOnboardingFail("REMOTE_OUTCOME_UNKNOWN", "已关闭的观察记录仍含未结操作", "保留原操作回执，禁止启动第二认证", false)
	}
	return s.runOnboardingPhase(ctx, job, state, "reconcile_subscription", &KiloExecutorCredential{Email: material.Email, Password: material.Password, TOTPSecret: material.TOTPSecret})
}

func validateKiloReconcileObservation(job *kiloClaim, state *kiloOnboardingRuntime, task *KiloExecutorTask, sourceEmail string, now time.Time) error {
	if state == nil || task == nil || !state.ReconcileSubscription || task.Phase != "reconcile_subscription" || task.Status != "succeeded" || job.OrganizationID != "" || task.OrganizationID != "" || task.BYOKEnabled || task.BYOKEnableEvidence != "" || task.ConnectionWorkspaceID != "" || task.ConnectionIdentityEvidence != "credential_snapshot" || task.VerifiedIdentity.KiloUserID != state.RemoteUserID || !strings.EqualFold(strings.TrimSpace(task.VerifiedIdentity.OpenAIEmail), sourceEmail) || !strings.EqualFold(strings.TrimSpace(task.ConnectionEmail), sourceEmail) || !kiloCredentialAssociationMatches(state, task.IdentityAssociation, task.ConnectionSubject) || !kiloCredentialIdentityMatches(state, task.CredentialIdentityEvidence, task.ConnectionSubject, task.ConnectionAt) {
		return kiloOnboardingFail("IDENTITY_MISMATCH", "现存订阅观察的原身份或凭据快照不匹配", "保留原连接和身份，禁止重新授权", false)
	}
	evidence, observation := task.CredentialIdentityEvidence, task.ConnectionObservation
	if task.IdentityAssociation.KiloClientID != evidence.KiloClientID || !kiloOnboardingEvidenceInPhase(state, task.IdentityAssociation.VerifiedAt, now) {
		return kiloOnboardingFail("IDENTITY_MISMATCH", "现存连接身份关联与本次凭据核对不一致", "保留原身份和提供方回执，禁止重新授权", false)
	}
	if observation == nil || observation.Version != 1 || observation.Method != "existing_delegated_connection" || observation.KiloUserID != state.RemoteUserID || observation.OrganizationID != "" || !strings.EqualFold(strings.TrimSpace(observation.OpenAIEmail), sourceEmail) || observation.OpenAISubject != task.ConnectionSubject || !observation.ConnectedAt.Equal(task.ConnectionAt) || !observation.ObservedAt.Equal(evidence.ObservedAt) || !observation.ProviderCreatedAt.Equal(evidence.ProviderCreatedAt) || observation.ProviderFingerprint != evidence.ProviderFingerprint || !observation.ConnectionUnchanged || !observation.ProviderUnchanged || observation.EnabledEvidence != "unverified" || !kiloObservationInOperation(state, observation.ObservedAt, now) || !kiloObservationInOperation(state, evidence.ObservedAt, now) || task.ConnectionAt.After(now) {
		return kiloOnboardingFail("BYOK_ENABLE_UNVERIFIED", "现存连接观察缺少一致且属于本次操作的只读回执", "保留原操作和连接，核对执行器观察合同", false)
	}
	if state.ConnectionSubject != "" && state.ConnectionSubject != task.ConnectionSubject || !state.ConnectionAt.IsZero() && !state.ConnectionAt.Equal(task.ConnectionAt) {
		return kiloOnboardingFail("IDENTITY_MISMATCH", "原订阅连接在只读核对期间发生改变", "保留原身份并核对连接变更，不能覆盖原归属", false)
	}
	if previous := state.CredentialIdentityEvidence; previous != nil && (previous.ProviderFingerprint != evidence.ProviderFingerprint || !previous.ProviderCreatedAt.Equal(evidence.ProviderCreatedAt)) {
		return kiloOnboardingFail("IDENTITY_MISMATCH", "原身份提供方在只读核对期间发生改变", "保留原身份提供方和回执", false)
	}
	if !task.ExpiresAt.IsZero() && !task.ExpiresAt.After(now) {
		return kiloReconcileSessionFailure("AUTH_SESSION_EXPIRED")
	}
	return nil
}

func (s *KiloService) acceptReconcileSubscription(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime, task *KiloExecutorTask, source *Account) error {
	if err := validateKiloReconcileSource(source, state); err != nil {
		return err
	}
	if err := validateKiloReconcileObservation(job, state, task, kiloSourceEmail(source), time.Now()); err != nil {
		return err
	}
	state.ConnectionSubject, state.ConnectionAt = task.ConnectionSubject, task.ConnectionAt
	state.ConnectionOrganization, state.ConnectionWorkspaceID, state.ConnectionIdentityEvidence = "", "", "credential_snapshot"
	state.IdentityAssociation, state.ConnectionEmail = task.IdentityAssociation, task.ConnectionEmail
	state.CredentialIdentityEvidence, state.ConnectionObservation = task.CredentialIdentityEvidence, task.ConnectionObservation
	state.BYOKEnabled, state.BYOKEnableEvidence = false, ""
	job.SubscriptionConnected, job.CredentialReady, job.RemoteOutcome = false, false, "confirmed"
	if task.SessionHandle != "" {
		state.SessionHandle = task.SessionHandle
	}
	if !task.ExpiresAt.IsZero() {
		state.ExpiresAt = task.ExpiresAt
	}
	completedTask := task.TaskID
	state.AckTaskIDs = append(state.AckTaskIDs, completedTask)
	state.TaskID, state.OperationID, state.Phase, state.DeadlineAt = "", "", "", time.Time{}
	// Keep the lease id in the encrypted receipt until terminal cleanup. Even a
	// failed local release or acknowledgement cannot lose its exact remote owner.
	if err := s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Stage: "subscription_authorization", Status: "failed", Release: true, Code: "BYOK_ENABLE_UNVERIFIED", Message: "只读核对已确认原有订阅连接，尚无 BYOK 启用依据，网关保持不可调度", Action: "保留现有连接和身份，等待可核验的启用合同，禁止重新授权或设备取 Key"}); err != nil {
		return err
	}
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.releaseOnboardingLogin(ackCtx, job, state); err != nil {
		return err
	}
	// The durable acknowledgement list is retried by terminal cleanup.
	_ = s.executor.Ack(ackCtx, completedTask)
	return nil
}

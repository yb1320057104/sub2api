package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/google/uuid"
)

// This receipt is encrypted as one unit. Passwords, TOTP seeds, proxy URLs and
// device tokens are deliberately absent; those belong to their existing owners.
type kiloOnboardingRuntime struct {
	kiloOnboardingState
	Version                    int                                `json:"version"`
	Revision                   int64                              `json:"revision"`
	DeadlineAt                 time.Time                          `json:"deadline_at"`
	ProxyHash                  string                             `json:"proxy_hash"`
	LoginProxyPreflightHash    string                             `json:"login_proxy_preflight_hash,omitempty"`
	RemoteLeaseID              string                             `json:"remote_lease_id"`
	DeviceApproved             bool                               `json:"device_approved"`
	DeviceVerificationURL      string                             `json:"device_verification_url"`
	DeviceUserCode             string                             `json:"device_user_code"`
	SessionRebuilds            int                                `json:"session_rebuilds"`
	DeviceRestarts             int                                `json:"device_restarts"`
	Failures                   int                                `json:"failures"`
	AckTaskIDs                 []string                           `json:"ack_task_ids,omitempty"`
	IdentitySessionMaterial    string                             `json:"identity_session_material,omitempty"`
	IdentityCredential         *KiloExecutorCredential            `json:"identity_credential,omitempty"`
	SourceEmail                string                             `json:"source_email"`
	SourceWorkspace            string                             `json:"source_workspace"`
	SourceSubject              string                             `json:"source_subject,omitempty"`
	SourceIssuer               string                             `json:"source_issuer,omitempty"`
	SourcePlanType             string                             `json:"source_plan_type,omitempty"`
	InitialSourceIdentity      string                             `json:"initial_source_identity"`
	RemoteClosed               bool                               `json:"remote_closed"`
	BYOKEnableEvidence         string                             `json:"byok_enable_evidence"`
	ConnectionSubject          string                             `json:"connection_subject"`
	ConnectionEmail            string                             `json:"connection_email"`
	ConnectionAt               time.Time                          `json:"connection_at"`
	ConnectionOrganization     string                             `json:"connection_organization"`
	ConnectionWorkspaceID      string                             `json:"connection_workspace_id"`
	ConnectionIdentityEvidence string                             `json:"connection_identity_evidence,omitempty"`
	IdentityAssociation        *KiloCredentialIdentityAssociation `json:"identity_association,omitempty"`
	CredentialIdentityEvidence *KiloCredentialIdentityEvidence    `json:"credential_identity_evidence,omitempty"`
	ConnectionObservation      *KiloConnectionObservation         `json:"connection_observation,omitempty"`
	ReconcileSubscription      bool                               `json:"reconcile_subscription,omitempty"`
	ReauthorizeSubscription    bool                               `json:"reauthorize_subscription,omitempty"`
	ReauthorizationHistory     []kiloSubscriptionRecovery         `json:"reauthorization_history,omitempty"`
}

// Recovery records preserve the failed attempt while a new explicit operation runs.
type kiloSubscriptionRecovery struct {
	Revision     int64     `json:"revision"`
	ErrorCode    string    `json:"error_code"`
	Outcome      string    `json:"outcome"`
	AuthorizedAt time.Time `json:"authorized_at"`
}

type kiloOnboardingProgress struct {
	Stage, Status, Code, Message, Action string
	Next                                 time.Time
	Release                              bool
}

type kiloOnboardingFailure struct {
	code, message, action string
	retryable             bool
}

func (e *kiloOnboardingFailure) Error() string { return e.code + ": " + e.message }
func kiloOnboardingFail(code, message, action string, retryable bool) error {
	return &kiloOnboardingFailure{code: code, message: message, action: action, retryable: retryable}
}

func (s *KiloService) readOnboardingState(cipher string) (*kiloOnboardingRuntime, error) {
	state := &kiloOnboardingRuntime{Version: 1}
	if cipher == "" {
		return state, nil
	}
	plain, err := s.encryptor.Decrypt(cipher)
	if err != nil || json.Unmarshal([]byte(plain), state) != nil || state.Version != 1 {
		return nil, kiloOnboardingFail("ONBOARDING_STATE_INVALID", "自动接入恢复记录无效", "检查服务加密密钥并重新提交", false)
	}
	return state, nil
}

func kiloOnboardingModelMapping(models []string) (map[string]string, error) {
	if len(models) == 0 || len(models) > 200 {
		return nil, kiloBad("MODEL_SCOPE_EMPTY", "请提供非空的精确订阅模型范围，不能以空映射放行全部模型")
	}
	mapping := make(map[string]string, len(models))
	for _, model := range models {
		if model != strings.TrimSpace(model) || !strings.HasPrefix(model, "openai/") || len(model) <= len("openai/") || len(model) > 256 || strings.ContainsAny(model, "*?[]\\") || strings.ContainsFunc(model, unicode.IsSpace) || strings.ContainsFunc(model, unicode.IsControl) {
			return nil, kiloBad("MODEL_SCOPE_INVALID", "订阅模型必须是已核实的精确 openai/ 模型编号，不能使用通配符")
		}
		if _, exists := mapping[model]; exists {
			return nil, kiloBad("MODEL_SCOPE_INVALID", "订阅模型编号重复")
		}
		mapping[model] = model
	}
	return mapping, nil
}

func (s *KiloService) checkOnboardingSource(ctx context.Context, job *kiloClaim, source *Account) error {
	if source == nil || job.SourceID == nil || source.ID != *job.SourceID || !source.IsOpenAIOAuth() || source.ParentAccountID != nil || (kiloSourceIdentity(source) != job.SourceIdentity && (job.QuotaOAuthReady || job.Stage != "quota_oauth")) {
		return kiloBad("KILO_SOURCE_CHANGED", "来源账号身份已改变，请重新提交接入任务")
	}
	if job.LoginProxyID == nil || *job.LoginProxyID <= 0 || source.Extra["kilo_quota_only"] != true || int64(ParseExtraInt(source.Extra["kilo_onboarding_job_id"])) != job.ID || int64(ParseExtraInt(source.Extra["kilo_onboarding_proxy_id"])) != *job.LoginProxyID {
		return kiloConflict("来源账号的接入归属或固定登录代理已改变")
	}
	if source.ProxyID != nil && *source.ProxyID != *job.LoginProxyID {
		return kiloBad("LOGIN_PROXY_CONFLICT", "来源账号的固定代理与 Kilo 登录代理不一致")
	}
	if pinned := int64(ParseExtraInt(source.Extra[assignedAuthProxyExtraKey])); pinned > 0 && pinned != *job.LoginProxyID {
		return kiloBad("LOGIN_PROXY_CONFLICT", "来源账号已保存其他固定认证代理")
	}
	if prepared := int64(ParseExtraInt(source.Extra["kilo_source_prepared_id"])); prepared > 0 && prepared != job.PreparedID {
		return kiloConflict("来源账号已绑定其他 Kilo 身份")
	}
	if gateway := int64(ParseExtraInt(source.Extra["kilo_source_gateway_id"])); gateway > 0 && (job.AccountID == nil || gateway != *job.AccountID) {
		return kiloConflict("来源账号已绑定其他 Kilo 网关")
	}
	return ctx.Err()
}

func (s *KiloService) processOnboardingJob(ctx context.Context, job *kiloClaim) error {
	ctx = withKiloClaimLease(ctx, job)
	if job.SourceID == nil || job.LoginProxyID == nil || s.reauth == nil || !s.encryptionReady() {
		return kiloOnboardingFail("LOGIN_MATERIAL_MISSING", "来源登录材料或认证服务不可用", "检查来源账密、TOTP和服务器凭据加密配置", false)
	}
	if err := s.checkJobOwned(ctx, job); err != nil {
		return err
	}
	source, err := s.accounts.GetByID(ctx, *job.SourceID)
	if err != nil {
		return err
	}
	if err = s.checkOnboardingSource(ctx, job, source); err != nil {
		return err
	}
	if job.ReusedIdentity {
		policy, policyErr := kiloReadLifecyclePolicy(ctx, s.db, job.PreparedID)
		if policyErr != nil {
			return policyErr
		}
		if policyErr = kiloRequireLifecycleSource(policy, job.SourceID); policyErr != nil {
			return policyErr
		}
	}
	material, err := s.reauth.ReadKiloLoginMaterial(ctx, source.ID)
	if err != nil || material == nil || strings.TrimSpace(material.Email) == "" || material.Password == "" || material.Version == "" {
		return kiloOnboardingFail("LOGIN_MATERIAL_MISSING", "来源账号缺少可用的账密或 TOTP", "在原账号凭据配置中补充登录材料后重试", false)
	}
	if !strings.EqualFold(strings.TrimSpace(material.Email), kiloSourceEmail(source)) {
		return kiloOnboardingFail("IDENTITY_MISMATCH", "来源账号邮箱与登录材料不一致", "核对来源账号及保存的登录邮箱", false)
	}
	if job.MaterialVersion != "" && job.MaterialVersion != material.Version {
		return kiloOnboardingFail("LOGIN_MATERIAL_CHANGED", "登录材料已轮换，本次旧任务不能继续", "取消并以当前材料重新提交接入任务", false)
	}
	job.MaterialVersion = material.Version
	state, err := s.readOnboardingState(job.RemoteState)
	if err != nil {
		return err
	}
	proxy, err := s.proxies.GetByID(ctx, *job.LoginProxyID)
	if err != nil || !usableAssignedAuthProxy(proxy) {
		return kiloBad("LOGIN_PROXY_CONFLICT", "固定登录代理不可用，禁止更换代理或直连")
	}
	proxyHash := kiloOnboardingProxyHash(proxy.URL())
	if state.ProxyHash != "" && state.ProxyHash != proxyHash {
		return kiloBad("LOGIN_PROXY_CONFLICT", "登录代理凭据在授权期间已改变")
	}
	state.ProxyHash, job.LoginProxyHash = proxyHash, proxyHash
	// Reconciliation never resets into login, even after a stale revision or
	// expired executor session. Its only possible success is a durable observation.
	if state.ReconcileSubscription {
		return s.processReconcileSubscription(ctx, job, state, source, material)
	}
	if state.Revision != 0 && state.Revision != job.Revision {
		stopped, cleanupErr := s.cleanupOnboardingRuntime(ctx, state)
		if cleanupErr != nil || !stopped {
			return kiloOnboardingFail("REMOTE_OUTCOME_UNKNOWN", "正在停止上一执行代次的远端操作", "等待旧执行器任务停止后继续", true)
		}
		previous := state
		state = &kiloOnboardingRuntime{Version: 1, Revision: job.Revision, ProxyHash: previous.ProxyHash, LoginProxyPreflightHash: previous.LoginProxyPreflightHash, SourceEmail: previous.SourceEmail, SourceWorkspace: previous.SourceWorkspace, SourceSubject: previous.SourceSubject, SourceIssuer: previous.SourceIssuer, SourcePlanType: previous.SourcePlanType, InitialSourceIdentity: previous.InitialSourceIdentity, IdentityAssociation: previous.IdentityAssociation, CredentialIdentityEvidence: previous.CredentialIdentityEvidence, ConnectionObservation: previous.ConnectionObservation, ConnectionIdentityEvidence: previous.ConnectionIdentityEvidence, BYOKEnableEvidence: previous.BYOKEnableEvidence, ConnectionSubject: previous.ConnectionSubject, ConnectionEmail: previous.ConnectionEmail, ConnectionAt: previous.ConnectionAt, ConnectionOrganization: previous.ConnectionOrganization, ConnectionWorkspaceID: previous.ConnectionWorkspaceID, IdentitySessionMaterial: previous.IdentitySessionMaterial, IdentityCredential: previous.IdentityCredential, ReauthorizationHistory: previous.ReauthorizationHistory, kiloOnboardingState: kiloOnboardingState{RemoteUserID: previous.RemoteUserID, BYOKEnabled: previous.BYOKEnabled, DeviceSessionID: previous.DeviceSessionID}}
		state.DeviceApproved = previous.DeviceApproved
		if job.PreparedID == 0 {
			if state.DeviceApproved && state.DeviceSessionID != "" {
				job.Stage = "device_authorization"
			} else if !job.QuotaOAuthReady {
				job.Stage = "quota_oauth"
			} else {
				job.Stage, job.SubscriptionConnected = "kilo_login", false
			}
		}
		if err = s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Stage: job.Stage}); err != nil {
			return err
		}
	}
	state.Revision = job.Revision
	if state.InitialSourceIdentity == "" {
		if kiloSourceIdentity(source) != job.SourceIdentity {
			return kiloBad("KILO_SOURCE_CHANGED", "额度授权前的来源快照已改变，不能重新推定身份")
		}
		state.InitialSourceIdentity, state.SourceEmail = job.SourceIdentity, kiloSourceEmail(source)
		state.SourceWorkspace, _ = source.Credentials["chatgpt_account_id"].(string)
	}
	// The idle slot can choose a proxy after import validation. Probe that final
	// route once before a quota task or remote identity can be started.
	if !job.QuotaOAuthReady && job.QuotaTaskID == nil && state.LoginProxyPreflightHash != proxyHash {
		preflight, preflightErr := s.PreflightLoginProxy(ctx, *job.LoginProxyID)
		if preflightErr != nil {
			return preflightErr
		}
		if !preflight.Reachable {
			return kiloBad("KILO_LOGIN_PROXY_UNREACHABLE", "固定登录代理无法连接认证服务，请更换代理后重试；尚未开始远端登录")
		}
		state.LoginProxyPreflightHash = proxyHash
		if err = s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{}); err != nil {
			return err
		}
	}
	if job.Stage == "preflight" {
		if !kiloValidOrganization(job.OrganizationID) {
			return kiloBad("ORGANIZATION_DENIED", "目标组织编号无效")
		}
		if len(job.VerifiedModels) > 0 {
			if _, err = kiloOnboardingModelMapping(job.VerifiedModels); err != nil {
				return err
			}
		}
		if _, err = s.ensureOnboardingExecutor(ctx, state, "login", true); err != nil {
			return err
		}
		if _, err = s.ensureOnboardingIdentity(ctx, job, source); err != nil {
			return err
		}
		return s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Stage: "quota_oauth", Release: true, Next: time.Now()})
	}
	if job.AccountID == nil {
		if !kiloValidOrganization(job.OrganizationID) {
			return kiloBad("ORGANIZATION_DENIED", "目标组织编号无效")
		}
		if len(job.VerifiedModels) > 0 {
			if _, err = kiloOnboardingModelMapping(job.VerifiedModels); err != nil {
				return err
			}
		}
		if _, err = s.ensureOnboardingExecutor(ctx, state, "login", true); err != nil {
			return err
		}
		if _, err = s.ensureOnboardingIdentity(ctx, job, source); err != nil {
			return err
		}
	}
	if job.Stage == "quota_oauth" || !job.QuotaOAuthReady {
		if err = s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Stage: "quota_oauth"}); err != nil {
			return err
		}
		var previous int64
		if job.QuotaTaskID != nil {
			previous = *job.QuotaTaskID
		}
		nextID, ready, quotaErr := s.reauth.EnsureKiloQuotaOAuth(ctx, source.ID, previous)
		if quotaErr != nil {
			return quotaErr
		}
		if nextID > 0 {
			job.QuotaTaskID = &nextID
		}
		nextStage := "quota_oauth"
		if ready {
			if err = s.refreshOnboardingSourceIdentity(ctx, job, state); err != nil {
				return err
			}
			job.QuotaOAuthReady = true
			nextStage = "kilo_login"
		}
		return s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Stage: nextStage, Release: true, Next: time.Now().Add(5 * time.Second)})
	}
	if job.PreparedID > 0 && (!job.ReusedIdentity || job.SubscriptionConnected) {
		return s.verifyOnboardingCredential(ctx, job, state, source)
	}
	if job.ReusedIdentity && state.RemoteUserID == "" {
		if err = s.db.QueryRowContext(ctx, `SELECT remote_user_id FROM pk_kilo_prepared_accounts WHERE id=$1 AND binding_job_id=$2 AND identity_status='active' AND binding_state='binding' AND binding_revision=$3`, job.PreparedID, job.ID, job.BindingRevision).Scan(&state.RemoteUserID); err != nil {
			return err
		}
	}
	if job.ReusedIdentity && job.Stage == "kilo_login" {
		return s.runOnboardingPhase(ctx, job, state, "resume_identity", nil)
	}
	if state.TaskID == "" && state.SessionHandle != "" && !state.ExpiresAt.After(time.Now()) && !(job.Stage == "device_authorization" && state.DeviceApproved) {
		return s.rebuildOnboardingSession(ctx, job, state)
	}
	switch job.Stage {
	case "kilo_login":
		return s.runOnboardingPhase(ctx, job, state, "login", &KiloExecutorCredential{Email: material.Email, Password: material.Password, TOTPSecret: material.TOTPSecret})
	case "subscription_authorization":
		phase := "connect_subscription"
		if state.ReauthorizeSubscription {
			phase = "reauthorize_subscription"
		}
		return s.runOnboardingPhase(ctx, job, state, phase, &KiloExecutorCredential{Email: material.Email, Password: material.Password, TOTPSecret: material.TOTPSecret})
	case "device_authorization":
		return s.processOnboardingDevice(ctx, job, state)
	default:
		return kiloOnboardingFail("ONBOARDING_STAGE_INVALID", "自动接入阶段不能安全恢复", "重新提交任务并保留原错误记录", false)
	}
}

// The inactive account and the job pointer commit together. Repeated preflight
// cannot leak a second gateway, and no fake prepared/key is needed to own egress.
func (s *KiloService) ensureOnboardingIdentity(ctx context.Context, job *kiloClaim, source *Account) (*Account, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = LockKiloOnboardingLease(ctx, tx.Client()); err != nil {
		return nil, err
	}
	txctx := dbent.NewTxContext(ctx, tx)
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return nil, errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return nil, err
	}
	fresh, err := s.accounts.GetByID(txctx, source.ID)
	if err != nil {
		return nil, err
	}
	if err = s.checkOnboardingSource(ctx, job, fresh); err != nil {
		return nil, err
	}
	var accountID *int64
	if err = kiloScan(ctx, tx.Client(), `SELECT account_id FROM pk_kilo_jobs WHERE id=$1 FOR UPDATE`, []any{job.ID}, &accountID); err != nil {
		return nil, err
	}
	var account *Account
	if accountID != nil {
		account, err = s.accounts.GetByID(txctx, *accountID)
		if err != nil {
			return nil, err
		}
		if !kiloOnboardingIdentityMatches(account, job) {
			return nil, kiloConflict("自动接入网关归属或固定登录代理已改变")
		}
	} else {
		name := strings.TrimSpace(job.Name)
		if name == "" {
			name = "Kilo " + kiloSourceEmail(source)
		}
		extra := map[string]any{AccountExtraKiloBridgeEnabled: true, KiloManagedExtraKey: true, "kilo_gateway_profile": "cli", KiloLoginProxyExtraKey: *job.LoginProxyID, "kilo_onboarding_source_id": source.ID, "kilo_onboarding_job_id": job.ID}
		input := &CreateAccountInput{Name: name, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: job.Options.AccountConcurrency, Priority: job.Options.Priority, SkipDefaultGroupBind: true, Credentials: map[string]any{"base_url": KiloGatewayBaseURL, "kilo_machine_id": uuid.NewString(), "kilo_organization_id": job.OrganizationID}, Extra: extra}
		account, err = buildAccountForCreate(input, extra)
		if err != nil {
			return nil, err
		}
		account.Status, account.Schedulable = StatusDisabled, false
		if err = s.accounts.Create(txctx, account); err != nil {
			return nil, err
		}
		if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_jobs SET account_id=$2,material_version=$3,updated_at=NOW() WHERE id=$1`, job.ID, account.ID, job.MaterialVersion); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	job.AccountID = &account.ID
	if s.egress == nil {
		return nil, ErrKiloEgressUnavailable
	}
	if _, err = s.egress.Verify(ctx, account.ID, *job.LoginProxyID); err != nil {
		return nil, err
	}
	return account, nil
}

// The existing quota flow can fill a previously absent Codex workspace id.
// Only its completed receipt allows that narrow snapshot refresh; material,
// email, a previously selected workspace and the job reservation must match.
func (s *KiloService) refreshOnboardingSourceIdentity(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = LockKiloOnboardingLease(ctx, tx.Client()); err != nil {
		return err
	}
	var officialBusy bool
	if err = kiloScan(ctx, tx.Client(), `SELECT EXISTS(SELECT 1 FROM openai_oauth_reauth_tasks WHERE account_id=$1 AND status IN ('queued','running','callback_processing'))`, []any{*job.SourceID}, &officialBusy); err != nil {
		return err
	}
	if officialBusy {
		return kiloOnboardingFail("QUOTA_OAUTH_BUSY", "来源账号仍有官方认证任务，等待其结束后再进入 Kilo 登录", "等待原认证任务完成", true)
	}
	txctx := dbent.NewTxContext(ctx, tx)
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return err
	}
	source, err := s.accounts.GetByID(txctx, *job.SourceID)
	if err != nil {
		return err
	}
	if err = s.checkOnboardingSource(ctx, job, source); err != nil {
		return err
	}
	material, err := s.reauth.ReadKiloLoginMaterial(txctx, source.ID)
	workspace, _ := source.Credentials["chatgpt_account_id"].(string)
	if err != nil || material == nil || material.Version != job.MaterialVersion || !strings.EqualFold(strings.TrimSpace(material.Email), state.SourceEmail) || !strings.EqualFold(kiloSourceEmail(source), state.SourceEmail) || (state.SourceWorkspace != "" && workspace != state.SourceWorkspace) || state.InitialSourceIdentity != job.SourceIdentity {
		return kiloBad("KILO_SOURCE_CHANGED", "额度授权期间来源邮箱、材料版本或预期工作区已改变")
	}
	identity := kiloSourceIdentity(source)
	state.InitialSourceIdentity, state.SourceWorkspace = identity, workspace
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	cipher, err := s.encryptor.Encrypt(string(raw))
	if err != nil {
		return err
	}
	result, err := tx.Client().ExecContext(ctx, `UPDATE pk_kilo_jobs SET source_identity=$2,quota_oauth_ready=true,stage='kilo_login',remote_state_encrypted=$5,updated_at=NOW() WHERE id=$1 AND source_identity=$3 AND material_version=$4`, job.ID, identity, job.SourceIdentity, job.MaterialVersion, cipher)
	if err != nil {
		return err
	}
	if n, rowsErr := result.RowsAffected(); rowsErr != nil {
		return rowsErr
	} else if n != 1 {
		return kiloConflict("额度授权来源快照已由其他执行者改变")
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	job.SourceIdentity, job.RemoteState = identity, cipher
	return nil
}

func kiloOnboardingProxyHash(proxyURL string) string {
	hash := sha256.Sum256([]byte(proxyURL))
	return hex.EncodeToString(hash[:])
}

func kiloOnboardingIdentityMatches(account *Account, job *kiloClaim) bool {
	return account != nil && !account.Schedulable && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey && account.ParentAccountID == nil && account.Extra[KiloManagedExtraKey] == true && account.Extra[AccountExtraKiloBridgeEnabled] == true && account.Extra["kilo_gateway_profile"] == "cli" && job.SourceID != nil && int64(ParseExtraInt(account.Extra["kilo_onboarding_source_id"])) == *job.SourceID && int64(ParseExtraInt(account.Extra["kilo_onboarding_job_id"])) == job.ID && job.LoginProxyID != nil && int64(ParseExtraInt(account.Extra[KiloLoginProxyExtraKey])) == *job.LoginProxyID
}

func (s *KiloService) persistOnboarding(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime, progress kiloOnboardingProgress) error {
	state.Version, state.Revision = 1, job.Revision
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	cipher, err := s.encryptor.Encrypt(string(raw))
	if err != nil {
		return errors.New("自动接入恢复记录加密失败")
	}
	if progress.Stage == "" {
		progress.Stage = job.Stage
	}
	if progress.Status == "" {
		progress.Status = "running"
		if progress.Release {
			progress.Status = "awaiting_authorization"
		}
	}
	if progress.Next.IsZero() {
		progress.Next = time.Now().Add(5 * time.Second)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = LockKiloOnboardingLease(withKiloClaimLease(ctx, job), tx); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE pk_kilo_jobs SET stage=$4,status=$5,remote_state_encrypted=$6,remote_outcome=$7,material_version=$8,quota_task_id=$9,credential_ready=$10,subscription_connected=$11,quota_oauth_ready=$12,error_code=$13,last_error=$14,next_action=$15,failed_stage=CASE WHEN $13='' THEN '' ELSE $4 END,next_attempt_at=$16,lease_token=CASE WHEN $17 THEN NULL ELSE lease_token END,lease_until=CASE WHEN $17 THEN NULL ELSE lease_until END,verification_url=$18,user_code=$19,finished_at=CASE WHEN $5='failed' THEN NOW() ELSE NULL END,updated_at=NOW() WHERE id=$1 AND revision=$2 AND lease_token=$3 AND status='running' AND lease_until>NOW()`, job.ID, job.Revision, job.Lease, progress.Stage, progress.Status, cipher, job.RemoteOutcome, job.MaterialVersion, job.QuotaTaskID, job.CredentialReady, job.SubscriptionConnected, job.QuotaOAuthReady, progress.Code, progress.Message, progress.Action, progress.Next, progress.Release, state.DeviceVerificationURL, state.DeviceUserCode)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return kiloConflict("任务已取消、执行代次或租约已失效")
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	job.RemoteState, job.Stage = cipher, progress.Stage
	return nil
}

func (s *KiloService) ensureOnboardingExecutor(ctx context.Context, state *kiloOnboardingRuntime, phase string, capacity bool) (*KiloExecutorHealth, error) {
	if s.executor == nil {
		return nil, kiloOnboardingFail("EXECUTOR_UNAVAILABLE", "未配置自动接入执行器", "配置受认证的 Kilo 执行器后等待重试", true)
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	health, err := s.executor.Health(call)
	if err != nil {
		return nil, kiloOnboardingFail("EXECUTOR_UNAVAILABLE", "自动接入执行器暂不可用", "检查执行器服务后等待重试", true)
	}
	if health == nil || !health.OK || health.ProtocolVersion != 1 || strings.TrimSpace(health.InstanceID) == "" {
		return nil, kiloOnboardingFail("EXECUTOR_UNAVAILABLE", "执行器协议版本或身份无效", "升级并核对执行器协议版本", false)
	}
	capabilities := map[string]bool{}
	for _, item := range health.Capabilities {
		capabilities[item] = true
	}
	requiredCapabilities := []string{"login", "connect_subscription", "approve_device", "fixed_proxy", "cancel", "ack", "session_ttl", "workspace_identity"}
	if phase == "reconcile_subscription" || phase == "reauthorize_subscription" {
		requiredCapabilities = append(requiredCapabilities, phase)
	}
	for _, required := range requiredCapabilities {
		if !capabilities[required] {
			return nil, kiloOnboardingFail("EXECUTOR_UNAVAILABLE", "执行器尚未具备完整登录、订阅、设备确认与恢复能力", "检查执行器 health 能力声明", false)
		}
	}
	if state.InstanceID != "" && state.InstanceID != health.InstanceID {
		return nil, kiloOnboardingFail("SESSION_LOST", "原执行器实例已改变，旧会话不能继续", "在同一固定代理重建官方会话", true)
	}
	if capacity && health.AvailableCapacity < 1 {
		return nil, kiloOnboardingFail("EXECUTOR_BUSY", "执行器无空闲容量，任务继续在 Go 队列等待", "等待执行器释放容量", true)
	}
	return health, nil
}

func kiloExecutorPhaseStage(phase string) string {
	switch phase {
	case "login", "resume_identity":
		return "kilo_login"
	case "connect_subscription", "reconcile_subscription", "reauthorize_subscription":
		return "subscription_authorization"
	case "approve_device":
		return "device_authorization"
	default:
		return "preflight"
	}
}

func validateKiloExecutorTask(job *kiloClaim, state *kiloOnboardingRuntime, task *KiloExecutorTask) error {
	if task == nil || task.TaskID == "" || task.JobID != job.ID || task.Revision != job.Revision || task.Phase != state.Phase || task.InstanceID == "" || task.InstanceID != state.InstanceID || (state.TaskID != "" && task.TaskID != state.TaskID) {
		return kiloOnboardingFail("STALE_EXECUTION", "执行器回报不属于当前任务、阶段或执行代次", "检查执行器结果隔离和协议实现", false)
	}
	return nil
}

// Executor phases start only after quota OAuth confirms the source workspace.
// Kilo's organization is a separate identity and cannot supply this workspace.
func setKiloExecutorExpectedIdentity(input *KiloExecutorTaskInput, source *Account, state *kiloOnboardingRuntime) error {
	workspace, _ := source.Credentials["chatgpt_account_id"].(string)
	if strings.TrimSpace(workspace) == "" {
		return kiloBad("KILO_SOURCE_CHANGED", "来源额度授权缺少已确认的工作区，请重新完成额度授权")
	}
	if state.SourceWorkspace != "" && workspace != state.SourceWorkspace {
		return kiloBad("KILO_SOURCE_CHANGED", "来源工作区与额度授权确认的工作区不一致")
	}
	plan, _ := source.Credentials["plan_type"].(string)
	if state.SourcePlanType != "" && state.SourcePlanType != plan {
		return kiloBad("KILO_SOURCE_CHANGED", "来源套餐与原接入身份快照不一致")
	}
	input.ExpectedIdentity.SourceEmail = kiloSourceEmail(source)
	input.ExpectedIdentity.SourceWorkspaceID = workspace
	input.ExpectedIdentity.SourcePlanType = plan
	if input.Phase == "connect_subscription" || input.Phase == "reconcile_subscription" || input.Phase == "reauthorize_subscription" {
		input.SourceIDToken = source.GetCredential("id_token")
	}
	state.SourceWorkspace, state.SourcePlanType = workspace, plan
	previousSubject, previousIssuer := state.SourceSubject, state.SourceIssuer
	state.SourceSubject, state.SourceIssuer = "", ""
	// These claims came through the existing successful official OAuth exchange.
	// Subject evidence is separate from a selected workspace and never fabricates one.
	claims, claimErr := openai.ParseIDToken(source.GetCredential("id_token"))
	// A persisted subject anchors a historical snapshot, not a new login.
	// The executor verifies its signature and separately authenticates fresh proof.
	if claimErr != nil && previousSubject != "" && previousIssuer == "https://auth.openai.com" {
		claims, claimErr = openai.DecodeIDToken(source.GetCredential("id_token"))
	}
	if claimErr == nil && claims.Iss == "https://auth.openai.com" && strings.EqualFold(claims.Email, kiloSourceEmail(source)) {
		for _, audience := range claims.Aud {
			if audience == openai.ClientID && claims.Sub != "" {
				state.SourceSubject, state.SourceIssuer = claims.Sub, claims.Iss
				input.ExpectedIdentity.SourceSubject, input.ExpectedIdentity.SourceIssuer = claims.Sub, claims.Iss
				break
			}
		}
	}
	if previousSubject != "" && state.SourceSubject != previousSubject || previousIssuer != "" && state.SourceIssuer != previousIssuer {
		return kiloBad("KILO_SOURCE_CHANGED", "来源签发方或原始主体与原接入身份快照不一致")
	}
	return nil
}

// The encrypted receipt retains the original association timestamp. Freshness is
// checked when accepting the executor task, not reinvented when activating later.
func kiloCredentialAssociationMatches(state *kiloOnboardingRuntime, association *KiloCredentialIdentityAssociation, subject string) bool {
	return association != nil && association.Version == 1 && association.Method == "credential_snapshot" &&
		state.SourceIssuer == "https://auth.openai.com" && association.SourceIssuer == state.SourceIssuer &&
		strings.TrimSpace(state.SourceSubject) != "" && association.SourceSubject == state.SourceSubject &&
		strings.TrimSpace(state.SourceWorkspace) != "" && association.SourceWorkspaceID == state.SourceWorkspace &&
		strings.TrimSpace(state.SourcePlanType) != "" && association.SourcePlanType == state.SourcePlanType &&
		strings.TrimSpace(state.RemoteUserID) != "" && association.KiloUserID == state.RemoteUserID &&
		strings.TrimSpace(association.KiloClientID) != "" && strings.TrimSpace(subject) != "" &&
		association.KiloSubject == subject && !association.VerifiedAt.IsZero()
}

func kiloSubscriptionIdentityMatches(state *kiloOnboardingRuntime, workspace, subject, evidence string) bool {
	if evidence == "credential_snapshot" {
		return workspace == "" && (state.SourcePlanType == "plus" || state.SourcePlanType == "pro") && kiloCredentialAssociationMatches(state, state.IdentityAssociation, subject)
	}
	if evidence == "personal_subject" {
		return workspace == "" && (state.SourcePlanType == "plus" || state.SourcePlanType == "pro") &&
			state.SourceIssuer == "https://auth.openai.com" && state.SourceSubject != "" && subject == state.SourceSubject
	}
	return (evidence == "" || evidence == "accepted_workspace_selection") && state.SourceWorkspace != "" && workspace == state.SourceWorkspace
}

func (s *KiloService) runOnboardingPhase(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime, phase string, credential *KiloExecutorCredential) error {
	reconcile := phase == "reconcile_subscription"
	reauthorize := phase == "reauthorize_subscription"
	if state.ReauthorizeSubscription != reauthorize {
		return kiloOnboardingFail("ONBOARDING_STAGE_INVALID", "重新授权意图与执行阶段不一致", "核对原任务的显式重新授权操作", false)
	}
	if state.ReconcileSubscription != reconcile {
		return kiloOnboardingFail("ONBOARDING_STAGE_INVALID", "只读订阅观察不能切换到登录、授权或设备阶段", "保留原任务并核对观察意图", false)
	}
	if phase != "login" && phase != "resume_identity" && !reconcile && !reauthorize && state.SessionHandle == "" {
		return s.rebuildOnboardingSession(ctx, job, state)
	}
	health, err := s.ensureOnboardingExecutor(ctx, state, phase, state.OperationID == "")
	if err != nil {
		if typed := new(kiloOnboardingFailure); errors.As(err, &typed) && (typed.code == "SESSION_LOST" || typed.code == "AUTH_SESSION_EXPIRED") {
			if reconcile {
				return kiloReconcileSessionFailure(typed.code)
			}
			return s.rebuildOnboardingSession(ctx, job, state)
		}
		return err
	}
	if state.OperationID == "" {
		state.OperationID, state.Phase, state.DeadlineAt = uuid.NewString(), phase, time.Now().Add(kiloOnboardingPhaseLifetime)
		state.RemoteLeaseID = state.OperationID
		state.InstanceID, state.RemoteClosed = health.InstanceID, false
		job.RemoteOutcome = "pending"
		if err = s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Stage: kiloExecutorPhaseStage(phase)}); err != nil {
			return err
		}
	}
	if state.Phase != phase {
		return kiloOnboardingFail("ONBOARDING_STAGE_INVALID", "恢复记录阶段与当前任务不一致", "停止旧任务后重新授权", false)
	}
	if !state.DeadlineAt.After(time.Now()) {
		if reconcile {
			return kiloReconcileSessionFailure("AUTH_SESSION_EXPIRED")
		}
		return s.rebuildOnboardingSession(ctx, job, state)
	}
	var task *KiloExecutorTask
	call, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if state.TaskID == "" {
		source, readErr := s.accounts.GetByID(ctx, *job.SourceID)
		if readErr != nil {
			return readErr
		}
		if err = s.checkOnboardingSource(ctx, job, source); err != nil {
			return err
		}
		input := KiloExecutorTaskInput{ProtocolVersion: 1, JobID: job.ID, Revision: job.Revision, OperationID: state.OperationID, Phase: phase, DeadlineAt: state.DeadlineAt, Credential: credential, SessionHandle: state.SessionHandle, OrganizationID: job.OrganizationID}
		if reconcile || reauthorize {
			identity, identityErr := kiloReconcileIdentity(state)
			if identityErr != nil {
				return identityErr
			}
			input.KiloIdentity, input.SessionHandle = identity, ""
		}
		if phase == "resume_identity" {
			key, _, keyErr := s.preparedCredential(ctx, job.PreparedID)
			if keyErr != nil {
				return keyErr
			}
			var identityEmail string
			if keyErr = s.db.QueryRowContext(ctx, `SELECT email FROM pk_kilo_prepared_accounts WHERE id=$1 AND binding_job_id=$2`, job.PreparedID, job.ID).Scan(&identityEmail); keyErr != nil {
				return keyErr
			}
			identity, identityErr := s.kiloIdentityMaterial(ctx, job.PreparedID, key, state.RemoteUserID, identityEmail)
			if identityErr != nil {
				return identityErr
			}
			// Prefer the same identity session already verified by this job.
			if state.IdentitySessionMaterial != "" {
				identity.SessionMaterial = state.IdentitySessionMaterial
			}
			input.KiloIdentity = identity
		}
		if err = setKiloExecutorExpectedIdentity(&input, source, state); err != nil {
			return err
		}
		proxyURL, leaseErr := s.reserveOnboardingLogin(ctx, job, state)
		if leaseErr != nil {
			return leaseErr
		}
		if err = s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{}); err != nil {
			return err
		}
		input.LoginProxy = KiloExecutorProxy{ID: *job.LoginProxyID, URL: proxyURL}
		if phase == "approve_device" {
			input.Device = &KiloExecutorDevice{VerificationURL: state.DeviceVerificationURL, UserCode: state.DeviceUserCode}
		}
		task, err = s.executor.Submit(call, input)
	} else {
		task, err = s.executor.Task(call, state.TaskID)
	}
	if err != nil {
		code, retryable := kiloExecutorErrorInfo(err)
		if code == "SESSION_LOST" || code == "AUTH_SESSION_EXPIRED" {
			if reconcile {
				return kiloReconcileSessionFailure(code)
			}
			return s.rebuildOnboardingSession(ctx, job, state)
		}
		job.RemoteOutcome = "unknown"
		return kiloOnboardingFail(code, "执行器结果暂未确认，将按原操作编号恢复", "等待原操作结果，勿重复提交新登录", retryable)
	}
	if err = validateKiloExecutorTask(job, state, task); err != nil {
		return err
	}
	state.TaskID = task.TaskID
	state.Failures = 0
	if task.Status == "failed" || task.Status == "cancelled" || task.Status == "needs_action" {
		job.RemoteOutcome = task.Status
	}
	if err = s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{}); err != nil {
		return err
	}
	switch task.Status {
	case "pending", "queued", "running":
		return s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Release: true})
	case "needs_action":
		if reconcile {
			return kiloOnboardingFail("ACTION_REQUIRED", "只读观察未能在原身份材料内完成，未重新授权订阅", "保留原任务与身份材料，核对执行器观察结果", false)
		}
		if task.ErrorCode == "BYOK_AUTHORIZATION_REJECTED" {
			return s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Status: "failed", Release: true, Code: task.ErrorCode, Message: "Kilo官方未接受本次GPT订阅授权，原Kilo账号保持", Action: "核对本次官方订阅授权拒绝原因后手动重试"})
		}
		return s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Release: true, Code: "ACTION_REQUIRED", Message: "官方授权需要额外交互", Action: "在执行器中处理当前官方授权步骤", Next: time.Now().Add(time.Minute)})
	case "failed", "cancelled":
		if task.ErrorCode == "SESSION_LOST" || task.ErrorCode == "AUTH_SESSION_EXPIRED" {
			if reconcile {
				return kiloReconcileSessionFailure(task.ErrorCode)
			}
			return s.rebuildOnboardingSession(ctx, job, state)
		}
		_ = s.releaseOnboardingLogin(ctx, job, state)
		code := task.ErrorCode
		switch code {
		case "LOGIN_REJECTED", "MFA_REJECTED", "MFA_VERIFY_TRANSIENT", "ACTION_REQUIRED", "IDENTITY_MISMATCH", "ORGANIZATION_DENIED", "BYOK_NOT_CONNECTED", "BYOK_ENABLE_UNVERIFIED", "REMOTE_OUTCOME_UNKNOWN", "KILO_IDENTITY_FORBIDDEN":
		default:
			code = "EXECUTOR_FAILED"
		}
		// The executor owns the bounded MFA/verify retry budget. Starting a new
		// login here would multiply that budget or replay an unknown mutation.
		return kiloOnboardingFail(code, "执行器未完成本阶段授权", "查看分类错误并处理后手动重试", false)
	case "succeeded":
		return s.acceptOnboardingTask(ctx, job, state, task)
	default:
		return kiloOnboardingFail("EXECUTOR_PROTOCOL_INVALID", "执行器返回了未知任务状态", "检查执行器协议实现", false)
	}
}

func validateKiloSubscriptionEvidence(job *kiloClaim, state *kiloOnboardingRuntime, task *KiloExecutorTask, sourceEmail string, now time.Time) error {
	if task == nil {
		return kiloOnboardingFail("BYOK_ENABLE_UNVERIFIED", "缺少本次订阅授权回执", "保留原任务并核对执行器回执", false)
	}
	view := *state
	view.IdentityAssociation = task.IdentityAssociation
	if state.ReconcileSubscription || !task.BYOKEnabled || !strings.EqualFold(strings.TrimSpace(task.ConnectionEmail), sourceEmail) || task.OrganizationID != job.OrganizationID || !kiloSubscriptionIdentityMatches(&view, task.ConnectionWorkspaceID, task.ConnectionSubject, task.ConnectionIdentityEvidence) || task.BYOKEnableEvidence != "fresh_delegated_authorization" || strings.TrimSpace(task.ConnectionSubject) == "" || !kiloOnboardingEvidenceInPhase(state, task.ConnectionAt, now) {
		return kiloOnboardingFail("BYOK_ENABLE_UNVERIFIED", "目标空间订阅连接、启用依据或授权时间不符合本次操作", "在正确空间重新完成当前 ChatGPT 订阅授权", false)
	}
	if task.ConnectionIdentityEvidence == "credential_snapshot" && (task.VerifiedIdentity.KiloUserID != state.RemoteUserID || !kiloOnboardingEvidenceInPhase(state, task.IdentityAssociation.VerifiedAt, now)) {
		return kiloOnboardingFail("BYOK_ENABLE_UNVERIFIED", "目标空间订阅连接、启用依据或授权时间不符合本次操作", "在正确空间重新完成当前 ChatGPT 订阅授权", false)
	}
	return nil
}

const kiloOnboardingPhaseLifetime = 15 * time.Minute

func kiloOnboardingEvidenceInPhase(state *kiloOnboardingRuntime, at, now time.Time) bool {
	// Every new phase persists this fixed deadline before submission. Retaining
	// that lower bound prevents a previous phase's verification being replayed.
	return !state.DeadlineAt.IsZero() && !at.IsZero() && !at.Before(state.DeadlineAt.Add(-kiloOnboardingPhaseLifetime)) && !at.After(state.DeadlineAt) && !at.After(now)
}

func validateKiloExistingConnectionObservation(job *kiloClaim, state *kiloOnboardingRuntime, task *KiloExecutorTask, sourceEmail string, now time.Time) error {
	if task == nil {
		return kiloOnboardingFail("BYOK_ENABLE_UNVERIFIED", "缺少现存订阅连接观察", "保留原身份并核对执行器回执", false)
	}
	view := *state
	view.IdentityAssociation = task.IdentityAssociation
	if state.ReconcileSubscription || task.Phase != "connect_subscription" || task.BYOKEnabled || task.BYOKEnableEvidence != "existing_connection_enablement_unknown" || task.OrganizationID != job.OrganizationID || state.RemoteUserID == "" || task.VerifiedIdentity.KiloUserID != state.RemoteUserID || strings.TrimSpace(sourceEmail) == "" || !strings.EqualFold(strings.TrimSpace(task.ConnectionEmail), sourceEmail) || task.ConnectionWorkspaceID != "" || (task.ConnectionIdentityEvidence != "credential_snapshot" && task.ConnectionIdentityEvidence != "personal_subject") || !kiloSubscriptionIdentityMatches(&view, task.ConnectionWorkspaceID, task.ConnectionSubject, task.ConnectionIdentityEvidence) || task.ConnectionAt.IsZero() || task.ConnectionAt.After(now) {
		return kiloOnboardingFail("IDENTITY_MISMATCH", "现存订阅连接的来源、空间或身份归属未通过核对", "保留原身份并核对原始连接回执，禁止自动重新授权", false)
	}
	if task.ConnectionIdentityEvidence == "credential_snapshot" && !kiloOnboardingEvidenceInPhase(state, task.IdentityAssociation.VerifiedAt, now) {
		return kiloOnboardingFail("IDENTITY_MISMATCH", "现存连接的凭据关联不属于本次身份核对", "保留原身份并核对原始回执，禁止自动重新授权", false)
	}
	return nil
}

func validateKiloSubscriptionReceipt(job *kiloClaim, state *kiloOnboardingRuntime) error {
	if state.ReconcileSubscription || !state.BYOKEnabled || state.BYOKEnableEvidence != "fresh_delegated_authorization" || state.ConnectionSubject == "" || state.ConnectionAt.IsZero() || state.ConnectionOrganization != job.OrganizationID || !kiloSubscriptionIdentityMatches(state, state.ConnectionWorkspaceID, state.ConnectionSubject, state.ConnectionIdentityEvidence) {
		return kiloBad("BYOK_ENABLE_UNVERIFIED", "缺少目标空间及来源工作区的订阅启用依据，网关保持不可调度")
	}
	return nil
}

func (s *KiloService) acceptOnboardingTask(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime, task *KiloExecutorTask) error {
	if err := validateKiloExecutorTask(job, state, task); err != nil {
		return err
	}
	source, err := s.accounts.GetByID(ctx, *job.SourceID)
	if err != nil {
		return err
	}
	if err = s.checkOnboardingSource(ctx, job, source); err != nil {
		return err
	}
	if task.Phase == "reconcile_subscription" || state.ReconcileSubscription {
		return s.acceptReconcileSubscription(ctx, job, state, task, source)
	}
	if task.ConnectionIdentityEvidence == "credential_snapshot" && state.RemoteUserID == "" {
		return kiloOnboardingFail("IDENTITY_MISMATCH", "凭据快照缺少原 Kilo 身份归属", "保留原身份并核对原始回执", false)
	}
	if task.VerifiedIdentity.KiloUserID == "" || (task.Phase != "resume_identity" && !strings.EqualFold(strings.TrimSpace(task.VerifiedIdentity.OpenAIEmail), kiloSourceEmail(source))) || (state.RemoteUserID != "" && state.RemoteUserID != task.VerifiedIdentity.KiloUserID) {
		return kiloOnboardingFail("IDENTITY_MISMATCH", "执行器确认的 Kilo 或 ChatGPT 身份与本任务不一致", "核对来源账号和官方会话，禁止绑定其他身份", false)
	}
	if task.Phase == "connect_subscription" || task.Phase == "reauthorize_subscription" {
		var expected KiloExecutorTaskInput
		if err = setKiloExecutorExpectedIdentity(&expected, source, state); err != nil {
			return err
		}
	}
	existingObservation := task.Phase == "connect_subscription" && task.BYOKEnableEvidence == "existing_connection_enablement_unknown"
	if existingObservation {
		if err = validateKiloExistingConnectionObservation(job, state, task, kiloSourceEmail(source), time.Now()); err != nil {
			return err
		}
	} else {
		if task.SessionHandle != "" {
			state.SessionHandle = task.SessionHandle
		}
		if !task.ExpiresAt.IsZero() {
			state.ExpiresAt = task.ExpiresAt
		}
		if state.SessionHandle == "" || !state.ExpiresAt.After(time.Now()) {
			return s.rebuildOnboardingSession(ctx, job, state)
		}
		state.RemoteUserID = task.VerifiedIdentity.KiloUserID
		if task.IdentitySessionMaterial != "" {
			if len(task.IdentitySessionMaterial) > 64*1024 {
				return kiloRemoteShapeError()
			}
			state.IdentitySessionMaterial = task.IdentitySessionMaterial
		}
		if task.Phase == "login" && !job.ReusedIdentity {
			material, materialErr := s.reauth.ReadKiloLoginMaterial(ctx, *job.SourceID)
			if materialErr != nil {
				return materialErr
			}
			state.IdentityCredential = &KiloExecutorCredential{Email: material.Email, Password: material.Password, TOTPSecret: material.TOTPSecret}
		}
	}
	next := "subscription_authorization"
	progress := kiloOnboardingProgress{Release: true}
	switch task.Phase {
	case "login", "resume_identity":
	case "connect_subscription", "reauthorize_subscription":
		if existingObservation {
			// A known identity is not proof of enablement. Keep the original
			// identity material and stop without rebuilding or reauthorizing it.
			state.BYOKEnabled, job.SubscriptionConnected = false, false
			progress.Status, progress.Code = "failed", "BYOK_ENABLE_UNVERIFIED"
			progress.Message = "现存 ChatGPT 连接身份已核对，但缺少订阅启用权威依据，网关保持不可调度"
			progress.Action = "核对官方启用状态后人工处理；不会自动重新授权"
		} else {
			if err = validateKiloSubscriptionEvidence(job, state, task, kiloSourceEmail(source), time.Now()); err != nil {
				return err
			}
			state.BYOKEnabled, next = true, "device_authorization"
			state.ReauthorizeSubscription = false
			if job.ReusedIdentity {
				job.SubscriptionConnected = true
				job.CredentialReady = true
				next = "credential_verified"
			}
		}
		state.BYOKEnableEvidence, state.ConnectionSubject, state.ConnectionAt, state.ConnectionOrganization, state.ConnectionWorkspaceID = task.BYOKEnableEvidence, task.ConnectionSubject, task.ConnectionAt, task.OrganizationID, task.ConnectionWorkspaceID
		state.ConnectionEmail, state.ConnectionIdentityEvidence = task.ConnectionEmail, task.ConnectionIdentityEvidence
		state.IdentityAssociation = task.IdentityAssociation
		state.CredentialIdentityEvidence = task.CredentialIdentityEvidence
		state.ConnectionObservation = task.ConnectionObservation
	case "approve_device":
		state.DeviceApproved, next = true, "device_authorization"
	default:
		return kiloOnboardingFail("EXECUTOR_PROTOCOL_INVALID", "执行器阶段无效", "检查执行器协议实现", false)
	}
	if err = s.releaseOnboardingLogin(ctx, job, state); err != nil {
		return err
	}
	completedTask := state.TaskID
	state.AckTaskIDs = append(state.AckTaskIDs, completedTask)
	state.TaskID, state.OperationID, state.Phase = "", "", ""
	state.RemoteLeaseID = ""
	state.DeadlineAt = time.Time{}
	job.RemoteOutcome = "confirmed"
	progress.Stage = next
	if err = s.persistOnboarding(ctx, job, state, progress); err != nil {
		return err
	}
	// Ack follows the durable receipt. A failed ack is retried by terminal
	// cleanup; it cannot erase an uncommitted session/result on this side.
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.executor.Ack(ackCtx, completedTask)
	return nil
}

func (s *KiloService) rebuildOnboardingSession(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime) error {
	if state.ReauthorizeSubscription {
		return kiloOnboardingFail("SESSION_LOST", "本次重新授权会话已失效，未自动重放", "核对本次操作结果后再显式处理", false)
	}
	if state.ReconcileSubscription {
		return kiloReconcileSessionFailure("SESSION_LOST")
	}
	if state.SessionRebuilds >= 1 {
		return kiloOnboardingFail("SESSION_LOST", "官方会话再次丢失，自动重建预算已用完", "检查执行器稳定性后手动重新授权", false)
	}
	stopped, err := s.cleanupOnboardingRuntime(ctx, state)
	if err != nil || !stopped {
		return kiloOnboardingFail("REMOTE_OUTCOME_UNKNOWN", "旧授权操作尚未确认停止", "等待旧任务停止，保留固定代理占用", true)
	}
	userID, rebuilds, restarts := state.RemoteUserID, state.SessionRebuilds+1, state.DeviceRestarts
	sourceEmail, sourceWorkspace, sourceIdentity := state.SourceEmail, state.SourceWorkspace, state.InitialSourceIdentity
	if state.DeviceSessionID != "" {
		if err = s.discardOnboardingDevice(ctx, job, state.DeviceSessionID); err != nil {
			return err
		}
	}
	*state = kiloOnboardingRuntime{Version: 1, Revision: job.Revision, ProxyHash: state.ProxyHash, LoginProxyPreflightHash: state.LoginProxyPreflightHash, SourceEmail: sourceEmail, SourceWorkspace: sourceWorkspace, InitialSourceIdentity: sourceIdentity, IdentitySessionMaterial: state.IdentitySessionMaterial, IdentityCredential: state.IdentityCredential, ReauthorizationHistory: state.ReauthorizationHistory, SessionRebuilds: rebuilds, DeviceRestarts: restarts, kiloOnboardingState: kiloOnboardingState{RemoteUserID: userID}}
	job.SubscriptionConnected = false
	job.RemoteOutcome = ""
	return s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Stage: "kilo_login", Release: true, Code: "SESSION_LOST", Message: "原会话不可恢复，按原固定代理重建一次官方会话", Action: "等待会话重建", Next: time.Now().Add(5 * time.Second)})
}

func (s *KiloService) processOnboardingDevice(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime) error {
	if state.DeviceSessionID == "" {
		// Recover the insert-before-receipt crash window using the gateway that
		// belongs exclusively to this job, rather than creating another device.
		var id string
		err := s.db.QueryRowContext(ctx, `SELECT id::text FROM pk_kilo_device_sessions WHERE account_id=$1 AND status IN ('pending','approved','saved') ORDER BY created_at DESC LIMIT 1`, *job.AccountID).Scan(&id)
		if err == nil {
			state.DeviceSessionID = id
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		} else {
			device, issueErr := s.startDeviceForAccount(ctx, job.Name, *job.AccountID, *job.LoginProxyID)
			if issueErr != nil {
				return issueErr
			}
			state.DeviceSessionID = device.ID
		}
		var status string
		var expires time.Time
		if err = s.db.QueryRowContext(ctx, `SELECT status,verification_url,user_code,expires_at FROM pk_kilo_device_sessions WHERE id=$1 AND account_id=$2`, state.DeviceSessionID, *job.AccountID).Scan(&status, &state.DeviceVerificationURL, &state.DeviceUserCode, &expires); err != nil {
			return err
		}
		if !expires.After(time.Now()) && status == "pending" {
			return s.restartOnboardingDevice(ctx, job, state)
		}
		if err = s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{}); err != nil {
			return err
		}
	}
	if !state.DeviceApproved {
		return s.runOnboardingPhase(ctx, job, state, "approve_device", nil)
	}
	device, err := s.pollDevice(ctx, state.DeviceSessionID)
	if err != nil {
		if infraerrors.Reason(err) == "DEVICE_RESULT_LOST" {
			return s.restartOnboardingDevice(ctx, job, state)
		}
		return err
	}
	switch device.Status {
	case "pending":
		return s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Release: true})
	case "approved":
		if device.PreparedID == nil || *device.PreparedID <= 0 {
			return kiloOnboardingFail("DEVICE_RESULT_LOST", "设备凭据未能附加到当前任务", "重新设备授权", false)
		}
		job.PreparedID = *device.PreparedID
		state.DeviceVerificationURL, state.DeviceUserCode = "", ""
		return s.persistOnboarding(withKiloClaimLease(ctx, job), job, state, kiloOnboardingProgress{Stage: "credential_verified", Release: true})
	case "denied", "expired", "cancelled":
		return s.restartOnboardingDevice(ctx, job, state)
	default:
		return kiloOnboardingFail("DEVICE_RESULT_LOST", "设备授权状态不能恢复", "重新设备授权", false)
	}
}

func (s *KiloService) discardOnboardingDevice(ctx context.Context, job *kiloClaim, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = LockKiloOnboardingLease(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_kilo_device_sessions SET status='cancelled',code_encrypted='',token_encrypted='',updated_at=NOW() WHERE id=$1 AND account_id=$2 AND status IN ('pending','approved')`, id, *job.AccountID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *KiloService) restartOnboardingDevice(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime) error {
	if state.DeviceRestarts >= 2 {
		return kiloOnboardingFail("DEVICE_RESULT_LOST", "设备授权恢复预算已用完", "检查设备回执后手动重新授权", false)
	}
	if err := s.discardOnboardingDevice(ctx, job, state.DeviceSessionID); err != nil {
		return err
	}
	state.DeviceSessionID, state.DeviceVerificationURL, state.DeviceUserCode = "", "", ""
	state.DeviceApproved = false
	state.DeviceRestarts++
	return s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Stage: "device_authorization", Release: true, Code: "DEVICE_RESULT_LOST", Message: "原设备授权过期或消费结果丢失，将申请新设备授权", Action: "保留已完成的登录与订阅事实，重新设备授权"})
}

func (s *KiloService) verifyOnboardingCredential(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime, source *Account) error {
	operation, unlock, err := s.lockPreparedOperation(ctx, job.PreparedID)
	if err != nil {
		return err
	}
	defer unlock()
	ctx = withKiloClaimLease(operation, job)
	account, err := s.ensurePreparedAccount(ctx, job.PreparedID, &job.Options)
	if err != nil {
		return err
	}
	if !kiloOnboardingIdentityMatches(account, job) || job.AccountID == nil || account.ID != *job.AccountID {
		return kiloConflict("预备凭据不属于当前自动接入网关")
	}
	route, err := s.acquireLoginRoute(ctx, account.ID)
	if err != nil {
		return err
	}
	defer route.Close()
	if route.ProxyID != *job.LoginProxyID {
		return kiloBad("LOGIN_PROXY_CONFLICT", "验证出口与固定登录代理不一致")
	}
	credential, organization, err := s.preparedCredential(route.Context, job.PreparedID)
	if err != nil {
		return err
	}
	if organization != job.OrganizationID {
		return kiloBad("ORGANIZATION_DENIED", "预备凭据所属组织与任务不一致")
	}
	profile, err := s.remote.Profile(route.Context, credential, route.URL)
	if err != nil {
		return err
	}
	if profile == nil || state.RemoteUserID == "" || profile.ID != state.RemoteUserID {
		return kiloBad("IDENTITY_MISMATCH", "Gateway profile 与已验证 Kilo 身份不一致")
	}
	if err = kiloRequireOrganization(profile, organization); err != nil {
		return err
	}
	job.CredentialReady = true
	if err = s.saveKiloIdentityMaterial(ctx, job, state); err != nil {
		return err
	}
	if err = validateKiloSubscriptionReceipt(job, state); err != nil {
		return err
	}
	connection, err := s.remote.Connection(route.Context, credential, organization, route.URL)
	if err != nil {
		return err
	}
	if connection == nil || !connection.Connected {
		return kiloBad("BYOK_NOT_CONNECTED", "目标空间尚未连接 ChatGPT 订阅")
	}
	if !strings.EqualFold(strings.TrimSpace(connection.Email), kiloSourceEmail(source)) {
		return kiloBad("IDENTITY_MISMATCH", "官方连接的 ChatGPT 身份与来源账号不一致")
	}
	job.SubscriptionConnected = true
	if err = s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Stage: "subscription_verified"}); err != nil {
		return err
	}
	if connection.UsageLimitReached {
		return s.awaitQuotaReset(ctx, job, connection.UsageResetsAt)
	}
	models, modelErr := s.discoverKiloModels(route.Context, source, credential, organization, route.URL, job.VerifiedModels)
	if modelErr != nil {
		return modelErr
	}
	job.DiscoveredModels = models
	if job.Activate {
		if err = s.prepareJobEgress(ctx, job, account); err != nil {
			return err
		}
	}
	if err = s.completeJob(ctx, job, account.ID, profile, kiloHash([]string{credential, organization})); err != nil {
		return err
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = s.cancelOnboardingRemote(cleanup, job.ID)
	return nil
}

func (s *KiloService) finishOnboardingError(ctx context.Context, job *kiloClaim, cause error) error {
	// Every write still checks the durable lease/revision. A late network result
	// cannot turn cancellation into failure, much less publish a credential.
	if err := s.checkJobOwned(ctx, job); err != nil {
		return nil
	}
	var attached int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(prepared_id,0) FROM pk_kilo_jobs WHERE id=$1 AND revision=$2 AND lease_token=$3 AND status='running' AND lease_until>NOW()`, job.ID, job.Revision, job.Lease).Scan(&attached); err != nil {
		return err
	}
	job.PreparedID = attached
	state, err := s.readOnboardingState(job.RemoteState)
	if err != nil {
		state = &kiloOnboardingRuntime{Version: 1, Revision: job.Revision}
	}
	failure := &kiloOnboardingFailure{code: infraerrors.Reason(cause), message: "处理未完成，账号保持不可调度", action: "检查本阶段配置和执行器后重试", retryable: infraerrors.Code(cause) >= 500}
	var known *kiloOnboardingFailure
	if errors.As(cause, &known) {
		failure = known
	} else if infraerrors.Code(cause) > 0 && infraerrors.Code(cause) < 500 {
		failure.message = infraerrors.Message(cause)
	}
	if failure.code == "" {
		failure.code = "ONBOARDING_UNAVAILABLE"
	}
	if failure.code == "KILO_IDENTITY_FORBIDDEN" && job.AccountID != nil {
		if account, readErr := s.accounts.GetByID(ctx, *job.AccountID); readErr == nil {
			if recorder, ok := s.accounts.(KiloGatewayFailureRepository); ok {
				_, _ = recorder.RecordKiloGatewayFailure(ctx, account, http.StatusForbidden, KiloIdentityForbiddenReason)
			}
		}
	}
	if job.Kind == KiloJobUnbindSource {
		// A remote failure never advertises an empty slot. Keep retryable
		// transport failures in the durable queue; conflicts require review.
		failure.retryable = infraerrors.Code(cause) >= 500 || errors.Is(cause, ErrKiloEgressDraining)
	}
	if errors.Is(cause, ErrKiloEgressCapacity) {
		failure = &kiloOnboardingFailure{code: "EXECUTOR_BUSY", message: "固定登录代理正在执行认证操作", action: "等待原认证操作完成", retryable: true}
	}
	state.Failures++
	status, next := "failed", time.Now().Add(time.Minute)
	if failure.retryable && (state.Failures <= 8 || job.Kind == KiloJobUnbindSource) {
		status = "awaiting_authorization"
		delay := time.Duration(1<<min(state.Failures-1, 4)) * 10 * time.Second
		next = time.Now().Add(delay)
	}
	if job.PreparedID > 0 {
		// A previous transaction may have attached the device before its caller
		// observed the commit. Use the current prepared-aware fencing context.
		ctx = withKiloClaimLease(ctx, job)
	}
	if failure.code == "QUOTA_OAUTH_BUSY" {
		status, next = "awaiting_authorization", time.Now().Add(10*time.Second)
	}
	if state.ReconcileSubscription && (failure.code == "SESSION_LOST" || failure.code == "AUTH_SESSION_EXPIRED") {
		status = "failed"
		failure = kiloReconcileSessionFailureDetails(failure.code)
	}
	if !state.ReconcileSubscription && failure.code == "SESSION_LOST" && status == "awaiting_authorization" {
		return s.rebuildOnboardingSession(ctx, job, state)
	}
	return s.persistOnboarding(ctx, job, state, kiloOnboardingProgress{Status: status, Release: true, Code: failure.code, Message: failure.message, Action: failure.action, Next: next})
}

// Authentication tasks outlive a short Go claim. They reserve the existing
// login route's durable lease with the operation UUID, so a node restart or a
// cancelled HTTP context cannot accidentally admit another login on that exit.
// Like ordinary egress leases, expired rows still count until known stopped.
func (s *KiloService) reserveOnboardingLogin(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime) (string, error) {
	if job.AccountID == nil || job.LoginProxyID == nil || state.OperationID == "" {
		return "", kiloConflict("自动接入认证出口缺少持久归属")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = LockKiloOnboardingLease(ctx, tx.Client()); err != nil {
		return "", err
	}
	txctx := dbent.NewTxContext(ctx, tx)
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return "", errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return "", err
	}
	// This is the existing egress owner's lock, after the ordinary mutation
	// lock (70440001); no independent pool or ownership table is introduced.
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(70440079)); err != nil {
		return "", err
	}
	account, err := s.accounts.GetByID(txctx, *job.AccountID)
	if err != nil || !kiloOnboardingIdentityMatches(account, job) {
		return "", kiloConflict("认证网关身份已改变")
	}
	proxy, err := s.proxies.GetByID(txctx, *job.LoginProxyID)
	if err != nil || !usableAssignedAuthProxy(proxy) {
		return "", kiloBad("LOGIN_PROXY_CONFLICT", "固定登录代理不可用，禁止更换代理或直连")
	}
	configHash := kiloOnboardingProxyHash(proxy.URL())
	if state.ProxyHash != "" && state.ProxyHash != configHash {
		return "", kiloBad("LOGIN_PROXY_CONFLICT", "登录代理凭据在授权期间已改变")
	}
	var routeID int64
	var storedHash, fingerprint, routeState string
	var verified *time.Time
	if err = kiloScan(ctx, tx.Client(), `SELECT r.id,r.config_hash,COALESCE(r.credential_fingerprint,r.config_hash),r.state,r.verified_at FROM pk_kilo_egress_routes r JOIN accounts a ON a.id=r.account_id JOIN proxies p ON p.id=r.proxy_id WHERE r.account_id=$1 AND r.proxy_id=$2 AND a.deleted_at IS NULL AND p.deleted_at IS NULL AND a.extra->'kilo_login_proxy_id'=to_jsonb(r.proxy_id) FOR SHARE OF a,p FOR UPDATE OF r`, []any{*job.AccountID, *job.LoginProxyID}, &routeID, &storedHash, &fingerprint, &routeState, &verified); err != nil {
		return "", err
	}
	if storedHash != configHash || routeState != KiloEgressActive || verified == nil {
		return "", ErrKiloEgressUnavailable
	}
	var existing int64
	err = kiloScan(ctx, tx.Client(), `SELECT route_id FROM pk_kilo_egress_leases WHERE id=$1::uuid AND account_id=$2 FOR UPDATE`, []any{state.OperationID, *job.AccountID}, &existing)
	if err == nil {
		if existing != routeID {
			return "", kiloConflict("认证出口操作已属于其他路由")
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		var used int
		if err = kiloScan(ctx, tx.Client(), `SELECT COUNT(*) FROM pk_kilo_egress_leases l JOIN pk_kilo_egress_routes r ON r.id=l.route_id WHERE r.account_id=$1 AND COALESCE(r.credential_fingerprint,r.config_hash)=$2`, []any{*job.AccountID, fingerprint}, &used); err != nil {
			return "", err
		}
		if used != 0 {
			return "", ErrKiloEgressCapacity
		}
		if _, err = tx.Client().ExecContext(ctx, `INSERT INTO pk_kilo_egress_leases(id,account_id,route_id,expires_at) VALUES($1::uuid,$2,$3,$4)`, state.OperationID, *job.AccountID, routeID, state.DeadlineAt); err != nil {
			return "", err
		}
	} else {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	state.RemoteLeaseID, state.ProxyHash = state.OperationID, configHash
	return proxy.URL(), nil
}

func (s *KiloService) releaseOnboardingLogin(ctx context.Context, job *kiloClaim, state *kiloOnboardingRuntime) error {
	if state.RemoteLeaseID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM pk_kilo_egress_leases WHERE id=$1::uuid AND account_id=$2`, state.RemoteLeaseID, job.AccountID)
	return err
}

func (s *KiloService) cleanupOnboardingRuntime(ctx context.Context, state *kiloOnboardingRuntime) (bool, error) {
	if state.RemoteClosed {
		return true, nil
	}
	if state.TaskID != "" {
		if s.executor == nil {
			return false, nil
		}
		if err := s.executor.Cancel(ctx, state.TaskID); err != nil {
			code, _ := kiloExecutorErrorInfo(err)
			if code != "SESSION_LOST" {
				return false, err
			}
			if state.DeadlineAt.After(time.Now()) {
				return false, nil
			}
		}
		task, err := s.executor.Task(ctx, state.TaskID)
		if err != nil {
			code, _ := kiloExecutorErrorInfo(err)
			if code != "SESSION_LOST" {
				return false, err
			}
			if state.DeadlineAt.After(time.Now()) {
				return false, nil
			}
		} else if task == nil || (task.Status != "succeeded" && task.Status != "failed" && task.Status != "cancelled" && task.Status != "needs_action") {
			return false, nil
		}
	}
	if state.TaskID == "" && state.RemoteLeaseID != "" && !state.DeadlineAt.IsZero() && state.DeadlineAt.After(time.Now()) {
		// Submit may have reached the executor but its task id was lost. Reuse
		// the same operation in the active worker; cancellation cannot invent an
		// id or free its route while that remote deadline is still live.
		return false, nil
	}
	if state.RemoteLeaseID != "" {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM pk_kilo_egress_leases WHERE id=$1::uuid`, state.RemoteLeaseID); err != nil {
			return false, err
		}
	}
	if s.executor == nil && state.SessionHandle != "" && state.ExpiresAt.After(time.Now()) {
		return false, nil
	}
	if s.executor != nil {
		ackIDs := append([]string(nil), state.AckTaskIDs...)
		if state.TaskID != "" {
			ackIDs = append(ackIDs, state.TaskID)
		}
		for _, taskID := range ackIDs {
			if err := s.executor.Ack(ctx, taskID); err != nil {
				code, _ := kiloExecutorErrorInfo(err)
				if code != "SESSION_LOST" {
					return false, err
				}
			}
		}
		if state.SessionHandle != "" {
			if err := s.executor.CloseSession(ctx, state.SessionHandle); err != nil {
				code, _ := kiloExecutorErrorInfo(err)
				if code != "SESSION_LOST" {
					return false, err
				}
			}
		}
	}
	return true, nil
}

// CancelJob first revokes the local revision/lease, then invokes this cleanup.
// Maintenance retries incomplete cleanup without restoring the revoked identity.
func (s *KiloService) cancelOnboardingRemote(ctx context.Context, jobID int64) error {
	var cipher, status string
	var revision int64
	var accountID *int64
	if err := s.db.QueryRowContext(ctx, `SELECT remote_state_encrypted,status,revision,account_id FROM pk_kilo_jobs WHERE id=$1 AND job_kind IN ('onboard_source','unbind_source')`, jobID).Scan(&cipher, &status, &revision, &accountID); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if status != "cancelled" && status != "failed" && status != "succeeded" {
		return kiloConflict("请先撤销任务执行身份，再清理远端会话")
	}
	if cipher != "" {
		state, err := s.readOnboardingState(cipher)
		if err != nil {
			return err
		}
		stopped, err := s.cleanupOnboardingRuntime(ctx, state)
		if err != nil {
			return err
		}
		if !stopped {
			return nil
		}
		state.RemoteClosed = true
		state.SessionHandle, state.TaskID, state.OperationID, state.Phase, state.RemoteLeaseID = "", "", "", "", ""
		state.ExpiresAt, state.DeadlineAt, state.AckTaskIDs = time.Time{}, time.Time{}, nil
		raw, marshalErr := json.Marshal(state)
		if marshalErr != nil {
			return marshalErr
		}
		closedCipher, encryptErr := s.encryptor.Encrypt(string(raw))
		if encryptErr != nil {
			return encryptErr
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE pk_kilo_jobs SET remote_state_encrypted=$5,remote_outcome='cleaned',updated_at=NOW() WHERE id=$1 AND revision=$2 AND status=$3 AND remote_state_encrypted=$4`, jobID, revision, status, cipher, closedCipher); err != nil {
			return err
		}
	}
	if status == "cancelled" {
		if cleanupErr := s.cleanupCancelledKiloBinding(ctx, jobID); cleanupErr != nil {
			return cleanupErr
		}
	}
	if accountID != nil {
		// A failed local write may still have a durable approved token receipt.
		// Preserve it for Retry; only explicit cancellation discards that result.
		if status == "cancelled" || status == "succeeded" {
			_, err := s.db.ExecContext(ctx, `UPDATE pk_kilo_device_sessions SET status='cancelled',code_encrypted='',token_encrypted='',updated_at=NOW() WHERE account_id=$1 AND status IN ('pending','approved') AND NOT EXISTS(SELECT 1 FROM pk_kilo_jobs WHERE account_id=$1 AND status IN ('queued','running','awaiting_authorization'))`, *accountID)
			if err != nil {
				return err
			}
		}
		if status == "cancelled" {
			s.releaseInactiveKiloEgress(ctx, *accountID)
		}
	}
	if cipher == "" {
		_, err := s.db.ExecContext(ctx, `UPDATE pk_kilo_jobs SET remote_outcome='cleaned',updated_at=NOW() WHERE id=$1 AND revision=$2 AND status=$3`, jobID, revision, status)
		return err
	}
	return nil
}

func (s *KiloService) cleanupFinishedOnboarding(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM pk_kilo_jobs WHERE job_kind IN ('onboard_source','unbind_source') AND status IN ('failed','cancelled','succeeded') AND (remote_state_encrypted<>'' OR status='cancelled') AND remote_outcome<>'cleaned' ORDER BY updated_at,id LIMIT 10`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		cleanup, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = s.cancelOnboardingRemote(cleanup, id)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

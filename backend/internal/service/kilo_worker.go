package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

func (s *KiloService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			_ = s.expireDevices(ctx)
			_ = s.cleanupFinishedOnboarding(ctx)
			_ = s.drainRequestedEgress(ctx)
			if err := s.maintainKiloBindings(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("kilo binding recovery pending", "error_class", fmt.Sprintf("%T", err))
			}
			if _, err := s.ProcessBatch(ctx, 4); err != nil && ctx.Err() == nil {
				slog.Warn("kilo onboarding batch unavailable", "error_class", fmt.Sprintf("%T", err))
			}
			timer.Reset(5 * time.Second)
		}
	}()
}
func (s *KiloService) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	if cancel != nil {
		cancel()
	}
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	s.mu.Lock()
	if s.done == done {
		s.cancel = nil
		s.done = nil
	}
	s.mu.Unlock()
}

type kiloClaim struct {
	BindingRevision                                         int64
	ReusedIdentity                                          bool
	ID, PreparedID                                          int64
	SourceID                                                *int64
	SourceIdentity                                          string
	Lease                                                   string
	Options                                                 KiloBatchInput
	Kind, Stage                                             string
	Revision                                                int64
	AccountID                                               *int64
	LoginProxyID                                            *int64
	OrganizationID                                          string
	VerifiedModels                                          []string
	DiscoveredModels                                        []string
	Activate                                                bool
	MaterialVersion                                         string
	LoginProxyHash                                          string
	QuotaTaskID                                             *int64
	CredentialReady, SubscriptionConnected, QuotaOAuthReady bool
	RemoteState, RemoteOutcome, Name                        string
}

// SKIP LOCKED and an execution ownership token make ingress submissions portable
// to another maintenance node without creating a second local account.
func (s *KiloService) claimJob(ctx context.Context) (*kiloClaim, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	j := &kiloClaim{Lease: uuid.NewString()}
	var raw, models []byte
	err = tx.QueryRowContext(ctx, `SELECT id,COALESCE(prepared_id,0),source_account_id,source_identity,options,job_kind,stage,revision,account_id,login_proxy_id,organization_id,verified_model_ids,activate_when_ready,material_version,quota_task_id,credential_ready,subscription_connected,quota_oauth_ready,remote_state_encrypted,remote_outcome,binding_revision,reused_identity FROM pk_kilo_jobs WHERE job_kind IN ('activate_prepared','onboard_source','unbind_source') AND status IN ('queued','running','awaiting_authorization') AND next_attempt_at<=NOW() AND (lease_until IS NULL OR lease_until<=NOW()) ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&j.ID, &j.PreparedID, &j.SourceID, &j.SourceIdentity, &raw, &j.Kind, &j.Stage, &j.Revision, &j.AccountID, &j.LoginProxyID, &j.OrganizationID, &models, &j.Activate, &j.MaterialVersion, &j.QuotaTaskID, &j.CredentialReady, &j.SubscriptionConnected, &j.QuotaOAuthReady, &j.RemoteState, &j.RemoteOutcome, &j.BindingRevision, &j.ReusedIdentity)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &j.Options); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(models, &j.VerifiedModels); err != nil {
		return nil, err
	}
	var named struct {
		OnboardingName string `json:"onboarding_name"`
	}
	if err = json.Unmarshal(raw, &named); err != nil {
		return nil, err
	}
	j.Name = named.OnboardingName
	if j.Kind == KiloJobOnboardSource && (j.Stage == "queued" || j.Stage == "") {
		j.Stage = "preflight"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='running',stage=CASE WHEN job_kind='activate_prepared' THEN 'checking' WHEN stage='queued' OR stage='' THEN 'preflight' ELSE stage END,lease_token=$2,lease_until=NOW()+INTERVAL '4 minutes',attempt=attempt+1,updated_at=NOW() WHERE id=$1`, j.ID, j.Lease); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return j, nil
}
func (s *KiloService) ProcessBatch(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 20 {
		return 0, kiloBad("KILO_WORKER_LIMIT", "任务批次大小无效")
	}
	completed := 0
	for i := 0; i < limit; i++ {
		if err := ctx.Err(); err != nil {
			return completed, err
		}
		job, err := s.claimJob(ctx)
		if err != nil {
			return completed, err
		}
		if job == nil {
			break
		}
		workCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		err = s.processJob(workCtx, job)
		cancel()
		if err != nil {
			if job.Kind == KiloJobOnboardSource || job.Kind == KiloJobUnbindSource {
				stateCtx, stateCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				stateErr := s.finishOnboardingError(stateCtx, job, err)
				stateCancel()
				if stateErr != nil {
					return completed, stateErr
				}
				completed++
				continue
			}
			message := "处理未完成，请检查凭据、组织权限与出口后重新检查"
			if infraerrors.Code(err) > 0 && infraerrors.Code(err) < 500 {
				message = infraerrors.Message(err)
			}
			stateCtx, stateCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			stateErr := s.finishJobFailure(stateCtx, job, message)
			stateCancel()
			if stateErr != nil {
				return completed, stateErr
			}
		}
		completed++
	}
	return completed, nil
}
func (s *KiloService) finishJobFailure(ctx context.Context, job *kiloClaim, message string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='failed',stage='failed',last_error=$3,lease_token=NULL,lease_until=NULL,finished_at=NOW(),updated_at=NOW() WHERE id=$1 AND status='running' AND lease_token=$2`, job.ID, job.Lease, message)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		if _, err = tx.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status='ready',last_error=$3,updated_at=NOW() WHERE id=$1 AND binding_job_id=$2 AND status='binding'`, job.PreparedID, job.ID, message); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *KiloService) awaitAuthorization(ctx context.Context, job *kiloClaim, link, message string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='awaiting_authorization',stage='official_authorization',verification_url=$3,last_error=$4,next_attempt_at=NOW()+INTERVAL '60 seconds',lease_token=NULL,lease_until=NULL,updated_at=NOW() WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>NOW()`, job.ID, job.Lease, link, message)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return kiloConflict("任务已取消或由其他维护节点接管")
	}
	return nil
}
func kiloBYOKURL(organization string) string {
	if organization != "" {
		return "https://app.kilo.ai/organizations/" + url.PathEscape(organization) + "/byok"
	}
	return "https://app.kilo.ai/byok"
}

func (s *KiloService) processJob(ctx context.Context, job *kiloClaim) error {
	if job.Kind == KiloJobUnbindSource {
		return s.processKiloUnbind(ctx, job)
	}
	if job.Kind == KiloJobOnboardSource {
		return s.processOnboardingJob(ctx, job)
	}
	operationCtx, unlock, err := s.lockPreparedOperation(ctx, job.PreparedID)
	if err != nil {
		return err
	}
	defer unlock()
	ctx = withKiloClaimLease(operationCtx, job)
	if err := s.checkJobOwned(ctx, job); err != nil {
		return err
	}
	account, err := s.ensurePreparedAccount(ctx, job.PreparedID, &job.Options)
	if err != nil {
		return err
	}
	if err = s.prepareLoginRoute(ctx, job.PreparedID, account.ID, &job.Options); err != nil {
		return err
	}
	route, err := s.acquireLoginRoute(ctx, account.ID)
	if err != nil {
		return err
	}
	defer route.Close()
	if err = s.checkJobOwned(route.Context, job); err != nil {
		return err
	}
	credential, organization, err := s.preparedCredential(route.Context, job.PreparedID)
	if err != nil {
		return err
	}
	profile, err := s.remote.Profile(route.Context, credential, route.URL)
	if err != nil {
		return err
	}
	if err = kiloRequireOrganization(profile, organization); err != nil {
		return err
	}
	if job.SourceID != nil {
		// The public status endpoint does not expose the official enable switch.
		// Identity checks cannot silently stand in for the operator's confirmation.
		if !job.Options.BYOKEnabledConfirmed {
			return s.awaitAuthorization(ctx, job, kiloBYOKURL(organization), "请在官方页面完成ChatGPT授权并启用BYOK，再重新提交勾选启用确认的配对任务")
		}
		source, readErr := s.accounts.GetByID(route.Context, *job.SourceID)
		if readErr != nil {
			return readErr
		}
		if !source.IsOpenAIOAuth() || source.ParentAccountID != nil || kiloSourceIdentity(source) != job.SourceIdentity {
			return kiloBad("KILO_SOURCE_CHANGED", "来源账号身份已改变，请取消并重新提交配对任务")
		}
		email := kiloSourceEmail(source)
		if email == "" {
			return kiloBad("KILO_SOURCE_IDENTITY", "来源账号缺少可验证邮箱，不能自动确认订阅关联")
		}
		connection, checkErr := s.remote.Connection(route.Context, credential, organization, route.URL)
		if checkErr != nil {
			return checkErr
		}
		if !connection.Connected {
			return s.awaitAuthorization(ctx, job, kiloBYOKURL(organization), "请在官方BYOK页面登录目标Kilo账号并连接所选ChatGPT订阅；完成后将自动继续")
		}
		if connection.Email == "" || !strings.EqualFold(strings.TrimSpace(connection.Email), email) {
			return kiloBad("KILO_CONNECTION_MISMATCH", "官方连接的ChatGPT身份与本次选中的来源账号不一致，请核对后重试")
		}
		if connection.UsageLimitReached {
			return s.awaitQuotaReset(ctx, job, connection.UsageResetsAt)
		}
		if sourceID, ok := source.Credentials["chatgpt_account_id"].(string); ok && sourceID != "" && connection.AccountID != "" && sourceID != connection.AccountID {
			return kiloBad("KILO_CONNECTION_MISMATCH", "官方连接的订阅账号编号与所选来源不一致")
		}
	}
	if err = s.prepareJobEgress(ctx, job, account); err != nil {
		return err
	}
	return s.completeJob(ctx, job, account.ID, profile, kiloHash([]string{credential, organization}))
}
func kiloRequireOrganization(profile *KiloRemoteProfile, organization string) error {
	if profile == nil {
		return errors.New("Kilo账号资料无效")
	}
	if organization == "" {
		if !profile.HasPersonalAccount {
			return kiloBad("KILO_ORGANIZATION_REQUIRED", "此凭据没有个人空间，请选择可用组织")
		}
		return nil
	}
	for _, org := range profile.Organizations {
		if org.ID == organization {
			return nil
		}
	}
	return kiloBad("KILO_ORGANIZATION_DENIED", "此Kilo凭据无权访问选定组织")
}
func (s *KiloService) preparedCredential(ctx context.Context, id int64) (string, string, error) {
	var cipher, organization string
	err := s.db.QueryRowContext(ctx, `SELECT credential_encrypted,organization_id FROM pk_kilo_prepared_accounts WHERE id=$1`, id).Scan(&cipher, &organization)
	if err != nil {
		return "", "", err
	}
	credential, err := s.encryptor.Decrypt(cipher)
	if err != nil {
		return "", "", errors.New("Kilo凭据解密失败")
	}
	return credential, organization, nil
}

func (s *KiloService) prepareJobEgress(ctx context.Context, job *kiloClaim, account *Account) error {
	reader, ok := s.accounts.(AccountProxyBindingRepository)
	if !ok {
		return errors.New("账号代理配置不可用")
	}
	if err := s.checkJobOwned(ctx, job); err != nil {
		return err
	}
	assignmentCtx := ctx
	if job.Options.ProxyGroupID != kiloAllRequestProxies {
		assignmentCtx = WithProxyGroupFilter(ctx, job.Options.ProxyGroupID)
	}
	_, err := reader.AssignAccountProxies(assignmentCtx, AccountProxyAssignmentInput{AccountIDs: []int64{account.ID}, ProxiesPerAccount: job.Options.ProxiesPerAccount, MaxAccountsPerProxy: 1, ProxyConcurrency: job.Options.ProxyConcurrency})
	if err != nil {
		return err
	}
	bindings, err := reader.GetAccountProxyBindings(ctx, account.ID)
	if err != nil {
		return err
	}
	if len(bindings) < job.Options.ProxiesPerAccount {
		return kiloBad("KILO_EGRESS_INCOMPLETE", "可用出口不足，账号保持未启用，请补充代理后重新检查")
	}
	for _, binding := range bindings {
		if _, err = s.egress.Verify(ctx, account.ID, binding.ProxyID); err != nil {
			return err
		}
	}
	return nil
}

func (s *KiloService) completeJob(ctx context.Context, job *kiloClaim, accountID int64, profile *KiloRemoteProfile, verifiedCredential string) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	txctx := dbent.NewTxContext(ctx, tx)
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var owned int64
	if err = kiloScan(ctx, tx.Client(), `SELECT id FROM pk_kilo_jobs WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_until>NOW() AND ($3::bigint=0 OR revision=$3) FOR UPDATE`, []any{job.ID, job.Lease, job.Revision}, &owned); errors.Is(err, sql.ErrNoRows) {
		return kiloConflict("任务已取消或执行租约已失效")
	} else if err != nil {
		return err
	}
	var ownerID, storedAccountID int64
	var cipher, machine, organization string
	if err = kiloScan(ctx, tx.Client(), `SELECT binding_job_id,account_id,credential_encrypted,machine_id::text,organization_id FROM pk_kilo_prepared_accounts WHERE id=$1 AND status='binding' FOR UPDATE`, []any{job.PreparedID}, &ownerID, &storedAccountID, &cipher, &machine, &organization); err != nil {
		return err
	}
	if ownerID != job.ID || storedAccountID != accountID {
		return kiloConflict("预备账号已由其他任务接管")
	}
	policy, policyErr := kiloReadLifecyclePolicy(ctx, tx.Client(), job.PreparedID)
	if policyErr != nil {
		return policyErr
	}
	if policyErr = kiloRequireLifecycleSource(policy, job.SourceID); policyErr != nil {
		return policyErr
	}
	plaintext, decryptErr := s.encryptor.Decrypt(cipher)
	if decryptErr != nil {
		return errors.New("Kilo凭据解密失败")
	}
	if kiloHash([]string{plaintext, organization}) != verifiedCredential {
		return kiloConflict("验证后的Kilo凭据或组织已更改")
	}
	// Match ordinary account edits: prepared ownership, assignment, then rows.
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return err
	}
	if job.SourceID != nil {
		var lockedID int64
		if err = kiloScan(ctx, tx.Client(), `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, []any{*job.SourceID}, &lockedID); err != nil {
			return err
		}
		source, readErr := s.accounts.GetByID(txctx, *job.SourceID)
		if readErr != nil {
			return readErr
		}
		if kiloSourceIdentity(source) != job.SourceIdentity {
			return kiloBad("KILO_SOURCE_CHANGED", "来源账号在检查期间发生变化，账号未启用")
		}
		if !source.IsActive() || OpenAIAuthRecoveryPending(source) {
			return kiloBad("KILO_GPT_AUTH_UNAVAILABLE", "绑定GPT授权已失效，账号未启用；请先恢复官方来源授权")
		}
		if job.Kind == KiloJobOnboardSource {
			if err = s.checkOnboardingSource(ctx, job, source); err != nil {
				return err
			}
			material, materialErr := s.reauth.ReadKiloLoginMaterial(txctx, source.ID)
			if materialErr != nil || material == nil || material.Version != job.MaterialVersion {
				return kiloBad("LOGIN_MATERIAL_CHANGED", "登录材料在检查期间已改变，请重新提交接入任务")
			}
		}
		if source.Extra == nil {
			source.Extra = map[string]any{}
		}
		if bound := ParseExtraInt(source.Extra["kilo_source_prepared_id"]); bound > 0 && int64(bound) != job.PreparedID {
			return kiloConflict("来源账号已绑定其他Kilo身份")
		}
		source.Extra["kilo_quota_only"] = true
		source.Extra["kilo_source_prepared_id"], source.Extra["kilo_source_gateway_id"] = job.PreparedID, accountID
		if err = s.accounts.Update(txctx, source); err != nil {
			return err
		}
	}
	for _, groupID := range job.Options.GroupIDs {
		var validID int64
		if err = kiloScan(ctx, tx.Client(), `SELECT id FROM groups WHERE id=$1 AND deleted_at IS NULL AND status='active' AND platform='openai' FOR SHARE`, []any{groupID}, &validID); errors.Is(err, sql.ErrNoRows) {
			return kiloBad("KILO_GROUP_CHANGED", "目标分组已停用、删除或更改平台，账号未启用")
		} else if err != nil {
			return err
		}
	}
	var lockedID int64
	if err = kiloScan(ctx, tx.Client(), `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, []any{accountID}, &lockedID); err != nil {
		return err
	}
	account, err := s.accounts.GetByID(txctx, accountID)
	if err != nil {
		return err
	}
	if !kiloPreparedAccountMatches(account, job.PreparedID, plaintext, machine, organization) {
		return kiloBad("KILO_ACCOUNT_CHANGED", "网关账号凭据、身份或所属准备记录已更改，禁止覆盖")
	}
	if job.Kind == KiloJobOnboardSource {
		if !job.CredentialReady || !job.SubscriptionConnected || !job.QuotaOAuthReady {
			return kiloBad("KILO_VERIFICATION_INCOMPLETE", "凭据、订阅或额度授权尚未核实，账号未启用")
		}
		mapping, mappingErr := kiloOnboardingModelMapping(job.DiscoveredModels)
		if mappingErr != nil {
			return mappingErr
		}
		var currentModels []byte
		var currentMaterial, currentOrganization string
		var currentLogin int64
		var currentActivate bool
		if err = kiloScan(ctx, tx.Client(), `SELECT verified_model_ids,material_version,organization_id,login_proxy_id,activate_when_ready FROM pk_kilo_jobs WHERE id=$1`, []any{job.ID}, &currentModels, &currentMaterial, &currentOrganization, &currentLogin, &currentActivate); err != nil {
			return err
		}
		var selected []string
		if json.Unmarshal(currentModels, &selected) != nil || kiloHash(selected) != kiloHash(job.VerifiedModels) || currentMaterial != job.MaterialVersion || currentOrganization != job.OrganizationID || job.LoginProxyID == nil || currentLogin != *job.LoginProxyID || currentActivate != job.Activate || organization != job.OrganizationID || int64(ParseExtraInt(account.Extra[KiloLoginProxyExtraKey])) != currentLogin {
			return kiloConflict("验证后的模型、组织、登录材料或固定代理已改变")
		}
		proxy, proxyErr := s.proxies.GetByID(txctx, currentLogin)
		if proxyErr != nil || !usableAssignedAuthProxy(proxy) || job.LoginProxyHash == "" || kiloOnboardingProxyHash(proxy.URL()) != job.LoginProxyHash {
			return kiloBad("LOGIN_PROXY_CONFLICT", "登录代理配置在检查期间已改变，账号未启用")
		}
		account.Credentials["model_mapping"] = mapping
		for name, value := range kiloCatalogScopeExtra(account, job.DiscoveredModels, time.Now()) {
			account.Extra[name] = value
		}
		account.Extra["kilo_subscription_scope_source_identity"] = job.SourceIdentity
	}
	account.Status, account.Schedulable = StatusActive, true
	if job.Kind == KiloJobOnboardSource && !job.Activate {
		account.Status, account.Schedulable = StatusDisabled, false
	}
	account.Concurrency, account.Priority = job.Options.AccountConcurrency, job.Options.Priority
	if err = s.accounts.BindGroups(txctx, accountID, job.Options.GroupIDs); err != nil {
		return err
	}
	if err = s.accounts.Update(txctx, account); err != nil {
		return err
	}
	routing, _ := json.Marshal(KiloRoutingOptions{ProxyGroupID: job.Options.ProxyGroupID, ProxiesPerAccount: job.Options.ProxiesPerAccount, ProxyConcurrency: job.Options.ProxyConcurrency, AccountConcurrency: job.Options.AccountConcurrency, Priority: job.Options.Priority, AutoReleaseStatuses: job.Options.AutoReleaseStatuses})
	preparedStatus, finalStage := "bound", "bound"
	if job.Kind == KiloJobOnboardSource && !job.Activate {
		preparedStatus, finalStage = "ready", "imported"
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status=$6,binding_state=CASE WHEN $2::bigint IS NULL THEN 'empty' ELSE 'bound' END,identity_status='active',binding_resume=$7,recovery_task_id=NULL,binding_error='',source_account_id=$2,remote_user_id=$3,email=CASE WHEN email='' THEN $4 ELSE email END,last_error='',routing_options=$5::jsonb,updated_at=NOW() WHERE id=$1`, job.PreparedID, job.SourceID, profile.ID, profile.Email, string(routing), preparedStatus, account.Schedulable); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='succeeded',stage=$4,account_id=$3,last_error='',error_code='',failed_stage='',next_action='',lease_token=NULL,lease_until=NULL,verification_url='',user_code='',finished_at=NOW(),updated_at=NOW() WHERE id=$1 AND lease_token=$2`, job.ID, job.Lease, accountID, finalStage); err != nil {
		return err
	}
	return tx.Commit()
}

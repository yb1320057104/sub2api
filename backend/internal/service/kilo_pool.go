package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	KiloBindingNewIdentity = "new_identity"
	KiloBindingPreferIdle  = "prefer_idle"
)

type kiloIdleIdentity struct{ ID, AccountID, ProxyID, Revision int64 }

// The batch lock orders this reservation before account/proxy mutation locks.
// Empty means the remote subscription was confirmed disconnected, not merely
// that a failed source disappeared from the local card.
func (s *KiloService) claimIdleKiloIdentity(ctx context.Context, tx *dbent.Tx, item *KiloOnboardingItem, strategy string) (*kiloIdleIdentity, error) {
	if item.PreparedID == 0 && strategy != KiloBindingPreferIdle {
		return nil, nil
	}
	var p kiloIdleIdentity
	err := kiloScan(ctx, tx.Client(), `SELECT p.id,p.account_id,p.login_proxy_id,p.binding_revision FROM pk_kilo_prepared_accounts p JOIN accounts g ON g.id=p.account_id JOIN proxies x ON x.id=p.login_proxy_id
 WHERE ($1::bigint=0 OR p.id=$1) AND p.organization_id=$2 AND p.identity_status='active' AND p.identity_mode='independent' AND p.identity_checked_at IS NOT NULL AND p.identity_session_encrypted<>'' AND p.binding_state='empty' AND p.source_account_id IS NULL AND p.status='ready'
 AND p.remote_user_id<>'' AND p.credential_encrypted<>'' AND (p.identity_session_encrypted<>'' OR p.identity_credential_encrypted<>'') AND g.deleted_at IS NULL AND NOT g.schedulable AND g.extra->'kilo_managed'='true'::jsonb AND g.extra->>'kilo_prepared_id'=p.id::text
 AND x.deleted_at IS NULL AND x.status='active' AND NOT EXISTS(SELECT 1 FROM pk_kilo_jobs j WHERE j.prepared_id=p.id AND j.status IN ('queued','running','awaiting_authorization'))
 ORDER BY p.updated_at,p.id FOR UPDATE OF p SKIP LOCKED LIMIT 1`, []any{item.PreparedID, item.OrganizationID}, &p.ID, &p.AccountID, &p.ProxyID, &p.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		if item.PreparedID > 0 {
			return nil, kiloConflict("所选Kilo槽位已占用、封禁或尚未确认解绑")
		}
		if item.LoginProxyID <= 0 {
			return nil, kiloBad("KILO_LOGIN_PROXY_REQUIRED", "没有可复用的空闲槽位，请选择新建身份的固定登录代理")
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item.LoginProxyID = p.ProxyID
	return &p, nil
}

func (s *KiloService) reserveKiloPoolBinding(ctx context.Context, tx *dbent.Tx, p *kiloIdleIdentity, source *Account, jobID int64) error {
	txctx := dbent.NewTxContext(ctx, tx)
	account, err := s.accounts.GetByID(txctx, p.AccountID)
	if err != nil {
		return err
	}
	if account.Schedulable || !account.IsKiloGatewayAccount() || int64(ParseExtraInt(account.Extra[KiloLoginProxyExtraKey])) != p.ProxyID {
		return kiloConflict("空闲Kilo身份的网关或固定代理已改变")
	}
	p.Revision++
	account.Extra["kilo_onboarding_source_id"] = source.ID
	account.Extra["kilo_onboarding_job_id"] = jobID
	account.Extra["kilo_binding_revision"] = p.Revision
	source.Extra["kilo_source_prepared_id"], source.Extra["kilo_source_gateway_id"] = p.ID, p.AccountID
	clearKiloModelScope(account)
	account.Status, account.Schedulable, account.ErrorMessage = StatusDisabled, false, ""
	if err = s.accounts.Update(txctx, account); err != nil {
		return err
	}
	result, err := tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status='binding',binding_state='binding',binding_revision=$3,binding_job_id=$2,recovery_task_id=NULL,binding_error='',binding_resume=false,source_account_id=$4,updated_at=NOW() WHERE id=$1 AND binding_state='empty' AND source_account_id IS NULL AND identity_status='active' AND identity_mode='independent' AND identity_checked_at IS NOT NULL AND identity_session_encrypted<>''`, p.ID, jobID, p.Revision, source.ID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return kiloConflict("空闲槽位已由其他任务领取")
	}
	_, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_jobs SET binding_revision=$2 WHERE id=$1`, jobID, p.Revision)
	return err
}

func clearKiloModelScope(account *Account) {
	if account.Credentials == nil {
		account.Credentials = map[string]any{}
	}
	delete(account.Credentials, "model_mapping")
	for _, key := range []string{"kilo_subscription_models", "kilo_subscription_scope_source", "kilo_subscription_scope_verified_at", "kilo_subscription_scope_key", "kilo_subscription_scope_organization", "kilo_subscription_scope_source_identity"} {
		delete(account.Extra, key)
	}
}

// Read through the caller's transaction: an atomic 2FA import must not ask a
// second connection to read a configuration which has not committed yet.
func (s *KiloService) kiloMaterialVersionTx(ctx context.Context, tx *dbent.Tx, id int64) (*KiloLoginMaterial, error) {
	var email, password, totp, mode string
	var updated time.Time
	err := kiloScan(ctx, tx.Client(), `SELECT login_email,COALESCE(password_ciphertext,''),COALESCE(totp_secret_ciphertext,''),credential_mode,updated_at FROM openai_oauth_reauth_configs WHERE account_id=$1`, []any{id}, &email, &password, &totp, &mode, &updated)
	if err != nil {
		return nil, err
	}
	if mode != OpenAIOAuthReauthModePasswordTOTP || password == "" {
		return nil, kiloBad("KILO_LOGIN_MATERIAL_REQUIRED", "请先保存来源密码与二步验证登录配置")
	}
	raw, _ := json.Marshal([]string{email, password, totp, updated.UTC().Format(time.RFC3339Nano)})
	sum := sha256.Sum256(raw)
	return &KiloLoginMaterial{Email: email, Version: hex.EncodeToString(sum[:])}, nil
}

type KiloSourceRecoveryDriver interface {
	EnsureKiloSourceRecovery(context.Context, int64) (KiloSourceRecoveryResult, error)
}

type kiloPoolBinding struct {
	ExpectedTokenDigest, ExpectedRecoveryGeneration string
	ID, GatewayID, SourceID, Revision               int64
	State, Identity, Email                          string
	TaskID                                          int64
	Resume                                          bool
}

// Recovery is driven outside mutation transactions. The original re-login
// service owns its configured engine, attempts, cooldowns and terminal receipt.
func (s *KiloService) maintainKiloBindings(ctx context.Context) error {
	if s.recovery == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,account_id,CASE WHEN identity_mode='gpt_coupled' THEN identity_source_account_id ELSE source_account_id END,binding_revision,binding_state,identity_status,COALESCE(recovery_task_id,0),binding_resume FROM pk_kilo_prepared_accounts WHERE identity_status='active' AND ((identity_mode<>'gpt_coupled' AND source_account_id IS NOT NULL AND binding_state IN ('bound','recovering')) OR (identity_mode='gpt_coupled' AND identity_source_account_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM pk_kilo_jobs j WHERE j.prepared_id=pk_kilo_prepared_accounts.id AND j.status IN ('queued','running','awaiting_authorization')))) ORDER BY updated_at,id LIMIT 100`)
	if err != nil {
		return err
	}
	var bindings []kiloPoolBinding
	for rows.Next() {
		var p kiloPoolBinding
		if err = rows.Scan(&p.ID, &p.GatewayID, &p.SourceID, &p.Revision, &p.State, &p.Identity, &p.TaskID, &p.Resume); err != nil {
			break
		}
		bindings = append(bindings, p)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, p := range bindings {
		source, readErr := s.accounts.GetByID(ctx, p.SourceID)
		if readErr != nil {
			return readErr
		}
		policy, policyErr := kiloReadLifecyclePolicy(ctx, s.db, p.ID)
		if policyErr != nil {
			return policyErr
		}
		if policy.mode != KiloIdentityModeCoupled && p.SourceID != 0 {
			if err = s.retainLegacyKiloLogin(ctx, p, source); err != nil {
				return err
			}
		}
		if policy.mode == KiloIdentityModeCoupled {
			var matchesCurrent bool
			if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(source_account_id=$2,false) FROM pk_kilo_prepared_accounts WHERE id=$1`, p.ID, p.SourceID).Scan(&matchesCurrent); err != nil {
				return err
			}
			if !matchesCurrent && source.IsActive() && !OpenAIAuthRecoveryPending(source) {
				continue
			}
		}
		if source.IsActive() && !OpenAIAuthRecoveryPending(source) {
			if policy.mode == KiloIdentityModeCoupled && p.State != "bound" && p.State != "recovering" {
				continue
			}
			if p.State == "recovering" {
				if err = s.restoreKiloBinding(ctx, p, source); err != nil {
					return err
				}
			}
			continue
		}
		if !OpenAIAuthRecoveryPending(source) && !strings.HasPrefix(source.ErrorMessage, "Workspace deactivated (402):") && (policy.mode != KiloIdentityModeCoupled || source.IsActive()) {
			continue
		}
		if p.State != "recovering" {
			if _, err = s.db.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET binding_state='recovering',binding_error='source_authorization_unavailable',updated_at=NOW() WHERE id=$1 AND binding_revision=$2 AND source_account_id=$3 AND binding_state='bound' AND identity_status='active'`, p.ID, p.Revision, p.SourceID); err != nil {
				return err
			}
		}
		if strings.HasPrefix(source.ErrorMessage, "Workspace deactivated (402):") {
			if err = s.queueKiloUnbind(ctx, p, "workspace_deactivated", ""); err != nil {
				return err
			}
			continue
		}
		recovery, recoveryErr := s.recovery.EnsureKiloSourceRecovery(ctx, p.SourceID)
		if recoveryErr != nil {
			return recoveryErr
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET recovery_task_id=NULLIF($4,0),binding_error=$5,updated_at=NOW() WHERE id=$1 AND binding_revision=$2 AND source_account_id=$3 AND binding_state='recovering'`, p.ID, p.Revision, p.SourceID, recovery.TaskID, recovery.Reason); err != nil {
			return err
		}
		p.ExpectedTokenDigest, p.ExpectedRecoveryGeneration = recovery.AccessTokenDigest, recovery.RecoveryGeneration
		switch recovery.State {
		case "failed":
			if err = s.queueKiloUnbind(ctx, p, recovery.Reason, ""); err != nil {
				return err
			}
		case "blocked":
			if recovery.Reason == "budget_exhausted" || recovery.Reason == "source_inactive" {
				if err = s.queueKiloUnbind(ctx, p, recovery.Reason, ""); err != nil {
					return err
				}
			}
		case "recovered":
			fresh, e := s.accounts.GetByID(ctx, p.SourceID)
			if e != nil {
				return e
			}
			if policy.mode == KiloIdentityModeCoupled && p.State != "bound" && p.State != "recovering" {
				continue
			}
			if err = s.restoreKiloBinding(ctx, p, fresh); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *KiloService) restoreKiloBinding(ctx context.Context, p kiloPoolBinding, source *Account) error {
	operation, unlock, err := s.lockPreparedOperation(ctx, p.ID)
	if err != nil {
		return err
	}
	defer unlock()
	ctx = operation
	var valid bool
	if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_kilo_prepared_accounts WHERE id=$1 AND account_id=$2 AND source_account_id=$3 AND binding_revision=$4 AND binding_state='recovering' AND identity_status='active')`, p.ID, p.GatewayID, p.SourceID, p.Revision).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return nil
	}
	if !source.IsActive() || OpenAIAuthRecoveryPending(source) {
		return nil
	}
	route, err := s.acquireLoginRoute(ctx, p.GatewayID)
	if err != nil {
		return err
	}
	defer route.Close()
	key, org, err := s.preparedCredential(ctx, p.ID)
	if err != nil {
		return err
	}
	connection, err := s.remote.Connection(route.Context, key, org, route.URL)
	if err != nil {
		return err
	}
	if connection == nil {
		return kiloRemoteShapeError()
	}
	if connection.Email != "" && !strings.EqualFold(connection.Email, kiloSourceEmail(source)) {
		return kiloConflict("Kilo远端连接的是其他GPT，已保留槽位等待核对")
	}
	if !connection.Connected {
		// Successful source recovery repairs this same delegated grant. Only a
		// terminal source-login failure can turn an automatic recovery into detach.
		return s.queueKiloRebind(ctx, p, source)
	}
	models, err := s.discoverKiloModels(route.Context, source, key, org, route.URL, nil)
	if err != nil {
		return err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var resume bool
	if err = kiloScan(ctx, tx.Client(), `SELECT binding_resume FROM pk_kilo_prepared_accounts WHERE id=$1 AND binding_revision=$2 AND source_account_id=$3 AND binding_state='recovering' AND identity_status='active' FOR UPDATE`, []any{p.ID, p.Revision, p.SourceID}, &resume); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	txctx := dbent.NewTxContext(ctx, tx)
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return err
	}
	fresh, err := s.accounts.GetByID(txctx, p.SourceID)
	if err != nil {
		return err
	}
	if !fresh.IsActive() || OpenAIAuthRecoveryPending(fresh) || fresh.GetOpenAIAccessToken() != source.GetOpenAIAccessToken() || kiloSourceIdentity(fresh) != kiloSourceIdentity(source) {
		return kiloConflict("来源授权在恢复期间已改变")
	}
	gateway, err := s.accounts.GetByID(txctx, p.GatewayID)
	if err != nil {
		return err
	}
	if gateway.GetCredential("api_key") != key || int64(ParseExtraInt(gateway.Extra["kilo_prepared_id"])) != p.ID {
		return kiloConflict("Kilo身份在恢复期间已改变")
	}
	mapping, _ := kiloOnboardingModelMapping(models)
	gateway.Credentials["model_mapping"] = mapping
	for name, value := range kiloCatalogScopeExtra(gateway, models, time.Now(), fresh) {
		gateway.Extra[name] = value
	}
	if resume && gateway.Status == StatusError && strings.HasPrefix(gateway.ErrorMessage, "Kilo source authorization") {
		gateway.Status, gateway.Schedulable, gateway.ErrorMessage = StatusActive, true, ""
	}
	if err = s.accounts.Update(txctx, gateway); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status=CASE WHEN $4 THEN 'bound' ELSE 'ready' END,binding_state='bound',binding_error='',recovery_task_id=NULL,last_error='',updated_at=NOW() WHERE id=$1 AND binding_revision=$2 AND source_account_id=$3`, p.ID, p.Revision, p.SourceID, gateway.Schedulable); err != nil {
		return err
	}
	return tx.Commit()
}

// KiloConnectionMutator confirms a disconnect using the same identity Key and
// fixed proxy. It must never use the rejected GPT token as a Kilo credential.
type KiloConnectionMutator interface {
	Disconnect(context.Context, string, string, string) error
}

func (s *KiloService) UnbindPrepared(ctx context.Context, id int64, requestID string) error {
	if err := kiloID(requestID); err != nil {
		return err
	}
	var p kiloPoolBinding
	if err := s.db.QueryRowContext(ctx, `SELECT id,account_id,COALESCE(source_account_id,0),binding_revision,binding_state,identity_status FROM pk_kilo_prepared_accounts WHERE id=$1`, id).Scan(&p.ID, &p.GatewayID, &p.SourceID, &p.Revision, &p.State, &p.Identity); err != nil {
		return err
	}
	if p.SourceID == 0 {
		return nil
	}
	return s.queueKiloUnbind(ctx, p, "manual", requestID)
}

func (s *KiloService) queueKiloUnbind(ctx context.Context, p kiloPoolBinding, reason, requestID string) error {
	policy, err := kiloReadLifecyclePolicy(ctx, s.db, p.ID)
	if err != nil {
		return err
	}
	if reason != "manual" && reason != "cancelled_binding_cleanup" && policy.mode == KiloIdentityModeCoupled {
		return s.retireCoupledKiloIdentity(ctx, p, reason)
	}
	if requestID == "" {
		requestID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("kilo-unbind:%d:%d", p.ID, p.Revision))).String()
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var accountID, sourceID, revision, loginID int64
	var state, identity, org string
	err = kiloScan(ctx, tx.Client(), `SELECT account_id,source_account_id,binding_revision,login_proxy_id,binding_state,identity_status,organization_id FROM pk_kilo_prepared_accounts WHERE id=$1 AND source_account_id IS NOT NULL FOR UPDATE`, []any{p.ID}, &accountID, &sourceID, &revision, &loginID, &state, &identity, &org)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if revision != p.Revision || sourceID != p.SourceID || accountID != p.GatewayID {
		return nil
	}
	if identity == "banned" {
		return kiloConflict("Kilo身份已封禁，不能作为空闲槽位复用")
	}
	if state == "unbinding" {
		return nil
	}
	var busy bool
	if err = kiloScan(ctx, tx.Client(), `SELECT EXISTS(SELECT 1 FROM pk_kilo_jobs WHERE prepared_id=$1 AND status IN ('queued','running','awaiting_authorization'))`, []any{p.ID}, &busy); err != nil {
		return err
	}
	if busy {
		return kiloConflict("Kilo身份仍有进行中的任务")
	}
	txctx := dbent.NewTxContext(ctx, tx)
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return err
	}
	gateway, err := s.accounts.GetByID(txctx, accountID)
	if err != nil {
		return err
	}
	source, err := s.accounts.GetByID(txctx, sourceID)
	if err != nil {
		return err
	}
	if int64(ParseExtraInt(gateway.Extra["kilo_prepared_id"])) != p.ID || int64(ParseExtraInt(source.Extra["kilo_source_prepared_id"])) != p.ID {
		return kiloConflict("Kilo与GPT双向绑定已改变")
	}
	if p.ExpectedTokenDigest != "" && (p.ExpectedTokenDigest != OpenAIAccessTokenDigest(source.GetOpenAIAccessToken()) || p.ExpectedRecoveryGeneration != OpenAIAuthRecoveryGeneration(source)) {
		return nil
	}
	if p.ExpectedTokenDigest != "" && source.IsActive() && !OpenAIAuthRecoveryPending(source) {
		return nil
	}
	if reason != "manual" && p.ExpectedTokenDigest == "" && reason == "workspace_deactivated" && !strings.HasPrefix(source.ErrorMessage, "Workspace deactivated (402):") {
		return nil
	}
	gateway.Schedulable = false
	gateway.Status = StatusDisabled
	gateway.ErrorMessage = "Kilo subscription is disconnecting"
	if err = s.accounts.Update(txctx, gateway); err != nil {
		return err
	}
	batchID := uuid.NewString()
	options := `{}`
	hash := kiloHash([]any{"unbind", p.ID, revision, sourceID})
	if _, err = tx.Client().ExecContext(ctx, `INSERT INTO pk_kilo_batches(id,request_id,payload_hash,options) VALUES($1,$2,$3,$4) ON CONFLICT(request_id) DO NOTHING`, batchID, requestID, hash, options); err != nil {
		return err
	}
	var oldHash string
	if err = kiloScan(ctx, tx.Client(), `SELECT id::text,payload_hash FROM pk_kilo_batches WHERE request_id=$1`, []any{requestID}, &batchID, &oldHash); err != nil {
		return err
	}
	if oldHash != hash {
		return kiloConflict("同一请求编号不能解绑其他槽位")
	}
	var jobID int64
	if err = kiloScan(ctx, tx.Client(), `INSERT INTO pk_kilo_jobs(batch_id,job_kind,prepared_id,account_id,source_account_id,source_identity,login_proxy_id,organization_id,binding_revision,stage,options) VALUES($1,'unbind_source',$2,$3,$4,$5,$6,$7,$8,'disconnect_subscription','{}') RETURNING id`, []any{batchID, p.ID, accountID, sourceID, kiloSourceIdentity(source), loginID, org, revision}, &jobID); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status='binding',binding_state='unbinding',binding_job_id=$2,binding_error=$3,updated_at=NOW() WHERE id=$1`, p.ID, jobID, reason); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *KiloService) processKiloUnbind(ctx context.Context, job *kiloClaim) error {
	operation, unlock, err := s.lockPreparedOperation(ctx, job.PreparedID)
	if err != nil {
		return err
	}
	defer unlock()
	ctx = withKiloClaimLease(operation, job)
	if err = s.checkJobOwned(ctx, job); err != nil {
		return err
	}
	var sourceID, revision int64
	var remoteID, email, identity string
	if err = s.db.QueryRowContext(ctx, `SELECT p.source_account_id,p.binding_revision,p.remote_user_id,COALESCE(s.credentials->>'email',s.name),p.identity_status FROM pk_kilo_prepared_accounts p JOIN accounts s ON s.id=p.source_account_id WHERE p.id=$1 AND p.binding_job_id=$2 AND p.binding_state='unbinding'`, job.PreparedID, job.ID).Scan(&sourceID, &revision, &remoteID, &email, &identity); err != nil {
		return err
	}
	if job.SourceID == nil || sourceID != *job.SourceID || revision != job.BindingRevision || (identity != "active" && identity != "retired") {
		return kiloConflict("解绑任务的身份或绑定代次已改变")
	}
	// A manual detach can precede maintenance. Preserve the original Kilo
	// login before clearing the only source that can supply it.
	source, err := s.accounts.GetByID(ctx, sourceID)
	if err != nil {
		return err
	}
	if err = s.retainLegacyKiloLogin(ctx, kiloPoolBinding{ID: job.PreparedID, SourceID: sourceID, Revision: revision}, source); err != nil {
		return err
	}
	route, err := s.acquireLoginRoute(ctx, *job.AccountID)
	if err != nil {
		return err
	}
	defer route.Close()
	key, org, err := s.preparedCredential(ctx, job.PreparedID)
	if err != nil {
		return err
	}
	profile, err := s.remote.Profile(route.Context, key, route.URL)
	if err != nil {
		return err
	}
	if profile == nil || profile.ID != remoteID {
		return kiloConflict("解绑前Kilo身份不一致")
	}
	connection, err := s.remote.Connection(route.Context, key, org, route.URL)
	if err != nil {
		return err
	}
	if connection == nil {
		return kiloRemoteShapeError()
	}
	if connection.State != "disconnected" {
		if connection.Email == "" || !strings.EqualFold(connection.Email, email) {
			return kiloConflict("远端已连接其他GPT身份，保留槽位以防误解绑")
		}
		mutator, ok := s.remote.(KiloConnectionMutator)
		if !ok {
			return infraerrors.ServiceUnavailable("KILO_DISCONNECT_UNAVAILABLE", "Kilo协议解绑暂不可用")
		}
		if err = mutator.Disconnect(route.Context, key, org, route.URL); err != nil {
			return err
		}
		connection, err = s.remote.Connection(route.Context, key, org, route.URL)
		if err != nil {
			return err
		}
		if connection == nil || connection.State != "disconnected" {
			return kiloConflict("Kilo远端解绑尚未确认，槽位保持占用")
		}
	}
	policy, err := kiloReadLifecyclePolicy(ctx, s.db, job.PreparedID)
	if err != nil {
		return err
	}
	if policy.mode == KiloIdentityModeIndependent {
		mutator, ok := s.remote.(KiloIdentityRemote)
		if !ok {
			return kiloBad("KILO_IDENTITY_UNVERIFIED", "Kilo独立登录方式无法复核，槽位保持占用")
		}
		if err = mutator.UnlinkOpenAI(route.Context, key, route.URL); err != nil {
			return err
		}
	}
	if err = s.releaseRequestExitsOwned(ctx, job.PreparedID, false); err != nil {
		return err
	}
	return s.completeKiloUnbind(ctx, job, key)
}

func (s *KiloService) completeKiloUnbind(ctx context.Context, job *kiloClaim, key string) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	txctx := dbent.NewTxContext(ctx, tx)
	if err = LockKiloOnboardingLease(ctx, tx.Client()); err != nil {
		return err
	}
	var sourceID, revision int64
	var mode, identityStatus string
	if err = kiloScan(ctx, tx.Client(), `SELECT source_account_id,binding_revision,identity_mode,identity_status FROM pk_kilo_prepared_accounts WHERE id=$1 AND binding_job_id=$2 AND binding_state='unbinding' AND identity_status IN ('active','retired') FOR UPDATE`, []any{job.PreparedID, job.ID}, &sourceID, &revision, &mode, &identityStatus); err != nil {
		return err
	}
	if job.SourceID == nil || sourceID != *job.SourceID || revision != job.BindingRevision {
		return kiloConflict("绑定代次已改变，旧解绑结果无效")
	}
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return err
	}
	gateway, err := s.accounts.GetByID(txctx, *job.AccountID)
	if err != nil {
		return err
	}
	source, err := s.accounts.GetByID(txctx, sourceID)
	if err != nil {
		return err
	}
	if gateway.GetCredential("api_key") != key || int64(ParseExtraInt(gateway.Extra["kilo_prepared_id"])) != job.PreparedID || int64(ParseExtraInt(source.Extra["kilo_source_prepared_id"])) != job.PreparedID {
		return kiloConflict("Key或双向配对已改变")
	}
	for _, name := range []string{"kilo_source_prepared_id", "kilo_source_gateway_id", "kilo_onboarding_job_id", "kilo_onboarding_proxy_id"} {
		delete(source.Extra, name)
	}
	source.Extra["kilo_quota_only"] = true
	if err = s.accounts.Update(txctx, source); err != nil {
		return err
	}
	clearKiloModelScope(gateway)
	for _, name := range []string{"kilo_onboarding_source_id", "kilo_onboarding_job_id"} {
		delete(gateway.Extra, name)
	}
	gateway.Extra["kilo_binding_revision"] = revision + 1
	gateway.Extra[KiloIdentityModeExtraKey] = mode
	gateway.Status, gateway.Schedulable, gateway.ErrorMessage = StatusDisabled, false, ""
	if mode == KiloIdentityModeCoupled || identityStatus == "retired" {
		gateway.ErrorMessage = "Kilo lifecycle ended with its original GPT source"
	}
	if err = s.accounts.Update(txctx, gateway); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET source_account_id=NULL,status=CASE WHEN identity_mode='gpt_coupled' OR identity_status='retired' THEN 'disabled' ELSE 'ready' END,binding_state=CASE WHEN identity_mode='gpt_coupled' OR identity_status='retired' THEN 'blocked' ELSE 'empty' END,identity_status=CASE WHEN identity_mode='gpt_coupled' THEN 'retired' ELSE identity_status END,binding_revision=binding_revision+1,binding_job_id=NULL,recovery_task_id=NULL,binding_error=CASE WHEN identity_mode='gpt_coupled' OR identity_status='retired' THEN 'source_lifecycle_ended' ELSE '' END,binding_resume=false,last_error='',updated_at=NOW() WHERE id=$1`, job.PreparedID); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='succeeded',stage='unbound',lease_token=NULL,lease_until=NULL,finished_at=NOW(),updated_at=NOW(),last_error='',error_code='' WHERE id=$1 AND lease_token=$2`, job.ID, job.Lease); err != nil {
		return err
	}
	return tx.Commit()
}

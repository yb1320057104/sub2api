package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

const kiloCleanupEgressLock int64 = 70440079

// KiloFailedDeleteResult distinguishes deletion of local failed imports from
// retained remote identities. Retained keys and login pins remain owned.
type KiloFailedDeleteResult struct {
	Status                  string `json:"status"`
	JobID                   int64  `json:"job_id"`
	PreparedID              *int64 `json:"prepared_id,omitempty"`
	DeletedSourceAccountID  *int64 `json:"deleted_source_account_id,omitempty"`
	DeletedGatewayAccountID *int64 `json:"deleted_gateway_account_id,omitempty"`
	IdentityRetained        bool   `json:"identity_retained"`
	Message                 string `json:"message"`
}

type kiloFailedJob struct {
	ID, Revision, BindingRevision                        int64
	PreparedID, SourceID, AccountID, LoginProxyID        *int64
	Kind, Status, Stage, Cipher, Outcome, SourceIdentity string
	CredentialReady, Connected, Reused, LiveLease        bool
}

func readKiloFailedJob(ctx context.Context, q kiloQuerier, id int64, lock bool) (*kiloFailedJob, error) {
	query := `SELECT id,prepared_id,source_account_id,account_id,login_proxy_id,job_kind,status,stage,remote_state_encrypted,remote_outcome,source_identity,revision,binding_revision,credential_ready,subscription_connected,reused_identity,COALESCE(lease_until>NOW(),false) FROM pk_kilo_jobs WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	j := &kiloFailedJob{}
	err := kiloScan(ctx, q, query, []any{id}, &j.ID, &j.PreparedID, &j.SourceID, &j.AccountID, &j.LoginProxyID, &j.Kind, &j.Status, &j.Stage, &j.Cipher, &j.Outcome, &j.SourceIdentity, &j.Revision, &j.BindingRevision, &j.CredentialReady, &j.Connected, &j.Reused, &j.LiveLease)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kiloBad("KILO_JOB_NOT_FOUND", "Kilo任务不存在")
	}
	return j, err
}

func (j *kiloFailedJob) requireFailed() error {
	if (j.Status != "failed" && j.Status != "cancelled") || j.Stage == "deleted" {
		return kiloConflict("仅失败或已取消的任务可删除或更换登录代理")
	}
	if j.LiveLease {
		return kiloConflict("任务仍有执行租约，请等待执行结束")
	}
	if j.Kind != KiloJobOnboardSource && j.Kind != "activate_prepared" {
		return kiloConflict("解绑任务必须先完成远端解绑，不能直接删除")
	}
	if j.Connected {
		return kiloBad("KILO_SUBSCRIPTION_CONNECTED", "订阅已经连接，请先解除订阅绑定再删除")
	}
	return nil
}

// Closing the original receipt precedes local mutation. An unknown operation
// remains recoverable; a successful cancellation never implies no identity.
func (s *KiloService) settleFailedKiloJob(ctx context.Context, id int64) (*kiloFailedJob, error) {
	j, err := readKiloFailedJob(ctx, s.db, id, false)
	if err != nil || j.Status == "cancelled" && j.Stage == "deleted" {
		return j, err
	}
	if err = j.requireFailed(); err != nil {
		return nil, err
	}
	if j.Kind == KiloJobOnboardSource {
		state, readErr := s.readOnboardingState(j.Cipher)
		if readErr != nil {
			return nil, readErr
		}
		// Inspect a terminal executor receipt before its acknowledgement can
		// discard a late identity or subscription result that was never saved.
		if !state.RemoteClosed && state.TaskID != "" {
			if s.executor == nil {
				return nil, kiloBad("KILO_REMOTE_PENDING", "远端执行结果尚未确认，已保留任务和代理占用")
			}
			if err = s.executor.Cancel(ctx, state.TaskID); err != nil {
				return nil, err
			}
			receipt, receiptErr := s.executor.Task(ctx, state.TaskID)
			if receiptErr != nil {
				return nil, receiptErr
			}
			if receiptErr = validateKiloExecutorTask(&kiloClaim{ID: j.ID, Revision: state.Revision}, state, receipt); receiptErr != nil {
				return nil, receiptErr
			}
			if receipt.Status != "failed" && receipt.Status != "cancelled" && receipt.Status != "needs_action" {
				return nil, kiloBad("KILO_REMOTE_PENDING", "远端执行尚未确认失败，请先恢复任务核对身份和订阅")
			}
			if receipt.VerifiedIdentity.KiloUserID != "" && receipt.VerifiedIdentity.KiloUserID != state.RemoteUserID || receipt.BYOKEnabled && !state.BYOKEnabled {
				return nil, kiloBad("KILO_REMOTE_IDENTITY_UNRESOLVED", "远端已返回尚未保存的身份或订阅，请先恢复任务")
			}
		} else if !state.RemoteClosed && state.OperationID != "" && j.Outcome != "failed" && j.Outcome != "cancelled" && j.Outcome != "needs_action" {
			return nil, kiloBad("KILO_REMOTE_PENDING", "远端操作结果不明确，不能删除恢复记录或更换代理")
		}
		if !state.RemoteClosed && (state.OperationID != "" || state.TaskID != "" || state.SessionHandle != "" || state.RemoteLeaseID != "") {
			stopped, closeErr := s.cleanupOnboardingRuntime(ctx, state)
			if closeErr != nil {
				return nil, closeErr
			}
			if !stopped {
				return nil, kiloBad("KILO_REMOTE_PENDING", "远端执行尚未确认关闭，已保留任务和代理占用")
			}
			state.RemoteClosed = true
			state.SessionHandle, state.TaskID, state.OperationID, state.Phase, state.RemoteLeaseID = "", "", "", "", ""
			state.ExpiresAt, state.DeadlineAt, state.AckTaskIDs = time.Time{}, time.Time{}, nil
			raw, encodeErr := json.Marshal(state)
			if encodeErr != nil {
				return nil, encodeErr
			}
			cipher, encodeErr := s.encryptor.Encrypt(string(raw))
			if encodeErr != nil {
				return nil, encodeErr
			}
			updated, updateErr := s.db.ExecContext(ctx, `UPDATE pk_kilo_jobs SET remote_state_encrypted=$5,remote_outcome='cleaned',updated_at=NOW() WHERE id=$1 AND revision=$2 AND status=$3 AND remote_state_encrypted=$4`, j.ID, j.Revision, j.Status, j.Cipher, cipher)
			if updateErr != nil {
				return nil, updateErr
			}
			if n, affectedErr := updated.RowsAffected(); affectedErr != nil {
				return nil, affectedErr
			} else if n != 1 {
				return nil, kiloConflict("任务在远端清理期间已改变，请刷新后重试")
			}
			j.Cipher, j.Outcome = cipher, "cleaned"
		}
	}
	if err = j.requireFailed(); err != nil {
		return nil, err
	}
	state, err := s.readOnboardingState(j.Cipher)
	if err != nil {
		return nil, err
	}
	if !state.RemoteClosed && (state.OperationID != "" || state.TaskID != "" || state.SessionHandle != "" || state.RemoteLeaseID != "") {
		return nil, kiloBad("KILO_REMOTE_PENDING", "远端执行尚未确认关闭，已保留任务和代理占用，请稍后重试")
	}
	return j, nil
}

type kiloFailedPrepared struct {
	ID, Revision               int64
	AccountID, SourceID, JobID *int64
	OriginID                   *int64
	Cipher, RemoteID, Binding  string
}

func readKiloFailedPrepared(ctx context.Context, q kiloQuerier, id int64, lock bool) (*kiloFailedPrepared, error) {
	query := `SELECT id,account_id,source_account_id,binding_job_id,binding_revision,identity_source_account_id,credential_encrypted,remote_user_id,binding_state FROM pk_kilo_prepared_accounts WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	p := &kiloFailedPrepared{}
	err := kiloScan(ctx, q, query, []any{id}, &p.ID, &p.AccountID, &p.SourceID, &p.JobID, &p.Revision, &p.OriginID, &p.Cipher, &p.RemoteID, &p.Binding)
	return p, err
}

func kiloSameID(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (p *kiloFailedPrepared) ownedBy(j *kiloFailedJob) bool {
	return p != nil && kiloSameID(p.AccountID, j.AccountID) && kiloSameID(p.SourceID, j.SourceID) &&
		(p.JobID != nil && *p.JobID == j.ID && (j.BindingRevision == 0 || p.Revision == j.BindingRevision) ||
			p.JobID == nil && j.BindingRevision > 0 && p.Revision == j.BindingRevision)
}

func kiloFailureHasIdentity(j *kiloFailedJob, p *kiloFailedPrepared, state *kiloOnboardingRuntime, gateway *Account) bool {
	return j.CredentialReady || j.Reused || p != nil && (p.Cipher != "" || p.RemoteID != "" || p.OriginID != nil) ||
		state.RemoteUserID != "" || state.DeviceApproved || state.BYOKEnabled || state.ConnectionSubject != "" || state.IdentitySessionMaterial != "" || state.IdentityCredential != nil ||
		gateway != nil && gateway.GetCredential("api_key") != ""
}

// A repository miss is not evidence of deletion: only the same persisted row
// with a deletion timestamp may be omitted from account mutations.
func requireKiloDeletedAccount(ctx context.Context, q kiloQuerier, id int64) error {
	var storedID int64
	var deletedAt sql.NullTime
	err := kiloScan(ctx, q, `SELECT id,deleted_at FROM accounts WHERE id=$1`, []any{id}, &storedID, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAccountNotFound
	}
	if err != nil {
		return err
	}
	if storedID != id || !deletedAt.Valid {
		return ErrAccountNotFound
	}
	return nil
}

func (s *KiloService) readKiloFailedGateway(ctx context.Context, q kiloQuerier, j *kiloFailedJob) (*Account, bool, error) {
	id := *j.AccountID
	account, err := s.accounts.GetByID(ctx, id)
	if !errors.Is(err, ErrAccountNotFound) {
		return account, false, err
	}
	account = &Account{}
	var deletedAt sql.NullTime
	var credentials, extra []byte
	var remoteMaterial bool
	err = kiloScan(ctx, q, `SELECT a.id,a.deleted_at,a.platform,a.type,a.parent_account_id,a.credentials,a.extra,a.schedulable,EXISTS(SELECT 1 FROM pk_kilo_prepared_accounts p WHERE p.account_id=a.id) OR EXISTS(SELECT 1 FROM pk_kilo_device_sessions d WHERE d.account_id=a.id AND d.token_encrypted<>'') FROM accounts a WHERE a.id=$1`, []any{id}, &account.ID, &deletedAt, &account.Platform, &account.Type, &account.ParentAccountID, &credentials, &extra, &account.Schedulable, &remoteMaterial)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrAccountNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if account.ID != id || !deletedAt.Valid {
		return nil, false, ErrAccountNotFound
	}
	if err = json.Unmarshal(credentials, &account.Credentials); err != nil {
		return nil, false, err
	}
	if err = json.Unmarshal(extra, &account.Extra); err != nil {
		return nil, false, err
	}
	// Bootstrap routing metadata is created before authentication. Any other
	// credential or persisted identity must keep its key and proxy ownership.
	for key, value := range account.Credentials {
		if key == "base_url" || key == "kilo_machine_id" || key == "kilo_organization_id" {
			continue
		}
		if value != nil && value != "" {
			remoteMaterial = true
		}
	}
	for _, key := range []string{"kilo_prepared_id", "kilo_binding_revision", KiloIdentityModeExtraKey, KiloIdentitySourceExtraKey} {
		if value := account.Extra[key]; value != nil && value != "" {
			remoteMaterial = true
		}
	}
	if remoteMaterial {
		return nil, false, kiloBad("KILO_REMOTE_IDENTITY_UNRESOLVED", "已删除网关仍有远端身份材料，已保留任务和代理占用")
	}
	claim := &kiloClaim{ID: j.ID, SourceID: j.SourceID, AccountID: j.AccountID, LoginProxyID: j.LoginProxyID}
	if !kiloOnboardingIdentityMatches(account, claim) {
		return nil, false, kiloConflict("失败网关不再属于该任务，不能删除")
	}
	return nil, true, nil
}

// DeleteFailedPrepared resolves the currently owned failed job. A historical
// failed job cannot delete a later successful binding of this same identity.
func (s *KiloService) DeleteFailedPrepared(ctx context.Context, id int64) (*KiloFailedDeleteResult, error) {
	var jobID int64
	err := s.db.QueryRowContext(ctx, `SELECT j.id FROM pk_kilo_prepared_accounts p JOIN pk_kilo_jobs j ON j.prepared_id=p.id WHERE p.id=$1 AND (j.id=p.binding_job_id OR p.binding_job_id IS NULL) AND j.status IN ('failed','cancelled') ORDER BY j.id DESC LIMIT 1`, id).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kiloConflict("预备账号没有可删除的失败任务，请先解除已连接订阅")
	}
	if err != nil {
		return nil, err
	}
	return s.DeleteFailedJob(ctx, jobID)
}

func (s *KiloService) DeleteFailedJob(ctx context.Context, id int64) (*KiloFailedDeleteResult, error) {
	j, err := s.settleFailedKiloJob(ctx, id)
	if err != nil {
		return nil, err
	}
	result := &KiloFailedDeleteResult{Status: "deleted", JobID: id, PreparedID: j.PreparedID, Message: "失败任务已删除，审计历史保留"}
	if j.Stage == "deleted" {
		state, stateErr := s.readOnboardingState(j.Cipher)
		if stateErr != nil {
			return nil, stateErr
		}
		result.IdentityRetained = j.PreparedID != nil || kiloFailureHasIdentity(j, nil, state, nil)
		if result.IdentityRetained {
			result.Message = "失败的本地管理关联已删除；远端Kilo身份、原Key和固定登录代理占用保留"
		}
		return result, nil
	}
	var p *kiloFailedPrepared
	var unlock func()
	if j.PreparedID != nil {
		ctx, unlock, err = s.lockPreparedOperation(ctx, *j.PreparedID)
		if err != nil {
			return nil, err
		}
		defer unlock()
		p, err = readKiloFailedPrepared(ctx, s.db, *j.PreparedID, false)
		if err != nil {
			return nil, err
		}
	}
	state, err := s.readOnboardingState(j.Cipher)
	if err != nil {
		return nil, err
	}
	var gateway *Account
	var gatewayDeleted bool
	if j.AccountID != nil {
		gateway, gatewayDeleted, err = s.readKiloFailedGateway(ctx, s.db, j)
		if err != nil {
			return nil, err
		}
	}
	identity := kiloFailureHasIdentity(j, p, state, gateway)
	if gatewayDeleted && identity {
		return nil, kiloBad("KILO_REMOTE_IDENTITY_UNRESOLVED", "远端身份关联的网关已删除，已保留任务和代理占用")
	}
	ownedPrepared := p != nil && p.ownedBy(j)
	// Verify only this job's current binding. A stale failed card must not
	// contact, disconnect or mutate the replacement binding.
	if identity && (p == nil || ownedPrepared) {
		if gateway == nil || gateway.GetCredential("api_key") == "" {
			return nil, kiloBad("KILO_REMOTE_IDENTITY_UNRESOLVED", "已获得远端身份但凭据尚未保存，请恢复任务后核对订阅再删除")
		}
		route, routeErr := s.acquireLoginRoute(ctx, gateway.ID)
		if routeErr != nil {
			return nil, routeErr
		}
		key, org := gateway.GetCredential("api_key"), gateway.GetCredential("kilo_organization_id")
		connection, checkErr := s.remote.Connection(route.Context, key, org, route.URL)
		route.Close()
		if checkErr != nil {
			return nil, checkErr
		}
		if connection == nil || connection.Connected || connection.State != "disconnected" {
			return nil, kiloBad("KILO_SUBSCRIPTION_CONNECTED", "远端订阅尚未确认解除，请先解除订阅绑定再删除")
		}
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return nil, err
	}
	fresh, err := readKiloFailedJob(ctx, tx.Client(), id, true)
	if err != nil {
		return nil, err
	}
	if fresh.Status == "cancelled" && fresh.Stage == "deleted" {
		result.IdentityRetained = identity
		return result, nil
	}
	if err = fresh.requireFailed(); err != nil {
		return nil, err
	}
	if fresh.Revision != j.Revision || fresh.Cipher != j.Cipher || !kiloSameID(fresh.PreparedID, j.PreparedID) || !kiloSameID(fresh.AccountID, j.AccountID) || !kiloSameID(fresh.SourceID, j.SourceID) {
		return nil, kiloConflict("任务在清理期间已改变，请刷新后重试")
	}
	if p != nil {
		current, readErr := readKiloFailedPrepared(ctx, tx.Client(), p.ID, true)
		if readErr != nil {
			return nil, readErr
		}
		if current.Revision != p.Revision || current.Cipher != p.Cipher || current.RemoteID != p.RemoteID || !kiloSameID(current.JobID, p.JobID) || !kiloSameID(current.SourceID, p.SourceID) || !kiloSameID(current.AccountID, p.AccountID) {
			return nil, kiloConflict("Kilo身份或订阅归属在清理期间已改变")
		}
	}
	txctx := dbent.NewTxContext(ctx, tx)
	if err = s.lockKiloFailedAccounts(txctx, tx, j); err != nil {
		return nil, err
	}
	if err = checkKiloFailedBusy(ctx, tx.Client(), j); err != nil {
		return nil, err
	}
	var source *Account
	if j.SourceID != nil {
		source, err = s.accounts.GetByID(txctx, *j.SourceID)
		if err != nil {
			if !errors.Is(err, ErrAccountNotFound) {
				return nil, err
			}
			if err = requireKiloDeletedAccount(ctx, tx.Client(), *j.SourceID); err != nil {
				return nil, err
			}
			source = nil
		}
	}
	if j.AccountID != nil {
		current, currentDeleted, readErr := s.readKiloFailedGateway(txctx, tx.Client(), j)
		if readErr != nil {
			return nil, readErr
		}
		if currentDeleted != gatewayDeleted || !currentDeleted && (gateway == nil || current.GetCredential("api_key") != gateway.GetCredential("api_key") || ParseExtraInt(current.Extra[KiloLoginProxyExtraKey]) != ParseExtraInt(gateway.Extra[KiloLoginProxyExtraKey]) || current.Schedulable) {
			return nil, kiloConflict("网关身份或调度状态已改变，不能删除")
		}
		gateway = current
	}
	if source != nil && int64(ParseExtraInt(source.Extra["kilo_onboarding_job_id"])) == id {
		prepared := int64(ParseExtraInt(source.Extra["kilo_source_prepared_id"]))
		gatewayID := int64(ParseExtraInt(source.Extra["kilo_source_gateway_id"]))
		if prepared != 0 && (j.PreparedID == nil || prepared != *j.PreparedID) || gatewayID != 0 && (j.AccountID == nil || gatewayID != *j.AccountID) {
			return nil, kiloConflict("来源已经绑定其他Kilo身份，未执行删除")
		}
	}
	if p != nil && ownedPrepared {
		if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status='disabled',binding_state='blocked',source_account_id=NULL,binding_job_id=NULL,binding_revision=binding_revision+1,recovery_task_id=NULL,binding_resume=false,binding_error='local_deleted',last_error='kilo_deleted:failed_job',routing_options=jsonb_set(routing_options,'{management_deleted}','true'::jsonb,true),updated_at=NOW() WHERE id=$1`, p.ID); err != nil {
			return nil, err
		}
	}
	result.IdentityRetained = identity
	if gateway != nil && identity && ownedPrepared {
		delete(gateway.Extra, "kilo_onboarding_source_id")
		delete(gateway.Extra, "kilo_onboarding_job_id")
		gateway.Status, gateway.Schedulable = StatusDisabled, false
		if err = s.accounts.Update(txctx, gateway); err != nil {
			return nil, err
		}
	}
	if gatewayDeleted {
		if err = removeUnusedKiloRoutes(ctx, tx.Client(), *j.AccountID); err != nil {
			return nil, err
		}
	}
	if gateway != nil && !identity {
		claim := &kiloClaim{ID: j.ID, SourceID: j.SourceID, AccountID: j.AccountID, LoginProxyID: j.LoginProxyID}
		if !kiloOnboardingIdentityMatches(gateway, claim) {
			return nil, kiloConflict("失败网关不再属于该任务，不能删除")
		}
		if err = removeUnusedKiloRoutes(ctx, tx.Client(), gateway.ID); err != nil {
			return nil, err
		}
		delete(gateway.Extra, KiloLoginProxyExtraKey)
		if err = s.accounts.Update(txctx, gateway); err != nil {
			return nil, err
		}
		if err = s.accounts.Delete(txctx, gateway.ID); err != nil {
			return nil, err
		}
		result.DeletedGatewayAccountID = j.AccountID
	}
	if source != nil && (p == nil || ownedPrepared) && int64(ParseExtraInt(source.Extra["kilo_onboarding_job_id"])) == id {
		var disposable bool
		if err = kiloScan(ctx, tx.Client(), `SELECT EXISTS(SELECT 1 FROM pk_kilo_import_requests WHERE result->'items' @> jsonb_build_array(jsonb_build_object('source_account_id',$1::bigint,'job_id',$2::bigint))) AND NOT EXISTS(SELECT 1 FROM pk_kilo_prepared_accounts WHERE identity_source_account_id=$1 OR source_account_id=$1) AND NOT EXISTS(SELECT 1 FROM accounts WHERE parent_account_id=$1 AND deleted_at IS NULL) AND NOT EXISTS(SELECT 1 FROM usage_logs WHERE account_id=$1)`, []any{source.ID, id}, &disposable); err != nil {
			return nil, err
		}
		for _, key := range []string{"kilo_onboarding_job_id", "kilo_onboarding_proxy_id", "kilo_source_prepared_id", "kilo_source_gateway_id"} {
			delete(source.Extra, key)
		}
		source.Extra["kilo_quota_only"] = true
		if err = s.accounts.Update(txctx, source); err != nil {
			return nil, err
		}
		if disposable && !identity && !source.Schedulable && (p == nil || p.OriginID == nil || *p.OriginID != source.ID) {
			if err = s.accounts.Delete(txctx, source.ID); err != nil {
				return nil, err
			}
			result.DeletedSourceAccountID = j.SourceID
		}
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='cancelled',stage='deleted',revision=revision+1,lease_token=NULL,lease_until=NULL,verification_url='',user_code='',next_action='',finished_at=COALESCE(finished_at,NOW()),updated_at=NOW() WHERE id=$1`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if identity {
		result.Message = "失败的本地管理关联已删除；远端Kilo身份、原Key和固定登录代理占用保留"
	}
	return result, nil
}

func (s *KiloService) lockKiloFailedAccounts(ctx context.Context, tx *dbent.Tx, j *kiloFailedJob) error {
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return errors.New("账号事务锁不可用")
	}
	if err := locker.LockAccountMutation(ctx); err != nil {
		return err
	}
	if _, err := tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloCleanupEgressLock); err != nil {
		return err
	}
	rows, err := tx.Client().QueryContext(ctx, `SELECT id FROM accounts WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`, j.SourceID, j.AccountID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	return nil
}

func checkKiloFailedBusy(ctx context.Context, q kiloQuerier, j *kiloFailedJob) error {
	var busy bool
	err := kiloScan(ctx, q, `SELECT EXISTS(SELECT 1 FROM pk_kilo_jobs WHERE id<>$1 AND status IN ('queued','running','awaiting_authorization') AND (source_account_id=$2 OR account_id=$3 OR prepared_id=$4)) OR EXISTS(SELECT 1 FROM openai_oauth_reauth_tasks WHERE account_id=$2 AND status IN ('queued','running','callback_processing')) OR EXISTS(SELECT 1 FROM pk_kilo_egress_leases WHERE account_id IN ($2,$3)) OR EXISTS(SELECT 1 FROM pk_kilo_device_sessions WHERE account_id=$3 AND status='approved')`, []any{j.ID, j.SourceID, j.AccountID, j.PreparedID}, &busy)
	if err != nil {
		return err
	}
	if busy {
		return kiloConflict("关联账号仍有活跃任务或未排空的请求租约，请等待结束")
	}
	return nil
}

func removeUnusedKiloRoutes(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, accountID int64) error {
	if _, err := q.ExecContext(ctx, `UPDATE pk_kilo_device_sessions SET status='cancelled',code_encrypted='',token_encrypted='',updated_at=NOW() WHERE account_id=$1 AND status IN ('pending','approved')`, accountID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM pk_kilo_egress_routes WHERE account_id=$1 AND NOT EXISTS(SELECT 1 FROM pk_kilo_egress_leases WHERE account_id=$1)`, accountID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `DELETE FROM pk_kilo_proxy_owners o WHERE o.account_id=$1 AND NOT EXISTS(SELECT 1 FROM pk_kilo_egress_routes r WHERE r.credential_fingerprint=o.credential_fingerprint OR (r.credential_fingerprint IS NULL AND r.account_id=o.account_id))`, accountID)
	return err
}

// SetFailedJobLoginProxy changes only an unmaterialized Kilo identity. Public
// endpoint preflight occurs before any account pin or transaction is written.
func (s *KiloService) SetFailedJobLoginProxy(ctx context.Context, id, proxyID int64) error {
	j, err := s.settleFailedKiloJob(ctx, id)
	if err != nil {
		return err
	}
	if (j.Status != "failed" && j.Status != "cancelled") || j.Kind != KiloJobOnboardSource || j.PreparedID != nil || j.Reused || j.CredentialReady || j.Connected {
		return kiloConflict("已经建立Kilo身份的固定登录代理不能更换")
	}
	state, err := s.readOnboardingState(j.Cipher)
	if err != nil {
		return err
	}
	if kiloFailureHasIdentity(j, nil, state, nil) {
		return kiloConflict("已经取得远端Kilo身份，原固定代理必须保留")
	}
	proxy, err := s.proxies.GetByID(ctx, proxyID)
	if err != nil {
		return err
	}
	preflight, err := s.PreflightLoginProxy(ctx, proxyID)
	if err != nil {
		return err
	}
	if !preflight.Reachable {
		return kiloBad("KILO_LOGIN_PROXY_UNREACHABLE", "登录代理无法连接认证服务，原代理未修改")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	fresh, err := readKiloFailedJob(ctx, tx.Client(), id, true)
	if err != nil {
		return err
	}
	if err = fresh.requireFailed(); err != nil {
		return err
	}
	if fresh.Revision != j.Revision || fresh.Cipher != j.Cipher || fresh.PreparedID != nil || fresh.CredentialReady || fresh.Reused || !kiloSameID(fresh.LoginProxyID, j.LoginProxyID) {
		return kiloConflict("预检期间任务或身份已改变，请刷新后重试")
	}
	txctx := dbent.NewTxContext(ctx, tx)
	if err = s.lockKiloFailedAccounts(txctx, tx, j); err != nil {
		return err
	}
	if err = checkKiloFailedBusy(ctx, tx.Client(), j); err != nil {
		return err
	}
	var lockedID int64
	if err = kiloScan(ctx, tx.Client(), `SELECT id FROM proxies WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, []any{proxyID}, &lockedID); err != nil {
		return err
	}
	var occupied bool
	if err = kiloScan(ctx, tx.Client(), `SELECT EXISTS(SELECT 1 FROM accounts a JOIN proxies owned ON a.extra->'kilo_login_proxy_id'=to_jsonb(owned.id) JOIN proxies selected ON selected.id=$1 WHERE a.extra->'kilo_managed'='true'::jsonb AND a.id IS DISTINCT FROM $2::bigint AND pk_kilo_proxy_credential_fingerprint(owned.host,owned.port,owned.username,owned.password)=pk_kilo_proxy_credential_fingerprint(selected.host,selected.port,selected.username,selected.password)) OR EXISTS(SELECT 1 FROM pk_kilo_proxy_owners o JOIN proxies selected ON selected.id=$1 WHERE o.account_id IS DISTINCT FROM $2::bigint AND o.credential_fingerprint=pk_kilo_proxy_credential_fingerprint(selected.host,selected.port,selected.username,selected.password) AND EXISTS(SELECT 1 FROM pk_kilo_egress_routes r WHERE r.credential_fingerprint=o.credential_fingerprint OR (r.credential_fingerprint IS NULL AND (o.account_id IS NULL OR r.account_id=o.account_id))))`, []any{proxyID, j.AccountID}, &occupied); err != nil {
		return err
	}
	if occupied {
		return ErrKiloProxyOwned
	}
	current, err := s.proxies.GetByID(txctx, proxyID)
	if err != nil {
		return err
	}
	if !usableAssignedAuthProxy(current) || kiloOnboardingProxyHash(current.URL()) != kiloOnboardingProxyHash(proxy.URL()) {
		return kiloConflict("预检期间登录代理配置已改变，请重新预检")
	}
	source, err := s.accounts.GetByID(txctx, *j.SourceID)
	if err != nil {
		return err
	}
	if source.Extra["kilo_quota_only"] != true || int64(ParseExtraInt(source.Extra["kilo_onboarding_job_id"])) != j.ID || ParseExtraInt(source.Extra["kilo_source_prepared_id"]) != 0 || ParseExtraInt(source.Extra["kilo_source_gateway_id"]) != 0 || j.LoginProxyID == nil {
		return kiloConflict("来源归属或绑定已改变，不能更换登录代理")
	}
	old := *j.LoginProxyID
	for _, binding := range source.ProxyBindings {
		if binding.ProxyID != old {
			return kiloConflict("来源仍有其他代理绑定，不能改写其固定出口")
		}
	}
	if source.ProxyID != nil && *source.ProxyID != old || ParseExtraInt(source.Extra[assignedAuthProxyExtraKey]) != 0 && int64(ParseExtraInt(source.Extra[assignedAuthProxyExtraKey])) != old {
		return kiloConflict("来源存在其他固定登录代理，请保留原归属")
	}
	if j.AccountID != nil {
		gateway, readErr := s.accounts.GetByID(txctx, *j.AccountID)
		if readErr != nil {
			return readErr
		}
		claim := &kiloClaim{ID: j.ID, SourceID: j.SourceID, LoginProxyID: j.LoginProxyID}
		if !kiloOnboardingIdentityMatches(gateway, claim) || gateway.GetCredential("api_key") != "" {
			return kiloConflict("网关已经取得身份或归属改变，原代理必须保留")
		}
		if err = removeUnusedKiloRoutes(ctx, tx.Client(), gateway.ID); err != nil {
			return err
		}
		gateway.Extra[KiloLoginProxyExtraKey] = proxyID
		if err = s.accounts.Update(txctx, gateway); err != nil {
			return err
		}
	}
	if source.ProxyID != nil || len(source.ProxyBindings) > 0 {
		source.ProxyID = &proxyID
	}
	if ParseExtraInt(source.Extra[assignedAuthProxyExtraKey]) != 0 {
		source.Extra[assignedAuthProxyExtraKey] = proxyID
	}
	source.Extra["kilo_onboarding_proxy_id"] = proxyID
	if err = s.accounts.Update(txctx, source); err != nil {
		return err
	}
	var proxySource string
	var configuredProxy *int64
	if err = kiloScan(ctx, tx.Client(), `SELECT proxy_source,proxy_id FROM openai_oauth_reauth_configs WHERE account_id=$1 FOR UPDATE`, []any{source.ID}, &proxySource, &configuredProxy); err != nil {
		return err
	}
	if proxySource == OpenAIOAuthReauthProxySourceManagedProxy {
		if configuredProxy == nil || *configuredProxy != old {
			return kiloConflict("官方登录配置已指定其他代理，请保留原归属")
		}
		if _, err = tx.Client().ExecContext(ctx, `UPDATE openai_oauth_reauth_configs SET proxy_id=$2,updated_at=NOW() WHERE account_id=$1`, source.ID, proxyID); err != nil {
			return err
		}
	} else if proxySource != OpenAIOAuthReauthProxySourceAccount {
		return kiloConflict("官方登录配置并非账号固定代理，不能在Kilo任务中更换")
	}
	material, err := s.kiloMaterialVersionTx(ctx, tx, source.ID)
	if err != nil {
		return err
	}
	state.DeviceSessionID, state.DeviceVerificationURL, state.DeviceUserCode = "", "", ""
	state.ProxyHash, state.Revision = kiloOnboardingProxyHash(current.URL()), j.Revision+1
	state.SourceEmail, state.InitialSourceIdentity = kiloSourceEmail(source), kiloSourceIdentity(source)
	state.Failures, state.DeadlineAt = 0, time.Time{}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	cipher, err := s.encryptor.Encrypt(string(raw))
	if err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_jobs SET login_proxy_id=$2,revision=revision+1,quota_task_id=NULL,quota_oauth_ready=false,stage='quota_oauth',material_version=$3,source_identity=$4,remote_state_encrypted=$5,lease_token=NULL,lease_until=NULL,updated_at=NOW() WHERE id=$1`, j.ID, proxyID, material.Version, kiloSourceIdentity(source), cipher); err != nil {
		return err
	}
	return tx.Commit()
}

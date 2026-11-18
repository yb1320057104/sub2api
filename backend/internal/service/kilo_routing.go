package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

const KiloLoginProxyExtraKey = "kilo_login_proxy_id"

func kiloRoutingJSON(input KiloBatchInput) []byte {
	// These scalar options cannot contain values unsupported by encoding/json.
	raw, _ := json.Marshal(KiloRoutingOptions{ProxyGroupID: input.ProxyGroupID, ProxiesPerAccount: input.ProxiesPerAccount, ProxyConcurrency: input.ProxyConcurrency, AccountConcurrency: input.AccountConcurrency, Priority: input.Priority, AutoReleaseStatuses: input.AutoReleaseStatuses})
	return raw
}

// This Kilo-only value selects the normal candidate pool without a group filter.
// Zero remains an unselected pool, not a request to widen allocation.
const kiloAllRequestProxies int64 = -1

func validKiloRequestProxyPool(id int64) bool {
	return id == kiloAllRequestProxies || id > 0
}

func normalizeKiloRouting(options *KiloRoutingOptions) error {
	if options.ProxiesPerAccount == 0 {
		options.ProxiesPerAccount = 5
	}
	if options.ProxyConcurrency == 0 {
		options.ProxyConcurrency = 20
	}
	if options.AccountConcurrency == 0 {
		options.AccountConcurrency = options.ProxiesPerAccount * options.ProxyConcurrency
	}
	if options.AutoReleaseStatuses == nil {
		options.AutoReleaseStatuses = []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden}
	}
	if (options.ProxyGroupID != 0 && !validKiloRequestProxyPool(options.ProxyGroupID)) || options.ProxiesPerAccount < 1 || options.ProxiesPerAccount > 100 || options.ProxyConcurrency < 1 || options.ProxyConcurrency > 10000 || options.AccountConcurrency < 1 || options.AccountConcurrency > 10000 || options.Priority < 0 || options.Priority > 10000 {
		return kiloBad("KILO_ROUTING_INVALID", "请求出口数量、每IP并发、账号并发或优先级无效")
	}
	seen := map[int]bool{}
	for _, status := range options.AutoReleaseStatuses {
		if status < 400 || status > 599 || seen[status] {
			return kiloBad("KILO_RELEASE_STATUS", "自动释放状态码须为不重复的400至599整数")
		}
		seen[status] = true
	}
	return nil
}

func (s *KiloService) SetRouting(ctx context.Context, id int64, input KiloRoutingInput) error {
	if input.LoginProxyID == nil || *input.LoginProxyID <= 0 {
		return kiloBad("KILO_LOGIN_PROXY_REQUIRED", "请选择固定的登录代理")
	}
	if err := normalizeKiloRouting(&input.KiloRoutingOptions); err != nil {
		return err
	}
	if !validKiloRequestProxyPool(input.ProxyGroupID) {
		return kiloBad("KILO_PROXY_GROUP_REQUIRED", "请选择模型请求代理范围")
	}
	operation, unlock, err := s.lockPreparedOperation(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	ctx = operation
	proxy, err := s.proxies.GetByID(ctx, *input.LoginProxyID)
	if err != nil {
		return err
	}
	if !proxy.IsActive() || proxy.IsExpired(time.Now()) {
		return kiloBad("KILO_PROXY_INVALID", "登录代理已停用或过期")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var status string
	var accountID, oldProxyID, bindingJobID *int64
	if err = kiloScan(ctx, tx.Client(), `SELECT status,account_id,login_proxy_id,binding_job_id FROM pk_kilo_prepared_accounts WHERE id=$1 FOR UPDATE`, []any{id}, &status, &accountID, &oldProxyID, &bindingJobID); err != nil {
		return err
	}
	if status != "ready" && (status != "binding" || bindingJobID == nil || accountID == nil) {
		return kiloConflict("请先停止并释放当前配对，再修改固定登录代理或请求配置")
	}
	if oldProxyID != nil && *oldProxyID != *input.LoginProxyID {
		return kiloConflict("已有登录身份固定在原代理；请保留该代理，不能在已登录身份上切换登录IP")
	}
	if accountID != nil {
		account, readErr := s.accounts.GetByID(dbent.NewTxContext(ctx, tx), *accountID)
		if readErr != nil {
			return readErr
		}
		if account.Schedulable || account.Extra[KiloManagedExtraKey] != true {
			return kiloConflict("账号仍在调度或归属已改变")
		}
		if status == "binding" {
			if oldProxyID == nil || *oldProxyID != *input.LoginProxyID {
				return kiloConflict("已授权任务必须保留原固定登录代理")
			}
			if err = s.updateFailedOnboardingRouting(ctx, tx, id, *bindingJobID, account, input); err != nil {
				return err
			}
		} else {
			account.Extra[KiloLoginProxyExtraKey] = *input.LoginProxyID
			if err = s.accounts.Update(dbent.NewTxContext(ctx, tx), account); err != nil {
				return err
			}
		}
	}
	raw, err := json.Marshal(input.KiloRoutingOptions)
	if err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET login_proxy_id=$2,routing_options=$3::jsonb,updated_at=NOW() WHERE id=$1`, id, *input.LoginProxyID, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

// Routing recovery is limited to a closed, fully authorized onboarding job.
// It preserves the credential, subscription receipt, failure and revision;
// the ordinary retry entry later resumes credential verification only.
func (s *KiloService) updateFailedOnboardingRouting(ctx context.Context, tx *dbent.Tx, preparedID, jobID int64, account *Account, input KiloRoutingInput) error {
	job, err := readKiloFailedJob(ctx, tx.Client(), jobID, true)
	if err != nil {
		return err
	}
	if job.Kind != KiloJobOnboardSource || job.Status != "failed" || job.Stage != "subscription_verified" || job.LiveLease || !job.CredentialReady || !job.Connected || job.PreparedID == nil || *job.PreparedID != preparedID || job.AccountID == nil || *job.AccountID != account.ID || job.SourceID == nil || job.LoginProxyID == nil || *job.LoginProxyID != *input.LoginProxyID || account.GetCredential("api_key") == "" || int64(ParseExtraInt(account.Extra["kilo_prepared_id"])) != preparedID {
		return kiloConflict("仅已完成授权与取钥的路由容量失败任务可调整请求出口")
	}
	state, err := s.readOnboardingState(job.Cipher)
	if err != nil {
		return err
	}
	if !state.RemoteClosed || state.Revision != job.Revision || state.RemoteUserID == "" || state.OperationID != "" || state.TaskID != "" || state.Phase != "" || state.SessionHandle != "" || state.RemoteLeaseID != "" || state.ReconcileSubscription || state.ReauthorizeSubscription {
		return kiloConflict("原授权操作尚未闭合，不能修改请求出口")
	}
	claim := &kiloClaim{ID: job.ID, SourceID: job.SourceID, LoginProxyID: job.LoginProxyID}
	if !kiloOnboardingIdentityMatches(account, claim) {
		return kiloConflict("已授权网关与原任务的身份归属不一致")
	}
	var raw []byte
	var code, organization string
	var quotaReady bool
	if err = kiloScan(ctx, tx.Client(), `SELECT options,error_code,organization_id,quota_oauth_ready FROM pk_kilo_jobs WHERE id=$1`, []any{jobID}, &raw, &code, &organization, &quotaReady); err != nil {
		return err
	}
	if code != "PROXY_POOL_CAPACITY" || !quotaReady {
		return kiloConflict("仅请求代理容量不足的已授权任务可修改路由")
	}
	if err = validateKiloSubscriptionReceipt(&kiloClaim{OrganizationID: organization}, state); err != nil {
		return err
	}
	var busy bool
	if err = kiloScan(ctx, tx.Client(), `SELECT EXISTS(SELECT 1 FROM pk_kilo_jobs WHERE id<>$1 AND status IN ('queued','running','awaiting_authorization') AND (source_account_id=$2 OR account_id=$3 OR prepared_id=$4)) OR EXISTS(SELECT 1 FROM openai_oauth_reauth_tasks WHERE account_id=$2 AND status IN ('queued','running','callback_processing')) OR EXISTS(SELECT 1 FROM pk_kilo_egress_leases WHERE account_id IN ($2,$3))`, []any{jobID, job.SourceID, account.ID, preparedID}, &busy); err != nil {
		return err
	}
	if busy {
		return kiloConflict("关联账号仍有活跃操作，请等待结束后调整请求出口")
	}
	var options map[string]json.RawMessage
	if err = json.Unmarshal(raw, &options); err != nil {
		return err
	}
	if options == nil {
		return kiloConflict("原任务请求配置缺失，不能重新推定")
	}
	patch, err := json.Marshal(input.KiloRoutingOptions)
	if err != nil {
		return err
	}
	var routing map[string]json.RawMessage
	if err = json.Unmarshal(patch, &routing); err != nil {
		return err
	}
	for key, value := range routing {
		options[key] = value
	}
	updated, err := json.Marshal(options)
	if err != nil {
		return err
	}
	result, err := tx.Client().ExecContext(ctx, `UPDATE pk_kilo_jobs SET options=$3::jsonb,updated_at=NOW() WHERE id=$1 AND revision=$2 AND status='failed' AND stage='subscription_verified' AND error_code='PROXY_POOL_CAPACITY' AND (lease_until IS NULL OR lease_until<=NOW())`, jobID, job.Revision, string(updated))
	if err != nil {
		return err
	}
	if n, affectedErr := result.RowsAffected(); affectedErr != nil {
		return affectedErr
	} else if n != 1 {
		return kiloConflict("任务在请求出口调整期间已改变")
	}
	return nil
}

// Authentication calls use one explicit proxy and the same egress guard as
// requests. They never ask the random request-pool router to select an exit.
func (s *KiloService) acquireLoginRoute(ctx context.Context, accountID int64) (*AccountProxyRoute, error) {
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	proxyID := int64(ParseExtraInt(account.Extra[KiloLoginProxyExtraKey]))
	if proxyID <= 0 {
		return nil, kiloBad("KILO_LOGIN_PROXY_REQUIRED", "此Kilo账号尚未设置固定登录代理")
	}
	proxy, err := s.proxies.GetByID(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	if !proxy.IsActive() || proxy.IsExpired(time.Now()) {
		return nil, kiloBad("KILO_PROXY_INVALID", "固定登录代理不可用，已阻止切换或直连")
	}
	if s.router == nil || s.router.egress == nil {
		return nil, ErrKiloEgressUnavailable
	}
	permit, err := s.router.egress.Acquire(ctx, accountID, proxy.ID, proxy.URL())
	if err != nil {
		return nil, err
	}
	if permit == nil {
		return nil, ErrKiloEgressUnavailable
	}
	return &AccountProxyRoute{Context: permit.Context, URL: proxy.URL(), ProxyID: proxy.ID, close: permit.Close}, nil
}

func (s *KiloService) prepareLoginRoute(ctx context.Context, preparedID, accountID int64, options *KiloBatchInput) error {
	var loginID *int64
	if err := s.db.QueryRowContext(ctx, `SELECT login_proxy_id FROM pk_kilo_prepared_accounts WHERE id=$1`, preparedID).Scan(&loginID); err != nil {
		return err
	}
	if selected := options.LoginProxyIDs[preparedID]; selected > 0 {
		if loginID != nil && *loginID != selected {
			return kiloConflict("登录代理必须与已保存的固定IP一致")
		}
		loginID = &selected
	}
	if loginID == nil || *loginID <= 0 {
		return kiloBad("KILO_LOGIN_PROXY_REQUIRED", "请为每个Kilo账号设置一个固定登录代理")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = LockKiloOnboardingLease(ctx, tx.Client()); err != nil {
		return err
	}
	txctx := dbent.NewTxContext(ctx, tx)
	account, err := s.accounts.GetByID(txctx, accountID)
	if err != nil {
		return err
	}
	if previous := ParseExtraInt(account.Extra[KiloLoginProxyExtraKey]); previous > 0 && int64(previous) != *loginID {
		return kiloConflict("网关登录IP与预备账号不一致")
	}
	account.Extra[KiloLoginProxyExtraKey] = *loginID
	if err = s.accounts.Update(txctx, account); err != nil {
		return err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET login_proxy_id=$2,updated_at=NOW() WHERE id=$1`, preparedID, *loginID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	_, err = s.egress.Verify(ctx, accountID, *loginID)
	return err
}

// Manual request-exit additions must create the ordinary binding before probing.
// Login configuration remains outside this collection and cannot be released here.
func (s *KiloService) VerifyRequestEgress(ctx context.Context, accountID, proxyID int64) (*KiloEgressRoute, error) {
	var preparedID int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM pk_kilo_prepared_accounts WHERE account_id=$1`, accountID).Scan(&preparedID); err != nil {
		return nil, err
	}
	operation, unlock, err := s.lockPreparedOperation(ctx, preparedID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	ctx = operation
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if int64(ParseExtraInt(account.Extra[KiloLoginProxyExtraKey])) == proxyID {
		return nil, kiloBad("KILO_LOGIN_PROXY_FIXED", "登录代理保持固定，不可加入请求代理集合")
	}
	var raw []byte
	if err = s.db.QueryRowContext(ctx, `SELECT routing_options FROM pk_kilo_prepared_accounts WHERE id=$1`, preparedID).Scan(&raw); err != nil {
		return nil, err
	}
	var options KiloRoutingOptions
	if err = json.Unmarshal(raw, &options); err != nil {
		return nil, err
	}
	if err = normalizeKiloRouting(&options); err != nil {
		return nil, err
	}
	repo, ok := s.accounts.(AccountProxyBindingRepository)
	if !ok {
		return nil, errors.New("账号代理配置不可用")
	}
	bindings, err := repo.GetAccountProxyBindings(ctx, accountID)
	if err != nil {
		return nil, err
	}
	items := make([]AccountProxyBindingInput, 0, len(bindings)+1)
	exists := false
	for _, binding := range bindings {
		items = append(items, AccountProxyBindingInput{ProxyID: binding.ProxyID, MaxConcurrency: binding.MaxConcurrency, MaxAccounts: 1})
		if binding.ProxyID == proxyID {
			exists = true
		}
	}
	if !exists {
		items = append(items, AccountProxyBindingInput{ProxyID: proxyID, MaxConcurrency: options.ProxyConcurrency, MaxAccounts: 1})
		if err = repo.ReplaceAccountProxyBindings(ctx, []int64{accountID}, items); err != nil {
			return nil, err
		}
	}
	return s.egress.Verify(ctx, accountID, proxyID)
}

func (s *KiloService) ReleaseRequestEgress(ctx context.Context, routeID int64) error {
	var preparedID int64
	if err := s.db.QueryRowContext(ctx, `SELECT p.id FROM pk_kilo_prepared_accounts p JOIN pk_kilo_egress_routes r ON r.account_id=p.account_id WHERE r.id=$1`, routeID).Scan(&preparedID); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	operation, unlock, err := s.lockPreparedOperation(ctx, preparedID)
	if err != nil {
		return err
	}
	defer unlock()
	ctx = operation
	var accountID, proxyID int64
	if err := s.db.QueryRowContext(ctx, `SELECT account_id,proxy_id FROM pk_kilo_egress_routes WHERE id=$1`, routeID).Scan(&accountID, &proxyID); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	if int64(ParseExtraInt(account.Extra[KiloLoginProxyExtraKey])) == proxyID {
		return kiloConflict("这是固定登录代理，不能作为请求出口释放")
	}
	return s.egress.Release(ctx, routeID)
}

// A request failure only records a durable release intent. Maintenance drains
// the response leases before returning IPs to the shared pool.
type KiloGatewayFailureRepository interface {
	RecordKiloGatewayFailure(context.Context, *Account, int, string) (bool, error)
}

// Gateway stream failures nest the same structured identity code under response.
// Keep profile parsing unchanged and classify only these two gateway envelopes.
func kiloGatewayIdentityForbidden(payload []byte) bool {
	var envelope struct {
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(payload, &envelope) == nil && len(envelope.Response) > 0 && string(envelope.Response) != "null" {
		return kiloProfileIdentityForbidden(envelope.Response)
	}
	return kiloProfileIdentityForbidden(payload)
}

// handled separates lifecycle ownership from whether this response changed state.
// A stale or failed CAS must never fall through to unversioned account writes.
func (s *RateLimitService) observeKiloGatewayFailure(ctx context.Context, account *Account, status int, payload []byte) (handled, disabled bool) {
	if s == nil || account == nil || !account.IsKiloGatewayAccount() {
		return false, false
	}
	if status == http.StatusForbidden && !kiloGatewayIdentityForbidden(payload) {
		// Provider permission and edge refusals never establish an identity ban.
		return true, false
	}
	recorder, ok := s.accountRepo.(KiloGatewayFailureRepository)
	if !ok {
		return true, false
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	recorded, err := recorder.RecordKiloGatewayFailure(stateCtx, account, status, openAIStreamFailedEventErrorCode(payload))
	return true, err == nil && recorded
}

func (s *KiloService) drainRequestedEgress(ctx context.Context) error {
	// A manually released individual exit also finishes once its response
	// leases close; it must not require the whole account to be disabled.
	drainingRows, err := s.db.QueryContext(ctx, `SELECT r.id FROM pk_kilo_egress_routes r JOIN accounts a ON a.id=r.account_id WHERE r.state='draining' AND r.proxy_id<>COALESCE(NULLIF(a.extra->>'kilo_login_proxy_id','')::bigint,0) ORDER BY r.id LIMIT 100`)
	if err != nil {
		return err
	}
	routeIDs := []int64{}
	for drainingRows.Next() {
		var id int64
		if err = drainingRows.Scan(&id); err != nil {
			break
		}
		routeIDs = append(routeIDs, id)
	}
	if err == nil {
		err = drainingRows.Err()
	}
	_ = drainingRows.Close()
	if err != nil {
		return err
	}
	for _, id := range routeIDs {
		if err = s.egress.Release(ctx, id); err != nil && !errors.Is(err, ErrKiloEgressDraining) {
			return err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,FALSE FROM pk_kilo_prepared_accounts WHERE status='disabled' AND last_error LIKE 'kilo_release:%' ORDER BY id LIMIT 20`)
	if err != nil {
		return err
	}
	type pendingRelease struct {
		id        int64
		clearPair bool
	}
	ids := []pendingRelease{}
	for rows.Next() {
		var item pendingRelease
		if err = rows.Scan(&item.id, &item.clearPair); err != nil {
			break
		}
		ids = append(ids, item)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, item := range ids {
		if err = s.releaseRequestExits(ctx, item.id, item.clearPair); err != nil && !errors.Is(err, ErrKiloEgressDraining) && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func (s *KiloService) releaseRequestExits(ctx context.Context, preparedID int64, clearPair bool) error {
	operation, unlock, err := s.lockPreparedOperation(ctx, preparedID)
	if err != nil {
		return err
	}
	defer unlock()
	return s.releaseRequestExitsOwned(operation, preparedID, clearPair)
}

func (s *KiloService) releaseRequestExitsOwned(ctx context.Context, preparedID int64, clearPair bool) error {
	accountID, err := s.preparedAccountID(ctx, preparedID)
	if err != nil {
		return err
	}
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	if account.Schedulable {
		return kiloConflict("请先停止网关调度再回收请求IP")
	}
	loginID := int64(ParseExtraInt(account.Extra[KiloLoginProxyExtraKey]))
	routes, err := s.egress.List(ctx, accountID)
	if err != nil {
		return err
	}
	var draining bool
	for _, route := range routes {
		if route.ProxyID == loginID {
			continue
		}
		if err = s.egress.Release(ctx, route.ID); errors.Is(err, ErrKiloEgressDraining) {
			draining = true
		} else if err != nil {
			return err
		}
	}
	if draining {
		return ErrKiloEgressDraining
	}
	repo, ok := s.accounts.(AccountProxyBindingRepository)
	if !ok {
		return errors.New("账号代理配置不可用")
	}
	if err = repo.ReplaceAccountProxyBindings(ctx, []int64{accountID}, nil); err != nil {
		return err
	}
	if !clearPair {
		_, err = s.db.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET last_error=replace(last_error,'kilo_release:','kilo_released:'),updated_at=NOW() WHERE id=$1 AND status='disabled' AND last_error LIKE 'kilo_release:%'`, preparedID)
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
	var sourceID *int64
	if err = kiloScan(ctx, tx.Client(), `SELECT source_account_id FROM pk_kilo_prepared_accounts WHERE id=$1 AND status='disabled' FOR UPDATE`, []any{preparedID}, &sourceID); err != nil {
		return err
	}
	txctx := dbent.NewTxContext(ctx, tx)
	if sourceID != nil {
		locker, ok := s.accounts.(KiloAccountMutationLocker)
		if !ok {
			return errors.New("账号事务锁不可用")
		}
		if err = locker.LockAccountMutation(txctx); err != nil {
			return err
		}
		source, readErr := s.accounts.GetByID(txctx, *sourceID)
		if readErr != nil {
			return readErr
		}
		if int64(ParseExtraInt(source.Extra["kilo_source_prepared_id"])) == preparedID {
			delete(source.Extra, "kilo_source_prepared_id")
			delete(source.Extra, "kilo_source_gateway_id")
			if err = s.accounts.Update(txctx, source); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status=CASE WHEN identity_status='retired' OR identity_mode='gpt_coupled' THEN 'disabled' ELSE 'ready' END,binding_state=CASE WHEN identity_status='retired' OR identity_mode='gpt_coupled' THEN 'blocked' WHEN source_account_id IS NULL THEN 'empty' ELSE binding_state END,source_account_id=NULL,binding_job_id=NULL,last_error='',updated_at=NOW() WHERE id=$1 AND status='disabled'`, preparedID); err != nil {
		return err
	}
	return tx.Commit()
}

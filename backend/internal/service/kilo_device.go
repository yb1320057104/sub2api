package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/google/uuid"
)

// Authorization keeps its fixed login proxy on an inactive gateway identity.
// Model traffic receives a separate proxy set only after identity verification.
func (s *KiloService) StartDevice(ctx context.Context, input KiloDeviceInput) (*KiloDeviceStatus, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 256 || input.ProxyID == nil || *input.ProxyID <= 0 {
		return nil, kiloBad("KILO_DEVICE_INPUT", "请填写账号名称并选择授权出口代理")
	}
	if !s.encryptionReady() {
		return nil, kiloBad("KILO_ENCRYPTION", "请先配置服务器凭据加密密钥")
	}
	proxy, err := s.proxies.GetByID(ctx, *input.ProxyID)
	if err != nil {
		return nil, err
	}
	if !proxy.IsActive() || proxy.IsExpired(time.Now()) {
		return nil, kiloBad("KILO_PROXY_INVALID", "授权代理已停用或过期")
	}
	machine := uuid.NewString()
	account, err := buildAccountForCreate(&CreateAccountInput{Name: input.Name, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Priority: 1, SkipDefaultGroupBind: true, Credentials: map[string]any{"base_url": KiloGatewayBaseURL, "kilo_machine_id": machine}, Extra: map[string]any{AccountExtraKiloBridgeEnabled: true, KiloManagedExtraKey: true, "kilo_gateway_profile": "cli", KiloLoginProxyExtraKey: proxy.ID}}, map[string]any{AccountExtraKiloBridgeEnabled: true, KiloManagedExtraKey: true, "kilo_gateway_profile": "cli", KiloLoginProxyExtraKey: proxy.ID})
	if err != nil {
		return nil, err
	}
	account.Status, account.Schedulable = StatusDisabled, false
	if err = s.accounts.Create(ctx, account); err != nil {
		return nil, err
	}
	saved := false
	defer func() {
		if !saved {
			s.releaseInactiveKiloEgress(context.WithoutCancel(ctx), account.ID)
		}
	}()
	if _, err = s.egress.Verify(ctx, account.ID, proxy.ID); err != nil {
		return nil, err
	}
	out, err := s.startDeviceForAccount(ctx, input.Name, account.ID, proxy.ID)
	if err != nil {
		return nil, err
	}
	saved = true
	return out, nil
}

// Only internal callers can reuse an already reserved gateway. Its fixed login
// proxy is rechecked by the route owner, and the session insert is lease fenced.
func (s *KiloService) startDeviceForAccount(ctx context.Context, name string, accountID, proxyID int64) (*KiloDeviceStatus, error) {
	route, err := s.acquireLoginRoute(ctx, accountID)
	if err != nil {
		return nil, err
	}
	defer route.Close()
	if route.ProxyID != proxyID {
		return nil, kiloConflict("设备授权出口与固定登录代理不一致")
	}
	issued, err := s.remote.StartDevice(route.Context, route.URL)
	if err != nil {
		return nil, err
	}
	if issued == nil || issued.Code == "" || issued.ExpiresIn <= 0 || issued.ExpiresIn > 30*time.Minute || issued.VerificationURL == "" || issued.UserCode == "" {
		return nil, errors.New("官方设备授权响应无效")
	}
	encrypted, err := s.encryptor.Encrypt(issued.Code)
	if err != nil {
		return nil, errors.New("设备授权会话加密失败")
	}
	out := &KiloDeviceStatus{ID: uuid.NewString(), Status: "pending", VerificationURL: issued.VerificationURL, UserCode: issued.UserCode, ExpiresAt: time.Now().Add(issued.ExpiresIn)}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = LockKiloOnboardingLease(ctx, tx); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pk_kilo_device_sessions(id,name,proxy_id,account_id,code_encrypted,verification_url,user_code,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, out.ID, name, proxyID, accountID, encrypted, out.VerificationURL, out.UserCode, out.ExpiresAt); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *KiloService) PollDevice(ctx context.Context, id string) (*KiloDeviceStatus, error) {
	return s.pollDevice(ctx, id)
}

func (s *KiloService) pollDevice(ctx context.Context, id string) (*KiloDeviceStatus, error) {
	if err := kiloID(id); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = LockKiloOnboardingLease(ctx, tx); err != nil {
		return nil, err
	}
	out := &KiloDeviceStatus{ID: id}
	var code, receipt string
	var proxyID, accountID int64
	var due, onboarding bool
	err = tx.QueryRowContext(ctx, `SELECT d.status,d.verification_url,d.user_code,d.expires_at,d.prepared_id,d.email,d.code_encrypted,d.token_encrypted,d.proxy_id,d.account_id,d.next_poll_at<=NOW(),EXISTS(SELECT 1 FROM pk_kilo_jobs j WHERE j.account_id=d.account_id AND j.job_kind='onboard_source') FROM pk_kilo_device_sessions d WHERE d.id=$1 FOR UPDATE OF d`, id).Scan(&out.Status, &out.VerificationURL, &out.UserCode, &out.ExpiresAt, &out.PreparedID, &out.Email, &code, &receipt, &proxyID, &accountID, &due, &onboarding)
	if err != nil {
		return nil, err
	}
	if out.Status == "saved" {
		out.Status = "approved"
		return out, nil
	}
	if onboarding {
		lease, ok := ctx.Value(kiloOnboardingLeaseKey{}).(kiloOnboardingLease)
		if !ok || lease.jobID <= 0 {
			return nil, kiloConflict("此设备授权由自动接入任务管理")
		}
	}
	if out.Status == "approved" {
		if receipt != "" {
			if err = tx.Commit(); err != nil {
				return nil, err
			}
			plain, decryptErr := s.encryptor.Decrypt(receipt)
			if decryptErr != nil {
				return nil, errors.New("设备授权结果解密失败")
			}
			var issued KiloRemoteDevice
			if json.Unmarshal([]byte(plain), &issued) != nil {
				return nil, errors.New("设备授权结果格式无效")
			}
			return s.saveDeviceCredential(ctx, id, accountID, &issued)
		}
		if due {
			return nil, kiloBad("DEVICE_RESULT_LOST", "设备取钥结果未能保存，请重新设备授权；不会重放已可能消费的设备令牌")
		}
		out.Status = "pending"
		return out, nil
	}
	if out.Status != "pending" {
		return out, nil
	}
	if !out.ExpiresAt.After(time.Now()) {
		_, err = tx.ExecContext(ctx, `UPDATE pk_kilo_device_sessions SET status='expired',code_encrypted='',token_encrypted='',updated_at=NOW() WHERE id=$1`, id)
		if err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		out.Status = "expired"
		s.releaseInactiveKiloEgress(ctx, accountID)
		return out, nil
	}
	if !due {
		return out, nil
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	plaintext, err := s.encryptor.Decrypt(code)
	if err != nil {
		return nil, errors.New("设备授权会话解密失败")
	}
	route, err := s.acquireLoginRoute(ctx, accountID)
	if err != nil {
		return nil, err
	}
	defer route.Close()
	if route.ProxyID != proxyID {
		return nil, kiloConflict("设备授权出口与固定登录代理不一致")
	}
	// Persist consumption intent before the remote boundary. An interrupted
	// consumer is never allowed to poll the same possibly consumed token again.
	changed, err := s.updateDeviceReceipt(ctx, id, "pending", "approved", "", false)
	if err != nil {
		return nil, err
	}
	if !changed {
		return out, nil
	}
	result, err := s.remote.PollDevice(route.Context, plaintext, route.URL)
	if err != nil {
		return nil, kiloBad("DEVICE_RESULT_LOST", "设备取钥响应不明，请重新设备授权；不会重复消费原授权")
	}
	if result == nil {
		return nil, errors.New("官方设备授权状态无效")
	}
	switch result.Status {
	case "pending":
		_, err = s.updateDeviceReceipt(ctx, id, "approved", "pending", "", false)
		return out, err
	case "denied", "expired":
		_, err = s.updateDeviceReceipt(ctx, id, "approved", result.Status, "", true)
		if err != nil {
			return nil, err
		}
		out.Status = result.Status
		s.releaseInactiveKiloEgress(ctx, accountID)
		return out, nil
	case "approved":
		if !kiloValidKey(result.Token) || strings.TrimSpace(result.Email) == "" {
			return nil, errors.New("官方设备授权未返回有效凭据")
		}
		raw, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return nil, marshalErr
		}
		encrypted, encryptErr := s.encryptor.Encrypt(string(raw))
		if encryptErr != nil {
			return nil, errors.New("设备授权结果加密失败")
		}
		if changed, err = s.updateDeviceReceipt(ctx, id, "approved", "approved", encrypted, true); err != nil {
			return nil, err
		} else if !changed {
			return nil, kiloConflict("设备授权已取消或由其他任务接管")
		}
		return s.saveDeviceCredential(ctx, id, accountID, result)
	default:
		return nil, errors.New("官方设备授权状态无效")
	}
}
func (s *KiloService) updateDeviceReceipt(ctx context.Context, id, from, status, receipt string, clearCode bool) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err = LockKiloOnboardingLease(ctx, tx); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE pk_kilo_device_sessions SET status=$3,token_encrypted=$4,code_encrypted=CASE WHEN $5 THEN '' ELSE code_encrypted END,next_poll_at=NOW()+CASE WHEN $3='approved' AND $4='' THEN INTERVAL '60 seconds' ELSE INTERVAL '5 seconds' END,updated_at=NOW() WHERE id=$1 AND status=$2 AND token_encrypted='' AND ($2<>'pending' OR (next_poll_at<=NOW() AND expires_at>NOW()))`, id, from, status, receipt, clearCode)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *KiloService) saveDeviceCredential(ctx context.Context, id string, accountID int64, issued *KiloRemoteDevice) (*KiloDeviceStatus, error) {
	if !kiloValidKey(issued.Token) || strings.TrimSpace(issued.Email) == "" {
		return nil, errors.New("官方设备授权未返回有效凭据")
	}
	encrypted, err := s.encryptor.Encrypt(issued.Token)
	if err != nil {
		return nil, errors.New("Kilo凭据加密失败")
	}
	fingerprint := sha256.Sum256([]byte(issued.Token))
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	txctx := dbent.NewTxContext(ctx, tx)
	if err = LockKiloOnboardingLease(ctx, tx.Client()); err != nil {
		return nil, err
	}
	var name, status string
	var expires time.Time
	var prepared *int64
	var proxyID int64
	if err = kiloScan(ctx, tx.Client(), `SELECT name,status,expires_at,prepared_id,proxy_id FROM pk_kilo_device_sessions WHERE id=$1 AND account_id=$2 FOR UPDATE`, []any{id, accountID}, &name, &status, &expires, &prepared, &proxyID); err != nil {
		return nil, err
	}
	if status == "saved" {
		return &KiloDeviceStatus{ID: id, Status: "approved", PreparedID: prepared, Email: issued.Email, ExpiresAt: expires}, nil
	}
	if (status != "pending" && status != "approved") || (status == "pending" && !expires.After(time.Now())) {
		return nil, kiloConflict("设备授权已取消或过期，凭据未保存")
	}
	locker, ok := s.accounts.(KiloAccountMutationLocker)
	if !ok {
		return nil, errors.New("账号事务锁不可用")
	}
	if err = locker.LockAccountMutation(txctx); err != nil {
		return nil, err
	}
	account, err := s.accounts.GetByID(txctx, accountID)
	if err != nil {
		return nil, err
	}
	machine, _ := account.Credentials["kilo_machine_id"].(string)
	if account.Schedulable || account.Extra[KiloManagedExtraKey] != true || int64(ParseExtraInt(account.Extra[KiloLoginProxyExtraKey])) != proxyID || machine == "" {
		return nil, kiloConflict("设备授权网关身份或固定代理已改变")
	}
	var sourceID, bindingJobID *int64
	organization, preparedStatus := "", "ready"
	lease, fenced := ctx.Value(kiloOnboardingLeaseKey{}).(kiloOnboardingLease)
	if onboardingJob := int64(ParseExtraInt(account.Extra["kilo_onboarding_job_id"])); onboardingJob > 0 {
		if !fenced || lease.jobID != onboardingJob {
			return nil, kiloConflict("自动接入设备缺少有效任务所有权")
		}
		var jobAccountID int64
		if err = kiloScan(ctx, tx.Client(), `SELECT source_account_id,account_id,organization_id FROM pk_kilo_jobs WHERE id=$1 AND job_kind='onboard_source'`, []any{lease.jobID}, &sourceID, &jobAccountID, &organization); err != nil {
			return nil, err
		}
		if sourceID == nil || jobAccountID != accountID || int64(ParseExtraInt(account.Extra["kilo_onboarding_source_id"])) != *sourceID {
			return nil, kiloConflict("自动接入网关归属已改变")
		}
		bindingJobID, preparedStatus = &lease.jobID, "binding"
	}
	var preparedID int64
	err = kiloScan(ctx, tx.Client(), `INSERT INTO pk_kilo_prepared_accounts(name,email,credential_encrypted,credential_fingerprint,auth_method,machine_id,account_id,login_proxy_id,organization_id,status,binding_job_id) VALUES($1,$2,$3,$4,'device',$5,$6,$7,$8,$9,$10) ON CONFLICT(credential_fingerprint) DO NOTHING RETURNING id`, []any{name, issued.Email, encrypted, hex.EncodeToString(fingerprint[:]), machine, accountID, proxyID, organization, preparedStatus, bindingJobID}, &preparedID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, kiloConflict("此Kilo凭据已在准备池中，请使用已有记录")
	}
	if err != nil {
		return nil, err
	}
	account.Credentials["api_key"] = issued.Token
	account.Credentials["kilo_organization_id"] = organization
	account.Extra["kilo_prepared_id"] = preparedID
	account.Extra["kilo_binding_revision"] = int64(1)
	if sourceID != nil {
		account.Extra[KiloIdentityModeExtraKey] = KiloIdentityModeCoupled
		account.Extra[KiloIdentitySourceExtraKey] = *sourceID
		source, readErr := s.accounts.GetByID(txctx, *sourceID)
		if readErr != nil {
			return nil, readErr
		}
		source.Extra["kilo_source_prepared_id"], source.Extra["kilo_source_gateway_id"] = preparedID, accountID
		if err = s.accounts.Update(txctx, source); err != nil {
			return nil, err
		}
	}
	if err = s.accounts.Update(txctx, account); err != nil {
		return nil, err
	}
	if bindingJobID != nil {
		if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET binding_state='binding',source_account_id=$2,identity_mode='gpt_coupled',identity_source_account_id=$2 WHERE id=$1`, preparedID, sourceID); err != nil {
			return nil, err
		}
		if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_jobs SET prepared_id=$2,binding_revision=1,stage='credential_verified',updated_at=NOW() WHERE id=$1`, *bindingJobID, preparedID); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_device_sessions SET status='saved',prepared_id=$2,email=$3,code_encrypted='',token_encrypted='',updated_at=NOW() WHERE id=$1`, id, preparedID, issued.Email); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &KiloDeviceStatus{ID: id, Status: "approved", PreparedID: &preparedID, Email: issued.Email, ExpiresAt: expires}, nil
}
func (s *KiloService) CancelDevice(ctx context.Context, id string) error {
	if err := kiloID(id); err != nil {
		return err
	}
	var accountID int64
	err := s.db.QueryRowContext(ctx, `UPDATE pk_kilo_device_sessions SET status='cancelled',code_encrypted='',token_encrypted='',updated_at=NOW() WHERE id=$1 AND status IN ('pending','approved') AND NOT EXISTS(SELECT 1 FROM pk_kilo_jobs j WHERE j.account_id=pk_kilo_device_sessions.account_id AND j.job_kind='onboard_source' AND j.status IN ('queued','running','awaiting_authorization')) RETURNING account_id`, id).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	s.releaseInactiveKiloEgress(ctx, accountID)
	return nil
}
func (s *KiloService) releaseInactiveKiloEgress(ctx context.Context, accountID int64) {
	cleanup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var onboarding bool
	if err := s.db.QueryRowContext(cleanup, `SELECT EXISTS(SELECT 1 FROM pk_kilo_jobs WHERE account_id=$1 AND job_kind='onboard_source' AND status IN ('queued','running','awaiting_authorization'))`, accountID).Scan(&onboarding); err != nil || onboarding {
		return
	}
	account, err := s.accounts.GetByID(cleanup, accountID)
	if err != nil || account.Schedulable {
		return
	}
	// A Kilo login exit remains attached to its identity even after an
	// unsuccessful GPT grant. Reusing that IP for another identity is unsafe.
	loginID := int64(ParseExtraInt(account.Extra[KiloLoginProxyExtraKey]))
	routes, err := s.egress.List(cleanup, accountID)
	if err != nil {
		return
	}
	for _, route := range routes {
		if route.ProxyID != loginID {
			_ = s.egress.Release(cleanup, route.ID)
		}
	}
}

func (s *KiloService) Organizations(ctx context.Context, id int64) ([]KiloOrganization, error) {
	var accountID sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT account_id FROM pk_kilo_prepared_accounts WHERE id=$1`, id).Scan(&accountID); err != nil {
		return nil, err
	}
	if !accountID.Valid {
		return nil, kiloBad("KILO_EGRESS_REQUIRED", "请先将预备账号加入批量任务以分配并验证出口")
	}
	route, err := s.acquireLoginRoute(ctx, accountID.Int64)
	if err != nil {
		return nil, err
	}
	defer route.Close()
	key, _, err := s.preparedCredential(route.Context, id)
	if err != nil {
		return nil, err
	}
	profile, err := s.remote.Profile(route.Context, key, route.URL)
	if err != nil {
		return nil, err
	}
	out := []KiloOrganization{}
	if profile.HasPersonalAccount {
		out = append(out, KiloOrganization{ID: "", Name: "Personal"})
	}
	return append(out, profile.Organizations...), nil
}
func (s *KiloService) SetOrganization(ctx context.Context, id int64, organization string) error {
	if !kiloValidOrganization(organization) {
		return kiloBad("KILO_ORGANIZATION_INVALID", "组织编号无效")
	}
	choices, err := s.Organizations(ctx, id)
	if err != nil {
		return err
	}
	allowed := false
	for _, item := range choices {
		if item.ID == organization {
			allowed = true
		}
	}
	if !allowed {
		return kiloBad("KILO_ORGANIZATION_DENIED", "凭据无权访问所选组织")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	txctx := dbent.NewTxContext(ctx, tx)
	var status string
	var accountID *int64
	if err = kiloScan(ctx, tx.Client(), `SELECT status,account_id FROM pk_kilo_prepared_accounts WHERE id=$1 FOR UPDATE`, []any{id}, &status, &accountID); err != nil {
		return err
	}
	if status != "ready" {
		return kiloConflict("绑定中或已启用账号不能切换组织，请先停止相关任务")
	}
	if accountID != nil {
		account, readErr := s.accounts.GetByID(txctx, *accountID)
		if readErr != nil {
			return readErr
		}
		if account.Schedulable {
			return kiloConflict("账号仍可调度，不能更改组织")
		}
		account.Credentials["kilo_organization_id"] = organization
		if err = s.accounts.Update(txctx, account); err != nil {
			return err
		}
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET organization_id=$2,updated_at=NOW() WHERE id=$1`, id, organization); err != nil {
		return err
	}
	return tx.Commit()
}

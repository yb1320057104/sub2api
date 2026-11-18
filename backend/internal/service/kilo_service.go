package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"unicode/utf8"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const kiloBatchLock int64 = 70450001

// KiloService owns onboarding only. Gateway scheduling, usage, and financial
// settlement remain with the existing account and billing owners.
type KiloService struct {
	db          *sql.DB
	client      *dbent.Client
	accounts    AccountRepository
	admin       AdminService
	groups      GroupRepository
	proxies     ProxyRepository
	encryptor   SecretEncryptor
	remote      KiloRemote
	reauth      *OpenAIOAuthReauthService
	recovery    KiloSourceRecoveryDriver
	modelSource KiloSubscriptionModelReader
	executor    KiloOnboardingExecutor
	egress      KiloEgressManager
	router      *AccountProxyRouter
	cfg         *config.Config
	mu          sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewKiloService(db *sql.DB, client *dbent.Client, accounts AccountRepository, admin AdminService, groups GroupRepository, proxies ProxyRepository, encryptor SecretEncryptor, remote KiloRemote, egress KiloEgressManager, router *AccountProxyRouter, cfg *config.Config) *KiloService {
	return &KiloService{db: db, client: client, accounts: accounts, admin: admin, groups: groups, proxies: proxies, encryptor: encryptor, remote: remote, executor: newKiloExecutorFromEnv(), egress: egress, router: router, cfg: cfg}
}

func (s *KiloService) encryptionReady() bool {
	if s == nil || s.encryptor == nil {
		return false
	}
	_, err := s.encryptor.Encrypt("kilo-encryption-readiness")
	return err == nil
}

func kiloBad(reason, message string) error { return infraerrors.BadRequest(reason, message) }
func kiloConflict(message string) error    { return infraerrors.Conflict("KILO_CONFLICT", message) }
func kiloHash(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func kiloID(value string) error {
	if _, err := uuid.Parse(value); err != nil {
		return kiloBad("KILO_REQUEST_ID", "请提供有效的请求编号，重试时保留原编号")
	}
	return nil
}
func kiloUniqueIDs(values []int64, maximum int) error {
	if len(values) > maximum {
		return kiloBad("KILO_SELECTION_LIMIT", fmt.Sprintf("一次最多处理%d项", maximum))
	}
	seen := make(map[int64]bool, len(values))
	for _, id := range values {
		if id <= 0 || seen[id] {
			return kiloBad("KILO_SELECTION_INVALID", "编号无效或重复")
		}
		seen[id] = true
	}
	return nil
}
func kiloValidKey(key string) bool {
	if len(key) < 8 || len(key) > 32768 {
		return false
	}
	for _, r := range key {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
func kiloValidOrganization(id string) bool { return id == "" || uuid.Validate(id) == nil }

func (s *KiloService) Overview(ctx context.Context) (*KiloOverview, error) {
	out := &KiloOverview{Prepared: []KiloPrepared{}, Jobs: []KiloJob{}, EncryptionReady: s.encryptionReady()}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.name,p.email,p.auth_method,p.organization_id,p.status,p.account_id,p.source_account_id,p.credential_encrypted<>'',p.last_error,p.created_at,p.login_proxy_id,COALESCE(x.name,''),COALESCE(a.name,''),COALESCE(g.name,''),p.routing_options,p.identity_status,p.binding_state,p.binding_revision,p.recovery_task_id,p.binding_error,(p.identity_session_encrypted<>'' OR p.identity_credential_encrypted<>''),p.identity_mode,p.identity_source_account_id,p.identity_checked_at,p.identity_session_encrypted<>'' FROM pk_kilo_prepared_accounts p LEFT JOIN proxies x ON x.id=p.login_proxy_id LEFT JOIN accounts a ON a.id=p.source_account_id LEFT JOIN accounts g ON g.id=p.account_id WHERE COALESCE(p.routing_options->>'management_deleted','false')<>'true' ORDER BY p.id DESC LIMIT 5000`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p KiloPrepared
		var routing []byte
		var identityRecoveryReady, identitySessionReady bool
		if err = rows.Scan(&p.ID, &p.Name, &p.Email, &p.AuthMethod, &p.OrganizationID, &p.Status, &p.AccountID, &p.SourceAccountID, &p.HasCredential, &p.LastError, &p.CreatedAt, &p.LoginProxyID, &p.LoginProxyName, &p.SourceName, &p.GatewayName, &routing, &p.IdentityStatus, &p.BindingState, &p.BindingRevision, &p.RecoveryTaskID, &p.BindingError, &identityRecoveryReady, &p.IdentityMode, &p.IdentitySourceAccountID, &p.IdentityCheckedAt, &identitySessionReady); err != nil {
			break
		}
		if err = json.Unmarshal(routing, &p.RoutingOptions); err != nil {
			break
		}
		if err = normalizeKiloRouting(&p.RoutingOptions); err != nil {
			break
		}
		p.Reusable = p.IdentityStatus == "active" && p.BindingState == "empty" && p.SourceAccountID == nil && p.HasCredential && p.LoginProxyID != nil && identityRecoveryReady && identitySessionReady && p.IdentityMode == KiloIdentityModeIndependent && p.IdentityCheckedAt != nil
		out.Prepared = append(out.Prepared, p)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	accountIDs := make([]int64, 0, len(out.Prepared))
	for i := range out.Prepared {
		p := &out.Prepared[i]
		// Imported Keys have an identity before a gateway/source pair exists.
		p.AccountView = &KiloAccountView{PreparedID: &p.ID, Name: p.Name, Email: p.Email, OrganizationID: p.OrganizationID, HasKey: p.HasCredential}
		if id := p.AccountID; id != nil && *id > 0 {
			accountIDs = append(accountIDs, *id)
		}
	}
	if reader, ok := s.accounts.(KiloAccountViewReader); ok && len(accountIDs) > 0 {
		views, viewErr := reader.GetKiloAccountViews(ctx, accountIDs)
		if viewErr != nil {
			return nil, viewErr
		}
		for i := range out.Prepared {
			p := &out.Prepared[i]
			if p.AccountID != nil {
				if view, exists := views[*p.AccountID]; exists && view.PreparedID != nil && *view.PreparedID == p.ID {
					p.AccountView = &view
				}
			}
		}
	}
	out.Jobs, err = s.kiloJobs(ctx, "WHERE j.stage<>'deleted' ORDER BY j.id DESC LIMIT 2000")
	return out, err
}

func (s *KiloService) ImportPrepared(ctx context.Context, input KiloPreparedImportInput) (*KiloPreparedImportResult, error) {
	if err := kiloID(input.RequestID); err != nil {
		return nil, err
	}
	if len(input.Items) < 1 || len(input.Items) > 1000 {
		return nil, kiloBad("KILO_IMPORT_LIMIT", "每批导入1至1000个Kilo账号")
	}
	if !s.encryptionReady() {
		return nil, infraerrors.ServiceUnavailable("KILO_ENCRYPTION", "请先配置服务器凭据加密密钥")
	}
	hash := kiloHash(input.Items)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return nil, err
	}
	var existingHash string
	var existing []byte
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,result FROM pk_kilo_import_requests WHERE request_id=$1`, input.RequestID).Scan(&existingHash, &existing)
	if err == nil {
		if existingHash != hash {
			return nil, kiloConflict("同一请求编号不能用于不同的导入内容")
		}
		var result KiloPreparedImportResult
		if err = json.Unmarshal(existing, &result); err != nil {
			return nil, err
		}
		return &result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	result := &KiloPreparedImportResult{Items: make([]KiloImportItemResult, 0, len(input.Items))}
	for i, item := range input.Items {
		item.Name, item.Email, item.APIKey, item.OrganizationID = strings.TrimSpace(item.Name), strings.TrimSpace(item.Email), strings.TrimSpace(item.APIKey), strings.TrimSpace(item.OrganizationID)
		entry := KiloImportItemResult{Line: i + 1, Status: "failed"}
		if item.Name == "" || utf8.RuneCountInString(item.Name) > 256 || !kiloValidKey(item.APIKey) || !kiloValidOrganization(item.OrganizationID) {
			entry.Message = "账号名称、Kilo凭据或组织编号无效"
			result.Failed++
			result.Items = append(result.Items, entry)
			continue
		}
		if item.Email != "" {
			if _, err = mail.ParseAddress(item.Email); err != nil {
				entry.Message = "邮箱格式无效"
				result.Failed++
				result.Items = append(result.Items, entry)
				continue
			}
		}
		encrypted, encryptErr := s.encryptor.Encrypt(item.APIKey)
		if encryptErr != nil {
			return nil, errors.New("Kilo凭据加密失败")
		}
		fingerprint := sha256.Sum256([]byte(item.APIKey))
		var id int64
		err = tx.QueryRowContext(ctx, `INSERT INTO pk_kilo_prepared_accounts(name,email,credential_encrypted,credential_fingerprint,auth_method,organization_id,machine_id) VALUES($1,$2,$3,$4,'api_key',$5,$6) ON CONFLICT(credential_fingerprint) DO NOTHING RETURNING id`, item.Name, item.Email, encrypted, hex.EncodeToString(fingerprint[:]), item.OrganizationID, uuid.NewString()).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, `SELECT id FROM pk_kilo_prepared_accounts WHERE credential_fingerprint=$1`, hex.EncodeToString(fingerprint[:])).Scan(&id)
			entry.Status = "duplicate"
			result.Duplicates++
		} else if err == nil {
			entry.Status = "imported"
			result.Imported++
		}
		if err != nil {
			return nil, err
		}
		entry.ID = &id
		result.Items = append(result.Items, entry)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pk_kilo_import_requests(request_id,payload_hash,result) VALUES($1,$2,$3)`, input.RequestID, hash, string(encoded)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

type KiloCandidate struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Email            string `json:"email"`
	Status           string `json:"status"`
	HasPassword      bool   `json:"has_password"`
	HasTOTP          bool   `json:"has_totp"`
	LoginProxyID     *int64 `json:"login_proxy_id,omitempty"`
	OnboardingReason string `json:"onboarding_reason,omitempty"`
}

func kiloSourceIdentity(account *Account) string {
	return kiloHash([]any{account.ID, account.Platform, account.Type, account.ParentAccountID, kiloSourceEmail(account), account.Credentials["chatgpt_account_id"]})
}
func kiloSourceEmail(account *Account) string {
	for _, key := range []string{"email", "chatgpt_email"} {
		if email, ok := account.Credentials[key].(string); ok {
			if address, err := mail.ParseAddress(strings.TrimSpace(email)); err == nil {
				return strings.ToLower(address.Address)
			}
		}
	}
	if token, ok := account.Credentials["access_token"].(string); ok {
		parts := strings.Split(token, ".")
		if len(parts) == 3 {
			if raw, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
				var claims map[string]any
				if json.Unmarshal(raw, &claims) == nil {
					if p, ok := claims["https://api.openai.com/profile"].(map[string]any); ok {
						if email, ok := p["email"].(string); ok {
							return strings.ToLower(strings.TrimSpace(email))
						}
					}
					if email, ok := claims["email"].(string); ok {
						return strings.ToLower(strings.TrimSpace(email))
					}
				}
			}
		}
	}
	if address, err := mail.ParseAddress(account.Name); err == nil {
		return strings.ToLower(address.Address)
	}
	return ""
}
func (s *KiloService) Candidates(ctx context.Context) ([]KiloCandidate, error) {
	accounts, err := s.accounts.ListAllWithFilters(ctx, PlatformOpenAI, AccountTypeOAuth, "", "", 0, "")
	if err != nil {
		return nil, err
	}
	out := []KiloCandidate{}
	for _, a := range accounts {
		if a.ParentAccountID == nil && a.IsOpenAIOAuth() {
			candidate := KiloCandidate{ID: a.ID, Name: a.Name, Email: kiloSourceEmail(&a), Status: a.Status, LoginProxyID: a.ProxyID}
			if s.reauth != nil {
				material, materialErr := s.reauth.ReadKiloLoginMaterial(ctx, a.ID)
				if materialErr == nil && material != nil {
					candidate.HasPassword = material.Password != ""
					candidate.HasTOTP = material.TOTPSecret != ""
				} else {
					candidate.OnboardingReason = "请先保存完整账密与2FA登录配置"
				}
			} else {
				candidate.OnboardingReason = "认证材料服务未配置"
			}
			if pinned := int64(ParseExtraInt(a.Extra[assignedAuthProxyExtraKey])); pinned > 0 {
				candidate.LoginProxyID = &pinned
			}
			out = append(out, candidate)
		}
	}
	return out, nil
}
func (s *KiloService) Groups(ctx context.Context) ([]Group, error) {
	return s.groups.ListActiveByPlatform(ctx, PlatformOpenAI)
}
func (s *KiloService) ActiveProxies(ctx context.Context) ([]KiloProxyOption, error) {
	proxies, err := s.proxies.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(proxies))
	for _, proxy := range proxies {
		ids = append(ids, proxy.ID)
	}
	reader, ok := s.proxies.(KiloProxyOwnershipReader)
	if !ok {
		return nil, errors.New("代理归属查询不可用")
	}
	ownership, err := reader.KiloProxyOwnership(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]KiloProxyOption, 0, len(proxies))
	for _, proxy := range proxies {
		out = append(out, KiloProxyOption{ID: proxy.ID, Name: proxy.Name, Status: proxy.Status, KiloProxyOwnership: ownership[proxy.ID]})
	}
	return out, nil
}
func (s *KiloService) ProxyGroups(ctx context.Context) ([]ProxyGroup, error) {
	repo, ok := s.proxies.(ProxyGroupRepository)
	if !ok {
		return nil, errors.New("代理组管理不可用")
	}
	return repo.ListProxyGroups(ctx)
}

func validateKiloBatch(input KiloBatchInput) error {
	if err := kiloID(input.RequestID); err != nil {
		return err
	}
	if len(input.PreparedIDs) < 1 || len(input.PreparedIDs) > 1000 {
		return kiloBad("KILO_BATCH_LIMIT", "每批请选择1至1000个预备账号")
	}
	for _, ids := range [][]int64{input.PreparedIDs, input.SourceAccountIDs, input.GroupIDs} {
		if err := kiloUniqueIDs(ids, 1000); err != nil {
			return err
		}
	}
	if len(input.SourceAccountIDs) > 0 && len(input.SourceAccountIDs) != len(input.PreparedIDs) {
		return kiloBad("KILO_PAIR_COUNT", "订阅连接模式必须为每个预备Kilo账号选择一个来源账号")
	}
	if len(input.SourceAccountIDs) > 0 && !input.BYOKEnabledConfirmed {
		return kiloBad("KILO_BYOK_CONFIRM_REQUIRED", "请先在官方BYOK页面完成所选ChatGPT授权并启用，再确认此步骤")
	}
	if len(input.GroupIDs) == 0 || !validKiloRequestProxyPool(input.ProxyGroupID) || input.ProxiesPerAccount < 1 || input.ProxiesPerAccount > 100 || input.ProxyConcurrency < 1 || input.ProxyConcurrency > 10000 || input.AccountConcurrency < 1 || input.AccountConcurrency > 10000 || input.Priority < 0 || input.Priority > 10000 {
		return kiloBad("KILO_BATCH_OPTIONS", "请选择目标分组和请求代理范围，并填写有效的出口数、并发及优先级")
	}
	return nil
}
func (s *KiloService) SubmitBatch(ctx context.Context, input KiloBatchInput) (string, error) {
	if input.AutoReleaseStatuses == nil {
		input.AutoReleaseStatuses = []int{401, 402, 403}
	}
	options := KiloRoutingOptions{ProxyGroupID: input.ProxyGroupID, ProxiesPerAccount: input.ProxiesPerAccount, ProxyConcurrency: input.ProxyConcurrency, AccountConcurrency: input.AccountConcurrency, Priority: input.Priority, AutoReleaseStatuses: input.AutoReleaseStatuses}
	if err := normalizeKiloRouting(&options); err != nil {
		return "", err
	}
	input.ProxiesPerAccount, input.ProxyConcurrency, input.AccountConcurrency = options.ProxiesPerAccount, options.ProxyConcurrency, options.AccountConcurrency
	if err := validateKiloBatch(input); err != nil {
		return "", err
	}
	if err := s.admin.ValidateAccountGroupBindings(ctx, input.GroupIDs); err != nil {
		return "", err
	}
	for _, id := range input.GroupIDs {
		g, err := s.groups.GetByIDLite(ctx, id)
		if err != nil {
			return "", err
		}
		if g.Platform != PlatformOpenAI || g.Status != StatusActive {
			return "", kiloBad("KILO_GROUP_INVALID", "目标分组须为启用的OpenAI分组")
		}
	}
	sourceIdentities := make(map[int64]string, len(input.SourceAccountIDs))
	for _, id := range input.SourceAccountIDs {
		a, err := s.accounts.GetByID(ctx, id)
		if err != nil {
			return "", err
		}
		if !a.IsOpenAIOAuth() || a.ParentAccountID != nil || kiloSourceEmail(a) == "" {
			return "", kiloBad("KILO_SOURCE_INVALID", "来源须为可识别邮箱的OpenAI订阅账号，不能使用影子账号")
		}
		sourceIdentities[id] = kiloSourceIdentity(a)
	}
	raw, _ := json.Marshal(input)
	hash := kiloHash(input)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return "", err
	}
	var oldID, oldHash string
	err = tx.QueryRowContext(ctx, `SELECT id::text,payload_hash FROM pk_kilo_batches WHERE request_id=$1`, input.RequestID).Scan(&oldID, &oldHash)
	if err == nil {
		if oldHash != hash {
			return "", kiloConflict("同一请求编号不能提交不同的配对任务")
		}
		return oldID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	batchID := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO pk_kilo_batches(id,request_id,payload_hash,options) VALUES($1,$2,$3,$4)`, batchID, input.RequestID, hash, string(raw)); err != nil {
		return "", err
	}
	for i, id := range input.PreparedIDs {
		var status string
		var loginProxyID *int64
		var bindingRevision int64
		if err = tx.QueryRowContext(ctx, `SELECT status,login_proxy_id,binding_revision FROM pk_kilo_prepared_accounts WHERE id=$1 FOR UPDATE`, id).Scan(&status, &loginProxyID, &bindingRevision); err != nil {
			return "", err
		}
		if status != "ready" {
			return "", kiloConflict("所选预备账号已绑定、停用或正在处理，请刷新后重试")
		}
		policy, policyErr := kiloReadLifecyclePolicy(ctx, tx, id)
		if policyErr != nil {
			return "", policyErr
		}
		var lifecycleSource *int64
		if len(input.SourceAccountIDs) > 0 {
			lifecycleSource = &input.SourceAccountIDs[i]
		}
		if policyErr = kiloRequireLifecycleSource(policy, lifecycleSource); policyErr != nil {
			return "", policyErr
		}
		if selected := input.LoginProxyIDs[id]; selected > 0 {
			if loginProxyID != nil && *loginProxyID != selected {
				return "", kiloConflict("登录代理必须沿用此Kilo身份已保存的固定IP")
			}
			loginProxyID = &selected
		}
		if loginProxyID == nil || *loginProxyID <= 0 {
			return "", kiloBad("KILO_LOGIN_PROXY_REQUIRED", "请为每个预备Kilo账号先设置固定登录代理")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET login_proxy_id=$2,routing_options=$3::jsonb,updated_at=NOW() WHERE id=$1`, id, *loginProxyID, string(kiloRoutingJSON(input))); err != nil {
			return "", err
		}
		var source any
		sourceIdentity := ""
		if len(input.SourceAccountIDs) > 0 {
			source = input.SourceAccountIDs[i]
			sourceIdentity = sourceIdentities[input.SourceAccountIDs[i]]
			var used bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_kilo_prepared_accounts WHERE source_account_id=$1 AND id<>$2) OR EXISTS(SELECT 1 FROM pk_kilo_jobs WHERE source_account_id=$1 AND status IN ('queued','running','awaiting_authorization'))`, source, id).Scan(&used); err != nil {
				return "", err
			}
			if used {
				return "", kiloConflict("来源订阅账号已被另一项Kilo任务占用")
			}
		}
		var jobID int64
		if err = tx.QueryRowContext(ctx, `INSERT INTO pk_kilo_jobs(batch_id,prepared_id,source_account_id,source_identity,options,binding_revision) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, batchID, id, source, sourceIdentity, string(raw), bindingRevision).Scan(&jobID); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status='binding',binding_job_id=$2,last_error='',updated_at=NOW() WHERE id=$1`, id, jobID); err != nil {
			return "", err
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return batchID, nil
}

func (s *KiloService) RetryJob(ctx context.Context, id int64) error {
	if onboarding, err := s.isSourceOnboardingJob(ctx, id); err != nil {
		return err
	} else if onboarding {
		return s.retrySourceOnboardingJob(ctx, id)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var preparedID int64
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT prepared_id,status FROM pk_kilo_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&preparedID, &status); err != nil {
		return err
	}
	if status != "failed" && status != "awaiting_authorization" {
		return kiloConflict("仅失败或等待官方授权的任务可重新检查")
	}
	result, err := tx.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status='binding',binding_job_id=$2,last_error='',updated_at=NOW() WHERE id=$1 AND status IN ('ready','binding') AND (binding_job_id IS NULL OR binding_job_id=$2)`, preparedID, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return kiloConflict("预备账号已由其他任务接管")
	}
	result, err = tx.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='queued',stage='queued',lease_token=NULL,lease_until=NULL,last_error='',next_attempt_at=NOW(),revision=revision+1,finished_at=NULL,updated_at=NOW() WHERE id=$1 AND (lease_until IS NULL OR lease_until<=NOW())`, id)
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return kiloConflict("任务仍由维护节点执行，请稍后重试")
	}
	return tx.Commit()
}
func (s *KiloService) CancelJob(ctx context.Context, id int64) error {
	if onboarding, err := s.isSourceOnboardingJob(ctx, id); err != nil {
		return err
	} else if onboarding {
		return s.cancelSourceOnboardingJob(ctx, id)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return err
	}
	var preparedID int64
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT prepared_id,status FROM pk_kilo_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&preparedID, &status); err != nil {
		return err
	}
	if status == "succeeded" {
		return kiloConflict("已完成的绑定不能通过取消任务撤销")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='cancelled',stage='cancelled',revision=revision+1,lease_token=NULL,lease_until=NULL,finished_at=NOW(),updated_at=NOW() WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET status='ready',binding_job_id=NULL,updated_at=NOW() WHERE id=$1 AND binding_job_id=$2 AND status='binding'`, preparedID, id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	_, unlock, err := s.lockPreparedOperation(ctx, preparedID)
	if err != nil {
		return err
	}
	unlock()
	return nil
}

// Local account creation and its ownership pointer commit together. An expired
// worker may retry this stage without publishing a second gateway account.
func (s *KiloService) ensurePreparedAccount(ctx context.Context, preparedID int64, options *KiloBatchInput) (*Account, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = LockKiloOnboardingLease(ctx, tx.Client()); err != nil {
		return nil, err
	}
	var accountID, bindingJobID, sourceID sql.NullInt64
	var name, key, machine, organization string
	var bindingRevision int64
	if err = kiloScan(ctx, tx.Client(), `SELECT account_id,name,credential_encrypted,machine_id::text,organization_id,binding_revision,binding_job_id FROM pk_kilo_prepared_accounts WHERE id=$1 FOR UPDATE`, []any{preparedID}, &accountID, &name, &key, &machine, &organization, &bindingRevision, &bindingJobID); err != nil {
		return nil, err
	}
	if bindingJobID.Valid {
		var reservedRevision int64
		if err = kiloScan(ctx, tx.Client(), `SELECT source_account_id,binding_revision FROM pk_kilo_jobs WHERE id=$1 AND prepared_id=$2`, []any{bindingJobID.Int64, preparedID}, &sourceID, &reservedRevision); err != nil {
			return nil, err
		}
		// Jobs queued before the pool migration have revision zero; their
		// current prepared owner still supplies the binding generation.
		if reservedRevision != 0 && reservedRevision != bindingRevision {
			return nil, kiloConflict("预备任务的绑定代次已改变")
		}
	}
	plaintext, err := s.encryptor.Decrypt(key)
	if err != nil {
		return nil, errors.New("Kilo凭据解密失败")
	}
	txctx := dbent.NewTxContext(ctx, tx)
	if accountID.Valid {
		locker, ok := s.accounts.(KiloAccountMutationLocker)
		if !ok {
			return nil, errors.New("账号事务锁不可用")
		}
		if err = locker.LockAccountMutation(txctx); err != nil {
			return nil, err
		}
		account, readErr := s.accounts.GetByID(txctx, accountID.Int64)
		if readErr != nil {
			return nil, readErr
		}
		if account.Schedulable || !kiloPreparedAccountMatches(account, preparedID, plaintext, machine, organization) {
			return nil, kiloConflict("预备网关账号已启用或配置已改变")
		}
		previousRevision := int64(ParseExtraInt(account.Extra["kilo_binding_revision"]))
		previousSource := int64(ParseExtraInt(account.Extra["kilo_onboarding_source_id"]))
		if previousRevision > 0 && previousRevision != bindingRevision || previousSource > 0 && (!sourceID.Valid || previousSource != sourceID.Int64) {
			return nil, kiloConflict("预备网关的来源或绑定代次已改变")
		}
		if previousRevision == 0 || sourceID.Valid && previousSource == 0 {
			account.Extra["kilo_binding_revision"] = bindingRevision
			if sourceID.Valid {
				account.Extra["kilo_onboarding_source_id"] = sourceID.Int64
			}
			if err = s.accounts.Update(txctx, account); err != nil {
				return nil, err
			}
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return account, nil
	}
	extra := map[string]any{AccountExtraKiloBridgeEnabled: true, KiloManagedExtraKey: true, "kilo_gateway_profile": "cli", "kilo_prepared_id": preparedID, "kilo_binding_revision": bindingRevision}
	if sourceID.Valid {
		extra["kilo_onboarding_source_id"] = sourceID.Int64
	}
	input := &CreateAccountInput{Name: name, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 10, Priority: 1, SkipDefaultGroupBind: true, Credentials: map[string]any{"api_key": plaintext, "base_url": KiloGatewayBaseURL, "kilo_machine_id": machine, "kilo_organization_id": organization}, Extra: extra}
	if options != nil {
		input.Concurrency = options.AccountConcurrency
		input.Priority = options.Priority
	}
	account, err := buildAccountForCreate(input, input.Extra)
	if err != nil {
		return nil, err
	}
	account.Status = StatusDisabled
	account.Schedulable = false
	if err = s.accounts.Create(txctx, account); err != nil {
		return nil, err
	}
	if _, err = tx.Client().ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET account_id=$2,updated_at=NOW() WHERE id=$1`, preparedID, account.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return account, nil
}

func kiloPreparedAccountMatches(account *Account, preparedID int64, key, machine, organization string) bool {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey || account.ParentAccountID != nil || account.Extra[KiloManagedExtraKey] != true || account.Extra[AccountExtraKiloBridgeEnabled] != true || account.Extra["kilo_gateway_profile"] != "cli" {
		return false
	}
	if ParseExtraInt(account.Extra["kilo_prepared_id"]) != int(preparedID) {
		return false
	}
	actualKey, _ := account.Credentials["api_key"].(string)
	base, _ := account.Credentials["base_url"].(string)
	actualMachine, _ := account.Credentials["kilo_machine_id"].(string)
	actualOrganization, _ := account.Credentials["kilo_organization_id"].(string)
	return actualKey == key && base == KiloGatewayBaseURL && actualMachine == machine && actualOrganization == organization
}

type kiloQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func kiloScan(ctx context.Context, q kiloQuerier, query string, args []any, values ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	if err = rows.Scan(values...); err != nil {
		return err
	}
	return rows.Err()
}

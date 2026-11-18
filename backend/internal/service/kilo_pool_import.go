package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// KiloPoolImportItem supplies login material only at the import boundary. The
// queue receives a source account ID and reads its encrypted configuration.
type KiloPoolImportItem struct {
	Email            string   `json:"email"`
	Password         string   `json:"password"`
	TOTPSecret       string   `json:"totp_secret"`
	Name             string   `json:"name,omitempty"`
	LoginProxyID     int64    `json:"login_proxy_id"`
	OrganizationID   string   `json:"organization_id,omitempty"`
	WorkspaceMode    string   `json:"workspace_mode,omitempty"`
	VerifiedModelIDs []string `json:"verified_model_ids,omitempty"`
	PreparedID       int64    `json:"prepared_id,omitempty"`
}

type KiloPoolImportInput struct {
	RequestID         string               `json:"request_id"`
	BindingStrategy   string               `json:"binding_strategy"`
	Items             []KiloPoolImportItem `json:"items"`
	GroupIDs          []int64              `json:"group_ids"`
	Routing           KiloRoutingOptions   `json:"routing"`
	ActivateWhenReady bool                 `json:"activate_when_ready"`
}

// ClearSecrets drops request-held references on every handler exit, including a
// partially decoded request. Plaintext is never part of the persisted receipt.
func (input *KiloPoolImportInput) ClearSecrets() {
	for i := range input.Items {
		input.Items[i].Password = ""
		input.Items[i].TOTPSecret = ""
	}
}

func normalizeKiloPoolImport(input *KiloPoolImportInput) (*KiloOnboardingBatchInput, error) {
	if err := kiloID(input.RequestID); err != nil {
		return nil, err
	}
	if len(input.Items) < 1 || len(input.Items) > 100 {
		return nil, kiloBad("KILO_ONBOARDING_LIMIT", "每批自动接入1至100个来源账号")
	}
	batch := &KiloOnboardingBatchInput{
		RequestID: input.RequestID, BindingStrategy: input.BindingStrategy,
		GroupIDs: input.GroupIDs, Routing: input.Routing, ActivateWhenReady: input.ActivateWhenReady,
		Items: make([]KiloOnboardingItem, len(input.Items)),
	}
	seen := make(map[string]bool, len(input.Items))
	for i := range input.Items {
		item := &input.Items[i]
		email, err := normalizeReauthEmail(item.Email)
		if err != nil {
			return nil, err
		}
		if seen[email] {
			return nil, kiloBad("KILO_IMPORT_EMAIL_DUPLICATE", "同一批次不能重复导入相同邮箱")
		}
		seen[email] = true
		item.Email = email
		if item.Password == "" {
			return nil, infraerrors.BadRequest("OPENAI_REAUTH_PASSWORD_REQUIRED", "请提供登录密码")
		}
		// Match password_totp: preserve the password byte-for-byte and allow an
		// absent TOTP seed; the existing login engine owns challenge validation.
		item.TOTPSecret = strings.TrimSpace(item.TOTPSecret)
		item.Name = strings.TrimSpace(item.Name)
		if item.Name == "" {
			item.Name = "Kilo " + email
			if utf8.RuneCountInString(item.Name) > 256 {
				item.Name = string([]rune(item.Name)[:256])
			}
		}
		batch.Items[i] = KiloOnboardingItem{
			// Temporary line IDs let the shared validator reject routing/model/
			// slot errors before writes; real source IDs replace them in the tx.
			SourceAccountID: int64(i + 1), Name: item.Name, LoginProxyID: item.LoginProxyID,
			OrganizationID: item.OrganizationID, WorkspaceMode: item.WorkspaceMode, VerifiedModelIDs: item.VerifiedModelIDs, PreparedID: item.PreparedID,
		}
	}
	if err := normalizeKiloOnboarding(batch); err != nil {
		return nil, err
	}
	input.BindingStrategy, input.GroupIDs, input.Routing = batch.BindingStrategy, batch.GroupIDs, batch.Routing
	for i, normalized := range batch.Items {
		input.Items[i].Name = normalized.Name
		input.Items[i].OrganizationID = normalized.OrganizationID
		input.Items[i].WorkspaceMode = normalized.WorkspaceMode
		input.Items[i].VerifiedModelIDs = normalized.VerifiedModelIDs
	}
	return batch, nil
}

func kiloPoolImportSource(item KiloPoolImportItem) (*Account, error) {
	extra := map[string]any{"kilo_quota_only": true}
	source, err := buildAccountForCreate(&CreateAccountInput{
		Name: item.Name, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"email": item.Email}, Extra: extra,
		Concurrency: 1, Priority: 1, SkipDefaultGroupBind: true,
	}, extra)
	if err != nil {
		return nil, err
	}
	// The source exists only for official identity/quota reads, even before its
	// onboarding job runs. The slot owner chooses and pins the final login proxy.
	source.Schedulable = false
	return source, nil
}

// ImportOnboarding commits quota-only sources, encrypted login configurations,
// slot reservations, jobs and the replay receipt as one administrator operation.
func (s *KiloService) ImportOnboarding(ctx context.Context, input KiloPoolImportInput) (*KiloOnboardingBatchResult, error) {
	batch, err := normalizeKiloPoolImport(&input)
	if err != nil {
		return nil, err
	}
	if !s.encryptionReady() {
		return nil, kiloBad("KILO_ENCRYPTION", "请先配置服务器凭据加密密钥")
	}
	if s.reauth == nil || s.reauth.encryptor == nil {
		return nil, infraerrors.ServiceUnavailable("EXECUTOR_UNAVAILABLE", "请先配置原2FA登录材料服务")
	}
	if err = s.reauth.ensureDurableEncryption(); err != nil {
		return nil, err
	}
	if err = s.admin.ValidateAccountGroupBindings(ctx, batch.GroupIDs); err != nil {
		return nil, err
	}
	for _, id := range batch.GroupIDs {
		group, readErr := s.groups.GetByIDLite(ctx, id)
		if readErr != nil {
			return nil, readErr
		}
		if group.Platform != PlatformOpenAI || group.Status != StatusActive {
			return nil, kiloBad("KILO_GROUP_INVALID", "目标分组须为启用的OpenAI分组")
		}
	}
	// Only the digest of this secret-bearing input is persisted. The batch
	// payload below contains source IDs and routing options, never login secrets.
	hash := kiloHash(input)
	var existingHash string
	var existing []byte
	err = s.db.QueryRowContext(ctx, `SELECT payload_hash,result FROM pk_kilo_import_requests WHERE request_id=$1`, input.RequestID).Scan(&existingHash, &existing)
	if err == nil {
		if existingHash != hash {
			return nil, kiloConflict("同一请求编号不能用于不同的导入内容")
		}
		var result KiloOnboardingBatchResult
		if err = json.Unmarshal(existing, &result); err != nil {
			return nil, err
		}
		return &result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err = s.preflightOnboardingProxies(ctx, *batch); err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Client().ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloBatchLock); err != nil {
		return nil, err
	}
	err = kiloScan(ctx, tx.Client(), `SELECT payload_hash,result FROM pk_kilo_import_requests WHERE request_id=$1`, []any{input.RequestID}, &existingHash, &existing)
	if err == nil {
		if existingHash != hash {
			return nil, kiloConflict("同一请求编号不能用于不同的导入内容")
		}
		var result KiloOnboardingBatchResult
		if err = json.Unmarshal(existing, &result); err != nil {
			return nil, err
		}
		return &result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
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
	for i, item := range input.Items {
		passwordCiphertext, encryptErr := s.reauth.encryptor.Encrypt(item.Password)
		if encryptErr != nil {
			return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_CONFIG_ENCRYPT_FAILED", "登录密码加密失败")
		}
		var totpCiphertext string
		if item.TOTPSecret != "" {
			totpCiphertext, encryptErr = s.reauth.encryptor.Encrypt(item.TOTPSecret)
			if encryptErr != nil {
				return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_CONFIG_ENCRYPT_FAILED", "二步验证密钥加密失败")
			}
		}
		source, createErr := kiloPoolImportSource(item)
		if createErr != nil {
			return nil, createErr
		}
		if err = s.accounts.Create(txctx, source); err != nil {
			return nil, err
		}
		if _, err = tx.Client().ExecContext(ctx, `INSERT INTO openai_oauth_reauth_configs(account_id,login_email,credential_mode,engine,proxy_source,proxy_id,password_ciphertext,totp_secret_ciphertext,otp_url_ciphertext) VALUES($1,$2,$3,$4,$5,NULL,$6,NULLIF($7,''),NULL)`, source.ID, item.Email, OpenAIOAuthReauthModePasswordTOTP, OpenAIOAuthReauthEngineLocal, OpenAIOAuthReauthProxySourceAccount, passwordCiphertext, totpCiphertext); err != nil {
			return nil, err
		}
		// Enroll only this newly created source, retaining the credential owner's
		// default probe schedule and retry budget without resetting other rows.
		if _, err = tx.Client().ExecContext(ctx, `INSERT INTO account_token_guard_v2_accounts(account_id,enabled,auto_relogin_enabled) VALUES($1,true,true)`, source.ID); err != nil {
			return nil, err
		}
		batch.Items[i].SourceAccountID = source.ID
	}
	result, err := s.submitKiloOnboardingTx(ctx, tx, *batch)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Client().ExecContext(ctx, `INSERT INTO pk_kilo_import_requests(request_id,payload_hash,result) VALUES($1,$2,$3::jsonb)`, input.RequestID, hash, string(encoded)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

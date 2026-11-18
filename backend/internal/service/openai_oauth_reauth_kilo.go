package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// KiloSourceRecoveryResult describes only the source credentials observed by
// this call. The pool must compare their identity again when changing a binding.
type KiloSourceRecoveryResult struct {
	State                   string
	TaskID                  int64
	Reason                  string
	AccessTokenDigest       string
	RecoveryGeneration      string
	ExpectedCredentialsHash string
}

// EnsureKiloSourceRecovery uses the existing inspection lease and automatic
// re-login budget. Call it without holding Kilo or account transaction locks.
func (s *AccountTokenGuardV2Service) EnsureKiloSourceRecovery(ctx context.Context, sourceID int64) (KiloSourceRecoveryResult, error) {
	result := KiloSourceRecoveryResult{State: "pending"}
	if s == nil || s.repo == nil || s.accounts == nil || s.reauth == nil {
		return result, infraerrors.ServiceUnavailable("KILO_SOURCE_RECOVERY_UNAVAILABLE", "来源账号自动恢复服务暂不可用")
	}
	account, err := s.accounts.GetAccount(ctx, sourceID)
	if err != nil {
		return result, err
	}
	result, err = kiloSourceRecoverySnapshot(account, sourceID)
	if err != nil || result.State == "recovered" || result.Reason == "source_account_ineligible" {
		return result, err
	}
	observedSnapshot := result
	// A token can be refreshed while task/configuration reads are in flight.
	// Never return a destructive verdict for a superseded rejection snapshot.
	finish := func(candidate KiloSourceRecoveryResult) (KiloSourceRecoveryResult, error) {
		current, readErr := s.accounts.GetAccount(ctx, sourceID)
		if readErr != nil {
			result.State, result.Reason = "pending", "source_read_failed"
			return result, readErr
		}
		latest, readErr := kiloSourceRecoverySnapshot(current, sourceID)
		if readErr != nil {
			return KiloSourceRecoveryResult{State: "pending", Reason: "source_read_failed"}, readErr
		}
		if latest.ExpectedCredentialsHash != observedSnapshot.ExpectedCredentialsHash || latest.RecoveryGeneration != observedSnapshot.RecoveryGeneration || latest.State != observedSnapshot.State {
			if latest.State == "pending" {
				latest.Reason = "source_recovery_changed"
			}
			return latest, nil
		}
		return candidate, nil
	}
	if task, taskErr := s.kiloSourceRecoveryTask(ctx, account, result); taskErr != nil {
		result.State = "pending"
		return result, taskErr
	} else if task != nil {
		if task.Reason != "automatic_relogin_retry_pending" {
			return finish(*task)
		}
		result.TaskID = task.TaskID
	}
	if result.State == "blocked" {
		return finish(result)
	}
	record, err := s.repo.GetAccount(ctx, sourceID)
	if err != nil {
		return result, err
	}
	if record == nil || !record.Enabled || !record.AutoReloginEnabled {
		result.State, result.Reason = "blocked", "automatic_relogin_disabled"
		return finish(result)
	}
	if record.AutoReloginExhausted {
		result.State, result.Reason = "blocked", "budget_exhausted"
		return finish(result)
	}
	if _, ok := s.repo.(AccountReloginBudgetRepository); !ok {
		return result, infraerrors.ServiceUnavailable("KILO_SOURCE_RECOVERY_BUDGET_UNAVAILABLE", "来源账号自动恢复预算暂不可用")
	}
	if err := s.reauth.ensureReady(); err != nil {
		return result, err
	}
	if err := s.reauth.ensureDurableEncryption(); err != nil {
		if infraerrors.Reason(err) == "OPENAI_REAUTH_ENCRYPTION_KEY_REQUIRED" {
			result.State, result.Reason = "blocked", "relogin_encryption_key_missing"
			return finish(result)
		}
		return result, err
	}
	config, err := s.reauth.repo.GetConfig(ctx, sourceID)
	if err != nil {
		return result, err
	}
	if config == nil || config.AccountID != sourceID || config.CredentialMode != OpenAIOAuthReauthModePasswordTOTP {
		result.State, result.Reason = "blocked", "password_totp_configuration_missing"
		return finish(result)
	}
	view, err := s.reauth.configView(ctx, config)
	if err != nil {
		switch infraerrors.Reason(err) {
		case "OPENAI_REAUTH_CONFIG_DECRYPT_FAILED", "OPENAI_REAUTH_PROXY_SOURCE_INVALID", "OPENAI_REAUTH_MODE_INVALID":
			result.State, result.Reason = "blocked", "relogin_configuration_invalid"
			return finish(result)
		default:
			return result, err
		}
	}
	if view == nil || !view.Configured {
		result.State, result.Reason = "blocked", "relogin_credentials_missing"
		return finish(result)
	}
	now := s.clockNow()
	if record.CooldownUntil != nil && record.CooldownUntil.After(now) {
		result.Reason = "automatic_relogin_cooling_down"
		return finish(result)
	}
	if record.NextProbeAt.After(now) {
		result.Reason = "automatic_relogin_retry_scheduled"
		return finish(result)
	}
	claimed, err := s.repo.ClaimAccount(ctx, sourceID, s.newProbeOwner(), accountTokenGuardV2LeaseDuration)
	if err != nil {
		return result, err
	}
	if claimed == nil {
		result.Reason = "source_inspection_active"
		return finish(result)
	}
	// Configuration/task reads can outlive a refresh. Recheck after claiming
	// the lease, before the existing probe is allowed to queue a new login.
	beforeProbe, checkErr := finish(result)
	if checkErr != nil || beforeProbe.State != "pending" || beforeProbe.ExpectedCredentialsHash != observedSnapshot.ExpectedCredentialsHash || beforeProbe.RecoveryGeneration != observedSnapshot.RecoveryGeneration {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if releaseErr := s.repo.ReleaseProbe(releaseCtx, sourceID, claimed.LeaseOwner); releaseErr != nil {
			beforeProbe.State, beforeProbe.Reason = "pending", "source_inspection_release_failed"
			return beforeProbe, releaseErr
		}
		return beforeProbe, checkErr
	}
	// runProbe owns fail thresholds, refresh-before-relogin, reconciliation,
	// worker readiness, automatic insertion, cooldown and fenced lease release.
	if err := s.runProbe(ctx, *claimed); err != nil {
		return result, err
	}
	current, err := s.accounts.GetAccount(ctx, sourceID)
	if err != nil {
		return result, err
	}
	snapshot, err := kiloSourceRecoverySnapshot(current, sourceID)
	if err != nil || snapshot.State != "pending" {
		return snapshot, err
	}
	if snapshot.ExpectedCredentialsHash != result.ExpectedCredentialsHash || snapshot.RecoveryGeneration != result.RecoveryGeneration {
		snapshot.Reason = "source_recovery_changed"
		return snapshot, nil
	}
	if task, taskErr := s.kiloSourceRecoveryTask(ctx, current, result); taskErr != nil {
		return result, taskErr
	} else if task != nil {
		if task.Reason != "automatic_relogin_retry_pending" {
			return finish(*task)
		}
		result.TaskID = task.TaskID
	}
	record, err = s.repo.GetAccount(ctx, sourceID)
	if err != nil {
		return result, err
	}
	if record == nil || !record.Enabled || !record.AutoReloginEnabled {
		result.State, result.Reason = "blocked", "automatic_relogin_disabled"
	} else if record.AutoReloginExhausted {
		result.State, result.Reason = "blocked", "budget_exhausted"
	} else {
		result.Reason = "automatic_relogin_retry_pending"
	}
	return finish(result)
}

func kiloSourceRecoverySnapshot(account *Account, sourceID int64) (KiloSourceRecoveryResult, error) {
	result := KiloSourceRecoveryResult{State: "pending"}
	if account == nil || account.ID != sourceID || !account.IsOpenAIOAuth() || account.IsCredentialShadow() || account.IsOpenAIPersonalAccessToken() || account.IsOpenAIAgentIdentity() || account.IsExcelOAuth() {
		result.State, result.Reason = "blocked", "source_account_ineligible"
		return result, nil
	}
	var err error
	result.ExpectedCredentialsHash, err = hashReauthCredentials(account.Credentials)
	if err != nil {
		return result, err
	}
	result.AccessTokenDigest = OpenAIAccessTokenDigest(account.GetOpenAIAccessToken())
	result.RecoveryGeneration = OpenAIAuthRecoveryGeneration(account)
	if !account.IsActive() {
		result.State, result.Reason = "blocked", "source_inactive"
	} else if strings.TrimSpace(account.GetOpenAIAccessToken()) == "" {
		result.State, result.Reason = "blocked", "source_access_token_missing"
	} else if !OpenAIAuthRecoveryPending(account) {
		result.State, result.Reason = "recovered", "source_authorization_recovered"
	}
	return result, nil
}

func (s *AccountTokenGuardV2Service) kiloSourceRecoveryTask(ctx context.Context, account *Account, result KiloSourceRecoveryResult) (*KiloSourceRecoveryResult, error) {
	state, _ := account.Extra[OpenAIAuthRecoveryKey].(map[string]any)
	observed, _ := state["observed_at"].(string)
	active, err := s.reauth.activeKiloQuotaOAuthTask(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	if active != nil {
		result.State = "pending"
		result.TaskID, result.Reason = active.ID, "automatic_relogin_active"
		if active.ExpectedCredentialsHash != result.ExpectedCredentialsHash {
			result.Reason = "previous_credentials_relogin_active"
		}
		return &result, nil
	}
	task, err := s.reauth.repo.GetLatestTask(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	if task == nil || task.AccountID != account.ID || reauthTaskProfile(task) != "codex" || task.ExpectedCredentialsHash != result.ExpectedCredentialsHash || task.Status != OpenAIOAuthReauthStatusFailed || task.FinishedAt == nil {
		return nil, nil
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observed)
	if err != nil || task.FinishedAt.Before(observedAt) {
		return nil, nil // Historical failure cannot decide a later rejection.
	}
	view := safeReauthTask(task)
	result.TaskID, result.Reason = task.ID, "automatic_relogin_retry_pending"
	if view.Failure != nil {
		switch view.Failure.Code {
		case "phone_verification_required", "account_deactivated", "account_deleted", "account_retired", "requires_relogin", "password_rejected", "mfa_required", "security_check", "authorization_denied":
			result.State, result.Reason = "failed", view.Failure.Code
		}
	}
	if view.Diagnostic != nil && view.Diagnostic.Identity != nil {
		result.State, result.Reason = "failed", "relogin_identity_validation_failed"
	}
	if result.State == "failed" {
		return &result, nil
	}
	return &result, nil // Transport, timeout and unknown failures retain the budget retry path.
}

// KiloLoginMaterial is consumed only in memory by the authenticated onboarding
// executor. It must never be included in administrator DTOs, task options or logs.
type KiloLoginMaterial struct {
	Email      string `json:"-"`
	Password   string `json:"-"`
	TOTPSecret string `json:"-"`
	Version    string `json:"-"`
}

// ReadKiloLoginMaterial reads the existing encrypted login configuration without
// creating or claiming an OpenAI task. OAuth token refreshes do not change its
// version, so the quota-only authorization can finish before Kilo login starts.
func (s *OpenAIOAuthReauthService) ReadKiloLoginMaterial(ctx context.Context, accountID int64) (*KiloLoginMaterial, error) {
	if s == nil || s.repo == nil || s.accounts == nil || s.encryptor == nil {
		return nil, infraerrors.ServiceUnavailable("KILO_LOGIN_MATERIAL_UNAVAILABLE", "账号登录材料暂不可用")
	}
	if err := s.ensureDurableEncryption(); err != nil {
		return nil, err
	}
	if _, err := s.accountFor(ctx, accountID); err != nil {
		return nil, err
	}
	stored, err := s.repo.GetConfig(ctx, accountID)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "KILO_LOGIN_MATERIAL_READ_FAILED", "读取账号登录材料失败")
	}
	if stored == nil || stored.AccountID != accountID || strings.TrimSpace(stored.CredentialMode) != OpenAIOAuthReauthModePasswordTOTP {
		return nil, infraerrors.BadRequest("KILO_LOGIN_MATERIAL_REQUIRED", "请先保存来源账号的密码与二步验证登录配置")
	}
	email, err := normalizeReauthEmail(stored.LoginEmail)
	if err != nil {
		return nil, infraerrors.BadRequest("KILO_LOGIN_EMAIL_INVALID", "来源账号保存的登录邮箱无效")
	}
	password, err := s.decryptReauthSecret(stored.PasswordCiphertext, "保存的登录密码无法解密")
	if err != nil {
		return nil, err
	}
	if password == "" {
		return nil, infraerrors.BadRequest("KILO_LOGIN_MATERIAL_REQUIRED", "请先保存来源账号的登录密码")
	}
	totp, err := s.decryptReauthSecret(stored.TOTPSecretCiphertext, "保存的二步验证密钥无法解密")
	if err != nil {
		return nil, err
	}
	// Preserve optional TOTP semantics from the existing password_totp flow.
	// The ciphertext and timestamp, rather than plaintext secrets or OAuth tokens,
	// identify the saved configuration generation.
	raw, _ := json.Marshal([]string{stored.LoginEmail, stored.PasswordCiphertext, stored.TOTPSecretCiphertext, stored.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	digest := sha256.Sum256(raw)
	return &KiloLoginMaterial{Email: email, Password: password, TOTPSecret: totp, Version: hex.EncodeToString(digest[:])}, nil
}

// EnsureKiloQuotaOAuth drives only the normal codex authorization queue. The
// caller persists nextTaskID before retrying and must wait for ready before any
// Kilo remote login. Source inference is blocked by its durable quota-only flag.
func (s *OpenAIOAuthReauthService) EnsureKiloQuotaOAuth(ctx context.Context, accountID, taskID int64) (nextTaskID int64, ready bool, err error) {
	if s == nil || s.repo == nil || s.accounts == nil {
		return 0, false, infraerrors.ServiceUnavailable("KILO_QUOTA_OAUTH_UNAVAILABLE", "官方额度授权服务暂不可用")
	}
	if taskID < 0 {
		return 0, false, infraerrors.BadRequest("KILO_QUOTA_OAUTH_TASK_INVALID", "官方额度授权任务编号无效")
	}
	account, err := s.accountFor(ctx, accountID)
	if err != nil {
		return 0, false, err
	}
	if account.Extra["kilo_quota_only"] != true {
		return 0, false, infraerrors.Conflict("KILO_QUOTA_ONLY_REQUIRED", "来源账号尚未切换为官方信息与额度只读用途")
	}
	if taskID > 0 {
		record, readErr := s.repo.GetTask(ctx, taskID)
		if readErr != nil {
			return taskID, false, infraerrors.New(http.StatusInternalServerError, "KILO_QUOTA_OAUTH_TASK_READ_FAILED", "读取官方额度授权任务失败")
		}
		return kiloQuotaOAuthTaskState(record, accountID, taskID)
	}
	// An active task may predate the onboarding job, or may have been created
	// just before a worker lost its lease. Reuse it instead of logging in twice.
	record, err := s.activeKiloQuotaOAuthTask(ctx, accountID)
	if err != nil {
		return 0, false, err
	}
	if record != nil {
		return kiloQuotaOAuthTaskState(record, accountID, record.ID)
	}
	task, err := s.CreateTask(ctx, accountID)
	if err != nil {
		if infraerrors.Reason(err) == "OPENAI_REAUTH_TASK_ALREADY_ACTIVE" {
			// Another creator can win between the read and the unique insert.
			if record, readErr := s.activeKiloQuotaOAuthTask(ctx, accountID); readErr != nil {
				return 0, false, readErr
			} else if record != nil {
				return kiloQuotaOAuthTaskState(record, accountID, record.ID)
			}
		}
		return 0, false, err
	}
	if task == nil || task.ID <= 0 || task.AccountID != accountID || task.OAuthProfile != "codex" {
		return 0, false, infraerrors.Conflict("KILO_QUOTA_OAUTH_TASK_INVALID", "官方额度授权任务身份不匹配")
	}
	return task.ID, false, nil
}

func (s *OpenAIOAuthReauthService) activeKiloQuotaOAuthTask(ctx context.Context, accountID int64) (*OpenAIOAuthReauthTaskRecord, error) {
	var record *OpenAIOAuthReauthTaskRecord
	var err error
	if reader, ok := s.repo.(interface {
		GetActiveCodexTask(context.Context, int64) (*OpenAIOAuthReauthTaskRecord, error)
	}); ok {
		record, err = reader.GetActiveCodexTask(ctx, accountID)
	} else {
		// Small test/in-memory repositories can expose only the latest task.
		record, err = s.repo.GetLatestTask(ctx, accountID)
	}
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "KILO_QUOTA_OAUTH_TASK_READ_FAILED", "读取官方额度授权任务失败")
	}
	if record == nil || (record.Status != OpenAIOAuthReauthStatusQueued && record.Status != OpenAIOAuthReauthStatusRunning && record.Status != OpenAIOAuthReauthStatusCallbackProcessing) {
		return nil, nil
	}
	if record.AccountID != accountID || (record.OAuthProfile != "" && record.OAuthProfile != "codex") {
		return nil, infraerrors.Conflict("KILO_QUOTA_OAUTH_TASK_INVALID", "已有授权任务不属于来源账号的官方额度授权")
	}
	return record, nil
}

func kiloQuotaOAuthTaskState(record *OpenAIOAuthReauthTaskRecord, accountID, taskID int64) (int64, bool, error) {
	if record == nil || taskID <= 0 || record.ID != taskID || record.AccountID != accountID || (record.OAuthProfile != "" && record.OAuthProfile != "codex") {
		return taskID, false, infraerrors.Conflict("KILO_QUOTA_OAUTH_TASK_INVALID", "官方额度授权任务不存在或身份不匹配")
	}
	switch record.Status {
	case OpenAIOAuthReauthStatusQueued, OpenAIOAuthReauthStatusRunning, OpenAIOAuthReauthStatusCallbackProcessing:
		return taskID, false, nil
	case OpenAIOAuthReauthStatusSucceeded:
		return taskID, true, nil
	case OpenAIOAuthReauthStatusFailed:
		if failure := kiloQuotaOAuthInteractionFailure(record.Error); failure != nil {
			return taskID, false, failure
		}
		// Never copy worker errors here: they can contain remote response text.
		return taskID, false, infraerrors.Conflict("KILO_QUOTA_OAUTH_FAILED", "官方额度授权失败，请检查原重登任务的诊断后重试接入")
	default:
		return taskID, false, infraerrors.Conflict("KILO_QUOTA_OAUTH_TASK_INVALID", "官方额度授权任务状态无效")
	}
}

func kiloQuotaOAuthInteractionFailure(raw string) *kiloOnboardingFailure {
	_, diagnostic, _ := localProtocolError(raw)
	if diagnostic == nil {
		return nil
	}
	switch diagnostic.Reason {
	case "email_verification_required":
		return &kiloOnboardingFailure{code: "KILO_QUOTA_OAUTH_EMAIL_VERIFICATION_REQUIRED", message: "OpenAI 官方登录要求邮箱验证码，自动接入已停止", action: "如需全自动接入，请改用无需邮箱验证码的账号"}
	case "interaction_required":
		return &kiloOnboardingFailure{code: "KILO_QUOTA_OAUTH_INTERACTION_REQUIRED", message: "OpenAI 额度登录遇到需要输入的步骤，自动接入已停止", action: "诊断未区分输入类型，请核对 OpenAI 官方登录提示"}
	}
	return nil
}

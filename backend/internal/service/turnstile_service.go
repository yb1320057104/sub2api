package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

var (
	ErrTurnstileVerificationFailed = infraerrors.BadRequest("TURNSTILE_VERIFICATION_FAILED", "turnstile verification failed")
	ErrTurnstileNotConfigured      = infraerrors.ServiceUnavailable("TURNSTILE_NOT_CONFIGURED", "turnstile not configured")
	ErrTurnstileInvalidSecretKey   = infraerrors.BadRequest("TURNSTILE_INVALID_SECRET_KEY", "invalid turnstile secret key")
)

// TurnstileVerifier 验证 Turnstile token 的接口
type TurnstileVerifier interface {
	VerifyToken(ctx context.Context, secretKey, token, remoteIP string) (*TurnstileVerifyResponse, error)
}

// TurnstileService Turnstile 验证服务
type TurnstileService struct {
	settingService *SettingService
	verifier       TurnstileVerifier
	expectedHost   string
}

// TurnstileVerifyResponse Cloudflare Turnstile 验证响应
type TurnstileVerifyResponse struct {
	Success     bool     `json:"success"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
	ErrorCodes  []string `json:"error-codes"`
	Action      string   `json:"action"`
	CData       string   `json:"cdata"`
}

// NewTurnstileService 创建 Turnstile 服务实例
func NewTurnstileService(settingService *SettingService, verifier TurnstileVerifier) *TurnstileService {
	return NewTurnstileServiceWithConfig(settingService, verifier, config.TurnstileConfig{})
}

// NewTurnstileServiceWithConfig wires the deployment-level hostname policy.
// Site and secret keys remain in the settings store; the expected hostname is
// deliberately kept in server configuration so it is never exposed to users.
func NewTurnstileServiceWithConfig(settingService *SettingService, verifier TurnstileVerifier, cfg config.TurnstileConfig) *TurnstileService {
	return &TurnstileService{
		settingService: settingService,
		verifier:       verifier,
		expectedHost:   normalizeTurnstileHostname(cfg.ExpectedHostname),
	}
}

// VerifyToken 验证 Turnstile token
func (s *TurnstileService) VerifyToken(ctx context.Context, token string, remoteIP string) error {
	if s == nil || s.settingService == nil {
		return ErrTurnstileNotConfigured
	}
	// 检查是否启用 Turnstile
	if !s.settingService.IsTurnstileEnabled(ctx) {
		logger.LegacyPrintf("service.turnstile", "%s", "[Turnstile] Disabled, skipping verification")
		return nil
	}

	// 获取 Secret Key
	secretKey := s.settingService.GetTurnstileSecretKey(ctx)
	return s.VerifyTokenWithSecret(ctx, secretKey, token, remoteIP)
}

func (s *TurnstileService) VerifyTokenWithSecret(ctx context.Context, secretKey, token, remoteIP string) error {
	return s.VerifyTokenWithSecretAndAction(ctx, secretKey, token, remoteIP, "")
}

// VerifyTokenWithSecretAndAction verifies the token and, when supplied, the
// action emitted by the Turnstile widget. The action is intentionally supplied
// by the protected route so one token cannot be replayed across auth actions.
func (s *TurnstileService) VerifyTokenWithSecretAndAction(ctx context.Context, secretKey, token, remoteIP, action string) error {
	if secretKey == "" {
		logger.LegacyPrintf("service.turnstile", "%s", "[Turnstile] Secret key not configured")
		return ErrTurnstileNotConfigured
	}

	// 如果 token 为空，返回错误
	if token == "" {
		logger.LegacyPrintf("service.turnstile", "%s", "[Turnstile] Token is empty")
		return ErrTurnstileVerificationFailed
	}

	logger.LegacyPrintf("service.turnstile", "[Turnstile] Verifying token for IP: %s", remoteIP)
	if s == nil || s.verifier == nil {
		return ErrTurnstileNotConfigured
	}
	result, err := s.verifier.VerifyToken(ctx, secretKey, token, remoteIP)
	if err != nil {
		logger.LegacyPrintf("service.turnstile", "[Turnstile] Request failed: %v", err)
		return fmt.Errorf("send request: %w", err)
	}

	if result == nil {
		return ErrTurnstileVerificationFailed
	}
	if !result.Success {
		logger.LegacyPrintf("service.turnstile", "[Turnstile] Verification failed, error codes: %v", result.ErrorCodes)
		return ErrTurnstileVerificationFailed
	}
	expectedHost := s.expectedHost
	if expectedHost == "" && s.settingService != nil && s.settingService.settingRepo != nil {
		expectedHost = normalizeTurnstileHostname(s.settingService.GetFrontendURL(ctx))
	}
	if expectedAction := strings.TrimSpace(action); expectedAction != "" && expectedHost == "" {
		return ErrTurnstileNotConfigured
	}
	if expectedHost != "" {
		actualHost := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(result.Hostname), "."))
		if actualHost == "" || actualHost != expectedHost {
			logger.LegacyPrintf("service.turnstile", "[Turnstile] Hostname mismatch: expected %s, got %s", expectedHost, result.Hostname)
			return ErrTurnstileVerificationFailed
		}
	}
	if expectedAction := strings.TrimSpace(action); expectedAction != "" && result.Action != expectedAction {
		logger.LegacyPrintf("service.turnstile", "[Turnstile] Action mismatch: expected %s, got %s", expectedAction, result.Action)
		return ErrTurnstileVerificationFailed
	}

	logger.LegacyPrintf("service.turnstile", "%s", "[Turnstile] Verification successful")
	return nil
}

// IsEnabled 检查 Turnstile 是否启用
func (s *TurnstileService) IsEnabled(ctx context.Context) bool {
	if s == nil || s.settingService == nil {
		return false
	}
	return s.settingService.IsTurnstileEnabled(ctx)
}

// ValidateSecretKey 验证 Turnstile Secret Key 是否有效
func (s *TurnstileService) ValidateSecretKey(ctx context.Context, secretKey string) error {
	if s == nil || s.verifier == nil || strings.TrimSpace(secretKey) == "" {
		return ErrTurnstileNotConfigured
	}
	// 发送一个测试token的验证请求来检查secret_key是否有效
	result, err := s.verifier.VerifyToken(ctx, secretKey, "test-validation", "")
	if err != nil {
		return fmt.Errorf("validate secret key: %w", err)
	}

	if result == nil {
		return ErrTurnstileVerificationFailed
	}
	// 检查是否有 invalid-input-secret 错误
	for _, code := range result.ErrorCodes {
		if code == "invalid-input-secret" {
			return ErrTurnstileInvalidSecretKey
		}
	}

	// 其他错误（如 invalid-input-response）说明 secret key 是有效的
	return nil
}

// normalizeTurnstileHostname accepts either a plain hostname from
// turnstile.expected_hostname or a validated absolute frontend URL used as a
// compatibility fallback. It intentionally ignores paths, query strings and
// request headers; the latter are attacker-controlled.
func normalizeTurnstileHostname(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" {
		raw = parsed.Hostname()
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
}

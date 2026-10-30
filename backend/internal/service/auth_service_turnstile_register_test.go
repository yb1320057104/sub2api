//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type turnstileVerifierSpy struct {
	called    int
	lastToken string
	result    *TurnstileVerifyResponse
	err       error
	returnNil bool
}

func (s *turnstileVerifierSpy) VerifyToken(_ context.Context, _ string, token, _ string) (*TurnstileVerifyResponse, error) {
	s.called++
	s.lastToken = token
	if s.err != nil {
		return nil, s.err
	}
	if s.returnNil {
		return nil, nil
	}
	if s.result != nil {
		return s.result, nil
	}
	return &TurnstileVerifyResponse{Success: true, Hostname: "example.com", Action: CaptchaActionRegister}, nil
}

func newAuthServiceForRegisterTurnstileTest(settings map[string]string, verifier TurnstileVerifier) *AuthService {
	cfg := &config.Config{
		Server: config.ServerConfig{
			Mode: "release",
		},
		Turnstile: config.TurnstileConfig{
			Required:         true,
			ExpectedHostname: "example.com",
		},
	}

	settingService := NewSettingService(&settingRepoStub{values: settings}, cfg)
	turnstileService := NewTurnstileServiceWithConfig(settingService, verifier, cfg.Turnstile)

	return NewAuthService(
		nil, // entClient
		&userRepoStub{},
		nil, // redeemRepo
		nil, // refreshTokenCache
		cfg,
		settingService,
		nil, // emailService
		turnstileService,
		nil, // emailQueueService
		nil, // promoService
		nil, // defaultSubAssigner
		nil, // affiliateService
		nil, // userPlatformQuotaRepo
	)
}

func TestAuthService_VerifyTurnstileForRegister_SkipWhenEmailVerifyCodeProvided(t *testing.T) {
	verifier := &turnstileVerifierSpy{}
	service := newAuthServiceForRegisterTurnstileTest(map[string]string{
		SettingKeyEmailVerifyEnabled:  "true",
		SettingKeyTurnstileEnabled:    "true",
		SettingKeyTurnstileSecretKey:  "secret",
		SettingKeyRegistrationEnabled: "true",
	}, verifier)

	err := service.VerifyTurnstileForRegister(context.Background(), "", "127.0.0.1", "123456")
	require.NoError(t, err)
	require.Equal(t, 0, verifier.called)
}

func TestAuthService_VerifyTurnstileForRegister_RequireWhenVerifyCodeMissing(t *testing.T) {
	verifier := &turnstileVerifierSpy{}
	service := newAuthServiceForRegisterTurnstileTest(map[string]string{
		SettingKeyEmailVerifyEnabled: "true",
		SettingKeyTurnstileEnabled:   "true",
		SettingKeyTurnstileSecretKey: "secret",
	}, verifier)

	err := service.VerifyTurnstileForRegister(context.Background(), "", "127.0.0.1", "")
	require.ErrorIs(t, err, ErrTurnstileVerificationFailed)
}

func TestAuthService_VerifyTurnstileForRegister_NoSkipWhenEmailVerifyDisabled(t *testing.T) {
	verifier := &turnstileVerifierSpy{}
	service := newAuthServiceForRegisterTurnstileTest(map[string]string{
		SettingKeyEmailVerifyEnabled: "false",
		SettingKeyTurnstileEnabled:   "true",
		SettingKeyTurnstileSecretKey: "secret",
	}, verifier)

	err := service.VerifyTurnstileForRegister(context.Background(), "turnstile-token", "127.0.0.1", "123456")
	require.NoError(t, err)
	require.Equal(t, 1, verifier.called)
	require.Equal(t, "turnstile-token", verifier.lastToken)
}

func TestTurnstileServiceVerifiesHostnameAndAction(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hostname string
		action   string
		wantErr  bool
	}{
		{name: "matching", hostname: "example.com", action: CaptchaActionLogin},
		{name: "normalized hostname", hostname: "EXAMPLE.COM.", action: CaptchaActionLogin},
		{name: "missing hostname", action: CaptchaActionLogin, wantErr: true},
		{name: "wrong hostname", hostname: "other.example.com", action: CaptchaActionLogin, wantErr: true},
		{name: "wrong action", hostname: "example.com", action: CaptchaActionRegister, wantErr: true},
		{name: "missing action", hostname: "example.com", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier := &turnstileVerifierSpy{result: &TurnstileVerifyResponse{Success: true, Hostname: tc.hostname, Action: tc.action}}
			svc := NewTurnstileServiceWithConfig(nil, verifier, config.TurnstileConfig{ExpectedHostname: "example.com"})
			err := svc.VerifyTokenWithSecretAndAction(context.Background(), "secret", "token", "127.0.0.1", CaptchaActionLogin)
			if tc.wantErr {
				require.ErrorIs(t, err, ErrTurnstileVerificationFailed)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTurnstileServiceActionRequiresHostnameConfiguration(t *testing.T) {
	svc := NewTurnstileService(nil, &turnstileVerifierSpy{})
	require.ErrorIs(t, svc.VerifyTokenWithSecretAndAction(context.Background(), "secret", "token", "127.0.0.1", CaptchaActionLogin), ErrTurnstileNotConfigured)
}

func TestTurnstileServiceRejectsNilProviderResult(t *testing.T) {
	svc := NewTurnstileServiceWithConfig(nil, &turnstileVerifierSpy{returnNil: true}, config.TurnstileConfig{ExpectedHostname: "example.com"})
	require.ErrorIs(t, svc.VerifyTokenWithSecretAndAction(context.Background(), "secret", "token", "127.0.0.1", CaptchaActionLogin), ErrTurnstileVerificationFailed)
}

func TestProvideTurnstileServiceFallsBackToFrontendURLHostname(t *testing.T) {
	verifier := &turnstileVerifierSpy{result: &TurnstileVerifyResponse{
		Success:  true,
		Hostname: "EXAMPLE.COM.",
		Action:   CaptchaActionLogin,
	}}
	cfg := &config.Config{Server: config.ServerConfig{FrontendURL: "https://example.com/auth"}}
	svc := ProvideTurnstileService(nil, verifier, cfg)

	require.NoError(t, svc.VerifyTokenWithSecretAndAction(
		context.Background(), "secret", "token", "127.0.0.1", CaptchaActionLogin,
	))
}

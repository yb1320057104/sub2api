package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type kiloRecoveryTestRepo struct {
	*reauthTestRepo
	active     *OpenAIOAuthReauthTaskRecord
	readErr    error
	onReadTask func()
}

func (r *kiloRecoveryTestRepo) GetActiveCodexTask(context.Context, int64) (*OpenAIOAuthReauthTaskRecord, error) {
	if r.readErr != nil {
		return nil, r.readErr
	}
	if r.active != nil {
		return r.active, nil
	}
	if r.task != nil && (r.task.Status == OpenAIOAuthReauthStatusQueued || r.task.Status == OpenAIOAuthReauthStatusRunning || r.task.Status == OpenAIOAuthReauthStatusCallbackProcessing) {
		return r.task, nil
	}
	return nil, nil
}

func (r *kiloRecoveryTestRepo) GetLatestTask(context.Context, int64) (*OpenAIOAuthReauthTaskRecord, error) {
	if r.onReadTask != nil {
		r.onReadTask()
	}
	return r.task, r.readErr
}

type kiloRecoveryTestProber struct {
	err   error
	calls int
	onRun func()
}

func (p *kiloRecoveryTestProber) FetchOpenAIModelsListFresh(context.Context, *Account) (*OpenAIModelsResponse, error) {
	p.calls++
	if p.onRun != nil {
		p.onRun()
	}
	return nil, p.err
}

func newKiloRecoveryFixture(t *testing.T) (*AccountTokenGuardV2Service, *reauthTestAccountReader, *kiloRecoveryTestRepo, *accountTokenGuardV2TestRepo, *kiloRecoveryTestProber) {
	t.Helper()
	reauth, reader, base, _, _, _ := newReauthTestService("acct-1")
	savePasswordReauthConfig(t, reauth)
	now := time.Now()
	reader.account.Status = StatusActive
	reader.account.Extra = map[string]any{OpenAIAuthRecoveryKey: map[string]any{
		"access_token_digest": OpenAIAccessTokenDigest(reader.account.GetOpenAIAccessToken()),
		"generation":          "fault-current", "requires_relogin": true, "observed_at": now.Add(-time.Minute).UTC().Format(time.RFC3339Nano),
	}}
	repo := &kiloRecoveryTestRepo{reauthTestRepo: base}
	reauth.repo = repo
	guardRepo := &accountTokenGuardV2TestRepo{record: &AccountTokenGuardV2Record{
		AccountID: 42, Enabled: true, AutoReloginEnabled: true, NextProbeAt: now.Add(-time.Second),
	}, createTask: base.CreateTask}
	prober := &kiloRecoveryTestProber{err: &codexModelsManifestUpstreamError{statusCode: http.StatusUnauthorized, body: []byte(`{"error":{"code":"token_revoked"}}`)}}
	guard := NewAccountTokenGuardV2Service(guardRepo, nil, reader, prober, reauth)
	guard.now = func() time.Time { return now }
	return guard, reader, repo, guardRepo, prober
}

func TestKiloSourceRecoveryUsesAutomaticBudgetAndReusesCurrentTask(t *testing.T) {
	guard, _, repo, budget, prober := newKiloRecoveryFixture(t)
	result, err := guard.EnsureKiloSourceRecovery(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "pending", result.State)
	require.Equal(t, int64(71), result.TaskID)
	require.Equal(t, "automatic_relogin_active", result.Reason)
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, 1, prober.calls)
	require.NotEmpty(t, result.AccessTokenDigest)
	require.Equal(t, "fault-current", result.RecoveryGeneration)
	require.Empty(t, budget.record.LeaseOwner, "the existing probe must release its fenced lease")
	for _, status := range []string{OpenAIOAuthReauthStatusQueued, OpenAIOAuthReauthStatusRunning, OpenAIOAuthReauthStatusCallbackProcessing} {
		repo.task.Status = status
		again, err := guard.EnsureKiloSourceRecovery(context.Background(), 42)
		require.NoError(t, err)
		require.Equal(t, result.TaskID, again.TaskID)
		require.Equal(t, "pending", again.State)
	}
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, 1, prober.calls)
}

func TestKiloSourceRecoveryInactiveSourceWaitsForActiveTask(t *testing.T) {
	guard, reader, repo, _, prober := newKiloRecoveryFixture(t)
	reader.account.Status = StatusError
	hash, err := hashReauthCredentials(reader.account.Credentials)
	require.NoError(t, err)
	repo.active = &OpenAIOAuthReauthTaskRecord{ID: 91, AccountID: 42, OAuthProfile: "codex", Status: OpenAIOAuthReauthStatusRunning, ExpectedCredentialsHash: hash}
	result, err := guard.EnsureKiloSourceRecovery(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "pending", result.State)
	require.Equal(t, int64(91), result.TaskID)
	require.Equal(t, "automatic_relogin_active", result.Reason)
	repo.active = nil
	result, err = guard.EnsureKiloSourceRecovery(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "blocked", result.State)
	require.Equal(t, "source_inactive", result.Reason)
	require.Zero(t, repo.createCalls)
	require.Zero(t, prober.calls)
}

func TestKiloSourceRecoveryGatesConfigurationBudgetAndSchedule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  string
		reason string
		change func(*AccountTokenGuardV2Service, *kiloRecoveryTestRepo, *accountTokenGuardV2TestRepo)
	}{
		{"not monitored", "blocked", "automatic_relogin_disabled", func(_ *AccountTokenGuardV2Service, _ *kiloRecoveryTestRepo, b *accountTokenGuardV2TestRepo) {
			b.record = nil
		}},
		{"inspection disabled", "blocked", "automatic_relogin_disabled", func(_ *AccountTokenGuardV2Service, _ *kiloRecoveryTestRepo, b *accountTokenGuardV2TestRepo) {
			b.record.Enabled = false
		}},
		{"auto disabled", "blocked", "automatic_relogin_disabled", func(_ *AccountTokenGuardV2Service, _ *kiloRecoveryTestRepo, b *accountTokenGuardV2TestRepo) {
			b.record.AutoReloginEnabled = false
		}},
		{"exhausted", "blocked", "budget_exhausted", func(_ *AccountTokenGuardV2Service, _ *kiloRecoveryTestRepo, b *accountTokenGuardV2TestRepo) {
			b.record.AutoReloginExhausted = true
		}},
		{"key missing", "blocked", "relogin_encryption_key_missing", func(g *AccountTokenGuardV2Service, _ *kiloRecoveryTestRepo, _ *accountTokenGuardV2TestRepo) {
			g.reauth.encryptionKeyConfigured = false
		}},
		{"config missing", "blocked", "password_totp_configuration_missing", func(_ *AccountTokenGuardV2Service, r *kiloRecoveryTestRepo, _ *accountTokenGuardV2TestRepo) {
			r.config = nil
		}},
		{"mailbox mode", "blocked", "password_totp_configuration_missing", func(_ *AccountTokenGuardV2Service, r *kiloRecoveryTestRepo, _ *accountTokenGuardV2TestRepo) {
			r.config.CredentialMode = OpenAIOAuthReauthModeEmailOTPURL
		}},
		{"password missing", "blocked", "relogin_credentials_missing", func(_ *AccountTokenGuardV2Service, r *kiloRecoveryTestRepo, _ *accountTokenGuardV2TestRepo) {
			r.config.PasswordCiphertext = ""
		}},
		{"ciphertext invalid", "blocked", "relogin_configuration_invalid", func(_ *AccountTokenGuardV2Service, r *kiloRecoveryTestRepo, _ *accountTokenGuardV2TestRepo) {
			r.config.PasswordCiphertext = "bad-ciphertext"
		}},
		{"cooldown", "pending", "automatic_relogin_cooling_down", func(g *AccountTokenGuardV2Service, _ *kiloRecoveryTestRepo, b *accountTokenGuardV2TestRepo) {
			until := g.clockNow().Add(time.Minute)
			b.record.CooldownUntil = &until
		}},
		{"scheduled", "pending", "automatic_relogin_retry_scheduled", func(g *AccountTokenGuardV2Service, _ *kiloRecoveryTestRepo, b *accountTokenGuardV2TestRepo) {
			b.record.NextProbeAt = g.clockNow().Add(time.Minute)
		}},
		{"lease busy", "pending", "source_inspection_active", func(_ *AccountTokenGuardV2Service, _ *kiloRecoveryTestRepo, b *accountTokenGuardV2TestRepo) {
			b.claimBusy = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guard, _, repo, budget, prober := newKiloRecoveryFixture(t)
			tc.change(guard, repo, budget)
			result, err := guard.EnsureKiloSourceRecovery(context.Background(), 42)
			require.NoError(t, err)
			require.Equal(t, tc.state, result.State)
			require.Equal(t, tc.reason, result.Reason)
			require.Zero(t, repo.createCalls)
			require.Zero(t, prober.calls)
		})
	}
}

func TestKiloSourceRecoveryTerminalFailureMustBelongToCurrentRejection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  string
		change func(*OpenAIOAuthReauthTaskRecord, *Account)
	}{
		{"current failure", "failed", func(_ *OpenAIOAuthReauthTaskRecord, _ *Account) {}},
		{"older token", "pending", func(task *OpenAIOAuthReauthTaskRecord, _ *Account) {
			task.ExpectedCredentialsHash = "older-credentials"
		}},
		{"older rejection", "pending", func(task *OpenAIOAuthReauthTaskRecord, _ *Account) {
			finished := time.Now().Add(-time.Hour)
			task.FinishedAt = &finished
		}},
		{"no observation", "pending", func(_ *OpenAIOAuthReauthTaskRecord, account *Account) {
			delete(account.Extra[OpenAIAuthRecoveryKey].(map[string]any), "observed_at")
		}},
		{"wrong profile", "pending", func(task *OpenAIOAuthReauthTaskRecord, _ *Account) { task.OAuthProfile = "excel" }},
		{"wrong account", "pending", func(task *OpenAIOAuthReauthTaskRecord, _ *Account) { task.AccountID = 43 }},
		{"needs action is not an OpenAI terminal", "pending", func(task *OpenAIOAuthReauthTaskRecord, _ *Account) { task.Status = "needs_action" }},
		{"cancelled is not an OpenAI terminal", "pending", func(task *OpenAIOAuthReauthTaskRecord, _ *Account) { task.Status = "cancelled" }},
		{"success is not proof current rejection recovered", "pending", func(task *OpenAIOAuthReauthTaskRecord, _ *Account) { task.Status = OpenAIOAuthReauthStatusSucceeded }},
		{"transport error", "pending", func(task *OpenAIOAuthReauthTaskRecord, _ *Account) {
			task.Error = "local_protocol_failed stage=web_login reason=transport_error elapsed_ms=120 exit=1"
		}},
		{"service failure", "pending", func(task *OpenAIOAuthReauthTaskRecord, _ *Account) {
			task.Error = "session_studio_failed stage=http_request reason=http_rejected elapsed_ms=120 http_status=503"
		}},
		{"timeout", "pending", func(task *OpenAIOAuthReauthTaskRecord, _ *Account) { task.Error = "worker timeout" }},
		{"unknown failure", "pending", func(task *OpenAIOAuthReauthTaskRecord, _ *Account) { task.Error = "unknown remote text" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guard, reader, repo, budget, prober := newKiloRecoveryFixture(t)
			hash, err := hashReauthCredentials(reader.account.Credentials)
			require.NoError(t, err)
			finished := guard.clockNow().Add(-time.Second)
			repo.task = &OpenAIOAuthReauthTaskRecord{ID: 91, AccountID: 42, OAuthProfile: "codex", Status: OpenAIOAuthReauthStatusFailed, ExpectedCredentialsHash: hash, FinishedAt: &finished, Error: "local_protocol_failed stage=web_login reason=password_rejected elapsed_ms=120 exit=1"}
			tc.change(repo.task, reader.account)
			budget.record.NextProbeAt = guard.clockNow().Add(time.Minute)
			result, err := guard.EnsureKiloSourceRecovery(context.Background(), 42)
			require.NoError(t, err)
			require.Equal(t, tc.state, result.State)
			if tc.state == "failed" {
				require.Equal(t, int64(91), result.TaskID)
				require.Equal(t, "password_rejected", result.Reason)
			}
			require.Zero(t, prober.calls)
			require.Zero(t, repo.createCalls)
		})
	}
}

func TestKiloSourceRecoveryRefreshRaceCannotReleaseNewCredentials(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  string
		mutate func(*Account)
	}{
		{"credentials recovered", "recovered", func(account *Account) {
			account.Credentials["access_token"] = "fresh-access"
			delete(account.Extra, OpenAIAuthRecoveryKey)
		}},
		{"new rejection", "pending", func(account *Account) {
			account.Extra[OpenAIAuthRecoveryKey].(map[string]any)["generation"] = "fault-new"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guard, reader, repo, budget, prober := newKiloRecoveryFixture(t)
			hash, err := hashReauthCredentials(reader.account.Credentials)
			require.NoError(t, err)
			finished := guard.clockNow().Add(-time.Second)
			repo.task = &OpenAIOAuthReauthTaskRecord{ID: 91, AccountID: 42, OAuthProfile: "codex", Status: OpenAIOAuthReauthStatusFailed, ExpectedCredentialsHash: hash, FinishedAt: &finished, Error: "password_rejected"}
			repo.onReadTask = func() { tc.mutate(reader.account) }
			result, err := guard.EnsureKiloSourceRecovery(context.Background(), 42)
			require.NoError(t, err)
			require.Equal(t, tc.state, result.State)
			require.Zero(t, prober.calls)
			require.Zero(t, repo.createCalls)
			require.Empty(t, budget.record.LeaseOwner, "a superseded recovery must release its inspection lease")
		})
	}
}

func TestKiloSourceRecoveryTemporaryReadAndProbeErrorsRemainPending(t *testing.T) {
	guard, _, repo, _, prober := newKiloRecoveryFixture(t)
	repo.readErr = errors.New("temporary database connection failure")
	result, err := guard.EnsureKiloSourceRecovery(context.Background(), 42)
	require.Error(t, err)
	require.Equal(t, "pending", result.State)
	require.Zero(t, repo.createCalls)
	require.Zero(t, prober.calls)
	repo.readErr = nil
	prober.err = context.DeadlineExceeded
	result, err = guard.EnsureKiloSourceRecovery(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "pending", result.State)
	require.Zero(t, repo.createCalls)
	require.Equal(t, 1, prober.calls)
}

func TestKiloSourceRecoverySuccessRequiresDurableCurrentState(t *testing.T) {
	guard, reader, repo, _, prober := newKiloRecoveryFixture(t)
	reader.account.Credentials["access_token"] = "fresh-access"
	result, err := guard.EnsureKiloSourceRecovery(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "recovered", result.State, "a rejection for replaced credentials is inert")
	require.Zero(t, repo.createCalls)
	require.Zero(t, prober.calls)
}

func TestKiloLoginMaterialReadsSavedSecretsWithoutClaim(t *testing.T) {
	svc, reader, repo, _, oauthClient, _ := newReauthTestService("acct-1")
	savePasswordReauthConfig(t, svc)
	repo.config.UpdatedAt = time.Date(2026, 11, 14, 1, 2, 3, 456000000, time.UTC)
	material, err := svc.ReadKiloLoginMaterial(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "user@example.com", material.Email)
	require.Equal(t, "password-secret", material.Password)
	require.Equal(t, "totp-secret", material.TOTPSecret)
	require.Len(t, material.Version, 64)
	require.Zero(t, repo.createCalls)
	require.Nil(t, repo.task)
	require.Zero(t, oauthClient.exchanges)
	raw, err := json.Marshal(material)
	require.NoError(t, err)
	require.Equal(t, "{}", string(raw), "login material must not be exposed as a DTO")

	reader.account.Credentials["access_token"] = "refreshed-access"
	reader.account.Credentials["refresh_token"] = "refreshed-refresh"
	again, err := svc.ReadKiloLoginMaterial(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, material.Version, again.Version, "quota OAuth token refresh must not invalidate Kilo login material")
	repo.config.TOTPSecretCiphertext = "encrypted:updated-seed"
	again, err = svc.ReadKiloLoginMaterial(context.Background(), 42)
	require.NoError(t, err)
	require.NotEqual(t, material.Version, again.Version)
}

func TestKiloLoginMaterialRejectsIneligibleOrBrokenConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*OpenAIOAuthReauthService, *reauthTestAccountReader, *reauthTestRepo)
	}{
		{name: "shadow", mutate: func(_ *OpenAIOAuthReauthService, reader *reauthTestAccountReader, _ *reauthTestRepo) {
			id := int64(1)
			reader.account.ParentAccountID = &id
		}},
		{name: "api key", mutate: func(_ *OpenAIOAuthReauthService, reader *reauthTestAccountReader, _ *reauthTestRepo) {
			reader.account.Type = AccountTypeAPIKey
		}},
		{name: "missing config", mutate: func(_ *OpenAIOAuthReauthService, _ *reauthTestAccountReader, repo *reauthTestRepo) { repo.config = nil }},
		{name: "mailbox mode", mutate: func(_ *OpenAIOAuthReauthService, _ *reauthTestAccountReader, repo *reauthTestRepo) {
			repo.config.CredentialMode = OpenAIOAuthReauthModeEmailOTPURL
		}},
		{name: "mismatched config", mutate: func(_ *OpenAIOAuthReauthService, _ *reauthTestAccountReader, repo *reauthTestRepo) {
			repo.config.AccountID = 99
		}},
		{name: "bad password ciphertext", mutate: func(_ *OpenAIOAuthReauthService, _ *reauthTestAccountReader, repo *reauthTestRepo) {
			repo.config.PasswordCiphertext = "bad-ciphertext-with-sensitive-value"
		}},
		{name: "bad totp ciphertext", mutate: func(_ *OpenAIOAuthReauthService, _ *reauthTestAccountReader, repo *reauthTestRepo) {
			repo.config.TOTPSecretCiphertext = "bad-ciphertext-with-sensitive-value"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, reader, repo, _, _, _ := newReauthTestService("acct-1")
			savePasswordReauthConfig(t, svc)
			tc.mutate(svc, reader, repo)
			material, err := svc.ReadKiloLoginMaterial(context.Background(), 42)
			require.Error(t, err)
			require.Nil(t, material)
			require.NotContains(t, err.Error(), "sensitive-value")
			require.Zero(t, repo.createCalls)
		})
	}
}

func TestKiloQuotaOAuthClaimsLocalProtocolWithFixedProxy(t *testing.T) {
	for _, global := range []string{"", OpenAIOAuthReauthEngineSessionStudio} {
		t.Run("global="+global, func(t *testing.T) {
			svc, reader, repo, _, _, _ := newReauthTestService("acct-1")
			ctx := context.Background()
			svc.settings = &reauthRuntimeTestSettings{values: map[string]string{}}
			_, err := svc.SaveCredentialConfig(ctx, 42, OpenAIOAuthReauthConfigInput{LoginEmail: "user@example.com", CredentialMode: OpenAIOAuthReauthModePasswordTOTP, Engine: OpenAIOAuthReauthEngineSessionStudio, Password: "password-secret", TOTPSecret: "totp-secret"})
			require.NoError(t, err)
			if global != "" {
				_, err = svc.SaveRuntimeSettings(ctx, OpenAIOAuthReauthRuntimeSettings{Engine: global, WorkerConcurrency: 1})
				require.NoError(t, err)
			}
			reader.account.Extra = map[string]any{"kilo_quota_only": true, "kilo_onboarding_job_id": int64(9), "kilo_onboarding_proxy_id": int64(4)}
			reader.proxy = &Proxy{ID: 4, Protocol: "http", Host: "203.0.113.4", Port: 8080, Status: StatusActive}
			repo.quotaOnly = true
			material, err := svc.ReadKiloLoginMaterial(ctx, 42)
			require.NoError(t, err)
			taskID, ready, err := svc.EnsureKiloQuotaOAuth(ctx, 42, 0)
			require.NoError(t, err)
			require.False(t, ready)

			claim, err := svc.ClaimTaskWithEngines(ctx, "remote-only", []string{OpenAIOAuthReauthEngineSessionStudio})
			require.NoError(t, err)
			require.Nil(t, claim, "remote-only workers must leave a local quota task queued")
			require.Equal(t, OpenAIOAuthReauthStatusQueued, repo.task.Status)
			claim, err = svc.ClaimTaskWithEngines(ctx, "local-capable", []string{OpenAIOAuthReauthEngineLocal, OpenAIOAuthReauthEngineSessionStudio})
			require.NoError(t, err)
			require.NotNil(t, claim)
			require.Equal(t, taskID, claim.TaskID)
			require.Equal(t, "codex", claim.OAuthProfile)
			require.Equal(t, "auto", claim.WorkspaceMode)
			require.Equal(t, OpenAIOAuthReauthEngineLocal, claim.Engine)
			require.Equal(t, reader.proxy.URL(), claim.ProxyURL)
			require.Empty(t, claim.ReloginEndpoint)
			require.Empty(t, claim.ReloginHeaders)
			require.Equal(t, OpenAIOAuthReauthEngineSessionStudio, repo.config.Engine, "task engine override must not rewrite the saved engine")
			runtime, err := svc.GetRuntimeSettings(ctx)
			require.NoError(t, err)
			require.Equal(t, global, runtime.Engine)
			after, err := svc.ReadKiloLoginMaterial(ctx, 42)
			require.NoError(t, err)
			require.Equal(t, material.Version, after.Version)
		})
	}
}

func TestKiloReauthWorkspaceRequiresExplicitIdentityAndMatchingPlan(t *testing.T) {
	for _, tc := range []struct {
		mode, plan, workspace string
		valid                 bool
	}{
		{"auto", "plus", "real-workspace", true}, {"auto", "team", "real-workspace", true},
		{"personal", "pro", "real-workspace", true}, {"business", "business", "real-workspace", true},
		{"business", "self_serve_business_prolite", "real-workspace", true},
		{"personal", "team", "real-workspace", false}, {"business", "plus", "real-workspace", false},
		{"auto", "free", "real-workspace", false}, {"auto", "plus", "", false},
	} {
		t.Run(tc.mode+"/"+tc.plan+"/"+tc.workspace, func(t *testing.T) {
			account := &Account{Extra: map[string]any{"kilo_quota_only": true, kiloWorkspaceModeExtraKey: tc.mode}}
			auth := map[string]any{"chatgpt_plan_type": tc.plan}
			if tc.workspace != "" {
				auth["chatgpt_account_id"] = tc.workspace
			}
			credentials := map[string]any{
				"access_token":  reauthTestJWT(map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "https://api.openai.com/auth": auth, "https://api.openai.com/profile": map[string]any{"email": "fixture@example.test"}}),
				"id_token":      reauthTestJWT(map[string]any{"sid": "new-login-session", "sub": "new-subject", "exp": time.Now().Add(time.Hour).Unix(), "email": "fixture@example.test"}),
				"refresh_token": "synthetic-refresh",
			}
			info, _, err := directReauthTokenInfo(credentials, nil)
			require.NoError(t, err)
			require.Empty(t, resolveReauthIdentity(info))
			err = validateKiloReauthWorkspace(account, info)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, tc.workspace, info.ChatGPTAccountID)
				require.Equal(t, tc.plan, info.PlanType)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestKiloQuotaOAuthCreatesCodexOnceAndWaitsForSuccess(t *testing.T) {
	svc, reader, repo, _, oauthClient, _ := newReauthTestService("acct-1")
	savePasswordReauthConfig(t, svc)
	reader.account.Extra = map[string]any{"kilo_quota_only": true}
	id, ready, err := svc.EnsureKiloQuotaOAuth(context.Background(), 42, 0)
	require.NoError(t, err)
	require.Positive(t, id)
	require.False(t, ready)
	require.Equal(t, 1, repo.createCalls)
	require.Zero(t, oauthClient.exchanges, "queue creation must not exchange tokens or invoke a model")

	for _, status := range []string{OpenAIOAuthReauthStatusQueued, OpenAIOAuthReauthStatusRunning, OpenAIOAuthReauthStatusCallbackProcessing, OpenAIOAuthReauthStatusSucceeded} {
		repo.task.Status = status
		nextID, ready, err := svc.EnsureKiloQuotaOAuth(context.Background(), 42, id)
		require.NoError(t, err)
		require.Equal(t, id, nextID)
		require.Equal(t, status == OpenAIOAuthReauthStatusSucceeded, ready)
	}
	require.Equal(t, 1, repo.createCalls)
}

func TestKiloQuotaOAuthReportsValidatedInteractionFailure(t *testing.T) {
	svc, reader, repo, _, _, _ := newReauthTestService("acct-1")
	reader.account.Extra = map[string]any{"kilo_quota_only": true}
	for _, tc := range []struct {
		name string
		raw  string
		code string
	}{
		{"email", "local_protocol_failed stage=web_login reason=email_verification_required elapsed_ms=100 exit=1", "KILO_QUOTA_OAUTH_EMAIL_VERIFICATION_REQUIRED"},
		{"unspecified", "local_protocol_failed stage=web_login reason=interaction_required elapsed_ms=100 exit=1", "KILO_QUOTA_OAUTH_INTERACTION_REQUIRED"},
		{"untrusted", "local_protocol_failed stage=web_login reason=email_verification_required elapsed_ms=100 exit=1 synthetic-private", "KILO_QUOTA_OAUTH_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo.task = &OpenAIOAuthReauthTaskRecord{ID: 123, AccountID: 42, OAuthProfile: "codex", Status: OpenAIOAuthReauthStatusFailed, Error: tc.raw}
			id, ready, err := svc.EnsureKiloQuotaOAuth(context.Background(), 42, 123)
			require.EqualValues(t, 123, id)
			require.False(t, ready)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "synthetic-private")
			if tc.code == "KILO_QUOTA_OAUTH_FAILED" {
				require.Equal(t, tc.code, infraerrors.Reason(err))
				return
			}
			var failure *kiloOnboardingFailure
			require.ErrorAs(t, err, &failure)
			require.Equal(t, tc.code, failure.code)
			require.False(t, failure.retryable)
			require.NotEmpty(t, failure.action)
		})
	}
	require.Zero(t, repo.createCalls)
}

func TestKiloQuotaOAuthReusesActiveTaskAndRejectsInvalidTask(t *testing.T) {
	svc, reader, repo, _, _, _ := newReauthTestService("acct-1")
	reader.account.Extra = map[string]any{"kilo_quota_only": true}
	repo.task = &OpenAIOAuthReauthTaskRecord{ID: 123, AccountID: 42, OAuthProfile: "codex", Status: OpenAIOAuthReauthStatusRunning}
	id, ready, err := svc.EnsureKiloQuotaOAuth(context.Background(), 42, 0)
	require.NoError(t, err)
	require.Equal(t, int64(123), id)
	require.False(t, ready)
	require.Zero(t, repo.createCalls)
	for _, tc := range []struct {
		name string
		task *OpenAIOAuthReauthTaskRecord
	}{
		{name: "missing"},
		{name: "other account", task: &OpenAIOAuthReauthTaskRecord{ID: 123, AccountID: 43, OAuthProfile: "codex", Status: OpenAIOAuthReauthStatusSucceeded}},
		{name: "other profile", task: &OpenAIOAuthReauthTaskRecord{ID: 123, AccountID: 42, OAuthProfile: "excel", Status: OpenAIOAuthReauthStatusSucceeded}},
		{name: "unknown profile", task: &OpenAIOAuthReauthTaskRecord{ID: 123, AccountID: 42, OAuthProfile: "unknown", Status: OpenAIOAuthReauthStatusSucceeded}},
		{name: "failed", task: &OpenAIOAuthReauthTaskRecord{ID: 123, AccountID: 42, OAuthProfile: "codex", Status: OpenAIOAuthReauthStatusFailed, Error: "sensitive-upstream-text"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo.task = tc.task
			_, ready, err := svc.EnsureKiloQuotaOAuth(context.Background(), 42, 123)
			require.Error(t, err)
			require.False(t, ready)
			require.NotContains(t, err.Error(), "sensitive-upstream-text")
		})
	}
	reader.account.Extra = nil
	_, _, err = svc.EnsureKiloQuotaOAuth(context.Background(), 42, 0)
	require.Equal(t, "KILO_QUOTA_ONLY_REQUIRED", infraerrors.Reason(err))
	require.Zero(t, repo.createCalls)
}

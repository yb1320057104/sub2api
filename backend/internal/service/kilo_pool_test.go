package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

const (
	kiloPoolPreparedID int64 = 7
	kiloPoolGatewayID  int64 = 17
	kiloPoolSourceID   int64 = 27
	kiloPoolJobID      int64 = 47
	kiloPoolProxyID    int64 = 73
	kiloPoolRevision   int64 = 4
	kiloPoolKey              = "synthetic-original-kilo-key"
)

type kiloPoolCipher struct{}

func (kiloPoolCipher) Encrypt(value string) (string, error) { return "fixture:" + value, nil }
func (kiloPoolCipher) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "fixture:") {
		return "", errors.New("invalid fixture ciphertext")
	}
	return strings.TrimPrefix(value, "fixture:"), nil
}

type kiloPoolAccounts struct {
	AccountRepository
	AccountProxyBindingRepository
	gateway, source *Account
	writes          []int64
	bindingClears   int
}

func (r *kiloPoolAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	switch id {
	case kiloPoolGatewayID:
		return r.gateway, nil
	case kiloPoolSourceID:
		return r.source, nil
	default:
		return nil, errors.New("unexpected account")
	}
}
func (r *kiloPoolAccounts) Update(_ context.Context, account *Account) error {
	r.writes = append(r.writes, account.ID)
	return nil
}
func (*kiloPoolAccounts) LockAccountMutation(context.Context) error { return nil }
func (r *kiloPoolAccounts) ReplaceAccountProxyBindings(_ context.Context, ids []int64, bindings []AccountProxyBindingInput) error {
	if len(ids) != 1 || ids[0] != kiloPoolGatewayID || len(bindings) != 0 {
		return errors.New("unexpected proxy mutation")
	}
	r.bindingClears++
	return nil
}

type kiloPoolRecoveryFunc func(context.Context, int64) (KiloSourceRecoveryResult, error)

func (f kiloPoolRecoveryFunc) EnsureKiloSourceRecovery(ctx context.Context, id int64) (KiloSourceRecoveryResult, error) {
	return f(ctx, id)
}

type kiloPoolProxies struct{ ProxyRepository }

func (kiloPoolProxies) GetByID(_ context.Context, id int64) (*Proxy, error) {
	if id != kiloPoolProxyID {
		return nil, errors.New("attempted to change fixed login proxy")
	}
	return &Proxy{ID: id, Protocol: "http", Host: "fixed-login.invalid", Port: 8080, Status: StatusActive}, nil
}

type kiloPoolGuard struct{ closed int }

func (g *kiloPoolGuard) Acquire(ctx context.Context, accountID, proxyID int64, proxyURL string) (*KiloEgressPermit, error) {
	if accountID != kiloPoolGatewayID || proxyID != kiloPoolProxyID || proxyURL != "http://fixed-login.invalid:8080" {
		return nil, errors.New("unexpected login route")
	}
	return NewKiloEgressPermit(ctx, func() { g.closed++ }), nil
}

type kiloPoolEgress struct {
	listed   int
	released []int64
}

func (e *kiloPoolEgress) List(context.Context, int64) ([]KiloEgressRoute, error) {
	e.listed++
	return []KiloEgressRoute{{ID: 101, ProxyID: kiloPoolProxyID}, {ID: 102, ProxyID: 74}}, nil
}
func (*kiloPoolEgress) Verify(context.Context, int64, int64) (*KiloEgressRoute, error) {
	return nil, errors.New("unbind must not reprovision login exit")
}
func (e *kiloPoolEgress) Release(_ context.Context, id int64) error {
	e.released = append(e.released, id)
	return nil
}

type kiloPoolRemote struct {
	KiloRemote
	before *KiloConnectionStatus
	after  *KiloConnectionStatus
	calls  []string
	keys   []string
}

func (r *kiloPoolRemote) Profile(_ context.Context, key, _ string) (*KiloRemoteProfile, error) {
	r.calls, r.keys = append(r.calls, "profile"), append(r.keys, key)
	return &KiloRemoteProfile{ID: "original-kilo-user"}, nil
}
func (r *kiloPoolRemote) Connection(_ context.Context, key, _, _ string) (*KiloConnectionStatus, error) {
	r.calls, r.keys = append(r.calls, "connection"), append(r.keys, key)
	if len(r.calls) == 2 {
		if r.before != nil {
			return r.before, nil
		}
		return &KiloConnectionStatus{Connected: true, State: "connected", Email: "source@example.test"}, nil
	}
	return r.after, nil
}
func (r *kiloPoolRemote) Disconnect(_ context.Context, key, _, _ string) error {
	r.calls, r.keys = append(r.calls, "disconnect"), append(r.keys, key)
	return nil
}

func kiloPoolFixture(t *testing.T) (*KiloService, sqlmock.Sqlmock, *kiloPoolAccounts) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	accounts := &kiloPoolAccounts{
		gateway: &Account{ID: kiloPoolGatewayID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusDisabled,
			Credentials: map[string]any{"api_key": kiloPoolKey, "model_mapping": map[string]string{"openai/gpt-6.1-sol": "openai/gpt-6.1-sol"}},
			Extra: map[string]any{KiloManagedExtraKey: true, KiloLoginProxyExtraKey: kiloPoolProxyID, "kilo_prepared_id": kiloPoolPreparedID,
				"kilo_onboarding_source_id": kiloPoolSourceID, "kilo_onboarding_job_id": kiloPoolJobID, "kilo_binding_revision": kiloPoolRevision,
				"kilo_subscription_models": []string{"openai/gpt-6.1-sol"}, "kilo_subscription_scope_source": KiloAuthenticatedModelScope,
				"kilo_subscription_scope_verified_at": "2026-11-15T00:00:00Z", "kilo_subscription_scope_key": "original-proof",
				"kilo_subscription_scope_organization": "", "kilo_subscription_scope_source_identity": "original-source", "unrelated": "keep"}},
		source: &Account{ID: kiloPoolSourceID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusError, Schedulable: false,
			Credentials: map[string]any{"email": "source@example.test", "access_token": "synthetic-rejected-gpt-token", "chatgpt_account_id": "source-workspace"},
			Extra: map[string]any{"kilo_quota_only": true, "kilo_source_prepared_id": kiloPoolPreparedID, "kilo_source_gateway_id": kiloPoolGatewayID,
				"kilo_onboarding_job_id": kiloPoolJobID, "kilo_onboarding_proxy_id": kiloPoolProxyID, "unrelated": "keep",
				OpenAIAuthRecoveryKey: map[string]any{"access_token_digest": OpenAIAccessTokenDigest("synthetic-rejected-gpt-token"), "generation": "source-generation"}}},
	}
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	return &KiloService{db: db, client: client, accounts: accounts, encryptor: kiloPoolCipher{}}, mock, accounts
}

func kiloPoolLeaseExpectation(mock sqlmock.Sqlmock, job *kiloClaim, owned bool) {
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
	rows := sqlmock.NewRows([]string{"id"})
	if owned {
		rows.AddRow(job.ID)
	}
	mock.ExpectQuery("SELECT j.id FROM pk_kilo_jobs j JOIN pk_kilo_prepared_accounts p.*FOR SHARE").
		WithArgs(job.ID, job.PreparedID, job.Lease, job.Revision, job.BindingRevision).WillReturnRows(rows)
}

func kiloPoolClaim() *kiloClaim {
	sourceID, gatewayID := kiloPoolSourceID, kiloPoolGatewayID
	return &kiloClaim{ID: kiloPoolJobID, PreparedID: kiloPoolPreparedID, SourceID: &sourceID, AccountID: &gatewayID,
		Revision: 2, BindingRevision: kiloPoolRevision, Lease: "owned-unbind-lease", Kind: KiloJobUnbindSource}
}

func TestKiloPoolRecoveryRetainsSlotUntilTerminalFailure(t *testing.T) {
	for _, test := range []struct {
		name, state, reason string
		manual, refresh     bool
	}{
		{name: "pending", state: "pending", reason: "automatic_relogin_pending"},
		{name: "blocked_requires_action", state: "blocked", reason: "automatic_relogin_disabled"},
		{name: "manual_source_disabled", manual: true},
		{name: "failed_queues_only", state: "failed", reason: "automatic_relogin_failed"},
		{name: "late_failure_after_refresh", state: "failed", reason: "automatic_relogin_failed", refresh: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, mock, accounts := kiloPoolFixture(t)
			remote := &kiloPoolRemote{}
			svc.remote = remote
			if test.manual {
				accounts.source.Status = StatusDisabled
				delete(accounts.source.Extra, OpenAIAuthRecoveryKey)
			}
			mock.ExpectQuery("SELECT id,account_id,.*binding_revision,binding_state,identity_status").WillReturnRows(
				sqlmock.NewRows([]string{"id", "gateway", "source", "revision", "state", "identity", "task", "resume"}).
					AddRow(kiloPoolPreparedID, kiloPoolGatewayID, kiloPoolSourceID, kiloPoolRevision, "bound", "active", 0, true))
			mock.ExpectQuery("SELECT identity_mode,identity_source_account_id,identity_status").WithArgs(kiloPoolPreparedID).
				WillReturnRows(sqlmock.NewRows([]string{"mode", "origin", "identity"}).AddRow(KiloIdentityModeIndependent, nil, "active"))
			mock.ExpectQuery("SELECT email,identity_credential_encrypted").WithArgs(kiloPoolPreparedID, kiloPoolSourceID, kiloPoolRevision).
				WillReturnRows(sqlmock.NewRows([]string{"email", "credential"}).AddRow("original-kilo@example.test", "already-saved"))
			calls := 0
			svc.recovery = kiloPoolRecoveryFunc(func(_ context.Context, id int64) (KiloSourceRecoveryResult, error) {
				calls++
				require.Equal(t, kiloPoolSourceID, id)
				result := KiloSourceRecoveryResult{State: test.state, Reason: test.reason, TaskID: 97,
					AccessTokenDigest: OpenAIAccessTokenDigest(accounts.source.GetOpenAIAccessToken()), RecoveryGeneration: "source-generation"}
				if test.refresh {
					accounts.source.Credentials["access_token"] = "synthetic-new-gpt-token"
					accounts.source.Status = StatusActive
					delete(accounts.source.Extra, OpenAIAuthRecoveryKey)
				}
				return result, nil
			})
			if !test.manual {
				mock.ExpectExec("UPDATE pk_kilo_prepared_accounts SET binding_state='recovering'").WithArgs(kiloPoolPreparedID, kiloPoolRevision, kiloPoolSourceID).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("UPDATE pk_kilo_prepared_accounts SET recovery_task_id").WithArgs(kiloPoolPreparedID, kiloPoolRevision, kiloPoolSourceID, int64(97), test.reason).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if test.state == "failed" {
				mock.ExpectQuery("SELECT identity_mode,identity_source_account_id,identity_status").WithArgs(kiloPoolPreparedID).
					WillReturnRows(sqlmock.NewRows([]string{"mode", "origin", "identity"}).AddRow(KiloIdentityModeIndependent, nil, "active"))
				mock.ExpectBegin()
				mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery("SELECT account_id,source_account_id,binding_revision,login_proxy_id,binding_state,identity_status,organization_id").WithArgs(kiloPoolPreparedID).
					WillReturnRows(sqlmock.NewRows([]string{"gateway", "source", "revision", "proxy", "state", "identity", "org"}).AddRow(kiloPoolGatewayID, kiloPoolSourceID, kiloPoolRevision, kiloPoolProxyID, "recovering", "active", ""))
				mock.ExpectQuery("SELECT EXISTS.*pk_kilo_jobs WHERE prepared_id").WithArgs(kiloPoolPreparedID).WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(false))
				if test.refresh {
					mock.ExpectRollback()
				} else {
					mock.ExpectExec("INSERT INTO pk_kilo_batches").WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectQuery("SELECT id::text,payload_hash FROM pk_kilo_batches").WillReturnRows(sqlmock.NewRows([]string{"id", "hash"}).AddRow("2f467f83-1b3d-4921-b37a-35c541e2048c", kiloHash([]any{"unbind", kiloPoolPreparedID, kiloPoolRevision, kiloPoolSourceID})))
					mock.ExpectQuery("INSERT INTO pk_kilo_jobs.*'unbind_source'").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(kiloPoolJobID))
					mock.ExpectExec("UPDATE pk_kilo_prepared_accounts SET status='binding',binding_state='unbinding'").WithArgs(kiloPoolPreparedID, kiloPoolJobID, test.reason).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit()
				}
			}
			require.NoError(t, svc.maintainKiloBindings(context.Background()))
			if test.manual {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
			require.Empty(t, remote.calls, "recovery must not invoke remote disconnect")
			if test.state == "failed" && !test.refresh {
				require.Equal(t, []int64{kiloPoolGatewayID}, accounts.writes)
			} else {
				require.Empty(t, accounts.writes)
			}
			require.Equal(t, kiloPoolKey, accounts.gateway.GetCredential("api_key"))
			require.Equal(t, kiloPoolProxyID, accounts.gateway.Extra[KiloLoginProxyExtraKey])
			require.Contains(t, accounts.gateway.Credentials, "model_mapping", "a queued unbind still owns its scope")
			require.Equal(t, kiloPoolPreparedID, accounts.source.Extra["kilo_source_prepared_id"])
			require.Zero(t, accounts.bindingClears)
		})
	}
}

func TestKiloPoolUnbindPublishesEmptyOnlyAfterRemoteConfirmation(t *testing.T) {
	for _, test := range []struct {
		name      string
		before    *KiloConnectionStatus
		after     *KiloConnectionStatus
		confirmed bool
		legacy    bool
	}{
		{name: "still_connected", after: &KiloConnectionStatus{Connected: true, State: "connected", Email: "source@example.test"}},
		{name: "unknown_status", after: &KiloConnectionStatus{State: "unknown"}},
		{name: "missing_status"},
		{name: "confirmed_disconnected", after: &KiloConnectionStatus{State: "disconnected"}, confirmed: true},
		{name: "legacy_capture_before_first_detach", after: &KiloConnectionStatus{State: "disconnected"}, confirmed: true, legacy: true},
		{name: "already_disconnected_skips_mutation", before: &KiloConnectionStatus{State: "disconnected"}, confirmed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, mock, accounts := kiloPoolFixture(t)
			job := kiloPoolClaim()
			accounts.source.Status = StatusDisabled
			remote, egress, guard := &kiloPoolRemote{before: test.before, after: test.after}, &kiloPoolEgress{}, &kiloPoolGuard{}
			if test.legacy {
				reauth, reader, repo, _, _, _ := newReauthTestService("source-workspace")
				savePasswordReauthConfig(t, reauth)
				reader.account = accounts.source
				repo.config.AccountID, repo.config.LoginEmail = kiloPoolSourceID, "source@example.test"
				svc.reauth = reauth
			}
			svc.remote, svc.proxies, svc.egress = remote, kiloPoolProxies{}, egress
			svc.router = NewAccountProxyRouter(nil, nil, guard)
			lockKey := kiloPoolPreparedID ^ int64(-5424871661416316928)
			mock.ExpectExec("SELECT pg_advisory_lock").WithArgs(lockKey).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT EXISTS.*pk_kilo_jobs WHERE id").WithArgs(job.ID, job.Lease).WillReturnRows(sqlmock.NewRows([]string{"owned"}).AddRow(true))
			mock.ExpectQuery("SELECT p.source_account_id,p.binding_revision,p.remote_user_id").WithArgs(job.PreparedID, job.ID).
				WillReturnRows(sqlmock.NewRows([]string{"source", "revision", "remote", "email", "identity"}).AddRow(kiloPoolSourceID, kiloPoolRevision, "original-kilo-user", "source@example.test", "active"))
			cipher := "already-saved"
			if test.legacy {
				cipher = ""
			}
			mock.ExpectQuery("SELECT email,identity_credential_encrypted").WithArgs(kiloPoolPreparedID, kiloPoolSourceID, kiloPoolRevision).
				WillReturnRows(sqlmock.NewRows([]string{"email", "credential"}).AddRow("source@example.test", cipher))
			if test.legacy {
				mock.ExpectExec("UPDATE pk_kilo_prepared_accounts SET identity_credential_encrypted").
					WithArgs(kiloPoolPreparedID, kiloPoolSourceID, kiloPoolRevision, `fixture:{"email":"source@example.test","password":"password-secret","totp_secret":"totp-secret"}`).
					WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectQuery("SELECT credential_encrypted,organization_id").WithArgs(job.PreparedID).WillReturnRows(sqlmock.NewRows([]string{"key", "org"}).AddRow("fixture:"+kiloPoolKey, ""))
			if test.confirmed {
				mock.ExpectQuery("SELECT identity_mode,identity_source_account_id,identity_status").WithArgs(kiloPoolPreparedID).
					WillReturnRows(sqlmock.NewRows([]string{"mode", "origin", "identity"}).AddRow(KiloIdentityModeUnknown, nil, "active"))
				mock.ExpectQuery("SELECT account_id FROM pk_kilo_prepared_accounts").WithArgs(job.PreparedID).WillReturnRows(sqlmock.NewRows([]string{"gateway"}).AddRow(kiloPoolGatewayID))
				mock.ExpectExec("UPDATE pk_kilo_prepared_accounts SET last_error=replace").WithArgs(job.PreparedID).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectBegin()
				kiloPoolLeaseExpectation(mock, job, true)
				mock.ExpectQuery("SELECT source_account_id,binding_revision.*binding_state='unbinding'.*FOR UPDATE").WithArgs(job.PreparedID, job.ID).
					WillReturnRows(sqlmock.NewRows([]string{"source", "revision", "mode", "identity"}).AddRow(kiloPoolSourceID, kiloPoolRevision, KiloIdentityModeUnknown, "active"))
				mock.ExpectExec("UPDATE pk_kilo_prepared_accounts SET source_account_id=NULL,status=CASE").WithArgs(job.PreparedID).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("UPDATE pk_kilo_jobs SET status='succeeded',stage='unbound'").WithArgs(job.ID, job.Lease).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(lockKey).WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
			err := svc.processKiloUnbind(context.Background(), job)
			if test.confirmed {
				require.NoError(t, err)
				require.Equal(t, []int64{102}, egress.released, "fixed login exit must outlive the GPT slot")
				require.Equal(t, []int64{kiloPoolSourceID, kiloPoolGatewayID}, accounts.writes)
				require.Equal(t, 1, accounts.bindingClears)
				require.NotContains(t, accounts.gateway.Credentials, "model_mapping")
				for _, name := range []string{"kilo_subscription_models", "kilo_subscription_scope_source", "kilo_subscription_scope_verified_at", "kilo_subscription_scope_key", "kilo_subscription_scope_organization", "kilo_subscription_scope_source_identity", "kilo_onboarding_source_id", "kilo_onboarding_job_id"} {
					require.NotContains(t, accounts.gateway.Extra, name)
				}
				for _, name := range []string{"kilo_source_prepared_id", "kilo_source_gateway_id", "kilo_onboarding_job_id", "kilo_onboarding_proxy_id"} {
					require.NotContains(t, accounts.source.Extra, name)
				}
				require.Equal(t, kiloPoolRevision+1, accounts.gateway.Extra["kilo_binding_revision"])
			} else {
				require.Equal(t, "KILO_CONFLICT", infraerrors.Reason(err))
				require.Empty(t, accounts.writes)
				require.Zero(t, accounts.bindingClears)
				require.Zero(t, egress.listed)
				require.Contains(t, accounts.gateway.Credentials, "model_mapping")
				require.Equal(t, kiloPoolPreparedID, accounts.source.Extra["kilo_source_prepared_id"])
			}
			if test.before != nil {
				require.Equal(t, []string{"profile", "connection"}, remote.calls)
			} else {
				require.Equal(t, []string{"profile", "connection", "disconnect", "connection"}, remote.calls)
			}
			for _, key := range remote.keys {
				require.Equal(t, kiloPoolKey, key, "GPT bearer must never become the Kilo credential")
			}
			require.Equal(t, 1, guard.closed)
			require.Equal(t, kiloPoolKey, accounts.gateway.GetCredential("api_key"))
			require.Equal(t, kiloPoolProxyID, accounts.gateway.Extra[KiloLoginProxyExtraKey])
			require.Equal(t, "synthetic-rejected-gpt-token", accounts.source.GetOpenAIAccessToken())
			require.Equal(t, StatusDisabled, accounts.source.Status)
			require.False(t, accounts.source.Schedulable)
			require.Equal(t, true, accounts.source.Extra["kilo_quota_only"])
			require.Equal(t, "keep", accounts.source.Extra["unrelated"])
			require.Equal(t, "keep", accounts.gateway.Extra["unrelated"])
		})
	}
}

func TestKiloPoolUnbindRejectsLateOrRevokedCompletion(t *testing.T) {
	for _, name := range []string{"new_binding_revision", "revoked_lease", "already_empty_slot"} {
		t.Run(name, func(t *testing.T) {
			svc, mock, accounts := kiloPoolFixture(t)
			job := kiloPoolClaim()
			mock.ExpectBegin()
			kiloPoolLeaseExpectation(mock, job, name != "revoked_lease")
			if name != "revoked_lease" {
				rows := sqlmock.NewRows([]string{"source", "revision", "mode", "identity"})
				if name == "new_binding_revision" {
					rows.AddRow(kiloPoolSourceID, kiloPoolRevision+1, KiloIdentityModeUnknown, "active")
				}
				mock.ExpectQuery("SELECT source_account_id,binding_revision.*binding_state='unbinding'.*FOR UPDATE").WithArgs(job.PreparedID, job.ID).WillReturnRows(rows)
			}
			mock.ExpectRollback()
			err := svc.completeKiloUnbind(withKiloClaimLease(context.Background(), job), job, kiloPoolKey)
			require.Error(t, err)
			if name != "already_empty_slot" {
				require.Equal(t, "KILO_CONFLICT", infraerrors.Reason(err))
			}
			require.Empty(t, accounts.writes, "a late remote receipt must not clear a later binding")
			require.Zero(t, accounts.bindingClears)
			require.Equal(t, kiloPoolRevision, accounts.gateway.Extra["kilo_binding_revision"])
			require.Equal(t, kiloPoolPreparedID, accounts.source.Extra["kilo_source_prepared_id"])
			require.Equal(t, kiloPoolKey, accounts.gateway.GetCredential("api_key"))
			require.Equal(t, kiloPoolProxyID, accounts.gateway.Extra[KiloLoginProxyExtraKey])
		})
	}
}

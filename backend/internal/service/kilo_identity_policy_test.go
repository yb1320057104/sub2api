package service

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestClassifyKiloIdentityProviders(t *testing.T) {
	for _, provider := range []string{"email", "google", "github", "gitlab", "apple", "discord", "linkedin", "workos", "anaconda", "claude"} {
		t.Run(provider, func(t *testing.T) {
			require.Equal(t, KiloIdentityModeIndependent, ClassifyKiloIdentityProviders([]KiloAuthProvider{
				{Provider: "openai", Email: "same@example.invalid"}, {Provider: provider, Email: "same@example.invalid"},
			}))
		})
	}
	for _, test := range []struct {
		name      string
		providers []KiloAuthProvider
		mode      string
	}{
		{"nil", nil, KiloIdentityModeUnknown},
		{"empty", []KiloAuthProvider{}, KiloIdentityModeUnknown},
		{"openai-only", []KiloAuthProvider{{Provider: "openai"}}, KiloIdentityModeCoupled},
		{"passkey", []KiloAuthProvider{{Provider: "passkey"}}, KiloIdentityModeUnknown},
		{"fake-login", []KiloAuthProvider{{Provider: "fake-login"}}, KiloIdentityModeUnknown},
		{"openai-and-fake", []KiloAuthProvider{{Provider: "openai"}, {Provider: "fake-login"}}, KiloIdentityModeUnknown},
		{"openai-and-passkey", []KiloAuthProvider{{Provider: "openai"}, {Provider: "passkey"}}, KiloIdentityModeUnknown},
		{"future", []KiloAuthProvider{{Provider: "future-provider"}}, KiloIdentityModeUnknown},
		{"case-not-official", []KiloAuthProvider{{Provider: "EMAIL"}}, KiloIdentityModeUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.mode, ClassifyKiloIdentityProviders(test.providers))
		})
	}
}

type kiloIdentityPolicyRemote struct {
	KiloRemote
	profile   *KiloRemoteProfile
	providers []KiloAuthProvider
	calls     []string
}

func (r *kiloIdentityPolicyRemote) Profile(_ context.Context, key, proxy string) (*KiloRemoteProfile, error) {
	r.calls = append(r.calls, "profile", key, proxy)
	return r.profile, nil
}
func (r *kiloIdentityPolicyRemote) AuthProviders(_ context.Context, key, proxy string) ([]KiloAuthProvider, error) {
	r.calls = append(r.calls, "providers", key, proxy)
	return r.providers, nil
}
func (r *kiloIdentityPolicyRemote) UnlinkOpenAI(context.Context, string, string) error {
	return errors.New("classification must never mutate upstream login methods")
}

func kiloIdentityPolicyRows(origin any, health string, busy bool, revision int64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"account_id", "login_proxy_id", "email", "remote_user_id", "credential_encrypted", "binding_revision", "identity_mode", "identity_source_account_id", "identity_checked_at", "identity_status", "busy"}).
		AddRow(kiloPoolGatewayID, kiloPoolProxyID, "original@example.invalid", "original-kilo-user", "fixture:"+kiloPoolKey, revision, KiloIdentityModeUnknown, origin, nil, health, busy)
}

func kiloIdentityOperationStart(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_lock($1)")).
		WithArgs(kiloPoolPreparedID ^ int64(-5424871661416316928)).WillReturnResult(sqlmock.NewResult(0, 1))
}
func kiloIdentityOperationEnd(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
		WithArgs(kiloPoolPreparedID ^ int64(-5424871661416316928)).
		WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
}

func TestKiloIdentityPolicyRefreshPersistsOfficialClassification(t *testing.T) {
	const originalSource int64 = 122
	const replacementSource int64 = 126
	for _, test := range []struct {
		name, health, mode string
		providers          []KiloAuthProvider
		storedOrigin       any
		historyOrigin      any
		wantOrigin         any
	}{
		{"coupled-unique-history", "active", KiloIdentityModeCoupled, []KiloAuthProvider{{Provider: "openai"}}, nil, originalSource, originalSource},
		{"coupled-ambiguous-history", "active", KiloIdentityModeCoupled, []KiloAuthProvider{{Provider: "openai"}}, nil, nil, nil},
		{"coupled-preserves-origin", "active", KiloIdentityModeCoupled, []KiloAuthProvider{{Provider: "openai"}}, originalSource, nil, originalSource},
		{"independent-preserves-origin", "retired", KiloIdentityModeIndependent, []KiloAuthProvider{{Provider: "openai", Email: "original@example.invalid"}, {Provider: "email", Email: "original@example.invalid"}}, originalSource, nil, originalSource},
		{"independent-no-invented-origin", "active", KiloIdentityModeIndependent, []KiloAuthProvider{{Provider: "github"}}, nil, nil, nil},
		{"unknown-preserves-health", "banned", KiloIdentityModeUnknown, []KiloAuthProvider{{Provider: "passkey"}}, originalSource, nil, originalSource},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, mock, accounts := kiloPoolFixture(t)
			guard := &kiloPoolGuard{}
			svc.router = NewAccountProxyRouter(nil, nil, guard)
			svc.proxies = kiloPoolProxies{}
			remote := &kiloIdentityPolicyRemote{profile: &KiloRemoteProfile{ID: "original-kilo-user", Email: "ORIGINAL@example.invalid"}, providers: test.providers}
			svc.remote = remote
			accounts.gateway.Extra["kilo_onboarding_source_id"] = replacementSource
			accounts.gateway.Extra[KiloIdentitySourceExtraKey] = replacementSource
			accounts.gateway.Extra[KiloIdentityModeExtraKey] = KiloIdentityModeUnknown
			beforeCredential := accounts.gateway.GetCredential("api_key")
			beforeMapping := accounts.gateway.Credentials["model_mapping"]
			checked := time.Date(2026, 11, 15, 8, 30, 0, 0, time.UTC)

			kiloIdentityOperationStart(mock)
			mock.ExpectQuery(regexp.QuoteMeta(kiloIdentitySnapshotSQL)).WithArgs(kiloPoolPreparedID).
				WillReturnRows(kiloIdentityPolicyRows(test.storedOrigin, test.health, false, kiloPoolRevision))
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(regexp.QuoteMeta(kiloIdentitySnapshotSQL + " FOR UPDATE")).WithArgs(kiloPoolPreparedID).
				WillReturnRows(kiloIdentityPolicyRows(test.storedOrigin, test.health, false, kiloPoolRevision))
			if test.storedOrigin == nil && test.mode == KiloIdentityModeCoupled {
				mock.ExpectQuery(regexp.QuoteMeta(kiloIdentityOriginSQL)).WithArgs(kiloPoolPreparedID, "original@example.invalid").
					WillReturnRows(sqlmock.NewRows([]string{"origin"}).AddRow(test.historyOrigin))
			}
			mock.ExpectQuery("UPDATE pk_kilo_prepared_accounts SET identity_mode=.*identity_checked_at=NOW\\(\\).+RETURNING identity_source_account_id,identity_checked_at,identity_status").
				WithArgs(kiloPoolPreparedID, test.mode, test.wantOrigin).
				WillReturnRows(sqlmock.NewRows([]string{"origin", "checked_at", "health"}).AddRow(test.wantOrigin, checked, test.health))
			mock.ExpectCommit()
			kiloIdentityOperationEnd(mock)

			policy, err := svc.RefreshIdentityPolicy(context.Background(), kiloPoolPreparedID)
			require.NoError(t, err)
			require.Equal(t, kiloPoolPreparedID, policy.PreparedID)
			require.Equal(t, test.mode, policy.IdentityMode)
			require.Equal(t, test.health, policy.IdentityStatus)
			require.Equal(t, checked, *policy.IdentityCheckedAt)
			if test.wantOrigin == nil {
				require.Nil(t, policy.IdentitySourceAccountID)
				require.NotContains(t, accounts.gateway.Extra, KiloIdentitySourceExtraKey)
			} else {
				require.Equal(t, originalSource, *policy.IdentitySourceAccountID)
				require.Equal(t, originalSource, accounts.gateway.Extra[KiloIdentitySourceExtraKey])
			}
			require.Equal(t, test.mode, accounts.gateway.Extra[KiloIdentityModeExtraKey])
			require.Equal(t, replacementSource, accounts.gateway.Extra["kilo_onboarding_source_id"])
			require.Equal(t, beforeCredential, accounts.gateway.GetCredential("api_key"))
			require.True(t, reflect.DeepEqual(beforeMapping, accounts.gateway.Credentials["model_mapping"]))
			require.Equal(t, []int64{kiloPoolGatewayID}, accounts.writes)
			require.Equal(t, 1, guard.closed)
			require.Equal(t, []string{"profile", kiloPoolKey, "http://fixed-login.invalid:8080", "providers", kiloPoolKey, "http://fixed-login.invalid:8080"}, remote.calls)
		})
	}
}

func TestKiloIdentityPolicyRefreshRejectsBusyMismatchAndRaces(t *testing.T) {
	for _, test := range []struct {
		name         string
		initialBusy  bool
		missing      bool
		wrongProfile bool
		freshBusy    bool
		freshChange  bool
		wrongGateway bool
	}{
		{name: "initial-busy", initialBusy: true},
		{name: "missing", missing: true},
		{name: "profile-mismatch", wrongProfile: true},
		{name: "new-job", freshBusy: true},
		{name: "binding-revision-changed", freshChange: true},
		{name: "gateway-key-changed", wrongGateway: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, mock, accounts := kiloPoolFixture(t)
			guard := &kiloPoolGuard{}
			svc.router = NewAccountProxyRouter(nil, nil, guard)
			svc.proxies = kiloPoolProxies{}
			profile := &KiloRemoteProfile{ID: "original-kilo-user", Email: "original@example.invalid"}
			if test.wrongProfile {
				profile.Email = "replacement@example.invalid"
			}
			if test.wrongGateway {
				accounts.gateway.Credentials["api_key"] = "replacement-key"
			}
			remote := &kiloIdentityPolicyRemote{profile: profile, providers: []KiloAuthProvider{{Provider: "email"}}}
			svc.remote = remote
			kiloIdentityOperationStart(mock)
			rows := kiloIdentityPolicyRows(nil, "active", test.initialBusy, kiloPoolRevision)
			if test.missing {
				rows = sqlmock.NewRows([]string{"missing"})
			}
			mock.ExpectQuery(regexp.QuoteMeta(kiloIdentitySnapshotSQL)).WithArgs(kiloPoolPreparedID).WillReturnRows(rows)
			if !test.initialBusy && !test.missing && !test.wrongProfile {
				mock.ExpectBegin()
				mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
				revision := kiloPoolRevision
				if test.freshChange {
					revision++
				}
				mock.ExpectQuery(regexp.QuoteMeta(kiloIdentitySnapshotSQL + " FOR UPDATE")).WithArgs(kiloPoolPreparedID).
					WillReturnRows(kiloIdentityPolicyRows(nil, "active", test.freshBusy, revision))
				mock.ExpectRollback()
			}
			kiloIdentityOperationEnd(mock)
			policy, err := svc.RefreshIdentityPolicy(context.Background(), kiloPoolPreparedID)
			require.Error(t, err)
			require.Nil(t, policy)
			wantReason := "KILO_CONFLICT"
			if test.missing {
				wantReason = "KILO_PREPARED_NOT_FOUND"
			}
			require.Equal(t, wantReason, infraerrors.Reason(err))
			require.Empty(t, accounts.writes)
			if test.initialBusy || test.missing {
				require.Empty(t, remote.calls)
			} else {
				require.Equal(t, 1, guard.closed)
			}
		})
	}
}

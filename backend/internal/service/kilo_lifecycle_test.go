package service

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestKiloLifecycleSourceBoundary(t *testing.T) {
	original, replacement := int64(122), int64(126)
	for _, test := range []struct {
		name, mode, status, reason string
		origin                     sql.NullInt64
		source                     *int64
	}{
		{name: "unbound-unknown", mode: KiloIdentityModeUnknown, status: "active"},
		{name: "unknown-cannot-bind", mode: KiloIdentityModeUnknown, status: "active", source: &replacement, reason: "KILO_IDENTITY_UNVERIFIED"},
		{name: "independent-can-bind", mode: KiloIdentityModeIndependent, status: "active", source: &replacement},
		{name: "coupled-original-recovery", mode: KiloIdentityModeCoupled, status: "active", origin: sql.NullInt64{Int64: original, Valid: true}, source: &original},
		{name: "coupled-no-replacement", mode: KiloIdentityModeCoupled, status: "active", origin: sql.NullInt64{Int64: original, Valid: true}, source: &replacement, reason: "KILO_IDENTITY_COUPLED"},
		{name: "coupled-missing-origin", mode: KiloIdentityModeCoupled, status: "active", source: &original, reason: "KILO_IDENTITY_COUPLED"},
		{name: "retired-cannot-reactivate", mode: KiloIdentityModeCoupled, status: "retired", origin: sql.NullInt64{Int64: original, Valid: true}, source: &original, reason: "KILO_IDENTITY_RETIRED"},
		{name: "retired-independent-cannot-reactivate", mode: KiloIdentityModeIndependent, status: "retired", reason: "KILO_IDENTITY_RETIRED"},
		{name: "banned-cannot-reactivate", mode: KiloIdentityModeIndependent, status: "banned", source: &replacement, reason: "KILO_IDENTITY_BANNED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := kiloRequireLifecycleSource(kiloLifecyclePolicy{mode: test.mode, status: test.status, origin: test.origin}, test.source)
			if test.reason == "" {
				require.NoError(t, err)
			} else {
				require.Equal(t, test.reason, infraerrors.Reason(err))
			}
		})
	}
}

func TestKiloCoupledRetirementFencesOriginalSourceAndPreservesIdentity(t *testing.T) {
	for _, name := range []string{"binding-changed", "origin-changed", "job-in-flight", "token-refreshed", "generation-changed", "source-recovered", "workspace-not-terminal", "terminal-recovery-failure", "workspace-deactivated"} {
		t.Run(name, func(t *testing.T) {
			svc, mock, accounts := kiloPoolFixture(t)
			remote, egress := &kiloPoolRemote{}, &kiloPoolEgress{}
			svc.remote, svc.egress = remote, egress
			p := kiloPoolBinding{ID: kiloPoolPreparedID, GatewayID: kiloPoolGatewayID, SourceID: kiloPoolSourceID, Revision: kiloPoolRevision,
				ExpectedTokenDigest: OpenAIAccessTokenDigest(accounts.source.GetOpenAIAccessToken()), ExpectedRecoveryGeneration: "source-generation"}
			reason := "automatic_relogin_failed"
			if name == "workspace-not-terminal" || name == "workspace-deactivated" {
				reason = "workspace_deactivated"
			}
			switch name {
			case "token-refreshed":
				accounts.source.Credentials["access_token"] = "synthetic-new-token"
			case "generation-changed":
				accounts.source.Extra[OpenAIAuthRecoveryKey].(map[string]any)["generation"] = "new-generation"
			case "source-recovered":
				accounts.source.Status = StatusActive
				delete(accounts.source.Extra, OpenAIAuthRecoveryKey)
				p.ExpectedRecoveryGeneration = ""
			case "workspace-deactivated":
				accounts.source.ErrorMessage = "Workspace deactivated (402): original workspace"
			}
			kiloIdentityOperationStart(mock)
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
			origin, revision := kiloPoolSourceID, kiloPoolRevision
			if name == "origin-changed" {
				origin++
			}
			if name == "binding-changed" {
				revision++
			}
			mock.ExpectQuery("SELECT account_id,identity_source_account_id,binding_revision.*identity_mode='gpt_coupled'.*FOR UPDATE").WithArgs(p.ID).
				WillReturnRows(sqlmock.NewRows([]string{"gateway", "origin", "revision"}).AddRow(p.GatewayID, origin, revision))
			if name != "binding-changed" && name != "origin-changed" {
				mock.ExpectQuery("SELECT EXISTS.*pk_kilo_jobs WHERE prepared_id").WithArgs(p.ID).
					WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(name == "job-in-flight"))
			}
			retired := name == "terminal-recovery-failure" || name == "workspace-deactivated"
			if retired {
				mock.ExpectExec("UPDATE pk_kilo_prepared_accounts SET identity_status='retired'.*identity_source_account_id=\\$2 AND binding_revision=\\$3").
					WithArgs(p.ID, p.SourceID, p.Revision).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				mock.ExpectQuery("SELECT account_id FROM pk_kilo_prepared_accounts").WithArgs(p.ID).
					WillReturnRows(sqlmock.NewRows([]string{"gateway"}).AddRow(p.GatewayID))
				mock.ExpectExec("UPDATE pk_kilo_prepared_accounts SET last_error=replace").WithArgs(p.ID).WillReturnResult(sqlmock.NewResult(0, 1))
			} else {
				mock.ExpectRollback()
			}
			kiloIdentityOperationEnd(mock)

			require.NoError(t, svc.retireCoupledKiloIdentity(context.Background(), p, reason))
			require.Equal(t, kiloPoolKey, accounts.gateway.GetCredential("api_key"))
			require.Equal(t, kiloPoolProxyID, accounts.gateway.Extra[KiloLoginProxyExtraKey])
			require.Equal(t, "keep", accounts.gateway.Extra["unrelated"])
			require.Empty(t, remote.calls, "local retirement must not mutate the upstream Kilo identity")
			if retired {
				require.Equal(t, []int64{kiloPoolSourceID, kiloPoolGatewayID}, accounts.writes)
				require.Equal(t, StatusDisabled, accounts.source.Status)
				require.Equal(t, true, accounts.source.Extra["kilo_lifecycle_ended"])
				require.Equal(t, StatusDisabled, accounts.gateway.Status)
				require.False(t, accounts.gateway.Schedulable)
				require.Equal(t, KiloIdentityModeCoupled, accounts.gateway.Extra[KiloIdentityModeExtraKey])
				require.Equal(t, kiloPoolSourceID, accounts.gateway.Extra[KiloIdentitySourceExtraKey])
				require.NotContains(t, accounts.gateway.Credentials, "model_mapping")
				require.NotContains(t, accounts.gateway.Extra, "kilo_subscription_models")
				require.False(t, strings.Contains(accounts.gateway.ErrorMessage, "banned"))
				require.Equal(t, []int64{102}, egress.released)
				require.Equal(t, 1, accounts.bindingClears)
			} else {
				require.Empty(t, accounts.writes)
				require.Contains(t, accounts.gateway.Credentials, "model_mapping")
				require.Zero(t, egress.listed)
			}
		})
	}
}

func TestKiloCoupledUnboundIdentityStillObservesOriginalSource(t *testing.T) {
	svc, mock, accounts := kiloPoolFixture(t)
	accounts.source.Status = StatusActive
	delete(accounts.source.Extra, OpenAIAuthRecoveryKey)
	recoveryCalls := 0
	svc.recovery = kiloPoolRecoveryFunc(func(context.Context, int64) (KiloSourceRecoveryResult, error) {
		recoveryCalls++
		return KiloSourceRecoveryResult{}, nil
	})
	mock.ExpectQuery("SELECT id,account_id,CASE WHEN identity_mode='gpt_coupled'").WillReturnRows(
		sqlmock.NewRows([]string{"id", "gateway", "origin", "revision", "state", "identity", "task", "resume"}).
			AddRow(kiloPoolPreparedID, kiloPoolGatewayID, kiloPoolSourceID, kiloPoolRevision, "empty", "active", 0, false))
	mock.ExpectQuery("SELECT identity_mode,identity_source_account_id,identity_status").WithArgs(kiloPoolPreparedID).
		WillReturnRows(sqlmock.NewRows([]string{"mode", "origin", "identity"}).AddRow(KiloIdentityModeCoupled, kiloPoolSourceID, "active"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(source_account_id=$2,false) FROM pk_kilo_prepared_accounts WHERE id=$1")).
		WithArgs(kiloPoolPreparedID, kiloPoolSourceID).WillReturnRows(sqlmock.NewRows([]string{"matches"}).AddRow(false))
	require.NoError(t, svc.maintainKiloBindings(context.Background()))
	require.Zero(t, recoveryCalls)
	require.Empty(t, accounts.writes)
}

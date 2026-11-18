package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type kiloCleanupAccounts struct {
	AccountRepository
	items            map[int64]*Account
	readErrors       map[int64]error
	deleted, updated []int64
}

func (r *kiloCleanupAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	if err := r.readErrors[id]; err != nil {
		return nil, err
	}
	account := r.items[id]
	if account == nil {
		return nil, ErrAccountNotFound
	}
	return account, nil
}
func (*kiloCleanupAccounts) LockAccountMutation(context.Context) error { return nil }
func (r *kiloCleanupAccounts) Update(_ context.Context, a *Account) error {
	r.updated = append(r.updated, a.ID)
	return nil
}
func (r *kiloCleanupAccounts) Delete(_ context.Context, id int64) error {
	r.deleted = append(r.deleted, id)
	return nil
}

func kiloCleanupFixture(t *testing.T) (*KiloService, sqlmock.Sqlmock, *kiloCleanupAccounts, *kiloFailedJob) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	const sourceID, gatewayID, proxyID int64 = 131, 132, 15
	raw, err := json.Marshal(&kiloOnboardingRuntime{Version: 1, RemoteClosed: true})
	require.NoError(t, err)
	cipher, err := (kiloPoolCipher{}).Encrypt(string(raw))
	require.NoError(t, err)
	j := &kiloFailedJob{ID: 6, SourceID: ptrKiloCleanup(sourceID), AccountID: ptrKiloCleanup(gatewayID), LoginProxyID: ptrKiloCleanup(proxyID), Kind: KiloJobOnboardSource, Status: "failed", Stage: "quota_oauth", Revision: 2, Cipher: cipher}
	accounts := &kiloCleanupAccounts{items: map[int64]*Account{
		sourceID:  {ID: sourceID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"email": "synthetic@example.test", "access_token": "synthetic-source-token"}, Extra: map[string]any{"kilo_quota_only": true, "kilo_onboarding_job_id": j.ID, "kilo_onboarding_proxy_id": proxyID}},
		gatewayID: {ID: gatewayID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusDisabled, Credentials: map[string]any{"base_url": KiloGatewayBaseURL}, Extra: map[string]any{KiloManagedExtraKey: true, AccountExtraKiloBridgeEnabled: true, "kilo_gateway_profile": "cli", "kilo_onboarding_source_id": sourceID, "kilo_onboarding_job_id": j.ID, KiloLoginProxyExtraKey: proxyID}},
	}}
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	return &KiloService{db: db, client: client, accounts: accounts, encryptor: kiloPoolCipher{}}, mock, accounts, j
}

func ptrKiloCleanup(id int64) *int64 { return &id }

func kiloCleanupRows(j *kiloFailedJob) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "prepared_id", "source_account_id", "account_id", "login_proxy_id", "job_kind", "status", "stage", "remote_state_encrypted", "remote_outcome", "source_identity", "revision", "binding_revision", "credential_ready", "subscription_connected", "reused_identity", "live_lease"}).AddRow(j.ID, j.PreparedID, j.SourceID, j.AccountID, j.LoginProxyID, j.Kind, j.Status, j.Stage, j.Cipher, j.Outcome, j.SourceIdentity, j.Revision, j.BindingRevision, j.CredentialReady, j.Connected, j.Reused, j.LiveLease)
}

func expectKiloCleanupRead(mock sqlmock.Sqlmock, j *kiloFailedJob) {
	mock.ExpectQuery("SELECT id,prepared_id,source_account_id.*FROM pk_kilo_jobs WHERE id").WithArgs(j.ID).WillReturnRows(kiloCleanupRows(j))
}

func expectKiloCleanupTx(mock sqlmock.Sqlmock, j *kiloFailedJob, busy bool) {
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
	expectKiloCleanupRead(mock, j)
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloCleanupEgressLock).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id FROM accounts WHERE id IN").WithArgs(j.SourceID, j.AccountID).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(*j.SourceID).AddRow(*j.AccountID))
	mock.ExpectQuery("SELECT EXISTS.*pk_kilo_jobs.*openai_oauth_reauth_tasks.*pk_kilo_egress_leases.*pk_kilo_device_sessions").WithArgs(j.ID, j.SourceID, j.AccountID, j.PreparedID).WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(busy))
}

func TestKiloFailedDeleteSoftDeletesOnlyNewUnmaterializedAccounts(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			svc, mock, accounts, j := kiloCleanupFixture(t)
			j.Status = status
			expectKiloCleanupRead(mock, j)
			expectKiloCleanupTx(mock, j, false)
			mock.ExpectExec("UPDATE pk_kilo_device_sessions").WithArgs(*j.AccountID).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("DELETE FROM pk_kilo_egress_routes").WithArgs(*j.AccountID).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("DELETE FROM pk_kilo_proxy_owners o WHERE o.account_id").WithArgs(*j.AccountID).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT EXISTS.*pk_kilo_import_requests.*usage_logs").WithArgs(*j.SourceID, j.ID).WillReturnRows(sqlmock.NewRows([]string{"disposable"}).AddRow(true))
			mock.ExpectExec("UPDATE pk_kilo_jobs SET status='cancelled',stage='deleted'").WithArgs(j.ID).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			result, err := svc.DeleteFailedJob(context.Background(), j.ID)
			require.NoError(t, err)
			require.Equal(t, "deleted", result.Status)
			require.False(t, result.IdentityRetained)
			require.Equal(t, j.SourceID, result.DeletedSourceAccountID)
			require.Equal(t, j.AccountID, result.DeletedGatewayAccountID)
			require.Equal(t, []int64{*j.AccountID, *j.SourceID}, accounts.deleted)
			require.NotContains(t, accounts.items[*j.AccountID].Extra, KiloLoginProxyExtraKey)
			require.Equal(t, "synthetic-source-token", accounts.items[*j.SourceID].GetOpenAIAccessToken())
		})
	}
}

func TestKiloFailedDeleteRetainsExistingSchedulableSource(t *testing.T) {
	svc, mock, accounts, j := kiloCleanupFixture(t)
	accounts.items[*j.SourceID].Schedulable = true
	expectKiloCleanupRead(mock, j)
	expectKiloCleanupTx(mock, j, false)
	mock.ExpectExec("UPDATE pk_kilo_device_sessions").WithArgs(*j.AccountID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM pk_kilo_egress_routes").WithArgs(*j.AccountID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM pk_kilo_proxy_owners o WHERE o.account_id").WithArgs(*j.AccountID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT EXISTS.*pk_kilo_import_requests.*usage_logs").WithArgs(*j.SourceID, j.ID).WillReturnRows(sqlmock.NewRows([]string{"disposable"}).AddRow(false))
	mock.ExpectExec("UPDATE pk_kilo_jobs SET status='cancelled',stage='deleted'").WithArgs(j.ID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	result, err := svc.DeleteFailedJob(context.Background(), j.ID)
	require.NoError(t, err)
	require.Nil(t, result.DeletedSourceAccountID)
	require.Equal(t, []int64{*j.AccountID}, accounts.deleted)
	require.True(t, accounts.items[*j.SourceID].Schedulable)
	require.Equal(t, "synthetic-source-token", accounts.items[*j.SourceID].GetOpenAIAccessToken())
}

func TestKiloFailedDeleteRejectsBusyWithoutAccountWrites(t *testing.T) {
	svc, mock, accounts, j := kiloCleanupFixture(t)
	expectKiloCleanupRead(mock, j)
	expectKiloCleanupTx(mock, j, true)
	mock.ExpectRollback()
	_, err := svc.DeleteFailedJob(context.Background(), j.ID)
	require.Equal(t, "KILO_CONFLICT", infraerrors.Reason(err))
	require.Empty(t, accounts.deleted)
	require.Empty(t, accounts.updated)
}

func TestKiloFailedDeleteRejectsConnectedOrLiveLeaseAndReplaysTombstone(t *testing.T) {
	for _, mode := range []string{"connected", "lease", "succeeded", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			svc, mock, accounts, j := kiloCleanupFixture(t)
			switch mode {
			case "connected":
				j.Connected = true
			case "lease":
				j.LiveLease = true
			case "succeeded":
				j.Status = "succeeded"
			case "deleted":
				j.Status, j.Stage = "cancelled", "deleted"
			}
			expectKiloCleanupRead(mock, j)
			result, err := svc.DeleteFailedJob(context.Background(), j.ID)
			if mode == "deleted" {
				require.NoError(t, err)
				require.Equal(t, "deleted", result.Status)
			} else {
				require.Error(t, err)
			}
			require.Empty(t, accounts.deleted)
			require.Empty(t, accounts.updated)
		})
	}
}

func TestKiloFailedIdentityAndRevisionBoundary(t *testing.T) {
	j := &kiloFailedJob{ID: 5, BindingRevision: 4, AccountID: ptrKiloCleanup(124), SourceID: ptrKiloCleanup(126)}
	p := &kiloFailedPrepared{ID: 2, Revision: 4, JobID: ptrKiloCleanup(5), AccountID: ptrKiloCleanup(124), SourceID: ptrKiloCleanup(126), OriginID: ptrKiloCleanup(122), Cipher: "synthetic-original-key"}
	require.True(t, p.ownedBy(j))
	require.True(t, kiloFailureHasIdentity(j, p, &kiloOnboardingRuntime{}, nil))
	p.Revision++
	require.False(t, p.ownedBy(j), "a later binding must not be changed by deleting an old failure")
	p.Revision, p.JobID = 4, nil
	require.True(t, p.ownedBy(j), "cancelled binding retains the exact revision even after clearing its job pointer")
}

type kiloCleanupGuard struct{}

func (kiloCleanupGuard) Acquire(ctx context.Context, _, _ int64, _ string) (*KiloEgressPermit, error) {
	return NewKiloEgressPermit(ctx, func() {}), nil
}

func TestKiloFailedDeleteRetainsOriginalIdentityKeyAndProxy(t *testing.T) {
	for _, sourceDeleted := range []bool{false, true} {
		name := "source_present"
		if sourceDeleted {
			name = "source_soft_deleted"
		}
		t.Run(name, func(t *testing.T) {
			testKiloFailedDeleteRetainsOriginalIdentity(t, sourceDeleted)
		})
	}
}

func testKiloFailedDeleteRetainsOriginalIdentity(t *testing.T, sourceDeleted bool) {
	svc, mock, accounts, j := kiloCleanupFixture(t)
	j.PreparedID, j.BindingRevision, j.CredentialReady = ptrKiloCleanup(2), 4, true
	gateway := accounts.items[*j.AccountID]
	gateway.Credentials["api_key"] = "synthetic-original-key"
	gateway.Extra["kilo_prepared_id"] = int64(2)
	source := accounts.items[*j.SourceID]
	source.Extra["kilo_source_prepared_id"], source.Extra["kilo_source_gateway_id"] = int64(2), *j.AccountID
	if sourceDeleted {
		delete(accounts.items, *j.SourceID)
	}
	svc.proxies = kiloPreflightProxyRepo{}
	svc.router = &AccountProxyRouter{egress: kiloCleanupGuard{}}
	svc.remote = &kiloPoolRemote{after: &KiloConnectionStatus{State: "disconnected"}}
	expectKiloCleanupRead(mock, j)
	mock.ExpectExec("SELECT pg_advisory_lock").WithArgs(int64(2) ^ int64(-5424871661416316928)).WillReturnResult(sqlmock.NewResult(0, 1))
	preparedRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "account_id", "source_account_id", "binding_job_id", "binding_revision", "identity_source_account_id", "credential_encrypted", "remote_user_id", "binding_state"}).AddRow(2, *j.AccountID, *j.SourceID, j.ID, 4, 122, "synthetic-preserved-cipher", "original-remote-user", "blocked")
	}
	mock.ExpectQuery("SELECT id,account_id,source_account_id,binding_job_id").WithArgs(int64(2)).WillReturnRows(preparedRows())
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloBatchLock).WillReturnResult(sqlmock.NewResult(0, 1))
	expectKiloCleanupRead(mock, j)
	mock.ExpectQuery("SELECT id,account_id,source_account_id,binding_job_id").WithArgs(int64(2)).WillReturnRows(preparedRows())
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(kiloCleanupEgressLock).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id FROM accounts WHERE id IN").WithArgs(j.SourceID, j.AccountID).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(*j.SourceID).AddRow(*j.AccountID))
	mock.ExpectQuery("SELECT EXISTS.*pk_kilo_jobs.*openai_oauth_reauth_tasks.*pk_kilo_egress_leases.*pk_kilo_device_sessions").WithArgs(j.ID, j.SourceID, j.AccountID, j.PreparedID).WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(false))
	if sourceDeleted {
		mock.ExpectQuery("SELECT id,deleted_at FROM accounts WHERE id").WithArgs(*j.SourceID).WillReturnRows(sqlmock.NewRows([]string{"id", "deleted_at"}).AddRow(*j.SourceID, time.Now()))
	}
	mock.ExpectExec("UPDATE pk_kilo_prepared_accounts SET.*management_deleted").WithArgs(int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	if !sourceDeleted {
		mock.ExpectQuery("SELECT EXISTS.*pk_kilo_import_requests.*usage_logs").WithArgs(*j.SourceID, j.ID).WillReturnRows(sqlmock.NewRows([]string{"disposable"}).AddRow(true))
	}
	mock.ExpectExec("UPDATE pk_kilo_jobs SET status='cancelled',stage='deleted'").WithArgs(j.ID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(int64(2) ^ int64(-5424871661416316928)).WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
	result, err := svc.DeleteFailedJob(context.Background(), j.ID)
	require.NoError(t, err)
	require.True(t, result.IdentityRetained)
	require.Empty(t, accounts.deleted)
	require.Equal(t, "synthetic-original-key", gateway.GetCredential("api_key"))
	require.EqualValues(t, 15, gateway.Extra[KiloLoginProxyExtraKey])
	require.Equal(t, "synthetic-source-token", source.GetOpenAIAccessToken())
	if sourceDeleted {
		require.Equal(t, []int64{*j.AccountID}, accounts.updated)
		require.Equal(t, j.ID, source.Extra["kilo_onboarding_job_id"], "soft-deleted sources must not be mutated")
		require.Nil(t, result.DeletedSourceAccountID)
	}
}

func TestKiloFailedDeleteSourceMissRequiresDeletedRow(t *testing.T) {
	lookupErr := errors.New("account lookup unavailable")
	sqlErr := errors.New("deleted account lookup unavailable")
	for _, mode := range []string{"missing", "not_deleted", "wrong_id", "lookup_error", "sql_error"} {
		t.Run(mode, func(t *testing.T) {
			svc, mock, accounts, j := kiloCleanupFixture(t)
			delete(accounts.items, *j.SourceID)
			expectKiloCleanupRead(mock, j)
			expectKiloCleanupTx(mock, j, false)
			var wantErr error = ErrAccountNotFound
			if mode == "lookup_error" {
				accounts.readErrors = map[int64]error{*j.SourceID: lookupErr}
				wantErr = lookupErr
			} else {
				query := mock.ExpectQuery("SELECT id,deleted_at FROM accounts WHERE id").WithArgs(*j.SourceID)
				switch mode {
				case "missing":
					query.WillReturnError(sql.ErrNoRows)
				case "not_deleted":
					query.WillReturnRows(sqlmock.NewRows([]string{"id", "deleted_at"}).AddRow(*j.SourceID, nil))
				case "wrong_id":
					query.WillReturnRows(sqlmock.NewRows([]string{"id", "deleted_at"}).AddRow(*j.SourceID+1, time.Now()))
				case "sql_error":
					query.WillReturnError(sqlErr)
					wantErr = sqlErr
				}
			}
			mock.ExpectRollback()
			_, err := svc.DeleteFailedJob(context.Background(), j.ID)
			require.ErrorIs(t, err, wantErr)
			require.Empty(t, accounts.updated)
			require.Empty(t, accounts.deleted)
		})
	}
}

func kiloDeletedGatewayRows(t *testing.T, gateway *Account, deletedAt any, material bool) *sqlmock.Rows {
	t.Helper()
	credentials, err := json.Marshal(gateway.Credentials)
	require.NoError(t, err)
	extra, err := json.Marshal(gateway.Extra)
	require.NoError(t, err)
	return sqlmock.NewRows([]string{"id", "deleted_at", "platform", "type", "parent_account_id", "credentials", "extra", "schedulable", "remote_material"}).AddRow(gateway.ID, deletedAt, gateway.Platform, gateway.Type, gateway.ParentAccountID, credentials, extra, gateway.Schedulable, material)
}

func expectKiloDeletedGateway(t *testing.T, mock sqlmock.Sqlmock, gateway *Account, material bool) {
	t.Helper()
	mock.ExpectQuery("SELECT a.id,a.deleted_at.*FROM accounts a WHERE a.id").WithArgs(gateway.ID).WillReturnRows(kiloDeletedGatewayRows(t, gateway, time.Now(), material))
}

func TestKiloFailedDeleteSoftDeletedGatewayRetainsExistingSource(t *testing.T) {
	svc, mock, accounts, j := kiloCleanupFixture(t)
	gateway := accounts.items[*j.AccountID]
	delete(accounts.items, *j.AccountID)
	gateway.Credentials["kilo_machine_id"] = "synthetic-bootstrap-machine"
	gateway.Credentials["kilo_organization_id"] = ""
	gateway.Credentials["api_key"] = ""
	source := accounts.items[*j.SourceID]
	source.Schedulable = true
	expectKiloCleanupRead(mock, j)
	expectKiloDeletedGateway(t, mock, gateway, false)
	expectKiloCleanupTx(mock, j, false)
	expectKiloDeletedGateway(t, mock, gateway, false)
	mock.ExpectExec("UPDATE pk_kilo_device_sessions").WithArgs(gateway.ID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM pk_kilo_egress_routes").WithArgs(gateway.ID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM pk_kilo_proxy_owners o WHERE o.account_id").WithArgs(gateway.ID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT EXISTS.*pk_kilo_import_requests.*usage_logs").WithArgs(source.ID, j.ID).WillReturnRows(sqlmock.NewRows([]string{"disposable"}).AddRow(false))
	mock.ExpectExec("UPDATE pk_kilo_jobs SET status='cancelled',stage='deleted'").WithArgs(j.ID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	result, err := svc.DeleteFailedJob(context.Background(), j.ID)
	require.NoError(t, err)
	require.False(t, result.IdentityRetained)
	require.Nil(t, result.DeletedSourceAccountID)
	require.Nil(t, result.DeletedGatewayAccountID)
	require.Empty(t, accounts.deleted)
	require.Equal(t, []int64{source.ID}, accounts.updated)
	require.True(t, source.Schedulable)
	require.Equal(t, "synthetic-source-token", source.GetOpenAIAccessToken())
	require.EqualValues(t, 15, gateway.Extra[KiloLoginProxyExtraKey], "the deleted gateway itself must not be updated")
}

func TestKiloFailedDeleteSoftDeletedGatewayRejectsIdentityOrMismatch(t *testing.T) {
	for _, mode := range []string{"key", "credential", "prepared_or_device_material", "identity_extra", "runtime_identity", "job_identity", "missing", "not_deleted", "wrong_id", "lookup_error", "sql_error", "wrong_owner", "new_material", "restored", "busy"} {
		t.Run(mode, func(t *testing.T) {
			svc, mock, accounts, j := kiloCleanupFixture(t)
			gateway := accounts.items[*j.AccountID]
			delete(accounts.items, *j.AccountID)
			material := false
			wantReason := "KILO_REMOTE_IDENTITY_UNRESOLVED"
			var wantErr error
			switch mode {
			case "key":
				gateway.Credentials["api_key"] = "synthetic-retained-key"
			case "credential":
				gateway.Credentials["session_material"] = "synthetic-retained-session"
			case "prepared_or_device_material":
				material = true
			case "identity_extra":
				gateway.Extra[KiloIdentitySourceExtraKey] = int64(122)
			case "runtime_identity":
				raw, err := json.Marshal(&kiloOnboardingRuntime{Version: 1, RemoteClosed: true, RemoteUserID: "synthetic-remote-user"})
				require.NoError(t, err)
				j.Cipher, err = svc.encryptor.Encrypt(string(raw))
				require.NoError(t, err)
			case "job_identity":
				j.CredentialReady = true
			case "wrong_owner":
				gateway.Extra["kilo_onboarding_job_id"] = j.ID + 1
				wantReason = "KILO_CONFLICT"
			case "restored", "busy":
				wantReason = "KILO_CONFLICT"
			case "lookup_error":
				wantErr = errors.New("account lookup unavailable")
				accounts.readErrors = map[int64]error{gateway.ID: wantErr}
			case "sql_error":
				wantErr = errors.New("deleted gateway lookup unavailable")
			case "missing", "not_deleted", "wrong_id":
				wantErr = ErrAccountNotFound
			}
			expectKiloCleanupRead(mock, j)
			if mode != "lookup_error" {
				query := mock.ExpectQuery("SELECT a.id,a.deleted_at.*FROM accounts a WHERE a.id").WithArgs(*j.AccountID)
				switch mode {
				case "missing":
					query.WillReturnError(sql.ErrNoRows)
				case "not_deleted":
					query.WillReturnRows(kiloDeletedGatewayRows(t, gateway, nil, false))
				case "wrong_id":
					gateway.ID++
					query.WillReturnRows(kiloDeletedGatewayRows(t, gateway, time.Now(), false))
				case "sql_error":
					query.WillReturnError(wantErr)
				default:
					query.WillReturnRows(kiloDeletedGatewayRows(t, gateway, time.Now(), material))
				}
			}
			if mode == "new_material" || mode == "restored" || mode == "busy" {
				expectKiloCleanupTx(mock, j, mode == "busy")
				if mode != "busy" {
					if mode == "restored" {
						mock.ExpectQuery("SELECT a.id,a.deleted_at.*FROM accounts a WHERE a.id").WithArgs(gateway.ID).WillReturnRows(kiloDeletedGatewayRows(t, gateway, nil, false))
						wantErr = ErrAccountNotFound
					} else {
						expectKiloDeletedGateway(t, mock, gateway, mode == "new_material")
					}
				}
				mock.ExpectRollback()
			}
			_, err := svc.DeleteFailedJob(context.Background(), j.ID)
			if wantErr != nil {
				require.ErrorIs(t, err, wantErr)
			} else {
				require.Equal(t, wantReason, infraerrors.Reason(err))
			}
			require.Empty(t, accounts.updated)
			require.Empty(t, accounts.deleted)
		})
	}
}

type kiloCleanupProber struct {
	KiloRemote
	reachable bool
}

func (p kiloCleanupProber) ProbeLoginProxy(context.Context, string) ([]KiloProxyCheck, error) {
	return []KiloProxyCheck{{Target: "openai", Reachable: p.reachable}, {Target: "kilo_api", Reachable: p.reachable}, {Target: "kilo_login", Reachable: p.reachable}}, nil
}

func TestKiloFailedProxyPreflightFailureDoesNotWritePin(t *testing.T) {
	svc, mock, accounts, j := kiloCleanupFixture(t)
	svc.proxies, svc.remote = kiloPreflightProxyRepo{}, kiloCleanupProber{}
	expectKiloCleanupRead(mock, j)
	err := svc.SetFailedJobLoginProxy(context.Background(), j.ID, 22)
	require.Equal(t, "KILO_LOGIN_PROXY_UNREACHABLE", infraerrors.Reason(err))
	require.Empty(t, accounts.updated)
	require.Empty(t, accounts.deleted)
	require.EqualValues(t, 15, accounts.items[*j.AccountID].Extra[KiloLoginProxyExtraKey])
}

func TestKiloFailedProxyChangePreservesCredentialsAndWaitsForRetry(t *testing.T) {
	svc, mock, accounts, j := kiloCleanupFixture(t)
	svc.proxies, svc.remote = kiloPreflightProxyRepo{}, kiloCleanupProber{reachable: true}
	expectKiloCleanupRead(mock, j)
	expectKiloCleanupTx(mock, j, false)
	mock.ExpectQuery("SELECT id FROM proxies WHERE id").WithArgs(int64(22)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(22))
	mock.ExpectQuery("SELECT EXISTS.*pk_kilo_proxy_owners").WithArgs(int64(22), j.AccountID).WillReturnRows(sqlmock.NewRows([]string{"occupied"}).AddRow(false))
	mock.ExpectExec("UPDATE pk_kilo_device_sessions").WithArgs(*j.AccountID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM pk_kilo_egress_routes").WithArgs(*j.AccountID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM pk_kilo_proxy_owners o WHERE o.account_id").WithArgs(*j.AccountID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT proxy_source,proxy_id FROM openai_oauth_reauth_configs").WithArgs(*j.SourceID).WillReturnRows(sqlmock.NewRows([]string{"proxy_source", "proxy_id"}).AddRow("account", nil))
	mock.ExpectQuery("SELECT login_email,COALESCE.*openai_oauth_reauth_configs").WithArgs(*j.SourceID).WillReturnRows(sqlmock.NewRows([]string{"email", "password", "totp", "mode", "updated"}).AddRow("synthetic@example.test", "unchanged-password-cipher", "unchanged-totp-cipher", OpenAIOAuthReauthModePasswordTOTP, time.Now()))
	mock.ExpectExec("UPDATE pk_kilo_jobs SET login_proxy_id").WithArgs(j.ID, int64(22), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	err := svc.SetFailedJobLoginProxy(context.Background(), j.ID, 22)
	require.NoError(t, err)
	require.Empty(t, accounts.deleted)
	require.EqualValues(t, 22, accounts.items[*j.SourceID].Extra["kilo_onboarding_proxy_id"])
	require.EqualValues(t, 22, accounts.items[*j.AccountID].Extra[KiloLoginProxyExtraKey])
	require.Equal(t, "synthetic-source-token", accounts.items[*j.SourceID].GetOpenAIAccessToken())
}

func TestKiloFailedProxyRejectsMaterializedIdentity(t *testing.T) {
	svc, mock, accounts, j := kiloCleanupFixture(t)
	j.PreparedID, j.CredentialReady = ptrKiloCleanup(2), true
	expectKiloCleanupRead(mock, j)
	err := svc.SetFailedJobLoginProxy(context.Background(), j.ID, 22)
	require.Equal(t, "KILO_CONFLICT", infraerrors.Reason(err))
	require.Empty(t, accounts.updated)
}

//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The operation connection can disappear independently of the worker's SQL
// pool. Keep the worker context live so only the durable fence can reject it.
func TestKiloNativeRevokedOperationCannotRecreateEgress(t *testing.T) {
	f := newKiloNativeFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	preparedID := f.prepared(t, ctx, "revoked-operation", "synthetic-kilo-revoked-operation")
	_, err := f.service().SubmitBatch(ctx, f.batch([]int64{preparedID}, nil))
	require.NoError(t, err)
	_, job := f.snapshot(t, ctx, preparedID)
	lease := uuid.NewString()
	_, err = f.db.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='running',lease_token=$2,lease_until=NOW()+INTERVAL '4 minutes' WHERE id=$1`, job.ID, lease)
	require.NoError(t, err)
	account := kiloSQLManagedAccount(t, f.client)
	_, err = f.db.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET account_id=$2 WHERE id=$1`, preparedID, account.ID)
	require.NoError(t, err)
	lockConn, err := f.db.Conn(ctx)
	require.NoError(t, err)
	defer lockConn.Close()
	var backendPID int
	require.NoError(t, lockConn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID))
	_, err = lockConn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, preparedID^int64(-5424871661416316928))
	require.NoError(t, err)
	var terminated bool
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT pg_terminate_backend($1)`, backendPID).Scan(&terminated))
	require.True(t, terminated)
	// Exercise the real release boundary so revocation and persistent cleanup
	// remain covered even though the onboarding fixture uses synthetic permits.
	egress := newKiloEgressWithProber(f.db, &kiloSQLEgressProber{exits: map[string]string{}})
	svc := service.NewKiloService(f.db, f.client, f.accounts, &kiloNativeAdmin{}, f.groups, f.proxies, kiloNativeEncryptor{}, f.remote, egress, service.NewAccountProxyRouter(f.bindings, f.cache, egress), &config.Config{})
	require.NoError(t, svc.ReleasePrepared(ctx, preparedID))
	stale := service.WithKiloOnboardingLease(ctx, job.ID, preparedID, lease)
	require.NoError(t, stale.Err(), "the test must not rely on heartbeat cancellation")
	_, err = f.bindings.AssignAccountProxies(service.WithProxyGroupFilter(stale, f.proxyGroupID), service.AccountProxyAssignmentInput{
		AccountIDs: []int64{account.ID}, ProxiesPerAccount: 2, MaxAccountsPerProxy: 1, ProxyConcurrency: 3,
	})
	require.Error(t, err, "the revoked worker cannot allocate after release has returned")
	bindings, err := f.bindings.GetAccountProxyBindings(ctx, account.ID)
	require.NoError(t, err)
	require.Empty(t, bindings)

	var proxyID int64
	var proxyURL string
	for id, value := range f.selected {
		proxyID, proxyURL = id, value
		break
	}
	repo := newKiloEgressWithProber(f.db, &kiloSQLEgressProber{exits: map[string]string{proxyURL: "8.8.8.8"}})
	_, err = repo.Verify(stale, account.ID, proxyID)
	require.Error(t, err)
	require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_egress_routes`))
	require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_egress_owners`))
}

func TestKiloNativeCancelledProbeCannotPublishIP(t *testing.T) {
	f := newKiloNativeFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	preparedID := f.prepared(t, ctx, "cancelled-probe", "synthetic-kilo-cancelled-probe")
	_, err := f.service().SubmitBatch(ctx, f.batch([]int64{preparedID}, nil))
	require.NoError(t, err)
	_, job := f.snapshot(t, ctx, preparedID)
	lease := uuid.NewString()
	_, err = f.db.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='running',lease_token=$2,lease_until=NOW()+INTERVAL '4 minutes' WHERE id=$1`, job.ID, lease)
	require.NoError(t, err)
	account := kiloSQLManagedAccount(t, f.client)
	_, err = f.db.ExecContext(ctx, `UPDATE pk_kilo_prepared_accounts SET account_id=$2 WHERE id=$1`, preparedID, account.ID)
	require.NoError(t, err)
	stale := service.WithKiloOnboardingLease(ctx, job.ID, preparedID, lease)
	_, err = f.bindings.AssignAccountProxies(service.WithProxyGroupFilter(stale, f.proxyGroupID), service.AccountProxyAssignmentInput{
		AccountIDs: []int64{account.ID}, ProxiesPerAccount: 2, MaxAccountsPerProxy: 1, ProxyConcurrency: 3,
	})
	require.NoError(t, err)
	bindings, err := f.bindings.GetAccountProxyBindings(ctx, account.ID)
	require.NoError(t, err)
	repo := NewKiloEgressGuard(f.db)
	pending, _, err := repo.prepareVerification(stale, account.ID, bindings[0].ProxyID)
	require.NoError(t, err)
	require.NoError(t, f.service().CancelJob(ctx, job.ID))
	_, err = repo.finishVerification(stale, pending, "", nil)
	require.Error(t, err, "a detached registration must retain the durable job fence")
	require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_proxy_owners`))
	require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_egress_owners`))
	var state string
	var published bool
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT state,egress_ip IS NOT NULL FROM pk_kilo_egress_routes WHERE account_id=$1`, account.ID).Scan(&state, &published))
	require.Equal(t, service.KiloEgressUnverified, state)
	require.False(t, published)
}

// A mutation already holding the row fence commits before cancellation can
// finish, so the opposite serialization order cannot bypass revocation either.
func TestKiloNativeRevocationWaitsForMutationTransaction(t *testing.T) {
	f := newKiloNativeFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	preparedID := f.prepared(t, ctx, "transaction-fence", "synthetic-kilo-transaction-fence")
	_, err := f.service().SubmitBatch(ctx, f.batch([]int64{preparedID}, nil))
	require.NoError(t, err)
	_, job := f.snapshot(t, ctx, preparedID)
	lease := uuid.NewString()
	_, err = f.db.ExecContext(ctx, `UPDATE pk_kilo_jobs SET status='running',lease_token=$2,lease_until=NOW()+INTERVAL '4 minutes' WHERE id=$1`, job.ID, lease)
	require.NoError(t, err)
	tx, err := f.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, service.LockKiloOnboardingLease(service.WithKiloOnboardingLease(ctx, job.ID, preparedID, lease), tx))
	var mutatorPID int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&mutatorPID))
	cancelled := make(chan error, 1)
	go func() { cancelled <- f.service().CancelJob(ctx, job.ID) }()
	require.Eventually(t, func() bool {
		var waiting bool
		err := f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)))`, mutatorPID).Scan(&waiting)
		return err == nil && waiting
	}, 3*time.Second, 10*time.Millisecond, "revocation must be blocked on the live mutation")
	select {
	case err := <-cancelled:
		t.Fatalf("revocation returned before the transaction ended: %v", err)
	default:
	}
	require.NoError(t, tx.Commit())
	select {
	case err := <-cancelled:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("revocation did not finish after mutation commit")
	}
	require.EqualValues(t, 1, kiloNativeCount(t, ctx, f.db, fmt.Sprintf(`SELECT count(*) FROM pk_kilo_jobs WHERE id=%d AND status='cancelled' AND lease_token IS NULL`, job.ID)))
}

type kiloActivationBarrierRepo struct {
	service.AccountRepository
	service.AccountProxyBindingRepository
	locker   service.KiloAccountMutationLocker
	sourceID int64
	entered  chan int
	resume   <-chan struct{}
	once     sync.Once
}

func (r *kiloActivationBarrierRepo) LockAccountMutation(ctx context.Context) error {
	return r.locker.LockAccountMutation(ctx)
}

func (r *kiloActivationBarrierRepo) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	if tx := dbent.TxFromContext(ctx); tx != nil && id == r.sourceID {
		var pauseErr error
		r.once.Do(func() {
			rows, err := tx.Client().QueryContext(ctx, `SELECT pg_backend_pid()`)
			if err != nil {
				pauseErr = err
				return
			}
			var pid int
			if rows.Next() {
				pauseErr = rows.Scan(&pid)
			}
			if pauseErr == nil {
				pauseErr = rows.Err()
			}
			_ = rows.Close()
			if pauseErr != nil {
				return
			}
			r.entered <- pid
			select {
			case <-r.resume:
			case <-ctx.Done():
				pauseErr = ctx.Err()
			}
		})
		if pauseErr != nil {
			return nil, pauseErr
		}
	}
	return r.AccountRepository.GetByID(ctx, id)
}

func TestKiloNativeActivationSerializesSourceEdit(t *testing.T) {
	f := newKiloNativeFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key := "synthetic-kilo-activation-edit"
	preparedID := f.prepared(t, ctx, "activation-edit", key)
	source := f.source(t, "activation-edit", "before-activation@example.test")
	f.remote.expect(key, "activation-edit", f.organization, service.KiloConnectionStatus{Connected: true, Email: "before-activation@example.test"})
	_, err := f.service().SubmitBatch(ctx, f.batch([]int64{preparedID}, []int64{source.ID}))
	require.NoError(t, err)
	resume := make(chan struct{})
	var resumeOnce sync.Once
	release := func() { resumeOnce.Do(func() { close(resume) }) }
	t.Cleanup(release)
	repo := &kiloActivationBarrierRepo{AccountRepository: f.accounts, AccountProxyBindingRepository: f.bindings, locker: f.accounts.(service.KiloAccountMutationLocker), sourceID: source.ID, entered: make(chan int, 1), resume: resume}
	svc := service.NewKiloService(f.db, f.client, repo, &kiloNativeAdmin{}, f.groups, f.proxies, kiloNativeEncryptor{}, f.remote, f.egress, service.NewAccountProxyRouter(f.bindings, f.cache, f.egress), &config.Config{})
	worker := kiloNativeStartWorker(t, ctx, svc)
	var activationPID int
	select {
	case activationPID = <-repo.entered:
	case <-ctx.Done():
		t.Fatal("activation did not reach source-row barrier")
	}
	source.Credentials["email"] = "after-activation@example.test"
	edited := make(chan error, 1)
	go func() { edited <- f.accounts.Update(ctx, source) }()
	require.Eventually(t, func() bool {
		var blocked bool
		err := f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)))`, activationPID).Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond, "ordinary edit must wait for the activation transaction")
	release()
	kiloNativeWorkerFinished(t, worker)
	select {
	case err = <-edited:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("ordinary edit did not finish")
	}
	prepared, job := f.snapshot(t, ctx, preparedID)
	require.Equal(t, "bound", prepared.Status)
	require.Equal(t, "succeeded", job.Status, "activation and an ordinary edit must not deadlock")
	current, err := f.accounts.GetByID(ctx, source.ID)
	require.NoError(t, err)
	require.Equal(t, "after-activation@example.test", current.Credentials["email"], "the edit serialized after activation must be preserved")
}

func TestKiloNativeReleasePreservesLoginAndSourcePreference(t *testing.T) {
	for _, manualSchedule := range []bool{true, false} {
		t.Run(fmt.Sprintf("manual_schedulable_%t", manualSchedule), func(t *testing.T) {
			f := newKiloNativeFixture(t, 2)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			key := "synthetic-kilo-release-source"
			preparedID := f.prepared(t, ctx, "release-source", key)
			source := f.source(t, "release-source", "release-source@example.test")
			source.Schedulable = manualSchedule
			require.NoError(t, f.accounts.Update(ctx, source))
			f.remote.expect(key, "release-source", f.organization, service.KiloConnectionStatus{Connected: true, Email: "release-source@example.test"})
			batch := f.batch([]int64{preparedID}, []int64{source.ID})
			_, err := f.service().SubmitBatch(ctx, batch)
			require.NoError(t, err)
			processed, err := f.service().ProcessBatch(ctx, 1)
			require.NoError(t, err)
			require.Equal(t, 1, processed)
			prepared, job := f.snapshot(t, ctx, preparedID)
			require.Equal(t, "succeeded", job.Status)
			require.Equal(t, "bound", prepared.Status)
			require.NotNil(t, prepared.AccountID)
			require.NotNil(t, prepared.LoginProxyID)
			accountID, loginID := *prepared.AccountID, *prepared.LoginProxyID
			boundSource, err := f.accounts.GetByID(ctx, source.ID)
			require.NoError(t, err)
			require.Equal(t, manualSchedule, boundSource.Schedulable)
			require.False(t, boundSource.IsSchedulable())
			require.EqualValues(t, preparedID, service.ParseExtraInt(boundSource.Extra["kilo_source_prepared_id"]))
			require.EqualValues(t, accountID, service.ParseExtraInt(boundSource.Extra["kilo_source_gateway_id"]))
			require.Equal(t, source.Credentials, boundSource.Credentials)

			// Populate real persistent routes through an offline prober; release
			// must retain only the login route and return both request exits.
			loginProxy, err := f.proxies.GetByID(ctx, loginID)
			require.NoError(t, err)
			exits := map[string]string{loginProxy.URL(): "8.8.8.8"}
			requestIPs := []string{"1.1.1.1", "8.8.4.4"}
			i := 0
			for _, proxyURL := range f.selected {
				exits[proxyURL] = requestIPs[i]
				i++
			}
			egress := newKiloEgressWithProber(f.db, &kiloSQLEgressProber{exits: exits})
			loginRoute, err := egress.Verify(ctx, accountID, loginID)
			require.NoError(t, err)
			bindings, err := f.bindings.GetAccountProxyBindings(ctx, accountID)
			require.NoError(t, err)
			require.Len(t, bindings, batch.ProxiesPerAccount)
			for _, binding := range bindings {
				require.NotEqual(t, loginID, binding.ProxyID)
				_, err = egress.Verify(ctx, accountID, binding.ProxyID)
				require.NoError(t, err)
			}
			routes, err := egress.List(ctx, accountID)
			require.NoError(t, err)
			require.Len(t, routes, batch.ProxiesPerAccount+1)
			for _, route := range routes {
				if route.ProxyID == loginID {
					require.Equal(t, "login", route.Purpose)
				} else {
					require.Equal(t, "request", route.Purpose)
				}
			}
			svc := service.NewKiloService(f.db, f.client, f.accounts, &kiloNativeAdmin{}, f.groups, f.proxies, kiloNativeEncryptor{}, f.remote, egress, service.NewAccountProxyRouter(f.bindings, f.cache, egress), &config.Config{})
			require.NoError(t, svc.ReleasePrepared(ctx, preparedID))
			prepared, job = f.snapshot(t, ctx, preparedID)
			require.Equal(t, "ready", prepared.Status)
			require.Nil(t, prepared.SourceAccountID)
			require.Equal(t, &accountID, prepared.AccountID)
			require.Equal(t, &loginID, prepared.LoginProxyID)
			require.Equal(t, batch.ProxiesPerAccount, prepared.RoutingOptions.ProxiesPerAccount)
			require.Equal(t, batch.AutoReleaseStatuses, prepared.RoutingOptions.AutoReleaseStatuses)
			require.Equal(t, "succeeded", job.Status, "release must preserve the completed job receipt")
			gateway, err := f.accounts.GetByID(ctx, accountID)
			require.NoError(t, err)
			require.Equal(t, service.StatusDisabled, gateway.Status)
			require.False(t, gateway.Schedulable)
			require.EqualValues(t, loginID, service.ParseExtraInt(gateway.Extra[service.KiloLoginProxyExtraKey]))
			bindings, err = f.bindings.GetAccountProxyBindings(ctx, accountID)
			require.NoError(t, err)
			require.Empty(t, bindings)
			routes, err = egress.List(ctx, accountID)
			require.NoError(t, err)
			require.Len(t, routes, 1)
			require.Equal(t, loginRoute.ID, routes[0].ID)
			require.Equal(t, loginID, routes[0].ProxyID)
			require.Equal(t, "login", routes[0].Purpose)
			require.Equal(t, service.KiloEgressActive, routes[0].State)
			require.Empty(t, routes[0].EgressIP, "the fixed login is identified by its assigned proxy, without public-IP probing")
			require.EqualValues(t, 0, kiloNativeCount(t, ctx, f.db, `SELECT count(*) FROM pk_kilo_egress_owners`))
			currentSource, err := f.accounts.GetByID(ctx, source.ID)
			require.NoError(t, err)
			require.NotContains(t, currentSource.Extra, "kilo_source_prepared_id")
			require.NotContains(t, currentSource.Extra, "kilo_source_gateway_id")
			require.Equal(t, "preserve-source-preference", currentSource.Extra["fixture_note"])
			require.Equal(t, source.Credentials, currentSource.Credentials)
			require.Equal(t, manualSchedule, currentSource.Schedulable)
			require.Equal(t, manualSchedule, currentSource.IsSchedulable(), "release must restore eligibility only when the manual switch allows it")
			f.effects(t)
		})
	}
}

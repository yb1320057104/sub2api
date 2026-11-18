//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func kiloSQLCredentialProxy(t *testing.T, client *dbent.Client, protocol, host string, port int) *service.Proxy {
	t.Helper()
	return mustCreateProxy(t, client, &service.Proxy{
		Name:     "kilo-credential-" + uuid.NewString(),
		Protocol: protocol,
		Host:     host,
		Port:     port,
		Username: "synthetic-user",
		Password: "synthetic-password",
	})
}

func kiloSQLCredentialOwner(t *testing.T, db *sql.DB, proxyID int64) (int64, int) {
	t.Helper()
	var owner int64
	var count int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT COALESCE(MAX(o.account_id),0),COUNT(*) FROM pk_kilo_proxy_owners o JOIN proxies p ON o.credential_fingerprint=pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password) WHERE p.id=$1`, proxyID).Scan(&owner, &count))
	return owner, count
}

func TestKiloProxyCredentialOwnershipLifecycle(t *testing.T) {
	// The fixture owns a fully migrated private database; synthetic proxy URLs
	// and the injected prober never cause real proxy or public-IP requests.
	db, client := newBillingAuditDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a := kiloSQLManagedAccount(t, client)
	b := kiloSQLManagedAccount(t, client)
	original := kiloSQLCredentialProxy(t, client, "http", "Credential.Proxy.Invalid", 28001)
	httpAlias := kiloSQLCredentialProxy(t, client, "http", "CREDENTIAL.PROXY.INVALID", 28001)
	socksAlias := kiloSQLCredentialProxy(t, client, "socks5", "credential.proxy.invalid", 28001)
	blocker := kiloSQLCredentialProxy(t, client, "http", "other.proxy.invalid", 28002)
	aliases := []*service.Proxy{original, httpAlias, socksAlias}
	aliasIDs := []int64{original.ID, httpAlias.ID, socksAlias.ID}
	bindings := NewAccountRepository(client, db, nil).(service.AccountProxyBindingRepository)
	reader := NewProxyRepository(client, db).(service.KiloProxyOwnershipReader)
	prober := &kiloSQLEgressProber{exits: map[string]string{blocker.URL(): "8.8.8.8"}}
	egress := newKiloEgressWithProber(db, prober)
	binding := func(proxyID int64) []service.AccountProxyBindingInput {
		return []service.AccountProxyBindingInput{{ProxyID: proxyID, MaxAccounts: 1, MaxConcurrency: 1}}
	}
	assertOwner := func(expected int64, count int) {
		t.Helper()
		for _, proxy := range aliases {
			owner, gotCount := kiloSQLCredentialOwner(t, db, proxy.ID)
			require.Equal(t, expected, owner)
			require.Equal(t, count, gotCount)
		}
	}
	assertMetadata := func(deleted bool) {
		t.Helper()
		metadata, err := reader.KiloProxyOwnership(ctx, aliasIDs)
		require.NoError(t, err)
		require.Len(t, metadata, len(aliases))
		for _, proxy := range aliases {
			value, found := metadata[proxy.ID]
			require.True(t, found)
			require.True(t, value.Owned)
			require.NotNil(t, value.OwnerAccountID)
			require.Equal(t, a.ID, *value.OwnerAccountID)
			require.Equal(t, a.Name, value.OwnerAccountName)
			require.Equal(t, deleted, value.OwnerDeleted)
		}
	}

	require.NoError(t, bindings.ReplaceAccountProxyBindings(ctx, []int64{a.ID}, binding(original.ID)))
	assertOwner(0, 0)
	metadata, err := reader.KiloProxyOwnership(ctx, aliasIDs)
	require.NoError(t, err)
	require.Len(t, metadata, len(aliases))
	for _, value := range metadata {
		require.False(t, value.Owned, "binding alone must not consume credentials")
		require.Nil(t, value.OwnerAccountID)
		require.Empty(t, value.OwnerAccountName)
		require.False(t, value.OwnerDeleted)
	}

	// No external probe or public-IP ownership participates in registration.
	require.NoError(t, bindings.ReplaceAccountProxyBindings(ctx, []int64{b.ID}, binding(blocker.ID)))
	blockingRoute, err := egress.Verify(ctx, b.ID, blocker.ID)
	require.NoError(t, err)
	route, err := egress.Verify(ctx, a.ID, original.ID)
	require.NoError(t, err)
	require.Equal(t, service.KiloEgressActive, route.State)
	require.Equal(t, a.ID, route.AccountID)
	require.Empty(t, route.EgressIP)
	_, err = db.ExecContext(ctx, `UPDATE pk_kilo_egress_routes SET egress_ip='8.8.8.8' WHERE id IN ($1,$2)`, route.ID, blockingRoute.ID)
	require.NoError(t, err, "different proxy credentials may share historical IP metadata across accounts")
	require.NoError(t, egress.Release(ctx, blockingRoute.ID))
	require.NoError(t, bindings.ReplaceAccountProxyBindings(ctx, []int64{b.ID}, nil))
	assertOwner(a.ID, 1)
	assertMetadata(false)

	permit, err := egress.Acquire(ctx, a.ID, original.ID, original.URL())
	require.NoError(t, err)
	defer permit.Close()
	var aliasRouteID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO pk_kilo_egress_routes(account_id,proxy_id,config_hash,max_concurrency,credential_fingerprint) SELECT $1,$2,$3,1,pk_kilo_proxy_credential_fingerprint(host,port,username,password) FROM proxies WHERE id=$2 RETURNING id`, a.ID, httpAlias.ID, kiloProxyConfigHash(httpAlias.URL())).Scan(&aliasRouteID))
	require.ErrorIs(t, egress.Release(ctx, route.ID), service.ErrKiloEgressDraining)
	var boundCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pk_effective_account_proxy_bindings WHERE account_id=$1`, a.ID).Scan(&boundCount))
	require.Zero(t, boundCount, "request admission and planner binding stop immediately while the response stays open")
	var leaseID string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id::text FROM pk_kilo_egress_leases WHERE route_id=$1`, route.ID).Scan(&leaseID))
	stalePermit, err := egress.Acquire(ctx, a.ID, original.ID, original.URL())
	require.ErrorIs(t, err, service.ErrKiloEgressDraining, "a pre-release binding snapshot cannot admit another request")
	require.Nil(t, stalePermit)
	var drainReason string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT last_error FROM pk_kilo_egress_routes WHERE id=$1`, route.ID).Scan(&drainReason))
	require.Equal(t, "explicit_release", drainReason, "stale admission cannot overwrite the keeper's explicit drain marker")
	require.True(t, egress.refreshLease(ctx, a.ID, original.ID, route.ID, kiloProxyConfigHash(original.URL()), leaseID), "draining never cancels a matching held response")
	require.NoError(t, permit.Context.Err(), "the held response remains live after stale admission was rejected")
	require.NoError(t, ctx.Err())
	require.ErrorIs(t, bindings.ReplaceAccountProxyBindings(ctx, []int64{b.ID}, binding(socksAlias.ID)), service.ErrKiloProxyOwned)
	permit.Close()
	require.NoError(t, egress.Release(ctx, route.ID))
	assertOwner(a.ID, 1)
	require.ErrorIs(t, bindings.ReplaceAccountProxyBindings(ctx, []int64{b.ID}, binding(socksAlias.ID)), service.ErrKiloProxyOwned, "an unverified alias route retains the verified credential claim")
	require.NoError(t, egress.Release(ctx, aliasRouteID))
	require.NoError(t, egress.Release(ctx, aliasRouteID), "repeated maintenance release is idempotent")
	assertOwner(0, 0)
	_, ipCount := kiloSQLIPOwner(t, db, "8.8.8.8")
	require.Zero(t, ipCount, "proxy-entry admission never writes public-IP ownership")
	metadata, err = reader.KiloProxyOwnership(ctx, aliasIDs)
	require.NoError(t, err)
	for _, value := range metadata {
		require.False(t, value.Owned)
	}

	// Only credential aliases remain in the pool. The final release makes the
	// credential allocatable again, while one selected alias excludes the rest.
	_, err = db.ExecContext(ctx, `UPDATE proxies SET deleted_at=NOW() WHERE id=$1`, blocker.ID)
	require.NoError(t, err)
	for _, proxy := range aliases {
		require.NoError(t, bindings.ReplaceAccountProxyBindings(ctx, []int64{b.ID}, binding(proxy.ID)), "another account may reuse a completely released credential alias")
	}
	require.NoError(t, bindings.ReplaceAccountProxyBindings(ctx, []int64{b.ID}, nil))
	planInput := service.AccountProxyAssignmentInput{AccountIDs: []int64{b.ID}, ProxiesPerAccount: 1, MaxAccountsPerProxy: 1, ProxyConcurrency: 1}
	plan, err := bindings.AssignAccountProxies(ctx, planInput)
	require.NoError(t, err, "planner can reassign a released credential")
	require.Equal(t, 1, plan.BindingCount)
	require.NoError(t, bindings.ReplaceAccountProxyBindings(ctx, []int64{b.ID}, nil))

	for _, proxy := range aliases {
		require.NoError(t, bindings.ReplaceAccountProxyBindings(ctx, []int64{a.ID}, binding(proxy.ID)))
		prober.set(proxy.URL(), "1.1.1.1")
		reused, err := egress.Verify(ctx, a.ID, proxy.ID)
		require.NoError(t, err, "the original account may reuse HTTP, SOCKS and host-case aliases")
		require.Equal(t, a.ID, reused.AccountID)
		require.Empty(t, reused.EgressIP, "registration does not depend on an observed IP")
		require.NoError(t, egress.Release(ctx, reused.ID))
		require.NoError(t, bindings.ReplaceAccountProxyBindings(ctx, []int64{a.ID}, nil))
		assertOwner(0, 0)
	}
	planInput.AccountIDs = []int64{a.ID}
	plan, err = bindings.AssignAccountProxies(ctx, planInput)
	require.NoError(t, err, "planner may restore the original owner's credential")
	require.Equal(t, 1, plan.BindingCount)
	require.NoError(t, bindings.ReplaceAccountProxyBindings(ctx, []int64{a.ID}, nil))

	_, err = db.ExecContext(ctx, `UPDATE accounts SET deleted_at=NOW() WHERE id=$1`, a.ID)
	require.NoError(t, err)
	recreated := mustCreateAccount(t, client, &service.Account{Name: a.Name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Extra: map[string]any{service.KiloManagedExtraKey: true}})
	require.NotEqual(t, a.ID, recreated.ID)
	for _, proxy := range aliases {
		err = bindings.ReplaceAccountProxyBindings(ctx, []int64{recreated.ID}, binding(proxy.ID))
		require.NoError(t, err, "a recreated account can acquire a completely released credential")
	}
	planInput.AccountIDs = []int64{recreated.ID}
	plan, err = bindings.AssignAccountProxies(ctx, planInput)
	require.NoError(t, err, "soft deletion cannot resurrect an already released credential owner")
	require.Equal(t, 1, plan.BindingCount)
	assertOwner(0, 0)
	empty, err := reader.KiloProxyOwnership(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestKiloProxyCredentialOwnershipBatchReassignment(t *testing.T) {
	t.Run("replaces_old_aliases_using_final_plan", func(t *testing.T) {
		db, client := newBillingAuditDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		a, b := kiloSQLManagedAccount(t, client), kiloSQLManagedAccount(t, client)
		first := kiloSQLCredentialProxy(t, client, "http", "batch.proxy.invalid", 28201)
		alias := kiloSQLCredentialProxy(t, client, "socks5", "BATCH.PROXY.INVALID", 28201)
		independent := kiloSQLCredentialProxy(t, client, "http", "independent.proxy.invalid", 28202)
		// Upgrade data can contain unverified aliases; both old bindings are
		// replaced atomically, so they must not fence the final valid plan.
		_, err := db.ExecContext(ctx, `INSERT INTO pk_account_proxy_bindings(account_id,proxy_id,position) VALUES($1,$2,0),($3,$4,0)`, a.ID, first.ID, b.ID, alias.ID)
		require.NoError(t, err)
		repo := NewAccountRepository(client, db, nil).(service.AccountProxyBindingRepository)
		input := service.AccountProxyAssignmentInput{AccountIDs: []int64{a.ID, b.ID}, ProxiesPerAccount: 1, MaxAccountsPerProxy: 1, ProxyConcurrency: 1}
		result, err := repo.AssignAccountProxies(ctx, input)
		require.NoError(t, err)
		require.Equal(t, 2, result.BindingCount)
		var distinct int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)) FROM pk_account_proxy_bindings b JOIN proxies p ON p.id=b.proxy_id WHERE b.account_id IN ($1,$2)`, a.ID, b.ID).Scan(&distinct))
		require.Equal(t, 2, distinct)
		var independentUsed bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_account_proxy_bindings WHERE proxy_id=$1)`, independent.ID).Scan(&independentUsed))
		require.True(t, independentUsed)
		_, err = repo.AssignAccountProxies(ctx, input)
		require.NoError(t, err, "a valid final plan remains idempotent")
	})
	t.Run("mixed_accounts_cannot_share_aliases", func(t *testing.T) {
		db, client := newBillingAuditDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		managed := kiloSQLManagedAccount(t, client)
		ordinary := mustCreateAccount(t, client, &service.Account{Name: "ordinary-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey})
		kiloSQLCredentialProxy(t, client, "http", "mixed.proxy.invalid", 28203)
		kiloSQLCredentialProxy(t, client, "socks5", "MIXED.PROXY.INVALID", 28203)
		repo := NewAccountRepository(client, db, nil).(service.AccountProxyBindingRepository)
		for _, ids := range [][]int64{{managed.ID, ordinary.ID}, {ordinary.ID, managed.ID}} {
			_, err := repo.AssignAccountProxies(ctx, service.AccountProxyAssignmentInput{AccountIDs: ids, ProxiesPerAccount: 1, MaxAccountsPerProxy: 1, ProxyConcurrency: 1})
			require.Error(t, err)
		}
		var count int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pk_account_proxy_bindings WHERE account_id IN ($1,$2)`, managed.ID, ordinary.ID).Scan(&count))
		require.Zero(t, count, "a rejected mixed plan cannot publish either alias")
	})
}

func TestKiloProxyCredentialOwnershipConcurrent(t *testing.T) {
	db, client := newBillingAuditDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn, err := url.Parse(integrationPostgresDSN)
	require.NoError(t, err)
	var databaseName string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&databaseName))
	dsn.Path = "/" + databaseName
	peerDB, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	defer peerDB.Close()
	require.NoError(t, peerDB.PingContext(ctx))

	a := kiloSQLManagedAccount(t, client)
	b := kiloSQLManagedAccount(t, client)
	pa := kiloSQLCredentialProxy(t, client, "http", "Race.Proxy.Invalid", 28101)
	pb := kiloSQLCredentialProxy(t, client, "socks5", "race.proxy.invalid", 28101)
	// Simulate unverified bindings left by an older release. The new binding
	// policy correctly rejects these aliases before Verify, so using that helper
	// here would bypass the upgrade race this contract needs to exercise.
	_, err = db.ExecContext(ctx, `INSERT INTO pk_account_proxy_bindings(account_id,proxy_id,position) VALUES($1,$2,0),($3,$4,0)`, a.ID, pa.ID, b.ID, pb.ID)
	require.NoError(t, err)
	_, count := kiloSQLCredentialOwner(t, db, pa.ID)
	require.Zero(t, count)
	// Different public IPs ensure the loser is fenced by credential occupancy,
	// independently of IP uniqueness. Both repositories start simultaneously;
	// the first committed route reserves the credential before probing.
	ips := []string{"8.8.4.4", "9.9.9.9"}
	prober := &kiloSQLEgressProber{exits: map[string]string{pa.URL(): ips[0], pb.URL(): ips[1]}}
	repos := []*KiloEgressRepository{newKiloEgressWithProber(db, prober), newKiloEgressWithProber(peerDB, prober)}
	accountIDs := []int64{a.ID, b.ID}
	proxies := []*service.Proxy{pa, pb}
	type verification struct {
		index int
		route *service.KiloEgressRoute
		err   error
	}
	results := make(chan verification, 2)
	start := make(chan struct{})
	for i := range repos {
		go func(index int) {
			<-start
			route, err := repos[index].Verify(ctx, accountIDs[index], proxies[index].ID)
			results <- verification{index: index, route: route, err: err}
		}(i)
	}
	close(start)
	winner, successes, owned := -1, 0, 0
	for range repos {
		result := <-results
		switch {
		case result.err == nil:
			winner = result.index
			successes++
			require.NotNil(t, result.route)
			require.Equal(t, accountIDs[result.index], result.route.AccountID)
			require.Empty(t, result.route.EgressIP)
			require.Equal(t, service.KiloEgressActive, result.route.State)
		case errors.Is(result.err, service.ErrKiloProxyOwned):
			owned++
			require.Nil(t, result.route)
		default:
			t.Fatalf("unexpected concurrent credential verification failure: %v", result.err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, owned)
	require.GreaterOrEqual(t, winner, 0)
	for i, proxy := range proxies {
		owner, count := kiloSQLCredentialOwner(t, peerDB, proxy.ID)
		require.Equal(t, accountIDs[winner], owner)
		require.Equal(t, 1, count, "both transport aliases resolve to one current verified owner")
		ipOwner, ipCount := kiloSQLIPOwner(t, peerDB, ips[i])
		require.Zero(t, ipOwner)
		require.Zero(t, ipCount, "proxy registration must not create IP ownership")
	}
	var credentialOwners, ipOwners, activeRoutes int
	require.NoError(t, peerDB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM pk_kilo_proxy_owners),(SELECT COUNT(*) FROM pk_kilo_egress_owners),(SELECT COUNT(*) FROM pk_kilo_egress_routes WHERE state='active')`).Scan(&credentialOwners, &ipOwners, &activeRoutes))
	require.Equal(t, 1, credentialOwners)
	require.Zero(t, ipOwners)
	require.Equal(t, 1, activeRoutes)
}

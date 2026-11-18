//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestKiloEgressSQLTwentyByFiveRequestsKeepIndependentLogin(t *testing.T) {
	db, client := newBillingAuditDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	account := kiloSQLManagedAccount(t, client)
	login := mustCreateProxy(t, client, &service.Proxy{Name: "login-" + uuid.NewString(), Host: "login.invalid", Port: 29001})
	alias := mustCreateProxy(t, client, &service.Proxy{Name: "login-alias-" + uuid.NewString(), Protocol: "socks5", Host: "LOGIN.INVALID", Port: 29001})
	_, err := db.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(extra,'{kilo_login_proxy_id}',to_jsonb($2::bigint)) WHERE id=$1`, account.ID, login.ID)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		mustCreateProxy(t, client, &service.Proxy{Name: fmt.Sprintf("request-%d-%s", i, uuid.NewString()), Host: fmt.Sprintf("request-%d.invalid", i), Port: 29010 + i})
	}
	repo := NewAccountRepository(client, db, nil).(service.AccountProxyBindingRepository)
	assigned, err := repo.AssignAccountProxies(ctx, service.AccountProxyAssignmentInput{AccountIDs: []int64{account.ID}, ProxiesPerAccount: 5, MaxAccountsPerProxy: 1, ProxyConcurrency: 20})
	require.NoError(t, err)
	require.Equal(t, 5, assigned.BindingCount)
	bindings, err := repo.GetAccountProxyBindings(ctx, account.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 5)
	egress := NewKiloEgressGuard(db)
	loginRoute, err := egress.Verify(ctx, account.ID, login.ID)
	require.NoError(t, err, "login is allowed without an ordinary request binding")
	require.Equal(t, "login", loginRoute.Purpose)
	require.Equal(t, 1, loginRoute.MaxConcurrency)
	// All proxies deliberately share historical IP metadata. It must neither
	// merge their capacity nor affect login/request separation.
	_, err = db.ExecContext(ctx, `UPDATE pk_kilo_egress_routes SET egress_ip='8.8.8.8' WHERE id=$1`, loginRoute.ID)
	require.NoError(t, err)
	permits := make([]*service.KiloEgressPermit, 0, 101)
	t.Cleanup(func() {
		for _, permit := range permits {
			permit.Close()
		}
	})
	loginPermit, err := egress.Acquire(ctx, account.ID, 0, login.URL())
	require.NoError(t, err, "URL-only background callers resolve the exact pinned login")
	permits = append(permits, loginPermit)
	_, err = egress.Acquire(ctx, account.ID, login.ID, login.URL())
	require.ErrorIs(t, err, service.ErrKiloEgressCapacity, "login remains at one slot even when requests allow twenty")
	var requestRoutes []service.KiloEgressRoute
	for _, binding := range bindings {
		require.NotEqual(t, login.ID, binding.ProxyID)
		require.NotEqual(t, alias.ID, binding.ProxyID, "the login credential's transport alias cannot consume a request slot")
		var proxy service.Proxy
		proxy.ID = binding.ProxyID
		require.NoError(t, db.QueryRowContext(ctx, `SELECT protocol,host,port FROM proxies WHERE id=$1`, proxy.ID).Scan(&proxy.Protocol, &proxy.Host, &proxy.Port))
		route, err := egress.Verify(ctx, account.ID, proxy.ID)
		require.NoError(t, err)
		require.Equal(t, "request", route.Purpose)
		require.Equal(t, 20, route.MaxConcurrency)
		_, err = db.ExecContext(ctx, `UPDATE pk_kilo_egress_routes SET egress_ip='8.8.8.8' WHERE id=$1`, route.ID)
		require.NoError(t, err)
		requestRoutes = append(requestRoutes, *route)
		for slot := 0; slot < 20; slot++ {
			permit, err := egress.Acquire(ctx, account.ID, proxy.ID, proxy.URL())
			require.NoError(t, err, "all twenty per-proxy slots remain available despite shared IP metadata")
			permits = append(permits, permit)
		}
		_, err = egress.Acquire(ctx, account.ID, proxy.ID, proxy.URL())
		require.ErrorIs(t, err, service.ErrKiloEgressCapacity, "request concurrency is not relaxed")
	}
	var held int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pk_kilo_egress_leases WHERE account_id=$1`, account.ID).Scan(&held))
	require.Equal(t, 101, held, "100 request streams plus one independent login")
	for _, route := range requestRoutes {
		require.ErrorIs(t, egress.Release(ctx, route.ID), service.ErrKiloEgressDraining)
	}
	for _, permit := range permits[1:] {
		permit.Close()
	}
	for _, route := range requestRoutes {
		require.NoError(t, egress.Release(ctx, route.ID))
	}
	listed, err := egress.List(ctx, account.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, loginRoute.ID, listed[0].ID)
	require.Equal(t, "login", listed[0].Purpose)
	require.Equal(t, 1, listed[0].InFlight)
	var loginID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (extra->>'kilo_login_proxy_id')::bigint FROM accounts WHERE id=$1`, account.ID).Scan(&loginID))
	require.Equal(t, login.ID, loginID, "request release preserves fixed login identity")
	metadata, err := NewProxyRepository(client, db).(service.KiloProxyOwnershipReader).KiloProxyOwnership(ctx, []int64{login.ID, alias.ID})
	require.NoError(t, err)
	require.True(t, metadata[alias.ID].Owned)
	loginPermit.Close()
	require.NoError(t, egress.Release(ctx, loginRoute.ID), "abandoned login cleanup can explicitly release a route")
	owner, count := kiloSQLCredentialOwner(t, db, login.ID)
	require.Zero(t, owner)
	require.Zero(t, count, "no historical owner survives its final route")
	metadata, err = NewProxyRepository(client, db).(service.KiloProxyOwnershipReader).KiloProxyOwnership(ctx, []int64{alias.ID})
	require.NoError(t, err)
	require.True(t, metadata[alias.ID].Owned, "the explicit login pin reserves credentials even without a route")
	other := kiloSQLManagedAccount(t, client)
	err = repo.ReplaceAccountProxyBindings(ctx, []int64{other.ID}, []service.AccountProxyBindingInput{{ProxyID: alias.ID, MaxAccounts: 1, MaxConcurrency: 20}})
	require.ErrorIs(t, err, service.ErrKiloProxyOwned)
	_, err = db.ExecContext(ctx, `UPDATE accounts SET extra=extra-'kilo_login_proxy_id' WHERE id=$1`, account.ID)
	require.NoError(t, err)
	require.NoError(t, repo.ReplaceAccountProxyBindings(ctx, []int64{other.ID}, []service.AccountProxyBindingInput{{ProxyID: alias.ID, MaxAccounts: 1, MaxConcurrency: 20}}), "only explicit login teardown returns the login credential to the pool")
}

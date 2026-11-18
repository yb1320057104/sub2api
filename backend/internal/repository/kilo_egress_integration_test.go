//go:build integration

package repository

import (
	"context"
	"database/sql"
	"net/url"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The existing fixture migrates a private database and verifies shared billing
// and routing state is unchanged on cleanup. Every proxy/IP below is synthetic;
// the prober never performs network I/O.
type kiloSQLEgressProber struct {
	mu    sync.RWMutex
	exits map[string]string
}

func (p *kiloSQLEgressProber) set(proxyURL, ip string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exits[proxyURL] = ip
}

func (p *kiloSQLEgressProber) ProbeProxy(ctx context.Context, proxyURL string) (*service.ProxyExitInfo, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	ip, ok := p.exits[proxyURL]
	if !ok {
		return nil, 0, service.ErrKiloEgressUnavailable
	}
	return &service.ProxyExitInfo{IP: ip}, 1, nil
}

func kiloSQLManagedAccount(t *testing.T, client *dbent.Client) *service.Account {
	t.Helper()
	return mustCreateAccount(t, client, &service.Account{Name: "kilo-egress-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Extra: map[string]any{service.KiloManagedExtraKey: true}})
}

func kiloSQLBoundProxies(t *testing.T, db *sql.DB, client *dbent.Client, accountID int64, ports ...int) []*service.Proxy {
	t.Helper()
	proxies := make([]*service.Proxy, 0, len(ports))
	bindings := make([]service.AccountProxyBindingInput, 0, len(ports))
	for _, port := range ports {
		proxy := mustCreateProxy(t, client, &service.Proxy{Name: "kilo-egress-" + uuid.NewString(), Host: "proxy.invalid", Port: port})
		proxies = append(proxies, proxy)
		bindings = append(bindings, service.AccountProxyBindingInput{ProxyID: proxy.ID, MaxAccounts: 1, MaxConcurrency: 1})
	}
	repo := NewAccountRepository(client, db, nil).(service.AccountProxyBindingRepository)
	require.NoError(t, repo.ReplaceAccountProxyBindings(context.Background(), []int64{accountID}, bindings))
	return proxies
}

func kiloSQLIPOwner(t *testing.T, db *sql.DB, ip string) (int64, int) {
	t.Helper()
	var id int64
	var count int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT COALESCE(MAX(account_id),0),COUNT(*) FROM pk_kilo_egress_owners WHERE egress_ip=$1::inet`, ip).Scan(&id, &count))
	return id, count
}

// Historical test names remain stable for the release selector; the contract
// now admits independent proxy credentials even when public IP metadata matches.
func TestKiloEgressSQLPublicIPOwnershipAndDrain(t *testing.T) {
	db, client := newBillingAuditDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, b := kiloSQLManagedAccount(t, client), kiloSQLManagedAccount(t, client)
	pa := kiloSQLBoundProxies(t, db, client, a.ID, 18001, 18002, 18003)
	pb := kiloSQLBoundProxies(t, db, client, b.ID, 18004)
	repo := NewKiloEgressGuard(db)
	routes := make([]*service.KiloEgressRoute, len(pa))
	for i, proxy := range pa {
		var err error
		routes[i], err = repo.Verify(ctx, a.ID, proxy.ID)
		require.NoError(t, err)
		require.Equal(t, service.KiloEgressActive, routes[i].State)
		require.Empty(t, routes[i].EgressIP)
	}
	other, err := repo.Verify(ctx, b.ID, pb[0].ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE pk_kilo_egress_routes SET egress_ip='8.8.8.8' WHERE account_id IN ($1,$2)`, a.ID, b.ID)
	require.NoError(t, err)
	permit, err := repo.Acquire(ctx, a.ID, pa[0].ID, pa[0].URL())
	require.NoError(t, err)
	defer permit.Close()
	independent, err := repo.Acquire(ctx, a.ID, pa[1].ID, pa[1].URL())
	require.NoError(t, err, "same IP metadata must not merge independent proxy budgets")
	defer independent.Close()
	crossAccount, err := repo.Acquire(ctx, b.ID, pb[0].ID, pb[0].URL())
	require.NoError(t, err, "a different account may use its own proxy with the same public IP")
	defer crossAccount.Close()
	_, err = repo.Acquire(ctx, a.ID, pa[0].ID, pa[0].URL())
	require.ErrorIs(t, err, service.ErrKiloEgressCapacity)
	_, err = db.ExecContext(ctx, `UPDATE pk_kilo_egress_leases SET expires_at=NOW()-INTERVAL '1 second' WHERE route_id=$1`, routes[0].ID)
	require.NoError(t, err)
	listed, err := repo.List(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 1, listed[0].ExpiredLeases)
	require.ErrorIs(t, repo.Release(ctx, routes[0].ID), service.ErrKiloEgressDraining)
	require.ErrorIs(t, NewAccountRepository(client, db, nil).(service.AccountProxyBindingRepository).ReplaceAccountProxyBindings(ctx, []int64{b.ID}, []service.AccountProxyBindingInput{{ProxyID: pa[0].ID, MaxAccounts: 1, MaxConcurrency: 1}}), service.ErrKiloProxyOwned)
	permit.Close()
	require.NoError(t, repo.Release(ctx, routes[0].ID))
	owner, count := kiloSQLCredentialOwner(t, db, pa[0].ID)
	require.Zero(t, owner)
	require.Zero(t, count)
	owner, count = kiloSQLCredentialOwner(t, db, pa[1].ID)
	require.Equal(t, a.ID, owner)
	require.Equal(t, 1, count)
	crossAccount.Close()
	require.NoError(t, repo.Release(ctx, other.ID))
}

func TestKiloEgressSQLIndependentRepositoriesConcurrentOwnershipAndSlots(t *testing.T) {
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
	a, b := kiloSQLManagedAccount(t, client), kiloSQLManagedAccount(t, client)
	pa := kiloSQLBoundProxies(t, db, client, a.ID, 19001)[0]
	pb := kiloSQLBoundProxies(t, db, client, b.ID, 19002)[0]
	repos := []*KiloEgressRepository{NewKiloEgressGuard(db), NewKiloEgressGuard(peerDB)}
	ids := []int64{a.ID, b.ID}
	proxies := []*service.Proxy{pa, pb}
	results := make(chan error, 2)
	start := make(chan struct{})
	for i := range repos {
		go func(index int) {
			<-start
			_, err := repos[index].Verify(ctx, ids[index], proxies[index].ID)
			results <- err
		}(i)
	}
	close(start)
	for range repos {
		require.NoError(t, <-results)
	}
	_, err = db.ExecContext(ctx, `UPDATE pk_kilo_egress_routes SET egress_ip='8.8.4.4'`)
	require.NoError(t, err)
	type admission struct {
		permit *service.KiloEgressPermit
		err    error
	}
	admitted := make(chan admission, 2)
	start = make(chan struct{})
	for _, repo := range repos {
		go func(r *KiloEgressRepository) {
			<-start
			p, e := r.Acquire(ctx, a.ID, pa.ID, pa.URL())
			admitted <- admission{p, e}
		}(repo)
	}
	close(start)
	successes, capacity := 0, 0
	var held *service.KiloEgressPermit
	for range repos {
		v := <-admitted
		if v.err == nil {
			successes++
			held = v.permit
			defer held.Close()
		} else {
			require.ErrorIs(t, v.err, service.ErrKiloEgressCapacity)
			capacity++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, capacity)
	other, err := repos[1].Acquire(ctx, b.ID, pb.ID, pb.URL())
	require.NoError(t, err, "another credential keeps its own independent slot")
	defer other.Close()
	held.Close()
	var leases int
	require.NoError(t, peerDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pk_kilo_egress_leases WHERE account_id=$1`, a.ID).Scan(&leases))
	require.Zero(t, leases)
	owner, count := kiloSQLCredentialOwner(t, peerDB, pa.ID)
	require.Equal(t, a.ID, owner)
	require.Equal(t, 1, count, "closing a response frees capacity; explicit release returns its credential")
}

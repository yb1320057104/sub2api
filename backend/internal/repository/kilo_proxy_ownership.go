package repository

import (
	"context"
	"database/sql"
	"errors"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Credential aliases share current occupancy across rows and transports.
// Request bindings may be replaced as a batch; login pins and routes survive it.
type kiloProxyAssignmentPolicy struct {
	managed          map[int64]bool
	fingerprints     map[int64]string
	owners           map[string]int64
	liveOwners       map[string]map[int64]bool
	loginOwners      map[string]map[int64]bool
	explicitBindings bool
}

func (p *kiloProxyAssignmentPolicy) allows(accountID, proxyID int64, selected map[string]int64) bool {
	if p == nil {
		return true
	}
	fingerprint, exists := p.fingerprints[proxyID]
	if !exists {
		return false
	}
	// Ordinary accounts retain their sharing policy, but a mixed batch must
	// never hide a managed account behind another row for the same credential.
	if owner, chosen := selected[fingerprint]; chosen && (p.managed[accountID] || p.managed[owner]) {
		return false
	}
	if !p.explicitBindings && p.loginOwners[fingerprint][accountID] {
		return false
	}
	if owner, claimed := p.owners[fingerprint]; claimed {
		if owner == accountID {
			return true
		}
		if owner == 0 || p.managed[accountID] || p.managed[owner] {
			return false
		}
	}
	for owner := range p.liveOwners[fingerprint] {
		if owner != accountID && (p.managed[accountID] || p.managed[owner]) {
			return false
		}
	}
	return true
}

func (p *kiloProxyAssignmentPolicy) remember(accountID, proxyID int64, selected map[string]int64) {
	if p != nil {
		selected[p.fingerprints[proxyID]] = accountID
	}
}

func (p *kiloProxyAssignmentPolicy) prefers(accountID, proxyID, otherID int64) bool {
	return p != nil && p.managed[accountID] && p.owners[p.fingerprints[proxyID]] == accountID && p.owners[p.fingerprints[otherID]] != accountID
}

func loadKiloProxyAssignmentPolicy(ctx context.Context, client *dbent.Client, accountIDs, proxyIDs []int64) (*kiloProxyAssignmentPolicy, error) {
	rows, err := client.QueryContext(ctx, `SELECT id FROM accounts WHERE (id=ANY($1) OR parent_account_id IS NULL) AND extra->'kilo_managed'='true'::jsonb`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	policy := &kiloProxyAssignmentPolicy{managed: map[int64]bool{}, fingerprints: map[int64]string{}, owners: map[string]int64{}, liveOwners: map[string]map[int64]bool{}, loginOwners: map[string]map[int64]bool{}}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		policy.managed[id] = true
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if len(policy.managed) == 0 {
		return nil, nil
	}
	rows, err = client.QueryContext(ctx, `SELECT p.id,pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password),o.credential_fingerprint IS NOT NULL,o.account_id FROM proxies p LEFT JOIN pk_kilo_proxy_owners o ON o.credential_fingerprint=pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password) AND EXISTS(SELECT 1 FROM pk_kilo_egress_routes r WHERE r.credential_fingerprint=o.credential_fingerprint OR (r.credential_fingerprint IS NULL AND (o.account_id IS NULL OR r.account_id=o.account_id))) WHERE p.id=ANY($1)`, pq.Array(proxyIDs))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var fingerprint string
		var claimed bool
		var owner sql.NullInt64
		if err = rows.Scan(&id, &fingerprint, &claimed, &owner); err != nil {
			break
		}
		policy.fingerprints[id] = fingerprint
		if claimed {
			policy.owners[fingerprint] = owner.Int64
		}
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = client.QueryContext(ctx, `WITH occupancy AS (
 SELECT pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password) AS fingerprint,b.account_id,0::bigint AS login_proxy_id FROM pk_effective_account_proxy_bindings b JOIN accounts a ON a.id=b.account_id AND a.parent_account_id IS NULL JOIN proxies p ON p.id=b.proxy_id WHERE NOT (b.account_id=ANY($2))
 UNION ALL SELECT pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password),a.id,p.id FROM accounts a JOIN proxies p ON a.extra->'kilo_login_proxy_id'=to_jsonb(p.id) WHERE a.extra->'kilo_managed'='true'::jsonb
 UNION ALL SELECT COALESCE(r.credential_fingerprint,pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)),r.account_id,0::bigint FROM pk_kilo_egress_routes r JOIN proxies p ON p.id=r.proxy_id
) SELECT DISTINCT fingerprint,account_id,login_proxy_id FROM occupancy WHERE fingerprint IN (SELECT pk_kilo_proxy_credential_fingerprint(q.host,q.port,q.username,q.password) FROM proxies q WHERE q.id=ANY($1))`, pq.Array(proxyIDs), pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var fingerprint string
		var owner, loginProxyID int64
		if err = rows.Scan(&fingerprint, &owner, &loginProxyID); err != nil {
			break
		}
		if policy.liveOwners[fingerprint] == nil {
			policy.liveOwners[fingerprint] = map[int64]bool{}
		}
		policy.liveOwners[fingerprint][owner] = true
		if loginProxyID > 0 {
			if policy.loginOwners[fingerprint] == nil {
				policy.loginOwners[fingerprint] = map[int64]bool{}
			}
			policy.loginOwners[fingerprint][owner] = true
		}
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	return policy, err
}

// Reads only. Verified claims live while a route exists; a configured login
// pin is also reserved before its first verification and after route cleanup.
func checkKiloProxyFingerprint(ctx context.Context, q kiloEgressQueryer, accountID int64, proxy *service.Proxy) (string, error) {
	var fingerprint string
	if err := q.QueryRowContext(ctx, `SELECT pk_kilo_proxy_credential_fingerprint($1::text,$2::integer,$3::text,$4::text)`, proxy.Host, proxy.Port, proxy.Username, proxy.Password).Scan(&fingerprint); err != nil {
		return "", service.ErrKiloEgressUnavailable
	}
	var owner sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT account_id FROM (SELECT o.account_id,0 AS priority,0::bigint AS claim_id FROM pk_kilo_proxy_owners o WHERE o.credential_fingerprint=$1 AND EXISTS(SELECT 1 FROM pk_kilo_egress_routes r WHERE r.credential_fingerprint=o.credential_fingerprint OR (r.credential_fingerprint IS NULL AND (o.account_id IS NULL OR r.account_id=o.account_id))) UNION ALL SELECT r.account_id,1,r.id FROM pk_kilo_egress_routes r JOIN proxies p ON p.id=r.proxy_id WHERE COALESCE(r.credential_fingerprint,pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password))=$1 UNION ALL SELECT a.id,2,a.id FROM accounts a JOIN proxies p ON a.extra->'kilo_login_proxy_id'=to_jsonb(p.id) WHERE a.extra->'kilo_managed'='true'::jsonb AND pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)=$1) claims ORDER BY priority,claim_id LIMIT 1`, fingerprint).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return fingerprint, nil
	}
	if err != nil {
		return "", service.ErrKiloEgressUnavailable
	}
	if !owner.Valid || owner.Int64 != accountID {
		return "", service.ErrKiloProxyOwned
	}
	return fingerprint, nil
}

func checkKiloProxyAssignments(ctx context.Context, client *dbent.Client, accountIDs []int64, assignments map[int64][]service.AccountProxyBindingInput) error {
	seen := map[int64]bool{}
	for _, items := range assignments {
		for _, item := range items {
			seen[item.ProxyID] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	policy, err := loadKiloProxyAssignmentPolicy(ctx, client, accountIDs, proxyIDSet(seen))
	if err != nil || policy == nil {
		return err
	}
	// Explicit request binding edits may opt into a shared login route; automatic
	// planning always selects independent request credentials.
	policy.explicitBindings = true
	selected := map[string]int64{}
	for _, accountID := range accountIDs {
		for _, item := range assignments[accountID] {
			if !policy.allows(accountID, item.ProxyID, selected) {
				return infraerrors.Conflict("KILO_PROXY_OWNED", "代理凭据当前由其他账号占用、存在别名占用或本次重复选择；请先排空释放或选择独立出口").WithCause(service.ErrKiloProxyOwned)
			}
			policy.remember(accountID, item.ProxyID, selected)
		}
	}
	return nil
}

// The admin list exposes ownership metadata without returning proxy credentials.
func (r *proxyRepository) KiloProxyOwnership(ctx context.Context, ids []int64) (map[int64]service.KiloProxyOwnership, error) {
	out := map[int64]service.KiloProxyOwnership{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.sql.QueryContext(ctx, `WITH claims AS (
 SELECT o.credential_fingerprint,o.account_id,0 AS priority,0::bigint AS claim_id FROM pk_kilo_proxy_owners o WHERE EXISTS(SELECT 1 FROM pk_kilo_egress_routes r WHERE r.credential_fingerprint=o.credential_fingerprint OR (r.credential_fingerprint IS NULL AND (o.account_id IS NULL OR r.account_id=o.account_id)))
 UNION ALL SELECT pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password),a.id,2,a.id FROM accounts a JOIN proxies p ON a.extra->'kilo_login_proxy_id'=to_jsonb(p.id) WHERE a.extra->'kilo_managed'='true'::jsonb
 UNION ALL SELECT COALESCE(r.credential_fingerprint,pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)),r.account_id,1,r.id FROM pk_kilo_egress_routes r JOIN proxies p ON p.id=r.proxy_id
),owners AS (SELECT DISTINCT ON (credential_fingerprint) credential_fingerprint,account_id FROM claims ORDER BY credential_fingerprint,priority,claim_id) SELECT p.id,o.credential_fingerprint IS NOT NULL,o.account_id,COALESCE(a.name,''),o.credential_fingerprint IS NOT NULL AND (a.id IS NULL OR a.deleted_at IS NOT NULL) FROM proxies p LEFT JOIN owners o ON o.credential_fingerprint=pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password) LEFT JOIN accounts a ON a.id=o.account_id WHERE p.id=ANY($1)`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var value service.KiloProxyOwnership
		if err = rows.Scan(&id, &value.Owned, &value.OwnerAccountID, &value.OwnerAccountName, &value.OwnerDeleted); err != nil {
			return nil, err
		}
		out[id] = value
	}
	return out, rows.Err()
}

var _ service.KiloProxyOwnershipReader = (*proxyRepository)(nil)

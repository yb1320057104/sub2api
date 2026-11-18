package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

const (
	kiloEgressLock         int64 = 70440079
	kiloEgressLeaseTTL           = 45 * time.Second
	kiloEgressOperationTTL       = 3 * time.Second
)

// KiloEgressRepository owns proxy credential admission and response leases.
// Public IP observations never determine allocation or concurrency.
type KiloEgressRepository struct {
	db *sql.DB
}

func NewKiloEgressGuard(db *sql.DB) *KiloEgressRepository {
	return &KiloEgressRepository{db: db}
}

// Keep the existing test boundary while proving no IP probe is needed.
func newKiloEgressWithProber(db *sql.DB, _ service.ProxyExitInfoProber) *KiloEgressRepository {
	return NewKiloEgressGuard(db)
}

// NewKiloEgressManager is the management-side Wire provider. Accounts are
// managed by the existing repository; verification reads its committed bindings.
func NewKiloEgressManager(db *sql.DB, _ service.AccountRepository) service.KiloEgressManager {
	return NewKiloEgressGuard(db)
}

var _ service.KiloEgressGuard = (*KiloEgressRepository)(nil)
var _ service.KiloEgressManager = (*KiloEgressRepository)(nil)

func kiloProxyConfigHash(proxyURL string) string {
	hash := sha256.Sum256([]byte(proxyURL))
	return hex.EncodeToString(hash[:])
}

type kiloEgressQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func kiloManagedAccount(ctx context.Context, q kiloEgressQueryer, accountID int64, lock bool) (bool, error) {
	query := `SELECT COALESCE(extra->'kilo_managed'='true'::jsonb,FALSE),deleted_at FROM accounts WHERE id=$1`
	if lock {
		query += ` FOR SHARE`
	}
	var managed bool
	var deleted sql.NullTime
	err := q.QueryRowContext(ctx, query, accountID).Scan(&managed, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err == nil && managed && deleted.Valid {
		return false, service.ErrKiloEgressUnavailable
	}
	return managed, err
}

type kiloProxySnapshot struct {
	proxy   service.Proxy
	bound   bool
	login   bool
	deleted sql.NullTime
	hash    string
	limit   int
}

func readKiloProxy(ctx context.Context, q kiloEgressQueryer, accountID, proxyID int64, lock bool) (*kiloProxySnapshot, error) {
	query := `SELECT p.protocol,p.host,p.port,COALESCE(p.username,''),COALESCE(p.password,''),p.status,p.expires_at,p.deleted_at,COALESCE(l.max_concurrency,0),EXISTS(SELECT 1 FROM pk_effective_account_proxy_bindings b WHERE b.account_id=$1 AND b.proxy_id=p.id),EXISTS(SELECT 1 FROM accounts a WHERE a.id=$1 AND a.extra->'kilo_login_proxy_id'=to_jsonb(p.id))
FROM proxies p LEFT JOIN pk_proxy_limits l ON l.proxy_id=p.id WHERE p.id=$2`
	if lock {
		query += ` FOR SHARE OF p`
	}
	s := &kiloProxySnapshot{}
	if err := q.QueryRowContext(ctx, query, accountID, proxyID).Scan(&s.proxy.Protocol, &s.proxy.Host, &s.proxy.Port, &s.proxy.Username, &s.proxy.Password, &s.proxy.Status, &s.proxy.ExpiresAt, &s.deleted, &s.limit, &s.bound, &s.login); err != nil {
		return nil, err
	}
	s.proxy.ID = proxyID
	s.hash = kiloProxyConfigHash(s.proxy.URL())
	if s.limit <= 0 {
		s.limit = 10
	}
	if s.login {
		s.limit = 1
	}
	return s, nil
}

func (s *kiloProxySnapshot) available() bool {
	return (s.bound || s.login) && !s.deleted.Valid && s.proxy.IsActive() && !s.proxy.IsExpired(time.Now())
}

// Short metadata transactions serialize credential admission, slot counts and
// explicit release. Response streams retain leases without holding these locks;
// aliases of one credential share a budget while different proxies do not.
func (r *KiloEgressRepository) begin(ctx context.Context) (*sql.Tx, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	if err = service.LockKiloOnboardingLease(ctx, tx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	// Match ordinary binding mutations before taking the egress metadata lock.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, accountProxyAssignmentLock); err != nil {
		_ = tx.Rollback()
		return nil, service.ErrKiloEgressUnavailable
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kiloEgressLock); err != nil {
		_ = tx.Rollback()
		return nil, service.ErrKiloEgressUnavailable
	}
	return tx, nil
}

const kiloRouteColumns = `r.id,r.account_id,r.proxy_id,COALESCE(host(r.egress_ip),''),r.state,r.max_concurrency,(SELECT COUNT(*) FROM pk_kilo_egress_leases l WHERE l.route_id=r.id),r.verified_at,r.last_error,r.created_at,r.updated_at,CASE WHEN (SELECT a.extra->'kilo_login_proxy_id' FROM accounts a WHERE a.id=r.account_id)=to_jsonb(r.proxy_id) THEN 'login' ELSE 'request' END`

type kiloRouteRow struct {
	service.KiloEgressRoute
	hash        string
	fingerprint string
}

func (r *kiloRouteRow) fingerprintOrHash() string {
	if r.fingerprint != "" {
		return r.fingerprint
	}
	return r.hash
}

type kiloRowScanner interface{ Scan(...any) error }

func scanKiloRoute(row kiloRowScanner, withHash bool) (*kiloRouteRow, error) {
	out := &kiloRouteRow{}
	args := []any{&out.ID, &out.AccountID, &out.ProxyID, &out.EgressIP, &out.State, &out.MaxConcurrency, &out.InFlight, &out.VerifiedAt, &out.LastError, &out.CreatedAt, &out.UpdatedAt, &out.Purpose}
	if withHash {
		args = append(args, &out.hash, &out.fingerprint)
	}
	if err := row.Scan(args...); err != nil {
		return nil, err
	}
	return out, nil
}

func readKiloRoute(ctx context.Context, tx *sql.Tx, accountID, proxyID int64) (*kiloRouteRow, error) {
	return scanKiloRoute(tx.QueryRowContext(ctx, `SELECT `+kiloRouteColumns+`,r.config_hash,COALESCE(r.credential_fingerprint,'') FROM pk_kilo_egress_routes r WHERE r.account_id=$1 AND r.proxy_id=$2 FOR UPDATE OF r`, accountID, proxyID), true)
}

func (r *KiloEgressRepository) List(ctx context.Context, accountID int64) ([]service.KiloEgressRoute, error) {
	if r == nil || r.db == nil || accountID <= 0 {
		return nil, service.ErrKiloEgressUnavailable
	}
	managed, err := kiloManagedAccount(ctx, r.db, accountID, false)
	if err != nil || !managed {
		return nil, service.ErrKiloEgressUnavailable
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+kiloRouteColumns+` FROM pk_kilo_egress_routes r WHERE r.account_id=$1 ORDER BY r.id`, accountID)
	if err != nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.KiloEgressRoute, 0)
	for rows.Next() {
		route, err := scanKiloRoute(rows, false)
		if err != nil {
			return nil, service.ErrKiloEgressUnavailable
		}
		out = append(out, route.KiloEgressRoute)
	}
	if rows.Err() != nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	_ = rows.Close()
	leases, err := r.db.QueryContext(ctx, `SELECT id::text,route_id,expires_at,created_at,expires_at<=NOW() FROM pk_kilo_egress_leases WHERE account_id=$1 ORDER BY route_id,created_at,id`, accountID)
	if err != nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	defer func() { _ = leases.Close() }()
	indices := make(map[int64]int, len(out))
	for i := range out {
		indices[out[i].ID] = i
		out[i].Leases = make([]service.KiloEgressLease, 0)
	}
	for leases.Next() {
		var lease service.KiloEgressLease
		if leases.Scan(&lease.ID, &lease.RouteID, &lease.ExpiresAt, &lease.CreatedAt, &lease.Expired) != nil {
			return nil, service.ErrKiloEgressUnavailable
		}
		if i, ok := indices[lease.RouteID]; ok {
			out[i].Leases = append(out[i].Leases, lease)
			if lease.Expired {
				out[i].ExpiredLeases++
			}
		}
	}
	if leases.Err() != nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	return out, nil
}

func commitKiloResult(tx *sql.Tx, route *kiloRouteRow, resultErr error) (*service.KiloEgressRoute, error) {
	if tx.Commit() != nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	if route == nil {
		return nil, resultErr
	}
	return &route.KiloEgressRoute, resultErr
}

func setKiloRouteState(ctx context.Context, tx *sql.Tx, id int64, state, reason string) error {
	_, err := tx.ExecContext(ctx, `UPDATE pk_kilo_egress_routes SET state=$2,last_error=$3,updated_at=NOW() WHERE id=$1`, id, state, reason)
	return err
}

func (r *KiloEgressRepository) prepareVerification(ctx context.Context, accountID, proxyID int64) (*kiloRouteRow, *kiloProxySnapshot, error) {
	opCtx, cancel := context.WithTimeout(ctx, kiloEgressOperationTTL)
	defer cancel()
	tx, err := r.begin(opCtx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	managed, err := kiloManagedAccount(opCtx, tx, accountID, true)
	if err != nil || !managed {
		return nil, nil, service.ErrKiloEgressUnavailable
	}
	proxy, err := readKiloProxy(opCtx, tx, accountID, proxyID, true)
	if err != nil || !proxy.available() {
		return nil, nil, service.ErrKiloEgressUnavailable
	}
	fingerprint, err := checkKiloProxyFingerprint(opCtx, tx, accountID, &proxy.proxy)
	if err != nil {
		return nil, nil, err
	}
	_, err = tx.ExecContext(opCtx, `INSERT INTO pk_kilo_egress_routes(account_id,proxy_id,config_hash,max_concurrency,credential_fingerprint) VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_id,proxy_id) DO NOTHING`, accountID, proxyID, proxy.hash, proxy.limit, fingerprint)
	if err != nil {
		return nil, nil, service.ErrKiloEgressUnavailable
	}
	route, err := readKiloRoute(opCtx, tx, accountID, proxyID)
	if err != nil {
		return nil, nil, service.ErrKiloEgressUnavailable
	}
	if route.State == service.KiloEgressDraining {
		return nil, nil, service.ErrKiloEgressDraining
	}
	if route.hash != proxy.hash {
		if setKiloRouteState(opCtx, tx, route.ID, service.KiloEgressDraining, "proxy_configuration_changed") != nil {
			return nil, nil, service.ErrKiloEgressUnavailable
		}
		_, err = commitKiloResult(tx, nil, service.ErrKiloEgressDraining)
		return nil, nil, err
	}
	if route.VerifiedAt == nil {
		if _, err = tx.ExecContext(opCtx, `UPDATE pk_kilo_egress_routes SET config_hash=$2,max_concurrency=$3,credential_fingerprint=$4,state='unverified',last_error='',updated_at=NOW() WHERE id=$1`, route.ID, proxy.hash, proxy.limit, fingerprint); err != nil {
			return nil, nil, service.ErrKiloEgressUnavailable
		}
		route.hash = proxy.hash
	}
	if tx.Commit() != nil {
		return nil, nil, service.ErrKiloEgressUnavailable
	}
	return route, proxy, nil
}

func (r *KiloEgressRepository) Verify(ctx context.Context, accountID, proxyID int64) (*service.KiloEgressRoute, error) {
	if accountID <= 0 || proxyID <= 0 || r == nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	route, _, err := r.prepareVerification(ctx, accountID, proxyID)
	if err != nil {
		return nil, err
	}
	// Admission belongs to the configured proxy credential, not its public IP.
	// Metadata publication still checks the durable onboarding lease.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), kiloEgressOperationTTL)
	defer finishCancel()
	return r.finishVerification(finishCtx, route, "", nil)
}

func (r *KiloEgressRepository) finishVerification(ctx context.Context, before *kiloRouteRow, ip string, probeErr error, admissionLease ...string) (*service.KiloEgressRoute, error) {
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	managed, err := kiloManagedAccount(ctx, tx, before.AccountID, true)
	if err != nil || !managed {
		return nil, service.ErrKiloEgressUnavailable
	}
	proxy, err := readKiloProxy(ctx, tx, before.AccountID, before.ProxyID, true)
	if err != nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	route, err := readKiloRoute(ctx, tx, before.AccountID, before.ProxyID)
	if err != nil || route.ID != before.ID {
		return nil, service.ErrKiloEgressUnavailable
	}
	if route.State == service.KiloEgressDraining {
		return nil, service.ErrKiloEgressDraining
	}
	if len(admissionLease) > 0 && route.State != service.KiloEgressActive {
		return nil, service.ErrKiloEgressUnavailable
	}
	if !proxy.available() || proxy.hash != before.hash || route.hash != before.hash {
		if setKiloRouteState(ctx, tx, route.ID, service.KiloEgressDraining, "proxy_configuration_or_egress_changed") != nil {
			return nil, service.ErrKiloEgressUnavailable
		}
		return commitKiloResult(tx, nil, service.ErrKiloEgressDraining)
	}
	if probeErr != nil {
		state, reason := service.KiloEgressFailed, "egress_probe_failed"
		if errors.Is(probeErr, service.ErrKiloEgressUnstable) {
			state, reason = service.KiloEgressDraining, "egress_not_stable_public_ip"
		}
		if setKiloRouteState(ctx, tx, route.ID, state, reason) != nil {
			return nil, service.ErrKiloEgressUnavailable
		}
		return commitKiloResult(tx, nil, probeErr)
	}
	fingerprint, err := checkKiloProxyFingerprint(ctx, tx, route.AccountID, &proxy.proxy)
	if err != nil {
		return nil, err
	}
	// A successful claim lasts until its last route is explicitly drained and
	// released, including unverified or failed aliases and outstanding leases.
	if _, err = tx.ExecContext(ctx, `INSERT INTO pk_kilo_proxy_owners(credential_fingerprint,account_id) VALUES($1,$2) ON CONFLICT(credential_fingerprint) DO NOTHING`, fingerprint, route.AccountID); err != nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	if len(admissionLease) > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE pk_kilo_egress_leases SET expires_at=NOW()+INTERVAL '45 seconds' WHERE id=$1::uuid AND account_id=$2 AND route_id=$3 AND expires_at>NOW()`, admissionLease[0], route.AccountID, route.ID)
		if err != nil {
			return nil, service.ErrKiloEgressUnavailable
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return nil, service.ErrKiloEgressUnavailable
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pk_kilo_egress_routes SET egress_ip=COALESCE(NULLIF($2,'')::inet,egress_ip),state='active',verified_at=NOW(),max_concurrency=$3,credential_fingerprint=COALESCE(credential_fingerprint,$4),last_error='',updated_at=NOW() WHERE id=$1`, route.ID, ip, proxy.limit, fingerprint); err != nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	route, err = readKiloRoute(ctx, tx, before.AccountID, before.ProxyID)
	if err != nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	return commitKiloResult(tx, route, nil)
}

func (r *KiloEgressRepository) Release(ctx context.Context, routeID int64) error {
	if routeID <= 0 {
		return service.ErrKiloEgressUnavailable
	}
	opCtx, cancel := context.WithTimeout(ctx, kiloEgressOperationTTL)
	defer cancel()
	tx, err := r.begin(opCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var accountID, proxyID int64
	var ip string
	err = tx.QueryRowContext(opCtx, `SELECT account_id,proxy_id,COALESCE(host(egress_ip),'') FROM pk_kilo_egress_routes WHERE id=$1 FOR UPDATE`, routeID).Scan(&accountID, &proxyID, &ip)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return service.ErrKiloEgressUnavailable
	}
	if err = releaseKiloRequestBinding(opCtx, tx, accountID, proxyID); err != nil {
		return service.ErrKiloEgressUnavailable
	}
	if setKiloRouteState(opCtx, tx, routeID, service.KiloEgressDraining, "explicit_release") != nil {
		return service.ErrKiloEgressUnavailable
	}
	var pending int
	if tx.QueryRowContext(opCtx, `SELECT COUNT(*) FROM pk_kilo_egress_leases WHERE route_id=$1`, routeID).Scan(&pending) != nil {
		return service.ErrKiloEgressUnavailable
	}
	if pending > 0 {
		_, err = commitKiloResult(tx, nil, service.ErrKiloEgressDraining)
		return err
	}
	if _, err = tx.ExecContext(opCtx, `DELETE FROM pk_kilo_egress_routes WHERE id=$1`, routeID); err != nil {
		return service.ErrKiloEgressUnavailable
	}
	if _, err = tx.ExecContext(opCtx, `DELETE FROM pk_kilo_proxy_owners o WHERE NOT EXISTS(SELECT 1 FROM pk_kilo_egress_routes r WHERE r.credential_fingerprint=o.credential_fingerprint OR (r.credential_fingerprint IS NULL AND (o.account_id IS NULL OR r.account_id=o.account_id)))`); err != nil {
		return service.ErrKiloEgressUnavailable
	}
	if tx.Commit() != nil {
		return service.ErrKiloEgressUnavailable
	}
	return nil
}

// Changing the legacy primary invokes a trigger that clears the complete set.
// Materialize and restore the remaining bindings around that trigger, so a
// release removes only its request route and never clears the login pointer.
func releaseKiloRequestBinding(ctx context.Context, tx *sql.Tx, accountID, proxyID int64) error {
	var remaining []byte
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('proxy_id',proxy_id,'position',position) ORDER BY position),'[]'::jsonb) FROM pk_account_proxy_bindings WHERE account_id=$1 AND proxy_id<>$2`, accountID, proxyID).Scan(&remaining); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET proxy_id=(SELECT proxy_id FROM pk_account_proxy_bindings WHERE account_id=$1 AND proxy_id<>$2 ORDER BY position LIMIT 1),proxy_fallback_origin_id=NULL,extra=extra-'upstream_billing_probe'-'ollama_cloud_usage_session'-'ollama_cloud_usage_snapshot'-'opencode_go_usage_snapshot',updated_at=NOW() WHERE id=$1 AND proxy_id=$2`, accountID, proxyID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pk_account_proxy_bindings(account_id,proxy_id,position) SELECT $1,r.proxy_id,r.position FROM jsonb_to_recordset($2::jsonb) AS r(proxy_id bigint,position integer) ON CONFLICT DO NOTHING`, accountID, string(remaining)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pk_account_proxy_bindings WHERE account_id=$1 AND proxy_id=$2`, accountID, proxyID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pk_account_proxy_fallback_sets WHERE account_id=$1`, accountID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE accounts child SET proxy_id=parent.proxy_id,proxy_fallback_origin_id=NULL,updated_at=NOW() FROM accounts parent WHERE child.parent_account_id=parent.id AND parent.id=$1 AND child.deleted_at IS NULL AND child.proxy_id IS DISTINCT FROM parent.proxy_id`, accountID)
	return err
}

func (r *KiloEgressRepository) Acquire(ctx context.Context, accountID, proxyID int64, proxyURL string) (*service.KiloEgressPermit, error) {
	if accountID <= 0 {
		return nil, nil
	}
	if r == nil || r.db == nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	managed, err := kiloManagedAccount(ctx, r.db, accountID, false)
	if err != nil {
		return nil, service.ErrKiloEgressUnavailable
	}
	if !managed {
		return nil, nil
	}
	if strings.TrimSpace(proxyURL) == "" {
		return nil, service.ErrKiloEgressUnavailable
	}
	if proxyID <= 0 {
		// Administrator/background HTTP callers may lack the gateway routing
		// flag. Admit only their exact verified request binding or pinned login;
		// an empty or unrecognized fallback can never become direct traffic.
		if err = r.db.QueryRowContext(ctx, `SELECT r.proxy_id FROM pk_kilo_egress_routes r JOIN accounts a ON a.id=r.account_id WHERE r.account_id=$1 AND r.config_hash=$2 AND r.state='active' AND (a.extra->'kilo_login_proxy_id'=to_jsonb(r.proxy_id) OR EXISTS(SELECT 1 FROM pk_effective_account_proxy_bindings b WHERE b.account_id=r.account_id AND b.proxy_id=r.proxy_id)) ORDER BY COALESCE(a.extra->'kilo_login_proxy_id'=to_jsonb(r.proxy_id),FALSE) DESC,r.id LIMIT 1`, accountID, kiloProxyConfigHash(proxyURL)).Scan(&proxyID); err != nil {
			return nil, service.ErrKiloEgressUnavailable
		}
	}
	leaseID := uuid.NewString()
	routeID, hash, err := r.reserve(ctx, accountID, proxyID, proxyURL, leaseID)
	if err != nil {
		return nil, err
	}
	permitCtx, cancel := context.WithCancel(ctx)
	permit := service.NewKiloEgressPermit(permitCtx, func() {
		cancel()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), kiloEgressOperationTTL)
		defer closeCancel()
		// Only Body.Close releases this row. A failed delete deliberately keeps
		// ownership and capacity occupied instead of enabling unsafe takeover.
		_, _ = r.db.ExecContext(closeCtx, `DELETE FROM pk_kilo_egress_leases WHERE id=$1::uuid AND route_id=$2 AND account_id=$3`, leaseID, routeID, accountID)
	})
	if ctx.Err() != nil {
		permit.Close()
		return nil, ctx.Err()
	}
	go r.keepLease(permitCtx, cancel, accountID, proxyID, routeID, hash, leaseID)
	return permit, nil
}

func (r *KiloEgressRepository) reserve(ctx context.Context, accountID, proxyID int64, proxyURL, leaseID string) (int64, string, error) {
	opCtx, cancel := context.WithTimeout(ctx, kiloEgressOperationTTL)
	defer cancel()
	tx, err := r.begin(opCtx)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = tx.Rollback() }()
	managed, err := kiloManagedAccount(opCtx, tx, accountID, true)
	if err != nil || !managed {
		return 0, "", service.ErrKiloEgressUnavailable
	}
	proxy, err := readKiloProxy(opCtx, tx, accountID, proxyID, true)
	if err != nil {
		return 0, "", service.ErrKiloEgressUnavailable
	}
	if _, err = checkKiloProxyFingerprint(opCtx, tx, accountID, &proxy.proxy); err != nil {
		return 0, "", err
	}
	route, err := readKiloRoute(opCtx, tx, accountID, proxyID)
	if err != nil {
		return 0, "", service.ErrKiloEgressUnavailable
	}
	// A request may still hold a binding snapshot read before explicit release.
	// Reject it without overwriting the drain reason used by existing keepers.
	if route.State == service.KiloEgressDraining {
		return 0, "", service.ErrKiloEgressDraining
	}
	if !proxy.available() || route.hash != proxy.hash || proxy.hash != kiloProxyConfigHash(proxyURL) {
		if setKiloRouteState(opCtx, tx, route.ID, service.KiloEgressDraining, "proxy_configuration_or_binding_changed") != nil {
			return 0, "", service.ErrKiloEgressUnavailable
		}
		_, err = commitKiloResult(tx, nil, service.ErrKiloEgressDraining)
		return 0, "", err
	}
	if route.State != service.KiloEgressActive || route.VerifiedAt == nil {
		return 0, "", service.ErrKiloEgressUnavailable
	}
	var used, limit int
	if err = tx.QueryRowContext(opCtx, `SELECT (SELECT COUNT(*) FROM pk_kilo_egress_leases l JOIN pk_kilo_egress_routes r ON r.id=l.route_id WHERE COALESCE(r.credential_fingerprint,r.config_hash)=$1 AND r.account_id=$2),MIN(max_concurrency) FROM pk_kilo_egress_routes WHERE COALESCE(credential_fingerprint,config_hash)=$1 AND account_id=$2`, route.fingerprintOrHash(), accountID).Scan(&used, &limit); err != nil {
		return 0, "", service.ErrKiloEgressUnavailable
	}
	// A newly pinned login is limited immediately, before re-verification can
	// update an older route's persisted concurrency value.
	if proxy.limit < limit {
		limit = proxy.limit
	}
	if used >= limit {
		return 0, "", service.ErrKiloEgressCapacity
	}
	if _, err = tx.ExecContext(opCtx, `INSERT INTO pk_kilo_egress_leases(id,account_id,route_id,expires_at) VALUES($1::uuid,$2,$3,NOW()+INTERVAL '45 seconds')`, leaseID, accountID, route.ID); err != nil {
		return 0, "", service.ErrKiloEgressUnavailable
	}
	if tx.Commit() != nil {
		return 0, "", service.ErrKiloEgressUnavailable
	}
	return route.ID, route.hash, nil
}

func (r *KiloEgressRepository) keepLease(ctx context.Context, cancel context.CancelFunc, accountID, proxyID, routeID int64, hash, leaseID string) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	// Cancel before the database TTL even when the final refresh is blocked.
	deadline := time.NewTimer(kiloEgressLeaseTTL - 2*kiloEgressOperationTTL)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			cancel()
			return
		case <-ticker.C:
			opCtx, opCancel := context.WithTimeout(ctx, kiloEgressOperationTTL)
			ok := r.refreshLease(opCtx, accountID, proxyID, routeID, hash, leaseID)
			opCancel()
			if !ok {
				cancel()
				return
			}
			if !deadline.Stop() {
				select {
				case <-deadline.C:
				default:
				}
			}
			deadline.Reset(kiloEgressLeaseTTL - 2*kiloEgressOperationTTL)
		}
	}
}

func (r *KiloEgressRepository) refreshLease(ctx context.Context, accountID, proxyID, routeID int64, hash, leaseID string) bool {
	tx, err := r.begin(ctx)
	if err != nil {
		return false
	}
	defer func() { _ = tx.Rollback() }()
	managed, err := kiloManagedAccount(ctx, tx, accountID, true)
	if err != nil || !managed {
		return false
	}
	proxy, err := readKiloProxy(ctx, tx, accountID, proxyID, true)
	if err != nil {
		return false
	}
	route, err := readKiloRoute(ctx, tx, accountID, proxyID)
	if err != nil || route.ID != routeID {
		return false
	}
	explicitDrain := route.State == service.KiloEgressDraining && route.LastError == "explicit_release"
	available := proxy.available()
	if explicitDrain {
		// Removing a request binding closes admission, not its existing streams.
		available = !proxy.deleted.Valid && proxy.proxy.IsActive() && !proxy.proxy.IsExpired(time.Now())
	}
	if !available || proxy.hash != hash || route.hash != hash {
		if setKiloRouteState(ctx, tx, route.ID, service.KiloEgressDraining, "proxy_configuration_or_binding_changed") == nil {
			_ = tx.Commit()
		}
		return false
	}
	if route.State != service.KiloEgressActive && !explicitDrain {
		return false
	}
	result, err := tx.ExecContext(ctx, `UPDATE pk_kilo_egress_leases SET expires_at=NOW()+INTERVAL '45 seconds' WHERE id=$1::uuid AND account_id=$2 AND route_id=$3 AND expires_at>NOW()`, leaseID, accountID, routeID)
	if err != nil {
		return false
	}
	changed, err := result.RowsAffected()
	return err == nil && changed == 1 && tx.Commit() == nil
}

//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestKiloLoginMigrationSQLSeparatesLegacyBindingsPreservingLeases(t *testing.T) {
	db, client := newBillingAuditDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Rewind only 347's additive objects in this empty, private fixture. The
	// actual embedded SQL below must perform the historical-data transition.
	_, err := db.ExecContext(ctx, `DROP TRIGGER pk_kilo_login_proxy_capacity ON accounts;
DROP FUNCTION pk_kilo_login_proxy_guard();
ALTER TABLE pk_kilo_prepared_accounts DROP COLUMN login_proxy_id,DROP COLUMN routing_options;
ALTER TABLE pk_kilo_egress_routes DROP COLUMN credential_fingerprint;`)
	require.NoError(t, err)
	a := kiloSQLManagedAccount(t, client)
	legacy := kiloSQLManagedAccount(t, client)
	login := kiloSQLCredentialProxy(t, client, "http", "migration.login.invalid", 29101)
	alias := kiloSQLCredentialProxy(t, client, "socks5", "MIGRATION.LOGIN.INVALID", 29101)
	requestA := kiloSQLCredentialProxy(t, client, "http", "migration.request-a.invalid", 29102)
	requestB := kiloSQLCredentialProxy(t, client, "http", "migration.request-b.invalid", 29103)
	legacyLogin := kiloSQLCredentialProxy(t, client, "http", "migration.legacy.invalid", 29104)
	_, err = db.ExecContext(ctx, `UPDATE accounts SET proxy_id=$2 WHERE id=$1`, a.ID, alias.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO pk_account_proxy_bindings(account_id,proxy_id,position) VALUES($1,$2,0),($1,$3,1),($1,$4,2),($1,$5,3)`, a.ID, alias.ID, login.ID, requestA.ID, requestB.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE accounts SET proxy_id=$2 WHERE id=$1`, legacy.ID, legacyLogin.ID)
	require.NoError(t, err)
	var preparedID, legacyPreparedID int64
	for _, item := range []struct {
		account *service.Account
		id      *int64
	}{{a, &preparedID}, {legacy, &legacyPreparedID}} {
		require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO pk_kilo_prepared_accounts(name,credential_encrypted,credential_fingerprint,auth_method,machine_id,status,account_id) VALUES($1,'synthetic',repeat(md5($1),2),'device',$2::uuid,'bound',$3) RETURNING id`, item.account.Name, uuid.NewString(), item.account.ID).Scan(item.id))
	}
	// The saved device's login takes precedence over the first ordinary alias.
	_, err = db.ExecContext(ctx, `INSERT INTO pk_kilo_device_sessions(id,name,proxy_id,account_id,code_encrypted,verification_url,user_code,status,prepared_id,expires_at) VALUES($1::uuid,'saved',$2,$3,'synthetic','https://device.invalid','synthetic','saved',$4,NOW()+INTERVAL '1 hour')`, uuid.NewString(), login.ID, a.ID, preparedID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO pk_account_proxy_fallback_sets(account_id,proxy_ids) VALUES($1,ARRAY[$2,$3]::bigint[])`, a.ID, alias.ID, requestA.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO pk_kilo_egress_owners(egress_ip,account_id) VALUES('1.1.1.1',$1)`, a.ID)
	require.NoError(t, err)
	var routeID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO pk_kilo_egress_routes(account_id,proxy_id,egress_ip,config_hash,verified_at,state,max_concurrency) VALUES($1,$2,'1.1.1.1',$3,NOW(),'active',1) RETURNING id`, a.ID, login.ID, kiloProxyConfigHash(login.URL())).Scan(&routeID))
	leaseID := uuid.NewString()
	_, err = db.ExecContext(ctx, `INSERT INTO pk_kilo_egress_leases(id,account_id,route_id,expires_at) VALUES($1::uuid,$2,$3,NOW()+INTERVAL '45 seconds')`, leaseID, a.ID, routeID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO pk_kilo_proxy_owners(credential_fingerprint,account_id) SELECT pk_kilo_proxy_credential_fingerprint(host,port,username,password),$1 FROM proxies WHERE id=$2`, a.ID, login.ID)
	require.NoError(t, err)
	migration, err := dbmigrations.FS.ReadFile("347_kilo_login_request_egress.sql")
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	var primary, selectedLogin, extraLogin int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT a.proxy_id,p.login_proxy_id,(a.extra->>'kilo_login_proxy_id')::bigint FROM accounts a JOIN pk_kilo_prepared_accounts p ON p.account_id=a.id WHERE a.id=$1`, a.ID).Scan(&primary, &selectedLogin, &extraLogin))
	require.Equal(t, requestA.ID, primary)
	require.Equal(t, login.ID, selectedLogin)
	require.Equal(t, login.ID, extraLogin)
	bindings, err := NewAccountRepository(client, db, nil).(service.AccountProxyBindingRepository).GetAccountProxyBindings(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, bindings, 2)
	require.Equal(t, requestA.ID, bindings[0].ProxyID)
	require.Equal(t, requestB.ID, bindings[1].ProxyID)
	var fallback, legacyBindings, routes, leases int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM pk_account_proxy_fallback_sets WHERE account_id=$1),(SELECT COUNT(*) FROM pk_effective_account_proxy_bindings WHERE account_id=$2),(SELECT COUNT(*) FROM pk_kilo_egress_routes WHERE id=$3),(SELECT COUNT(*) FROM pk_kilo_egress_leases WHERE id=$4::uuid)`, a.ID, legacy.ID, routeID, leaseID).Scan(&fallback, &legacyBindings, &routes, &leases))
	require.Zero(t, fallback)
	require.Zero(t, legacyBindings, "a login-only legacy primary cannot leak back through the effective-binding fallback")
	require.Equal(t, 1, routes)
	require.Equal(t, 1, leases)
	var legacyFixed bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT a.proxy_id IS NULL AND p.login_proxy_id=$2 AND a.extra->'kilo_login_proxy_id'=to_jsonb($2::bigint) FROM accounts a JOIN pk_kilo_prepared_accounts p ON p.account_id=a.id WHERE a.id=$1`, legacy.ID, legacyLogin.ID).Scan(&legacyFixed))
	require.True(t, legacyFixed)
	egress := NewKiloEgressGuard(db)
	require.True(t, egress.refreshLease(ctx, a.ID, login.ID, routeID, kiloProxyConfigHash(login.URL()), leaseID), "removing ordinary login bindings must not stop the existing response keeper")
	owner, count := kiloSQLCredentialOwner(t, db, login.ID)
	require.Equal(t, a.ID, owner)
	require.Equal(t, 1, count)
}

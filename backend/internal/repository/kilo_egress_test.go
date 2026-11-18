package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func kiloTestRouteRows(hash string) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{"id", "account_id", "proxy_id", "egress_ip", "state", "max_concurrency", "in_flight", "verified_at", "last_error", "created_at", "updated_at", "purpose", "config_hash", "credential_fingerprint"}).AddRow(31, 7, 11, "8.8.8.8", "active", 10, 1, now, "", now, now, "request", hash, "synthetic-fingerprint")
}

func kiloTestBegin(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).WithArgs(accountProxyAssignmentLock).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).WithArgs(kiloEgressLock).WillReturnResult(sqlmock.NewResult(0, 1))
}

func kiloTestManagedProxy(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT COALESCE.*kilo_managed.*FOR SHARE").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"managed", "deleted_at"}).AddRow(true, nil))
	mock.ExpectQuery("(?s)SELECT p.protocol.*FOR SHARE OF p").WithArgs(int64(7), int64(11)).WillReturnRows(sqlmock.NewRows([]string{"protocol", "host", "port", "username", "password", "status", "expires_at", "deleted_at", "max_concurrency", "bound", "login"}).AddRow("http", "proxy.invalid", 8000, "", "", "active", nil, nil, 10, true, false))
}

func TestKiloEgressConfigChangeDrainsWithoutTransferringOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := NewKiloEgressGuard(db)
	kiloTestBegin(mock)
	kiloTestManagedProxy(mock)
	mock.ExpectQuery("SELECT pk_kilo_proxy_credential_fingerprint").WithArgs("proxy.invalid", 8000, "", "").WillReturnRows(sqlmock.NewRows([]string{"fingerprint"}).AddRow("synthetic-fingerprint"))
	mock.ExpectQuery("SELECT account_id FROM .*pk_kilo_proxy_owners").WithArgs("synthetic-fingerprint").WillReturnRows(sqlmock.NewRows([]string{"account_id"}))
	mock.ExpectExec("INSERT INTO pk_kilo_egress_routes").WithArgs(int64(7), int64(11), kiloProxyConfigHash("http://proxy.invalid:8000"), 10, "synthetic-fingerprint").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT r.id.*FOR UPDATE OF r").WithArgs(int64(7), int64(11)).WillReturnRows(kiloTestRouteRows(kiloProxyConfigHash("http://old.invalid:8000")))
	mock.ExpectExec("UPDATE pk_kilo_egress_routes SET state").WithArgs(int64(31), "draining", "proxy_configuration_changed").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	_, _, err = r.prepareVerification(context.Background(), 7, 11)
	require.ErrorIs(t, err, service.ErrKiloEgressDraining)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloEgressStaleAdmissionPreservesExplicitDrain(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := NewKiloEgressGuard(db)
	hash := kiloProxyConfigHash("http://proxy.invalid:8000")
	kiloTestBegin(mock)
	mock.ExpectQuery("SELECT COALESCE.*kilo_managed.*FOR SHARE").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"managed", "deleted_at"}).AddRow(true, nil))
	mock.ExpectQuery("(?s)SELECT p.protocol.*FOR SHARE OF p").WithArgs(int64(7), int64(11)).WillReturnRows(sqlmock.NewRows([]string{"protocol", "host", "port", "username", "password", "status", "expires_at", "deleted_at", "max_concurrency", "bound", "login"}).AddRow("http", "proxy.invalid", 8000, "", "", "active", nil, nil, 10, false, false))
	mock.ExpectQuery("SELECT pk_kilo_proxy_credential_fingerprint").WithArgs("proxy.invalid", 8000, "", "").WillReturnRows(sqlmock.NewRows([]string{"fingerprint"}).AddRow("synthetic-fingerprint"))
	mock.ExpectQuery("SELECT account_id FROM .*pk_kilo_proxy_owners").WithArgs("synthetic-fingerprint").WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(7))
	now := time.Now()
	mock.ExpectQuery("SELECT r.id.*FOR UPDATE OF r").WithArgs(int64(7), int64(11)).WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "proxy_id", "egress_ip", "state", "max_concurrency", "in_flight", "verified_at", "last_error", "created_at", "updated_at", "purpose", "config_hash", "credential_fingerprint"}).AddRow(31, 7, 11, "8.8.8.8", "draining", 10, 1, now, "explicit_release", now, now, "request", hash, "synthetic-fingerprint"))
	mock.ExpectRollback()
	_, _, err = r.reserve(context.Background(), 7, 11, "http://proxy.invalid:8000", "00000000-0000-0000-0000-000000000008")
	require.ErrorIs(t, err, service.ErrKiloEgressDraining)
	require.NoError(t, mock.ExpectationsWereMet(), "stale admission performs no drain-state rewrite")
}

func TestKiloEgressDriftRetainsOldOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := NewKiloEgressGuard(db)
	hash := kiloProxyConfigHash("http://proxy.invalid:8000")
	kiloTestBegin(mock)
	kiloTestManagedProxy(mock)
	mock.ExpectQuery("SELECT r.id.*FOR UPDATE OF r").WithArgs(int64(7), int64(11)).WillReturnRows(kiloTestRouteRows(hash))
	mock.ExpectExec("UPDATE pk_kilo_egress_routes SET state").WithArgs(int64(31), "draining", "proxy_configuration_or_egress_changed").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	_, err = r.finishVerification(context.Background(), &kiloRouteRow{KiloEgressRoute: service.KiloEgressRoute{ID: 31, AccountID: 7, ProxyID: 11}, hash: kiloProxyConfigHash("http://old.invalid:8000")}, "", nil)
	require.ErrorIs(t, err, service.ErrKiloEgressDraining)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloEgressReleaseRequiresBodyCloseEvenAfterExpiry(t *testing.T) {
	for _, pending := range []int{1, 0} {
		t.Run(map[int]string{1: "retains_outstanding_lease", 0: "explicit_release"}[pending], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			r := NewKiloEgressGuard(db)
			kiloTestBegin(mock)
			mock.ExpectQuery("SELECT account_id,proxy_id,COALESCE.*FOR UPDATE").WithArgs(int64(31)).WillReturnRows(sqlmock.NewRows([]string{"account_id", "proxy_id", "egress_ip"}).AddRow(7, 11, "8.8.8.8"))
			mock.ExpectQuery("SELECT COALESCE.*jsonb_agg").WithArgs(int64(7), int64(11)).WillReturnRows(sqlmock.NewRows([]string{"remaining"}).AddRow([]byte(`[]`)))
			mock.ExpectExec("UPDATE accounts SET proxy_id").WithArgs(int64(7), int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO pk_account_proxy_bindings").WithArgs(int64(7), "[]").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("DELETE FROM pk_account_proxy_bindings").WithArgs(int64(7), int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("DELETE FROM pk_account_proxy_fallback_sets").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE accounts child").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("UPDATE pk_kilo_egress_routes SET state").WithArgs(int64(31), "draining", "explicit_release").WillReturnResult(sqlmock.NewResult(0, 1))
			// Every lease counts, including expired rows on a disconnected node.
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM pk_kilo_egress_leases WHERE route_id=$1`)).WithArgs(int64(31)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(pending))
			if pending == 0 {
				mock.ExpectExec("DELETE FROM pk_kilo_egress_routes").WithArgs(int64(31)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("DELETE FROM pk_kilo_proxy_owners.*NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectCommit()
			err = r.Release(context.Background(), 31)
			if pending > 0 {
				require.ErrorIs(t, err, service.ErrKiloEgressDraining)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiloEgressOptInAndDirectFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		managed bool
		deleted any
		wantErr bool
	}{
		{"existing_account", false, nil, false},
		{"managed_direct", true, nil, true},
		{"managed_deleted", true, time.Now(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectQuery("SELECT COALESCE.*kilo_managed").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"managed", "deleted_at"}).AddRow(tc.managed, tc.deleted))
			permit, err := NewKiloEgressGuard(db).Acquire(context.Background(), 7, 0, "")
			require.Nil(t, permit)
			if tc.wantErr {
				require.ErrorIs(t, err, service.ErrKiloEgressUnavailable)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiloEgressListKeepsExpiredLeaseVisible(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now()
	mock.ExpectQuery("SELECT COALESCE.*kilo_managed").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"managed", "deleted_at"}).AddRow(true, nil))
	mock.ExpectQuery("SELECT r.id.*FROM pk_kilo_egress_routes").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "proxy_id", "egress_ip", "state", "max_concurrency", "in_flight", "verified_at", "last_error", "created_at", "updated_at", "purpose"}).AddRow(31, 7, 11, "8.8.8.8", "draining", 10, 2, now, "explicit_release", now, now, "request"))
	mock.ExpectQuery("SELECT id::text,route_id,expires_at,created_at,expires_at<=NOW").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "route_id", "expires_at", "created_at", "expired"}).
		AddRow("00000000-0000-0000-0000-000000000007", 31, now.Add(-time.Minute), now.Add(-2*time.Minute), true).
		AddRow("00000000-0000-0000-0000-000000000008", 31, now.Add(time.Minute), now, false))
	routes, err := NewKiloEgressGuard(db).List(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, 2, routes[0].InFlight)
	require.Equal(t, 1, routes[0].ExpiredLeases)
	require.Len(t, routes[0].Leases, 2)
	require.True(t, routes[0].Leases[0].Expired)
	require.Equal(t, "00000000-0000-0000-0000-000000000007", routes[0].Leases[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloEgressLostLeaseNeverResurrects(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := NewKiloEgressGuard(db)
	hash := kiloProxyConfigHash("http://proxy.invalid:8000")
	kiloTestBegin(mock)
	kiloTestManagedProxy(mock)
	mock.ExpectQuery("SELECT r.id.*FOR UPDATE OF r").WithArgs(int64(7), int64(11)).WillReturnRows(kiloTestRouteRows(hash))
	mock.ExpectExec("UPDATE pk_kilo_egress_leases.*expires_at>NOW").WithArgs("00000000-0000-0000-0000-000000000007", int64(7), int64(31)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	require.False(t, r.refreshLease(context.Background(), 7, 11, 31, hash, "00000000-0000-0000-0000-000000000007"))
	require.NoError(t, mock.ExpectationsWereMet())
}

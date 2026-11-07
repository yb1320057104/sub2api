package repository

import (
	"context"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPK51ProxyGroupAssignmentRetainsCapacityAndRejectsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		active  bool
		limit   int
		wantErr bool
	}{{"existing capacity", true, 3, false}, {"unlimited default", true, 0, false}, {"inactive member", false, 4, true}} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectQuery(`SELECT EXISTS`).WithArgs(int64(9)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
			mock.ExpectQuery(`(?s)SELECT p.id.*FOR UPDATE OF p`).WithArgs(int64(9)).WillReturnRows(sqlmock.NewRows([]string{"id", "active", "max_accounts", "max_concurrency"}).AddRow(2, tc.active, tc.limit, 7))
			items, err := proxyGroupAssignmentItems(context.Background(), db, 9, 10)
			if tc.wantErr {
				require.ErrorContains(t, err, "#2")
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.limit, items[0].MaxAccounts)
				require.Equal(t, 10, items[0].MaxConcurrency)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPK51ProxyGroupStrictMemberMutationRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT EXISTS`).WithArgs(int64(9)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`(?s)SELECT p.id,m.proxy_group_id.*FOR UPDATE OF p`).WillReturnRows(sqlmock.NewRows([]string{"id", "group"}).AddRow(2, 3))
	mock.ExpectQuery(`(?s)SELECT EXISTS.*p.strict_priority`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()
	group := int64(9)
	_, err = newProxyRepositoryWithSQL(client, db).SetProxyGroupMembers(context.Background(), service.ProxyGroupMembersInput{ProxyIDs: []int64{2}, ProxyGroupID: &group})
	require.ErrorContains(t, err, "先关闭")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPK51ProxyGroupPolicyReadFailsClosedAndLoadsProxyIDs(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "load", true: "database failure"}[fail], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			query := mock.ExpectQuery(`(?s)SELECT COALESCE.*FROM groups`).WithArgs(int64(7))
			if fail {
				query.WillReturnError(errors.New("offline"))
			} else {
				query.WillReturnRows(sqlmock.NewRows([]string{"strict", "group", "ids"}).AddRow(true, 9, "{2,3}"))
			}
			policy, err := readGroupProxyPriorityPolicy(context.Background(), db, 7)
			if fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.True(t, policy.StrictPriority)
				require.Equal(t, []int64{2, 3}, policy.ProxyIDs)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPK51ProxyGroupShadowOutsideSelectionRejected(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery(`(?s)SELECT a.id,a.parent_account_id.*FOR SHARE OF ag,a`).WillReturnRows(sqlmock.NewRows([]string{"id", "parent", "selected"}).AddRow(3, 2, false))
	_, err = proxyPolicyAccountRoots(context.Background(), db, []int64{1})
	require.ErrorContains(t, err, "母账号不在所选")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPK53ProxyGroupCandidatePersistenceFailureRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`(?s)SELECT p.id.*FOR UPDATE OF p`).WillReturnRows(sqlmock.NewRows([]string{"id", "active", "max_accounts", "max_concurrency"}).AddRow(5, true, 1, 7))
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)SELECT id,parent_account_id.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"id", "parent"}).AddRow(2, nil).AddRow(3, nil))
	mock.ExpectExec(`DELETE FROM pk_account_proxy_fallback_sets`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM pk_account_proxy_bindings`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE accounts SET proxy_id=NULL`).WillReturnResult(sqlmock.NewResult(0, 2))
	// The old limit is replaced before the legacy account trigger sees a new
	// primary. A later write failure must roll back the entire local transaction.
	mock.ExpectExec(`INSERT INTO pk_proxy_limits`).WithArgs(int64(5), 10, 0).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE accounts SET proxy_id=\$2`).WithArgs(int64(2), int64(5)).WillReturnError(errors.New("injected primary write failure"))
	mock.ExpectRollback()
	_, err = newAccountRepositoryWithSQL(client, db, nil).AssignAccountProxyGroup(context.Background(), service.AccountProxyGroupAssignmentInput{AccountIDs: []int64{2, 3}, ProxyGroupID: 9, ProxyConcurrency: 10})
	require.ErrorContains(t, err, "injected primary write failure")
	require.NoError(t, mock.ExpectationsWereMet())
}

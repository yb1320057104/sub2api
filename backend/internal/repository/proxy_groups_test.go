package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
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

func TestScopedProxyGroupLockedAccountAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		group                  int64
		shadow, child, allowed bool
	}{
		{"authorized", 1, false, false, true},
		{"foreign group", 9, false, false, false},
		{"shadow target", 1, true, false, false},
		{"inherited child", 1, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			principal := &adminaccess.Principal{KeyID: "key-1", Policy: adminaccess.Policy{GlobalPermissions: []string{"proxy-groups.assign"}, Rules: []adminaccess.Rule{{Permissions: []string{"accounts.read", "accounts.network", "accounts.schedule"}, GroupIDs: []int64{1}, AccountTypes: []string{"oauth"}, Platforms: []string{"openai"}}}}}
			var parent any
			if tc.shadow {
				parent = int64(3)
			}
			mock.ExpectQuery(`(?s)SELECT id,type,platform,parent_account_id.*ORDER BY id FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"id", "type", "platform", "parent"}).AddRow(2, "oauth", "openai", parent))
			mock.ExpectQuery(`(?s)SELECT account_id,group_id.*ORDER BY account_id,group_id FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id"}).AddRow(2, tc.group))
			if tc.group == 1 {
				mock.ExpectQuery(`(?s)SELECT deleted_at IS NULL.*parent_account_id.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"active"}).AddRow(tc.child))
			}
			err = authorizeScopedProxyGroupAccounts(adminaccess.WithPrincipal(context.Background(), principal), db, []int64{2}, true)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestScopedProxyGroupUnauthorizedBatchRollsBackBeforeWrites(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	p := &adminaccess.Principal{KeyID: "key-1", Policy: adminaccess.Policy{GlobalPermissions: []string{"proxy-groups.assign"}, Rules: []adminaccess.Rule{{Permissions: []string{"accounts.read", "accounts.network"}, GroupIDs: []int64{1}, AccountTypes: []string{"oauth"}}}}}
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`(?s)SELECT p.id.*FOR UPDATE OF p`).WillReturnRows(sqlmock.NewRows([]string{"id", "active", "max_accounts", "max_concurrency"}).AddRow(5, true, 1, 7))
	mock.ExpectQuery(`(?s)SELECT id,type,platform,parent_account_id.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"id", "type", "platform", "parent"}).AddRow(2, "oauth", "openai", nil).AddRow(3, "oauth", "openai", nil))
	mock.ExpectQuery(`(?s)SELECT account_id,group_id.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id"}).AddRow(2, 1).AddRow(3, 9))
	mock.ExpectRollback()
	_, err = newAccountRepositoryWithSQL(client, db, nil).AssignAccountProxyGroup(adminaccess.WithPrincipal(context.Background(), p), service.AccountProxyGroupAssignmentInput{AccountIDs: []int64{2, 3}, ProxyGroupID: 9, ProxyConcurrency: 10})
	require.ErrorIs(t, err, service.ErrAccountNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScopedProxyGroupExpectedMembers(t *testing.T) {
	items := []service.AccountProxyBindingInput{{ProxyID: 2}, {ProxyID: 3}}
	require.NoError(t, validateExpectedProxyGroup(service.AccountProxyGroupAssignmentInput{ExpectedProxyIDs: []int64{3, 2}}, items))
	for _, ids := range [][]int64{nil, {}, {2, 2}, {-1}, {2}, {2, 4}} {
		require.Error(t, validateExpectedProxyGroup(service.AccountProxyGroupAssignmentInput{ExpectedProxyIDs: ids}, items))
	}
}

func TestScopedProxyGroupPostgresDurableOperation(t *testing.T) {
	dsn := os.Getenv("SCOPED_ADMIN_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated SCOPED_ADMIN_TEST_DSN")
	}
	base, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("scoped_proxy_%d", time.Now().UnixNano())
	_, err = base.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn+" search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(); _, _ = base.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = base.Close() })
	_, err = db.Exec(`
CREATE TABLE accounts(id BIGINT PRIMARY KEY,type TEXT DEFAULT 'oauth',platform TEXT DEFAULT 'openai',parent_account_id BIGINT REFERENCES accounts(id),deleted_at TIMESTAMPTZ,proxy_id BIGINT,proxy_fallback_origin_id BIGINT,extra JSONB DEFAULT '{}',concurrency INT DEFAULT 3,updated_at TIMESTAMPTZ DEFAULT NOW());
CREATE TABLE account_groups(account_id BIGINT REFERENCES accounts(id),group_id BIGINT,PRIMARY KEY(account_id,group_id));
CREATE TABLE proxies(id BIGINT PRIMARY KEY,status TEXT DEFAULT 'active',expires_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
CREATE TABLE pk_proxy_groups(id BIGINT PRIMARY KEY);
CREATE TABLE pk_proxy_group_members(proxy_group_id BIGINT REFERENCES pk_proxy_groups(id),proxy_id BIGINT REFERENCES proxies(id),PRIMARY KEY(proxy_id));
CREATE TABLE pk_proxy_limits(proxy_id BIGINT PRIMARY KEY,max_concurrency INT,max_accounts INT,updated_at TIMESTAMPTZ DEFAULT NOW());
CREATE TABLE pk_account_proxy_fallback_sets(account_id BIGINT);
CREATE TABLE pk_account_proxy_bindings(account_id BIGINT,proxy_id BIGINT,position INT,PRIMARY KEY(account_id,proxy_id));
CREATE VIEW pk_effective_account_proxy_bindings AS SELECT account_id,proxy_id,position FROM pk_account_proxy_bindings;
CREATE TABLE scheduler_outbox(id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT);
CREATE TABLE idempotency_records(scope VARCHAR(128),idempotency_key_hash VARCHAR(64),request_fingerprint VARCHAR(64),status TEXT,response_status INT,response_body TEXT,expires_at TIMESTAMPTZ,PRIMARY KEY(scope,idempotency_key_hash));
INSERT INTO accounts(id) VALUES(1),(2); INSERT INTO proxies(id) VALUES(10),(11); INSERT INTO pk_proxy_groups(id) VALUES(4); INSERT INTO pk_proxy_group_members VALUES(4,10),(4,11);`)
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	p := &adminaccess.Principal{KeyID: "db-key", Policy: adminaccess.Policy{GlobalPermissions: []string{"proxy-groups.assign"}, Rules: []adminaccess.Rule{{Permissions: []string{"accounts.read", "accounts.network", "accounts.schedule"}, IncludeUngrouped: true, AccountTypes: []string{"oauth"}, Platforms: []string{"openai"}}}}}
	ctx := adminaccess.WithPrincipal(context.Background(), p)
	concurrency := 100
	input := service.AccountProxyGroupAssignmentInput{AccountIDs: []int64{1}, ProxyGroupID: 4, ProxyConcurrency: 10, AccountConcurrency: &concurrency, ExpectedProxyIDs: []int64{11, 10}}
	// Receipt failure rolls back proxy limits, account concurrency and bindings.
	_, err = db.Exec(`ALTER TABLE idempotency_records ADD CONSTRAINT injected_receipt_failure CHECK (false)`)
	require.NoError(t, err)
	_, err = repo.AssignScopedAccountProxyGroup(ctx, input, "operation")
	require.Error(t, err)
	var count, value int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pk_account_proxy_bindings`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pk_proxy_limits`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT concurrency FROM accounts WHERE id=1`).Scan(&value))
	require.Equal(t, 3, value)
	_, err = db.Exec(`ALTER TABLE idempotency_records DROP CONSTRAINT injected_receipt_failure`)
	require.NoError(t, err)
	type answer struct {
		result *service.ScopedProxyGroupAssignmentResult
		err    error
	}
	answers := make(chan answer, 8)
	for i := 0; i < 8; i++ {
		go func() {
			result, e := repo.AssignScopedAccountProxyGroup(ctx, input, "operation")
			answers <- answer{result, e}
		}()
	}
	fresh := 0
	for i := 0; i < 8; i++ {
		a := <-answers
		require.NoError(t, a.err)
		if !a.result.Replayed {
			fresh++
		}
	}
	require.Equal(t, 1, fresh)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM idempotency_records WHERE expires_at='infinity'::timestamptz`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pk_account_proxy_bindings`).Scan(&count))
	require.Equal(t, 2, count)
	_, err = db.Exec(`UPDATE pk_proxy_limits SET max_concurrency=7 WHERE proxy_id=10`)
	require.NoError(t, err)
	replayed, err := repo.AssignScopedAccountProxyGroup(ctx, input, "operation")
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.NoError(t, db.QueryRow(`SELECT max_concurrency FROM pk_proxy_limits WHERE proxy_id=10`).Scan(&value))
	require.Equal(t, 7, value)
	narrow := *p
	narrow.Policy.Rules = []adminaccess.Rule{{Permissions: []string{"accounts.read"}, IncludeUngrouped: true, AccountTypes: []string{"oauth"}}}
	_, err = repo.AssignScopedAccountProxyGroup(adminaccess.WithPrincipal(context.Background(), &narrow), input, "operation")
	require.ErrorIs(t, err, service.ErrAccountNotFound)
	other := *p
	other.KeyID = "db-key-other"
	result, err := repo.AssignScopedAccountProxyGroup(adminaccess.WithPrincipal(context.Background(), &other), input, "operation")
	require.NoError(t, err)
	require.False(t, result.Replayed)
	changed := input
	changed.ProxyConcurrency = 11
	_, err = repo.AssignScopedAccountProxyGroup(ctx, changed, "operation")
	require.ErrorIs(t, err, service.ErrIdempotencyKeyConflict)
	// Queue a request behind an actual group writer; after it commits, stale
	// expected members must fail before any account or global limit mutation.
	memberTx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, err = memberTx.Exec(`SELECT pg_advisory_xact_lock($1)`, accountProxyAssignmentLock)
	require.NoError(t, err)
	_, err = memberTx.Exec(`DELETE FROM pk_proxy_group_members WHERE proxy_id=11`)
	require.NoError(t, err)
	go func() {
		result, e := repo.AssignScopedAccountProxyGroup(ctx, input, "operation")
		answers <- answer{result, e}
	}()
	require.NoError(t, memberTx.Commit())
	a := <-answers
	require.ErrorContains(t, a.err, "成员已变化")
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pk_account_proxy_bindings`).Scan(&count))
	require.Equal(t, 2, count)
}

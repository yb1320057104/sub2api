package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"modernc.org/sqlite"
)

// SQLite executes the production relationship and current-token predicates.
// Only PostgreSQL syntax and its UTF-8/SHA-256 functions need adaptation.
var kiloViewSQLiteHashFunctions sync.Once

func registerKiloViewSQLiteHashFunctions() {
	kiloViewSQLiteHashFunctions.Do(func() {
		sqlite.MustRegisterDeterministicScalarFunction("convert_to", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if args[0] == nil {
				return nil, nil
			}
			return []byte(args[0].(string)), nil
		})
		sqlite.MustRegisterDeterministicScalarFunction("sha256", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if args[0] == nil {
				return nil, nil
			}
			sum := sha256.Sum256(args[0].([]byte))
			return sum[:], nil
		})
		sqlite.MustRegisterDeterministicScalarFunction("encode", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if args[0] == nil {
				return nil, nil
			}
			return hex.EncodeToString(args[0].([]byte)), nil
		})
	})
}
func kiloViewSQLiteQuery(query string) string {
	for _, column := range []string{"p.id", "p.account_id"} {
		query = strings.ReplaceAll(query, column+"::text", "CAST("+column+" AS TEXT)")
	}
	query = strings.ReplaceAll(query, "g.extra->>'kilo_managed'='true'", "g.extra->>'kilo_managed' IN ('true',1)")
	query = strings.ReplaceAll(query, "strpos(", "instr(")
	return query
}

type kiloViewSQLiteDriver struct{ dialect.Driver }

func (d kiloViewSQLiteDriver) Query(ctx context.Context, query string, args, value any) error {
	if strings.Contains(query, "=ANY(?)") {
		query = strings.ReplaceAll(query, "=ANY(?)", " IN (SELECT value FROM json_each(?))")
		values := args.([]any)
		for i, value := range values {
			if array, ok := value.(driver.Valuer); ok {
				raw, err := array.Value()
				if err != nil {
					return err
				}
				var ids pq.Int64Array
				if err := ids.Scan(raw); err != nil {
					return err
				}
				encoded, err := json.Marshal(ids)
				if err != nil {
					return err
				}
				values[i] = string(encoded)
			}
		}
	}
	return d.Driver.Query(ctx, kiloViewSQLiteQuery(query), args, value)
}

type kiloViewSQLiteSQL struct {
	*sql.DB
	queries int
}

func (d *kiloViewSQLiteSQL) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	d.queries++
	query = kiloViewSQLiteQuery(query)
	if strings.Contains(query, "=ANY($1)") {
		value, err := args[0].(driver.Valuer).Value()
		if err != nil {
			return nil, err
		}
		var ids pq.Int64Array
		if err := ids.Scan(value); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(ids)
		if err != nil {
			return nil, err
		}
		args[0] = string(raw)
		query = strings.ReplaceAll(query, "=ANY($1)", " IN (SELECT value FROM json_each($1))")
	}
	return d.DB.QueryContext(ctx, query, args...)
}

func newKiloViewFixture(t *testing.T) (*accountRepository, *sql.DB, *kiloViewSQLiteSQL) {
	t.Helper()
	registerKiloViewSQLiteHashFunctions()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE accounts(id INTEGER PRIMARY KEY,name TEXT NOT NULL,platform TEXT NOT NULL,type TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'active',parent_account_id INTEGER,deleted_at TEXT,extra TEXT NOT NULL DEFAULT '{}',credentials TEXT NOT NULL DEFAULT '{}')`,
		`CREATE TABLE pk_kilo_prepared_accounts(id INTEGER PRIMARY KEY,account_id INTEGER,source_account_id INTEGER,status TEXT NOT NULL,name TEXT NOT NULL DEFAULT '',email TEXT NOT NULL DEFAULT '',organization_id TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE account_groups(account_id INTEGER,group_id INTEGER)`,
		`INSERT INTO accounts(id,name,platform,type,extra,credentials) VALUES(122,'GPT source','openai','oauth','{"kilo_source_gateway_id":124,"kilo_source_prepared_id":2}','{"email":"owner@example.test","plan_type":"pro","chatgpt_account_id":"workspace-1","access_token":"synthetic-source-token","refresh_token":"synthetic-refresh"}'),(124,'z-key','openai','apikey','{"kilo_managed":true,"kilo_prepared_id":2}','{"api_key":"synthetic-gateway-secret","kilo_organization_id":"org-1"}')`,
		`INSERT INTO pk_kilo_prepared_accounts(id,account_id,source_account_id,status,name,email,organization_id) VALUES(2,124,122,'bound','prepared-key','key@example.test','org-1')`,
	} {
		_, err := db.Exec(statement)
		require.NoError(t, err)
	}
	drv := kiloViewSQLiteDriver{entsql.OpenDB(dialect.SQLite, db)}
	client := dbent.NewClient(dbent.Driver(drv))
	native := &kiloViewSQLiteSQL{DB: db}
	return &accountRepository{client: client, sql: native}, db, native
}

func TestKiloAccountViewRejectsBrokenOrOneWayPairs(t *testing.T) {
	for _, test := range []struct {
		name       string
		mutation   string
		paired     bool
		sourceLive bool
	}{
		{"reciprocal bound", "", true, true},
		{"reciprocal disabled", "UPDATE pk_kilo_prepared_accounts SET status='disabled'", true, true},
		{"not bound", "UPDATE pk_kilo_prepared_accounts SET status='prepared'", false, true},
		{"gateway marker missing", "UPDATE accounts SET extra='{}' WHERE id=124", false, true},
		{"gateway prepared mismatch", "UPDATE accounts SET extra='{" + `"kilo_managed":true,"kilo_prepared_id":9` + "}' WHERE id=124", false, true},
		{"source gateway mismatch", "UPDATE accounts SET extra='{" + `"kilo_source_gateway_id":999,"kilo_source_prepared_id":2` + "}' WHERE id=122", false, true},
		{"source prepared mismatch", "UPDATE accounts SET extra='{" + `"kilo_source_gateway_id":124,"kilo_source_prepared_id":9` + "}' WHERE id=122", false, true},
		{"same email without markers", "UPDATE accounts SET extra='{}' WHERE id=122", false, true},
		{"prepared source mismatch", "UPDATE pk_kilo_prepared_accounts SET source_account_id=999", false, true},
		{"source is shadow", "UPDATE accounts SET parent_account_id=999 WHERE id=122", false, true},
		{"gateway deleted", "UPDATE accounts SET deleted_at='2026-11-15' WHERE id=124", false, true},
		{"source deleted", "UPDATE accounts SET deleted_at='2026-11-15' WHERE id=122", false, false},
		{"gateway not apikey", "UPDATE accounts SET type='oauth' WHERE id=124", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, db, _ := newKiloViewFixture(t)
			if test.mutation != "" {
				_, err := db.Exec(test.mutation)
				require.NoError(t, err)
			}
			ctx := service.WithKiloAccountList(context.Background())
			ids, err := repo.kiloAccountListFilteredQuery(ctx, "", "", "", "", 0, "").IDs(ctx)
			require.NoError(t, err)
			if test.paired || !test.sourceLive {
				require.NotContains(t, ids, int64(122))
			} else {
				require.Contains(t, ids, int64(122), "only a complete reciprocal pair can hide a live source")
			}
			views, err := repo.GetKiloAccountViews(ctx, []int64{124})
			require.NoError(t, err)
			require.Equal(t, test.paired, views[124].Source != nil)
		})
	}
}

func TestKiloAccountViewCurrentTokenQuarantineIsDisplayOnly(t *testing.T) {
	for _, test := range []struct {
		name, token, want string
	}{
		{"current bearer rejected", "synthetic-source-token", service.StatusError},
		{"old bearer rejection", "synthetic-retired-token", service.StatusActive},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, db, _ := newKiloViewFixture(t)
			state := map[string]any{"kilo_source_gateway_id": 124, "kilo_source_prepared_id": 2,
				service.OpenAIAuthRecoveryKey: map[string]any{"access_token_digest": service.OpenAIAccessTokenDigest(test.token), "requires_relogin": true}}
			encoded, err := json.Marshal(state)
			require.NoError(t, err)
			_, err = db.Exec("UPDATE accounts SET extra=$1 WHERE id=122", string(encoded))
			require.NoError(t, err)
			views, err := repo.GetKiloAccountViews(context.Background(), []int64{124})
			require.NoError(t, err)
			require.NotNil(t, views[124].Source)
			require.Equal(t, test.want, views[124].Source.Status)
			var rawStatus string
			require.NoError(t, db.QueryRow("SELECT status FROM accounts WHERE id=122").Scan(&rawStatus))
			require.Equal(t, service.StatusActive, rawStatus, "display projection cannot mutate the source lifecycle")
		})
	}
}

func TestKiloAccountViewLogicalCountPaginationSearchAndSafeLite(t *testing.T) {
	repo, db, native := newKiloViewFixture(t)
	_, err := db.Exec(`INSERT INTO accounts(id,name,platform,type,extra,credentials) VALUES(200,'unbound owner@example.test','openai','oauth','{}','{"email":"owner@example.test"}'),(300,'single-key','openai','apikey','{"kilo_managed":true,"kilo_prepared_id":3}','{"api_key":"synthetic-single-secret"}')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO pk_kilo_prepared_accounts(id,account_id,status,name) VALUES(3,300,'prepared','single-key')`)
	require.NoError(t, err)
	physical := context.Background()
	logical := service.WithKiloAccountList(physical)
	total, err := repo.kiloAccountListFilteredQuery(physical, "", "", "", "", 0, "").Count(physical)
	require.NoError(t, err)
	require.Equal(t, 4, total)
	query := repo.kiloAccountListFilteredQuery(logical, "", "", "", "", 0, "")
	total, err = query.Clone().Count(logical)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	var got []int64
	for page := 0; page < total; page++ {
		ids, err := query.Clone().Order(dbent.Asc(dbaccount.FieldID)).Offset(page).Limit(1).IDs(logical)
		require.NoError(t, err)
		require.Len(t, ids, 1)
		got = append(got, ids...)
	}
	require.Equal(t, []int64{124, 200, 300}, got)
	for _, test := range []struct {
		search string
		ids    []int64
	}{
		{"GPT source", []int64{124}}, {"z-key", []int64{124}}, {"owner@example.test", []int64{124, 200}},
	} {
		ids, err := repo.kiloAccountListFilteredQuery(logical, "", "", "", test.search, 0, "").Order(dbent.Asc(dbaccount.FieldID)).IDs(logical)
		require.NoError(t, err)
		require.Equal(t, test.ids, ids)
	}
	ids, err := repo.kiloAccountListFilteredQuery(logical, "", service.AccountTypeOAuth, "", "", 0, "").IDs(logical)
	require.NoError(t, err)
	require.Equal(t, []int64{200}, ids)

	views, err := repo.GetKiloAccountViews(logical, []int64{124, 300, 124, -1})
	require.NoError(t, err)
	require.Equal(t, 1, native.queries, "one safe projection query for all page Key IDs")
	require.Len(t, views, 2)
	pair := views[124]
	require.EqualValues(t, 2, *pair.PreparedID)
	require.Equal(t, "prepared-key", pair.Name)
	require.Equal(t, "GPT source", pair.Source.Name)
	require.Equal(t, "owner@example.test", pair.Email)
	require.Equal(t, "org-1", pair.OrganizationID)
	require.True(t, pair.HasKey)
	require.EqualValues(t, 122, pair.Source.ID)
	require.Equal(t, "workspace-1", pair.Source.WorkspaceID)
	require.True(t, pair.Source.QuotaOnly)
	require.True(t, views[300].HasKey)
	require.Nil(t, views[300].Source, "an unbound Key is a single identity")
	full := dto.AccountFromService(&service.Account{ID: 124, Credentials: map[string]any{"api_key": "synthetic-gateway-secret"}})
	full.Kilo = &pair
	lite := dto.AccountListItemFromAccount(full)
	require.Equal(t, full.Kilo, lite.Kilo)
	for _, value := range []any{views, full, lite} {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "synthetic-")
		require.NotContains(t, string(raw), "access_token")
		require.NotContains(t, string(raw), "refresh_token")
	}
	observer := service.WithObserverScope(logical, []int64{7})
	_, err = db.Exec(`INSERT INTO account_groups(account_id,group_id) VALUES(122,7),(124,8)`)
	require.NoError(t, err)
	ids, err = observerAccountQuery(observer, repo.kiloAccountListFilteredQuery(observer, "", "", "", "", 0, "")).IDs(observer)
	require.NoError(t, err)
	require.Equal(t, []int64{122}, ids, "a gateway outside scope cannot hide an observer's visible source")
	require.Nil(t, dto.AccountForObserver(observer, full).Kilo)
}

func TestKiloAccountViewPostgresBindsSearchAndObserverScope(t *testing.T) {
	for _, observer := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
		repo := &accountRepository{client: client, sql: db}
		ctx := service.WithKiloAccountList(context.Background())
		if observer {
			ctx = service.WithObserverScope(ctx, []int64{7})
			mock.ExpectQuery(`.*ANY\(\$2\).*`).WithArgs(service.PlatformOpenAI, "{7}", "%gpt owner%", int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		} else {
			mock.ExpectQuery(`.*strpos\(lower\(s.name\),lower\(\$3\)\).*`).WithArgs(service.PlatformOpenAI, "%gpt owner%", "GPT owner", "GPT owner").WillReturnRows(sqlmock.NewRows([]string{"id"}))
		}
		_, err = observerAccountQuery(ctx, repo.kiloAccountListFilteredQuery(ctx, service.PlatformOpenAI, "", "", "GPT owner", 0, "")).IDs(ctx)
		require.NoError(t, err)
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestKiloAccountViewListEntrypointsUsePredicateBeforeCount(t *testing.T) {
	for _, all := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
		repo := &accountRepository{client: client, sql: db}
		ctx := service.WithKiloAccountList(context.Background())
		// Empty SQL rows keep this check on the entrypoint contract, without
		// involving unrelated group/proxy hydration queries.
		if !all {
			mock.ExpectQuery(`SELECT COUNT.*NOT EXISTS.*pk_kilo_prepared_accounts.*kilo_source_gateway_id.*kilo_source_prepared_id`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		}
		pattern := `SELECT .*NOT EXISTS.*pk_kilo_prepared_accounts.*kilo_source_gateway_id.*kilo_source_prepared_id`
		if !all {
			pattern += `.*LIMIT 1 OFFSET 1`
		}
		mock.ExpectQuery(pattern).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		if all {
			_, err = repo.ListAllWithFilters(ctx, "", "", "", "", 0, "")
		} else {
			_, result, listErr := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 2, PageSize: 1}, "", "", "", "", 0, "")
			err = listErr
			if err == nil {
				require.Zero(t, result.Total)
			}
		}
		require.NoError(t, err)
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

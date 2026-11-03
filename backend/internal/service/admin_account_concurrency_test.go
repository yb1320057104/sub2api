//go:build unit

package service

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAccountConcurrencyDefaultsInvalidGrokOAuthToOne(t *testing.T) {
	require.Equal(t, 1, normalizeAccountConcurrency(PlatformGrok, AccountTypeOAuth, 0))
	require.Equal(t, 1, normalizeAccountConcurrency(PlatformGrok, AccountTypeOAuth, -5))
}

func TestNormalizeAccountConcurrencyPreservesExplicitValues(t *testing.T) {
	require.Equal(t, 50, normalizeAccountConcurrency(PlatformGrok, AccountTypeOAuth, 50))
	require.Equal(t, 2, normalizeAccountConcurrency(PlatformOpenAI, AccountTypeOAuth, 2))
	require.Equal(t, 2, normalizeAccountConcurrency(PlatformGrok, AccountTypeAPIKey, 2))
}

func TestPK50ProxyBindingsValidateLimitsAndDuplicates(t *testing.T) {
	require.NoError(t, ValidateAccountProxyBindings(nil))
	require.NoError(t, ValidateAccountProxyBindings([]AccountProxyBindingInput{{ProxyID: 1, MaxConcurrency: 8, MaxAccounts: 1}}))
	require.Error(t, ValidateAccountProxyBindings([]AccountProxyBindingInput{{ProxyID: 1}, {ProxyID: 1}}))
	require.Error(t, ValidateAccountProxyBindings([]AccountProxyBindingInput{{ProxyID: 1, MaxConcurrency: -1}}))
	require.Error(t, ValidateAccountProxyBindings([]AccountProxyBindingInput{{ProxyID: 1, MaxAccounts: 10001}}))
}

type pk50AuthorizationProxyRepo struct {
	AccountRepository
	account  *Account
	bindings []AccountProxyBindingInput
	boundTx  *dbent.Tx
	fail     error
}

func (r *pk50AuthorizationProxyRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}
func (r *pk50AuthorizationProxyRepo) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Account, error) {
	return nil, nil
}
func (r *pk50AuthorizationProxyRepo) GetAccountProxyBindings(context.Context, int64) ([]AccountProxyBinding, error) {
	return r.account.ProxyBindings, nil
}
func (r *pk50AuthorizationProxyRepo) AssignAccountProxies(context.Context, AccountProxyAssignmentInput) (*AccountProxyAssignmentResult, error) {
	panic("authorization must not open independent assignment transaction")
}
func (r *pk50AuthorizationProxyRepo) ReplaceAccountProxyBindings(ctx context.Context, ids []int64, items []AccountProxyBindingInput) error {
	r.boundTx = dbent.TxFromContext(ctx)
	r.bindings = append([]AccountProxyBindingInput(nil), items...)
	if r.fail != nil {
		return r.fail
	}
	r.account.ProxyBindings = nil
	for _, item := range items {
		r.account.ProxyBindings = append(r.account.ProxyBindings, AccountProxyBinding{ProxyID: item.ProxyID, MaxConcurrency: item.MaxConcurrency, MaxAccounts: item.MaxAccounts})
	}
	return nil
}

type pk50AuthorizationProxyAdmin struct {
	AdminService
	repo      *pk50AuthorizationProxyRepo
	createdTx *dbent.Tx
	updated   *UpdateAccountInput
}

type pk50AuthorizationProxySnapshot struct {
	bindings  []AccountProxyBindingInput
	preserved bool
}

func (want pk50AuthorizationProxySnapshot) Match(value driver.Value) bool {
	raw, ok := value.(string)
	if !ok {
		return false
	}
	var got struct {
		Bindings  []AccountProxyBindingInput `json:"account_proxy_bindings"`
		Preserved bool                       `json:"account_proxy_preserved"`
	}
	if json.Unmarshal([]byte(raw), &got) != nil || got.Preserved != want.preserved || len(got.Bindings) != len(want.bindings) {
		return false
	}
	for i := range want.bindings {
		if got.Bindings[i] != want.bindings[i] {
			return false
		}
	}
	return true
}

func (a *pk50AuthorizationProxyAdmin) ValidateAccountGroupBindings(context.Context, []int64) error {
	return nil
}
func (a *pk50AuthorizationProxyAdmin) CreateAccount(ctx context.Context, in *CreateAccountInput) (*Account, error) {
	a.createdTx = dbent.TxFromContext(ctx)
	a.repo.account = &Account{ID: 91, Platform: in.Platform, Type: in.Type, Credentials: in.Credentials, ProxyID: in.ProxyID, GroupIDs: in.GroupIDs}
	return a.repo.account, nil
}
func (a *pk50AuthorizationProxyAdmin) UpdateAccount(ctx context.Context, id int64, in *UpdateAccountInput) (*Account, error) {
	a.updated = in
	a.repo.account.Credentials = in.Credentials
	return a.repo.account, nil
}

func TestPK50AuthorizationProxySettingsSnapshotAndLegacyDefaults(t *testing.T) {
	var legacy AuthorizationSubmit
	require.NoError(t, json.Unmarshal([]byte(`{"account_proxy_mode":"managed","max_accounts_per_proxy":3}`), &legacy))
	require.NoError(t, normalizeAuthorizationProxyOptions(&legacy))
	require.Equal(t, 1, legacy.ProxiesPerAccount)
	require.Nil(t, legacy.ProxyConcurrency, "old queued jobs must not set a new global concurrency limit")
	concurrency := 8
	input := &AuthorizationSubmit{AuthorizationConcurrency: 3, AccountConcurrency: 20, AccountPriority: 1, AccountProxyMode: "managed", MaxAccountsPerProxy: 10000, ProxiesPerAccount: 4, ProxyConcurrency: &concurrency, PassGroupIDs: []int64{7}}
	s := &AccountAuthorizationService{admin: &pk50AuthorizationProxyAdmin{}}
	settings, err := s.normalizeAuthorizationWorkbench(context.Background(), input)
	require.NoError(t, err)
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	var queued AuthorizationSubmit
	require.NoError(t, json.Unmarshal(raw, &queued))
	require.Equal(t, 4, queued.ProxiesPerAccount)
	require.Equal(t, 8, *queued.ProxyConcurrency)
	require.Equal(t, 10000, queued.MaxAccountsPerProxy)
	require.Equal(t, []int64{7}, queued.GroupIDs)
	for _, invalid := range []int{0, -1, 10001} {
		settings.ProxyConcurrency = &invalid
		require.Error(t, normalizeAuthorizationProxyOptions(&settings))
	}
	settings.ProxyConcurrency = &concurrency
	settings.ProxiesPerAccount = 101
	require.Error(t, normalizeAuthorizationProxyOptions(&settings))
}

func TestPK50AuthorizationProxyPlannerRespectsGlobalCapacity(t *testing.T) {
	limit := 6
	settings := &AuthorizationSubmit{AccountProxyMode: "managed", ProxiesPerAccount: 2, ProxyConcurrency: &limit, MaxAccountsPerProxy: 3}
	pool := []authorizationProxyCandidate{{ID: 1, AccountCount: 1, MaxAccounts: 1, MaxConcurrency: 2}, {ID: 2, AccountCount: 1, MaxAccounts: 2, MaxConcurrency: 3}, {ID: 3, MaxConcurrency: 9}}
	plan, err := planAuthorizationImportProxies(settings, pool)
	require.NoError(t, err)
	require.True(t, plan.ApplyBindings)
	require.Equal(t, []AccountProxyBindingInput{{ProxyID: 3, MaxAccounts: 3, MaxConcurrency: 6}, {ProxyID: 2, MaxAccounts: 2, MaxConcurrency: 3}}, plan.Bindings)
	settings.ProxiesPerAccount = 3
	plan, err = planAuthorizationImportProxies(settings, pool)
	require.Error(t, err)
	require.Nil(t, plan, "capacity failure cannot return a partial assignment")
	legacy := &AuthorizationSubmit{AccountProxyMode: "managed", MaxAccountsPerProxy: 3}
	plan, err = planAuthorizationImportProxies(legacy, pool)
	require.NoError(t, err)
	require.False(t, plan.ApplyBindings)
	require.EqualValues(t, 2, *plan.PrimaryID)
	require.Equal(t, 3, plan.Bindings[0].MaxConcurrency)
	settings.AccountProxyMode = "direct"
	plan, err = planAuthorizationImportProxies(settings, pool)
	require.NoError(t, err)
	require.Nil(t, plan.PrimaryID)
	require.False(t, plan.ApplyBindings)
}

func TestPK50AuthorizationProxyImportUsesAccountTransaction(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commits_all_bindings", true: "binding_failure_rolls_back_import"}[fail], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := &pk50AuthorizationProxyRepo{}
			if fail {
				repo.fail = errors.New("fixture binding failure")
			}
			admin := &pk50AuthorizationProxyAdmin{repo: repo}
			s := &AccountAuthorizationService{db: db, client: client, accounts: repo, admin: admin, oauth: &OpenAIOAuthService{}}
			concurrency := 5
			job := &authorizationJob{id: 11, token: "fixture-owner", email: "fixture@example.test", settings: AuthorizationSubmit{AccountProxyMode: "managed", MaxAccountsPerProxy: 2, ProxiesPerAccount: 2, ProxyConcurrency: &concurrency, AccountConcurrency: 20, GroupIDs: []int64{7}}}
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE pk_authorization_jobs SET stage=stage").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT p.id,.*COALESCE").WillReturnRows(sqlmock.NewRows([]string{"id", "max_accounts", "max_concurrency", "count"}).AddRow(21, 0, 0, 0).AddRow(22, 1, 3, 0))
			if fail {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec("INSERT INTO pk_authorization_state").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("UPDATE pk_authorization_jobs SET account_id").WithArgs(job.id, job.token, int64(91), int64(0), "completed", "completed", sqlmock.AnyArg(), pk50AuthorizationProxySnapshot{bindings: []AccountProxyBindingInput{{ProxyID: 21, MaxAccounts: 2, MaxConcurrency: 5}, {ProxyID: 22, MaxAccounts: 1, MaxConcurrency: 3}}}).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			err = s.applyAuthorization(context.Background(), &AuthorizationConfig{}, job, nil, &OpenAITokenInfo{AccessToken: "synthetic", Email: job.email})
			if fail {
				require.Error(t, err)
				require.False(t, job.authorizationCompleted)
			} else {
				require.NoError(t, err)
				require.True(t, job.authorizationCompleted)
			}
			require.NotNil(t, admin.createdTx)
			require.Same(t, admin.createdTx, repo.boundTx)
			require.Equal(t, []AccountProxyBindingInput{{ProxyID: 21, MaxAccounts: 2, MaxConcurrency: 5}, {ProxyID: 22, MaxAccounts: 1, MaxConcurrency: 3}}, repo.bindings)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPK50AuthorizationProxyReauthorizationPreservesLatestBindings(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	oldPrimary, newPrimary := int64(21), int64(31)
	before := &Account{ID: 91, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"email": "fixture@example.test", "access_token": "old-fixture"}, ProxyID: &oldPrimary, ProxyBindings: []AccountProxyBinding{{ProxyID: 21}, {ProxyID: 22}}}
	current := *before
	current.ProxyID = &newPrimary
	current.ProxyBindings = []AccountProxyBinding{{ProxyID: 31, MaxConcurrency: 7, MaxAccounts: 1}, {ProxyID: 32, MaxConcurrency: 8, MaxAccounts: 2}}
	repo := &pk50AuthorizationProxyRepo{account: &current}
	admin := &pk50AuthorizationProxyAdmin{repo: repo}
	s := &AccountAuthorizationService{db: db, client: client, accounts: repo, admin: admin, oauth: &OpenAIOAuthService{}}
	concurrency := 2
	job := &authorizationJob{id: 11, token: "fixture-owner", email: "fixture@example.test", settings: AuthorizationSubmit{AccountProxyMode: "managed", MaxAccountsPerProxy: 1, ProxiesPerAccount: 4, ProxyConcurrency: &concurrency}}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE pk_authorization_jobs SET stage=stage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT id FROM accounts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO pk_authorization_state").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE pk_authorization_jobs SET account_id").WithArgs(job.id, job.token, int64(91), int64(0), "completed", "completed", sqlmock.AnyArg(), pk50AuthorizationProxySnapshot{preserved: true, bindings: []AccountProxyBindingInput{{ProxyID: 31, MaxConcurrency: 7, MaxAccounts: 1}, {ProxyID: 32, MaxConcurrency: 8, MaxAccounts: 2}}}).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, s.applyAuthorization(context.Background(), &AuthorizationConfig{}, job, before, &OpenAITokenInfo{AccessToken: "new-fixture", Email: job.email}))
	require.NotNil(t, admin.updated)
	require.Nil(t, admin.updated.ProxyBindings)
	require.Nil(t, admin.updated.ProxyID)
	require.Nil(t, repo.bindings)
	require.Equal(t, []AccountProxyBinding{{ProxyID: 31, MaxConcurrency: 7, MaxAccounts: 1}, {ProxyID: 32, MaxConcurrency: 8, MaxAccounts: 2}}, repo.account.ProxyBindings)
	require.EqualValues(t, 31, *repo.account.ProxyID)
	require.NoError(t, mock.ExpectationsWereMet())
}

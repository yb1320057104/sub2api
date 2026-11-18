//go:build importtest

package kiloimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// This package compiles the real service and HTTP boundary without compiling
// unrelated service tests or requiring an external database/login runtime.
type importCipher struct{}

func (importCipher) Encrypt(raw string) (string, error) {
	sum := sha256.Sum256([]byte(raw))
	return "fixture-cipher:" + hex.EncodeToString(sum[:]), nil
}
func (importCipher) Decrypt(string) (string, error) { return "", errors.New("unexpected decrypt") }

type importAccounts struct {
	service.AccountRepository
	t       *testing.T
	tx      *dbent.Tx
	created []*service.Account
	updated int
}

func (a *importAccounts) transaction(ctx context.Context) {
	a.t.Helper()
	tx := dbent.TxFromContext(ctx)
	require.NotNil(a.t, tx, "every account operation must use the import transaction")
	if a.tx == nil {
		a.tx = tx
	}
	require.Same(a.t, a.tx, tx)
}
func (a *importAccounts) LockAccountMutation(ctx context.Context) error {
	a.transaction(ctx)
	return nil
}
func (a *importAccounts) Create(ctx context.Context, account *service.Account) error {
	a.transaction(ctx)
	require.Equal(a.t, service.PlatformOpenAI, account.Platform)
	require.Equal(a.t, service.AccountTypeOAuth, account.Type)
	require.False(a.t, account.Schedulable)
	require.Equal(a.t, true, account.Extra["kilo_quota_only"])
	require.Nil(a.t, account.ProxyID, "the selected idle slot may change the final login proxy")
	require.Empty(a.t, account.GroupIDs)
	require.Equal(a.t, map[string]any{"email": "import@example.test"}, account.Credentials)
	account.ID = int64(101 + len(a.created))
	a.created = append(a.created, account)
	return nil
}
func (a *importAccounts) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	a.transaction(ctx)
	for _, account := range a.created {
		if account.ID == id {
			return account, nil
		}
	}
	return nil, sql.ErrNoRows
}
func (a *importAccounts) Update(ctx context.Context, account *service.Account) error {
	a.transaction(ctx)
	require.False(a.t, account.Schedulable)
	require.EqualValues(a.t, 8, account.Extra["kilo_onboarding_proxy_id"])
	a.updated++
	return nil
}

type importAdmin struct{ service.AdminService }

func (*importAdmin) ValidateAccountGroupBindings(context.Context, []int64) error { return nil }

type importGroups struct{ service.GroupRepository }

func (*importGroups) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	return &service.Group{ID: id, Platform: service.PlatformOpenAI, Status: service.StatusActive}, nil
}

type importProxies struct{ service.ProxyRepository }

func (*importProxies) GetByID(_ context.Context, id int64) (*service.Proxy, error) {
	return &service.Proxy{ID: id, Protocol: "http", Host: "127.0.0.1", Port: 12345, Status: service.StatusActive}, nil
}

type importRemote struct{ service.KiloRemote }

func (*importRemote) ProbeLoginProxy(context.Context, string) ([]service.KiloProxyCheck, error) {
	return []service.KiloProxyCheck{{Target: "openai", Reachable: true}, {Target: "kilo_api", Reachable: true}, {Target: "kilo_login", Reachable: true}}, nil
}

type noLoginSecrets struct{}

func (noLoginSecrets) Match(value driver.Value) bool {
	raw, ok := value.(string)
	return ok && !strings.Contains(raw, "fixture-password") && !strings.Contains(raw, "JBSWY3DPEHPK3PXP") && !strings.Contains(raw, "\"password\"") && !strings.Contains(raw, "\"totp_secret\"")
}

func newImportFixture(t *testing.T) (*service.KiloService, sqlmock.Sqlmock, *importAccounts) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	accounts := &importAccounts{t: t}
	cipher := importCipher{}
	svc := service.NewKiloService(db, client, accounts, &importAdmin{}, &importGroups{}, &importProxies{}, cipher, &importRemote{}, nil, nil, nil)
	svc.SetOnboardingDependencies(service.NewOpenAIOAuthReauthService(nil, nil, nil, nil, cipher, true, nil, nil), nil)
	return svc, mock, accounts
}

func importInput() service.KiloPoolImportInput {
	return service.KiloPoolImportInput{
		RequestID: uuid.NewString(), BindingStrategy: "new_identity",
		Items:    []service.KiloPoolImportItem{{Email: "import@example.test", Password: " fixture-password ", TOTPSecret: "JBSWY3DPEHPK3PXP", Name: "import", LoginProxyID: 8}},
		GroupIDs: []int64{7},
		Routing:  service.KiloRoutingOptions{ProxyGroupID: 9, ProxiesPerAccount: 5, ProxyConcurrency: 20, AccountConcurrency: 100, AutoReleaseStatuses: []int{401, 402, 403}},
	}
}

func expectImportStart(mock sqlmock.Sqlmock, input service.KiloPoolImportInput, result any) {
	query := mock.ExpectQuery("SELECT payload_hash,result FROM pk_kilo_import_requests").WithArgs(input.RequestID)
	if result == nil {
		query.WillReturnRows(sqlmock.NewRows([]string{"payload_hash", "result"}))
		mock.ExpectBegin()
		mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(int64(70450001)).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("SELECT payload_hash,result FROM pk_kilo_import_requests").WithArgs(input.RequestID).WillReturnRows(sqlmock.NewRows([]string{"payload_hash", "result"}))
		return
	}
	raw, _ := json.Marshal(input)
	sum := sha256.Sum256(raw)
	encoded, _ := json.Marshal(result)
	query.WillReturnRows(sqlmock.NewRows([]string{"payload_hash", "result"}).AddRow(hex.EncodeToString(sum[:]), encoded))
}

func expectImportSubmission(mock sqlmock.Sqlmock, input service.KiloPoolImportInput, failure error) {
	password, _ := (importCipher{}).Encrypt(input.Items[0].Password)
	totp, _ := (importCipher{}).Encrypt(input.Items[0].TOTPSecret)
	mock.ExpectExec("INSERT INTO openai_oauth_reauth_configs").WithArgs(int64(101), "import@example.test", "password_totp", "local_worker", "account", password, totp).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO account_token_guard_v2_accounts.*VALUES\\(\\$1,true,true\\)").WithArgs(int64(101)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(int64(70450001)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id::text,payload_hash FROM pk_kilo_batches").WithArgs(input.RequestID).WillReturnRows(sqlmock.NewRows([]string{"id", "hash"}))
	mock.ExpectExec("INSERT INTO pk_kilo_batches").WithArgs(sqlmock.AnyArg(), input.RequestID, sqlmock.AnyArg(), noLoginSecrets{}).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT EXISTS.*FROM pk_kilo_jobs").WithArgs(int64(101)).WillReturnRows(sqlmock.NewRows([]string{"used"}).AddRow(false))
	mock.ExpectQuery("SELECT login_email,COALESCE.*FROM openai_oauth_reauth_configs").WithArgs(int64(101)).WillReturnRows(sqlmock.NewRows([]string{"email", "password", "totp", "mode", "updated"}).AddRow("import@example.test", password, totp, "password_totp", time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)))
	job := mock.ExpectQuery("INSERT INTO pk_kilo_jobs.*RETURNING id")
	if failure != nil {
		job.WillReturnError(failure)
		mock.ExpectRollback()
		return
	}
	job.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(501))
	mock.ExpectExec("INSERT INTO pk_kilo_import_requests").WithArgs(input.RequestID, sqlmock.AnyArg(), noLoginSecrets{}).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func TestKiloPoolImportCommitsOnlyQuotaSourceAndEncryptedLogin(t *testing.T) {
	svc, mock, accounts := newImportFixture(t)
	input := importInput()
	expectImportStart(mock, input, nil)
	expectImportSubmission(mock, input, nil)
	result, err := svc.ImportOnboarding(context.Background(), input)
	require.NoError(t, err)
	require.NoError(t, uuid.Validate(result.BatchID))
	require.Equal(t, []service.KiloOnboardingItemResult{{Line: 1, SourceAccountID: 101, JobID: 501, Status: "queued"}}, result.Items)
	require.Len(t, accounts.created, 1)
	require.Equal(t, "auto", accounts.created[0].Extra["kilo_workspace_mode"])
	require.Equal(t, 1, accounts.updated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloPoolImportReplaysAndRejectsDifferentSecret(t *testing.T) {
	input := importInput()
	original := &service.KiloOnboardingBatchResult{BatchID: uuid.NewString(), Items: []service.KiloOnboardingItemResult{{Line: 1, SourceAccountID: 101, JobID: 501, Status: "queued"}}}
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "replay", true: "different_password"}[changed], func(t *testing.T) {
			svc, mock, accounts := newImportFixture(t)
			expectImportStart(mock, input, original)
			request := input
			request.Items = append([]service.KiloPoolImportItem(nil), input.Items...)
			if changed {
				request.Items[0].Password = "different-fixture-password"
			}
			result, err := svc.ImportOnboarding(context.Background(), request)
			if changed {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, original, result)
			}
			require.Empty(t, accounts.created, "replay/conflict must happen before any source write")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiloPoolImportQueueFailureRollsBackSourceAndConfig(t *testing.T) {
	svc, mock, accounts := newImportFixture(t)
	input := importInput()
	failure := errors.New("fixture queue write failure")
	expectImportStart(mock, input, nil)
	expectImportSubmission(mock, input, failure)
	result, err := svc.ImportOnboarding(context.Background(), input)
	require.ErrorIs(t, err, failure)
	require.Nil(t, result)
	require.Len(t, accounts.created, 1)
	require.Zero(t, accounts.updated)
	require.NoError(t, mock.ExpectationsWereMet(), "the same transaction must roll back without persisting a receipt")
}

func TestKiloPoolImportRejectsBatchErrorsBeforePersistence(t *testing.T) {
	for _, kind := range []string{"duplicate_email", "over_limit", "invalid_uuid", "missing_password", "invalid_model"} {
		t.Run(kind, func(t *testing.T) {
			svc, mock, accounts := newImportFixture(t)
			input := importInput()
			switch kind {
			case "duplicate_email":
				input.Items = append(input.Items, input.Items[0])
				input.Items[1].Email = " IMPORT@EXAMPLE.TEST "
			case "over_limit":
				input.Items = make([]service.KiloPoolImportItem, 101)
			case "invalid_uuid":
				input.RequestID = "invalid"
			case "missing_password":
				input.Items[0].Password = ""
			case "invalid_model":
				input.Items[0].VerifiedModelIDs = []string{"openai/*"}
			}
			_, err := svc.ImportOnboarding(context.Background(), input)
			require.Error(t, err)
			require.Empty(t, accounts.created)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiloPoolImportHTTPBodyIsHiddenBeforeAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, authorized := range []bool{false, true} {
		t.Run(map[bool]string{false: "denied", true: "malformed_with_secrets"}[authorized], func(t *testing.T) {
			router := gin.New()
			router.Use(handler.KiloOnboardingImportPrivacy())
			before, after := "", ""
			router.Use(func(c *gin.Context) {
				raw, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				before = string(raw)
				if authorized {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
					c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
				}
				c.Next()
				raw, err = io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				after = string(raw)
			})
			h := handler.NewKiloHandler(nil, nil)
			router.POST("/api/v1/admin/kilo/onboarding/import", h.ImportOnboarding)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/kilo/onboarding/import", strings.NewReader(`{"items":[{"password":"fixture-password","totp_secret":"JBSWY3DPEHPK3PXP"}],"unexpected":true}`)))
			require.Empty(t, before, "audit runs before the handler and must never read the import stream")
			require.Empty(t, after)
			expected := http.StatusUnauthorized
			if authorized {
				expected = http.StatusBadRequest
			}
			require.Equal(t, expected, recorder.Code)
			require.NotContains(t, recorder.Body.String(), "fixture-password")
			require.NotContains(t, recorder.Body.String(), "JBSWY3DPEHPK3PXP")
		})
	}
}

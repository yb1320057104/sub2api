package repository

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestExcelOAuthUsesOfficialTokenContract(t *testing.T) {
	for _, grant := range []string{"refresh_token", "authorization_code"} {
		t.Run(grant, func(t *testing.T) {
			var got *http.Request
			server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				got = r
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"new-at","refresh_token":"new-rt","expires_in":3600}`))
			}))
			defer server.Close()
			client := &openaiOAuthService{tokenURL: server.URL + "/oauth/token?existing=kept"}
			var result *openai.TokenResponse
			var err error
			if grant == "refresh_token" {
				result, err = client.RefreshTokenWithClientID(context.Background(), "excel-rt", "", openai.ExcelClientID)
			} else {
				result, err = client.ExchangeCode(context.Background(), "code", "verifier", "https://auth.openai.com/basispoints/deviceauth/callback", "", openai.ExcelClientID)
			}
			require.NoError(t, err)
			require.Equal(t, "new-at", result.AccessToken)
			require.NotNil(t, got)
			require.Equal(t, "true", got.URL.Query().Get("unified"))
			require.Equal(t, "kept", got.URL.Query().Get("existing"))
			require.Equal(t, openai.ExcelClientID, got.PostForm.Get("client_id"))
			require.Equal(t, grant, got.PostForm.Get("grant_type"))
			require.False(t, got.PostForm.Has("scope"))
			require.Empty(t, got.Header.Get("originator"))
		})
	}
}

func TestExcelOAuthRejectsWithoutEchoingCredentialBody(t *testing.T) {
	server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"private-credential"}`))
	}))
	defer server.Close()
	client := &openaiOAuthService{tokenURL: server.URL}
	_, err := client.RefreshTokenWithClientID(context.Background(), "excel-rt", "", openai.ExcelClientID)
	require.ErrorContains(t, err, "status 401")
	require.NotContains(t, err.Error(), "private-credential")
	_, err = client.ExchangeCode(context.Background(), "code", "verifier", openai.ExcelRedirectURI, "", openai.ExcelClientID)
	require.ErrorContains(t, err, "status 401")
	require.NotContains(t, err.Error(), "private-credential")
}

func TestExcelOAuthCandidateQueryExcludesLegacyTasksBeforeLimit(t *testing.T) {
	// Verify the actual repository query, including that exclusion is applied
	// before LIMIT rather than discarding the first bounded page in Go.
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, query string) error {
		query = strings.Join(strings.Fields(query), " ")
		require.Contains(t, query, "a.status<>'disabled' AND a.schedulable")
		require.Contains(t, query, "NOT guard.enabled OR NOT guard.auto_relogin_enabled")
		require.Contains(t, query, "st.status='banned' AND st.credential_version=md5(a.credentials::text)")
		require.Contains(t, query, "settings->>'authorization_completed'='true') ORDER BY id DESC LIMIT 1),'')<>'banned'")
		require.Contains(t, query, "NOT EXISTS(SELECT 1 FROM pk_authorization_jobs legacy WHERE legacy.status IN ('queued','running')")
		require.Contains(t, query, "legacy.account_id=a.id OR legacy.email=lower(trim(COALESCE(NULLIF(a.credentials->>'email',''),NULLIF(a.extra->>'email_address',''),NULLIF(a.extra->>'email',''),'')))")
		require.Less(t, strings.Index(query, "FROM pk_authorization_jobs legacy"), strings.Index(query, "ORDER BY a.id LIMIT $1"))
		return nil
	})))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("eligible candidates").WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
	repo := &openAIOAuthReauthRepository{db: db}
	ids, err := repo.ListMissingExcelAuthorizations(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, []int64{3}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExcelOAuthAutomaticInsertionRechecksEligibility(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT EXISTS").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(false))
	mock.ExpectQuery("SELECT id FROM accounts.*FOR UPDATE").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
	mock.ExpectQuery("SELECT account_id FROM account_token_guard_v2_accounts.*FOR UPDATE").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
	// A control changed after candidate selection: the locked recheck is now
	// empty and must not fall back to an unchecked/manual INSERT.
	mock.ExpectQuery("SELECT a.id FROM accounts a.*NOT guard.enabled OR NOT guard.auto_relogin_enabled.*AND a.id=").
		WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	repo := &openAIOAuthReauthRepository{db: db}
	task, err := repo.CreateAutomaticExcelTask(context.Background(), 42, "snapshot")
	require.NoError(t, err)
	require.Nil(t, task)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExcelOAuthAutomaticInsertionSkipsBusyControls(t *testing.T) {
	for _, busy := range []string{"account", "guard"} {
		t.Run(busy, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT EXISTS").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"busy"}).AddRow(false))
			account := mock.ExpectQuery("SELECT id FROM accounts.*FOR UPDATE NOWAIT").WithArgs(int64(42))
			if busy == "account" {
				account.WillReturnError(&pq.Error{Code: "55P03"})
			} else {
				account.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
				mock.ExpectQuery("SELECT account_id FROM account_token_guard_v2_accounts.*FOR UPDATE NOWAIT").WithArgs(int64(42)).WillReturnError(&pq.Error{Code: "55P03"})
			}
			mock.ExpectRollback()
			repo := &openAIOAuthReauthRepository{db: db}
			task, err := repo.CreateAutomaticExcelTask(context.Background(), 42, "snapshot")
			require.NoError(t, err)
			require.Nil(t, task)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

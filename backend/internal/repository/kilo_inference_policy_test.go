package repository

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestKiloInferenceTransportGuardUsesCurrentRow(t *testing.T) {
	const query = `SELECT id,platform,type,status,schedulable,parent_account_id,credentials,extra FROM accounts WHERE id=$1 AND deleted_at IS NULL`
	for _, phase := range []string{"pending", "bound", "shadow", "managed_official_url", "state_unavailable"} {
		t.Run(phase, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := &accountRepository{sql: db}
			router := service.NewAccountProxyRouter(repo, nil)
			columns := []string{"id", "platform", "type", "status", "schedulable", "parent_account_id", "credentials", "extra"}
			request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"input":"synthetic"}`))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer synthetic-source-oauth")
			row := mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(int64(21))
			switch phase {
			case "pending":
				row.WillReturnRows(sqlmock.NewRows(columns).AddRow(21, "openai", "oauth", "active", true, nil, `{"access_token":"synthetic-source-oauth"}`, `{"kilo_quota_only":true,"kilo_onboarding_job_id":3}`))
			case "bound":
				row.WillReturnRows(sqlmock.NewRows(columns).AddRow(21, "openai", "oauth", "active", true, nil, `{"access_token":"synthetic-source-oauth"}`, `{"kilo_source_prepared_id":7,"kilo_source_gateway_id":22}`))
			case "shadow":
				row.WillReturnRows(sqlmock.NewRows(columns).AddRow(21, "openai", "oauth", "active", true, 23, `{}`, `{}`))
				mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(int64(23)).WillReturnRows(sqlmock.NewRows(columns).AddRow(23, "openai", "oauth", "active", true, nil, `{"access_token":"synthetic-source-oauth"}`, `{"kilo_quota_only":true}`))
			case "managed_official_url":
				row.WillReturnRows(sqlmock.NewRows(columns).AddRow(21, "openai", "apikey", "active", true, nil, `{"api_key":"synthetic-kilo-key","base_url":"https://ai-gateway.kilo.ai/api/v1"}`, `{"kilo_managed":true,"kilo_bridge_enabled":true,"kilo_gateway_profile":"cli","kilo_prepared_id":7}`))
				mock.ExpectQuery(regexp.QuoteMeta(kiloSourceSendHealthSQL)).WithArgs(int64(21), int64(7)).
					WillReturnRows(sqlmock.NewRows([]string{"source_id", "reciprocal", "healthy"}).AddRow(nil, false, false))
			case "state_unavailable":
				row.WillReturnError(errors.New("synthetic state read failed"))
			}
			upstream := &httpUpstreamService{proxyRouter: router}
			response, err := upstream.Do(request, "", 21, 1)
			require.Error(t, err, "final transport must reject before DNS/client construction")
			require.Nil(t, response)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiloInferenceQuotaOnlySurvivesOrdinaryExtraWrites(t *testing.T) {
	for _, current := range []map[string]any{{"kilo_quota_only": true}, {"kilo_source_prepared_id": 7}, {"kilo_source_gateway_id": 22}} {
		for _, incoming := range []map[string]any{nil, {}, {"kilo_quota_only": false}, {"kilo_quota_only": nil}} {
			got := preserveKiloQuotaOnlyExtra(incoming, current)
			require.Equal(t, true, got["kilo_quota_only"], "release cannot restore official inference")
		}
	}
	query := preserveKiloQuotaOnlySQL("COALESCE(extra, '{}'::jsonb) || $1::jsonb")
	require.Contains(t, query, "extra->>'kilo_quota_only'")
	require.Contains(t, query, "extra->>'kilo_source_prepared_id'")
	require.Contains(t, query, "extra->>'kilo_source_gateway_id'")
	// A bulk form can explicitly submit false. The generated statement must
	// make the committed marker win atomically over that payload.
	recording := &recordingSQLExecutor{result: rowsAffectedResult(1)}
	bulk := newAccountRepositoryWithSQL(nil, recording, nil)
	_, err := bulk.BulkUpdate(context.Background(), []int64{21}, service.AccountBulkUpdate{Extra: map[string]any{"kilo_quota_only": false}})
	require.NoError(t, err)
	require.NotEmpty(t, recording.execQueries)
	require.Contains(t, recording.execQueries[0], "THEN (")
	require.Contains(t, recording.execQueries[0], `|| '{"kilo_quota_only":true}'::jsonb`)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	repo := newAccountRepositoryWithSQL(client, db, nil)
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts SET extra = CASE WHEN .*kilo_quota_only.*THEN .*kilo_quota_only.* ELSE .* END, updated_at`).
		WithArgs(`{"kilo_quota_only":false}`, int64(21)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WithArgs(service.SchedulerOutboxEventAccountChanged, int64(21), nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.UpdateExtra(context.Background(), 21, map[string]any{"kilo_quota_only": false}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiloInferenceSchedulerProjectionKeepsQuotaOnlyAndScope(t *testing.T) {
	source := service.Account{ID: 21, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Extra: map[string]any{"kilo_quota_only": true}}
	projected := buildSchedulerMetadataAccount(source)
	require.True(t, projected.IsKiloQuotaOnly())
	require.False(t, projected.IsSchedulable(), "cleared pair markers after release do not restore OAuth dispatch")
	gateway := service.Account{ID: 22, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "synthetic-kilo-key", "base_url": service.KiloGatewayBaseURL, "model_mapping": map[string]any{"openai/gpt-test": "openai/gpt-test"}},
		Extra: map[string]any{service.KiloManagedExtraKey: true, service.AccountExtraKiloBridgeEnabled: true, service.KiloGatewayProfileExtra: service.KiloGatewayCLIProfile, "kilo_prepared_id": 7, "kilo_onboarding_source_id": 21,
			"kilo_subscription_models": []string{"openai/gpt-test"}, "kilo_subscription_scope_source": "administrator_verified_exact_models", "kilo_subscription_scope_verified_at": "2026-11-14T12:00:00Z", "openai_passthrough": true}}
	projected = buildSchedulerMetadataAccount(gateway)
	require.True(t, projected.IsKiloGatewayAccount())
	require.True(t, projected.IsModelSupported("openai/gpt-test"))
	require.False(t, projected.IsModelSupported("openai/paid-model"))
	require.Equal(t, gateway.Extra["kilo_subscription_models"], projected.Extra["kilo_subscription_models"])
}

func TestKiloInferenceTransportGuardLeavesReadOnlyQueriesAlone(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	router := service.NewAccountProxyRouter(&accountRepository{sql: db}, nil)
	for _, endpoint := range []string{"https://chatgpt.com/backend-api/wham/usage", "https://chatgpt.com/backend-api/codex/models", "https://chatgpt.com/backend-api/accounts/check/v4-2023-04-27"} {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
		require.NoError(t, err)
		require.NoError(t, router.GuardKiloInferenceRequest(request, 21))
	}
	require.NoError(t, mock.ExpectationsWereMet(), "read-only metadata does not require scheduler eligibility or inference SQL")
}

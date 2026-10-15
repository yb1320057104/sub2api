//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPKUsageOriginalSnapshotsAndTrends(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{Email: "pk-original-" + uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-original-" + uuid.NewString(), Name: "original"})
	account := mustCreateAccount(t, client, &service.Account{Name: "pk-original-" + uuid.NewString()})
	start := time.Date(2026, 1, 12, 0, 0, 0, 0, timezone.Now().Location())
	end := start.Add(24 * time.Hour)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM usage_logs WHERE user_id = $1`, user.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM usage_dashboard_hourly WHERE bucket_start >= $1 AND bucket_start < $2`, start, end)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM usage_dashboard_daily WHERE bucket_date = $1::date`, start)
	})

	original := &usagestats.OriginalTokenUsage{InputTokens: 0, OutputTokens: 20, CacheCreationTokens: 2000, CacheReadTokens: 8000}
	for index, mode := range []string{"single", "batch", "best-effort"} {
		log := &service.UsageLog{
			UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID,
			RequestID: uuid.NewString(), Model: "gpt-6.1-sol",
			InputTokens: 0, OutputTokens: 20, CacheCreationTokens: 0, CacheReadTokens: 9000,
			OriginalUsage: original, TotalCost: 0.5, ActualCost: 1, CreatedAt: start.Add(time.Duration(index) * time.Hour),
		}
		var err error
		switch mode {
		case "single":
			_, err = repo.createSingle(ctx, integrationDB, log)
		case "batch":
			_, err = repo.Create(ctx, log)
		case "best-effort":
			err = repo.CreateBestEffort(ctx, log)
		}
		require.NoError(t, err)
		var id int64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT id FROM usage_logs WHERE request_id = $1 AND api_key_id = $2`, log.RequestID, key.ID).Scan(&id))
		stored, err := repo.GetByID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, original, stored.OriginalUsage, mode)
		require.Equal(t, 9000, stored.CacheReadTokens, "customer counters must retain conversion")
		require.Zero(t, stored.CacheCreationTokens)
		require.Equal(t, 1.0, stored.ActualCost)
		inserted, err := repo.Create(ctx, log)
		require.NoError(t, err)
		require.False(t, inserted, "retry must not duplicate either set of tokens")
	}
	// Old rows remain unknown, even when the account's current conversion changes.
	legacy := &service.UsageLog{
		UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID,
		RequestID: uuid.NewString(), Model: "gpt-6.1-sol", CacheReadTokens: 123,
		CreatedAt: start.Add(3 * time.Hour),
	}
	_, err := repo.Create(ctx, legacy)
	require.NoError(t, err)
	stored, err := repo.GetByID(ctx, legacy.ID)
	require.NoError(t, err)
	require.Nil(t, stored.OriginalUsage)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra = '{"cache_conversion_enabled":true,"cache_conversion_multiplier":0}' WHERE id=$1`, account.ID)
	require.NoError(t, err)
	filters := usagestats.UsageLogFilters{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, Model: "gpt-6.1-sol"}
	for _, readCtx := range []context.Context{ctx, usagestats.WithCustomerBillingView(ctx)} {
		trend, err := repo.GetUsageTrendWithUsageFilters(readCtx, start, end, "hour", filters)
		require.NoError(t, err)
		require.Len(t, trend, 4)
		for _, point := range trend[:3] {
			require.Equal(t, original, point.OriginalUsage)
			require.Equal(t, point.Requests, point.OriginalUsageRequests)
			require.Equal(t, int64(9000), point.CacheReadTokens)
			require.Equal(t, 1.0, point.ActualCost)
		}
		require.Nil(t, trend[3].OriginalUsage)
		require.Zero(t, trend[3].OriginalUsageRequests)
		userTrend, err := repo.GetUserUsageTrendByUserID(readCtx, user.ID, start, end, "hour")
		require.NoError(t, err)
		require.Equal(t, trend, userTrend)
	}
	// Exercise the production hourly -> daily aggregate path, including mixed
	// historical coverage. A partial day's 80% must not be labeled a full-day rate.
	aggregator := newDashboardAggregationRepositoryWithSQL(integrationDB)
	require.NoError(t, aggregator.upsertHourlyAggregates(ctx, start, end))
	require.NoError(t, aggregator.upsertDailyAggregates(ctx, start, end))
	for _, granularity := range []string{"hour", "day"} {
		direct, err := repo.GetUsageTrendWithUsageFilters(ctx, start, end, granularity, filters)
		require.NoError(t, err)
		aggregated, err := repo.GetUsageTrendWithFilters(ctx, start, end, granularity, 0, 0, 0, 0, "", nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, direct, aggregated)
		if granularity == "day" {
			require.Len(t, direct, 1)
			require.Equal(t, int64(4), direct[0].Requests)
			require.Equal(t, int64(3), direct[0].OriginalUsageRequests)
			require.Equal(t, int64(6000), direct[0].OriginalUsage.CacheCreationTokens)
			require.Equal(t, int64(24000), direct[0].OriginalUsage.CacheReadTokens)
		}
	}
}

func TestPKHiddenBillingUsageSnapshotsAndCustomerAggregates(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{Email: "pk-hidden-" + uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-pk-hidden-" + uuid.NewString(), Name: "k"})
	account := mustCreateAccount(t, client, &service.Account{Name: "pk-hidden-" + uuid.NewString()})
	hidden, zero := 2.123456, 0.0
	for _, mode := range []string{"single", "batch", "best-effort"} {
		for _, multiplier := range []*float64{nil, &hidden, &zero} {
			log := &service.UsageLog{
				UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID,
				RequestID: uuid.NewString(), Model: "gpt-5", RequestedModel: "gpt-5",
				InputTokens: 100, OutputTokens: 20, InputCost: 0.5,
				TotalCost: 0.5, RateMultiplier: 0.25, HiddenRateMultiplier: multiplier, CreatedAt: time.Now().UTC(),
			}
			log.ActualCost = service.QuantizeUsageBillingAmount(0.125 * log.HiddenBillingMultiplier())
			var err error
			switch mode {
			case "single":
				_, err = repo.createSingle(ctx, integrationDB, log)
			case "batch":
				_, err = repo.Create(ctx, log)
			case "best-effort":
				err = repo.CreateBestEffort(ctx, log)
			}
			require.NoError(t, err)
			var id int64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT id FROM usage_logs WHERE request_id = $1 AND api_key_id = $2`, log.RequestID, key.ID).Scan(&id))
			got, err := repo.GetByID(ctx, id)
			require.NoError(t, err)
			require.Equal(t, multiplier, got.HiddenRateMultiplier, mode)
			require.Equal(t, log.ActualCost, got.ActualCost)
			require.Equal(t, 0.5, got.TotalCost)
			// Changing the current account must not rewrite a recorded price.
			_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra = jsonb_set(COALESCE(extra, '{}'::jsonb), '{hidden_rate_multiplier}', '9') WHERE id = $1`, account.ID)
			require.NoError(t, err)
			got, err = repo.GetByID(ctx, id)
			require.NoError(t, err)
			require.Equal(t, multiplier, got.HiddenRateMultiplier)
		}
	}
	start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	filters := usagestats.UsageLogFilters{UserID: user.ID, StartTime: &start, EndTime: &end}
	admin, err := repo.GetStatsWithFilters(ctx, filters)
	require.NoError(t, err)
	require.Equal(t, 4.5, admin.TotalCost)
	customerCtx := usagestats.WithCustomerBillingView(ctx)
	customer, err := repo.GetStatsWithFilters(customerCtx, filters)
	require.NoError(t, err)
	wantBase := 1.5 * (1 + hidden)
	require.InDelta(t, wantBase, customer.TotalCost, 1e-9)
	require.Equal(t, admin.TotalActualCost, customer.TotalActualCost)
	trend, err := repo.GetUsageTrendWithUsageFilters(customerCtx, start, end, "day", filters)
	require.NoError(t, err)
	require.Len(t, trend, 1)
	require.InDelta(t, wantBase, trend[0].Cost, 1e-9)
	models, err := repo.GetModelStatsWithUsageFiltersBySource(customerCtx, start, end, filters, usagestats.ModelSourceRequested)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.InDelta(t, wantBase, models[0].Cost, 1e-9)
	groups, err := repo.GetGroupStatsWithUsageFilters(customerCtx, start, end, filters)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.InDelta(t, wantBase, groups[0].Cost, 1e-9)
	dashboard, err := repo.GetUserDashboardStats(customerCtx, user.ID)
	require.NoError(t, err)
	require.InDelta(t, wantBase, dashboard.TotalCost, 1e-9)
	keyDashboard, err := repo.GetAPIKeyDashboardStats(customerCtx, key.ID)
	require.NoError(t, err)
	require.InDelta(t, wantBase, keyDashboard.TotalCost, 1e-9)
}

// TestUsageLog_SessionIDPersistence proves session_id round-trips from insert to
// read and is omitted (NULL) when absent.
func TestUsageLog_SessionIDPersistence(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{Email: "session-id-" + uuid.NewString() + "@example.com"})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-session-" + uuid.NewString(), Name: "k"})
	account := mustCreateAccount(t, client, &service.Account{Name: "acc-session-" + uuid.NewString()})

	sessionID := "sess-" + uuid.NewString()

	withSession := &service.UsageLog{
		UserID:       user.ID,
		APIKeyID:     apiKey.ID,
		AccountID:    account.ID,
		RequestID:    uuid.NewString(),
		Model:        "claude-3",
		InputTokens:  10,
		OutputTokens: 5,
		TotalCost:    1.0,
		ActualCost:   1.0,
		SessionID:    &sessionID,
		CreatedAt:    time.Now().UTC(),
	}
	_, err := repo.Create(ctx, withSession)
	require.NoError(t, err)
	require.NotZero(t, withSession.ID)

	withoutSession := &service.UsageLog{
		UserID:       user.ID,
		APIKeyID:     apiKey.ID,
		AccountID:    account.ID,
		RequestID:    uuid.NewString(),
		Model:        "claude-3",
		InputTokens:  7,
		OutputTokens: 3,
		TotalCost:    0.5,
		ActualCost:   0.5,
		CreatedAt:    time.Now().UTC(),
	}
	_, err = repo.Create(ctx, withoutSession)
	require.NoError(t, err)

	// Round-trip: session id survives insert → read.
	got, err := repo.GetByID(ctx, withSession.ID)
	require.NoError(t, err)
	require.NotNil(t, got.SessionID)
	require.Equal(t, sessionID, *got.SessionID)

	// Omission: absent session id reads back as nil (NULL), not empty string.
	gotNone, err := repo.GetByID(ctx, withoutSession.ID)
	require.NoError(t, err)
	require.Nil(t, gotNone.SessionID)
}

func TestPKUsageFirstResponsePersistence(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{Email: "pk-response-" + uuid.NewString() + "@example.com"})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-pk-response-" + uuid.NewString(), Name: "k"})
	account := mustCreateAccount(t, client, &service.Account{Name: "pk-response-" + uuid.NewString()})
	firstToken, firstResponse := 9876, 17
	for _, response := range []*int{&firstResponse, new(int), nil} {
		log := &service.UsageLog{
			UserID: user.ID, APIKeyID: apiKey.ID, AccountID: account.ID,
			RequestID: uuid.NewString(), Model: "gpt-5",
			FirstTokenMs: &firstToken, FirstResponseMs: response,
			InputTokens: 100, OutputTokens: 20, CacheReadTokens: 80, CacheCreationTokens: 5,
			TotalCost: 0.25, ActualCost: 0.5, CreatedAt: time.Now().UTC(),
		}
		inserted, err := repo.Create(ctx, log)
		require.NoError(t, err)
		require.True(t, inserted)
		got, err := repo.GetByID(ctx, log.ID)
		require.NoError(t, err)
		require.Equal(t, response, got.FirstResponseMs)
		require.Equal(t, &firstToken, got.FirstTokenMs)
		require.Equal(t, 100, got.InputTokens)
		require.Equal(t, 20, got.OutputTokens)
		require.Equal(t, 80, got.CacheReadTokens)
		require.Equal(t, 5, got.CacheCreationTokens)
		require.Equal(t, 0.25, got.TotalCost)
		require.Equal(t, 0.5, got.ActualCost)
	}
}

//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

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

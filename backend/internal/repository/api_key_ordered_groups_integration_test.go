//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyOrderedGroupsPersistenceFilteringAndMigration(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newAPIKeyRepositoryWithSQL(client, tx)
	user, err := client.User.Create().SetEmail("ordered-key@test.invalid").SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	first, err := client.Group.Create().SetName("ordered-first").SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	second, err := client.Group.Create().SetName("ordered-second").SetPlatform(service.PlatformAnthropic).SetRequirePrivacySet(true).SetRequireOauthOnly(true).Save(ctx)
	require.NoError(t, err)
	third, err := client.Group.Create().SetName("ordered-third").SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{UserID: user.ID, Key: "sk-ordered-integration", Name: "ordered", Status: service.StatusActive, GroupID: &second.ID, GroupIDs: []int64{second.ID, first.ID, third.ID}}
	require.NoError(t, repo.Create(ctx, key))
	loaded, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, key.GroupIDs, loaded.SelectedGroupIDs())
	require.Len(t, loaded.Groups, 3)
	require.Equal(t, service.PlatformAnthropic, loaded.Groups[0].Platform)
	require.True(t, loaded.Group.RequirePrivacySet)
	require.True(t, loaded.Group.RequireOAuthOnly)
	for _, id := range key.GroupIDs {
		listed, page, err := repo.ListByUserID(ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 20}, service.APIKeyListFilters{GroupID: &id})
		require.NoError(t, err)
		require.Equal(t, int64(1), page.Total)
		require.Len(t, listed, 1)
		count, err := repo.CountByGroupID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, int64(1), count)
		keys, err := repo.ListKeysByGroupID(ctx, id)
		require.NoError(t, err)
		require.Contains(t, keys, key.Key, "editing any candidate must invalidate this key")
	}
	changed, err := repo.UpdateGroupIDByUserAndGroup(ctx, user.ID, first.ID, third.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), changed)
	loaded, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{second.ID, third.ID}, loaded.SelectedGroupIDs(), "subscription migration replaces in place and deduplicates")
	changed, err = repo.ClearGroupIDByGroupID(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), changed)
	loaded, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{third.ID}, loaded.SelectedGroupIDs())
	require.Equal(t, third.ID, *loaded.GroupID)
	loaded.GroupID, loaded.GroupIDs = nil, []int64{}
	require.NoError(t, repo.Update(ctx, loaded, service.APIKeyUpdateFields{GroupID: true}))
	loaded, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.GroupIDs)
	require.Empty(t, loaded.GroupIDs)
	require.Nil(t, loaded.GroupID)
}

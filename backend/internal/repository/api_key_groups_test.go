package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyGroupsPersistenceMembershipAndAtomicRollback(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "multi-group@test.invalid")
	first, err := client.Group.Create().SetName("first").Save(ctx)
	require.NoError(t, err)
	second, err := client.Group.Create().SetName("second").Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{UserID: user.ID, Key: "sk-multi-group-test", Name: "original", Status: service.StatusActive, GroupID: &first.ID, GroupIDs: []int64{first.ID, second.ID}}
	require.NoError(t, repo.Create(ctx, key))
	got, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{first.ID, second.ID}, got.GroupIDs)
	page, _, err := repo.ListByUserID(ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 10}, service.APIKeyListFilters{GroupID: &second.ID})
	require.NoError(t, err)
	require.Len(t, page, 1)
	keys, err := repo.ListKeysByGroupID(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, []string{key.Key}, keys)

	// A failing FK must roll back the primary-key edit and its peer bindings.
	got.Name, got.GroupIDs = "must-rollback", []int64{first.ID, 999999999}
	err = repo.Update(ctx, got, service.APIKeyUpdateFields{Name: true, GroupID: true})
	require.Error(t, err)
	unchanged, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, "original", unchanged.Name)
	require.Equal(t, []int64{first.ID, second.ID}, unchanged.GroupIDs)

	unchanged.GroupID, unchanged.GroupIDs = &second.ID, []int64{second.ID}
	require.NoError(t, repo.Update(ctx, unchanged, service.APIKeyUpdateFields{GroupID: true}))
	got, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{second.ID}, got.GroupIDs)
}

func TestAPIKeyGroupsOmitLegacySoftDeletedAdditionalBinding(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "deleted-group@test.invalid")
	primary, err := client.Group.Create().SetName("primary").Save(ctx)
	require.NoError(t, err)
	deleted, err := client.Group.Create().SetName("deleted-extra").Save(ctx)
	require.NoError(t, err)
	inactive, err := client.Group.Create().SetName("inactive-extra").SetStatus(service.StatusDisabled).Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{UserID: user.ID, Key: "sk-deleted-extra", Name: "original", Status: service.StatusActive, GroupID: &primary.ID, GroupIDs: []int64{primary.ID, deleted.ID, inactive.ID}}
	require.NoError(t, repo.Create(ctx, key))
	// Leave the old join row in place, as pre-fix group deletion did.
	require.NoError(t, client.Group.DeleteOneID(deleted.ID).Exec(ctx))
	got, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{primary.ID, inactive.ID}, got.GroupIDs)
	page, _, err := repo.ListByUserID(ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 10}, service.APIKeyListFilters{})
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, got.GroupIDs, page[0].GroupIDs)
	got.Name = "saved-after-deletion"
	require.NoError(t, repo.Update(ctx, got, service.APIKeyUpdateFields{Name: true, GroupID: true}))
	got, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, "saved-after-deletion", got.Name)
	require.Equal(t, []int64{primary.ID, inactive.ID}, got.GroupIDs)
}

func TestAPIKeyGroupsHydrateUltrafastColumn(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "ultrafast@test.invalid")
	primary, err := client.Group.Create().SetName("ultrafast-primary").SetPlatform(service.PlatformOpenAI).
		SetForceOpenaiUltrafast(true).Save(ctx)
	require.NoError(t, err)
	secondary, err := client.Group.Create().SetName("ultrafast-secondary").SetPlatform(service.PlatformComposite).
		SetForceOpenaiUltrafast(true).Save(ctx)
	require.NoError(t, err)
	legacy, err := client.Group.Create().SetName("ultrafast-legacy").SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{UserID: user.ID, Key: "sk-ultrafast-groups", Name: "ultrafast", Status: service.StatusActive,
		GroupID: &primary.ID, GroupIDs: []int64{primary.ID, secondary.ID, legacy.ID}}
	require.NoError(t, repo.Create(ctx, key))
	for _, lookup := range []struct {
		name string
		get  func() (*service.APIKey, error)
	}{
		{name: "auth", get: func() (*service.APIKey, error) { return repo.GetByKeyForAuth(ctx, key.Key) }},
		{name: "key", get: func() (*service.APIKey, error) { return repo.GetByKey(ctx, key.Key) }},
		{name: "id", get: func() (*service.APIKey, error) { return repo.GetByID(ctx, key.ID) }},
	} {
		t.Run(lookup.name, func(t *testing.T) {
			got, err := lookup.get()
			require.NoError(t, err)
			require.True(t, got.Group.ForceOpenAIUltrafast)
			require.Len(t, got.Groups, 3)
			require.True(t, got.Groups[0].ForceOpenAIUltrafast)
			require.True(t, got.Groups[1].ForceOpenAIUltrafast)
			require.False(t, got.Groups[2].ForceOpenAIUltrafast)
		})
	}
	page, _, err := repo.ListByUserID(ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 10}, service.APIKeyListFilters{})
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.True(t, page[0].Group.ForceOpenAIUltrafast)
	require.True(t, page[0].Groups[1].ForceOpenAIUltrafast)
}

func TestGroupUltrafastColumnCreateReadAndDisable(t *testing.T) {
	_, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	repo := newGroupRepositoryWithSQL(client, nil)
	group := &service.Group{Name: "ultrafast", Platform: service.PlatformOpenAI,
		Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard,
		RateMultiplier: 1, ForceOpenAIFast: true, ForceOpenAIUltrafast: true}
	require.NoError(t, repo.Create(ctx, group))
	stored, err := client.Group.Get(ctx, group.ID)
	require.NoError(t, err)
	require.True(t, stored.ForceOpenaiUltrafast)
	got, err := repo.GetByIDLite(ctx, group.ID)
	require.NoError(t, err)
	require.True(t, got.ForceOpenAIUltrafast)
	got.ForceOpenAIUltrafast = false
	require.NoError(t, repo.Update(ctx, got))
	stored, err = client.Group.Get(ctx, group.ID)
	require.NoError(t, err)
	require.False(t, stored.ForceOpenaiUltrafast)
	require.True(t, stored.ForceOpenaiFast)
}

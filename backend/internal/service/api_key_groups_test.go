package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type orderedKeyGroupRepo struct {
	GroupRepository
	groups  map[int64]*Group
	visited []int64
}

func (r *orderedKeyGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	r.visited = append(r.visited, id)
	if group := r.groups[id]; group != nil {
		return group, nil
	}
	return nil, ErrGroupNotFound
}

type orderedKeyUserRepo struct {
	UserRepository
	user *User
}

func (r orderedKeyUserRepo) GetByID(context.Context, int64) (*User, error) { return r.user, nil }

type orderedKeyRepo struct {
	APIKeyRepository
	key    *APIKey
	fields APIKeyUpdateFields
}

func (r *orderedKeyRepo) GetByID(context.Context, int64) (*APIKey, error) {
	key := *r.key
	return &key, nil
}
func (r *orderedKeyRepo) Update(_ context.Context, key *APIKey, fields APIKeyUpdateFields) error {
	r.key = key
	r.fields = fields
	return nil
}

func TestAPIKeyOrderedGroupsNormalizeAndLegacy(t *testing.T) {
	ids, err := NormalizeAPIKeyGroupIDs([]int64{3, 1, 3, 2})
	require.NoError(t, err)
	require.Equal(t, []int64{3, 1, 2}, ids)
	for _, invalid := range [][]int64{{0}, {-1}, make([]int64, 21)} {
		_, err = NormalizeAPIKeyGroupIDs(invalid)
		require.Error(t, err)
	}
	primary := int64(8)
	key := &APIKey{GroupID: &primary}
	require.Equal(t, []int64{8}, key.SelectedGroupIDs())
	key.GroupIDs = []int64{}
	require.Empty(t, key.SelectedGroupIDs(), "explicit empty must never inherit stale primary")
	key.GroupIDs = []int64{3, 1}
	copy := key.SelectedGroupIDs()
	copy[0] = 99
	require.Equal(t, []int64{3, 1}, key.SelectedGroupIDs())
	_, err = requestedAPIKeyGroups(&primary, []int64{3, 8})
	require.Error(t, err, "conflicting legacy primary cannot silently override priority")
}

func TestAPIKeyOrderedGroupsUpdatePreservesOmissionClearsAndChecksEveryGroup(t *testing.T) {
	ctx := context.Background()
	primary := int64(3)
	repo := &orderedKeyRepo{key: &APIKey{ID: 1, UserID: 7, Key: "sk-ordered", GroupID: &primary, GroupIDs: []int64{3, 1}, QuotaUsed: 29, Usage5h: 4}}
	groupRepo := &orderedKeyGroupRepo{groups: map[int64]*Group{
		1: {ID: 1, Status: StatusActive}, 3: {ID: 3, Status: StatusActive},
		5: {ID: 5, Status: StatusActive, IsExclusive: true}, 6: {ID: 6, Status: StatusDisabled},
	}}
	svc := &APIKeyService{apiKeyRepo: repo, userRepo: orderedKeyUserRepo{user: &User{ID: 7}}, groupRepo: groupRepo}
	name := "rename"
	got, err := svc.Update(ctx, 1, 7, UpdateAPIKeyRequest{Name: &name})
	require.NoError(t, err)
	require.Equal(t, []int64{3, 1}, got.SelectedGroupIDs())
	require.False(t, repo.fields.GroupID)
	ids := []int64{1, 3, 1}
	got, err = svc.Update(ctx, 1, 7, UpdateAPIKeyRequest{GroupIDs: &ids})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3}, got.SelectedGroupIDs())
	require.Equal(t, int64(1), *got.GroupID)
	require.Equal(t, []int64{1, 3}, groupRepo.visited)
	require.Equal(t, APIKeyUpdateFields{GroupID: true}, repo.fields)
	require.Equal(t, float64(29), got.QuotaUsed)
	require.Equal(t, float64(4), got.Usage5h)
	for _, forbidden := range [][]int64{{1, 5}, {1, 6}, {1, 999}} {
		_, err = svc.Update(ctx, 1, 7, UpdateAPIKeyRequest{GroupIDs: &forbidden})
		require.Error(t, err)
		require.Equal(t, []int64{1, 3}, repo.key.SelectedGroupIDs(), "failed validation must not partially commit groups")
	}
	empty := []int64{}
	got, err = svc.Update(ctx, 1, 7, UpdateAPIKeyRequest{GroupIDs: &empty})
	require.NoError(t, err)
	require.Nil(t, got.GroupID)
	require.NotNil(t, got.GroupIDs)
	require.Empty(t, got.Groups)
}

func TestAPIKeyOrderedGroupsProtectManagedKeys(t *testing.T) {
	id := int64(3)
	repo := &orderedKeyRepo{key: &APIKey{ID: 1, UserID: 7, Key: "sk-iq-managed", GroupID: &id}}
	svc := &APIKeyService{apiKeyRepo: repo}
	for _, ids := range [][]int64{{3, 1}, {}, {1}} {
		_, err := svc.Update(context.Background(), 1, 7, UpdateAPIKeyRequest{GroupIDs: &ids})
		require.ErrorContains(t, err, "智商测试专用 Key")
	}
}

func TestAPIKeyOrderedGroupsAuthCacheRoundTrip(t *testing.T) {
	primary := int64(3)
	key := &APIKey{ID: 9, UserID: 7, User: &User{ID: 7}, GroupID: &primary, GroupIDs: []int64{3, 1},
		Group:  &Group{ID: 3, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 2},
		Groups: []*Group{{ID: 3, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 2}, {ID: 1, Platform: PlatformAnthropic, Status: StatusActive, RateMultiplier: 4, RPMLimit: 8, RequirePrivacySet: true, RequireOAuthOnly: true}}}
	svc := &APIKeyService{}
	snapshot := svc.snapshotFromAPIKey(context.Background(), key)
	require.Equal(t, 26, snapshot.Version)
	got := svc.snapshotToAPIKey("sk-test", snapshot)
	require.Equal(t, []int64{3, 1}, got.SelectedGroupIDs())
	require.Len(t, got.Groups, 2)
	require.Equal(t, PlatformAnthropic, got.Groups[1].Platform)
	require.Equal(t, float64(4), got.Groups[1].RateMultiplier)
	require.Equal(t, 8, got.Groups[1].RPMLimit)
	require.True(t, got.Groups[1].RequirePrivacySet)
	require.True(t, got.Groups[1].RequireOAuthOnly)
	got.GroupIDs[0] = 99
	require.Equal(t, []int64{3, 1}, snapshot.GroupIDs)
}

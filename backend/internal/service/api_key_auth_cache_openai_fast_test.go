package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthSnapshotGroupForceOpenAIFastRoundtrip(t *testing.T) {
	groupID := int64(50)
	apiKey := &APIKey{
		ID: 82, UserID: 40, GroupID: &groupID, Key: "sk-fast-roundtrip", Status: StatusActive,
		User: &User{ID: 40, Status: StatusActive},
		Group: &Group{
			ID: groupID, Name: "fast-roundtrip", Platform: PlatformOpenAI, Status: StatusActive,
			Hydrated: true, ForceOpenAIFast: true, ForceOpenAIUltrafast: true, FreeOpenAIFast: true, FreeOpenAIUltrafast: true,
		},
	}
	svc := &APIKeyService{}

	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), apiKey)})
	require.NoError(t, err)
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))

	materialized, used, err := svc.applyAuthCacheEntry(apiKey.Key, &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.NotNil(t, materialized.Group)
	require.True(t, materialized.Group.Hydrated)
	require.True(t, materialized.Group.ForceOpenAIFast)
	require.True(t, materialized.Group.ForceOpenAIUltrafast)
	require.True(t, materialized.Group.FreeOpenAIFast)
	require.True(t, materialized.Group.FreeOpenAIUltrafast)
	require.Equal(t, apiKeyAuthSnapshotVersion, cached.Snapshot.Version)
}

func TestAPIKeyAuthSnapshotUltrafastCandidateGroupsAndLegacyDefault(t *testing.T) {
	primaryID := int64(51)
	primary := &Group{ID: primaryID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIUltrafast: true}
	secondary := &Group{ID: 52, Platform: PlatformComposite, Status: StatusActive, Hydrated: true, FreeOpenAIUltrafast: true}
	apiKey := &APIKey{
		ID: 83, UserID: 41, GroupID: &primaryID, GroupIDs: []int64{primaryID, secondary.ID},
		Key: "sk-ultrafast-candidates", Status: StatusActive, User: &User{ID: 41, Status: StatusActive},
		Group: primary, Groups: []*Group{primary, secondary},
	}
	svc := &APIKeyService{}
	entry := &APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), apiKey)}
	payload, err := json.Marshal(entry)
	require.NoError(t, err)
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))
	materialized, used, err := svc.applyAuthCacheEntry(apiKey.Key, &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.Len(t, materialized.Groups, 2)
	require.True(t, materialized.Groups[0].ForceOpenAIUltrafast)
	require.False(t, materialized.Groups[1].ForceOpenAIUltrafast)
	require.False(t, materialized.Groups[0].FreeOpenAIUltrafast)
	require.True(t, materialized.Groups[1].FreeOpenAIUltrafast)
	require.False(t, materialized.Groups[1].FreeOpenAIFast)

	// Missing fields decode as false; older cache versions must reload
	// the database rather than silently using that default for an enabled group.
	var legacy APIKeyAuthGroupSnapshot
	require.NoError(t, json.Unmarshal([]byte(`{"id":51,"platform":"openai","force_openai_fast":true}`), &legacy))
	require.False(t, legacy.ForceOpenAIUltrafast)
	require.False(t, legacy.FreeOpenAIUltrafast)
	require.True(t, legacy.ForceOpenAIFast)
	require.False(t, authSnapshotToGroup(&legacy).ForceOpenAIUltrafast)
	require.False(t, authSnapshotToGroup(&legacy).FreeOpenAIUltrafast)
	legacyEntry := &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{Version: 35, Group: &legacy}}
	materialized, used, err = svc.applyAuthCacheEntry(apiKey.Key, legacyEntry)
	require.NoError(t, err)
	require.False(t, used)
	require.Nil(t, materialized)
}

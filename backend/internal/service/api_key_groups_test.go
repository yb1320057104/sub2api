package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

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

type antiDistillationSettings struct {
	SettingRepository
	mu     sync.Mutex
	values map[string]string
	err    error
}

func TestAPIKeyGroupAntiDistillationUsesEffectiveGroupAndUser(t *testing.T) {
	protection, cache := antiDistillationTestService(t)
	protection.users = &antiDistillationUsers{users: map[int64]*User{7: {ID: 7}}}
	ctx := context.Background()
	require.NoError(t, protection.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5}))
	cache.remaining[7] = time.Minute
	svc := &APIKeyService{groupAntiDistillation: protection}
	for _, tc := range []struct {
		name string
		key  *APIKey
		want time.Duration
	}{
		{"enabled effective group", &APIKey{UserID: 7, Group: &Group{ID: 1, Platform: PlatformOpenAI}}, time.Minute},
		{"another user", &APIKey{UserID: 8, Group: &Group{ID: 1, Platform: PlatformOpenAI}}, 0},
		{"disabled effective group", &APIKey{UserID: 7, Group: &Group{ID: 2, Platform: PlatformOpenAI}}, 0},
		{"other platform", &APIKey{UserID: 7, Group: &Group{ID: 3, Platform: PlatformAnthropic}}, 0},
		{"missing group", &APIKey{UserID: 7}, 0},
		{"missing key", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remaining, err := svc.CheckGroupAntiDistillation(ctx, tc.key)
			require.NoError(t, err)
			require.Equal(t, tc.want, remaining)
		})
	}
	require.NoError(t, protection.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5, UserAllowlistIDs: []int64{7}}))
	require.NoError(t, protection.SaveConfig(ctx, 2, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5}))
	cache.remaining[8] = 2 * time.Minute
	primary := int64(2)
	for _, tc := range []struct {
		name string
		key  *APIKey
		want time.Duration
	}{
		{"allowlisted effective group", &APIKey{ID: 101, UserID: 7, GroupID: &primary, GroupIDs: []int64{2, 1}, Group: &Group{ID: 1, Platform: PlatformOpenAI}}, 0},
		{"same user another key", &APIKey{ID: 102, UserID: 7, Group: &Group{ID: 1, Platform: PlatformOpenAI}}, 0},
		{"same user another enabled group", &APIKey{ID: 101, UserID: 7, Group: &Group{ID: 2, Platform: PlatformOpenAI}}, time.Minute},
		{"same group another paused user", &APIKey{ID: 103, UserID: 8, Group: &Group{ID: 1, Platform: PlatformOpenAI}}, 2 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remaining, err := svc.CheckGroupAntiDistillation(ctx, tc.key)
			require.NoError(t, err)
			require.Equal(t, tc.want, remaining)
		})
	}
}

func (r *antiDistillationSettings) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return "", r.err
	}
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *antiDistillationSettings) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.values[key] = value
	return nil
}

func (r *antiDistillationSettings) CompareAndSwap(_ context.Context, key string, expected *string, value string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return false, r.err
	}
	current, exists := r.values[key]
	if expected == nil && exists || expected != nil && (!exists || current != *expected) {
		return false, nil
	}
	r.values[key] = value
	return true, nil
}

type antiDistillationGroups struct {
	GroupRepository
	groups map[int64]*Group
}

type antiDistillationUsers struct {
	UserRepository
	users map[int64]*User
	err   error
	calls []int64
}

func (r *antiDistillationUsers) GetByID(_ context.Context, id int64) (*User, error) {
	r.calls = append(r.calls, id)
	if r.err != nil {
		return nil, r.err
	}
	if user := r.users[id]; user != nil {
		return user, nil
	}
	return nil, ErrUserNotFound
}

func (r *antiDistillationGroups) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	if group := r.groups[id]; group != nil {
		return group, nil
	}
	return nil, ErrGroupNotFound
}

type antiDistillationObservation struct {
	userID    int64
	requestID string
	zero      bool
	pause     time.Duration
	startedAt time.Time
}

// The service fake records policy delegation. Atomic window/TTL/deduplication
// behavior is exercised against the real cache adapter in repository tests.
type antiDistillationCacheFake struct {
	mu           sync.Mutex
	observations []antiDistillationObservation
	remaining    map[int64]time.Duration
	checks       []int64
	err          error
}

func (f *antiDistillationCacheFake) PauseRemaining(_ context.Context, userID int64) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks = append(f.checks, userID)
	if f.err != nil {
		return 0, f.err
	}
	return f.remaining[userID], nil
}

func (f *antiDistillationCacheFake) Observe(_ context.Context, userID int64, requestID string, zero bool, pause time.Duration, startedAt time.Time) (bool, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, time.Time{}, f.err
	}
	f.observations = append(f.observations, antiDistillationObservation{
		userID: userID, requestID: requestID, zero: zero, pause: pause, startedAt: startedAt,
	})
	return false, time.Time{}, nil
}

func (f *antiDistillationCacheFake) Observations() []antiDistillationObservation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]antiDistillationObservation(nil), f.observations...)
}

func (f *antiDistillationCacheFake) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.observations))
	for _, observation := range f.observations {
		keys = append(keys, observation.requestID)
	}
	return keys
}

func antiDistillationTestService(t *testing.T) (*GroupAntiDistillationService, *antiDistillationCacheFake) {
	t.Helper()
	cache := &antiDistillationCacheFake{remaining: make(map[int64]time.Duration)}
	settings := &antiDistillationSettings{values: make(map[string]string)}
	groups := &antiDistillationGroups{groups: map[int64]*Group{
		1: {ID: 1, Platform: PlatformOpenAI},
		2: {ID: 2, Platform: PlatformOpenAI},
		3: {ID: 3, Platform: PlatformAnthropic},
	}}
	return NewGroupAntiDistillationService(settings, groups, cache, nil, nil), cache
}

func antiDistillationTestSettings(t *testing.T, svc *GroupAntiDistillationService) *antiDistillationSettings {
	t.Helper()
	settings, ok := svc.settings.(*antiDistillationSettings)
	require.True(t, ok, "test service must use the settings fake")
	return settings
}

func TestGroupAntiDistillationConfigurationDefaultsAndValidation(t *testing.T) {
	svc, _ := antiDistillationTestService(t)
	ctx := context.Background()
	cfg, err := svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, DefaultGroupAntiDistillationConfig(), *cfg)
	require.NoError(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5}))
	cfg, err = svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 5, cfg.PauseMinutes)
	require.True(t, cfg.Enabled)

	for _, minutes := range []int{-1, 0, 1441} {
		require.Error(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{PauseMinutes: minutes}))
	}
	for _, minutes := range []int{1, 1440} {
		require.NoError(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{PauseMinutes: minutes}))
	}
	require.Error(t, svc.SaveConfig(ctx, 3, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5}))
	require.ErrorIs(t, svc.SaveConfig(ctx, 404, GroupAntiDistillationConfig{PauseMinutes: 5}), ErrGroupNotFound)

	// Returned values cannot mutate a cached setting without saving.
	cfg, err = svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	cfg.Enabled = true
	stored, err := svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	require.False(t, stored.Enabled)
}

func TestGroupAntiDistillationAllowlistValidationAndNormalization(t *testing.T) {
	svc, _ := antiDistillationTestService(t)
	ctx := context.Background()
	deletedAt := time.Now()
	users := &antiDistillationUsers{users: map[int64]*User{7: {ID: 7}, 8: {ID: 8}, 9: {ID: 9, DeletedAt: &deletedAt}, 10: {ID: 11}}}
	svc.users = users
	input := []int64{8, 7, 8}
	saved, err := svc.SaveConfigWithResult(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5, UserAllowlistIDs: input})
	require.NoError(t, err)
	require.Equal(t, []int64{7, 8}, saved.UserAllowlistIDs)
	require.Equal(t, []int64{8, 7, 8}, input, "normalization must not mutate caller slices")
	require.Equal(t, []int64{7, 8}, users.calls, "validate each distinct ID once")
	settings := antiDistillationTestSettings(t, svc)
	before, err := settings.GetValue(ctx, groupAntiDistillationSettingKey(1))
	require.NoError(t, err)
	tooMany := make([]int64, 501)
	for i := range tooMany {
		tooMany[i] = 7
	}
	for _, ids := range [][]int64{{0}, {-1}, {404}, {9}, {10}, tooMany} {
		require.Error(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{PauseMinutes: 5, UserAllowlistIDs: ids}))
		after, err := settings.GetValue(ctx, groupAntiDistillationSettingKey(1))
		require.NoError(t, err)
		require.Equal(t, before, after, "invalid input must never partially persist")
	}
	users.err = errors.New("user lookup offline")
	require.ErrorIs(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{PauseMinutes: 5, UserAllowlistIDs: []int64{7}}), users.err)
	svc.users = nil
	require.Error(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{PauseMinutes: 5, UserAllowlistIDs: []int64{7}}))
	after, err := settings.GetValue(ctx, groupAntiDistillationSettingKey(1))
	require.NoError(t, err)
	require.Equal(t, before, after)
	users.err = nil
	svc.users = users
	boundary := make([]int64, 500)
	for i := range boundary {
		boundary[i] = int64(i + 1)
		users.users[int64(i+1)] = &User{ID: int64(i + 1)}
	}
	require.NoError(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{PauseMinutes: 5, UserAllowlistIDs: boundary}))
}

func TestGroupAntiDistillationAllowlistOmissionAndCrossNodeRefresh(t *testing.T) {
	svc, _ := antiDistillationTestService(t)
	ctx := context.Background()
	settings := antiDistillationTestSettings(t, svc)
	require.NoError(t, settings.Set(ctx, groupAntiDistillationSettingKey(1), `{"enabled":true,"pause_minutes":5}`))
	legacy, err := svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []int64{}, legacy.UserAllowlistIDs)
	node := NewGroupAntiDistillationService(settings, svc.groups, svc.cache, &antiDistillationUsers{users: map[int64]*User{7: {ID: 7}, 8: {ID: 8}}}, nil)
	require.NoError(t, node.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5, UserAllowlistIDs: []int64{7}}))
	cached, err := svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, cached.UserAllowlistIDs, "other nodes retain the existing short TTL")
	preserved, err := svc.SaveConfigWithResult(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 9})
	require.NoError(t, err)
	require.Equal(t, []int64{7}, preserved.UserAllowlistIDs, "omission reads current storage instead of stale local cache")
	cached, err = svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, cached.UserAllowlistIDs)
	require.NoError(t, node.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 9, UserAllowlistIDs: []int64{8}}))
	svc.mu.Lock()
	entry := svc.configs[1]
	entry.expiresAt = time.Now().Add(-time.Second)
	svc.configs[1] = entry
	svc.mu.Unlock()
	refreshed, err := svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []int64{8}, refreshed.UserAllowlistIDs)
	cleared, err := svc.SaveConfigWithResult(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 9, UserAllowlistIDs: []int64{}})
	require.NoError(t, err)
	require.Equal(t, []int64{}, cleared.UserAllowlistIDs)
	raw, err := settings.GetValue(ctx, groupAntiDistillationSettingKey(1))
	require.NoError(t, err)
	require.Contains(t, raw, `"user_allowlist_ids":[]`)
}

type antiDistillationInterceptedCASSettings struct {
	*antiDistillationSettings
	beforeCompare func(attempt int, expected *string)
	compareCalls  int
	compareErr    error
}

func (s *antiDistillationInterceptedCASSettings) CompareAndSwap(ctx context.Context, key string, expected *string, value string) (bool, error) {
	s.compareCalls++
	if s.beforeCompare != nil {
		s.beforeCompare(s.compareCalls, expected)
	}
	if s.compareErr != nil {
		return false, s.compareErr
	}
	return s.antiDistillationSettings.CompareAndSwap(ctx, key, expected, value)
}

func TestGroupAntiDistillationAllowlistOmissionRetriesConcurrentUpdates(t *testing.T) {
	for _, tc := range []struct {
		name        string
		initial     string
		concurrent  []int64
		firstAbsent bool
	}{
		{"concurrent clear is not restored", `{"enabled":true,"pause_minutes":5,"user_allowlist_ids":[7]}`, []int64{}, false},
		{"concurrent creation is preserved", "", []int64{7}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := antiDistillationTestService(t)
			ctx := context.Background()
			settings := antiDistillationTestSettings(t, svc)
			if tc.initial != "" {
				require.NoError(t, settings.Set(ctx, groupAntiDistillationSettingKey(1), tc.initial))
			}
			node := NewGroupAntiDistillationService(settings, svc.groups, svc.cache, &antiDistillationUsers{users: map[int64]*User{7: {ID: 7}}}, nil)
			interleaved := &antiDistillationInterceptedCASSettings{antiDistillationSettings: settings}
			interleaved.beforeCompare = func(attempt int, expected *string) {
				if attempt == 1 {
					require.Equal(t, tc.firstAbsent, expected == nil)
					require.NoError(t, node.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: false, PauseMinutes: 30, UserAllowlistIDs: tc.concurrent}))
				} else {
					require.NotNil(t, expected)
				}
			}
			svc.settings = interleaved
			saved, err := svc.SaveConfigWithResult(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 9})
			require.NoError(t, err)
			require.Equal(t, 2, interleaved.compareCalls, "the stale write must fail before a fresh storage read")
			require.Equal(t, tc.concurrent, saved.UserAllowlistIDs)
			require.True(t, saved.Enabled)
			require.Equal(t, 9, saved.PauseMinutes)
			current, err := svc.GetConfig(ctx, 1)
			require.NoError(t, err)
			require.Equal(t, *saved, *current)
		})
	}
}

func TestGroupAntiDistillationAllowlistOmissionFailsSafely(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"missing atomic writer", "continuous conflicts", "atomic write error"} {
		t.Run(mode, func(t *testing.T) {
			svc, _ := antiDistillationTestService(t)
			settings := antiDistillationTestSettings(t, svc)
			key := groupAntiDistillationSettingKey(1)
			lastConcurrent := `{"enabled":false,"pause_minutes":5,"user_allowlist_ids":[8]}`
			require.NoError(t, settings.Set(ctx, key, lastConcurrent))
			interleaved := &antiDistillationInterceptedCASSettings{antiDistillationSettings: settings}
			svc.settings = interleaved
			switch mode {
			case "missing atomic writer":
				svc.settings = struct{ SettingRepository }{settings}
			case "continuous conflicts":
				interleaved.beforeCompare = func(attempt int, _ *string) {
					lastConcurrent = fmt.Sprintf(`{"enabled":false,"pause_minutes":%d,"user_allowlist_ids":[8]}`, 20+attempt)
					require.NoError(t, settings.Set(ctx, key, lastConcurrent))
				}
			case "atomic write error":
				interleaved.compareErr = errors.New("atomic writer offline")
			}
			saved, err := svc.SaveConfigWithResult(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 9})
			require.Error(t, err)
			require.Nil(t, saved)
			if mode == "continuous conflicts" {
				require.Equal(t, 3, interleaved.compareCalls, "retry budget must be bounded")
			}
			if interleaved.compareErr != nil {
				require.ErrorIs(t, err, interleaved.compareErr)
			}
			raw, err := settings.GetValue(ctx, key)
			require.NoError(t, err)
			require.Equal(t, lastConcurrent, raw, "failed atomic save must never fall back to unconditional Set")
			require.Zero(t, svc.configEpoch, "failed saves do not invalidate or publish a new config")
		})
	}
}

func TestGroupAntiDistillationAllowlistPreservesSharedStateAndAvoidsHotPathUserLookup(t *testing.T) {
	svc, cache := antiDistillationTestService(t)
	ctx := context.Background()
	users := &antiDistillationUsers{users: map[int64]*User{7: {ID: 7}}}
	svc.users = users
	require.NoError(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5, UserAllowlistIDs: []int64{7}}))
	require.NoError(t, svc.SaveConfig(ctx, 2, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 10}))
	cache.remaining[7] = 10 * time.Minute
	users.calls = nil
	for _, keyID := range []int64{101, 102} {
		remaining, err := svc.Check(ctx, 7, 1)
		require.NoError(t, err)
		require.Zero(t, remaining)
		svc.Observe(ctx, &APIKey{ID: keyID, UserID: 7, Group: &Group{ID: 1, Platform: PlatformOpenAI}}, fmt.Sprint(keyID), 0)
	}
	require.Empty(t, cache.checks, "allowlisted admission never reads or changes shared pause")
	require.Empty(t, cache.Observations(), "allowlisted requests never contribute evidence")
	remaining, err := svc.Check(ctx, 7, 2)
	require.NoError(t, err)
	require.Equal(t, 10*time.Minute, remaining)
	svc.Observe(ctx, &APIKey{UserID: 7, Group: &Group{ID: 2, Platform: PlatformOpenAI}}, "other-group", 0)
	svc.Observe(ctx, &APIKey{UserID: 8, Group: &Group{ID: 1, Platform: PlatformOpenAI}}, "other-user", 0)
	require.Equal(t, []string{"other-group", "other-user"}, cache.Keys())
	require.Empty(t, users.calls, "gateway admission and evidence must not query users")
	require.NoError(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5, UserAllowlistIDs: []int64{}}))
	remaining, err = svc.Check(ctx, 7, 1)
	require.NoError(t, err)
	require.Equal(t, 10*time.Minute, remaining, "removing exemption restores enforcement of the original pause")
	require.Equal(t, []string{"other-group", "other-user"}, cache.Keys(), "saving cannot clear the cross-group evidence window")
}

type antiDistillationOverwrittenSettings struct{ *antiDistillationSettings }

func (s antiDistillationOverwrittenSettings) Set(ctx context.Context, key, value string) error {
	if err := s.antiDistillationSettings.Set(ctx, key, value); err != nil {
		return err
	}
	return s.antiDistillationSettings.Set(ctx, key, `{"enabled":false,"pause_minutes":30,"user_allowlist_ids":[8]}`)
}

func TestGroupAntiDistillationSaveReturnsOwnNormalizedValue(t *testing.T) {
	svc, _ := antiDistillationTestService(t)
	svc.settings = antiDistillationOverwrittenSettings{antiDistillationTestSettings(t, svc)}
	svc.users = &antiDistillationUsers{users: map[int64]*User{7: {ID: 7}}}
	saved, err := svc.SaveConfigWithResult(context.Background(), 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5, UserAllowlistIDs: []int64{7, 7}})
	require.NoError(t, err)
	require.True(t, saved.Enabled)
	require.Equal(t, 5, saved.PauseMinutes)
	require.Equal(t, []int64{7}, saved.UserAllowlistIDs)
	current, err := svc.GetConfig(context.Background(), 1)
	require.NoError(t, err)
	require.False(t, current.Enabled)
	require.Equal(t, []int64{8}, current.UserAllowlistIDs, "a later writer cannot change this save's response")
}

func TestGroupAntiDistillationAllowlistConfigCopiesAreIndependent(t *testing.T) {
	svc, _ := antiDistillationTestService(t)
	ctx := context.Background()
	settings := antiDistillationTestSettings(t, svc)
	raw, err := json.Marshal(GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5, UserAllowlistIDs: []int64{7, 8}})
	require.NoError(t, err)
	require.NoError(t, settings.Set(ctx, groupAntiDistillationSettingKey(1), string(raw)))
	blocked := &antiDistillationBlockedSettings{antiDistillationSettings: settings, started: make(chan struct{}), release: make(chan struct{})}
	svc.settings = blocked
	type result struct {
		config *GroupAntiDistillationConfig
		err    error
	}
	results := make(chan result, 32)
	for i := 0; i < cap(results); i++ {
		go func() {
			cfg, err := svc.GetConfig(ctx, 1)
			results <- result{cfg, err}
		}()
	}
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("shared config load did not start")
	}
	close(blocked.release)
	configs := make([]*GroupAntiDistillationConfig, 0, cap(results))
	for i := 0; i < cap(results); i++ {
		select {
		case result := <-results:
			require.NoError(t, result.err)
			configs = append(configs, result.config)
		case <-time.After(time.Second):
			t.Fatal("config waiter did not finish")
		}
	}
	for _, cfg := range configs {
		require.Equal(t, []int64{7, 8}, cfg.UserAllowlistIDs)
		cfg.UserAllowlistIDs[0] = 99
	}
	cached, err := svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []int64{7, 8}, cached.UserAllowlistIDs)
	cached.UserAllowlistIDs[0] = 100
	cached, err = svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []int64{7, 8}, cached.UserAllowlistIDs)
	input := GroupAntiDistillationConfig{PauseMinutes: 5, UserAllowlistIDs: []int64{10}}
	svc.mu.Lock()
	svc.cacheConfigLocked(2, input, time.Now())
	svc.mu.Unlock()
	input.UserAllowlistIDs[0] = 99
	cached, err = svc.GetConfig(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, []int64{10}, cached.UserAllowlistIDs, "cache insertion owns its slice")
}

func TestGroupAntiDistillationDelegatesUserScopeAcrossKeysAndGroups(t *testing.T) {
	svc, cache := antiDistillationTestService(t)
	ctx := context.Background()
	require.NoError(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5}))
	require.NoError(t, svc.SaveConfig(ctx, 2, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 10}))
	startedAt := time.Unix(1700000000, 0)
	ctx = WithGroupAntiDistillationRequestStart(ctx, startedAt)
	for i := 0; i < 20; i++ {
		groupID := int64(i%2 + 1)
		svc.Observe(ctx, &APIKey{ID: int64(i + 100), UserID: 7, Group: &Group{ID: groupID, Platform: PlatformOpenAI}}, fmt.Sprint(i), i%2)
	}
	observations := cache.Observations()
	require.Len(t, observations, 20)
	for i, observation := range observations {
		require.EqualValues(t, 7, observation.userID, "API keys and groups share the customer user scope")
		require.Equal(t, fmt.Sprint(i), observation.requestID, "adapter owns hashing and deduplication")
		require.Equal(t, i%2 == 0, observation.zero)
		require.Equal(t, time.Duration(5+5*(i%2))*time.Minute, observation.pause, "the observing group supplies its duration")
		require.Equal(t, startedAt, observation.startedAt)
	}
	cache.mu.Lock()
	cache.remaining[7] = 10 * time.Minute
	cache.mu.Unlock()
	remaining, err := svc.Check(ctx, 7, 1)
	require.NoError(t, err)
	require.Equal(t, 10*time.Minute, remaining)
	remaining, err = svc.Check(ctx, 8, 1)
	require.NoError(t, err)
	require.Zero(t, remaining, "another user is isolated")

	require.NoError(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{PauseMinutes: 5}))
	remaining, err = svc.Check(ctx, 7, 1)
	require.NoError(t, err)
	require.Zero(t, remaining, "disabled groups ignore an active shared pause")
	remaining, err = svc.Check(ctx, 7, 2)
	require.NoError(t, err)
	require.Equal(t, 10*time.Minute, remaining, "other enabled groups still enforce it")
}

func TestGroupAntiDistillationInvalidSamplesAndFailuresDoNotPause(t *testing.T) {
	svc, cache := antiDistillationTestService(t)
	ctx := context.Background()
	key := &APIKey{UserID: 7, Group: &Group{ID: 1, Platform: PlatformOpenAI}}
	for i := 0; i < 21; i++ {
		svc.Observe(ctx, key, fmt.Sprint(i), 0)
	}
	require.Empty(t, cache.Keys(), "disabled groups never submit evidence")
	require.NoError(t, svc.SaveConfig(ctx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 5}))
	for i := 0; i < 21; i++ {
		svc.Observe(ctx, key, "", 0)
		svc.Observe(ctx, key, fmt.Sprint(i), -1)
		svc.Observe(ctx, nil, fmt.Sprint(i), 0)
	}
	require.Empty(t, cache.Keys(), "unknown/invalid samples have no effect")
	cache.mu.Lock()
	cache.err = errors.New("offline")
	cache.mu.Unlock()
	remaining, err := svc.Check(ctx, 7, 1)
	require.Error(t, err)
	require.Zero(t, remaining, "cache failure cannot establish a pause")

	settings := antiDistillationTestSettings(t, svc)
	settings.mu.Lock()
	settings.err = errors.New("offline")
	settings.mu.Unlock()
	svc.mu.Lock()
	svc.configs = nil
	svc.mu.Unlock()
	remaining, err = svc.Check(ctx, 7, 1)
	require.Error(t, err)
	require.Zero(t, remaining, "config failures cannot establish a pause")
}

func TestGroupAntiDistillationConfigCacheBoundedAndRefreshes(t *testing.T) {
	svc, _ := antiDistillationTestService(t)
	ctx := context.Background()
	_, err := svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	settings := antiDistillationTestSettings(t, svc)
	require.NoError(t, settings.Set(ctx, groupAntiDistillationSettingKey(1), `{"enabled":true,"pause_minutes":12}`))
	svc.mu.Lock()
	cached := svc.configs[1]
	cached.expiresAt = time.Now().Add(-time.Second)
	svc.configs[1] = cached
	svc.mu.Unlock()
	cfg, err := svc.GetConfig(ctx, 1)
	require.NoError(t, err)
	require.True(t, cfg.Enabled)
	require.Equal(t, 12, cfg.PauseMinutes)
	svc.mu.Lock()
	for id := int64(2); id < 1000; id++ {
		svc.cacheConfigLocked(id, DefaultGroupAntiDistillationConfig(), time.Now())
	}
	require.LessOrEqual(t, len(svc.configs), groupAntiDistillationCacheLimit)
	svc.mu.Unlock()
}

type antiDistillationBlockedSettings struct {
	*antiDistillationSettings
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (r *antiDistillationBlockedSettings) GetValue(ctx context.Context, key string) (string, error) {
	value, err := r.antiDistillationSettings.GetValue(ctx, key)
	if key == groupAntiDistillationSettingKey(1) {
		blocked := false
		r.once.Do(func() {
			blocked = true
			close(r.started)
		})
		if blocked {
			select {
			case <-r.release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	return value, err
}

func TestGroupAntiDistillationSlowConfigDoesNotBlockGroupsOrOverwriteSave(t *testing.T) {
	svc, _ := antiDistillationTestService(t)
	ctx := context.Background()
	blocked := &antiDistillationBlockedSettings{
		antiDistillationSettings: antiDistillationTestSettings(t, svc),
		started:                  make(chan struct{}), release: make(chan struct{}),
	}
	svc.settings = blocked
	finished := make(chan error, 1)
	go func() {
		_, err := svc.GetConfig(ctx, 1)
		finished <- err
	}()
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("config read did not start")
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(blocked.release) }) }
	defer release()
	otherCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err := svc.GetConfig(otherCtx, 2)
	require.NoError(t, err, "one blocked SQL read must not hold the global cache mutex")

	waitCtx, stopWait := context.WithCancel(ctx)
	stopWait()
	_, err = svc.GetConfig(waitCtx, 1)
	require.ErrorIs(t, err, context.Canceled, "a waiter can stop without waiting for the owner SQL")
	require.NoError(t, svc.SaveConfig(otherCtx, 1, GroupAntiDistillationConfig{Enabled: true, PauseMinutes: 9}))
	cfg, err := svc.GetConfig(otherCtx, 1)
	require.NoError(t, err)
	require.True(t, cfg.Enabled)
	require.Equal(t, 9, cfg.PauseMinutes)
	release()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("pre-save read did not finish")
	}
	cfg, err = svc.GetConfig(otherCtx, 1)
	require.NoError(t, err)
	require.True(t, cfg.Enabled, "pre-save read must not replace the newer cached value")
	require.Equal(t, 9, cfg.PauseMinutes)
}

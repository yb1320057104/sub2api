package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type keyGroupAvailabilityStub struct {
	calls     []int64
	available map[int64]bool
	fail      error
	requests  []service.APIKeyGroupRouteRequest
}

func (s *keyGroupAvailabilityStub) Available(_ context.Context, group *service.Group, request service.APIKeyGroupRouteRequest) (bool, error) {
	s.calls = append(s.calls, group.ID)
	s.requests = append(s.requests, request)
	return s.available[group.ID], s.fail
}

func TestAPIKeyGroupRequestUsesActualProtocolModelAndResourceOwner(t *testing.T) {
	for _, tc := range []struct{ method, route, url, body, model, kind, resource string }{
		{http.MethodPost, "/v1/live", "/v1/live?model=ignored", `{"model":"text","session":{"model":"live"}}`, "live", "", ""},
		{http.MethodPost, "/v1/responses", "/v1/responses?model=ignored", `{"model":"actual"}`, "actual", "", ""},
		{http.MethodPost, "/v1beta/models/*modelAction", "/v1beta/models/gemini-test:generateContent", `{"model":"ignored"}`, "gemini-test", "", ""},
		{http.MethodGet, "/v1/videos/:request_id/content", "/v1/videos/task/content", "", "", "video", "task"},
		{http.MethodDelete, "/api/v3/contents/generations/tasks/:task_id", "/api/v3/contents/generations/tasks/task", "", "", "video", "seedance:task"},
		{http.MethodPatch, "/v1beta/cachedContents/:cacheID", "/v1beta/cachedContents/abc", `{"ttl":"60s"}`, "", "gemini_cache", "abc"},
		{http.MethodPost, "/v1beta/models/*modelAction", "/v1beta/models/gemini-test:generateContent", `{"cachedContent":"cachedContents/abc"}`, "gemini-test", "gemini_cache", "cachedContents/abc"},
	} {
		t.Run(tc.method+tc.url, func(t *testing.T) {
			groups := []*service.Group{{ID: 1, Status: service.StatusActive}, {ID: 2, Status: service.StatusActive}}
			key := &service.APIKey{ID: 7, UserID: 8, GroupIDs: []int64{1, 2}, Groups: groups, User: &service.User{ID: 8}}
			router := &keyGroupAvailabilityStub{available: map[int64]bool{2: true}}
			r := gin.New()
			r.Handle(tc.method, tc.route, func(c *gin.Context) {
				c.Set(keyGroupRouterContextKey, router)
				selected, ok := routeAPIKeyRequestGroup(c, key, nil, nil)
				require.True(t, ok)
				require.Equal(t, int64(2), *selected.GroupID)
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(tc.method, tc.url, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusNoContent, w.Code)
			require.Len(t, router.requests, 2)
			got := router.requests[1]
			require.Equal(t, tc.model, got.Model)
			require.Equal(t, tc.kind, got.ResourceKind)
			require.Equal(t, tc.resource, got.ResourceID)
			require.Equal(t, int64(7), got.APIKeyID)
			require.Equal(t, int64(8), got.UserID)
			require.Equal(t, tc.body, string(got.Body))
		})
	}
}

func TestAPIKeyGroupResourceListKeepsOnlyAdmittedGroups(t *testing.T) {
	groups := []*service.Group{{ID: 1, Status: service.StatusActive}, {ID: 2, Status: service.StatusActive, IsExclusive: true}, {ID: 3, Status: service.StatusDisabled}, {ID: 4, Status: service.StatusActive}}
	key := &service.APIKey{GroupIDs: []int64{4, 2, 3, 1}, Groups: groups, User: &service.User{ID: 7}}
	router := &keyGroupAvailabilityStub{available: map[int64]bool{1: true, 4: true}}
	eligible := func(*service.Group) (bool, error) { return true, nil }
	ids, err := apiKeyResourceGroupIDs(context.Background(), key, service.APIKeyGroupRouteRequest{IdentityOnly: true}, router, eligible)
	require.NoError(t, err)
	require.Equal(t, []int64{4, 1}, ids)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(keyResourceGroupsContextKey, ids)
	view := APIKeyResourceGroupIDs(c, key)
	view[0] = 999
	require.Equal(t, []int64{4, 1}, APIKeyResourceGroupIDs(c, key))
	_, err = apiKeyResourceGroupIDs(context.Background(), key, service.APIKeyGroupRouteRequest{}, router, func(*service.Group) (bool, error) { return false, errors.New("membership unavailable") })
	require.Error(t, err, "permission backend errors cannot publish a partial resource scope")
}

type keyGroupSubscriptionStub struct {
	subscription    *service.UserSubscription
	err             error
	userID, groupID int64
}

func (s *keyGroupSubscriptionStub) GetActiveSubscription(_ context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	s.userID, s.groupID = userID, groupID
	return s.subscription, s.err
}

func TestAPIKeyGroupRouterFirstAvailableAndRequestIsolation(t *testing.T) {
	first, second := &service.Group{ID: 3, Status: service.StatusActive}, &service.Group{ID: 1, Status: service.StatusActive}
	override := 8
	key := &service.APIKey{GroupID: &first.ID, Group: first, GroupIDs: []int64{3, 1}, Groups: []*service.Group{first, second}, User: &service.User{ID: 7, UserGroupRPMOverride: &override}}
	router := &keyGroupAvailabilityStub{available: map[int64]bool{1: true}}
	selected, err := chooseAPIKeyRequestGroup(context.Background(), key, service.APIKeyGroupRouteRequest{Model: "m"}, router)
	require.NoError(t, err)
	require.Equal(t, []int64{3, 1}, router.calls)
	require.Equal(t, int64(1), *selected.GroupID)
	require.Equal(t, []int64{3, 1}, selected.SelectedGroupIDs())
	require.Nil(t, selected.User.UserGroupRPMOverride)
	require.Equal(t, int64(3), *key.GroupID, "cached key cannot be mutated by per-request routing")
	require.NotNil(t, key.User.UserGroupRPMOverride)
}

func TestAPIKeyGroupRouterSkipsDeniedInactiveMissingAndStopsOnError(t *testing.T) {
	groups := []*service.Group{{ID: 1, Status: service.StatusActive, IsExclusive: true}, {ID: 2, Status: service.StatusDisabled}, {ID: 4, Status: service.StatusActive}}
	key := &service.APIKey{GroupIDs: []int64{1, 2, 3, 4}, Groups: groups, User: &service.User{ID: 7}}
	router := &keyGroupAvailabilityStub{available: map[int64]bool{4: true}}
	selected, err := chooseAPIKeyRequestGroup(context.Background(), key, service.APIKeyGroupRouteRequest{}, router)
	require.NoError(t, err)
	require.Equal(t, []int64{4}, router.calls)
	require.Equal(t, int64(4), *selected.GroupID)
	router.fail = errors.New("scheduler unavailable")
	_, err = chooseAPIKeyRequestGroup(context.Background(), key, service.APIKeyGroupRouteRequest{}, router)
	require.ErrorIs(t, err, router.fail)
	router.fail = nil
	router.available = nil
	_, err = chooseAPIKeyRequestGroup(context.Background(), key, service.APIKeyGroupRouteRequest{}, router)
	require.ErrorIs(t, err, service.ErrNoAvailableAccounts)
	_, err = chooseAPIKeyRequestGroup(context.Background(), key, service.APIKeyGroupRouteRequest{}, nil)
	require.Error(t, err)
}

func TestAPIKeyGroupRouterLegacySingleGroupPreservesExistingPath(t *testing.T) {
	id := int64(5)
	key := &service.APIKey{GroupID: &id}
	selected, err := chooseAPIKeyRequestGroup(context.Background(), key, service.APIKeyGroupRouteRequest{}, nil)
	require.NoError(t, err)
	require.Same(t, key, selected)
}

func TestAPIKeyGroupRouterWebSocketDefersAccountsButKeepsPlatformAndPermissions(t *testing.T) {
	groups := []*service.Group{
		{ID: 1, Status: service.StatusActive, Platform: service.PlatformAnthropic},
		{ID: 2, Status: service.StatusActive, Platform: service.PlatformOpenAI, IsExclusive: true},
		{ID: 3, Status: service.StatusActive, Platform: service.PlatformComposite},
		{ID: 4, Status: service.StatusActive, Platform: service.PlatformOpenAI},
	}
	key := &service.APIKey{GroupIDs: []int64{1, 2, 3, 4}, Groups: groups, User: &service.User{ID: 7}}
	router := &keyGroupAvailabilityStub{available: map[int64]bool{4: true}}
	request := service.APIKeyGroupRouteRequest{WebSocket: true}
	selected, err := chooseAPIKeyRequestGroup(context.Background(), key, request, router)
	require.NoError(t, err)
	require.Equal(t, int64(3), *selected.GroupID)
	require.Empty(t, router.calls, "model and account availability wait for the first frame")
	request.Model = "gpt-5.1"
	selected, err = chooseAPIKeyRequestGroup(context.Background(), key, request, router)
	require.NoError(t, err)
	require.Equal(t, int64(4), *selected.GroupID)
	require.Equal(t, []int64{1, 3, 4}, router.calls, "the first frame must evaluate actual account availability")
}

func TestAPIKeyGroupSubscriptionAdmissionDoesNotBypassOrRerouteLimits(t *testing.T) {
	key := &service.APIKey{User: &service.User{ID: 7}}
	group := &service.Group{ID: 3, SubscriptionType: service.SubscriptionTypeSubscription}
	ctx := context.Background()
	for _, absent := range []error{service.ErrSubscriptionNotFound, service.ErrSubscriptionExpired, service.ErrSubscriptionSuspended} {
		ok, err := eligibleAPIKeyGroupSubscriptionWithLookup(ctx, key, group, &keyGroupSubscriptionStub{err: absent}, nil)
		require.NoError(t, err)
		require.False(t, ok)
	}
	for _, failure := range []error{errors.New("database unavailable"), service.ErrDailyLimitExceeded} {
		ok, err := eligibleAPIKeyGroupSubscriptionWithLookup(ctx, key, group, &keyGroupSubscriptionStub{err: failure}, nil)
		require.ErrorIs(t, err, failure)
		require.False(t, ok)
	}
	ok, err := eligibleAPIKeyGroupSubscription(ctx, key, group, nil, nil)
	require.Error(t, err)
	require.False(t, ok)
	lookup := &keyGroupSubscriptionStub{subscription: &service.UserSubscription{UserID: 7, GroupID: 3, DailyUsageUSD: 999, Status: service.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)}}
	ok, err = eligibleAPIKeyGroupSubscriptionWithLookup(ctx, key, group, lookup, nil)
	require.NoError(t, err)
	require.True(t, ok, "limits are enforced on the selected group by normal auth, never trigger candidate fallback")
	require.Equal(t, int64(7), lookup.userID)
	require.Equal(t, int64(3), lookup.groupID)
	lookup.subscription.ExpiresAt = time.Now().Add(-time.Second)
	ok, err = eligibleAPIKeyGroupSubscriptionWithLookup(ctx, key, group, lookup, nil)
	require.NoError(t, err)
	require.False(t, ok, "cached membership must not survive its expiration")
	lookup.subscription.GroupID = 99
	ok, err = eligibleAPIKeyGroupSubscriptionWithLookup(ctx, key, group, lookup, nil)
	require.Error(t, err)
	require.False(t, ok)
	ok, err = eligibleAPIKeyGroupSubscription(ctx, key, group, nil, &config.Config{RunMode: config.RunModeSimple})
	require.NoError(t, err)
	require.True(t, ok)
}

func TestAPIKeyGroupRouterSubscriptionAdmissionSkipsOnlyMissingMembership(t *testing.T) {
	groups := []*service.Group{{ID: 1, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeSubscription}, {ID: 2, Status: service.StatusActive}}
	key := &service.APIKey{GroupIDs: []int64{1, 2}, Groups: groups, User: &service.User{ID: 7}}
	router := &keyGroupAvailabilityStub{available: map[int64]bool{1: true, 2: true}}
	lookup := &keyGroupSubscriptionStub{err: service.ErrSubscriptionNotFound}
	eligibility := func(group *service.Group) (bool, error) {
		return eligibleAPIKeyGroupSubscriptionWithLookup(context.Background(), key, group, lookup, nil)
	}
	selected, err := chooseAPIKeyRequestGroup(context.Background(), key, service.APIKeyGroupRouteRequest{}, router, eligibility)
	require.NoError(t, err)
	require.Equal(t, int64(2), *selected.GroupID)
	require.Equal(t, []int64{2}, router.calls)
	lookup.err = service.ErrDailyLimitExceeded
	_, err = chooseAPIKeyRequestGroup(context.Background(), key, service.APIKeyGroupRouteRequest{}, router, eligibility)
	require.ErrorIs(t, err, service.ErrDailyLimitExceeded, "usage limits cannot become a fallback bypass")
}

//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type proxyPriorityAccountRepo struct {
	schedulerTestOpenAIAccountRepo
	policy      GroupProxyPriorityPolicy
	policyError error
}

func (r *proxyPriorityAccountRepo) GetGroupProxyPriorityPolicy(context.Context, int64) (*GroupProxyPriorityPolicy, error) {
	return &r.policy, r.policyError
}
func (r *proxyPriorityAccountRepo) ListByGroup(context.Context, int64) ([]Account, error) {
	return append([]Account(nil), r.accounts...), nil
}
func (r *proxyPriorityAccountRepo) GetAccountProxyBindings(_ context.Context, id int64) ([]AccountProxyBinding, error) {
	a, err := r.GetByID(context.Background(), id)
	if err != nil {
		return nil, err
	}
	return a.ProxyBindings, nil
}

func newProxyPriorityRepo() *proxyPriorityAccountRepo {
	groupID := int64(9)
	poolID := int64(7)
	accounts := newLegacySchedulerDecisionTestAccounts(groupID, true)
	for i := range accounts {
		accounts[i].ProxyBindings = []AccountProxyBinding{{ProxyID: 1, Proxy: &Proxy{ID: 1, Status: StatusActive}}, {ProxyID: 2, Proxy: &Proxy{ID: 2, Status: StatusActive}}}
	}
	return &proxyPriorityAccountRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, policy: GroupProxyPriorityPolicy{GroupID: groupID, StrictPriority: true, ProxyGroupID: &poolID, ProxyIDs: []int64{1, 2}}}
}

func TestProxyPriorityWinnerHealthCapacityExclusionsAndBinding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(*proxyPriorityAccountRepo)
		excluded map[int64]struct{}
		want     int64
		wantErr  bool
	}{
		{name: "highest", want: 38101},
		{name: "same priority stable id", mutate: func(r *proxyPriorityAccountRepo) { r.accounts[1].Priority = 0 }, want: 38101},
		{name: "concurrency zero is not health", mutate: func(r *proxyPriorityAccountRepo) { r.accounts[0].Concurrency = 0 }, want: 38101},
		{name: "excluded owner cannot spill", excluded: map[int64]struct{}{38101: {}}, want: 38101},
		{name: "disabled failover", mutate: func(r *proxyPriorityAccountRepo) { r.accounts[0].Schedulable = false }, want: 38102},
		{name: "rate limited failover", mutate: func(r *proxyPriorityAccountRepo) {
			until := time.Now().Add(time.Minute)
			r.accounts[0].RateLimitResetAt = &until
		}, want: 38102},
		{name: "no pool binding excluded", mutate: func(r *proxyPriorityAccountRepo) { r.accounts[0].ProxyBindings = nil }, want: 38102},
		{name: "all unavailable", mutate: func(r *proxyPriorityAccountRepo) {
			for i := range r.accounts {
				r.accounts[i].Schedulable = false
			}
		}, wantErr: true},
		{name: "database failure closed", mutate: func(r *proxyPriorityAccountRepo) { r.policyError = errors.New("db down") }, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newProxyPriorityRepo()
			if tc.mutate != nil {
				tc.mutate(r)
			}
			groupID := int64(9)
			ctx, excluded, err := prepareProxyPriority(context.Background(), r, &groupID, tc.excluded, func(context.Context, *Account) bool { return true })
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			gate := ctx.Value(proxyPriorityGateKey{}).(*proxyPriorityGate)
			require.Equal(t, tc.want, gate.winner)
			require.False(t, proxyPriorityAllowed(ctx, 38103))
			for _, a := range r.accounts {
				if a.ID != tc.want {
					_, found := excluded[a.ID]
					require.True(t, found)
				}
			}
			if _, wasExcluded := tc.excluded[tc.want]; wasExcluded {
				_, stillExcluded := excluded[tc.want]
				require.True(t, stillExcluded)
			}
		})
	}
}

func TestProxyPriorityModelCompatibilityAndFreshAdmission(t *testing.T) {
	r := newProxyPriorityRepo()
	groupID := int64(9)
	original := WithAccountProxyRouting(context.Background())
	ctx, _, err := prepareProxyPriority(original, r, &groupID, nil, func(_ context.Context, a *Account) bool { return a.ID == 38102 })
	require.NoError(t, err)
	gate := ctx.Value(proxyPriorityGateKey{}).(*proxyPriorityGate)
	require.Equal(t, int64(38102), gate.winner)
	state := original.Value(accountProxyContextKey{}).(*accountProxyRequest)
	require.Same(t, gate, state.priority, "gate must reach original handler context")
	r.accounts[1].Schedulable = false
	fresh, err := gate.validate(original)
	require.NoError(t, err)
	require.Zero(t, fresh.winner, "health rechecked at dispatch")
}

func TestProxyPriorityOpenAISchedulerStickyAndFullAccount(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, busy := range []bool{false, true} {
			t.Run(fmt.Sprintf("advanced_%v_busy_%v", advanced, busy), func(t *testing.T) {
				r := newProxyPriorityRepo()
				groupID := int64(9)
				ctx := WithAccountProxyRouting(context.Background())
				concurrency := schedulerTestConcurrencyCache{}
				if busy {
					concurrency.acquireResults = map[int64]bool{38101: false}
					concurrency.loadMap = map[int64]*AccountLoadInfo{38101: {AccountID: 38101, LoadRate: 100}}
				}
				svc := newLegacySchedulerDecisionTestService(r.accounts, true, concurrency)
				svc.accountRepo = r
				require.NoError(t, svc.setStickySessionAccountID(ctx, &groupID, "sticky", 38102, time.Hour))
				var selection *AccountSelectionResult
				var err error
				if advanced {
					scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
					selection, _, err = scheduler.Select(ctx, OpenAIAccountScheduleRequest{GroupID: &groupID, Platform: PlatformOpenAI, SessionHash: "sticky", StickyAccountID: 38102, RequestedModel: "gpt-5.1", RequiredTransport: OpenAIUpstreamTransportAny, RequiredCapability: OpenAIEndpointCapabilityChatCompletions})
				} else {
					selection, err = svc.selectAccountWithLoadAwareness(ctx, &groupID, PlatformOpenAI, "sticky", "gpt-5.1", nil, false, OpenAIEndpointCapabilityChatCompletions, false)
				}
				require.NoError(t, err)
				require.NotNil(t, selection)
				defer releaseLegacySchedulerDecisionSelection(selection)
				require.Equal(t, int64(38101), selection.Account.ID)
				if busy {
					require.NotNil(t, selection.WaitPlan)
					require.False(t, selection.Acquired)
				}
			})
		}
	}
}

func TestProxyPriorityPreviousResponseDoesNotCrossAccounts(t *testing.T) {
	r := newProxyPriorityRepo()
	groupID := int64(9)
	svc := newLegacySchedulerDecisionTestService(r.accounts, true, schedulerTestConcurrencyCache{})
	svc.accountRepo = r
	ctx, _, err := svc.prepareOpenAIProxyPriority(context.Background(), OpenAIAccountScheduleRequest{GroupID: &groupID, Platform: PlatformOpenAI, RequiredTransport: OpenAIUpstreamTransportAny})
	require.NoError(t, err)
	require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, groupID, "resp_other", 38102, time.Hour))
	require.ErrorIs(t, svc.checkProxyPriorityPrevious(ctx, &groupID, "resp_other"), ErrNoAvailableAccounts)
}

func TestProxyPriorityDisabledPreservesExistingSelection(t *testing.T) {
	r := newProxyPriorityRepo()
	r.policy.StrictPriority = false
	groupID := int64(9)
	svc := newLegacySchedulerDecisionTestService(r.accounts, true, schedulerTestConcurrencyCache{})
	svc.accountRepo = r
	ctx := context.Background()
	require.NoError(t, svc.setStickySessionAccountID(ctx, &groupID, "sticky", 38102, time.Hour))
	selection, err := svc.selectAccountWithLoadAwareness(ctx, &groupID, PlatformOpenAI, "sticky", "gpt-5.1", nil, false, OpenAIEndpointCapabilityChatCompletions, false)
	require.NoError(t, err)
	defer releaseLegacySchedulerDecisionSelection(selection)
	require.Equal(t, int64(38102), selection.Account.ID)
}

func TestProxyPriorityCrossGroupFallbackClearsOldWinner(t *testing.T) {
	r := newProxyPriorityRepo()
	groupID := int64(9)
	ctx, _, err := prepareProxyPriority(WithAccountProxyRouting(context.Background()), r, &groupID, nil, func(context.Context, *Account) bool { return true })
	require.NoError(t, err)
	r.accounts[0].Schedulable = false
	nextGroup := int64(10)
	ctx, _, err = prepareProxyPriority(ctx, r, &nextGroup, nil, func(ctx context.Context, a *Account) bool { return proxyPriorityAllowed(ctx, a.ID) })
	require.NoError(t, err)
	require.True(t, proxyPriorityAllowed(ctx, 38102))
	require.False(t, proxyPriorityAllowed(ctx, 38101))
}

func TestProxyPriorityGatewayStickyOnlyIsRequestSpecific(t *testing.T) {
	for _, sticky := range []bool{false, true} {
		t.Run(fmt.Sprint(sticky), func(t *testing.T) {
			r := newProxyPriorityRepo()
			groupID := int64(9)
			for i := range r.accounts {
				r.accounts[i].Platform = PlatformAnthropic
				r.accounts[i].Type = AccountTypeOAuth
			}
			r.accounts[0].Extra = map[string]any{"base_rpm": 10, "rpm_strategy": "sticky_exempt"}
			group := &Group{ID: groupID, Platform: PlatformAnthropic, Hydrated: true, Status: StatusActive}
			svc := &GatewayService{accountRepo: r, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: group}}, cache: &schedulerTestGatewayCache{}}
			ctx := context.WithValue(context.Background(), ctxkey.Group, group)
			ctx = context.WithValue(ctx, rpmPrefetchContextKey, map[int64]int{38101: 10})
			session := ""
			want := int64(38102)
			if sticky {
				session = "known"
				want = 38101
				require.NoError(t, svc.cache.SetSessionAccountID(ctx, groupID, session, 38101, time.Hour))
			}
			ctx, _, err := svc.prepareGatewayProxyPriority(ctx, &groupID, session, "", nil)
			require.NoError(t, err)
			require.True(t, proxyPriorityAllowed(ctx, want))
		})
	}
}

type proxyPriorityCacheStub struct {
	acquired bool
	calls    int
}

func (c *proxyPriorityCacheStub) AcquireProxy(context.Context, int64, int, string) (bool, error) {
	c.calls++
	return c.acquired, nil
}
func (c *proxyPriorityCacheStub) AcquireAccountProxy(context.Context, int64, int, string, int64, []int64, bool) (bool, error) {
	c.calls++
	return c.acquired, nil
}
func (c *proxyPriorityCacheStub) RefreshProxy(context.Context, int64, string) (bool, error) {
	return true, nil
}
func (c *proxyPriorityCacheStub) ReleaseProxy(context.Context, int64, string) error { return nil }
func (c *proxyPriorityCacheStub) GetProxyConcurrencyBatch(context.Context, []int64) (map[int64]int, error) {
	return nil, nil
}

func TestProxyPriorityRouterDispatchAndPinnedBoundary(t *testing.T) {
	for _, tc := range []struct {
		name            string
		mutate          func(*proxyPriorityAccountRepo)
		account, pinned int64
		wantErr         error
	}{
		{name: "owner", account: 38101},
		{name: "standby cannot dispatch", account: 38102, wantErr: ErrProxyCapacity},
		{name: "owner changed before dispatch", account: 38101, mutate: func(r *proxyPriorityAccountRepo) { r.accounts[0].Schedulable = false }, wantErr: ErrProxyCapacity},
		{name: "binding removed cannot become direct", account: 38101, mutate: func(r *proxyPriorityAccountRepo) { r.accounts[0].ProxyBindings = nil }, wantErr: ErrProxyCapacity},
		{name: "pinned outside chosen group", account: 38101, pinned: 3, wantErr: ErrProxyUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newProxyPriorityRepo()
			groupID := int64(9)
			ctx := WithAccountProxyRouting(context.Background())
			_, _, err := prepareProxyPriority(ctx, r, &groupID, nil, func(context.Context, *Account) bool { return true })
			require.NoError(t, err)
			if tc.mutate != nil {
				tc.mutate(r)
			}
			cache := &proxyPriorityCacheStub{acquired: true}
			router := NewAccountProxyRouter(r, cache)
			route, err := router.Acquire(ctx, tc.account, "http://must-not-fallback.invalid", tc.pinned)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, cache.calls)
				return
			}
			require.NoError(t, err)
			defer route.Close()
			require.NotEmpty(t, route.LeaseID)
		})
	}
}

func TestProxyPriorityRouterUsesResolvedFallbackGroup(t *testing.T) {
	r := newProxyPriorityRepo()
	original := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 99})
	original = WithAccountProxyRouting(original)
	groupID := int64(9)
	_, _, err := prepareProxyPriority(original, r, &groupID, nil, func(context.Context, *Account) bool { return true })
	require.NoError(t, err)
	router := NewAccountProxyRouter(r, &proxyPriorityCacheStub{acquired: true})
	route, err := router.Acquire(original, 38101, "", 0)
	require.NoError(t, err)
	route.Close()
	r.policy.StrictPriority = false
	_, _, err = prepareProxyPriority(original, r, &groupID, nil, func(context.Context, *Account) bool { return true })
	require.NoError(t, err)
	r.policy.StrictPriority = true
	_, err = router.Acquire(original, 38101, "", 0)
	require.ErrorIs(t, err, ErrProxyLeaseUnavailable, "enabling after selection requires a fresh strict gate")
}

func TestProxyPriorityGrokModelHealthAllowsStandby(t *testing.T) {
	for _, kind := range []string{"team_limit", "model_quota"} {
		t.Run(kind, func(t *testing.T) {
			r := newProxyPriorityRepo()
			groupID := int64(9)
			for i := range r.accounts {
				r.accounts[i].Platform = PlatformGrok
				r.accounts[i].Type = AccountTypeOAuth
				r.accounts[i].Credentials = map[string]any{"team_id": fmt.Sprintf("pk51_%s_%d", kind, i)}
			}
			model := "grok-4.5"
			if kind == "team_limit" {
				markGrokTeamModelRateLimit(&r.accounts[0], model, time.Now().Add(time.Minute))
			} else {
				markGrokModelQuotaBlock(r.accounts[0].ID, model, time.Now().Add(time.Hour))
				t.Cleanup(func() {
					globalGrokModelQuotaBlocks.mu.Lock()
					delete(globalGrokModelQuotaBlocks.items, grokModelQuotaBlockKey(r.accounts[0].ID, model))
					globalGrokModelQuotaBlocks.mu.Unlock()
				})
			}
			svc := &OpenAIGatewayService{accountRepo: r}
			ctx, _, err := svc.prepareOpenAIProxyPriority(context.Background(), OpenAIAccountScheduleRequest{GroupID: &groupID, Platform: PlatformGrok, RequestedModel: model, RequiredTransport: OpenAIUpstreamTransportAny})
			require.NoError(t, err)
			require.True(t, proxyPriorityAllowed(ctx, 38102))
			require.False(t, proxyPriorityAllowed(ctx, 38101))
		})
	}
}

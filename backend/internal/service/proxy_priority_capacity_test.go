//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type pk52PartitionGatewayRepo struct{ *proxyPriorityAccountRepo }

func (r pk52PartitionGatewayRepo) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, _ []string) ([]Account, error) {
	return r.ListByGroup(ctx, groupID)
}

func pk52PartitionSchedulerRepo() *proxyPriorityAccountRepo {
	r := newProxyPriorityRepo()
	standby := r.accounts[1]
	standby.ID, standby.Priority = 38103, 10
	r.accounts = append(r.accounts, standby)
	r.policy.ProxyIDs = nil
	for i := range r.accounts {
		r.accounts[i].Concurrency = 100
		r.accounts[i].ProxyBindings = nil
		for id := int64(1); id <= 20; id++ {
			r.accounts[i].ProxyBindings = append(r.accounts[i].ProxyBindings, AccountProxyBinding{ProxyID: id, MaxConcurrency: 10, Proxy: &Proxy{ID: id, Status: StatusActive, MaxConcurrency: 10}})
			if i == 0 {
				r.policy.ProxyIDs = append(r.policy.ProxyIDs, id)
			}
		}
	}
	return r
}

func TestPK52ProxyPartitionOpenAISlotSelection(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, batch := range []bool{false, true} {
			for _, sticky := range []bool{false, true} {
				t.Run(fmt.Sprintf("advanced_%t_batch_%t_sticky_%t", advanced, batch, sticky), func(t *testing.T) {
					r := pk52PartitionSchedulerRepo()
					var acquired []int64
					cache := schedulerTestConcurrencyCache{acquiredIDs: &acquired, acquireResults: map[int64]bool{38101: false, 38102: true, 38103: true}, loadMap: map[int64]*AccountLoadInfo{38101: {AccountID: 38101, CurrentConcurrency: 100, LoadRate: 100}}}
					svc := newLegacySchedulerDecisionTestService(r.accounts, batch, cache)
					svc.accountRepo = r
					svc.cfg.Gateway.OpenAIWS.LBTopK = 1
					ctx := WithAccountProxyRouting(context.Background())
					groupID := int64(9)
					session := ""
					if sticky {
						session = "partition-sticky"
						require.NoError(t, svc.setStickySessionAccountID(ctx, &groupID, session, 38101, time.Hour))
					}
					var selection *AccountSelectionResult
					var err error
					if advanced {
						scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
						selection, _, err = scheduler.Select(ctx, OpenAIAccountScheduleRequest{GroupID: &groupID, Platform: PlatformOpenAI, SessionHash: session, RequestedModel: "gpt-5.1", RequiredTransport: OpenAIUpstreamTransportAny, RequiredCapability: OpenAIEndpointCapabilityChatCompletions})
					} else {
						selection, err = svc.selectAccountWithLoadAwareness(ctx, &groupID, PlatformOpenAI, session, "gpt-5.1", nil, false, OpenAIEndpointCapabilityChatCompletions, false)
					}
					require.NoError(t, err)
					require.NotNil(t, selection)
					defer releaseLegacySchedulerDecisionSelection(selection)
					require.Equal(t, int64(38102), selection.Account.ID)
					require.True(t, selection.Acquired)
					require.Nil(t, selection.WaitPlan)
					require.NotContains(t, acquired, int64(38103), "unallocated standby must not be probed")
				})
			}
		}
	}
}

func TestPK52ProxyPartitionAllFullWaitsWithoutStandby(t *testing.T) {
	r := pk52PartitionSchedulerRepo()
	var acquired []int64
	svc := newLegacySchedulerDecisionTestService(r.accounts, true, schedulerTestConcurrencyCache{acquiredIDs: &acquired, acquireResults: map[int64]bool{38101: false, 38102: false, 38103: true}})
	svc.accountRepo = r
	svc.cfg.Gateway.OpenAIWS.LBTopK = 1
	scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
	groupID := int64(9)
	selection, _, err := scheduler.Select(WithAccountProxyRouting(context.Background()), OpenAIAccountScheduleRequest{GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.1", RequiredTransport: OpenAIUpstreamTransportAny, RequiredCapability: OpenAIEndpointCapabilityChatCompletions})
	require.NoError(t, err)
	require.NotNil(t, selection)
	defer releaseLegacySchedulerDecisionSelection(selection)
	require.False(t, selection.Acquired)
	require.NotNil(t, selection.WaitPlan)
	require.Contains(t, []int64{38101, 38102}, selection.Account.ID)
	require.NotContains(t, acquired, int64(38103))
	require.Contains(t, acquired, int64(38102), "TopK=1 must try other allocated owner before waiting")
}

func TestPK52ProxyPartitionGatewayStickySlotSelection(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprint(batch), func(t *testing.T) {
			r := pk52PartitionSchedulerRepo()
			for i := range r.accounts {
				r.accounts[i].Platform = PlatformAnthropic
				r.accounts[i].Type = AccountTypeAPIKey
			}
			groupID := int64(9)
			group := &Group{ID: groupID, Platform: PlatformAnthropic, Hydrated: true, Status: StatusActive}
			cfg := newSchedulerTestOpenAIWSV2Config()
			cfg.Gateway.Scheduling.LoadBatchEnabled = batch
			var acquired []int64
			svc := &GatewayService{accountRepo: pk52PartitionGatewayRepo{r}, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: group}}, cache: &schedulerTestGatewayCache{}, cfg: cfg, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquired, acquireResults: map[int64]bool{38101: false, 38102: true, 38103: true}, loadMap: map[int64]*AccountLoadInfo{38101: {AccountID: 38101, CurrentConcurrency: 100, LoadRate: 100}}})}
			ctx := WithAccountProxyRouting(context.WithValue(context.Background(), ctxkey.Group, group))
			require.NoError(t, svc.cache.SetSessionAccountID(ctx, groupID, "sticky", 38101, time.Hour))
			selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "sticky", "", nil, "", 0)
			require.NoError(t, err)
			require.NotNil(t, selection)
			defer releaseLegacySchedulerDecisionSelection(selection)
			require.Equal(t, int64(38102), selection.Account.ID)
			require.True(t, selection.Acquired)
			require.NotContains(t, acquired, int64(38103))
		})
	}
}

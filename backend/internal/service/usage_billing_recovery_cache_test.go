//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type recoveryCacheFailure struct{ BillingCache }

func (*recoveryCacheFailure) InvalidateUserBalance(_ context.Context, user int64) error {
	if user == 1 {
		return errors.New("injected cache failure")
	}
	return nil
}

type recoveryCacheRepo struct {
	UsageBillingRepository
	seen     map[string]bool
	acked    []string
	deferred []string
}

func (*recoveryCacheRepo) StageUsageBilling(context.Context, *UsageBillingCommand, *UsageLog) error {
	return nil
}
func (*recoveryCacheRepo) PendingUsageBilling(context.Context, int) ([]UsageBillingEventKey, error) {
	return []UsageBillingEventKey{{RequestID: "first", APIKeyID: 1}, {RequestID: "second", APIKeyID: 2}}, nil
}
func (r *recoveryCacheRepo) ApplyStagedUsageBilling(_ context.Context, key UsageBillingEventKey) (*UsageBillingRecovery, error) {
	fresh := !r.seen[key.RequestID]
	r.seen[key.RequestID] = true
	return &UsageBillingRecovery{Event: UsageBillingEvent{Command: UsageBillingCommand{UserID: key.APIKeyID, BalanceCost: 1}},
		Result: UsageBillingApplyResult{Applied: fresh}, NeedsCache: true}, nil
}
func (r *recoveryCacheRepo) AcknowledgeUsageBillingCache(_ context.Context, key UsageBillingEventKey) error {
	r.acked = append(r.acked, key.RequestID)
	return nil
}
func (r *recoveryCacheRepo) DeferUsageBillingCache(_ context.Context, key UsageBillingEventKey) error {
	r.deferred = append(r.deferred, key.RequestID)
	return nil
}

func TestUsageRecoveryCacheFailureDoesNotStopAnotherEventOrReplayDeltas(t *testing.T) {
	for _, flusher := range []bool{false, true} {
		r := &recoveryCacheRepo{seen: map[string]bool{}}
		cfg := &config.Config{}
		cfg.Database.UserPlatformQuotaFlusherEnabled = flusher
		cache := &BillingCacheService{cache: &recoveryCacheFailure{}, cfg: cfg}
		for attempt := 0; attempt < 2; attempt++ {
			require.Error(t, recoverUsageBillingBatch(context.Background(), r, cache, nil))
		}
		require.Equal(t, []string{"second", "second"}, r.acked)
		require.Equal(t, []string{"first", "first"}, r.deferred)
		require.Len(t, r.seen, 2)
		// The embedded nil BillingCache panics on a monetary delta method. This
		// path only invalidates, under both legacy flusher configurations.
	}
}

type durableAuditFailureRepo struct {
	UsageBillingRepository
	event       UsageBillingEvent
	usage       *UsageLog
	debits      int
	cacheFailed bool
	acked       bool
	deferred    int
}

func (r *durableAuditFailureRepo) StageUsageBilling(_ context.Context, cmd *UsageBillingCommand, usage *UsageLog) error {
	r.usage = usage
	if r.event.Version == 0 {
		r.event = UsageBillingEvent{Version: 1, Command: *cmd, Usage: *usage}
		r.event.Usage.ID = 905
		r.event.Usage.CreatedAt = time.Now().UTC()
	}
	return nil
}
func (r *durableAuditFailureRepo) ApplyStagedUsageBilling(context.Context, UsageBillingEventKey) (*UsageBillingRecovery, error) {
	fresh := r.debits == 0
	if fresh {
		r.debits++
	}
	return &UsageBillingRecovery{Event: r.event, Result: UsageBillingApplyResult{Applied: fresh}, NeedsCache: !r.acked}, nil
}
func (r *durableAuditFailureRepo) PendingUsageBilling(context.Context, int) ([]UsageBillingEventKey, error) {
	if r.acked {
		return nil, nil
	}
	return []UsageBillingEventKey{{RequestID: r.event.Command.RequestID, APIKeyID: r.event.Command.APIKeyID}}, nil
}
func (r *durableAuditFailureRepo) AcknowledgeUsageBillingCache(context.Context, UsageBillingEventKey) error {
	if r.cacheFailed {
		return errors.New("injected acknowledgement failure")
	}
	r.acked = true
	return nil
}
func (r *durableAuditFailureRepo) DeferUsageBillingCache(context.Context, UsageBillingEventKey) error {
	r.deferred++
	return nil
}

func TestDurableBillingCacheFailurePreservesChargeAndFailsAuditReceipt(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		for _, failure := range []string{"invalidation", "acknowledgement"} {
			t.Run(platform+"/"+failure, func(t *testing.T) {
				repo := &durableAuditFailureRepo{cacheFailed: failure == "acknowledgement"}
				logs := &openAIRecordUsageLogRepoStub{}
				users := &openAIRecordUsageUserRepoStub{}
				tracker := &auditSettlementTracker{userID: 1, apiKeyID: 7}
				ctx := context.WithValue(context.Background(), auditSettlementContextKey{}, tracker)
				observed := 0
				ctx = WithAuditSettlementUsageObserver(ctx, func(context.Context, *UsageLog) error { observed++; return nil })
				var cache *BillingCacheService
				if failure == "invalidation" {
					cache = &BillingCacheService{cache: &recoveryCacheFailure{}}
				}
				user := &User{ID: 1}
				key := &APIKey{ID: 7, UserID: 1, User: user}
				account := &Account{ID: 8, Platform: platform, Type: AccountTypeAPIKey}
				var err error
				if platform == PlatformOpenAI {
					svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, repo, users, &openAIRecordUsageSubRepoStub{}, nil)
					svc.billingCacheService = cache
					err = svc.RecordUsage(ctx, &OpenAIRecordUsageInput{APIKey: key, User: user, Account: account,
						Result: &OpenAIForwardResult{RequestID: "durable-audit", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 20}}})
				} else {
					svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, repo, users, &openAIRecordUsageSubRepoStub{})
					svc.billingCacheService = cache
					err = svc.RecordUsage(ctx, &RecordUsageInput{APIKey: key, User: user, Account: account,
						Result: &ForwardResult{RequestID: "durable-audit", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 100, OutputTokens: 20}}})
				}
				require.Error(t, err)
				require.True(t, tracker.failed, "a cache failure must not export an apparently settled audit receipt")
				require.Zero(t, observed, "failed settlement cannot register a remote charge")
				require.Zero(t, logs.calls, "the committed usage must not be replaced by a zero-charge fallback")
				require.Zero(t, users.deductCalls, "durable billing must not fall back to legacy debit")
				require.Equal(t, 1, repo.debits)
				require.Greater(t, repo.usage.ActualCost, 0.0)
				require.Equal(t, repo.event.Command.BalanceCost, repo.usage.ActualCost)
				require.True(t, repo.usage.durableBillingCommitted)
				require.Equal(t, int64(905), repo.usage.ID)
				require.False(t, repo.acked)
				repo.cacheFailed = false
				require.NoError(t, recoverUsageBillingBatch(context.Background(), repo, nil, nil))
				require.True(t, repo.acked)
				require.Equal(t, 1, repo.debits, "cache recovery must not charge a second time")
				require.True(t, tracker.failed, "cache recovery does not silently re-run audit penalties")
			})
		}
	}
}

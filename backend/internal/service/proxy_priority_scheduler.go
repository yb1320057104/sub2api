package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// A strict pool picks one account before sticky, cost and load ordering. The
// choice deliberately ignores request-local exclusions and concurrency: those
// are reasons to wait/fail this request, never permission to spill to a standby.
type proxyPriorityGate struct {
	groupID   int64
	winner    int64
	resources []int64
	egress    []int64
	validate  func(context.Context) (*proxyPriorityGate, error)
}

type proxyPriorityGateKey struct{}

func proxyPriorityAllowed(ctx context.Context, accountID int64) bool {
	gate, _ := ctx.Value(proxyPriorityGateKey{}).(*proxyPriorityGate)
	return gate == nil || gate.winner == accountID
}

func buildProxyPriorityGate(ctx context.Context, repo AccountRepository, groupID int64, eligible func(context.Context, *Account) bool) (*proxyPriorityGate, []Account, error) {
	reader, ok := repo.(GroupProxyPriorityPolicyReader)
	if !ok || groupID <= 0 {
		return nil, nil, nil
	}
	policy, err := reader.GetGroupProxyPriorityPolicy(ctx, groupID)
	if err != nil {
		return nil, nil, fmt.Errorf("proxy priority policy unavailable: %w", err)
	}
	if policy == nil || !policy.StrictPriority {
		return nil, nil, nil
	}
	if policy.ProxyGroupID == nil || len(policy.ProxyIDs) == 0 {
		return nil, nil, ErrProxyUnavailable
	}
	accounts, err := repo.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, nil, fmt.Errorf("proxy priority candidates unavailable: %w", err)
	}
	pool := make(map[int64]bool, len(policy.ProxyIDs))
	for _, id := range policy.ProxyIDs {
		pool[id] = true
	}
	gate := &proxyPriorityGate{groupID: groupID, resources: append([]int64(nil), policy.ProxyIDs...)}
	var winner *Account
	for i := range accounts {
		account := &accounts[i]
		if !account.IsSchedulable() || !eligible(ctx, account) {
			continue
		}
		bound := false
		for _, binding := range account.ProxyBindings {
			if pool[binding.ProxyID] && binding.Proxy != nil && binding.Proxy.IsActive() && !binding.Proxy.IsExpired(time.Now()) {
				bound = true
				break
			}
		}
		if !bound {
			continue
		}
		if winner == nil || account.Priority < winner.Priority || (account.Priority == winner.Priority && account.ID < winner.ID) {
			winner = account
		}
	}
	if winner != nil {
		gate.winner = winner.ID
		// Lock physical resources, including any manually overlapping bindings.
		// Moving a proxy between groups cannot evade an in-flight reservation.
		for _, binding := range winner.ProxyBindings {
			gate.resources = append(gate.resources, binding.ProxyID)
			if pool[binding.ProxyID] {
				gate.egress = append(gate.egress, binding.ProxyID)
			}
		}
	}
	gate.resources = uniqueProxyResourceIDs(gate.resources)
	return gate, accounts, nil
}

func uniqueProxyResourceIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func prepareProxyPriority(ctx context.Context, repo AccountRepository, groupID *int64, excluded map[int64]struct{}, eligible func(context.Context, *Account) bool) (context.Context, map[int64]struct{}, error) {
	if groupID == nil || *groupID <= 0 || repo == nil {
		return ctx, excluded, nil
	}
	if existing, _ := ctx.Value(proxyPriorityGateKey{}).(*proxyPriorityGate); existing != nil && existing.groupID == *groupID {
		return ctx, excluded, nil
	}
	ctx = context.WithValue(ctx, proxyPriorityGateKey{}, (*proxyPriorityGate)(nil))
	if state, ok := ctx.Value(accountProxyContextKey{}).(*accountProxyRequest); ok {
		state.mu.Lock()
		state.priorityGroupID = *groupID
		state.mu.Unlock()
	}
	gate, accounts, err := buildProxyPriorityGate(ctx, repo, *groupID, eligible)
	if err != nil {
		return ctx, excluded, err
	}
	if gate == nil {
		if state, ok := ctx.Value(accountProxyContextKey{}).(*accountProxyRequest); ok {
			state.mu.Lock()
			state.priority = nil
			state.mu.Unlock()
		}
		return context.WithValue(ctx, proxyPriorityGateKey{}, (*proxyPriorityGate)(nil)), excluded, nil
	}
	id := *groupID
	eligibilityCtx := context.WithValue(ctx, proxyPriorityGateKey{}, (*proxyPriorityGate)(nil))
	gate.validate = func(checkCtx context.Context) (*proxyPriorityGate, error) {
		fresh, _, err := buildProxyPriorityGate(checkCtx, repo, id, func(_ context.Context, a *Account) bool { return eligible(eligibilityCtx, a) })
		return fresh, err
	}
	ctx = context.WithValue(ctx, proxyPriorityGateKey{}, gate)
	if state, ok := ctx.Value(accountProxyContextKey{}).(*accountProxyRequest); ok {
		state.mu.Lock()
		state.priority = gate
		state.mu.Unlock()
	}
	result := make(map[int64]struct{}, len(excluded)+len(accounts))
	for id := range excluded {
		result[id] = struct{}{}
	}
	for i := range accounts {
		if accounts[i].ID != gate.winner {
			result[accounts[i].ID] = struct{}{}
		}
	}
	if gate.winner == 0 {
		return ctx, result, ErrNoAvailableAccounts
	}
	return ctx, result, nil
}

func (s *OpenAIGatewayService) prepareOpenAIProxyPriority(ctx context.Context, req OpenAIAccountScheduleRequest) (context.Context, map[int64]struct{}, error) {
	checker := &defaultOpenAIAccountScheduler{service: s}
	return prepareProxyPriority(ctx, s.accountRepo, req.GroupID, req.ExcludedIDs, func(checkCtx context.Context, a *Account) bool {
		if a.IsGrok() {
			if paused, _ := shouldAutoPauseGrokAccountByQuota(a); paused {
				return false
			}
			pool := s.filterGrokFreeQuotaAccountsForOpenAI(checkCtx, []Account{*a})
			pool = filterGrokTeamModelRateLimitedAccounts(pool, req.RequestedModel, time.Now())
			pool = filterGrokModelQuotaBlockedAccounts(pool, req.RequestedModel, time.Now())
			if len(pool) == 0 {
				return false
			}
		}
		if s.isOpenAIAccountBlockedBySchedulingThreshold(checkCtx, a) {
			return false
		}
		if a.Platform != NormalizeOpenAICompatiblePlatform(req.Platform) || !a.IsSchedulableForModelWithContext(checkCtx, req.RequestedModel) {
			return false
		}
		if !s.isOpenAIAccountTransportCompatible(a, req.RequiredTransport) {
			return false
		}
		ok, _ := checker.isAccountRequestCompatibleReason(checkCtx, a, req)
		return ok
	})
}

func (s *OpenAIGatewayService) checkProxyPriorityPrevious(ctx context.Context, groupID *int64, previousResponseID string) error {
	if previousResponseID == "" {
		return nil
	}
	gate, _ := ctx.Value(proxyPriorityGateKey{}).(*proxyPriorityGate)
	if gate == nil {
		return nil
	}
	owner, err := s.getOpenAIWSStateStore().GetResponseAccount(ctx, derefGroupID(groupID), previousResponseID)
	if err != nil {
		return ErrProxyLeaseUnavailable
	}
	if owner > 0 && owner != gate.winner {
		return fmt.Errorf("%w: response owner is waiting for its strict proxy pool", ErrNoAvailableAccounts)
	}
	return nil
}

func (s *GatewayService) prepareGatewayProxyPriority(ctx context.Context, groupID *int64, sessionHash, model string, excluded map[int64]struct{}) (context.Context, map[int64]struct{}, error) {
	stickyID := prefetchedStickyAccountIDFromContext(ctx, groupID)
	if stickyID == 0 && sessionHash != "" && s.cache != nil {
		stickyID, _ = s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), sessionHash)
	}
	return prepareProxyPriority(ctx, s.accountRepo, groupID, excluded, func(checkCtx context.Context, a *Account) bool {
		if a.IsGrok() {
			if paused, _ := shouldAutoPauseGrokAccountByQuota(a); paused {
				return false
			}
			pool := s.filterGrokFreeQuotaAccountsForGateway(checkCtx, []Account{*a})
			pool = filterGrokTeamModelRateLimitedAccounts(pool, model, time.Now())
			pool = filterGrokModelQuotaBlockedAccounts(pool, model, time.Now())
			if len(pool) == 0 {
				return false
			}
		}
		if s.isAccountBlockedBySchedulingThreshold(checkCtx, a) {
			return false
		}
		group, _ := checkCtx.Value(ctxkey.Group).(*Group)
		platform, forced, err := s.resolvePlatform(checkCtx, groupID, group, model)
		if err != nil {
			return false
		}
		mixed := !forced && (platform == PlatformAnthropic || platform == PlatformGemini)
		return s.isAccountAllowedForPlatform(a, platform, mixed) &&
			(group == nil || !group.RequirePrivacySet || a.IsPrivacySet()) &&
			(model == "" || s.isModelSupportedByAccountWithContext(checkCtx, a, model)) &&
			s.isAccountSchedulableForModelSelection(checkCtx, a, model) && s.isAccountSchedulableForQuota(a) &&
			s.isAccountSchedulableForWindowCost(checkCtx, a, stickyID == a.ID) && s.isAccountSchedulableForRPM(checkCtx, a, stickyID == a.ID) &&
			s.isGatewayAccountProfitEligible(checkCtx, a) && !s.isStickyAccountUpstreamRestricted(checkCtx, groupID, a, model)
	})
}

package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// A strict pool assigns disjoint subsets in account priority order before sticky,
// cost and load ordering. Occupancy and request-local exclusions do not change
// those assignments; only an unavailable account releases its place in the pool.
type proxyPriorityGate struct {
	groupID  int64
	winner   int64 // first admitted account; retained for diagnostics
	subsets  map[int64][]int64
	validate func(context.Context) (*proxyPriorityGate, error)
}

type proxyPriorityGateKey struct{}

func proxyPriorityAllowed(ctx context.Context, accountID int64) bool {
	gate, _ := ctx.Value(proxyPriorityGateKey{}).(*proxyPriorityGate)
	return gate == nil || len(gate.subsets[accountID]) > 0
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
	gate := &proxyPriorityGate{groupID: groupID, subsets: make(map[int64][]int64)}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Priority != accounts[j].Priority {
			return accounts[i].Priority < accounts[j].Priority
		}
		return accounts[i].ID < accounts[j].ID
	})
	assigned := make(map[int64]bool, len(pool))
	now := time.Now()
	for i := range accounts {
		account := &accounts[i]
		if !account.IsSchedulable() || !eligible(ctx, account) {
			continue
		}
		bindings := make(map[int64]AccountProxyBinding)
		var candidates []int64
		unlimited := false
		for _, binding := range account.ProxyBindings {
			if pool[binding.ProxyID] && !assigned[binding.ProxyID] && binding.Proxy != nil && binding.Proxy.IsActive() && !binding.Proxy.IsExpired(now) {
				bindings[binding.ProxyID] = binding
				candidates = append(candidates, binding.ProxyID)
				unlimited = unlimited || binding.MaxConcurrency <= 0
			}
		}
		if len(candidates) == 0 {
			continue
		}
		candidates = uniqueProxyResourceIDs(candidates)
		remaining := account.Concurrency
		for _, id := range candidates {
			// A pool with legacy unlimited proxy limits has no finite ratio;
			// preserve its single-owner behavior until explicit limits are saved.
			limit := bindings[id].MaxConcurrency
			if !unlimited && len(gate.subsets[account.ID]) > 0 && limit > remaining {
				break
			}
			gate.subsets[account.ID] = append(gate.subsets[account.ID], id)
			assigned[id] = true
			remaining -= limit
		}
		if gate.winner == 0 && len(gate.subsets[account.ID]) > 0 {
			gate.winner = account.ID
		}
	}
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
		if len(gate.subsets[accounts[i].ID]) == 0 {
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
	if owner > 0 && len(gate.subsets[owner]) == 0 {
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

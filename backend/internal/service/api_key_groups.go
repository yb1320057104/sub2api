package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"sort"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const MaxAPIKeyGroups = 20
const maxAPIKeyGroups = MaxAPIKeyGroups

// SelectedGroupIDs returns a copy and preserves the administrator's priority.
// Only legacy NULL rows inherit group_id; [] explicitly means ungrouped.
func (k *APIKey) SelectedGroupIDs() []int64 {
	if k == nil {
		return []int64{}
	}
	if k.GroupIDs != nil {
		return append([]int64{}, k.GroupIDs...)
	}
	if k.GroupID != nil && *k.GroupID > 0 {
		return []int64{*k.GroupID}
	}
	return []int64{}
}

func NormalizeAPIKeyGroupIDs(ids []int64) ([]int64, error) {
	if len(ids) > MaxAPIKeyGroups {
		return nil, infraerrors.BadRequest("API_KEY_GROUPS_INVALID", "API Key 最多选择 20 个分组")
	}
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, infraerrors.BadRequest("API_KEY_GROUPS_INVALID", "分组 ID 必须为正整数")
		}
		if !seen[id] {
			out, seen[id] = append(out, id), true
		}
	}
	return out, nil
}

func requestedAPIKeyGroups(primary *int64, ids []int64) ([]int64, error) {
	if ids == nil && primary != nil {
		ids = []int64{*primary}
	}
	groups, err := NormalizeAPIKeyGroupIDs(ids)
	if err != nil {
		return nil, err
	}
	if primary != nil && (len(groups) == 0 || groups[0] != *primary) {
		return nil, infraerrors.BadRequest("API_KEY_GROUPS_INVALID", "group_id 必须与 group_ids 的首项一致")
	}
	return groups, nil
}

func (s *APIKeyService) validateCandidateGroups(ctx context.Context, user *User, ids []int64) ([]*Group, error) {
	groups := make([]*Group, 0, len(ids))
	for _, id := range ids {
		group, err := s.groupRepo.GetByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("get group %d: %w", id, err)
		}
		if group == nil || group.Status != StatusActive {
			return nil, infraerrors.BadRequest("GROUP_NOT_ACTIVE", "target group is not active")
		}
		if !s.canUserBindGroup(ctx, user, group) {
			return nil, ErrGroupNotAllowed
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func setAPIKeyCandidateGroups(key *APIKey, ids []int64, groups []*Group) {
	key.GroupIDs, key.Groups = append([]int64{}, ids...), groups
	key.GroupID, key.Group = nil, nil
	if len(ids) > 0 {
		id := ids[0]
		key.GroupID = &id
		for _, group := range groups {
			if group != nil && group.ID == id {
				key.Group = group
				break
			}
		}
	}
}

// APIKeyGroupAvailability describes a snapshot, not a reservation. The selected
// group's normal admission and account scheduler still own the final checks.
type APIKeyGroupAvailability struct {
	Available bool
	Busy      bool
	Load      float64
}

type APIKeyGroupProbeRouter interface {
	ProbeAPIKeyGroup(context.Context, *Group, string, string) (APIKeyGroupAvailability, error)
}

// SetGroupRouter is called once during gateway route registration.
func (s *APIKeyService) SetGroupRouter(router APIKeyGroupProbeRouter) { s.groupRouter = router }

func (k *APIKey) ConfiguredGroupIDs() []int64 { return k.SelectedGroupIDs() }
func normalizeAPIKeyGroupIDs(primary *int64, ids []int64) ([]int64, error) {
	return requestedAPIKeyGroups(primary, ids)
}

// PinAPIKeyGroup preserves a request's chosen billing identity across queue/WS
// revalidation. Never reselect underneath a request that already reserved funds.
func (s *APIKeyService) PinAPIKeyGroup(ctx context.Context, key *APIKey, id int64) (*APIKey, error) {
	bound := false
	for _, candidate := range key.ConfiguredGroupIDs() {
		if candidate == id {
			bound = true
			break
		}
	}
	if !bound {
		return nil, infraerrors.ServiceUnavailable("API_KEY_GROUP_CHANGED", "API key groups changed; please retry")
	}
	group, err := s.groupRepo.GetByIDLite(ctx, id)
	if err != nil {
		return nil, err
	}
	if key.User == nil || !group.IsActive() || !s.canUserBindGroup(ctx, key.User, group) {
		return nil, ErrGroupNotAllowed
	}
	return s.apiKeyWithGroup(ctx, key, group)
}

func (s *APIKeyService) apiKeyWithGroup(ctx context.Context, key *APIKey, group *Group) (*APIKey, error) {
	copyKey := *key
	copyKey.ModelMapping = maps.Clone(key.ModelMapping)
	copyUser := *key.User
	copyKey.User = &copyUser
	id := group.ID
	copyKey.GroupID, copyKey.Group = &id, group
	copyUser.UserGroupRPMOverride = nil
	copyUser.UserGroupDeniedModels = nil
	if s.userGroupRateRepo != nil {
		var err error
		copyUser.UserGroupRPMOverride, err = s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, key.UserID, id)
		if err != nil {
			return nil, err
		}
		if err := s.loadUserGroupDeniedModels(ctx, &copyKey); err != nil {
			return nil, err
		}
	}
	return &copyKey, nil
}

// BillingGroups exposes only groups bound to this key and still permitted for
// its owner. Inactive groups remain visible with their status in the declaration.
func (s *APIKeyService) BillingGroups(ctx context.Context, key *APIKey) ([]*APIKey, error) {
	var out []*APIKey
	for _, id := range key.ConfiguredGroupIDs() {
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if errors.Is(err, ErrGroupNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !s.canUserBindGroup(ctx, key.User, group) {
			continue
		}
		copyKey := *key
		copyKey.ModelMapping = maps.Clone(key.ModelMapping)
		copyKey.GroupID, copyKey.Group = &group.ID, group
		out = append(out, &copyKey)
	}
	return out, nil
}

type apiKeyGroupCandidate struct {
	key          *APIKey
	rate         float64
	availability APIKeyGroupAvailability
}

// ModelListingGroups checks only read entitlements. It never selects a billing
// group, probes capacity, loads prices, or recovers credit. Each candidate owns
// its User snapshot so per-group restrictions cannot contaminate auth caches.
func (s *APIKeyService) ModelListingGroups(ctx context.Context, key *APIKey, accepts func(*APIKey) bool) ([]*APIKey, error) {
	if key == nil || key.User == nil || s.groupRepo == nil || s.userGroupRateRepo == nil || s.localAuth != nil {
		return nil, infraerrors.ServiceUnavailable("MODEL_LISTING_UNAVAILABLE", "Model listing authorization is unavailable")
	}
	var out []*APIKey
	for _, id := range key.ConfiguredGroupIDs() {
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if errors.Is(err, ErrGroupNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if group == nil || !group.IsActive() {
			continue
		}
		if group.IsSubscriptionType() {
			if s.userSubRepo == nil {
				return nil, infraerrors.ServiceUnavailable("MODEL_LISTING_UNAVAILABLE", "Subscription authorization is unavailable")
			}
			sub, err := s.userSubRepo.GetActiveByUserIDAndGroupID(ctx, key.UserID, id)
			if errors.Is(err, ErrSubscriptionNotFound) || errors.Is(err, ErrSubscriptionExpired) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if sub == nil || !sub.IsActive() {
				continue
			}
		} else if !key.User.CanBindGroup(id, group.IsExclusive) {
			continue
		}
		candidate, err := s.apiKeyWithGroup(ctx, key, group)
		if err != nil {
			return nil, err
		}
		if accepts == nil || accepts(candidate) {
			out = append(out, candidate)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group.ID < out[j].Group.ID })
	if len(out) == 0 {
		return nil, infraerrors.Forbidden("NO_ELIGIBLE_API_KEY_GROUP", "No selected group permits model listing")
	}
	return out, nil
}

// SelectRequestGroup checks candidates in the configured PK-042 priority order.
// Admission and account scheduling still own the final capacity reservation.
func (s *APIKeyService) SelectRequestGroup(ctx context.Context, key *APIKey, models []string, kind string, subscriptions *SubscriptionService, accepts func(*APIKey) bool, checkBilling bool) (*APIKey, error) {
	if key == nil || len(key.ConfiguredGroupIDs()) < 2 {
		return key, nil
	}
	if s.groupRouter == nil || s.localAuth != nil {
		return nil, infraerrors.ServiceUnavailable("MULTI_GROUP_ROUTING_UNAVAILABLE", "Multi-group routing is unavailable on this gateway")
	}
	var candidates []apiKeyGroupCandidate
	now := time.Now()
	var lastErr error
	for _, id := range key.ConfiguredGroupIDs() {
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if err != nil {
			if !errors.Is(err, ErrGroupNotFound) {
				return nil, err
			}
			continue
		}
		if !group.IsActive() {
			continue
		}
		if group.IsSubscriptionType() {
			if s.userSubRepo == nil {
				continue
			}
			sub, subErr := s.userSubRepo.GetActiveByUserIDAndGroupID(ctx, key.UserID, id)
			if subErr != nil {
				if !errors.Is(subErr, ErrSubscriptionNotFound) && !errors.Is(subErr, ErrSubscriptionExpired) {
					return nil, subErr
				}
				continue
			}
			if checkBilling && subscriptions != nil {
				// Validation adjusts expired windows in memory; normal auth performs
				// authoritative maintenance after the group is selected.
				copySub := *sub
				if _, subErr := subscriptions.ValidateAndCheckLimits(&copySub, group); subErr != nil {
					lastErr = subErr
					continue
				}
			}
		} else {
			if !key.User.CanBindGroup(id, group.IsExclusive) {
				continue
			}
		}
		candidate, err := s.apiKeyWithGroup(ctx, key, group)
		if err != nil {
			return nil, err
		}
		permitted := true
		for _, model := range models {
			if group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(model) {
				permitted = false
				break
			}
		}
		if !permitted || FirstUserGroupDeniedModel(candidate.DeniedModelsInGroup(), models) != "" || (accepts != nil && !accepts(candidate)) {
			continue
		}
		rate := group.RateMultiplier
		if s.userGroupRateRepo != nil {
			userRate, err := s.userGroupRateRepo.GetByUserAndGroup(ctx, key.UserID, id)
			if err != nil {
				return nil, err
			}
			if userRate != nil {
				rate = *userRate
			}
		}
		switch kind {
		case "image":
			rate = resolveImageRateMultiplier(candidate, rate)
		case "video":
			rate = resolveVideoRateMultiplier(candidate, rate)
		default:
			rate *= group.PeakMultiplierAt(now)
		}
		if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
			continue
		}
		candidates = append(candidates, apiKeyGroupCandidate{key: candidate, rate: rate})
	}
	if len(candidates) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, infraerrors.Forbidden("NO_ELIGIBLE_API_KEY_GROUP", "No selected group permits this request or has usable billing entitlement")
	}
	var best *apiKeyGroupCandidate
	for i := range candidates {
		candidate := &candidates[i]
		model := ""
		if len(models) > 0 {
			model = models[0]
		}
		availability, err := s.groupRouter.ProbeAPIKeyGroup(ctx, candidate.key.Group, model, kind)
		if err != nil {
			lastErr = err
			continue
		}
		if !availability.Available {
			continue
		}
		candidate.availability = availability
		best = candidate
		break
	}
	if best == nil {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, infraerrors.ServiceUnavailable("NO_AVAILABLE_API_KEY_GROUP", "No selected group currently has accounts available for this model")
	}
	return best.key, nil
}

func apiKeyGroupLess(a, b apiKeyGroupCandidate) bool {
	if a.availability.Busy != b.availability.Busy {
		return !a.availability.Busy
	}
	if a.rate != b.rate {
		return a.rate < b.rate
	}
	return a.availability.Load < b.availability.Load
}

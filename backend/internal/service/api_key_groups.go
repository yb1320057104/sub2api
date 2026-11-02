package service

import (
	"context"
	"fmt"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const MaxAPIKeyGroups = 20

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

package repository

import (
	"context"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func cloneAPIKeyGroupIDs(ids []int64) []int64 {
	if ids == nil {
		return nil
	}
	return append([]int64{}, ids...)
}

func apiKeyHasGroup(groupID int64) predicate.APIKey {
	return func(s *entsql.Selector) {
		s.Where(entsql.Or(
			entsql.And(entsql.IsNull(s.C(apikey.FieldGroupIds)), entsql.EQ(s.C(apikey.FieldGroupID), groupID)),
			sqljson.ValueContains(s.C(apikey.FieldGroupIds), groupID),
		))
	}
}

func apiKeyHasNoGroup() predicate.APIKey {
	return func(s *entsql.Selector) {
		s.Where(entsql.Or(
			entsql.And(entsql.IsNull(s.C(apikey.FieldGroupIds)), entsql.IsNull(s.C(apikey.FieldGroupID))),
			sqljson.LenEQ(s.C(apikey.FieldGroupIds), 0),
		))
	}
}

func (r *apiKeyRepository) apiKeyEntityWithGroups(ctx context.Context, entity *dbent.APIKey) (*service.APIKey, error) {
	key := apiKeyEntityToService(entity)
	keys := []service.APIKey{*key}
	if err := r.attachCandidateGroups(ctx, keys); err != nil {
		return nil, err
	}
	return &keys[0], nil
}

// Batch only candidates missing from the eager-loaded primary relation. A
// legacy one-group key keeps its existing authentication query cost.
func (r *apiKeyRepository) attachCandidateGroups(ctx context.Context, keys []service.APIKey) error {
	known := make(map[int64]*service.Group)
	missing := make(map[int64]bool)
	for i := range keys {
		if keys[i].Group != nil {
			known[keys[i].Group.ID] = keys[i].Group
		}
	}
	for i := range keys {
		for _, id := range keys[i].SelectedGroupIDs() {
			if known[id] == nil {
				missing[id] = true
			}
		}
	}
	if len(missing) > 0 {
		ids := make([]int64, 0, len(missing))
		for id := range missing {
			ids = append(ids, id)
		}
		entities, err := clientFromContext(ctx, r.client).Group.Query().Where(group.IDIn(ids...), group.DeletedAtIsNil()).All(ctx)
		if err != nil {
			return err
		}
		for _, entity := range entities {
			known[entity.ID] = groupEntityToService(entity)
		}
	}
	for i := range keys {
		keys[i].Groups = make([]*service.Group, 0, len(keys[i].SelectedGroupIDs()))
		for _, id := range keys[i].SelectedGroupIDs() {
			if value := known[id]; value != nil {
				keys[i].Groups = append(keys[i].Groups, value)
			}
		}
	}
	return nil
}

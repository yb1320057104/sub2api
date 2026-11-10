package repository

import (
	"context"
	"errors"
	"fmt"

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

// Keep upstream callers on the same JSON-authoritative membership predicate.
func apiKeyInGroup(groupID int64) predicate.APIKey { return apiKeyHasGroup(groupID) }

func (r *apiKeyRepository) attachGroupIDs(ctx context.Context, keys []service.APIKey) error {
	return r.attachCandidateGroups(ctx, keys)
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

func (r *apiKeyRepository) mutateWithGroups(ctx context.Context, key *service.APIKey, mutate func(context.Context) error) error {
	if dbent.TxFromContext(ctx) != nil {
		if err := mutate(ctx); err != nil {
			return err
		}
		return r.replaceExtraGroups(ctx, key)
	}
	tx, err := r.client.Tx(ctx)
	if errors.Is(err, dbent.ErrTxStarted) {
		if err := mutate(ctx); err != nil {
			return err
		}
		return r.replaceExtraGroups(ctx, key)
	}
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	if err := mutate(txCtx); err != nil {
		return err
	}
	if err := r.replaceExtraGroups(txCtx, key); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *apiKeyRepository) replaceExtraGroups(ctx context.Context, key *service.APIKey) error {
	client := clientFromContext(ctx, r.client)
	if _, err := client.ExecContext(ctx, `DELETE FROM api_key_groups WHERE api_key_id = $1`, key.ID); err != nil {
		return err
	}
	if key.GroupID == nil {
		return nil
	}
	for _, id := range key.GroupIDs {
		if key.GroupID != nil && *key.GroupID == id {
			continue
		}
		if _, err := client.ExecContext(ctx, `INSERT INTO api_key_groups (api_key_id, group_id) VALUES ($1, $2)`, key.ID, id); err != nil {
			return fmt.Errorf("save API key groups: %w", err)
		}
	}
	return nil
}

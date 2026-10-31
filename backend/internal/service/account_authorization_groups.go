package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// The workbench stores passing destinations separately from legacy scheduler
// group_ids. An explicit empty selection never revives the legacy selection.
func authorizationSavedPassGroups(ctx context.Context, tx *sql.Tx, legacy []int64) ([]int64, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, authorizationWorkbenchSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return append([]int64{}, legacy...), nil
	}
	if err != nil {
		return nil, err
	}
	var saved struct {
		PassGroupIDs *[]int64 `json:"pass_group_ids"`
	}
	if err = json.Unmarshal([]byte(raw), &saved); err != nil {
		return nil, err
	}
	if saved.PassGroupIDs == nil {
		return append([]int64{}, legacy...), nil
	}
	return append([]int64{}, (*saved.PassGroupIDs)...), nil
}

// Existing live memberships stay the pass destination for reauthorization.
// An ungrouped account falls back to the same saved choice shown in the workbench.
func authorizationAccountPassGroups(ctx context.Context, tx *sql.Tx, accountID *int64, legacy []int64) ([]int64, error) {
	if accountID != nil {
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(ag.group_id ORDER BY ag.group_id),'[]'::jsonb) FROM account_groups ag JOIN groups g ON g.id=ag.group_id AND g.deleted_at IS NULL WHERE ag.account_id=$1`, *accountID).Scan(&raw)
		if err != nil {
			return nil, err
		}
		var ids []int64
		if err = json.Unmarshal(raw, &ids); err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			return ids, nil
		}
	}
	return authorizationSavedPassGroups(ctx, tx, legacy)
}

// Older manual retries only stored their parent job ID. Follow that chain to
// retain the original account-group policy without rewriting historical jobs.
func authorizationRetryOrigin(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	var reauthorization bool
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE origins AS (
        SELECT id,source,settings FROM pk_authorization_jobs WHERE id=$1
        UNION ALL
        SELECT parent.id,parent.source,parent.settings FROM pk_authorization_jobs parent
        JOIN origins child ON parent.id=NULLIF(child.settings->>'retry_of_job_id','')::bigint AND parent.id<child.id
        WHERE child.source<>'reauthorization' AND COALESCE(child.settings->>'reauthorization_retry','false')<>'true' AND COALESCE(child.settings->>'immediate_authorization','false')<>'true'
    ) SELECT EXISTS(SELECT 1 FROM origins WHERE source='reauthorization' OR settings->>'reauthorization_retry'='true' OR settings->>'immediate_authorization'='true')`, id).Scan(&reauthorization)
	return reauthorization, err
}

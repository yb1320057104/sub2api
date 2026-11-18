package repository

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/ent/account"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Authentication owns an extra-state quarantine, never the operator's status,
// scheduling switch or independent rate limits. Legacy auth errors are cleared
// only while their exact error remains; their scheduling switch is not inferred.
const openAIRecoveredStatusSQL = `CASE WHEN a.status='error' AND (
 a.error_message LIKE 'Token revoked (401):%' OR a.error_message LIKE 'Unauthorized (401):%'
 OR a.error_message LIKE 'Authentication failed (401):%' OR a.error_message LIKE 'OAuth 401%'
 OR a.error_message LIKE 'Token refresh failed (non-retryable):%'
 OR a.error_message='openai access_token expired and refresh_token is missing') THEN 'active' ELSE a.status END`
const openAIRecoveredErrorSQL = `CASE WHEN ` + openAIRecoveredStatusSQL + `='active' AND a.status='error' THEN '' ELSE a.error_message END`
const openAIRecoveredTempUntilSQL = `CASE WHEN a.temp_unschedulable_reason LIKE 'OAuth 401:%' OR a.temp_unschedulable_reason LIKE 'Authentication failed (401):%' OR a.temp_unschedulable_reason LIKE 'token refresh retry exhausted:%' THEN NULL ELSE a.temp_unschedulable_until END`
const openAIRecoveredTempReasonSQL = `CASE WHEN a.temp_unschedulable_reason LIKE 'OAuth 401:%' OR a.temp_unschedulable_reason LIKE 'Authentication failed (401):%' OR a.temp_unschedulable_reason LIKE 'token refresh retry exhausted:%' THEN NULL ELSE a.temp_unschedulable_reason END`

const openAIWakeQueuedSQL = `, awakened_tests AS (
 UPDATE account_tests AS test SET available_at=LEAST(test.available_at,NOW()),queue_reason=''
 FROM updated WHERE test.account_id=updated.id AND test.status='queued' AND NOT test.cancel_requested
 RETURNING test.id
) INSERT INTO scheduler_outbox(event_type,account_id) SELECT $4,id FROM updated`

func (r *accountRepository) finishOpenAILifecycle(ctx context.Context, id int64, affected int64, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	if affected > 0 {
		r.syncSchedulerAccountSnapshotDetached(ctx, id)
	}
	return affected > 0, nil
}

// Token validation needs the current row, not the management view's group and
// proxy joins. RefreshIfNeeded rereads the complete account before any send.
func (r *accountRepository) GetOpenAITokenState(ctx context.Context, id int64) (*service.Account, error) {
	row, err := observerAccountQuery(ctx, r.client.Account.Query()).Where(account.IDEQ(id)).Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrAccountNotFound, nil)
	}
	return accountEntityToService(row), nil
}

func (r *accountRepository) ApplyOpenAIRefresh(ctx context.Context, id int64, expected map[string]any, proxy *int64, next map[string]any) (bool, error) {
	before, err := json.Marshal(normalizeJSONMap(expected))
	if err != nil {
		return false, err
	}
	after, err := json.Marshal(normalizeJSONMap(next))
	if err != nil {
		return false, err
	}
	query := `WITH updated AS (
 UPDATE accounts a SET credentials=jsonb_set($3::jsonb,'{_token_version}',to_jsonb(GREATEST(
 COALESCE((a.credentials->>'_token_version')::bigint,0)+1,(extract(epoch from clock_timestamp())*1000)::bigint))),
 extra=COALESCE(a.extra,'{}'::jsonb)-'openai_auth_recovery',
 status=` + openAIRecoveredStatusSQL + `,error_message=` + openAIRecoveredErrorSQL + `,
 temp_unschedulable_until=` + openAIRecoveredTempUntilSQL + `,temp_unschedulable_reason=` + openAIRecoveredTempReasonSQL + `,
 updated_at=clock_timestamp()
 WHERE a.id=$1 AND a.deleted_at IS NULL AND a.platform='openai' AND a.type='oauth'
 AND a.credentials=$2::jsonb AND a.proxy_id IS NOT DISTINCT FROM $5 RETURNING a.id
)` + openAIWakeQueuedSQL
	result, err := r.sql.ExecContext(ctx, query, id, string(before), string(after), service.SchedulerOutboxEventAccountChanged, proxy)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return r.finishOpenAILifecycle(ctx, id, n, err)
}

func (r *accountRepository) RecoverVerifiedOpenAIAuth(ctx context.Context, id int64, expected map[string]any, generation string) (bool, error) {
	credentials, err := json.Marshal(normalizeJSONMap(expected))
	if err != nil {
		return false, err
	}
	query := `WITH updated AS (
 UPDATE accounts a SET extra=COALESCE(a.extra,'{}'::jsonb)-'openai_auth_recovery',
 status=` + openAIRecoveredStatusSQL + `,error_message=` + openAIRecoveredErrorSQL + `,
 temp_unschedulable_until=` + openAIRecoveredTempUntilSQL + `,temp_unschedulable_reason=` + openAIRecoveredTempReasonSQL + `,updated_at=clock_timestamp()
 WHERE a.id=$1 AND a.deleted_at IS NULL AND a.platform='openai' AND a.type='oauth' AND a.credentials=$2::jsonb
 AND COALESCE(a.extra->'openai_auth_recovery'->>'generation','')=$3
 AND (a.extra ? 'openai_auth_recovery' OR ` + openAIRecoveredStatusSQL + ` IS DISTINCT FROM a.status
 OR ` + openAIRecoveredTempUntilSQL + ` IS DISTINCT FROM a.temp_unschedulable_until) RETURNING a.id
)` + openAIWakeQueuedSQL
	result, err := r.sql.ExecContext(ctx, query, id, string(credentials), generation, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return r.finishOpenAILifecycle(ctx, id, n, err)
}

func (r *accountRepository) MarkOpenAIRefreshFailure(ctx context.Context, id int64, expected map[string]any, proxy *int64, terminal bool, until time.Time, reason string) (bool, error) {
	credentials, err := json.Marshal(normalizeJSONMap(expected))
	if err != nil {
		return false, err
	}
	return r.markOpenAIAuthState(ctx, id, expected["access_token"], string(credentials), proxy, terminal, until, reason)
}

func (r *accountRepository) markOpenAIAuthState(ctx context.Context, id int64, accessToken any, expectedJSON string, proxy *int64, terminal bool, until time.Time, reason string) (bool, error) {
	token, _ := accessToken.(string)
	// All recovery paths acquire guard before account. Reauth uses task->account
	// and budget uses guard->task, so this order cannot close a three-row lock ring.
	query := `WITH guard_lock AS MATERIALIZED (
 SELECT account_id FROM account_token_guard_v2_accounts WHERE account_id=$1 FOR UPDATE
), rejected AS (
 UPDATE accounts a SET extra=jsonb_set(COALESCE(a.extra,'{}'::jsonb),'{openai_auth_recovery}',
 jsonb_build_object('generation',$3::text,'access_token_digest',$4::text,'requires_relogin',($5::boolean OR COALESCE(a.extra->'openai_auth_recovery'->>'access_token_digest'=$4 AND a.extra->'openai_auth_recovery'->>'requires_relogin'='true',false)),
 'retry_at',$6::timestamptz,'reason',$7::text,'observed_at',clock_timestamp())),updated_at=clock_timestamp()
 WHERE a.id=$1 AND a.deleted_at IS NULL AND a.platform='openai' AND a.type='oauth'
 AND a.credentials->>'access_token'=$2 AND ($8::jsonb IS NULL OR (a.credentials=$8::jsonb AND a.proxy_id IS NOT DISTINCT FROM $9))
 AND (SELECT count(*) FROM guard_lock)>=0 RETURNING a.id
), due AS (
 UPDATE account_token_guard_v2_accounts g SET next_probe_at=LEAST(next_probe_at,NOW()),updated_at=NOW()
 FROM rejected a WHERE g.account_id=a.id AND g.enabled RETURNING g.account_id
) INSERT INTO scheduler_outbox(event_type,account_id) SELECT $10,id FROM rejected`
	var expected any
	if expectedJSON != "" {
		expected = expectedJSON
	}
	return r.mutateKiloSourceHealth(ctx, id, func(opCtx context.Context, scoped *accountRepository, callerOwned bool, _ bool) (bool, error) {
		authQuery := query
		if callerOwned {
			// The caller may already own the source row; never wait backwards
			// for a guard held by a guard->task->source recovery transaction.
			authQuery = strings.Replace(authQuery, "FOR UPDATE\n), rejected", "FOR UPDATE NOWAIT\n), rejected", 1)
		}
		result, err := scoped.sql.ExecContext(opCtx, authQuery, id, token, newOpenAIRecoveryGeneration(), service.OpenAIAccessTokenDigest(token), terminal, until, reason, expected, proxy, service.SchedulerOutboxEventAccountChanged)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		return n > 0, err
	})
}

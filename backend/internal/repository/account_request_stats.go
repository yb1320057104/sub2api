package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/lib/pq"
)

// GetAccountRequestStatsBatch reads completion-time records, not a rolling
// average or today-only window. Lateral reads use the existing account/time
// indexes; only accounts on the requested page are queried.
func (r *usageLogRepository) GetAccountRequestStatsBatch(ctx context.Context, accountIDs []int64) (map[int64]*usagestats.AccountRequestStats, error) {
	ids := normalizePositiveInt64IDs(accountIDs)
	result := make(map[int64]*usagestats.AccountRequestStats, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.sql.QueryContext(ctx, accountRequestStatsSQL, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var firstToken sql.NullInt64
		var completedAt, failureAt sql.NullTime
		stats := &usagestats.AccountRequestStats{}
		if err := rows.Scan(&id, &firstToken, &completedAt, &stats.ConsecutiveUpstreamFailures, &failureAt); err != nil {
			return nil, err
		}
		if firstToken.Valid {
			ms := int(firstToken.Int64)
			stats.LatestFirstTokenMs = &ms
		}
		if completedAt.Valid {
			stats.LatestCompletedAt = &completedAt.Time
		}
		if failureAt.Valid {
			stats.LastUpstreamFailureAt = &failureAt.Time
		}
		result[id] = stats
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// A positive price is not required for success: free/multiplier-zero requests
// can carry real usage. Exclude failed placeholders, policy rows, and partial
// billed streams with a correlated terminal error. Recovered 2xx telemetry is
// not a failed request. Ignore local errors rather than resetting the streak.
const accountRequestStatsSQL = `
SELECT requested.id, latest.first_response_ms, latest.created_at,
       failures.failure_count, failures.last_failure_at
FROM unnest($1::bigint[]) AS requested(id)
LEFT JOIN LATERAL (
    SELECT ul.first_response_ms, ul.created_at
    FROM usage_logs ul
    WHERE ul.account_id = requested.id
      AND COALESCE(ul.request_type, 0) NOT IN (4, 6)
      AND (ul.actual_cost > 0 OR ul.total_cost > 0
           OR ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens > 0
           OR ul.first_token_ms IS NOT NULL
           OR COALESCE(ul.image_output_tokens, 0) > 0 OR COALESCE(ul.image_count, 0) > 0)
      AND NOT EXISTS (
          SELECT 1 FROM ops_error_logs terminal
          WHERE terminal.account_id = ul.account_id
            AND (terminal.api_key_id IS NULL OR terminal.api_key_id = ul.api_key_id)
            AND terminal.status_code >= 400
            AND NOT terminal.is_count_tokens
            AND NULLIF(ul.request_id, '') IS NOT NULL
            AND (terminal.request_id = ul.request_id
                 OR terminal.client_request_id = ul.request_id
                 OR (ul.request_id LIKE 'local:%' AND terminal.request_id = substring(ul.request_id FROM 7))
                 OR (ul.request_id LIKE 'client:%' AND terminal.client_request_id = substring(ul.request_id FROM 8)))
      )
    ORDER BY ul.created_at DESC, ul.id DESC
    LIMIT 1
) latest ON TRUE
LEFT JOIN LATERAL (
    SELECT COUNT(*) AS failure_count, MAX(dedup.created_at) AS last_failure_at
    FROM (
        SELECT MAX(e.created_at) AS created_at
        FROM ops_error_logs e
        WHERE e.account_id = requested.id
          AND e.created_at > COALESCE(latest.created_at, '-infinity'::timestamptz)
          AND e.status_code >= 400 AND e.status_code <> 499
          AND e.error_owner = 'provider'
          AND e.error_phase IN ('upstream', 'network', 'account_auth')
          AND NOT e.is_business_limited AND NOT e.is_count_tokens
        GROUP BY e.api_key_id, COALESCE(NULLIF(e.request_id, ''), NULLIF(e.client_request_id, ''), 'error:' || e.id::text)
    ) dedup
) failures ON TRUE
`

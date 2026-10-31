package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// GetAccountHealthStats runs for one queued account only, with a bounded window
// and caller timeout. HTTP list/radar handlers only read the stored snapshot.
func (r *usageLogRepository) GetAccountHealthStats(ctx context.Context, accountID int64, start, end time.Time) (*usagestats.AccountHealthStats, error) {
	stats := &usagestats.AccountHealthStats{WindowStart: start, WindowEnd: end}
	var fastest, slowest sql.NullInt64
	err := scanSingleRow(ctx, r.sql, accountHealthSQL, []any{accountID, start, end}, &stats.SuccessRequests, &stats.UpstreamFailures, &fastest, &slowest)
	if err != nil {
		return nil, err
	}
	stats.TotalRequests = stats.SuccessRequests + stats.UpstreamFailures
	if stats.TotalRequests > 0 {
		score := 100 * float64(stats.SuccessRequests) / float64(stats.TotalRequests)
		stats.HealthScore = &score
	}
	if fastest.Valid {
		n := int(fastest.Int64)
		stats.FastestFirstTokenMs = &n
	}
	if slowest.Valid {
		n := int(slowest.Int64)
		stats.SlowestFirstTokenMs = &n
	}
	stats.ComputedAt = time.Now()
	return stats, nil
}

// Keep success and terminal-error attribution aligned with account_request_stats.go.
// Zero-price successes count. Partial billed failures and platform errors do not.
const accountHealthSQL = `
WITH successes AS (
 SELECT ul.first_response_ms FROM usage_logs ul
 WHERE ul.account_id=$1 AND ul.created_at >= $2 AND ul.created_at < $3
 AND COALESCE(ul.request_type,0) NOT IN (4,6)
 AND (ul.actual_cost>0 OR ul.total_cost>0 OR ul.first_token_ms IS NOT NULL
      OR ul.input_tokens+ul.output_tokens+ul.cache_creation_tokens+ul.cache_read_tokens>0
      OR COALESCE(ul.image_output_tokens,0)>0 OR COALESCE(ul.image_count,0)>0)
 AND NOT EXISTS (
   SELECT 1 FROM ops_error_logs terminal WHERE terminal.account_id=ul.account_id
   AND (terminal.api_key_id IS NULL OR terminal.api_key_id=ul.api_key_id)
   AND terminal.status_code>=400 AND NOT terminal.is_count_tokens
   AND NULLIF(ul.request_id,'') IS NOT NULL
   AND (terminal.request_id=ul.request_id OR terminal.client_request_id=ul.request_id
     OR (ul.request_id LIKE 'local:%' AND terminal.request_id=substring(ul.request_id FROM 7))
     OR (ul.request_id LIKE 'client:%' AND terminal.client_request_id=substring(ul.request_id FROM 8)))
 )
), failures AS (
 SELECT 1 FROM ops_error_logs e
 WHERE e.account_id=$1 AND e.created_at >= $2 AND e.created_at < $3
 AND e.status_code>=400 AND e.status_code<>499 AND e.error_owner='provider'
 AND e.error_phase IN ('upstream','network','account_auth')
 AND NOT e.is_business_limited AND NOT e.is_count_tokens
 GROUP BY e.api_key_id,COALESCE(NULLIF(e.request_id,''),NULLIF(e.client_request_id,''),'error:'||e.id::text)
)
SELECT (SELECT COUNT(*) FROM successes),(SELECT COUNT(*) FROM failures),
       (SELECT MIN(first_response_ms) FROM successes WHERE first_response_ms>=0),(SELECT MAX(first_response_ms) FROM successes WHERE first_response_ms>=0)
`

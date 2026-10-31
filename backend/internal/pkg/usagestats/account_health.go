package usagestats

import "time"

// AccountHealthStats is a durable, passive snapshot; an empty sample has no score.
type AccountHealthStats struct {
	SuccessRequests     int64     `json:"success_requests"`
	UpstreamFailures    int64     `json:"upstream_failures"`
	TotalRequests       int64     `json:"total_requests"`
	HealthScore         *float64  `json:"health_score"`
	FastestFirstTokenMs *int      `json:"fastest_first_token_ms"`
	SlowestFirstTokenMs *int      `json:"slowest_first_token_ms"`
	WindowStart         time.Time `json:"window_start"`
	WindowEnd           time.Time `json:"window_end"`
	ComputedAt          time.Time `json:"computed_at"`
}

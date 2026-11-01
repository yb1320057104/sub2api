package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"
)

// countRadarEmailAccounts observes the current account rows in one database
// snapshot. It never probes upstreams or scans request/usage logs. Conditions
// are combined with AND; an unobserved quota is not assumed to be unused.
// conn is the runner's session-lock connection, so counting does not borrow a
// second pool connection while the first remains occupied by the same cycle.
func (s *AccountCandyTestService) countRadarEmailAccounts(ctx context.Context, conn *sql.Conn, conditions RadarEmailAlertConditions) (*RadarEmailAlertCounts, error) {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now()
	args := []any{now}
	qualifiedSQL, joins := "TRUE", ""
	if conditions.Qualified {
		// The existing verdict expression binds the current generation at $2.
		// An uninitialized radar has no current qualified verdict; counting must
		// not initialize or alter radar settings as a side effect.
		var generation string
		err = tx.QueryRowContext(ctx, `SELECT generation FROM pk_radar_account_config WHERE id=1`).Scan(&generation)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		qualifiedSQL = "FALSE"
		if err == nil {
			args = append(args, generation)
			qualifiedSQL = `COALESCE((` + radarObservedVerdictSQL + `)='pass',false)`
			joins = ` LEFT JOIN pk_radar_account_state st ON st.account_id=a.id
				LEFT JOIN pk_radar_account_runs rr ON rr.id=COALESCE(st.active_run_id,st.latest_run_id)` + authorizationRadarAccountJoin
		}
	}

	extraSQL := `'{}'::jsonb`
	if conditions.QuotaBelow100 {
		extraSQL = `COALESCE(a.extra,'{}'::jsonb)`
	}
	// Native OAuth 401s may be represented either by a permanent error or an
	// active temporary cooldown. Ignore stale error text after the cooldown.
	const not401SQL = `NOT (
		(COALESCE(a.status='error',false) AND COALESCE(a.error_message,'') ~ '(^|[^0-9])401([^0-9]|$)')
		OR (COALESCE(a.temp_unschedulable_until>$1,false) AND COALESCE(a.temp_unschedulable_reason,'') ~ '(^|[^0-9])401([^0-9]|$)')
	)`
	rows, err := tx.QueryContext(ctx, `SELECT a.platform,a.type,`+extraSQL+`,`+qualifiedSQL+`,`+not401SQL+`
		FROM accounts a`+joins+` WHERE a.deleted_at IS NULL`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	counts := &RadarEmailAlertCounts{}
	for rows.Next() {
		var platform, accountType string
		var extraRaw []byte
		var qualified, not401 bool
		if err = rows.Scan(&platform, &accountType, &extraRaw, &qualified, &not401); err != nil {
			return nil, err
		}
		counts.TotalAccounts++
		quotaMatches := true
		if conditions.QuotaBelow100 {
			var known bool
			quotaMatches, known = radarEmailQuotaBelow100(platform, accountType, extraRaw, now)
			if !known {
				counts.UnknownQuotaAccounts++
			}
		}
		if quotaMatches && (!conditions.Qualified || qualified) && (!conditions.Not401 || not401) {
			counts.MatchedAccounts++
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return counts, nil
}

func radarEmailQuotaBelow100(platform, accountType string, extraRaw []byte, now time.Time) (matches, known bool) {
	// This is the native Codex quota path. Other account types/platforms have
	// no supported comparable persisted percentage and remain unknown.
	if platform != PlatformOpenAI || accountType != AccountTypeOAuth {
		return false, false
	}
	var extra map[string]any
	if json.Unmarshal(extraRaw, &extra) != nil || len(extra) == 0 {
		return false, false
	}
	if synthetic, _ := extra["synthetic_ui_test"].(bool); synthetic {
		return false, false
	}
	// Spark shadows persist their own codex_bengalfox quota under codex_* on
	// the shadow row; substituting the parent's global quota would be wrong.
	window5h, window7d := openAICanonicalQuotaWindows(extra, now)
	matches = true
	for _, window := range []openAICanonicalQuotaWindow{window5h, window7d} {
		if !window.hasUsed {
			continue
		}
		if math.IsNaN(window.usedPercent) || math.IsInf(window.usedPercent, 0) || window.usedPercent < 0 {
			return false, false
		}
		known = true
		// Match the native display: an observed window becomes zero after its
		// persisted reset time, without inventing a fresh usage observation.
		if !window.reset && window.usedPercent >= 100 {
			matches = false
		}
	}
	return matches && known, known
}

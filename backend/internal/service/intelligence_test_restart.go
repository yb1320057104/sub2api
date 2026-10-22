package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// The generation is persistence metadata only. Keeping it outside the public
// configuration type's JSON fields prevents clients from selecting an epoch.
type intelligenceTestStoredConfig struct {
	*IntelligenceTestConfig
	Generation string `json:"_generation"`
}

func decodeIntelligenceTestConfig(raw string) (*IntelligenceTestConfig, error) {
	cfg := defaultIntelligenceTestConfig()
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	stored := intelligenceTestStoredConfig{IntelligenceTestConfig: cfg}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, errors.New("invalid intelligence test configuration")
	}
	cfg.generation = stored.Generation
	if err := validateIntelligenceTestConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func intelligenceTestConfigInTransaction(ctx context.Context, tx *sql.Tx) (*IntelligenceTestConfig, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, intelligenceTestSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultIntelligenceTestConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	return decodeIntelligenceTestConfig(raw)
}

func sameIntelligenceTestConfig(a, b *IntelligenceTestConfig) bool {
	if a == nil || b == nil || a.generation != b.generation {
		return false
	}
	// Also compare public settings for legacy rows without generation metadata.
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && string(left) == string(right)
}

// The caller holds the claim advisory lock. New selected groups start due now;
// existing leases and their result IDs remain owned by their original workers.
func ensureIntelligenceTestState(ctx context.Context, tx *sql.Tx, cfg *IntelligenceTestConfig) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO pk_intelligence_test_state(group_id,mode,interval_minutes)
		SELECT g.id,m.mode,CASE WHEN m.mode='svg' THEN $1::integer ELSE $2::integer END
		FROM groups g JOIN pk_intelligence_test_keys k ON k.group_id=g.id
		CROSS JOIN (VALUES ('svg'),('candy')) AS m(mode)
		WHERE g.status='active' AND g.deleted_at IS NULL ON CONFLICT DO NOTHING`, cfg.SVG.IntervalMinutes, cfg.Candy.IntervalMinutes)
	return err
}

// UpdateConfig atomically replaces the configuration and discards all history.
// Every successful save receives a new epoch, including identical settings.
func (s *IntelligenceTestService) UpdateConfig(ctx context.Context, cfg *IntelligenceTestConfig) error {
	if err := validateIntelligenceTestConfig(cfg); err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errors.New("cannot create intelligence test configuration generation")
	}
	generation := hex.EncodeToString(random[:])
	data, err := json.Marshal(intelligenceTestStoredConfig{IntelligenceTestConfig: cfg, Generation: generation})
	if err != nil {
		return err
	}
	queryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(queryCtx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(queryCtx, `SELECT pg_advisory_xact_lock(706832,1)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(queryCtx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW())
		ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`, intelligenceTestSettingKey, string(data)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(queryCtx, `DELETE FROM pk_intelligence_test_results`); err != nil {
		return err
	}
	if err = ensureIntelligenceTestState(queryCtx, tx, cfg); err != nil {
		return err
	}
	if _, err = tx.ExecContext(queryCtx, `UPDATE pk_intelligence_test_state SET next_run_at=NOW(),
		interval_minutes=CASE WHEN mode='svg' THEN $1::integer ELSE $2::integer END`, cfg.SVG.IntervalMinutes, cfg.Candy.IntervalMinutes); err != nil {
		return err
	}
	// Snapshot while the claim lock still fences out this generation's new jobs.
	// A delayed post-commit callback must not cancel work already started by a
	// newer save, or by the scheduler immediately after this commit.
	activeCancels := s.activeIntelligenceTestCancels()
	if err = tx.Commit(); err != nil {
		return err
	}
	cfg.generation = generation
	for _, cancelActive := range activeCancels {
		cancelActive()
	}
	s.wakeIntelligenceTests()
	return nil
}

func (s *IntelligenceTestService) activeIntelligenceTestCancels() []context.CancelFunc {
	s.mu.Lock()
	defer s.mu.Unlock()
	cancels := make([]context.CancelFunc, 0, len(s.active))
	for _, cancel := range s.active {
		cancels = append(cancels, cancel)
	}
	return cancels
}

func (s *IntelligenceTestService) wakeIntelligenceTests() {
	if s == nil || s.wake == nil {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

type intelligenceTestJob struct {
	groupID int64
	mode    string
}

// Planning/reset take the exclusive side of the claims' shared gate. An older
// planner cannot postpone a reset with stale settings, while independent pairs
// can claim together after this brief planning transaction commits.
func (s *IntelligenceTestService) planIntelligenceTests(ctx context.Context) (*IntelligenceTestConfig, int64, []intelligenceTestJob, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(queryCtx, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(queryCtx, `SELECT pg_advisory_xact_lock(706832,1)`); err != nil {
		return nil, 0, nil, err
	}
	cfg, err := intelligenceTestConfigInTransaction(queryCtx, tx)
	if err != nil {
		return nil, 0, nil, err
	}
	// A lost worker cannot manufacture a measured duration or an answer. Reset
	// removes old rows entirely, so this cleanup never recreates erased history.
	if _, err = tx.ExecContext(queryCtx, `UPDATE pk_intelligence_test_results r SET status='failed',finished_at=NOW(),duration_ms=NULL,error_code='worker_interrupted',error_detail='任务执行中断或租约已过期，未收到最终结果' WHERE r.status='running' AND NOT EXISTS (SELECT 1 FROM pk_intelligence_test_state s WHERE s.group_id=r.group_id AND s.mode=r.mode AND s.active_result_id=r.id AND s.lease_until>NOW())`); err != nil {
		return nil, 0, nil, err
	}
	if _, err = tx.ExecContext(queryCtx, `DELETE FROM pk_intelligence_test_results r USING groups g WHERE r.group_id=g.id AND g.deleted_at IS NOT NULL`); err != nil {
		return nil, 0, nil, err
	}
	// A soft-deleted group can still have a cancelling request in another
	// process. Keep its pair's live lease until the owning worker releases it.
	if _, err = tx.ExecContext(queryCtx, `DELETE FROM pk_intelligence_test_state s USING groups g WHERE s.group_id=g.id AND g.deleted_at IS NOT NULL AND (s.lease_until IS NULL OR s.lease_until<=NOW())`); err != nil {
		return nil, 0, nil, err
	}
	if !cfg.Enabled || (!cfg.SVG.Enabled && !cfg.Candy.Enabled) {
		if err = tx.Commit(); err != nil {
			return nil, 0, nil, err
		}
		return cfg, 0, nil, nil
	}
	if err = ensureIntelligenceTestState(queryCtx, tx, cfg); err != nil {
		return nil, 0, nil, err
	}
	if _, err = tx.ExecContext(queryCtx, `UPDATE pk_intelligence_test_state SET next_run_at=NOW(),interval_minutes=CASE WHEN mode='svg' THEN $1::integer ELSE $2::integer END WHERE interval_minutes<>(CASE WHEN mode='svg' THEN $1::integer ELSE $2::integer END)`, cfg.SVG.IntervalMinutes, cfg.Candy.IntervalMinutes); err != nil {
		return nil, 0, nil, err
	}
	var keyRevision int64
	if err = tx.QueryRowContext(queryCtx, `SELECT COALESCE(MAX(api_key_id),0) FROM pk_intelligence_test_keys`).Scan(&keyRevision); err != nil {
		return nil, 0, nil, err
	}
	rows, err := tx.QueryContext(queryCtx, `SELECT s.group_id,s.mode FROM pk_intelligence_test_state s JOIN groups g ON g.id=s.group_id JOIN pk_intelligence_test_keys k ON k.group_id=s.group_id WHERE g.status='active' AND g.deleted_at IS NULL AND s.next_run_at<=NOW() AND (s.lease_until IS NULL OR s.lease_until<=NOW()) AND ((s.mode='svg' AND $1) OR (s.mode='candy' AND $2)) ORDER BY s.next_run_at,s.group_id,s.mode`, cfg.SVG.Enabled, cfg.Candy.Enabled)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]intelligenceTestJob, 0)
	for rows.Next() {
		var job intelligenceTestJob
		if err = rows.Scan(&job.groupID, &job.mode); err != nil {
			return nil, 0, nil, err
		}
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, 0, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, nil, err
	}
	return cfg, keyRevision, jobs, nil
}

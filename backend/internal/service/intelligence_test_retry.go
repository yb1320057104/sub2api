package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var errIntelligenceTestLeaseLost = errors.New("intelligence test lease, configuration or key ownership changed")

type intelligenceTestOutcome struct {
	artifact   string
	answerText string
	passed     *bool
}

func intelligenceTestRetryDelay(failures int) time.Duration {
	if failures <= 1 {
		return time.Second
	}
	if failures >= 6 {
		return 30 * time.Second
	}
	return time.Second << (failures - 1)
}

func waitIntelligenceTestRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// An attempt's five-minute context is created before this renewal. Even a
// process paused after renewal cannot resume and send outside that deadline.
// Every request plus its maximum 30s backoff fits inside the six-minute lease.
func (s *IntelligenceTestService) renewIntelligenceTestLease(ctx context.Context, groupID int64, mode string, resultID, keyID int64, cfg *IntelligenceTestConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(queryCtx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(queryCtx, `SELECT pg_advisory_xact_lock_shared(706832,1)`); err != nil {
		return err
	}
	// Key rotations hold the exclusive side of this second gate. Bind every
	// retry to the key ID captured when its original result was claimed.
	if _, err = tx.ExecContext(queryCtx, `SELECT pg_advisory_xact_lock_shared(706832,2)`); err != nil {
		return err
	}
	latest, err := intelligenceTestConfigInTransaction(queryCtx, tx)
	if err != nil {
		return err
	}
	if latest == nil || !sameIntelligenceTestConfig(cfg, latest) || !latest.Enabled ||
		(mode == "svg" && !latest.SVG.Enabled) || (mode == "candy" && !latest.Candy.Enabled) {
		return errIntelligenceTestLeaseLost
	}
	result, err := tx.ExecContext(queryCtx, `UPDATE pk_intelligence_test_state s SET lease_until=NOW()+($5::integer*INTERVAL '1 second')
		WHERE s.group_id=$1 AND s.mode=$2 AND s.active_result_id=$3 AND s.lease_until>NOW()
		AND EXISTS (SELECT 1 FROM pk_intelligence_test_results r WHERE r.id=$3 AND r.group_id=$1 AND r.mode=$2 AND r.status='running')
		AND EXISTS (SELECT 1 FROM pk_intelligence_test_keys k
			JOIN groups g ON g.id=k.group_id AND g.status='active' AND g.deleted_at IS NULL
			JOIN api_keys a ON a.id=k.api_key_id AND a.group_id=k.group_id AND a.deleted_at IS NULL AND a.key LIKE $6
			JOIN users u ON u.id=a.user_id AND u.status='active' AND u.role='admin' AND u.deleted_at IS NULL
			WHERE k.group_id=$1 AND k.api_key_id=$4)`, groupID, mode, resultID, keyID, int(intelligenceTestLease.Seconds()), intelligenceTestKeyPrefix+"%")
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errIntelligenceTestLeaseLost
	}
	return tx.Commit()
}

func (s *IntelligenceTestService) intelligenceTestRetryKey(ctx context.Context, groupID, keyID int64) (string, error) {
	var key string
	err := s.db.QueryRowContext(ctx, `SELECT a.key FROM pk_intelligence_test_keys k
		JOIN api_keys a ON a.id=k.api_key_id AND a.group_id=k.group_id AND a.deleted_at IS NULL
		JOIN groups g ON g.id=k.group_id AND g.status='active' AND g.deleted_at IS NULL
		JOIN users u ON u.id=a.user_id AND u.status='active' AND u.role='admin' AND u.deleted_at IS NULL
		WHERE k.group_id=$1 AND k.api_key_id=$2`, groupID, keyID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errIntelligenceTestLeaseLost
	}
	if err != nil {
		return "", err
	}
	if !IsIntelligenceTestKey(key) {
		return "", errIntelligenceTestLeaseLost
	}
	return strings.TrimSpace(key), nil
}

func intelligenceTestCompletedOutcome(mode, answer, key string, settings IntelligenceTestModeConfig) (*intelligenceTestOutcome, error) {
	if strings.IndexByte(answer, 0) >= 0 || !utf8.ValidString(answer) {
		return nil, intelligenceTestFailure("invalid_final_answer_text", "最终回复包含无法保存的文本字符。")
	}
	answer = strings.ReplaceAll(answer, key, "[REDACTED]")
	if mode == "svg" {
		artifact := intelligenceExtractHTML(answer)
		if artifact == "" || len(artifact) > intelligenceTestArtifactLimit {
			return nil, intelligenceTestFailure("invalid_svg_artifact", "最终回复未包含完整且符合大小限制的 SVG 或 HTML 作品。")
		}
		return &intelligenceTestOutcome{artifact: artifact}, nil
	}
	// A complete candy answer is a successful request even when incorrect or
	// unrecognized. Never retry until the model happens to answer the target.
	number := intelligenceExtractAnswer(answer)
	passed := number != nil && *number == settings.ExpectedAnswer
	return &intelligenceTestOutcome{answerText: answer, passed: &passed}, nil
}

func (s *IntelligenceTestService) runIntelligenceTestAttempts(ctx context.Context, groupID int64, mode string, resultID, keyID int64, cfg *IntelligenceTestConfig, settings IntelligenceTestModeConfig) (*intelligenceTestOutcome, error) {
	failures := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attemptCtx, attemptCancel := context.WithTimeout(ctx, intelligenceTestTimeout)
		if err := s.renewIntelligenceTestLease(attemptCtx, groupID, mode, resultID, keyID, cfg); err != nil {
			attemptCancel()
			return nil, err // Ownership uncertainty never permits another HTTP call.
		}
		key, requestErr := s.intelligenceTestRetryKey(attemptCtx, groupID, keyID)
		if errors.Is(requestErr, errIntelligenceTestLeaseLost) {
			attemptCancel()
			return nil, requestErr
		}
		var outcome *intelligenceTestOutcome
		if requestErr == nil {
			requestErr = attemptCtx.Err()
			if deadline, ok := attemptCtx.Deadline(); requestErr == nil && ok && !time.Now().Before(deadline) {
				requestErr = context.DeadlineExceeded
			}
			if requestErr == nil {
				var answer string
				answer, requestErr = s.callModel(attemptCtx, key, mode, settings)
				if requestErr == nil {
					outcome, requestErr = intelligenceTestCompletedOutcome(mode, answer, key, settings)
				}
			}
		}
		attemptCancel()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if requestErr == nil {
			return outcome, nil
		}
		if failures < 6 {
			failures++
		}
		if err := waitIntelligenceTestRetry(ctx, intelligenceTestRetryDelay(failures)); err != nil {
			return nil, err
		}
	}
}

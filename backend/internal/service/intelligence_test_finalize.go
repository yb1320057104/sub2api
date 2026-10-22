package service

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	intelligenceTestResultWriteTimeout         = 2 * time.Second
	intelligenceTestLeaseReleaseTimeout        = 3 * time.Second
	intelligenceTestLeaseReleaseAttemptTimeout = 750 * time.Millisecond
	intelligenceTestLeaseReleaseAttempts       = 3
)

type intelligenceTestCompletion struct {
	id, groupID                                  int64
	mode, status                                 string
	passed                                       *bool
	artifact, answerText, errorCode, errorDetail string
	finishedAt                                   time.Time
	durationMS                                   int64
}

// The caller has already stopped its HTTP request. Result persistence and lease
// release have separate contexts, so a failed/deleted result cannot strand a
// stopped worker's lease. Their combined budget remains at most five seconds.
func (s *IntelligenceTestService) finalizeIntelligenceTestResult(completion intelligenceTestCompletion) error {
	writeCtx, writeCancel := context.WithTimeout(context.Background(), intelligenceTestResultWriteTimeout)
	_, writeErr := s.db.ExecContext(writeCtx, `UPDATE pk_intelligence_test_results SET status=$2,passed=$3,artifact=$4,finished_at=$5,duration_ms=$6,answer_text=$7,error_code=$8,error_detail=$9 WHERE id=$1 AND status='running'`,
		completion.id, completion.status, completion.passed, completion.artifact, completion.finishedAt.UTC(), completion.durationMS, completion.answerText, completion.errorCode, completion.errorDetail)
	writeCancel()
	if writeErr != nil {
		slog.Warn("intelligence_test_finalization_failed", "stage", "result_write", "group_id", completion.groupID, "mode", completion.mode, "result_id", completion.id)
	}
	leaseErr := s.releaseIntelligenceTestLease(completion.groupID, completion.mode, completion.id)
	if leaseErr != nil {
		slog.Warn("intelligence_test_finalization_failed", "stage", "lease_release", "group_id", completion.groupID, "mode", completion.mode, "result_id", completion.id)
	}
	return errors.Join(writeErr, leaseErr)
}

// Zero affected rows are harmless: a save may have removed the result, or a
// newer owner may already hold this pair. Never clear a different active ID.
func (s *IntelligenceTestService) releaseIntelligenceTestLease(groupID int64, mode string, resultID int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), intelligenceTestLeaseReleaseTimeout)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt < intelligenceTestLeaseReleaseAttempts; attempt++ {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, intelligenceTestLeaseReleaseAttemptTimeout)
		_, lastErr = s.db.ExecContext(attemptCtx, `UPDATE pk_intelligence_test_state SET lease_until=NULL,active_result_id=NULL WHERE group_id=$1 AND mode=$2 AND active_result_id=$3`, groupID, mode, resultID)
		attemptCancel()
		if lastErr == nil {
			return nil
		}
		if attempt+1 == intelligenceTestLeaseReleaseAttempts {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(lastErr, ctx.Err())
		case <-timer.C:
		}
	}
	return lastErr
}

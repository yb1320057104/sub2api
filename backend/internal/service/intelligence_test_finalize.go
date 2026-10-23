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
	var leaseErr error
	if writeErr != nil {
		// No final record was confirmed. Make this original pair due again rather
		// than waiting a normal success interval after a persistence failure.
		leaseErr = s.requeueIntelligenceTestLease(completion.groupID, completion.mode, completion.id)
	} else {
		leaseErr = s.releaseIntelligenceTestLease(completion.groupID, completion.mode, completion.id)
	}
	if leaseErr != nil {
		slog.Warn("intelligence_test_finalization_failed", "stage", "lease_release", "group_id", completion.groupID, "mode", completion.mode, "result_id", completion.id)
	}
	return errors.Join(writeErr, leaseErr)
}

// Zero affected rows are harmless: a save may have removed the result, or a
// newer owner may already hold this pair. Never clear a different active ID.
func (s *IntelligenceTestService) releaseIntelligenceTestLease(groupID int64, mode string, resultID int64) error {
	return s.releaseIntelligenceTestLeaseWithRequeue(groupID, mode, resultID, false)
}

func (s *IntelligenceTestService) requeueIntelligenceTestLease(groupID int64, mode string, resultID int64) error {
	return s.releaseIntelligenceTestLeaseWithRequeue(groupID, mode, resultID, true)
}

// Cancelled or interrupted attempts are not failures shown to users. Delete only
// this unfinished row; neither a completed result nor a newer owner's lease is
// removed. A future attempt always receives a new result ID and a new fence.
func (s *IntelligenceTestService) abandonIntelligenceTestResult(groupID int64, mode string, resultID int64) error {
	leaseErr := s.requeueIntelligenceTestLease(groupID, mode, resultID)
	if leaseErr != nil {
		slog.Warn("intelligence_test_finalization_failed", "stage", "pending_abandon", "group_id", groupID, "mode", mode, "result_id", resultID)
	}
	return leaseErr
}

func (s *IntelligenceTestService) releaseIntelligenceTestLeaseWithRequeue(groupID int64, mode string, resultID int64, requeue bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), intelligenceTestLeaseReleaseTimeout)
	defer cancel()
	query := `UPDATE pk_intelligence_test_state SET lease_until=NULL,active_result_id=NULL WHERE group_id=$1 AND mode=$2 AND active_result_id=$3`
	if requeue {
		// Delete and release together. A connection error cannot leave a deleted
		// pending row while its old long-interval schedule remains stranded. The
		// statement snapshot also preserves a confirmed terminal record/interval.
		query = `WITH discarded AS (
			DELETE FROM pk_intelligence_test_results WHERE id=$3 AND group_id=$1 AND mode=$2 AND status='running' RETURNING id
		) UPDATE pk_intelligence_test_state s SET
			next_run_at=CASE WHEN EXISTS (SELECT 1 FROM pk_intelligence_test_results r WHERE r.id=$3 AND r.group_id=$1 AND r.mode=$2 AND r.status IN ('completed','failed')) THEN s.next_run_at ELSE NOW() END,
			lease_until=NULL,active_result_id=NULL WHERE s.group_id=$1 AND s.mode=$2 AND s.active_result_id=$3`
	}
	var lastErr error
	for attempt := 0; attempt < intelligenceTestLeaseReleaseAttempts; attempt++ {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, intelligenceTestLeaseReleaseAttemptTimeout)
		_, lastErr = s.db.ExecContext(attemptCtx, query, groupID, mode, resultID)
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

//go:build unit

package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestBatchImageAuditOnlyChargesFullyCompletedBatchWithOriginalScope(t *testing.T) {
	group, score := int64(9), 80.0
	input := CyberPolicyAuditBlockInput{EventKey: "server:batch", UserID: 7, GroupID: &group, PolicyAction: "charge_allow", RiskScore: &score, Source: "prompt_audit"}
	for _, tc := range []struct {
		name                       string
		success, failed, cancelled int
		wrongUser, legacy          bool
		want                       int
	}{
		{"complete", 3, 0, 0, false, false, 1}, {"partial", 2, 1, 0, false, false, 0}, {"cancelled", 2, 0, 1, false, false, 0},
		{"wrong_user", 3, 0, 0, true, false, 0}, {"legacy", 3, 0, 0, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := testSettlingBatchImageJob("batch-audit")
			job.UserID = 7
			job.ItemCount = 3
			job.SuccessCount = tc.success
			job.FailCount = tc.failed
			job.CancelledCount = tc.cancelled
			raw, err := json.Marshal(input)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(raw, &job.AuditSnapshot))
			if tc.wrongUser {
				job.AuditSnapshot.UserID = 8
			}
			if tc.legacy {
				job.AuditSnapshot = nil
			}
			recorder := &auditUsageRecorderStub{}
			writer := &auditUsageWriterStub{inserted: true}
			svc := &BatchImageSettlementService{AuditRecorder: recorder, UsageLogRepo: writer}
			// An unrelated context must not override the server-owned job evidence.
			ctx := WithCyberPolicyAuditUsage(context.Background(), input, recorder)
			svc.recordUsageLog(ctx, job, 0.25, "batch-capture", time.Now())
			require.Len(t, recorder.inputs, tc.want)
			if tc.want > 0 {
				require.Equal(t, group, *recorder.usage[0].GroupID)
				require.Equal(t, 0.25, recorder.usage[0].ActualCost)
				require.Equal(t, "server:batch", recorder.inputs[0].EventKey)
			}
		})
	}
}

func TestBatchImageAuditSnapshotIsNotExposedInPublicJob(t *testing.T) {
	job := testSettlingBatchImageJob("batch-audit")
	job.AuditSnapshot = &CyberPolicyAuditBlockInput{EventKey: "private-event-id", UserID: 7, PolicyAction: "charge_allow"}
	for _, value := range []any{job, BatchImageJobToPublic(job)} {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "private-event-id")
		require.NotContains(t, string(raw), "AuditSnapshot")
	}
}

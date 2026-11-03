package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type auditUsageRecorderStub struct {
	inputs []CyberPolicyAuditBlockInput
	usage  []UsageLog
	err    error
}

func (r *auditUsageRecorderStub) RecordAuditUsage(_ context.Context, input CyberPolicyAuditBlockInput, usage *UsageLog) error {
	r.inputs = append(r.inputs, input)
	r.usage = append(r.usage, *usage)
	return r.err
}

type auditUsageWriterStub struct {
	UsageLogRepository
	inserted             bool
	createErr            error
	creates, bestEfforts int
}

func (r *auditUsageWriterStub) Create(ctx context.Context, usage *UsageLog) (bool, error) {
	r.creates++
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	usage.ID, usage.CreatedAt = 44, time.Unix(10000, 0)
	return r.inserted, r.createErr
}

func (r *auditUsageWriterStub) CreateBestEffort(context.Context, *UsageLog) error {
	r.bestEfforts++
	return nil
}

func TestCyberPolicyUsageSnapshotSurvivesDetachedCopyAndIsImmutable(t *testing.T) {
	group, score := int64(9), 65.0
	r := &auditUsageRecorderStub{}
	input := CyberPolicyAuditBlockInput{EventKey: "server:turn:1", UserID: 7, GroupID: &group, PolicyAction: "charge_allow", RiskScore: &score}
	parent := WithCyberPolicyAuditUsage(context.Background(), input, r)
	group, score = 10, 99
	worker := CopyCyberPolicyAuditUsage(parent, context.Background())
	copy := CyberPolicyAuditUsageInput(worker)
	require.Equal(t, int64(9), *copy.GroupID)
	require.Equal(t, 65.0, *copy.RiskScore)
	*copy.GroupID, *copy.RiskScore = 11, 100
	again := CyberPolicyAuditUsageInput(worker)
	require.Equal(t, int64(9), *again.GroupID)
	require.Equal(t, 65.0, *again.RiskScore)
	require.Nil(t, CyberPolicyAuditUsageInput(WithoutCyberPolicyAuditUsage(worker)))
	require.NotNil(t, CyberPolicyAuditUsageInput(parent), "clearing the next turn must not change an enqueued snapshot")
}

func TestCyberPolicyUsageWritesRealBillBeforePenaltyAndNeverRepeats(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		inserted              bool
		createErr, penaltyErr error
		expected              int
	}{
		{"success", true, nil, nil, 1},
		{"penalty_failure_preserves_bill", true, nil, errors.New("simulated"), 1},
		{"duplicate_bill", false, nil, nil, 0},
		{"bill_failure", false, errors.New("simulated"), nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := int64(9)
			recorder := &auditUsageRecorderStub{err: tc.penaltyErr}
			ctx := WithCyberPolicyAuditUsage(context.Background(), CyberPolicyAuditBlockInput{EventKey: "server:http", UserID: 7, GroupID: &group, PolicyAction: "charge_allow"}, recorder)
			writer := &auditUsageWriterStub{inserted: tc.inserted, createErr: tc.createErr}
			usage := &UsageLog{UserID: 7, GroupID: &group, ActualCost: 0.125, RequestType: RequestTypeSync}
			writeUsageLogBestEffort(ctx, writer, usage, "audit-fixture")
			require.Equal(t, 1, writer.creates)
			if tc.createErr != nil {
				require.Equal(t, 1, writer.bestEfforts, "failed audit identity insert retains ordinary bill recovery")
			} else {
				require.Zero(t, writer.bestEfforts)
			}
			require.Len(t, recorder.usage, tc.expected)
			if tc.expected > 0 {
				require.Equal(t, int64(44), recorder.usage[0].ID)
				require.Equal(t, 0.125, recorder.usage[0].ActualCost)
			}
		})
	}
}

func TestCyberPolicyUsageSkipsOtherUserGroupAndUpstreamPolicy(t *testing.T) {
	group, other := int64(9), int64(10)
	for _, usage := range []*UsageLog{
		{UserID: 8, GroupID: &group}, {UserID: 7, GroupID: &other}, {UserID: 7},
		{UserID: 7, GroupID: &group, RequestType: RequestTypeCyberBlocked},
	} {
		recorder := &auditUsageRecorderStub{}
		ctx := WithCyberPolicyAuditUsage(context.Background(), CyberPolicyAuditBlockInput{UserID: 7, GroupID: &group, PolicyAction: "charge_allow"}, recorder)
		writer := &auditUsageWriterStub{inserted: true}
		writeUsageLogBestEffort(ctx, writer, usage, "audit-fixture")
		require.Zero(t, writer.creates)
		require.Equal(t, 1, writer.bestEfforts)
		require.Empty(t, recorder.usage)
	}
}

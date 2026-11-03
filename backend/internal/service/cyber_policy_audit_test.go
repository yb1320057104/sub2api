package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestCyberPolicyAuditBlockInsertIsIndependentOfUsageAndBilling(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	svc := NewCyberPolicyService(db, nil, nil)
	group := int64(9)
	input := CyberPolicyAuditBlockInput{EventKey: "server-generated:turn:1", UserID: 7, GroupID: &group, Model: "fixture", Source: "combined_audit"}
	for _, inserted := range []int64{1, 0} {
		mock.ExpectExec(`INSERT INTO cyber_policy_audit_blocks[\s\S]*ON CONFLICT DO NOTHING`).
			WithArgs(input.EventKey, "", input.UserID, group, input.Model, input.Source, "block", nil, "", nil, nil).WillReturnResult(sqlmock.NewResult(1, inserted))
		require.NoError(t, svc.RecordAuditBlock(context.Background(), input))
	}
	input.Source = "audit_unavailable"
	require.Error(t, svc.RecordAuditBlock(context.Background(), input))
	require.NoError(t, mock.ExpectationsWereMet(), "only the independent evidence insert may run; no usage or balance writes")
}

func TestCyberPolicyAuditUsageRequiresMatchingOrdinaryBill(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	svc := NewCyberPolicyService(db, nil, nil)
	group, score := int64(9), 60.0
	input := CyberPolicyAuditBlockInput{EventKey: "server:turn:2", UserID: 7, GroupID: &group, Model: "fixture", Source: "combined_audit", PolicyAction: "charge_allow", RiskScore: &score, RiskLevel: "high"}
	usage := &UsageLog{ID: 18, UserID: 7, GroupID: &group, RequestType: RequestTypeSync, ActualCost: 0.125, CreatedAt: time.Now()}
	mock.ExpectExec(`INSERT INTO cyber_policy_audit_blocks[\s\S]*ON CONFLICT DO NOTHING`).
		WithArgs(input.EventKey, "", input.UserID, group, input.Model, input.Source, "charge_allow", score, "high", usage.ID, usage.CreatedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, svc.RecordAuditUsage(context.Background(), input, usage))
	for _, mutate := range []func(*UsageLog){
		func(u *UsageLog) { u.ID = 0 },
		func(u *UsageLog) { u.CreatedAt = time.Time{} },
		func(u *UsageLog) { u.UserID++ },
		func(u *UsageLog) { u.GroupID = nil },
		func(u *UsageLog) { u.RequestType = RequestTypeCyberBlocked },
	} {
		changed := *usage
		mutate(&changed)
		require.Error(t, svc.RecordAuditUsage(context.Background(), input, &changed))
	}
	require.Error(t, svc.RecordAuditBlock(context.Background(), input))
	for _, invalid := range []float64{-1, 101, math.NaN(), math.Inf(1)} {
		bad := input
		bad.RiskScore = &invalid
		require.Error(t, svc.RecordAuditUsage(context.Background(), bad, usage))
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

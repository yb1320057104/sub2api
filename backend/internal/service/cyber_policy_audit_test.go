package service

import (
	"context"
	"testing"

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
		mock.ExpectExec(`INSERT INTO cyber_policy_audit_blocks[\s\S]*ON CONFLICT\(event_key\) DO NOTHING`).
			WithArgs(input.EventKey, "", input.UserID, group, input.Model, input.Source).WillReturnResult(sqlmock.NewResult(1, inserted))
		require.NoError(t, svc.RecordAuditBlock(context.Background(), input))
	}
	input.Source = "audit_unavailable"
	require.Error(t, svc.RecordAuditBlock(context.Background(), input))
	require.NoError(t, mock.ExpectationsWereMet(), "only the independent evidence insert may run; no usage or balance writes")
}

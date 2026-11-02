package service

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestCyberPolicyRankingIncludesAuditBlocksWithoutAddingDeductions(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	window := CyberPolicyWindowInput{StartDate: "2026-11-02", EndDate: "2026-11-02", Timezone: "Asia/Shanghai"}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT multiplier::text`).WillReturnRows(sqlmock.NewRows([]string{"multiplier"}).AddRow("2.000000"))
	mock.ExpectQuery(regexp.QuoteMeta(cyberRankingCTE)+`SELECT COUNT\(\*\),COALESCE\(SUM\(trigger_count\),0\),COALESCE\(SUM\(usage_count-deducted_count\),0\)`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"total", "triggers", "pending", "counted", "skipped", "unknown", "audit"}).AddRow(2, 7, 2, 4, 2, 1, 4))
	mock.ExpectQuery(regexp.QuoteMeta(cyberRankingCTE)+`SELECT ROW_NUMBER\(\).*c.usage_count-c.deducted_count.*c.audit_block_count`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "2.000000", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{"rank", "user_id", "email", "username", "balance", "trigger_count", "deducted_count", "pending_count", "pending_amount", "status", "auto_count", "counted", "skipped", "unknown", "audit"}).
			AddRow(1, 7, "mixed@example.com", "mixed", "100", 5, 1, 2, "4.00000000", "active", 2, 2, 2, 1, 2).
			AddRow(2, 8, "audit@example.com", "audit only", "100", 2, 0, 0, "0.00000000", "active", 2, 2, 0, 0, 2)).RowsWillBeClosed()
	mock.ExpectCommit()
	result, err := NewCyberPolicyService(db, nil, nil).Ranking(context.Background(), window, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(7), result.TotalTriggers)
	require.Equal(t, int64(4), result.TotalAuditBlocks)
	require.Equal(t, int64(2), result.TotalPending)
	require.Len(t, result.Items, 2)
	require.Equal(t, int64(2), result.Items[0].PendingCount)
	require.Equal(t, "4.00000000", result.Items[0].PendingAmount)
	require.Equal(t, int64(2), result.Items[1].AuditBlockCount)
	require.Zero(t, result.Items[1].PendingCount)
	require.Zero(t, result.Items[1].DeductedCount)
	require.Equal(t, "0.00000000", result.Items[1].PendingAmount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCyberPolicyPublicRankingIncludesAuditOnlyUsersAndMasksIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(cyberPublicRankingCTE)+`SELECT COUNT`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"users", "triggers", "counted", "skipped", "unknown", "audit"}).AddRow(1, 2, 1, 1, 0, 2))
	mock.ExpectQuery(regexp.QuoteMeta(cyberPublicRankingCTE)+`SELECT ROW_NUMBER`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{"rank", "display_name", "triggers", "counted", "skipped", "unknown", "audit"}).AddRow(1, "private@example.com", 2, 1, 1, 0, 2)).RowsWillBeClosed()
	mock.ExpectCommit()
	result, err := NewCyberPolicyService(db, nil, nil).PublicRanking(context.Background(), CyberPolicyWindowInput{StartDate: "2026-11-02", EndDate: "2026-11-02", Timezone: "Asia/Shanghai"}, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(2), result.TotalAuditBlocks)
	require.Len(t, result.Items, 1)
	require.Equal(t, "pri***", result.Items[0].DisplayName)
	require.Equal(t, int64(2), result.Items[0].AuditBlockCount)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{"private@example.com", "user_id", "email", "balance", "pending_amount", "request_id"} {
		require.NotContains(t, string(raw), secret)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

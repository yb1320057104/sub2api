package service

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestCyberPolicyPublicGroupRulesExposeOnlyEffectiveGroupPricing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery(`^SELECT group_ids,group_rules FROM cyber_policy_settings WHERE id=1$`).
		WillReturnRows(sqlmock.NewRows([]string{"groups", "rules"}).AddRow("{7}", `[{"group_id":7,"multiplier":"2.500000","auto_deduct_enabled":true},{"group_id":8,"multiplier":"3.000000","auto_deduct_enabled":true},{"group_id":9,"multiplier":"4.000000","auto_deduct_enabled":false}]`))
	rules, err := NewCyberPolicyService(db, nil, nil).PublicGroupAuditRules(context.Background())
	require.NoError(t, err)
	require.Equal(t, "2.500000", rules[7].Multiplier)
	require.True(t, rules[7].AutoDeductEnabled)
	require.Equal(t, "3.000000", rules[8].Multiplier)
	require.False(t, rules[8].AutoDeductEnabled, "unselected scope cannot advertise active auto-deduction")
	require.False(t, rules[9].AutoDeductEnabled)
	require.NoError(t, mock.ExpectationsWereMet())
}

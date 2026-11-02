package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestCyberPolicySettingsRejectInvalidExemptionsAndChargeRulesBeforeWrite(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input CyberPolicySettingsInput
		code  string
	}{
		{"zero user", CyberPolicySettingsInput{UserAllowlistIDs: &[]int64{0}}, "CYBER_USERS_INVALID"},
		{"too many users", CyberPolicySettingsInput{UserAllowlistIDs: func() *[]int64 { ids := make([]int64, 1001); return &ids }()}, "CYBER_USERS_INVALID"},
		{"duplicate rules", CyberPolicySettingsInput{GroupRules: &[]CyberPolicyGroupRule{{GroupID: 7, Multiplier: "1"}, {GroupID: 7, Multiplier: "2"}}}, "CYBER_RULES_INVALID"},
		{"negative group", CyberPolicySettingsInput{GroupRules: &[]CyberPolicyGroupRule{{GroupID: -1, Multiplier: "1"}}}, "CYBER_RULES_INVALID"},
		{"zero multiplier", CyberPolicySettingsInput{GroupRules: &[]CyberPolicyGroupRule{{GroupID: 7, Multiplier: "0"}}}, "CYBER_MULTIPLIER_INVALID"},
		{"exponent multiplier", CyberPolicySettingsInput{GroupRules: &[]CyberPolicyGroupRule{{GroupID: 7, Multiplier: "1e2"}}}, "CYBER_MULTIPLIER_INVALID"},
		{"excess precision", CyberPolicySettingsInput{GroupRules: &[]CyberPolicyGroupRule{{GroupID: 7, Multiplier: "0.1234567"}}}, "CYBER_MULTIPLIER_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			_, err = NewCyberPolicyService(db, nil, nil).SaveSettings(context.Background(), tc.input)
			require.ErrorContains(t, err, tc.code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCyberPolicySettingsExemptionsAndRulesAreAtomicAndOmissionSafe(t *testing.T) {
	for _, mode := range []string{"save", "clear", "missing-user", "missing-group"} {
		t.Run(mode, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			input := CyberPolicySettingsInput{UserAllowlistIDs: &[]int64{9, 7, 9}, GroupRules: &[]CyberPolicyGroupRule{{GroupID: 8, Multiplier: "2.5", AutoDeductEnabled: true}}}
			userValue := "{7,9}"
			rules := `[{"group_id":8,"multiplier":"2.500000","auto_deduct_enabled":true}]`
			if mode == "clear" {
				input.UserAllowlistIDs = &[]int64{}
				input.GroupRules = &[]CyberPolicyGroupRule{}
				userValue = "{}"
				rules = "[]"
			}
			mock.ExpectBegin()
			if mode != "clear" {
				users := sqlmock.NewRows([]string{"id"}).AddRow(7)
				if mode != "missing-user" {
					users.AddRow(9)
				}
				mock.ExpectQuery(`SELECT id FROM users .*deleted_at IS NULL ORDER BY id FOR SHARE`).WithArgs(userValue).WillReturnRows(users).RowsWillBeClosed()
				if mode != "missing-user" {
					groups := sqlmock.NewRows([]string{"id"})
					if mode != "missing-group" {
						groups.AddRow(8)
					}
					mock.ExpectQuery(`SELECT id FROM groups .*status='active' ORDER BY id FOR SHARE`).WithArgs("{8}").WillReturnRows(groups).RowsWillBeClosed()
				}
			}
			if mode == "missing-user" || mode == "missing-group" {
				mock.ExpectRollback()
			} else {
				// Group IDs and the old manual multiplier are omitted and preserved.
				mock.ExpectQuery(`UPDATE cyber_policy_settings SET`).WithArgs(nil, nil, nil, nil, nil, userValue, rules).
					WillReturnRows(sqlmock.NewRows([]string{"multiplier", "enabled", "threshold", "reset", "groups", "revision", "users", "rules"}).AddRow("6.700000", false, 10, true, "{8}", 4, userValue, rules))
				mock.ExpectCommit()
			}
			settings, err := NewCyberPolicyService(db, nil, nil).SaveSettings(context.Background(), input)
			if mode == "missing-user" {
				require.ErrorContains(t, err, "CYBER_USERS_INVALID")
			} else if mode == "missing-group" {
				require.ErrorContains(t, err, "CYBER_GROUPS_INVALID")
			} else {
				require.NoError(t, err)
				require.Equal(t, "6.700000", settings.Multiplier)
				require.False(t, settings.AutoDisableEnabled)
				if mode == "clear" {
					require.Empty(t, settings.UserAllowlistIDs)
					require.Empty(t, settings.GroupRules)
				} else {
					require.Equal(t, []int64{7, 9}, settings.UserAllowlistIDs)
					require.True(t, settings.GroupRules[0].AutoDeductEnabled)
				}
				encoded, err := json.Marshal(settings)
				require.NoError(t, err)
				require.Contains(t, string(encoded), `"group_rules":`)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

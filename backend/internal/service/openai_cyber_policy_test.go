package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCyberPolicySettingsGroupValidationAndTransaction(t *testing.T) {
	for _, tc := range []struct {
		name       string
		input      *[]int64
		value      any
		groups     []int64
		validation string
		missing    bool
	}{
		{name: "omitted preserves selection", value: nil, groups: []int64{7}},
		{name: "empty clears selection", input: &[]int64{}, value: "{}", groups: []int64{}},
		{name: "normalizes duplicate selection", input: &[]int64{9, 7, 9}, value: "{7,9}", groups: []int64{7, 9}},
		{name: "zero rejected", input: &[]int64{0}, validation: "CYBER_GROUPS_INVALID"},
		{name: "negative rejected", input: &[]int64{-1}, validation: "CYBER_GROUPS_INVALID"},
		{name: "oversized rejected", input: func() *[]int64 { ids := make([]int64, 1001); return &ids }(), validation: "CYBER_GROUPS_INVALID"},
		{name: "missing disabled or deleted rejected", input: &[]int64{7, 9}, value: "{7,9}", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			svc := NewCyberPolicyService(db, nil, nil)
			if tc.validation == "" {
				mock.ExpectBegin()
				if tc.input != nil && len(*tc.input) > 0 {
					rows := sqlmock.NewRows([]string{"id"}).AddRow(7)
					if !tc.missing {
						rows.AddRow(9)
					}
					mock.ExpectQuery(`SELECT id FROM groups .*deleted_at IS NULL AND status='active' ORDER BY id FOR SHARE`).WithArgs(tc.value).WillReturnRows(rows).RowsWillBeClosed()
				}
				if tc.missing {
					mock.ExpectRollback()
				} else {
					array := "{}"
					if tc.input == nil {
						array = "{7}"
					} else if len(tc.groups) > 0 {
						array = "{7,9}"
					}
					mock.ExpectQuery(`UPDATE cyber_policy_settings SET`).WithArgs(nil, nil, nil, nil, tc.value, nil, nil).
						WillReturnRows(sqlmock.NewRows([]string{"multiplier", "auto_disable_enabled", "auto_disable_threshold", "reset_on_manual_enable", "group_ids", "settings_revision", "user_allowlist_ids", "group_rules"}).AddRow("6.700000", true, 10, true, array, 12, "{}", "[]"))
					mock.ExpectCommit()
				}
			}
			settings, err := svc.SaveSettings(context.Background(), CyberPolicySettingsInput{GroupIDs: tc.input})
			if tc.validation != "" || tc.missing {
				require.ErrorContains(t, err, "CYBER_GROUPS_INVALID")
				require.Nil(t, settings)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.groups, settings.GroupIDs)
				require.Equal(t, int64(12), settings.SettingsRevision)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCyberPolicyRecordsKeepsHistoricalUnknownAndInsertionEvidence(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	svc := NewCyberPolicyService(db, nil, nil)
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	start := time.Date(2006, 2, 3, 0, 0, 0, 0, loc)
	end := start.Add(24 * time.Hour)
	evaluated := time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT EXISTS`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(cyberRecordsCountQuery)).
		WithArgs(int64(7), start, end).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))
	mock.ExpectQuery(regexp.QuoteMeta(cyberRecordsQuery)).WithArgs(int64(7), start, end, 100, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "group_id", "group_name", "request_id", "model", "status", "reason", "scope", "revision", "evaluated_at", "source", "audit_id", "charge_status", "charge_reason", "cost", "multiplier", "charge_amount", "policy_action", "risk_score", "risk_level", "linked_usage_id", "auto_charge_source", "auto_charge_rule", "auto_charge_minimum"}).
			AddRow(3, start.Add(3*time.Hour), 9, "original skipped group", "request-c", "model", "skipped", "group_not_enabled", false, 12, evaluated, "upstream_policy", nil, "skipped", "group_not_enabled", "0.0200000000", "1.000000", "0.02000000", "", nil, nil, nil, nil, nil, nil).
			AddRow(2, start.Add(2*time.Hour), 7, "original counted group", "request-b", "model", "counted", "", true, 11, evaluated, "upstream_policy", nil, "deducted", "", "0.0000000000", "2.000000", "6.70000000", "", nil, nil, nil, "upstream_type4", "max_cost_multiplier_minimum", "6.70000000").
			AddRow(1, start.Add(time.Hour), nil, "", "request-a", "model", "unknown", "historical_unknown", nil, nil, nil, "upstream_policy", nil, "historical_unknown", "", nil, nil, nil, "", nil, nil, nil, nil, nil, nil).
			AddRow(0, start.Add(30*time.Minute), 7, "audit group snapshot", "request-d", "model", "counted", "", true, 11, evaluated, "content_audit", 41, "zero_cost", "", "0.0000000000", "2.000000", "0.00000000", "block", 70, "high", nil, nil, nil, nil)).RowsWillBeClosed()
	mock.ExpectCommit()
	result, err := svc.Records(context.Background(), 7, CyberPolicyWindowInput{StartDate: "2006-02-03", EndDate: "2006-02-03", Timezone: "Asia/Shanghai"}, 0, 1000)
	require.NoError(t, err)
	require.Equal(t, 1, result.Page)
	require.Equal(t, 100, result.PageSize)
	require.Equal(t, int64(1), result.Pages)
	require.Len(t, result.Items, 4)
	require.Equal(t, "skipped", result.Items[0].CountStatus)
	require.False(t, *result.Items[0].ScopeMatched)
	require.Equal(t, "counted", result.Items[1].CountStatus)
	require.True(t, *result.Items[1].ScopeMatched)
	require.Equal(t, evaluated, *result.Items[1].EvaluatedAt)
	require.NotEqual(t, result.Items[1].OccurredAt, *result.Items[1].EvaluatedAt, "evaluation uses insertion time, not the original usage date")
	require.Equal(t, "unknown", result.Items[2].CountStatus)
	require.Equal(t, "historical_unknown", result.Items[2].CountReason)
	require.Nil(t, result.Items[2].GroupID)
	require.Nil(t, result.Items[2].ScopeMatched)
	require.Nil(t, result.Items[2].SettingsRevision)
	require.Nil(t, result.Items[2].EvaluatedAt)
	require.Equal(t, "deducted", result.Items[1].AutoChargeStatus)
	require.Equal(t, "6.70000000", *result.Items[1].AutoChargeAmount)
	require.Equal(t, "0.0000000000", *result.Items[1].ActualCost)
	require.Equal(t, "upstream_type4", *result.Items[1].AutoChargeSource)
	require.Equal(t, "max_cost_multiplier_minimum", *result.Items[1].AutoChargeRule)
	require.Equal(t, "6.70000000", *result.Items[1].AutoChargeMinimum)
	require.Nil(t, result.Items[2].AutoChargeMinimum)
	require.Nil(t, result.Items[3].AutoChargeRule, "old zero-cost audit evidence must not inherit today's minimum")
	require.Nil(t, result.Items[2].AutoChargeAmount)
	require.Equal(t, "upstream_policy", result.Items[2].Source)
	require.Equal(t, "usage:1:"+result.Items[2].OccurredAt.UTC().Format(time.RFC3339Nano), result.Items[2].RecordKey)
	require.Nil(t, result.Items[2].AuditID)
	require.Zero(t, result.Items[3].UsageID, "local audit blocks must never masquerade as usage rows")
	require.Equal(t, int64(41), *result.Items[3].AuditID)
	require.Equal(t, "audit:41", result.Items[3].RecordKey)
	require.Equal(t, "content_audit", result.Items[3].Source)
	encoded, err := json.Marshal(result.Items[2])
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"scope_matched":null`)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkAndGetOpsCyberPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	require.Nil(t, GetOpsCyberPolicy(c), "no mark initially")

	MarkOpsCyberPolicy(c, CyberPolicyMark{
		Code:           "cyber_policy",
		Message:        "This request was flagged for cyber policy.",
		Body:           `{"error":{"code":"cyber_policy"}}`,
		UpstreamStatus: 400,
	})

	got := GetOpsCyberPolicy(c)
	require.NotNil(t, got)
	require.Equal(t, "cyber_policy", got.Code)
	require.Equal(t, 400, got.UpstreamStatus)
}

func TestMarkOpsCyberPolicyFirstWins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	MarkOpsCyberPolicy(c, CyberPolicyMark{Code: "cyber_policy", Message: "first"})
	MarkOpsCyberPolicy(c, CyberPolicyMark{Code: "cyber_policy", Message: "second"})
	require.Equal(t, "first", GetOpsCyberPolicy(c).Message, "first mark wins, later marks ignored")
}

func TestMarkOpsCyberPolicyNilContext(t *testing.T) {
	MarkOpsCyberPolicy(nil, CyberPolicyMark{Code: "cyber_policy"})
	require.Nil(t, GetOpsCyberPolicy(nil))
}

// TestClearOpsCyberPolicy_AllowsRemark verifies F1: after Clear, Get returns nil
// and a subsequent Mark takes effect (per-turn lifecycle in WS connections).
func TestClearOpsCyberPolicy_AllowsRemark(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	MarkOpsCyberPolicy(c, CyberPolicyMark{Message: "first", UpstreamStatus: 200})
	require.NotNil(t, GetOpsCyberPolicy(c))

	ClearOpsCyberPolicy(c)
	require.Nil(t, GetOpsCyberPolicy(c), "mark must be invisible after Clear")

	MarkOpsCyberPolicy(c, CyberPolicyMark{Message: "second", UpstreamStatus: 400})
	got := GetOpsCyberPolicy(c)
	require.NotNil(t, got, "re-mark after Clear must take effect")
	require.Equal(t, "second", got.Message)
}

func TestDetectOpenAICyberPolicy(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		hit     bool
		msg     string
	}{
		{"top-level error", `{"error":{"code":"cyber_policy","message":"flagged"}}`, true, "flagged"},
		{"response-wrapped", `{"response":{"error":{"code":"cyber_policy","message":"  bad  "}}}`, true, "bad"},
		{"case-insensitive", `{"error":{"code":"Cyber_Policy"}}`, true, ""},
		{"content_policy not cyber", `{"error":{"code":"content_policy","message":"x"}}`, false, ""},
		{"safety message not cyber", `{"error":{"type":"safety_error","message":"high-risk cyber activity"}}`, false, ""},
		{"empty", ``, false, ""},
		{"upstream_error", `{"error":{"code":"upstream_error"}}`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit, code, msg := detectOpenAICyberPolicy([]byte(tc.payload))
			require.Equal(t, tc.hit, hit)
			if tc.hit {
				require.Equal(t, "cyber_policy", code)
				require.Equal(t, tc.msg, msg)
			}
		})
	}
}

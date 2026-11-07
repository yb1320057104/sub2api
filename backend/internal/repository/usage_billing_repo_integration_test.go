//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func configurePK54UpstreamMinimum(t *testing.T, groupID int64) {
	t.Helper()
	svc := service.NewCyberPolicyService(integrationDB, nil, nil)
	old, err := svc.Settings(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, restoreErr := svc.SaveSettings(context.Background(), service.CyberPolicySettingsInput{
			Multiplier: &old.Multiplier, AutoDisableEnabled: &old.AutoDisableEnabled, AutoDisableThreshold: &old.AutoDisableThreshold,
			ResetOnManualEnable: &old.ResetOnManualEnable, GroupIDs: &old.GroupIDs, UserAllowlistIDs: &old.UserAllowlistIDs, GroupRules: &old.GroupRules,
		})
		require.NoError(t, restoreErr)
	})
	minimum, enabled, threshold := "6.7", true, int64(1000000)
	_, err = svc.SaveSettings(context.Background(), service.CyberPolicySettingsInput{Multiplier: &minimum, AutoDisableEnabled: &enabled, AutoDisableThreshold: &threshold,
		GroupIDs: &[]int64{groupID}, UserAllowlistIDs: &[]int64{}, GroupRules: &[]service.CyberPolicyGroupRule{{GroupID: groupID, Multiplier: "2", AutoDeductEnabled: true}}})
	require.NoError(t, err)
}

func TestPK54AuditSettlementUpstreamMinimumFinancialBoundaries(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	group := mustCreateGroup(t, client, &service.Group{Name: "pk54-minimum-" + uuid.NewString()})
	configurePK54UpstreamMinimum(t, group.ID)
	account := mustCreateAccount(t, client, &service.Account{Name: "minimum fixture"})
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	svc := service.NewCyberPolicyService(integrationDB, nil, nil)
	for _, tc := range []struct {
		name                                      string
		cost, balance                             float64
		role                                      string
		exempt, unscoped, off, banOff, failLedger bool
		status, reason, amount, remaining         string
	}{
		{name: "zero cost", balance: 100, status: "deducted", amount: "6.70000000", remaining: "93.30000000"},
		{name: "below minimum", cost: 1, balance: 100, status: "deducted", amount: "6.70000000", remaining: "93.30000000"},
		{name: "above minimum", cost: 4, balance: 100, status: "deducted", amount: "8.00000000", remaining: "92.00000000"},
		{name: "auto ban independent", balance: 100, banOff: true, status: "deducted", amount: "6.70000000", remaining: "93.30000000"},
		{name: "insufficient balance", balance: 3, status: "insufficient_balance", amount: "6.70000000", remaining: "3.00000000"},
		{name: "user exemption", balance: 100, exempt: true, status: "skipped", reason: "user_exempt", amount: "6.70000000", remaining: "100.00000000"},
		{name: "admin exemption", balance: 100, role: service.RoleAdmin, status: "skipped", reason: "admin_exempt", amount: "6.70000000", remaining: "100.00000000"},
		{name: "group not selected", balance: 100, unscoped: true, status: "skipped", reason: "group_not_enabled", amount: "6.70000000", remaining: "100.00000000"},
		{name: "auto deduction off", balance: 100, off: true, status: "skipped", reason: "auto_deduct_off", amount: "6.70000000", remaining: "100.00000000"},
		{name: "ledger failure", balance: 100, failLedger: true, status: "failed", reason: "charge_failed", amount: "6.70000000", remaining: "100.00000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := mustCreateUser(t, client, &service.User{Email: "pk54-min-" + uuid.NewString() + "@example.com", Balance: tc.balance, Role: tc.role})
			key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "pk54-min-" + uuid.NewString(), Name: "minimum", GroupID: &group.ID})
			users := []int64{}
			if tc.exempt {
				users = append(users, user.ID)
			}
			banEnabled := !tc.banOff
			_, err := svc.SaveSettings(ctx, service.CyberPolicySettingsInput{UserAllowlistIDs: &users, AutoDisableEnabled: &banEnabled,
				GroupRules: &[]service.CyberPolicyGroupRule{{GroupID: group.ID, Multiplier: "2", AutoDeductEnabled: !tc.off}}})
			require.NoError(t, err)
			if tc.failLedger {
				_, err = integrationDB.Exec(fmt.Sprintf(`CREATE FUNCTION pk54_min_reject_ledger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.used_by=%d AND NEW.notes LIKE '自动安全审计扣款%%' THEN RAISE EXCEPTION 'fixture ledger unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER pk54_min_reject_ledger BEFORE INSERT ON redeem_codes FOR EACH ROW EXECUTE FUNCTION pk54_min_reject_ledger()`, user.ID))
				require.NoError(t, err)
				t.Cleanup(func() {
					_, cleanupErr := integrationDB.Exec(`DROP TRIGGER IF EXISTS pk54_min_reject_ledger ON redeem_codes; DROP FUNCTION IF EXISTS pk54_min_reject_ledger()`)
					require.NoError(t, cleanupErr)
				})
			}
			usage := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, GroupID: &group.ID, RequestID: uuid.NewString(), Model: "minimum",
				RequestType: service.RequestTypeCyberBlocked, ActualCost: tc.cost, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
			if tc.unscoped {
				usage.GroupID = nil
			}
			_, err = repo.Create(ctx, usage)
			require.NoError(t, err)
			var status, reason, amount, minimum, source, rule, balance string
			require.NoError(t, integrationDB.QueryRow(`SELECT status,reason,amount::text,minimum_amount::text,charge_source,calculation_rule FROM cyber_policy_auto_charges WHERE usage_id=$1 AND usage_created_at=$2`, usage.ID, usage.CreatedAt).Scan(&status, &reason, &amount, &minimum, &source, &rule))
			require.Equal(t, tc.status, status)
			require.Equal(t, tc.reason, reason)
			require.Equal(t, tc.amount, amount)
			require.Equal(t, "6.70000000", minimum)
			require.Equal(t, "upstream_type4", source)
			require.Equal(t, "max_cost_multiplier_minimum", rule)
			require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, user.ID).Scan(&balance))
			require.Equal(t, tc.remaining, balance)
			var actual float64
			require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost FROM usage_logs WHERE id=$1 AND created_at=$2`, usage.ID, usage.CreatedAt).Scan(&actual))
			require.Equal(t, tc.cost, actual, "the minimum is an additional charge, never a fabricated upstream cost")
			// An identical helper invocation is harmless, including failed or insufficient decisions.
			_, err = integrationDB.Exec(`SELECT apply_cyber_policy_auto_charge(e.usage_id,e.usage_created_at,NULL,e.user_id,e.group_id,l.actual_cost,e.auto_deduct_enabled,e.audit_multiplier,e.scope_matched,e.count_reason,e.settings_revision,e.evaluated_at) FROM cyber_policy_count_events e JOIN usage_logs l ON l.id=e.usage_id AND l.created_at=e.usage_created_at WHERE e.usage_id=$1 AND e.usage_created_at=$2`, usage.ID, usage.CreatedAt)
			require.NoError(t, err)
			require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, user.ID).Scan(&balance))
			require.Equal(t, tc.remaining, balance)
			var claims, ledger int
			require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM cyber_policy_charges WHERE usage_id=$1 AND usage_created_at=$2`, usage.ID, usage.CreatedAt).Scan(&claims))
			require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM redeem_codes WHERE used_by=$1 AND notes LIKE '自动安全审计扣款%'`, user.ID).Scan(&ledger))
			want := 0
			if tc.status == "deducted" {
				want = 1
			}
			require.Equal(t, want, claims)
			require.Equal(t, want, ledger)
		})
	}
	// Ordinary completed audit actions keep their exact cost-multiplier rule.
	_, err := svc.SaveSettings(ctx, service.CyberPolicySettingsInput{UserAllowlistIDs: &[]int64{}, GroupRules: &[]service.CyberPolicyGroupRule{{GroupID: group.ID, Multiplier: "2", AutoDeductEnabled: true}}})
	require.NoError(t, err)
	user := mustCreateUser(t, client, &service.User{Email: "pk54-local-" + uuid.NewString() + "@example.com", Balance: 100})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "pk54-local-" + uuid.NewString(), Name: "local", GroupID: &group.ID})
	for _, cost := range []float64{0, 0.5} {
		usage := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, GroupID: &group.ID, RequestID: uuid.NewString(), Model: "local", ActualCost: cost, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
		_, err = repo.Create(ctx, usage)
		require.NoError(t, err)
		if cost > 0 {
			// An imported ordinary response may still have an independent local
			// charge_allow action. Only imported type4 suppresses local penalties.
			require.NoError(t, repo.RegisterAuditSettlementImport(ctx, service.AuditSettlementImportInput{
				UsageID: usage.ID, UsageCreatedAt: usage.CreatedAt, AccountID: account.ID, UserID: user.ID,
				APIKeyID: key.ID, GroupID: &group.ID, UpstreamIdentity: strings.Repeat("d", 64), ReceiptID: uuid.NewString(),
			}))
		}
		require.NoError(t, svc.RecordAuditUsage(ctx, service.CyberPolicyAuditBlockInput{EventKey: uuid.NewString(), UserID: user.ID, GroupID: &group.ID, Source: "prompt_audit", PolicyAction: "charge_allow"}, usage))
		var status, amount, minimum, rule string
		require.NoError(t, integrationDB.QueryRow(`SELECT status,amount::text,minimum_amount::text,calculation_rule FROM cyber_policy_auto_charges WHERE usage_id=$1 AND usage_created_at=$2`, usage.ID, usage.CreatedAt).Scan(&status, &amount, &minimum, &rule))
		require.Equal(t, "cost_multiplier", rule)
		require.Equal(t, "0.00000000", minimum)
		if cost == 0 {
			require.Equal(t, "zero_cost", status)
			require.Equal(t, "0.00000000", amount)
		} else {
			require.Equal(t, "deducted", status)
			require.Equal(t, "1.00000000", amount)
			var localClaims int
			require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM cyber_policy_charges WHERE usage_id=$1 AND usage_created_at=$2 AND auto_charge_id IS NOT NULL`, usage.ID, usage.CreatedAt).Scan(&localClaims))
			require.Equal(t, 1, localClaims, "a live ordinary import must not suppress an independent local audit charge")
		}
	}
	block := service.CyberPolicyAuditBlockInput{EventKey: uuid.NewString(), UserID: user.ID, GroupID: &group.ID, Source: "content_audit", PolicyAction: "block"}
	require.NoError(t, svc.RecordAuditBlock(ctx, block))
	var blockStatus, blockAmount string
	require.NoError(t, integrationDB.QueryRow(`SELECT ac.status,ac.amount::text FROM cyber_policy_auto_charges ac JOIN cyber_policy_audit_blocks b ON b.id=ac.audit_id WHERE b.event_key=$1`, block.EventKey).Scan(&blockStatus, &blockAmount))
	require.Equal(t, "zero_cost", blockStatus)
	require.Equal(t, "0.00000000", blockAmount)
	// A settings edit versions the minimum but does not rewrite existing decisions.
	before, err := svc.Settings(ctx)
	require.NoError(t, err)
	newMinimum := "9"
	after, err := svc.SaveSettings(ctx, service.CyberPolicySettingsInput{Multiplier: &newMinimum})
	require.NoError(t, err)
	require.Greater(t, after.SettingsRevision, before.SettingsRevision)
	var oldMinimum string
	require.NoError(t, integrationDB.QueryRow(`SELECT minimum_amount::text FROM cyber_policy_auto_charges WHERE charge_source='upstream_type4' AND group_id=$1 ORDER BY id LIMIT 1`, group.ID).Scan(&oldMinimum))
	require.Equal(t, "6.70000000", oldMinimum)
	newUsage := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, GroupID: &group.ID, RequestID: uuid.NewString(), Model: "new minimum",
		RequestType: service.RequestTypeCyberBlocked, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	_, err = repo.Create(ctx, newUsage)
	require.NoError(t, err)
	var newAmount, newSnapshot string
	require.NoError(t, integrationDB.QueryRow(`SELECT amount::text,minimum_amount::text FROM cyber_policy_auto_charges WHERE usage_id=$1 AND usage_created_at=$2`, newUsage.ID, newUsage.CreatedAt).Scan(&newAmount, &newSnapshot))
	require.Equal(t, "9.00000000", newAmount)
	require.Equal(t, "9.00000000", newSnapshot)
}

func TestPK54AuditSettlementUpstreamType4ReceiptChargesEachHopOnce(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	group := mustCreateGroup(t, client, &service.Group{Name: "pk54-hop-" + uuid.NewString()})
	configurePK54UpstreamMinimum(t, group.ID)
	account := mustCreateAccount(t, client, &service.Account{Name: "type4 relay"})
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	svc := service.NewCyberPolicyService(integrationDB, nil, nil)
	makeUser := func(name string) (*service.User, *service.APIKey) {
		user := mustCreateUser(t, client, &service.User{Email: name + "-" + uuid.NewString() + "@example.com", Balance: 100})
		key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: name + "-" + uuid.NewString(), Name: name, GroupID: &group.ID})
		return user, key
	}
	upstreamUser, upstreamKey := makeUser("upstream")
	downstreamUser, downstreamKey := makeUser("downstream")
	otherUser, otherKey := makeUser("other")
	makeUsage := func(user *service.User, key *service.APIKey, imported bool) *service.UsageLog {
		usage := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, GroupID: &group.ID, RequestID: uuid.NewString(), Model: "type4 hop",
			RequestType: service.RequestTypeCyberBlocked, AuditSettlementImportedEvent: imported, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
		_, err := repo.Create(ctx, usage)
		require.NoError(t, err)
		return usage
	}
	upstreamUsage := makeUsage(upstreamUser, upstreamKey, false)
	receiptCtx, receipt, err := svc.BeginAuditSettlement(ctx, upstreamUser.ID, upstreamKey.ID)
	require.NoError(t, err)
	service.RecordAuditSettlementUsage(receiptCtx, upstreamUsage)
	service.EndAuditSettlementRequest(receiptCtx)
	exported, err := svc.AuditSettlement(ctx, upstreamUser.ID, upstreamKey.ID, receipt)
	require.NoError(t, err)
	require.Equal(t, "ready", exported.Status)
	require.Equal(t, "6.70000000", *exported.AdditionalCost)
	downstreamUsage := makeUsage(downstreamUser, downstreamKey, true)
	persistedUsage, err := repo.GetByID(ctx, downstreamUsage.ID)
	require.NoError(t, err)
	require.True(t, persistedUsage.AuditSettlementImportedEvent, "the trusted imported-type4 marker must survive SQL insert/read roundtrip")
	require.Equal(t, service.RequestTypeCyberBlocked, persistedUsage.RequestType)
	in := service.AuditSettlementImportInput{UsageID: downstreamUsage.ID, UsageCreatedAt: downstreamUsage.CreatedAt, AccountID: account.ID, UserID: downstreamUser.ID, APIKeyID: downstreamKey.ID, GroupID: &group.ID, UpstreamIdentity: strings.Repeat("c", 64), ReceiptID: receipt}
	require.NoError(t, repo.RegisterAuditSettlementImport(ctx, in))
	require.NoError(t, repo.RegisterAuditSettlementImport(ctx, in))
	var importID int64
	require.NoError(t, integrationDB.QueryRow(`UPDATE audit_settlement_imports SET claimed_by='type4-fixture',charge_started_at=NOW() WHERE usage_id=$1 AND usage_created_at=$2 RETURNING id`, downstreamUsage.ID, downstreamUsage.CreatedAt).Scan(&importID))
	for i := 0; i < 3; i++ {
		var status string
		require.NoError(t, integrationDB.QueryRow(`SELECT settle_audit_settlement_import($1,'type4-fixture',$2::numeric)`, importID, *exported.AdditionalCost).Scan(&status))
		require.Equal(t, "deducted", status)
	}
	var balance string
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, downstreamUser.ID).Scan(&balance))
	require.Equal(t, "93.30000000", balance, "a relayed type4 must not pay both the local minimum and imported amount")
	var charges, counts, manual, ledger int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM cyber_policy_auto_charges WHERE user_id=$1`, downstreamUser.ID).Scan(&charges))
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM cyber_policy_count_events WHERE user_id=$1`, downstreamUser.ID).Scan(&counts))
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM cyber_policy_charges WHERE user_id=$1`, downstreamUser.ID).Scan(&manual))
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM redeem_codes WHERE used_by=$1`, downstreamUser.ID).Scan(&ledger))
	require.Zero(t, charges)
	require.Zero(t, counts)
	require.Zero(t, manual)
	require.Equal(t, 1, ledger)
	_, err = svc.Preview(ctx, 42, downstreamUser.ID, service.CyberPolicyWindowInput{})
	require.Equal(t, "CYBER_NOTHING_TO_DEDUCT", infraerrors.Reason(err))
	records, err := svc.Records(ctx, downstreamUser.ID, service.CyberPolicyWindowInput{}, 1, 20)
	require.NoError(t, err)
	require.Zero(t, records.Total)
	// A stale/older binary's manual preview cannot bypass the durable marker.
	previewID := uuid.NewString()
	_, err = integrationDB.Exec(`INSERT INTO cyber_policy_deductions(id,actor_id,user_id,email,username,start_date,end_date,timezone,trigger_count,multiplier,balance)
		VALUES($1,42,$2,'fixture@example.com','fixture',CURRENT_DATE,CURRENT_DATE,'UTC',1,6.7,100)`, previewID, downstreamUser.ID)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`INSERT INTO cyber_policy_preview_items(deduction_id,usage_id,usage_created_at) VALUES($1,$2,$3)`, previewID, downstreamUsage.ID, downstreamUsage.CreatedAt)
	require.NoError(t, err)
	claim, err := integrationDB.Exec(`INSERT INTO cyber_policy_charges(usage_id,usage_created_at,deduction_id,user_id) VALUES($1,$2,$3,$4)`, downstreamUsage.ID, downstreamUsage.CreatedAt, previewID, downstreamUser.ID)
	require.NoError(t, err)
	affected, err := claim.RowsAffected()
	require.NoError(t, err)
	require.Zero(t, affected)
	_, err = svc.Deduct(ctx, 42, downstreamUser.ID, previewID)
	require.Equal(t, "CYBER_PREVIEW_STALE", infraerrors.Reason(err))
	_, err = integrationDB.Exec(`SELECT apply_cyber_policy_auto_charge($1,$2,NULL,$3,$4,0,true,2,true,'',1,NOW())`, downstreamUsage.ID, downstreamUsage.CreatedAt, downstreamUser.ID, group.ID)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, downstreamUser.ID).Scan(&balance))
	require.Equal(t, "93.30000000", balance)
	localCtx, localReceipt, err := svc.BeginAuditSettlement(ctx, downstreamUser.ID, downstreamKey.ID)
	require.NoError(t, err)
	service.RecordAuditSettlementUsage(localCtx, downstreamUsage)
	service.EndAuditSettlementRequest(localCtx)
	onward, err := svc.AuditSettlement(ctx, downstreamUser.ID, downstreamKey.ID, localReceipt)
	require.NoError(t, err)
	require.Equal(t, "6.70000000", *onward.AdditionalCost)
	require.Len(t, onward.Items, 1)
	otherUsage := makeUsage(otherUser, otherKey, true)
	conflict := in
	conflict.UsageID, conflict.UsageCreatedAt, conflict.UserID, conflict.APIKeyID = otherUsage.ID, otherUsage.CreatedAt, otherUser.ID, otherKey.ID
	require.Error(t, repo.RegisterAuditSettlementImport(ctx, conflict))
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, otherUser.ID).Scan(&balance))
	require.Equal(t, "100.00000000", balance)
	_, err = svc.AuditSettlement(ctx, otherUser.ID, otherKey.ID, receipt)
	require.ErrorIs(t, err, service.ErrAuditSettlementNotFound)
	raw, err := json.Marshal(onward)
	require.NoError(t, err)
	require.NotContains(t, string(raw), upstreamKey.Key)
	// Usage retention must not make a frozen old-binary preview chargeable.
	_, err = integrationDB.Exec(`DELETE FROM usage_logs WHERE id=$1 AND created_at=$2`, downstreamUsage.ID, downstreamUsage.CreatedAt)
	require.NoError(t, err)
	claim, err = integrationDB.Exec(`INSERT INTO cyber_policy_charges(usage_id,usage_created_at,deduction_id,user_id) VALUES($1,$2,$3,$4)`, downstreamUsage.ID, downstreamUsage.CreatedAt, previewID, downstreamUser.ID)
	require.NoError(t, err)
	affected, err = claim.RowsAffected()
	require.NoError(t, err)
	require.Zero(t, affected, "durable import evidence protects even after the usage flag was removed by retention")
	_, err = svc.Deduct(ctx, 42, downstreamUser.ID, previewID)
	require.Equal(t, "CYBER_PREVIEW_STALE", infraerrors.Reason(err))
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, downstreamUser.ID).Scan(&balance))
	require.Equal(t, "93.30000000", balance)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM redeem_codes WHERE used_by=$1`, downstreamUser.ID).Scan(&ledger))
	require.Equal(t, 1, ledger)
}

func TestUsageBillingRepositoryApply_DeduplicatesBalanceBilling(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-" + uuid.NewString(),
		Name:   "billing",
		Quota:  1,
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: "usage-billing-account-" + uuid.NewString(),
		Type: service.AccountTypeAPIKey,
	})

	requestID := uuid.NewString()
	cmd := &service.UsageBillingCommand{
		RequestID:           requestID,
		APIKeyID:            apiKey.ID,
		UserID:              user.ID,
		AccountID:           account.ID,
		AccountType:         service.AccountTypeAPIKey,
		BalanceCost:         1.25,
		APIKeyQuotaCost:     1.25,
		APIKeyRateLimitCost: 1.25,
	}

	result1, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result1)
	require.True(t, result1.Applied)
	require.True(t, result1.APIKeyQuotaExhausted)

	result2, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result2)
	require.False(t, result2.Applied)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 98.75, balance, 0.000001)

	var quotaUsed float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used FROM api_keys WHERE id = $1", apiKey.ID).Scan(&quotaUsed))
	require.InDelta(t, 1.25, quotaUsed, 0.000001)

	var usage5h float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT usage_5h FROM api_keys WHERE id = $1", apiKey.ID).Scan(&usage5h))
	require.InDelta(t, 1.25, usage5h, 0.000001)

	var status string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT status FROM api_keys WHERE id = $1", apiKey.ID).Scan(&status))
	require.Equal(t, service.StatusAPIKeyQuotaExhausted, status)

	var dedupCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2", requestID, apiKey.ID).Scan(&dedupCount))
	require.Equal(t, 1, dedupCount)
}

func TestUsageBillingRepositoryApply_DeduplicatesSubscriptionBilling(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-sub-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             "usage-billing-group-" + uuid.NewString(),
		Platform:         service.PlatformAnthropic,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-sub-" + uuid.NewString(),
		Name:    "billing-sub",
	})
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:  user.ID,
		GroupID: group.ID,
	})

	requestID := uuid.NewString()
	cmd := &service.UsageBillingCommand{
		RequestID:        requestID,
		APIKeyID:         apiKey.ID,
		UserID:           user.ID,
		AccountID:        0,
		SubscriptionID:   &subscription.ID,
		SubscriptionCost: 2.5,
	}

	result1, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result1.Applied)

	result2, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result2.Applied)

	var dailyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&dailyUsage))
	require.InDelta(t, 2.5, dailyUsage, 0.000001)
}

func TestUsageBillingRepositoryApply_RequestFingerprintConflict(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-conflict-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-conflict-" + uuid.NewString(),
		Name:   "billing-conflict",
	})

	requestID := uuid.NewString()
	_, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   requestID,
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		BalanceCost: 1.25,
	})
	require.NoError(t, err)

	_, err = repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   requestID,
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		BalanceCost: 2.50,
	})
	require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
}

func TestUsageBillingRepositoryApply_DeletedAPIKeyStillBillsBalance(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-deleted-key-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:      user.ID,
		Key:         "sk-usage-billing-deleted-key-" + uuid.NewString(),
		Name:        "billing-deleted-key",
		Quota:       50,
		RateLimit5h: 50,
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: "usage-billing-deleted-key-account-" + uuid.NewString(),
		Type: service.AccountTypeAPIKey,
	})

	_, err := integrationDB.ExecContext(ctx, "UPDATE api_keys SET deleted_at = NOW() WHERE id = $1", apiKey.ID)
	require.NoError(t, err)

	requestID := uuid.NewString()
	result, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:           requestID,
		APIKeyID:            apiKey.ID,
		UserID:              user.ID,
		AccountID:           account.ID,
		AccountType:         service.AccountTypeAPIKey,
		BalanceCost:         1.25,
		APIKeyQuotaCost:     1.25,
		APIKeyRateLimitCost: 1.25,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Applied)
	require.False(t, result.APIKeyQuotaExhausted)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 98.75, balance, 0.000001)

	var quotaUsed, usage5h float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used, usage_5h FROM api_keys WHERE id = $1", apiKey.ID).Scan(&quotaUsed, &usage5h))
	require.InDelta(t, 0, quotaUsed, 0.000001)
	require.InDelta(t, 0, usage5h, 0.000001)

	var dedupCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2", requestID, apiKey.ID).Scan(&dedupCount))
	require.Equal(t, 1, dedupCount)
}

func TestUsageBillingRepositoryApply_DeletedAccountStillBillsBalance(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-deleted-account-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-deleted-account-" + uuid.NewString(),
		Name:   "billing-deleted-account",
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name:     "usage-billing-deleted-account-" + uuid.NewString(),
		Platform: service.PlatformGemini,
		Type:     service.AccountTypeServiceAccount,
	})

	_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET deleted_at = NOW() WHERE id = $1", account.ID)
	require.NoError(t, err)

	requestID := uuid.NewString()
	result, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:        requestID,
		APIKeyID:         apiKey.ID,
		UserID:           user.ID,
		AccountID:        account.ID,
		AccountType:      service.AccountTypeServiceAccount,
		BalanceCost:      1.25,
		AccountQuotaCost: 1.25,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Applied)
	require.Nil(t, result.QuotaState)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 98.75, balance, 0.000001)

	var dedupCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2", requestID, apiKey.ID).Scan(&dedupCount))
	require.Equal(t, 1, dedupCount)
}

func TestUsageBillingRepositoryApply_UpdatesAccountQuota(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-account-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-account-" + uuid.NewString(),
		Name:   "billing-account",
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: "usage-billing-account-quota-" + uuid.NewString(),
		Type: service.AccountTypeAPIKey,
		Extra: map[string]any{
			"quota_limit": 100.0,
		},
	})

	_, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:        uuid.NewString(),
		APIKeyID:         apiKey.ID,
		UserID:           user.ID,
		AccountID:        account.ID,
		AccountType:      service.AccountTypeAPIKey,
		AccountQuotaCost: 3.5,
	})
	require.NoError(t, err)

	var quotaUsed float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COALESCE((extra->>'quota_used')::numeric, 0) FROM accounts WHERE id = $1", account.ID).Scan(&quotaUsed))
	require.InDelta(t, 3.5, quotaUsed, 0.000001)
}

// service_account 只设总上限：累加 quota_used；日/周用量只在设置了对应 limit 时累加，未跨越上限不入队调度 outbox。
func TestUsageBillingRepositoryApply_UpdatesServiceAccountQuota(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-sa-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-sa-" + uuid.NewString(),
		Name:   "billing-sa",
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name:     "usage-billing-sa-quota-" + uuid.NewString(),
		Platform: service.PlatformGemini,
		Type:     service.AccountTypeServiceAccount,
		Extra: map[string]any{
			"quota_limit": 100.0,
		},
	})

	for _, cost := range []float64{2.25, 1.5} {
		_, err := repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:        uuid.NewString(),
			APIKeyID:         apiKey.ID,
			UserID:           user.ID,
			AccountID:        account.ID,
			AccountType:      service.AccountTypeServiceAccount,
			AccountQuotaCost: cost,
		})
		require.NoError(t, err)
	}

	var quotaUsed float64
	var hasDailyUsed, hasWeeklyUsed bool
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COALESCE((extra->>'quota_used')::numeric, 0), extra ? 'quota_daily_used', extra ? 'quota_weekly_used' FROM accounts WHERE id = $1",
		account.ID,
	).Scan(&quotaUsed, &hasDailyUsed, &hasWeeklyUsed))
	require.InDelta(t, 3.75, quotaUsed, 0.000001)
	require.False(t, hasDailyUsed)
	require.False(t, hasWeeklyUsed)

	var outboxCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND account_id = $2",
		service.SchedulerOutboxEventAccountChanged, account.ID,
	).Scan(&outboxCount))
	require.Zero(t, outboxCount, "service_account under its limit must not enqueue scheduler outbox")
}

func TestUsageBillingRepositoryApply_EnqueuesSchedulerOutboxOnQuotaCrossing(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)

	newFixture := func(t *testing.T, extra map[string]any) (int64, int64) {
		t.Helper()
		user := mustCreateUser(t, client, &service.User{
			Email:        fmt.Sprintf("usage-billing-outbox-user-%d-%s@example.com", time.Now().UnixNano(), uuid.NewString()),
			PasswordHash: "hash",
		})
		apiKey := mustCreateApiKey(t, client, &service.APIKey{
			UserID: user.ID,
			Key:    "sk-usage-billing-outbox-" + uuid.NewString(),
			Name:   "billing-outbox",
		})
		account := mustCreateAccount(t, client, &service.Account{
			Name:  "usage-billing-outbox-" + uuid.NewString(),
			Type:  service.AccountTypeAPIKey,
			Extra: extra,
		})
		return apiKey.ID, account.ID
	}

	outboxCountFor := func(t *testing.T, accountID int64) int {
		t.Helper()
		var count int
		require.NoError(t, integrationDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND account_id = $2",
			service.SchedulerOutboxEventAccountChanged, accountID,
		).Scan(&count))
		return count
	}

	t.Run("daily_first_crossing_enqueues", func(t *testing.T) {
		apiKeyID, accountID := newFixture(t, map[string]any{
			"quota_daily_limit": 10.0,
		})
		// 第一次低于日限额：不应入队 outbox
		_, err := repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:        uuid.NewString(),
			APIKeyID:         apiKeyID,
			AccountID:        accountID,
			AccountType:      service.AccountTypeAPIKey,
			AccountQuotaCost: 4,
		})
		require.NoError(t, err)
		require.Equal(t, 0, outboxCountFor(t, accountID), "below limit should not enqueue")

		// 第二次跨越日限额：应入队一次 outbox
		_, err = repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:        uuid.NewString(),
			APIKeyID:         apiKeyID,
			AccountID:        accountID,
			AccountType:      service.AccountTypeAPIKey,
			AccountQuotaCost: 8,
		})
		require.NoError(t, err)
		require.Equal(t, 1, outboxCountFor(t, accountID), "crossing daily limit should enqueue once")

		// 再次递增（已超）：不应重复入队
		_, err = repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:        uuid.NewString(),
			APIKeyID:         apiKeyID,
			AccountID:        accountID,
			AccountType:      service.AccountTypeAPIKey,
			AccountQuotaCost: 2,
		})
		require.NoError(t, err)
		require.Equal(t, 1, outboxCountFor(t, accountID), "subsequent increments beyond limit should not re-enqueue")
	})

	t.Run("weekly_first_crossing_enqueues", func(t *testing.T) {
		apiKeyID, accountID := newFixture(t, map[string]any{
			"quota_weekly_limit": 10.0,
		})
		_, err := repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:        uuid.NewString(),
			APIKeyID:         apiKeyID,
			AccountID:        accountID,
			AccountType:      service.AccountTypeAPIKey,
			AccountQuotaCost: 15, // 单次即跨越
		})
		require.NoError(t, err)
		require.Equal(t, 1, outboxCountFor(t, accountID), "single-shot crossing weekly limit should enqueue once")
	})

	t.Run("total_first_crossing_enqueues", func(t *testing.T) {
		apiKeyID, accountID := newFixture(t, map[string]any{
			"quota_limit": 10.0,
		})
		_, err := repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:        uuid.NewString(),
			APIKeyID:         apiKeyID,
			AccountID:        accountID,
			AccountType:      service.AccountTypeAPIKey,
			AccountQuotaCost: 4,
		})
		require.NoError(t, err)
		require.Equal(t, 0, outboxCountFor(t, accountID), "below limit should not enqueue")

		_, err = repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:        uuid.NewString(),
			APIKeyID:         apiKeyID,
			AccountID:        accountID,
			AccountType:      service.AccountTypeAPIKey,
			AccountQuotaCost: 8,
		})
		require.NoError(t, err)
		require.Equal(t, 1, outboxCountFor(t, accountID), "crossing total limit should enqueue once")
	})
}

func TestIncrementUsageBillingAccountQuota_StatementVariantsMatchFullStatement(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	now := time.Now().UTC()
	at := func(offset time.Duration) string { return now.Add(offset).Format(time.RFC3339) }

	// dailyUnset/weeklyUnset 为库中该维度视为未设置上限：省略该维度的语句在这些配置上命中，其余配置命中 0 行。
	configs := []struct {
		name        string
		extra       map[string]any
		rawExtra    string
		dailyUnset  bool
		weeklyUnset bool
		wantErr     bool
	}{
		{name: "no_limits", extra: map[string]any{}, dailyUnset: true, weeklyUnset: true},
		{name: "total_only", extra: map[string]any{"quota_limit": 100.0, "quota_used": 3.0}, dailyUnset: true, weeklyUnset: true},
		{name: "zero_and_negative_period_limits", extra: map[string]any{"quota_limit": 5.0, "quota_daily_limit": 0.0, "quota_weekly_limit": -1.0}, dailyUnset: true, weeklyUnset: true},
		{name: "null_period_limits", extra: map[string]any{"quota_daily_limit": nil, "quota_weekly_limit": nil}, dailyUnset: true, weeklyUnset: true},
		{name: "string_period_limits", extra: map[string]any{"quota_daily_limit": "10", "quota_weekly_limit": "0"}},
		{name: "daily_rolling_active", extra: map[string]any{"quota_daily_limit": 10.0, "quota_daily_used": 4.0, "quota_daily_start": at(-time.Hour)}, weeklyUnset: true},
		{name: "daily_rolling_expired", extra: map[string]any{"quota_daily_limit": 10.0, "quota_daily_used": 9.0, "quota_daily_start": at(-30 * time.Hour)}, weeklyUnset: true},
		{name: "daily_fixed_expired", extra: map[string]any{
			"quota_daily_limit": 10.0, "quota_daily_used": 7.0, "quota_daily_reset_mode": "fixed",
			"quota_daily_reset_hour": 3, "quota_reset_timezone": "Asia/Shanghai", "quota_daily_reset_at": at(-2 * time.Hour),
		}, weeklyUnset: true},
		{name: "weekly_rolling_active", extra: map[string]any{"quota_weekly_limit": 50.0, "quota_weekly_used": 20.0, "quota_weekly_start": at(-48 * time.Hour)}, dailyUnset: true},
		{name: "weekly_fixed_expired", extra: map[string]any{
			"quota_weekly_limit": 50.0, "quota_weekly_used": 49.0, "quota_weekly_reset_mode": "fixed",
			"quota_weekly_reset_day": 1, "quota_weekly_reset_hour": 0, "quota_reset_timezone": "UTC", "quota_weekly_reset_at": at(-time.Hour),
		}, dailyUnset: true},
		{name: "daily_and_weekly", extra: map[string]any{
			"quota_limit": 100.0, "quota_daily_limit": 10.0, "quota_weekly_limit": 50.0,
			"quota_daily_used": 1.0, "quota_weekly_used": 2.0, "quota_daily_start": at(-time.Hour), "quota_weekly_start": at(-200 * time.Hour),
		}},
		{name: "array_extra", rawExtra: `[]`},
		{name: "scalar_extra", rawExtra: `"value"`},
		{name: "non_numeric_daily_limit", extra: map[string]any{"quota_daily_limit": "abc"}, weeklyUnset: true, wantErr: true},
		{name: "boolean_weekly_limit", extra: map[string]any{"quota_weekly_limit": true}, dailyUnset: true, wantErr: true},
	}

	for _, cfg := range configs {
		for _, hints := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
			t.Run(fmt.Sprintf("%s/daily=%v/weekly=%v", cfg.name, hints[0], hints[1]), func(t *testing.T) {
				variant := mustCreateAccount(t, client, &service.Account{
					Name:  "usage-billing-quota-variant-" + uuid.NewString(),
					Type:  service.AccountTypeAPIKey,
					Extra: maps.Clone(cfg.extra),
				})
				reference := mustCreateAccount(t, client, &service.Account{
					Name:  "usage-billing-quota-reference-" + uuid.NewString(),
					Type:  service.AccountTypeAPIKey,
					Extra: maps.Clone(cfg.extra),
				})
				if cfg.rawExtra != "" {
					_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET extra = $1::jsonb WHERE id IN ($2, $3)", cfg.rawExtra, variant.ID, reference.ID)
					require.NoError(t, err)
					t.Cleanup(func() {
						_, _ = integrationDB.ExecContext(ctx, "UPDATE accounts SET extra = '{}'::jsonb WHERE id IN ($1, $2)", variant.ID, reference.ID)
					})
				}

				if cfg.wantErr {
					sqlState := func(accountID int64, daily, weekly bool) pq.ErrorCode {
						tx, err := integrationDB.BeginTx(ctx, nil)
						require.NoError(t, err)
						defer func() { _ = tx.Rollback() }()
						_, err = incrementUsageBillingAccountQuota(ctx, tx, accountID, 2.5, daily, weekly)
						var pqErr *pq.Error
						require.ErrorAs(t, err, &pqErr)
						return pqErr.Code
					}
					require.Equal(t, sqlState(reference.ID, true, true), sqlState(variant.ID, hints[0], hints[1]))
					return
				}

				tx, err := integrationDB.BeginTx(ctx, nil)
				require.NoError(t, err)
				defer func() { _ = tx.Rollback() }()

				isObject := cfg.rawExtra == ""
				wantHit := (hints[0] && hints[1]) || (isObject && (hints[0] || cfg.dailyUnset) && (hints[1] || cfg.weeklyUnset))
				_, err = tx.ExecContext(ctx, "SAVEPOINT quota_variant_probe")
				require.NoError(t, err)
				probe, err := execAccountQuotaIncrement(ctx, tx, accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(hints[0], hints[1])], variant.ID, 2.5)
				require.NoError(t, err)
				require.Equal(t, wantHit, probe != nil, "variant statement hit")
				_, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT quota_variant_probe")
				require.NoError(t, err)

				got, err := incrementUsageBillingAccountQuota(ctx, tx, variant.ID, 2.5, hints[0], hints[1])
				require.NoError(t, err)
				want, err := incrementUsageBillingAccountQuota(ctx, tx, reference.ID, 2.5, true, true)
				require.NoError(t, err)
				require.Equal(t, *want, *got)

				var gotExtra, wantExtra string
				var sameExtra, sameUpdatedAt bool
				require.NoError(t, tx.QueryRowContext(ctx,
					"SELECT v.extra::text, r.extra::text, v.extra = r.extra, v.updated_at = r.updated_at FROM accounts v, accounts r WHERE v.id = $1 AND r.id = $2",
					variant.ID, reference.ID,
				).Scan(&gotExtra, &wantExtra, &sameExtra, &sameUpdatedAt))
				require.True(t, sameExtra, "variant extra %s != reference extra %s", gotExtra, wantExtra)
				require.True(t, sameUpdatedAt)
			})
		}
	}
}

func TestDashboardAggregationRepositoryCleanupUsageBillingDedup_BatchDeletesOldRows(t *testing.T) {
	ctx := context.Background()
	repo := newDashboardAggregationRepositoryWithSQL(integrationDB)

	oldRequestID := "dedup-old-" + uuid.NewString()
	newRequestID := "dedup-new-" + uuid.NewString()
	oldCreatedAt := time.Now().UTC().AddDate(0, 0, -400)
	newCreatedAt := time.Now().UTC().Add(-time.Hour)

	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint, created_at)
		VALUES ($1, 1, $2, $3), ($4, 1, $5, $6)
	`,
		oldRequestID, strings.Repeat("a", 64), oldCreatedAt,
		newRequestID, strings.Repeat("b", 64), newCreatedAt,
	)
	require.NoError(t, err)

	require.NoError(t, repo.CleanupUsageBillingDedup(ctx, time.Now().UTC().AddDate(0, 0, -365)))

	var oldCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1", oldRequestID).Scan(&oldCount))
	require.Equal(t, 0, oldCount)

	var newCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1", newRequestID).Scan(&newCount))
	require.Equal(t, 1, newCount)

	var archivedCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup_archive WHERE request_id = $1", oldRequestID).Scan(&archivedCount))
	require.Equal(t, 1, archivedCount)
}

func TestUsageBillingRepositoryApply_DeduplicatesAgainstArchivedKey(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)
	aggRepo := newDashboardAggregationRepositoryWithSQL(integrationDB)

	user := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("usage-billing-archive-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-archive-" + uuid.NewString(),
		Name:   "billing-archive",
	})

	requestID := uuid.NewString()
	cmd := &service.UsageBillingCommand{
		RequestID:   requestID,
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		BalanceCost: 1.25,
	}

	result1, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result1.Applied)

	_, err = integrationDB.ExecContext(ctx, `
		UPDATE usage_billing_dedup
		SET created_at = $1
		WHERE request_id = $2 AND api_key_id = $3
	`, time.Now().UTC().AddDate(0, 0, -400), requestID, apiKey.ID)
	require.NoError(t, err)
	require.NoError(t, aggRepo.CleanupUsageBillingDedup(ctx, time.Now().UTC().AddDate(0, 0, -365)))

	result2, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result2.Applied)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 98.75, balance, 0.000001)
}

// These assertions exercise the real SQL transaction and provider query, not a
// mocked balance repository. They never invoke an upstream model.
func TestPK54AuditSettlementFinancialIdempotency(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Balance: 20})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "pk54-" + uuid.NewString(), Name: "audit settlement"})
	account := mustCreateAccount(t, client, &service.Account{Name: "audit settlement"})
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	newImport := func() (*service.UsageLog, int64) {
		usage := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, Model: "fixture", RequestID: uuid.NewString(), InputTokens: 10, OutputTokens: 5, TotalCost: 0.5, ActualCost: 0.5, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
		_, err := repo.Create(ctx, usage)
		require.NoError(t, err)
		in := service.AuditSettlementImportInput{UsageID: usage.ID, UsageCreatedAt: usage.CreatedAt, UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, ReceiptID: uuid.NewString(), UpstreamIdentity: strings.Repeat("a", 64)}
		require.NoError(t, repo.RegisterAuditSettlementImport(ctx, in))
		require.NoError(t, repo.RegisterAuditSettlementImport(ctx, in))
		var id int64
		require.NoError(t, integrationDB.QueryRow(`UPDATE audit_settlement_imports SET claimed_by='fixture',charge_started_at=NOW() WHERE usage_id=$1 AND usage_created_at=$2 RETURNING id`, usage.ID, usage.CreatedAt).Scan(&id))
		return usage, id
	}
	usage, id := newImport()
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var status string
			err := integrationDB.QueryRow(`SELECT settle_audit_settlement_import($1,'fixture',0.25)`, id).Scan(&status)
			if err == nil && status != "deducted" {
				err = fmt.Errorf("unexpected %s", status)
			}
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var balance, ordinaryCost string
	var ledgerCount, outboxCount int
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, user.ID).Scan(&balance))
	require.Equal(t, "19.75000000", balance)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM redeem_codes WHERE used_by=$1 AND notes LIKE '上游审计追加费用%'`, user.ID).Scan(&ledgerCount))
	require.Equal(t, 1, ledgerCount)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM cyber_policy_auto_charge_outbox WHERE user_id=$1`, user.ID).Scan(&outboxCount))
	require.Equal(t, 1, outboxCount)
	require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost::text FROM usage_logs WHERE id=$1 AND created_at=$2`, usage.ID, usage.CreatedAt).Scan(&ordinaryCost))
	require.InDelta(t, 0.5, usage.ActualCost, 1e-10)
	var sameUsageCost float64
	require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost FROM usage_logs WHERE id=$1 AND created_at=$2`, usage.ID, usage.CreatedAt).Scan(&sameUsageCost))
	require.Equal(t, 0.5, sameUsageCost)
	_, insufficientID := newImport()
	var status string
	require.NoError(t, integrationDB.QueryRow(`SELECT settle_audit_settlement_import($1,'fixture',99)`, insufficientID).Scan(&status))
	require.Equal(t, "insufficient_balance", status)
	require.NoError(t, integrationDB.QueryRow(`SELECT settle_audit_settlement_import($1,'fixture',0.01)`, insufficientID).Scan(&status))
	require.Equal(t, "insufficient_balance", status, "terminal results must never retry after balance or price changes")
	_, zeroID := newImport()
	require.NoError(t, integrationDB.QueryRow(`SELECT settle_audit_settlement_import($1,'fixture',0)`, zeroID).Scan(&status))
	require.Equal(t, "zero_cost", status)
	// Force a ledger write failure after the user debit, verifying subtransaction rollback.
	_, failedID := newImport()
	_, err := integrationDB.Exec(fmt.Sprintf(`CREATE FUNCTION pk54_reject_ledger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.used_by=%d AND NEW.notes LIKE '上游审计追加费用%%' THEN RAISE EXCEPTION 'fixture ledger unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER pk54_reject_ledger BEFORE INSERT ON redeem_codes FOR EACH ROW EXECUTE FUNCTION pk54_reject_ledger()`, user.ID))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`DROP TRIGGER IF EXISTS pk54_reject_ledger ON redeem_codes; DROP FUNCTION IF EXISTS pk54_reject_ledger()`)
	})
	require.NoError(t, integrationDB.QueryRow(`SELECT settle_audit_settlement_import($1,'fixture',0.5)`, failedID).Scan(&status))
	require.Equal(t, "failed", status)
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, user.ID).Scan(&balance))
	require.Equal(t, "19.75000000", balance)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM redeem_codes WHERE used_by=$1 AND notes LIKE '上游审计追加费用%'`, user.ID).Scan(&ledgerCount))
	require.Equal(t, 1, ledgerCount)
}

func TestPK54AuditSettlementProviderMultiHopAndOwnership(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Balance: 20})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "pk54-" + uuid.NewString(), Name: "receipt"})
	account := mustCreateAccount(t, client, &service.Account{Name: "receipt"})
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	usage := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, Model: "fixture", RequestID: uuid.NewString(), ActualCost: 0.5, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	_, err := repo.Create(ctx, usage)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`INSERT INTO cyber_policy_auto_charges(usage_id,usage_created_at,user_id,actual_cost,multiplier,amount,status,settings_revision,evaluated_at) VALUES($1,$2,$3,0.5,0.5,0.25,'deducted',1,NOW())`, usage.ID, usage.CreatedAt, user.ID)
	require.NoError(t, err)
	in := service.AuditSettlementImportInput{UsageID: usage.ID, UsageCreatedAt: usage.CreatedAt, UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, ReceiptID: uuid.NewString(), UpstreamIdentity: strings.Repeat("b", 64)}
	require.NoError(t, repo.RegisterAuditSettlementImport(ctx, in))
	svc := service.NewCyberPolicyService(integrationDB, nil, nil)
	receiptCtx, receipt, err := svc.BeginAuditSettlement(ctx, user.ID, key.ID)
	require.NoError(t, err)
	service.RecordAuditSettlementUsage(receiptCtx, usage)
	service.EndAuditSettlementRequest(receiptCtx)
	got, err := svc.AuditSettlement(ctx, user.ID, key.ID, receipt)
	require.NoError(t, err)
	require.Equal(t, "pending", got.Status)
	require.Nil(t, got.AdditionalCost)
	_, err = svc.AuditSettlement(ctx, user.ID, key.ID+999, receipt)
	require.ErrorIs(t, err, service.ErrAuditSettlementNotFound)
	_, err = svc.AuditSettlement(ctx, user.ID+999, key.ID, receipt)
	require.ErrorIs(t, err, service.ErrAuditSettlementNotFound)
	for _, status := range []string{"failed", "unavailable", "insufficient_balance", "skipped", "zero_cost", "deducted"} {
		amount := "0"
		if status == "deducted" {
			amount = "0.125"
		}
		_, err = integrationDB.Exec(`UPDATE audit_settlement_imports SET status=$3,amount=$4::numeric,reported_amount=$4::numeric WHERE usage_id=$1 AND usage_created_at=$2`, usage.ID, usage.CreatedAt, status, amount)
		require.NoError(t, err)
		got, err = svc.AuditSettlement(ctx, user.ID, key.ID, receipt)
		require.NoError(t, err)
		if status == "failed" || status == "unavailable" {
			require.Equal(t, "unavailable", got.Status)
			require.Nil(t, got.AdditionalCost)
		} else {
			require.Equal(t, "ready", got.Status)
			require.NotNil(t, got.AdditionalCost)
			expected := "0.25000000"
			if status == "deducted" {
				expected = "0.37500000"
				require.Len(t, got.Items, 2)
			}
			require.Equal(t, expected, *got.AdditionalCost)
		}
	}
	// An idempotent original bill must not be exportable again via a fresh receipt.
	secondCtx, second, err := svc.BeginAuditSettlement(ctx, user.ID, key.ID)
	require.NoError(t, err)
	service.RecordAuditSettlementUsage(secondCtx, usage)
	service.EndAuditSettlementRequest(secondCtx)
	got, err = svc.AuditSettlement(ctx, user.ID, key.ID, second)
	require.NoError(t, err)
	require.Equal(t, "unavailable", got.Status)
	// A repeated remote receipt cannot be attached to a different local usage.
	in.UsageID += 123456
	require.Error(t, repo.RegisterAuditSettlementImport(ctx, in))
}

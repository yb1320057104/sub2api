package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccount_BillingRateMultiplier_DefaultsToOneWhenNil(t *testing.T) {
	var a Account
	require.NoError(t, json.Unmarshal([]byte(`{"id":1,"name":"acc","status":"active"}`), &a))
	require.Nil(t, a.RateMultiplier)
	require.Equal(t, 1.0, a.BillingRateMultiplier())
}

func TestPKHiddenBillingSettings(t *testing.T) {
	defaults, err := normalizeHiddenBillingExtra(nil, nil)
	require.NoError(t, err)
	require.Nil(t, defaults, "omission must preserve the legacy extra shape")
	require.Equal(t, 1.0, (&Account{Extra: defaults}).HiddenBillingMultiplier())
	require.Equal(t, 1.0, (*Account)(nil).HiddenBillingMultiplier())
	cleared, err := normalizeHiddenBillingExtra(map[string]any{}, map[string]any{"quota_limit": 100.0})
	require.NoError(t, err)
	require.NotNil(t, cleared)
	require.Empty(t, cleared, "clearing old quota settings must retain the original empty-object behavior")

	for _, value := range []any{0, 1.0, 2.0, 0.29, 0.000001, 1000.0, json.Number("2.123456")} {
		current := map[string]any{AccountExtraHiddenRateMultiplier: value}
		updated, err := normalizeHiddenBillingExtra(map[string]any{"unrelated": true}, current)
		require.NoError(t, err)
		expected, ok := parseHiddenRateMultiplier(value)
		require.True(t, ok)
		require.Equal(t, expected, updated[AccountExtraHiddenRateMultiplier])
		require.Equal(t, value, current[AccountExtraHiddenRateMultiplier], "caller maps must not be mutated")
	}
	for _, value := range []any{nil, "2", true, -1.0, 1001.0, 0.1234567, math.NaN(), math.Inf(1)} {
		extra := map[string]any{AccountExtraHiddenRateMultiplier: value}
		require.Error(t, ValidateHiddenBillingExtra(extra))
		svc := &adminServiceImpl{}
		require.Error(t, svc.UpdateAccountExtra(context.Background(), 1, extra))
		_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: extra})
		require.Error(t, err, "invalid bulk settings must fail before touching any account")
	}

	accountRate := 7.0
	account, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, RateMultiplier: &accountRate}, map[string]any{AccountExtraHiddenRateMultiplier: 2.0})
	require.NoError(t, err)
	require.Equal(t, 2.0, account.HiddenBillingMultiplier())
	require.Equal(t, 7.0, account.BillingRateMultiplier())
}

func TestPKHiddenBillingBridgePrecisionAndIdempotency(t *testing.T) {
	account := &Account{Extra: map[string]any{AccountExtraHiddenRateMultiplier: 0.29}}
	cost := &CostBreakdown{TotalCost: 100, ActualCost: 100}
	log := &UsageLog{TotalCost: 100, ActualCost: 100, RateMultiplier: 1}
	require.NoError(t, applyHiddenBillingBridge(account, cost, log))
	require.Equal(t, 29.0, cost.ActualCost)
	require.Equal(t, 29.0, log.ActualCost)
	require.Equal(t, 100.0, log.TotalCost)
	account.Extra[AccountExtraHiddenRateMultiplier] = 3.0
	require.NoError(t, applyHiddenBillingBridge(account, cost, log))
	require.Equal(t, 29.0, cost.ActualCost, "bridge must not multiply a recorded snapshot twice")
	require.Equal(t, 0.29, log.HiddenBillingMultiplier())

	account.Extra[AccountExtraHiddenRateMultiplier] = 2.0
	cost = &CostBreakdown{TotalCost: 0.000000025, ActualCost: 0.000000025}
	log = &UsageLog{TotalCost: cost.TotalCost, RateMultiplier: 1}
	require.NoError(t, applyHiddenBillingBridge(account, cost, log))
	require.Equal(t, 0.00000005, log.ActualCost)

	cost = &CostBreakdown{ActualCost: math.MaxFloat64}
	log = &UsageLog{}
	require.Error(t, applyHiddenBillingBridge(account, cost, log))
	require.Nil(t, log.HiddenRateMultiplier, "overflow must not publish a successful snapshot")
}

func TestAccount_BillingRateMultiplier_AllowsZero(t *testing.T) {
	v := 0.0
	a := Account{RateMultiplier: &v}
	require.Equal(t, 0.0, a.BillingRateMultiplier())
}

func TestAccount_BillingRateMultiplier_NegativeFallsBackToOne(t *testing.T) {
	v := -1.0
	a := Account{RateMultiplier: &v}
	require.Equal(t, 1.0, a.BillingRateMultiplier())
}

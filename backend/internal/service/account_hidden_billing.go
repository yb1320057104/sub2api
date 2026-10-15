package service

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

// PK-029 keeps customer pricing separate from the upstream account-cost rate.
const AccountExtraHiddenRateMultiplier = "hidden_rate_multiplier"

func parseHiddenRateMultiplier(raw any) (float64, bool) {
	switch raw.(type) {
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
	default:
		return 0, false
	}
	value, err := decimal.NewFromString(fmt.Sprint(raw))
	if err != nil || value.IsNegative() || value.GreaterThan(decimal.NewFromInt(1000)) || !value.Equal(value.Round(6)) {
		return 0, false
	}
	multiplier, _ := value.Float64()
	return multiplier, true
}

func ValidateHiddenBillingExtra(extra map[string]any) error {
	if raw, exists := extra[AccountExtraHiddenRateMultiplier]; exists {
		if _, ok := parseHiddenRateMultiplier(raw); !ok {
			return infraerrors.BadRequest("HIDDEN_RATE_MULTIPLIER_INVALID", "hidden_rate_multiplier must be a number between 0 and 1000 with at most 6 decimal places")
		}
	}
	return nil
}

// Omitted keys retain the saved value, including an explicit zero. Clients that
// predate this extension can still replace other extra settings safely. A key
// absent from both maps stays absent and reads as one, preserving legacy shapes.
func normalizeHiddenBillingExtra(extra, current map[string]any) (map[string]any, error) {
	normalized := maps.Clone(extra)
	if _, exists := normalized[AccountExtraHiddenRateMultiplier]; !exists {
		if saved, ok := current[AccountExtraHiddenRateMultiplier]; ok {
			if normalized == nil {
				normalized = make(map[string]any)
			}
			normalized[AccountExtraHiddenRateMultiplier] = saved
		}
	}
	if err := ValidateHiddenBillingExtra(normalized); err != nil {
		return nil, err
	}
	if raw, exists := normalized[AccountExtraHiddenRateMultiplier]; exists {
		normalized[AccountExtraHiddenRateMultiplier], _ = parseHiddenRateMultiplier(raw)
	}
	return normalized, nil
}

func (a *Account) HiddenBillingMultiplier() float64 {
	if a != nil {
		if multiplier, ok := parseHiddenRateMultiplier(a.Extra[AccountExtraHiddenRateMultiplier]); ok {
			return multiplier
		}
	}
	return 1
}

// HiddenBillingMultiplier reads the immutable usage snapshot, never today's
// account configuration. Pre-extension rows have no snapshot and use one.
func (u *UsageLog) HiddenBillingMultiplier() float64 {
	if u == nil || u.HiddenRateMultiplier == nil {
		return 1
	}
	return *u.HiddenRateMultiplier
}

func (u *UsageLog) EffectiveBillingMultiplier() float64 {
	return hiddenBillingProduct(u.RateMultiplier, u.HiddenBillingMultiplier())
}

// CustomerBillingView includes the account price in the visible base prices.
// Only the public group/user factor is exposed; the final charge is unchanged.
// This is a copy: administrator exports and account statistics keep raw costs.
func (u *UsageLog) CustomerBillingView() *UsageLog {
	view := *u
	multiplier := u.HiddenBillingMultiplier()
	view.InputCost = hiddenBillingProduct(u.InputCost, multiplier)
	view.OutputCost = hiddenBillingProduct(u.OutputCost, multiplier)
	view.CacheCreationCost = hiddenBillingProduct(u.CacheCreationCost, multiplier)
	view.CacheReadCost = hiddenBillingProduct(u.CacheReadCost, multiplier)
	view.ImageInputCost = hiddenBillingProduct(u.ImageInputCost, multiplier)
	view.ImageOutputCost = hiddenBillingProduct(u.ImageOutputCost, multiplier)
	view.TotalCost = hiddenBillingProduct(u.TotalCost, multiplier)
	return &view
}

func hiddenBillingProduct(value, multiplier float64) float64 {
	if multiplier == 1 || value == 0 {
		return value
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		return multiplier
	}
	product, _ := decimal.NewFromFloat(value).Mul(decimal.NewFromFloat(multiplier)).Float64()
	return product
}

// applyHiddenBillingBridge runs once after upstream pricing and before every
// customer ledger/cache effect. Raw costs, tokens and account statistics remain
// unchanged. All customer scopes receive the same eight-decimal charge.
func applyHiddenBillingBridge(account *Account, cost *CostBreakdown, usage *UsageLog) error {
	if cost == nil || usage == nil || usage.HiddenRateMultiplier != nil {
		return nil
	}
	multiplier := account.HiddenBillingMultiplier()
	charge := hiddenBillingProduct(cost.ActualCost, multiplier)
	if multiplier != 1 {
		charge = QuantizeUsageBillingAmount(charge)
	}
	if math.IsNaN(charge) || math.IsInf(charge, 0) || charge < 0 || charge >= 1e10 {
		return infraerrors.BadRequest("HIDDEN_BILLING_AMOUNT_INVALID", "customer charge exceeds the supported billing range")
	}
	usage.HiddenRateMultiplier = &multiplier
	cost.ActualCost = charge
	usage.ActualCost = charge
	return nil
}

// QuoteUsageCostWithHiddenBilling keeps fixed-charge reservations on the same
// customer price as settlement, after the account has been selected.
func (s *GatewayService) QuoteUsageCostWithHiddenBilling(ctx context.Context, input *RecordUsageInput) (float64, error) {
	cost := &CostBreakdown{ActualCost: s.QuoteUsageCost(ctx, input)}
	if err := applyHiddenBillingBridge(input.Account, cost, &UsageLog{}); err != nil {
		return 0, err
	}
	return cost.ActualCost, nil
}

func applyBatchImageHiddenPricing(account *Account, snapshot *BatchImagePricingSnapshot, count int) error {
	multiplier := account.HiddenBillingMultiplier()
	for _, amount := range []*float64{&snapshot.BillableUnitPrice, &snapshot.HoldUnitPrice} {
		*amount = hiddenBillingProduct(*amount, multiplier)
		if math.IsNaN(*amount) || math.IsInf(*amount, 0) || *amount < 0 || *amount >= 1e10 {
			return infraerrors.BadRequest("HIDDEN_BILLING_AMOUNT_INVALID", "batch image price exceeds the supported billing range")
		}
		if multiplier != 1 {
			*amount, _ = decimal.NewFromFloat(*amount).Round(10).Float64()
		}
	}
	snapshot.HiddenRateMultiplier = &multiplier
	snapshot.EstimatedCost = snapshot.BillableUnitPrice * float64(count)
	snapshot.HoldAmount = snapshot.HoldUnitPrice * float64(count)
	if multiplier != 1 {
		snapshot.EstimatedCost = QuantizeUsageBillingAmount(snapshot.EstimatedCost)
		snapshot.HoldAmount = QuantizeUsageBillingAmount(snapshot.HoldAmount)
	}
	if snapshot.EstimatedCost >= 1e10 || snapshot.HoldAmount >= 1e10 {
		return infraerrors.BadRequest("HIDDEN_BILLING_AMOUNT_INVALID", "batch image charge exceeds the supported billing range")
	}
	return nil
}

func batchImageHiddenBillingCharge(job *BatchImageJob, charge float64) float64 {
	if job.HiddenRateMultiplier != nil && *job.HiddenRateMultiplier != 1 {
		return QuantizeUsageBillingAmount(charge)
	}
	return charge
}

// Batch captures already include the frozen factor; never multiply again when
// the worker writes usage, and never read account settings at settlement time.
func applyBatchImageHiddenBillingSnapshot(job *BatchImageJob, usage *UsageLog) {
	if job.HiddenRateMultiplier == nil {
		return
	}
	usage.HiddenRateMultiplier = job.HiddenRateMultiplier
	// Batch pricing already incorporates the upstream account rate in its base
	// price. Retain that rule, and retain the pre-extension account-statistics
	// basis even though the usage UI now separates base price and customer rate.
	usage.TotalCost = hiddenBillingProduct(job.BaseUnitPrice*float64(job.SuccessCount), job.AccountRateMultiplier)
	usage.ImageOutputCost = usage.TotalCost
	usage.RateMultiplier = hiddenBillingProduct(job.GroupRateMultiplier, job.BatchDiscountMultiplier)
	accountStatsCost := hiddenBillingProduct(usage.TotalCost, usage.RateMultiplier)
	usage.AccountStatsCost = &accountStatsCost
}

// Admission precedes account selection. Reserve conservatively using the
// largest configured factor in the same scheduler pool, without a DB query or
// a cache keyed by arbitrary model names. Existing reservation caps still apply.
func hiddenBillingInflightEstimate(ctx context.Context, snapshot *SchedulerSnapshotService, groupID *int64, platform string, forced bool, estimate float64, priced bool) (float64, bool) {
	if !priced || estimate <= 0 || snapshot == nil {
		return estimate, priced
	}
	if snapshot.cache == nil {
		return 0, false
	}
	bucket := snapshot.bucketFor(groupID, platform, snapshot.resolveMode(platform, forced))
	accounts, hit, err := snapshot.cache.GetSnapshot(ctx, bucket)
	if err != nil || !hit {
		return 0, false
	}
	multiplier := 1.0
	for i := range accounts {
		multiplier = math.Max(multiplier, accounts[i].HiddenBillingMultiplier())
	}
	estimate = hiddenBillingProduct(estimate, multiplier)
	if math.IsNaN(estimate) || math.IsInf(estimate, 0) || estimate < 0 {
		return 0, false
	}
	return estimate, true
}

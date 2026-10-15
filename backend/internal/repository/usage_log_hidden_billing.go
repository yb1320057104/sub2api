package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// PK-029: project immutable hidden prices only for customer statistics. Keep
// administrator and account-cost aggregations on the stored original costs.
// The alias is an internal SQL identifier, never a request parameter.
func usageTotalCostExpression(ctx context.Context, alias string) string {
	if alias != "" {
		alias += "."
	}
	expression := alias + "total_cost"
	if usagestats.IsCustomerBillingView(ctx) {
		expression += " * COALESCE(" + alias + "hidden_rate_multiplier, 1)"
	}
	return expression
}

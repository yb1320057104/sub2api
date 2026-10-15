package usagestats

import "context"

type customerBillingViewKey struct{}

// WithCustomerBillingView selects the customer price basis for read-only
// statistics. It does not grant access; callers must still enforce ownership.
func WithCustomerBillingView(ctx context.Context) context.Context {
	return context.WithValue(ctx, customerBillingViewKey{}, true)
}

func IsCustomerBillingView(ctx context.Context) bool {
	value, _ := ctx.Value(customerBillingViewKey{}).(bool)
	return value
}

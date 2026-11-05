package service

import "context"

// Capacity spillover is limited to accounts already assigned disjoint IPs.
// An unallocated standby never becomes eligible because a live owner is full.
func proxyPriorityHasAlternative(ctx context.Context, accountID int64) bool {
	gate, _ := ctx.Value(proxyPriorityGateKey{}).(*proxyPriorityGate)
	if gate == nil || len(gate.subsets[accountID]) == 0 {
		return false
	}
	for id, proxies := range gate.subsets {
		if id != accountID && len(proxies) > 0 {
			return true
		}
	}
	return false
}

func proxyPriorityHasPartitions(ctx context.Context) bool {
	gate, _ := ctx.Value(proxyPriorityGateKey{}).(*proxyPriorityGate)
	return gate != nil && len(gate.subsets) > 1
}

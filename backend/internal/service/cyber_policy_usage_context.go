package service

import "context"

// CyberPolicyAuditUsageRecorder is optional so audit decisions remain usable
// without the ranking service. The snapshot contains no prompt or credentials.
type CyberPolicyAuditUsageRecorder interface {
	RecordAuditUsage(context.Context, CyberPolicyAuditBlockInput, *UsageLog) error
}

type cyberAuditUsageContextKey struct{}
type cyberAuditUsageSnapshot struct {
	input    CyberPolicyAuditBlockInput
	recorder CyberPolicyAuditUsageRecorder
}

// CyberPolicyAuditUsageInput exports a copy for the server-owned deferred media
// billing snapshot. It must never be accepted from an API request payload.
func CyberPolicyAuditUsageInput(ctx context.Context) *CyberPolicyAuditBlockInput {
	if ctx == nil {
		return nil
	}
	snapshot, ok := ctx.Value(cyberAuditUsageContextKey{}).(cyberAuditUsageSnapshot)
	if !ok || snapshot.recorder == nil || snapshot.input.PolicyAction != "charge_allow" {
		return nil
	}
	input := snapshot.input
	if input.GroupID != nil {
		id := *input.GroupID
		input.GroupID = &id
	}
	if input.RiskScore != nil {
		score := *input.RiskScore
		input.RiskScore = &score
	}
	return &input
}

func WithCyberPolicyAuditUsage(ctx context.Context, input CyberPolicyAuditBlockInput, recorder CyberPolicyAuditUsageRecorder) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.GroupID != nil {
		id := *input.GroupID
		input.GroupID = &id
	}
	if input.RiskScore != nil {
		score := *input.RiskScore
		input.RiskScore = &score
	}
	return context.WithValue(ctx, cyberAuditUsageContextKey{}, cyberAuditUsageSnapshot{input: input, recorder: recorder})
}

func WithoutCyberPolicyAuditUsage(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, cyberAuditUsageContextKey{}, cyberAuditUsageSnapshot{})
}

func CopyCyberPolicyAuditUsage(parent, target context.Context) context.Context {
	if parent == nil {
		return target
	}
	if snapshot, ok := parent.Value(cyberAuditUsageContextKey{}).(cyberAuditUsageSnapshot); ok {
		return context.WithValue(target, cyberAuditUsageContextKey{}, snapshot)
	}
	return target
}

func cyberPolicyAuditUsageFromContext(ctx context.Context, usage *UsageLog) (cyberAuditUsageSnapshot, bool) {
	if ctx == nil || usage == nil {
		return cyberAuditUsageSnapshot{}, false
	}
	snapshot, ok := ctx.Value(cyberAuditUsageContextKey{}).(cyberAuditUsageSnapshot)
	return snapshot, ok && snapshot.recorder != nil && snapshot.input.PolicyAction == "charge_allow" &&
		snapshot.input.UserID == usage.UserID && sameCyberAuditGroup(snapshot.input.GroupID, usage.GroupID) &&
		usage.EffectiveRequestType() != RequestTypeCyberBlocked
}

package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type publicGroupAuditRulesReader interface {
	PublicGroupAuditRules(context.Context) (map[int64]service.CyberPolicyGroupRule, error)
}

// ProvideAPIKeyHandler wires policy reads without coupling DTO conversion or
// the authentication cache to the cyber policy service.
func ProvideAPIKeyHandler(keys *service.APIKeyService, policy *service.CyberPolicyService) *APIKeyHandler {
	h := NewAPIKeyHandler(keys)
	h.auditRules = policy
	return h
}

func (h *APIKeyHandler) publicAuditRules(ctx context.Context) (map[int64]service.CyberPolicyGroupRule, error) {
	if h.auditRules == nil {
		return nil, nil
	}
	return h.auditRules.PublicGroupAuditRules(ctx)
}

func decorateGroupAuditRule(group *dto.Group, rules map[int64]service.CyberPolicyGroupRule) {
	if group == nil {
		return
	}
	group.SecurityAuditMultiplier = "1.000000"
	group.SecurityAuditAutoDeductEnabled = false
	if rule, ok := rules[group.ID]; ok {
		group.SecurityAuditMultiplier = rule.Multiplier
		group.SecurityAuditAutoDeductEnabled = rule.AutoDeductEnabled
	}
}

func decorateKeyAuditRules(key *dto.APIKey, rules map[int64]service.CyberPolicyGroupRule) *dto.APIKey {
	if key == nil {
		return nil
	}
	decorateGroupAuditRule(key.Group, rules)
	for _, group := range key.Groups {
		decorateGroupAuditRule(group, rules)
	}
	return key
}

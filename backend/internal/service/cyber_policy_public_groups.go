package service

import (
	"context"
	"encoding/json"

	"github.com/lib/pq"
)

// PublicGroupAuditRules exposes only the per-group price policy. The user
// exemption list and automatic-disable settings are not selected or returned.
func (s *CyberPolicyService) PublicGroupAuditRules(ctx context.Context) (map[int64]CyberPolicyGroupRule, error) {
	var selected pq.Int64Array
	var encoded []byte
	if err := s.db.QueryRowContext(ctx, `SELECT group_ids,group_rules FROM cyber_policy_settings WHERE id=1`).Scan(&selected, &encoded); err != nil {
		return nil, err
	}
	var rules []CyberPolicyGroupRule
	if err := json.Unmarshal(encoded, &rules); err != nil {
		return nil, err
	}
	scope := make(map[int64]bool, len(selected))
	for _, id := range selected {
		scope[id] = true
	}
	out := make(map[int64]CyberPolicyGroupRule, len(rules))
	for _, rule := range rules {
		rule.AutoDeductEnabled = rule.AutoDeductEnabled && scope[rule.GroupID]
		out[rule.GroupID] = rule
	}
	return out, nil
}

package securityaudit

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/auditrisk"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func auditCenterDiagnostics(cfg *AuditCenterConfig) ([]string, []AuditCenterGroup) {
	c, p, r := cfg.Content, cfg.Prompt, cfg.Ranking
	differences := []string{}
	sameIDs := func(a, b []int64) bool { return reflect.DeepEqual(auditCenterIDs(a), auditCenterIDs(b)) }
	if !sameIDs(c.GroupIDs, p.GroupIDs) || !sameIDs(c.GroupIDs, r.GroupIDs) {
		differences = append(differences, "group_ids")
	}
	if !sameIDs(c.UserAllowlistIDs, p.UserAllowlistIDs) || !sameIDs(c.UserAllowlistIDs, r.UserAllowlistIDs) {
		differences = append(differences, "user_allowlist_ids")
	}
	cp, _ := json.Marshal(c.RiskPolicy)
	pp, _ := json.Marshal(p.RiskPolicy)
	if string(cp) != string(pp) {
		differences = append(differences, "risk_policy")
	}
	groups := []AuditCenterGroup{}
	contains := func(ids []int64, id int64) bool {
		for _, value := range ids {
			if value == id {
				return true
			}
		}
		return false
	}
	for _, id := range auditCenterIDs(c.GroupIDs, p.GroupIDs, r.GroupIDs) {
		g := AuditCenterGroup{GroupID: id, ContentState: "missing", PromptState: "missing", RankingState: "missing", ChargeState: "disabled", Reasons: []string{}}
		contentScope, promptScope, rankingScope := contains(c.GroupIDs, id), contains(p.GroupIDs, id), contains(r.GroupIDs, id)
		if !contentScope || !promptScope || !rankingScope {
			g.Reasons = append(g.Reasons, "scope_mismatch")
		}
		if !cfg.RiskControlEnabled {
			g.Reasons = append(g.Reasons, "risk_control_disabled")
		}
		contentCharge, promptCharge := false, false
		if !contentScope {
			g.Reasons = append(g.Reasons, "content_group_not_selected")
		} else {
			g.ContentState = "ready"
			if !c.Enabled || c.Mode == service.ContentModerationModeOff || !cfg.RiskControlEnabled {
				g.ContentState = "disabled"
				if !c.Enabled || c.Mode == service.ContentModerationModeOff {
					g.Reasons = append(g.Reasons, "content_disabled")
				}
			}
			if c.Mode == service.ContentModerationModeObserve {
				g.Reasons = append(g.Reasons, "content_observation_only")
			}
			if c.KeywordBlockingMode == service.ContentModerationKeywordModeKeywordAsyncAPI {
				g.Reasons = append(g.Reasons, "content_keyword_async_api_observation_only")
			}
			local := c.Mode == service.ContentModerationModePreBlock && c.KeywordBlockingMode != service.ContentModerationKeywordModeAPIOnly && len(c.BlockedKeywords) > 0
			remote := c.KeywordBlockingMode != service.ContentModerationKeywordModeKeywordOnly && c.APIKeyConfigured && c.SampleRate > 0
			if c.KeywordBlockingMode != service.ContentModerationKeywordModeKeywordOnly {
				if !c.APIKeyConfigured {
					g.Reasons = append(g.Reasons, "content_remote_api_key_missing")
				}
				if c.SampleRate == 0 {
					g.Reasons = append(g.Reasons, "content_remote_sampling_disabled")
				} else if c.SampleRate < 100 {
					g.Reasons = append(g.Reasons, "content_remote_sampling_partial")
				}
			}
			if !local && !remote && g.ContentState == "ready" {
				g.ContentState = "missing"
				g.Reasons = append(g.Reasons, "content_detection_source_missing")
			}
			if c.AutoBanEnabled {
				g.Reasons = append(g.Reasons, "content_legacy_auto_ban_enabled")
			}
			if c.ModelFilter.Type != "" && c.ModelFilter.Type != service.ContentModerationModelFilterAll {
				g.Reasons = append(g.Reasons, "content_model_filter_restricts_scope")
			}
			if len(c.UserAllowlistIDs) > 0 {
				g.Reasons = append(g.Reasons, "content_user_exemptions_configured")
			}
			g.Reasons = append(g.Reasons, auditCenterPolicyReasons("content", c.RiskPolicy)...)
			// A keyword-first asynchronous remote engine can enforce only local hits.
			localCharge := false
			for _, keyword := range c.BlockedKeywords {
				score, exists := c.KeywordScores[strings.ToLower(strings.TrimSpace(keyword))]
				if !exists {
					score = 100
				}
				_, action := c.RiskPolicy.Decide(float64(score))
				if action == auditrisk.ChargeAllow {
					localCharge = true
				}
			}
			if local && !localCharge && auditCenterCanCharge(c.RiskPolicy) {
				g.Reasons = append(g.Reasons, "content_keyword_scores_do_not_charge")
			}
			synchronousSource := (local && localCharge) || (remote && c.KeywordBlockingMode != service.ContentModerationKeywordModeKeywordAsyncAPI)
			contentCharge = cfg.RiskControlEnabled && c.Enabled && c.Mode == service.ContentModerationModePreBlock && synchronousSource && auditCenterCanCharge(c.RiskPolicy)
		}
		if !promptScope {
			g.Reasons = append(g.Reasons, "prompt_group_not_selected")
		} else {
			g.PromptState = "ready"
			if !p.Enabled || !cfg.RiskControlEnabled {
				g.PromptState = "disabled"
				if !p.Enabled {
					g.Reasons = append(g.Reasons, "prompt_disabled")
				}
			}
			if !p.BlockingEnabled {
				g.Reasons = append(g.Reasons, "prompt_async_observation_only")
			}
			local := false
			for _, rule := range p.LocalRules {
				if rule.Enabled {
					local = true
					break
				}
			}
			remote := false
			for _, endpoint := range p.Endpoints {
				if endpoint.Enabled && endpoint.TokenStatus != "invalid" {
					remote = true
				}
				if endpoint.Enabled && endpoint.TokenStatus == "invalid" {
					g.Reasons = append(g.Reasons, "prompt_endpoint_token_invalid")
				}
			}
			if p.Engine == PromptEngineLocalRules && !local {
				if g.PromptState == "ready" {
					g.PromptState = "missing"
				}
				g.Reasons = append(g.Reasons, "prompt_local_rules_missing")
			}
			if p.Engine == PromptEngineRemote && !remote {
				g.Reasons = append(g.Reasons, "prompt_remote_endpoint_missing")
				if g.PromptState == "ready" && (!p.FastMode || !local) {
					g.PromptState = "missing"
				}
			}
			if p.FastMode && p.Engine == PromptEngineRemote {
				g.Reasons = append(g.Reasons, "prompt_fast_remote_observation_only")
			}
			if len(p.UserAllowlistIDs) > 0 {
				g.Reasons = append(g.Reasons, "prompt_user_exemptions_configured")
			}
			g.Reasons = append(g.Reasons, auditCenterPolicyReasons("prompt", p.RiskPolicy)...)
			localCharge := false
			for _, rule := range p.LocalRules {
				_, action := p.RiskPolicy.Decide(localRuleScore(rule))
				if rule.Enabled && action == auditrisk.ChargeAllow {
					localCharge = true
				}
			}
			if local && !localCharge && auditCenterCanCharge(p.RiskPolicy) {
				g.Reasons = append(g.Reasons, "prompt_local_scores_do_not_charge")
			}
			synchronousSource := (p.Engine == PromptEngineLocalRules && localCharge) || (p.Engine == PromptEngineRemote && ((!p.FastMode && remote) || (p.FastMode && localCharge)))
			promptCharge = cfg.RiskControlEnabled && p.Enabled && p.BlockingEnabled && synchronousSource && auditCenterCanCharge(p.RiskPolicy)
		}
		if !rankingScope {
			g.ChargeState = "missing"
			g.Reasons = append(g.Reasons, "ranking_group_not_selected")
		} else {
			g.RankingState = "ready"
			if !r.AutoDisableEnabled {
				g.Reasons = append(g.Reasons, "ranking_auto_disable_off")
			}
			if len(r.UserAllowlistIDs) > 0 {
				g.Reasons = append(g.Reasons, "ranking_user_exemptions_configured")
			}
			enabled := false
			for _, rule := range r.GroupRules {
				if rule.GroupID == id {
					enabled = rule.AutoDeductEnabled
					break
				}
			}
			if !enabled {
				g.Reasons = append(g.Reasons, "ranking_auto_deduct_off")
			} else if !contentCharge && !promptCharge {
				g.Reasons = append(g.Reasons, "charge_no_synchronous_charge_allow_source")
			} else {
				g.ChargeState = "ready"
			}
		}
		// Readiness concerns audit charge_allow. Upstream type4 is a separate
		// existing charge source, and actual debits still require all runtime gates.
		g.Reasons = append(g.Reasons, "upstream_type4_uses_separate_existing_rules", "charge_requires_successful_positive_cost", "charge_requires_nonexempt_user_and_sufficient_balance", "preblock_has_zero_cost")
		groups = append(groups, g)
	}
	return differences, groups
}

func auditCenterCanCharge(policy *auditrisk.Policy) bool {
	return policy != nil && (policy.LowAction == auditrisk.ChargeAllow || policy.MediumAction == auditrisk.ChargeAllow || policy.HighAction == auditrisk.ChargeAllow)
}

func auditCenterPolicyReasons(source string, policy *auditrisk.Policy) []string {
	if policy == nil {
		return []string{source + "_risk_policy_disabled"}
	}
	return []string{source + "_low_" + policy.LowAction, source + "_medium_" + policy.MediumAction, source + "_high_" + policy.HighAction}
}

package securityaudit

import "github.com/Wei-Shaw/sub2api/internal/pkg/auditrisk"

func cloneLocalRules(rules []LocalRule) []LocalRule {
	cloned := append([]LocalRule{}, rules...)
	for index := range cloned {
		if cloned[index].Score != nil {
			value := *cloned[index].Score
			cloned[index].Score = &value
		}
	}
	return cloned
}

func localRuleScore(rule LocalRule) float64 {
	if rule.Score != nil {
		return float64(*rule.Score)
	}
	if rule.Action == "block" {
		return 100
	}
	return 50
}

// Apply a single policy after all chunks have completed: actions need not be
// monotonic, so an earlier block cannot substitute for the highest risk score.
func applyPromptRiskPolicy(result *NormalizedResult, cfg ActiveConfig, synchronous bool) {
	if result == nil || cfg.RiskPolicy == nil {
		return
	}
	if cfg.Engine != PromptEngineLocalRules {
		// Remote Qwen labels are discrete signals, not confidence probabilities.
		for _, score := range result.ScannerScores {
			if score*100 > result.RiskScore {
				result.RiskScore = score * 100
			}
		}
		if result.Safety == "Unsafe" {
			result.RiskScore = 100
		} else if result.Safety == "Controversial" && result.RiskScore < 50 {
			result.RiskScore = 50
		}
	}
	level, action := cfg.RiskPolicy.Decide(result.RiskScore)
	result.RiskLevel = RiskLevel(level)
	if level == "none" {
		result.RiskLevel = RiskLow
	}
	if !synchronous {
		action = auditrisk.Allow
	}
	result.PolicyAction = action
	result.Action = ActionAllow
	result.Decision = EventPass
	if level != "none" {
		result.Decision = EventFlag
	}
	switch action {
	case auditrisk.Block:
		result.Action = ActionBlock
		result.Decision = EventCritical
	case auditrisk.ChargeAllow:
		result.Action = ActionWarn
		result.Decision = EventFlag
	}
}

func promptRiskScoreForStorage(result *NormalizedResult) any {
	if result == nil || result.PolicyAction == "" {
		return nil
	}
	return result.RiskScore
}

func nullablePolicyAction(action string) any {
	if action == "" {
		return nil
	}
	return action
}

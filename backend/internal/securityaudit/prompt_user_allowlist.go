package securityaudit

import "strings"

// Retain the finding and original risk classification in the audit event while
// making the effective action log-only. This does not affect the other audit
// engine or the independent ranking exemptions.
func applyPromptUserExemption(result *NormalizedResult) {
	if result == nil || result.Action == ActionAllow {
		return
	}
	original := string(result.Action)
	if result.ScannerEvidence == nil {
		result.ScannerEvidence = map[string]string{}
	}
	if len(result.Categories) == 0 {
		result.ScannerEvidence["user_exempt"] = "用户豁免：原判定 " + original + "，仅记录并放行"
	}
	for _, category := range result.Categories {
		if !strings.HasPrefix(result.ScannerEvidence[category], "用户豁免：") {
			result.ScannerEvidence[category] = "用户豁免：原判定 " + original + "，仅记录并放行。" + result.ScannerEvidence[category]
		}
	}
	result.Action, result.Decision = ActionWarn, EventFlag
}

func (s *PromptService) isUserAllowlisted(userID int64) bool {
	if s == nil || s.config == nil || userID <= 0 {
		return false
	}
	if store, ok := s.config.(interface{ IsUserAllowlisted(int64) bool }); ok {
		return store.IsUserAllowlisted(userID)
	}
	cfg, ok := s.config.Active()
	return ok && cfg.IsUserAllowlisted(userID)
}

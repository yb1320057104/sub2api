package service

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/auditrisk"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func normalizeContentModerationKeywordScores(values map[string]int) (map[string]int, error) {
	result := make(map[string]int, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, original := range keys {
		key, score := strings.ToLower(strings.TrimSpace(original)), values[original]
		if key == "" || score < 0 || score > 100 {
			return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_KEYWORD_SCORE", "关键词不能为空，风险分数必须为 0～100 的整数")
		}
		if previous, exists := result[key]; exists && previous != score {
			return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_KEYWORD_SCORE", "同一规范化关键词不能配置不同风险分数")
		}
		result[key] = score
	}
	return result, nil
}

func cloneContentModerationKeywordScores(values map[string]int) map[string]int {
	result := make(map[string]int, len(values))
	for key, score := range values {
		result[key] = score
	}
	return result
}

func validateContentModerationRiskConfig(cfg *ContentModerationConfig) error {
	if err := cfg.RiskPolicy.Validate(); err != nil {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_RISK_POLICY", err.Error())
	}
	values, err := normalizeContentModerationKeywordScores(cfg.KeywordScores)
	if err != nil {
		return err
	}
	cfg.KeywordScores = values
	return nil
}

func (snapshot *contentModerationRuntimeSnapshot) matchRiskKeyword(text string) (string, int, bool) {
	if snapshot == nil || snapshot.config == nil {
		return "", 0, false
	}
	matcher := snapshot.keywordMatcher
	if matcher == nil {
		matcher = newContentModerationKeywordMatcher(snapshot.config.BlockedKeywords)
	}
	return matcher.MatchRisk(text, snapshot.config.KeywordScores)
}

// Scores are independent evidence. The legacy per-category threshold continues
// to apply only when no graded policy is configured. Never interpret NaN/Inf as
// a valid zero-to-one score, and bound finite upstream values to this domain.
func contentModerationMaximumRisk(scores map[string]float64) (string, float64) {
	category, highest := "", 0.0
	for name, score := range scores {
		if math.IsNaN(score) || math.IsInf(score, 0) || score <= 0 {
			continue
		}
		score = math.Min(score, 1)
		if score > highest || score == highest && (category == "" || name < category) {
			category, highest = name, score
		}
	}
	return category, highest * 100
}

func contentModerationRiskOutcome(input ContentModerationCheckInput, cfg *ContentModerationConfig, score float64, allowBlock bool) (string, string) {
	level, action := cfg.RiskPolicy.Decide(score)
	if !allowBlock || cfg.Mode != ContentModerationModePreBlock || input.riskControlLogOnly {
		action = auditrisk.Allow
	}
	return level, action
}

func contentModerationRiskDecision(cfg *ContentModerationConfig, score float64, level, policyAction, category string,
	scores map[string]float64, blockAction string) *ContentModerationDecision {
	blocked := policyAction == auditrisk.Block
	decision := &ContentModerationDecision{Allowed: !blocked, Blocked: blocked, Flagged: score > 0,
		RiskScore: score, RiskLevel: level, PolicyAction: policyAction, HighestCategory: category,
		HighestScore: score / 100, CategoryScores: scores, Action: ContentModerationActionAllow}
	if blocked {
		decision.Action, decision.Message, decision.StatusCode = blockAction, cfg.BlockMessage, cfg.BlockStatus
	}
	return decision
}

func setContentModerationRiskLog(log *ContentModerationLog, decision *ContentModerationDecision) {
	score := decision.RiskScore
	log.RiskScore, log.RiskLevel, log.PolicyAction = &score, decision.RiskLevel, decision.PolicyAction
}

func (s *ContentModerationService) keywordRiskDecision(input ContentModerationCheckInput, cfg *ContentModerationConfig,
	text, keyword string, score int) *ContentModerationDecision {
	level, policyAction := contentModerationRiskOutcome(input, cfg, float64(score), true)
	scores := map[string]float64{contentModerationKeywordCategory: float64(score) / 100}
	decision := contentModerationRiskDecision(cfg, float64(score), level, policyAction, contentModerationKeywordCategory,
		scores, ContentModerationActionKeywordBlock)
	s.recordPreBlockSyncMetric(0, decision.Action)
	log := s.buildLog(input, cfg, decision.Action, decision.Flagged, decision.HighestCategory, decision.HighestScore,
		scores, text, nil, nil, "")
	log.MatchedKeyword = keyword
	setContentModerationRiskLog(log, decision)
	// Even a zero-score match is evidence and terminates keyword-and-API mode.
	s.enqueueRecord(input, cfg, log, "", false, policyAction != auditrisk.Allow)
	return decision
}

func (s *ContentModerationService) apiRiskDecision(ctx context.Context, input ContentModerationCheckInput, cfg *ContentModerationConfig,
	content ContentModerationInput, result *moderationAPIResult, latency int, queueDelay *int, allowBlock, trackPreBlock bool) *ContentModerationDecision {
	activeScores := make(map[string]float64, len(result.CategoryScores))
	for category, value := range result.CategoryScores {
		if moderationCategoryEnabled(cfg.Engine, cfg.CategorySettings, category) {
			activeScores[category] = value
		}
	}
	category, score := contentModerationMaximumRisk(activeScores)
	level, policyAction := contentModerationRiskOutcome(input, cfg, score, allowBlock)
	decision := contentModerationRiskDecision(cfg, score, level, policyAction, category, result.CategoryScores, ContentModerationActionBlock)
	if trackPreBlock {
		s.recordPreBlockSyncMetric(latency, decision.Action)
	}
	if decision.Flagged || cfg.RecordNonHits {
		log := s.buildLog(input, cfg, decision.Action, decision.Flagged, category, decision.HighestScore,
			result.CategoryScores, content.ExcerptText(), &latency, queueDelay, "")
		log.EngineMeta = result.EngineMeta
		setContentModerationRiskLog(log, decision)
		penalized := policyAction != auditrisk.Allow
		if queueDelay == nil && cfg.Mode == ContentModerationModePreBlock {
			s.enqueueRecord(input, cfg, log, "", false, penalized)
		} else {
			s.persistContentModerationLog(ctx, cfg, log, "", false, penalized)
		}
	}
	return decision
}

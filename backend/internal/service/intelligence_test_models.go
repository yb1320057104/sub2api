package service

import (
	"context"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

type IntelligenceTestModelCatalog struct {
	Models         []string `json:"models"`
	ExcludedModels []string `json:"excluded_models"`
}

// ModelCandidates reads identifiers only. It never probes an
// account, refreshes an upstream catalog, saves configuration or schedules work.
// These are known choices, not a promise of availability in any particular group.
func (s *IntelligenceTestService) ModelCandidates(ctx context.Context) (*IntelligenceTestModelCatalog, error) {
	seen := make(map[string]struct{})
	nonTextAliases := make(map[string]struct{})
	aliasesByTarget := make(map[string][]string)
	add := func(model, target string) {
		model = strings.TrimSpace(model)
		target = strings.TrimSpace(target)
		if !validIntelligenceTestModel(model) || strings.ContainsAny(model, "*?") {
			return
		}
		if intelligenceTestNonTextModel(model) || intelligenceTestNonTextModel(target) {
			nonTextAliases[model] = struct{}{}
			return
		}
		seen[model] = struct{}{}
		aliasesByTarget[target] = append(aliasesByTarget[target], model)
	}
	for _, platform := range domain.Platforms() {
		for _, model := range defaultModelsListCandidateIDs(platform.ID) {
			add(model, model)
		}
	}
	for _, model := range geminicli.GoogleOneModels {
		add(model.ID, model.ID)
	}
	for _, model := range deepseekServableModels {
		add(model, model)
	}
	for _, mapping := range []map[string]string{domain.DefaultAntigravityModelMapping, domain.DefaultBedrockModelMapping, xai.DefaultModelMapping()} {
		for model, target := range mapping {
			add(model, target)
		}
	}
	for _, profile := range providerProfiles {
		add(profile.DefaultTestModel, profile.DefaultTestModel)
	}

	// Only extract model names/targets, never whole credentials or endpoint URLs.
	// JSON type guards preserve reads when legacy optional fields are malformed.
	rows, err := s.db.QueryContext(ctx, `
		WITH local_accounts AS (
			SELECT credentials->'model_mapping' AS mapping, extra->'grok_observed_models'->'models' AS observed
			FROM accounts WHERE deleted_at IS NULL
			AND COALESCE(extra->'synthetic_ui_test','false'::jsonb)<>'true'::jsonb
		), active_channels AS (
			SELECT id,model_mapping FROM channels WHERE status='active'
		)
		SELECT item.key,item.value #>> '{}',false FROM local_accounts a
		CROSS JOIN LATERAL jsonb_each(CASE WHEN jsonb_typeof(a.mapping)='object' THEN a.mapping ELSE '{}'::jsonb END) item
		WHERE jsonb_typeof(item.value)='string'
		UNION
		SELECT model #>> '{}',model #>> '{}',false FROM local_accounts a
		CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(a.observed)='array' THEN a.observed ELSE '[]'::jsonb END) model
		WHERE jsonb_typeof(model)='string'
		UNION
		SELECT item.key,item.value #>> '{}',false FROM active_channels c
		CROSS JOIN LATERAL jsonb_each(CASE WHEN jsonb_typeof(c.model_mapping)='object' THEN c.model_mapping ELSE '{}'::jsonb END) platform
		CROSS JOIN LATERAL jsonb_each(CASE WHEN jsonb_typeof(platform.value)='object' THEN platform.value ELSE '{}'::jsonb END) item
		WHERE jsonb_typeof(item.value)='string'
		UNION
		SELECT model #>> '{}',model #>> '{}',COALESCE(p.billing_mode,'token') IN ('image','video') FROM channel_model_pricing p
		JOIN active_channels c ON c.id=p.channel_id
		CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(p.models)='array' THEN p.models ELSE '[]'::jsonb END) model
		WHERE jsonb_typeof(model)='string'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var model, target string
		var mediaPricing bool
		if err := rows.Scan(&model, &target, &mediaPricing); err != nil {
			return nil, err
		}
		if mediaPricing {
			model = strings.TrimSpace(model)
			if validIntelligenceTestModel(model) && !strings.ContainsAny(model, "*?") {
				nonTextAliases[model] = struct{}{}
			}
			continue
		}
		add(model, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	config, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	for _, mode := range []IntelligenceTestModeConfig{config.SVG, config.Candy} {
		add(mode.Model, mode.Model)
		for _, override := range mode.GroupOverrides {
			add(override.Model, override.Model)
		}
	}
	// A channel alias may point to an account alias or a locally priced media
	// model. Propagate only known exclusions, without making upstream requests.
	queue := make([]string, 0, len(nonTextAliases))
	for model := range nonTextAliases {
		queue = append(queue, model)
	}
	for index := 0; index < len(queue); index++ {
		for _, alias := range aliasesByTarget[queue[index]] {
			if _, found := nonTextAliases[alias]; !found {
				nonTextAliases[alias] = struct{}{}
				queue = append(queue, alias)
			}
		}
	}
	models := make([]string, 0, len(seen))
	excludedModels := make([]string, 0, len(nonTextAliases))
	for model := range seen {
		if _, media := nonTextAliases[model]; !media {
			models = append(models, model)
		}
	}
	for model := range nonTextAliases {
		excludedModels = append(excludedModels, model)
	}
	sort.Strings(models)
	sort.Strings(excludedModels)
	return &IntelligenceTestModelCatalog{Models: models, ExcludedModels: excludedModels}, nil
}

// Filtering is advisory and only affects suggestions. Custom/saved IDs remain
// valid configuration; catalog knowledge must never rewrite execution settings.
func intelligenceTestNonTextModel(model string) bool {
	model = strings.ToLower(codexProviderQualifiedModelID(model))
	if isCodexDedicatedMediaModel(model) || isGrokVideoGenerationModel(model) {
		return true
	}
	for _, prefix := range []string{"dall-e", "imagen", "veo", "sora", "seedream", "seedance", "flux", "whisper", "cogview", "cogvideo"} {
		if model == prefix || strings.HasPrefix(model, prefix+"-") || strings.HasPrefix(model, prefix+".") {
			return true
		}
	}
	for _, part := range strings.FieldsFunc(model, func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
		switch part {
		case "image", "video", "audio", "realtime", "voice", "speech", "tts", "stt", "embedding", "embeddings", "rerank", "moderation":
			return true
		}
	}
	return false
}

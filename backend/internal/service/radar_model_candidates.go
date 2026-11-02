package service

import (
	"context"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// RadarModelCandidates lists local choices without probing accounts or exposing
// credentials. Custom text model IDs remain valid even when absent here.
func (s *AccountCandyTestService) RadarModelCandidates(ctx context.Context) ([]string, error) {
	seen := make(map[string]struct{})
	add := func(model, target string) {
		model = strings.TrimSpace(model)
		if !validIntelligenceTestModel(model) || strings.Contains(model, "*") ||
			isOpenAIImageModel(model) || isOpenAIImageModel(strings.TrimSpace(target)) {
			return
		}
		seen[model] = struct{}{}
	}
	for _, model := range openai.DefaultModelIDs() {
		add(model, model)
	}
	// Only select public model identifiers, never full account credentials.
	rows, err := s.db.QueryContext(ctx, `
		SELECT mapping.key, mapping.value #>> '{}'
		FROM accounts a
		CROSS JOIN LATERAL jsonb_each(CASE
			WHEN jsonb_typeof(a.credentials->'model_mapping')='object'
			THEN a.credentials->'model_mapping' ELSE '{}'::jsonb END) mapping
		WHERE a.deleted_at IS NULL AND a.platform='openai'
		AND jsonb_typeof(mapping.value)='string'
		AND COALESCE(a.extra->'synthetic_ui_test','false'::jsonb)<>'true'::jsonb
		UNION SELECT model, model FROM pk_radar_account_models`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var model, target string
		if err := rows.Scan(&model, &target); err != nil {
			return nil, err
		}
		add(model, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

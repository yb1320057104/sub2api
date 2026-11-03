//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

type intelligenceModelsSettingsStub struct {
	SettingRepository
	value string
	err   error
}

func (s *intelligenceModelsSettingsStub) GetValue(context.Context, string) (string, error) {
	return s.value, s.err
}

func TestIntelligenceModelsLocalCatalogAndSavedChoices(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	cfg := defaultIntelligenceTestConfig()
	cfg.SVG.Model = "my/saved-svg"
	cfg.Candy.Model = "custom-candy"
	cfg.SVG.GroupOverrides = []IntelligenceTestGroupOverride{{GroupID: 7, Model: "Custom-Group-ID", Effort: "persistent"}}
	cfg.Candy.GroupOverrides = []IntelligenceTestGroupOverride{{GroupID: 9, Model: "saved-candy-override", Effort: "ultra"}}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	// A mock database and a settings stub with no write methods make accidental
	// database/configuration mutation fail this read-only call.
	mock.ExpectQuery("WITH local_accounts AS .*deleted_at IS NULL.*synthetic_ui_test.*active_channels AS .*status='active'.*channel_model_pricing").
		WillReturnRows(sqlmock.NewRows([]string{"model", "target", "media"}).
			AddRow("account-alias", "gpt-6-astra", false).
			AddRow("channel-alias", "claude-sonnet-4-6", false).
			AddRow("observed-grok-model", "observed-grok-model", false).
			AddRow("priced-custom-text", "priced-custom-text", false).
			AddRow(" account-alias ", "gpt-6-astra", false).
			AddRow("paint-alias", "openai/gpt-image-2.5-flare", false).
			AddRow("paint-alias", "paint-alias", false).
			AddRow("channel-paint-alias", "paint-alias", false).
			AddRow("gpt-6.1-terra", "paint-alias", false).
			AddRow("priced-media-alias", "unknown-media-target", false).
			AddRow("priced-media-alias", "priced-media-alias", true).
			AddRow("video-alias", "xai/grok-imagine-video-1.5", false).
			AddRow("gpt-*", "gpt-6-astra", false).
			AddRow("bad\nmodel", "gpt-6-astra", false))
	s := &IntelligenceTestService{db: db, settingRepo: &intelligenceModelsSettingsStub{value: string(raw)}}
	catalog, err := s.ModelCandidates(context.Background())
	require.NoError(t, err)
	models := catalog.Models
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol", "claude-sonnet-4-6", "gemini-4-argon", "gemini-2.5-pro", "grok-4.7", "kimi-k3", "glm-5.3", "deepseek-flash", "my/saved-svg", "custom-candy", "Custom-Group-ID", "saved-candy-override", "account-alias", "channel-alias", "observed-grok-model", "priced-custom-text"} {
		require.Contains(t, models, model)
	}
	for _, model := range []string{"paint-alias", "channel-paint-alias", "priced-media-alias", "video-alias", "gpt-*", "bad\nmodel", "gpt-image-2.5-flare", "gemini-3.1-flash-image", "grok-imagine-video-1.5"} {
		require.NotContains(t, models, model)
	}
	for _, model := range []string{"paint-alias", "channel-paint-alias", "gpt-6.1-terra", "priced-media-alias", "video-alias"} {
		require.NotContains(t, models, model)
		require.Contains(t, catalog.ExcludedModels, model)
	}
	require.NotContains(t, catalog.ExcludedModels, "gpt-*")
	require.NotContains(t, catalog.ExcludedModels, "bad\nmodel")
	require.True(t, sort.StringsAreSorted(catalog.ExcludedModels))
	require.True(t, sort.StringsAreSorted(models))
	require.Equal(t, 1, strings.Count(strings.Join(models, "\n"), "account-alias"))
	require.False(t, cfg.Enabled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligenceModelsReadFailures(t *testing.T) {
	for _, failure := range []string{"query", "scan", "rows", "settings", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			settings := &intelligenceModelsSettingsStub{err: ErrSettingNotFound}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "cancelled" {
				cancel()
			} else {
				query := mock.ExpectQuery("WITH local_accounts")
				switch failure {
				case "query":
					query.WillReturnError(errors.New("private database detail"))
				case "scan":
					query.WillReturnRows(sqlmock.NewRows([]string{"model", "target", "media"}).AddRow(nil, "target", false))
				case "rows":
					query.WillReturnRows(sqlmock.NewRows([]string{"model", "target", "media"}).AddRow("a", "a", false).RowError(0, errors.New("row failure")))
				case "settings":
					query.WillReturnRows(sqlmock.NewRows([]string{"model", "target", "media"}))
					settings.err = errors.New("settings unavailable")
				}
			}
			s := &IntelligenceTestService{db: db, settingRepo: settings}
			models, err := s.ModelCandidates(ctx)
			require.Error(t, err)
			require.Nil(t, models, "an incomplete catalog is not reported as success")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestIntelligenceModelsNonTextAndExecutionValues(t *testing.T) {
	for _, model := range []string{"openai/gpt-image-2", "google/gemini-3-pro-image-preview", "xai/grok-imagine-image", "grok-voice-latest", "sora-2", "veo-3.1", "imagen-4", "tts-1", "whisper-1", "text-embedding-3-large", "omni-moderation-latest", "flux-1", "seedance-2", "cogview-3", "cogvideo", "gpt-4o-realtime-preview"} {
		require.True(t, intelligenceTestNonTextModel(model), model)
	}
	for _, model := range []string{"gpt-6.1-sol", "custom/vision-language-model", "claude-opus-4-8", "deepseek-v4-flash-vision-exp", "fluxion-text-model"} {
		require.False(t, intelligenceTestNonTextModel(model), model)
	}
	for _, effort := range []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent"} {
		cfg := defaultIntelligenceTestConfig()
		cfg.SVG.Model = "custom-unlisted-model"
		cfg.SVG.Effort = effort
		cfg.Candy.GroupOverrides = []IntelligenceTestGroupOverride{{GroupID: 7, Model: "custom-group-model", Effort: effort}}
		require.NoError(t, validateIntelligenceTestConfig(cfg))
		require.Equal(t, effort, cfg.SVG.Effort)
		require.Equal(t, effort, cfg.Candy.GroupOverrides[0].Effort)
		require.Equal(t, "custom-unlisted-model", cfg.SVG.Model)
	}
}

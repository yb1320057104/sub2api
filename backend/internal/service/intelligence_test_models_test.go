//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

type intelligenceModelsSettingsStub struct {
	SettingRepository
	value string
	err   error
}

func intelligenceQualityTestConfig(t *testing.T, enabled, candy bool) string {
	t.Helper()
	cfg := defaultIntelligenceTestConfig()
	cfg.Enabled, cfg.Candy.Enabled = enabled, candy
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	return string(raw)
}

func TestIntelligenceQualityDisplayGatesAndSelectedGroups(t *testing.T) {
	for _, tc := range []struct {
		name           string
		enabled, candy bool
	}{
		{"site disabled", false, true}, {"candy disabled", true, false}, {"selected groups only", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			raw := intelligenceQualityTestConfig(t, tc.enabled, tc.candy)
			mock.ExpectQuery("SELECT value FROM settings WHERE key=\\$1").WithArgs(intelligenceTestSettingKey).
				WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(raw))
			if tc.enabled && tc.candy {
				mock.ExpectQuery("SELECT g.id,g.name,g.platform FROM pk_intelligence_test_keys k JOIN groups g ON g.id=k.group_id AND g.status='active' AND g.deleted_at IS NULL JOIN settings configuration ON configuration.key='pk_intelligence_test_config' AND configuration.value=\\$1 ORDER BY g.id LIMIT \\$2").
					WithArgs(raw, 2000).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "platform"}).AddRow(7, "Visible group", "openai"))
			}
			view, err := (&IntelligenceTestService{db: db}).ListQualityGroups(context.Background())
			require.NoError(t, err)
			require.Equal(t, tc.enabled && tc.candy, view.Enabled)
			if view.Enabled {
				require.Equal(t, []IntelligenceTestQualityGroup{{ID: 7, Name: "Visible group", Platform: "openai"}}, view.Groups)
			} else {
				require.Empty(t, view.Groups)
			}
			body, err := json.Marshal(view)
			require.NoError(t, err)
			require.NotContains(t, string(body), "key_configured")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestIntelligenceQualityHistorySafeProjectionAndStates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	raw := intelligenceQualityTestConfig(t, true, true)
	mock.ExpectQuery("SELECT value FROM settings WHERE key=\\$1").WithArgs(intelligenceTestSettingKey).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(raw))
	now := time.Now().UTC().Truncate(time.Second)
	rows := sqlmock.NewRows([]string{"id", "model", "effort", "status", "passed", "started", "finished", "duration", "empty"}).
		AddRow(6, "model-a", "high", "running", nil, now, nil, nil, false).
		AddRow(5, "model-a", "high", "failed", nil, now, now, 10, false).
		AddRow(4, "model-b", "", "completed", nil, now, now, 10, true).
		AddRow(3, nil, nil, "completed", nil, now, now, 10, false).
		AddRow(2, "model-b", "", "completed", false, now, now, 10, false).
		AddRow(1, "model-a", "high", "completed", true, now, now, 10, false)
	mock.ExpectQuery("SELECT r.id,r.request_model,r.request_effort,r.status,r.passed,.*FROM pk_intelligence_test_keys k JOIN groups g ON g.id=k.group_id AND g.status='active' AND g.deleted_at IS NULL JOIN settings configuration ON configuration.key='pk_intelligence_test_config' AND configuration.value=\\$2 LEFT JOIN LATERAL.*WHERE group_id=k.group_id AND mode='candy' ORDER BY id DESC LIMIT \\$3.*WHERE k.group_id=\\$1").
		WithArgs(int64(7), raw, 30).WillReturnRows(rows)
	view, err := (&IntelligenceTestService{db: db}).QualityHistory(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, view.Enabled)
	require.Equal(t, 5, view.IntervalMinutes)
	require.Equal(t, 600, view.StaleAfterSeconds)
	states := make([]string, 0, len(view.Results))
	for _, result := range view.Results {
		states = append(states, result.State)
	}
	require.Equal(t, []string{"running", "error", "empty_pool", "unknown", "incorrect", "correct"}, states)
	body, err := json.Marshal(view)
	require.NoError(t, err)
	for _, field := range []string{"account_id", "key_configured", "error_code", "error_detail", "answer", "prompt", "cost", "ttft", "credentials", "egress_ip"} {
		require.NotContains(t, string(body), `"`+field+`"`)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligenceQualityHistoryRejectsOutOfScopeAndKeepsEmpty(t *testing.T) {
	for _, mode := range []string{"unselected or inactive or changed config", "empty history", "disabled", "invalid group", "query failed"} {
		t.Run(mode, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			raw := intelligenceQualityTestConfig(t, mode != "disabled", true)
			groupID := int64(7)
			if mode == "invalid group" {
				groupID = 0
			} else {
				mock.ExpectQuery("SELECT value FROM settings WHERE key=\\$1").WithArgs(intelligenceTestSettingKey).
					WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(raw))
			}
			if mode != "invalid group" && mode != "disabled" {
				query := mock.ExpectQuery("SELECT r.id,r.request_model").WithArgs(groupID, raw, 30)
				rows := sqlmock.NewRows([]string{"id", "model", "effort", "status", "passed", "started", "finished", "duration", "empty"})
				if mode == "empty history" {
					rows.AddRow(nil, nil, nil, nil, nil, nil, nil, nil, false)
				}
				if mode == "query failed" {
					query.WillReturnError(errors.New("private database detail"))
				} else {
					query.WillReturnRows(rows)
				}
			}
			view, err := (&IntelligenceTestService{db: db}).QualityHistory(context.Background(), groupID)
			if mode == "empty history" || mode == "disabled" {
				require.NoError(t, err)
				require.Empty(t, view.Results)
				require.Equal(t, mode == "empty history", view.Enabled)
			} else {
				require.Error(t, err)
				require.Nil(t, view)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
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

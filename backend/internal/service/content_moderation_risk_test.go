package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/auditrisk"
	"github.com/stretchr/testify/require"
)

func moderationRiskTestPolicy() *auditrisk.Policy {
	return &auditrisk.Policy{LowThreshold: 20, MediumThreshold: 40, HighThreshold: 60,
		LowAction: auditrisk.Allow, MediumAction: auditrisk.ChargeAllow, HighAction: auditrisk.Block}
}

func TestContentModerationRiskConfigRoundTrip(t *testing.T) {
	settings := &contentModerationTestSettingRepo{}
	svc := &ContentModerationService{settingRepo: settings}
	raw, err := json.Marshal(moderationRiskTestPolicy())
	require.NoError(t, err)
	scores := map[string]int{"  FLOW  ": 0, "other": 57}
	view, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{RiskPolicy: raw, KeywordScores: &scores})
	require.NoError(t, err)
	require.Equal(t, moderationRiskTestPolicy(), view.RiskPolicy)
	require.Equal(t, map[string]int{"flow": 0, "other": 57}, view.KeywordScores)
	view.RiskPolicy.HighAction = auditrisk.Allow
	view.KeywordScores["flow"] = 90
	unchanged, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{})
	require.NoError(t, err)
	require.Equal(t, moderationRiskTestPolicy(), unchanged.RiskPolicy)
	require.Zero(t, unchanged.KeywordScores["flow"])
	cleared, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{RiskPolicy: json.RawMessage("null")})
	require.NoError(t, err)
	require.Nil(t, cleared.RiskPolicy)
	require.Zero(t, cleared.KeywordScores["flow"])
	for _, raw := range []string{`{}`, `{"low_threshold":20}`, `"bad"`, `{"low_threshold":20,"medium_threshold":40,"high_threshold":60,"low_action":"allow","medium_action":"allow","high_action":"charge"}`} {
		_, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{RiskPolicy: json.RawMessage(raw)})
		require.Error(t, err)
	}
	_, err = parseContentModerationConfig(`{"risk_policy":{"medium_threshold":40,"high_threshold":60,"low_action":"allow","medium_action":"allow","high_action":"block"}}`)
	require.Error(t, err, "stored policies must be complete, not implicitly enable a missing zero threshold")
	for _, invalid := range []map[string]int{{"": 10}, {"x": -1}, {"x": 101}, {" A ": 10, "a": 20}} {
		_, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{KeywordScores: &invalid})
		require.Error(t, err)
	}
}

type moderationRiskTestTransport struct {
	calls  int
	scores map[string]float64
}

func (transport *moderationRiskTestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	body, err := json.Marshal(map[string]any{"results": []map[string]any{{"category_scores": transport.scores}}})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

type moderationRiskTestRepo struct {
	contentModerationTestRepo
	countCalls int
}

func (repo *moderationRiskTestRepo) CountFlaggedByUserSince(context.Context, int64, time.Time, bool) (int, error) {
	repo.countCalls++
	return 99, nil
}

func moderationRiskTestService(t *testing.T, cfg *ContentModerationConfig) (*ContentModerationService, *moderationRiskTestTransport, *moderationRiskTestRepo, *contentModerationTestHashCache) {
	t.Helper()
	cfg.Enabled, cfg.GroupIDs, cfg.SampleRate = true, []int64{7}, 100
	cfg.APIKeys, cfg.RecordNonHits = []string{"local-fixture"}, true
	cfg.RiskPolicy = moderationRiskTestPolicy()
	cfg.AutoBanEnabled, cfg.BanThreshold, cfg.PreHashCheckEnabled = true, 1, true
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	transport := &moderationRiskTestTransport{scores: map[string]float64{"sexual": .75}}
	repo, hashes := &moderationRiskTestRepo{}, &contentModerationTestHashCache{hasResult: true}
	svc := &ContentModerationService{settingRepo: &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled: "true", SettingKeyContentModerationConfig: string(raw)}},
		repo: repo, hashCache: hashes, httpClient: &http.Client{Transport: transport}, asyncQueue: make(chan contentModerationTask, 16)}
	return svc, transport, repo, hashes
}

func moderationRiskTestInput(prompt string) ContentModerationCheckInput {
	body, _ := json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": prompt}}})
	return ContentModerationCheckInput{UserID: 8, GroupID: contentModerationTestGroupID(), Protocol: ContentModerationProtocolOpenAIChat, Body: body}
}

func TestContentModerationPreflightRiskActionsAndQuickMode(t *testing.T) {
	for _, strategy := range []string{ContentModerationKeywordModeKeywordOnly, ContentModerationKeywordModeKeywordAndAPI, ContentModerationKeywordModeKeywordAsyncAPI} {
		for _, action := range []string{auditrisk.Allow, auditrisk.ChargeAllow, auditrisk.Block} {
			t.Run(strategy+"/"+action, func(t *testing.T) {
				cfg := defaultContentModerationConfig()
				cfg.Mode, cfg.KeywordBlockingMode = ContentModerationModePreBlock, strategy
				cfg.BlockedKeywords, cfg.KeywordScores = []string{"fixture"}, map[string]int{"fixture": 70}
				svc, transport, _, _ := moderationRiskTestService(t, cfg)
				stored, err := svc.loadRuntimeSnapshot(context.Background())
				require.NoError(t, err)
				stored.config.RiskPolicy.HighAction = action
				d, err := svc.Check(context.Background(), moderationRiskTestInput("fixture"))
				require.NoError(t, err)
				require.Equal(t, action, d.PolicyAction)
				require.Equal(t, action == auditrisk.Block, d.Blocked)
				require.Equal(t, action != auditrisk.Block, d.Allowed)
				require.Zero(t, transport.calls, "local graded hit must never fall into an API/legacy boolean block")
			})
		}
	}
	cfg := defaultContentModerationConfig()
	cfg.Mode, cfg.KeywordBlockingMode = ContentModerationModePreBlock, ContentModerationKeywordModeKeywordAsyncAPI
	cfg.BlockedKeywords = []string{"fixture"}
	svc, transport, repo, hashes := moderationRiskTestService(t, cfg)
	d, err := svc.Check(context.Background(), moderationRiskTestInput("normal hello"))
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.Zero(t, transport.calls, "first token must not wait for remote review")
	require.Len(t, svc.asyncQueue, 1)
	task := <-svc.asyncQueue
	delay := 0
	snapshot, err := svc.loadRuntimeSnapshot(context.Background())
	require.NoError(t, err)
	d = svc.checkSync(context.Background(), task.input, snapshot.config, task.content, task.inputHash, &delay, false)
	require.True(t, d.Allowed)
	require.Equal(t, auditrisk.Allow, d.PolicyAction)
	require.Equal(t, 1, transport.calls)
	require.Zero(t, repo.countCalls)
	require.Empty(t, hashes.snapshotRecorded())
}

func TestContentModerationRiskKeywordsHighestScoreZeroAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, prompt, keyword, action string
		score                         int
		hit                           bool
	}{
		{"highest-not-first", "low and high", "high", auditrisk.Block, 80, true},
		{"suffix-output", "risk token", "token", auditrisk.Block, 90, true},
		{"medium-charge", "LOW", "low", auditrisk.ChargeAllow, 45, true},
		{"explicit-zero", "flow", "flow", auditrisk.Allow, 0, true},
		{"default-score", "default", "default", auditrisk.Block, 100, true},
		{"english-boundaries", "a gallery has a flowchart", "", auditrisk.Allow, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultContentModerationConfig()
			cfg.KeywordBlockingMode = ContentModerationKeywordModeKeywordOnly
			cfg.BlockedKeywords = []string{"low", "high", "risk token", "token", "flow", "default", "all"}
			cfg.KeywordScores = map[string]int{"low": 45, "high": 80, "risk token": 20, "token": 90, "flow": 0}
			svc, transport, repo, hashes := moderationRiskTestService(t, cfg)
			decision, err := svc.Check(context.Background(), moderationRiskTestInput(tc.prompt))
			require.NoError(t, err)
			require.Equal(t, tc.action, decision.PolicyAction)
			require.Equal(t, tc.score > 0, decision.Flagged)
			require.Equal(t, float64(tc.score), decision.RiskScore)
			require.Equal(t, tc.action == auditrisk.Block, decision.Blocked)
			require.Zero(t, transport.calls)
			require.Empty(t, hashes.snapshotChecked())
			if !tc.hit {
				require.Empty(t, svc.asyncQueue)
				return
			}
			require.Len(t, svc.asyncQueue, 1)
			task := <-svc.asyncQueue
			require.Equal(t, tc.keyword, task.log.MatchedKeyword)
			require.NotNil(t, task.log.RiskScore)
			require.Equal(t, float64(tc.score), *task.log.RiskScore)
			svc.persistContentModerationLog(context.Background(), task.config, task.log, task.inputHash, task.recordHash, task.applySideEffects)
			require.Zero(t, repo.countCalls, "graded penalties are counted by the unified event recorder")
			require.Empty(t, hashes.snapshotRecorded())
			require.Zero(t, task.log.ViolationCount)
			require.False(t, task.log.AutoBanned)
		})
	}
	legacy := newContentModerationKeywordMatcher([]string{"all"})
	_, hit := legacy.Match("gallery")
	require.True(t, hit, "legacy matching is unchanged")
}

func TestContentModerationRiskKeywordAndAPIZeroHitShortCircuits(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.KeywordBlockingMode = ContentModerationKeywordModeKeywordAndAPI
	cfg.BlockedKeywords, cfg.KeywordScores = []string{"safe"}, map[string]int{"safe": 0}
	svc, transport, _, hashes := moderationRiskTestService(t, cfg)
	decision, err := svc.Check(context.Background(), moderationRiskTestInput("safe"))
	require.NoError(t, err)
	require.Equal(t, auditrisk.Allow, decision.PolicyAction)
	require.Zero(t, transport.calls)
	decision, err = svc.Check(context.Background(), moderationRiskTestInput("unmatched"))
	require.NoError(t, err)
	require.Equal(t, auditrisk.Block, decision.PolicyAction)
	require.Equal(t, 75.0, decision.RiskScore)
	require.Equal(t, 1, transport.calls)
	require.Empty(t, hashes.snapshotChecked(), "boolean legacy hashes cannot bypass current graded evaluation")
}

func TestContentModerationRiskAPIMaximumAndObserve(t *testing.T) {
	for _, tc := range []struct {
		name, mode, action, level string
		score                     float64
		trusted                   bool
	}{
		{"low", ContentModerationModePreBlock, auditrisk.Allow, "low", .30, false},
		{"medium", ContentModerationModePreBlock, auditrisk.ChargeAllow, "medium", .50, false},
		{"high", ContentModerationModePreBlock, auditrisk.Block, "high", .80, false},
		{"zero", ContentModerationModePreBlock, auditrisk.Allow, "none", 0, false},
		{"observe", ContentModerationModeObserve, auditrisk.Allow, "high", .80, false},
		{"trusted", ContentModerationModePreBlock, auditrisk.Allow, "high", .80, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultContentModerationConfig()
			cfg.Mode = tc.mode
			svc, transport, repo, hashes := moderationRiskTestService(t, cfg)
			transport.scores = map[string]float64{"sexual": tc.score, "violence": tc.score / 2}
			input := moderationRiskTestInput("local API fixture")
			input.riskControlLogOnly = tc.trusted
			decision := svc.checkSync(context.Background(), input, cfg, ContentModerationInput{Text: "fixture"}, "legacy-hash", nil, true)
			require.Equal(t, tc.action, decision.PolicyAction)
			require.Equal(t, tc.level, decision.RiskLevel)
			require.InDelta(t, tc.score*100, decision.RiskScore, .000001)
			require.Equal(t, tc.action == auditrisk.Block, decision.Blocked)
			require.Equal(t, 1, transport.calls)
			for len(svc.asyncQueue) > 0 {
				task := <-svc.asyncQueue
				svc.persistContentModerationLog(context.Background(), task.config, task.log, task.inputHash, task.recordHash, task.applySideEffects)
			}
			logs := repo.snapshotLogs()
			require.Len(t, logs, 1)
			require.Equal(t, tc.action, logs[0].PolicyAction)
			require.Equal(t, tc.score > 0, logs[0].Flagged, "risk hit evidence remains available before the repository's rollback-safe storage mapping")
			require.NotNil(t, logs[0].RiskScore)
			require.Zero(t, repo.countCalls)
			require.False(t, logs[0].AutoBanned)
			require.Empty(t, hashes.snapshotRecorded())
		})
	}
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type keywordOnlyNoNetworkTransport struct{ calls int }

func (transport *keywordOnlyNoNetworkTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	return nil, errors.New("keyword-only audit must not send HTTP requests")
}

func TestContentModerationKeywordOnlySavesWithoutKeyAndBlocksSelectedGroups(t *testing.T) {
	ctx := context.Background()
	settings := &contentModerationTestSettingRepo{values: map[string]string{SettingKeyRiskControlEnabled: "true"}}
	transport := &keywordOnlyNoNetworkTransport{}
	svc := &ContentModerationService{settingRepo: settings, repo: &contentModerationTestRepo{},
		httpClient: &http.Client{Transport: transport}, asyncQueue: make(chan contentModerationTask, 8)}
	enabled, mode, strategy := true, ContentModerationModePreBlock, ContentModerationKeywordModeKeywordOnly
	groups, keywords := []int64{7}, []string{"  制作钓鱼登录页  ", "GENERATE PHISHING EMAILS", "generate phishing emails", "literal.*phrase"}
	view, err := svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{
		Enabled: &enabled, Mode: &mode, KeywordBlockingMode: &strategy, GroupIDs: &groups, BlockedKeywords: &keywords,
	})
	require.NoError(t, err)
	require.False(t, view.APIKeyConfigured)
	require.Zero(t, view.APIKeyCount)
	require.Equal(t, []string{"制作钓鱼登录页", "GENERATE PHISHING EMAILS", "literal.*phrase"}, view.BlockedKeywords)
	var stored ContentModerationConfig
	require.NoError(t, json.Unmarshal([]byte(settings.values[SettingKeyContentModerationConfig]), &stored))
	require.Empty(t, stored.apiKeys())
	require.Equal(t, strategy, stored.KeywordBlockingMode)

	selected, other := int64(7), int64(8)
	for _, tc := range []struct {
		name, prompt string
		group        *int64
		blocked      bool
	}{
		{"selected_match", "请制作钓鱼登录页", &selected, true},
		{"case_insensitive", "Please generate phishing emails", &selected, true},
		{"literal_pattern", "literal.*phrase", &selected, true},
		{"no_regex_expansion", "literal random phrase", &selected, false},
		{"benign_miss", "如何识别钓鱼登录页？", &selected, false},
		{"unselected_group", "请制作钓鱼登录页", &other, false},
		{"no_group", "请制作钓鱼登录页", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": tc.prompt}}})
			require.NoError(t, err)
			decision, err := svc.Check(ctx, ContentModerationCheckInput{GroupID: tc.group, Protocol: ContentModerationProtocolOpenAIChat, Body: body})
			require.NoError(t, err)
			require.Equal(t, tc.blocked, decision.Blocked)
			require.Equal(t, !tc.blocked, decision.Allowed)
			if tc.blocked {
				require.Equal(t, ContentModerationActionKeywordBlock, decision.Action)
				require.Equal(t, 403, decision.StatusCode)
			}
		})
	}
	require.Zero(t, transport.calls)
	require.Len(t, svc.asyncQueue, 3, "only completed keyword hit evidence is queued; misses never enqueue remote scans")
	for len(svc.asyncQueue) > 0 {
		task := <-svc.asyncQueue
		require.NotNil(t, task.log)
		require.Equal(t, ContentModerationActionKeywordBlock, task.log.Action)
	}

	empty := []int64{}
	_, err = svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{GroupIDs: &empty})
	require.NoError(t, err)
	decision, err := svc.Check(ctx, ContentModerationCheckInput{GroupID: &selected, Protocol: ContentModerationProtocolOpenAIChat,
		Body: []byte(`{"messages":[{"role":"user","content":"制作钓鱼登录页"}]}`)})
	require.NoError(t, err)
	require.True(t, decision.Allowed, "clearing the selected groups must take effect immediately")
	require.Empty(t, svc.asyncQueue)
	require.Zero(t, transport.calls)
}

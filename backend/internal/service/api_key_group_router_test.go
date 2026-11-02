package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyGroupAvailabilityUsesModelAndAccountEligibility(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive}
	account := Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, GroupIDs: []int64{7}, Concurrency: 1}
	req := APIKeyGroupRouteRequest{Path: "/v1/responses", Model: "gpt-5.1", Models: []string{"gpt-5.1"}}
	check := func(group *Group, account Account) (bool, error) {
		svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}}
		return NewAPIKeyGroupRouter(nil, svc).Available(context.Background(), group, req)
	}
	ok, err := check(group, account)
	require.NoError(t, err)
	require.True(t, ok)
	blocked := *group
	blocked.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"other-model"}}
	ok, err = check(&blocked, account)
	require.NoError(t, err)
	require.False(t, ok)
	paused := account
	paused.Schedulable = false
	ok, err = check(group, paused)
	require.NoError(t, err)
	require.False(t, ok)
	rateLimited := account
	until := time.Now().Add(time.Hour)
	rateLimited.RateLimitResetAt = &until
	ok, err = check(group, rateLimited)
	require.NoError(t, err)
	require.False(t, ok)
	wrongGroup := account
	wrongGroup.GroupIDs = []int64{8}
	ok, err = check(group, wrongGroup)
	require.NoError(t, err)
	require.False(t, ok)
	mappingMiss := account
	mappingMiss.Credentials = map[string]any{"model_mapping": map[string]any{"gpt-4o": "gpt-4o"}}
	ok, err = check(group, mappingMiss)
	require.NoError(t, err)
	require.False(t, ok)
	private := *group
	private.RequirePrivacySet = true
	ok, err = check(&private, account)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestAPIKeyGroupAvailabilityRejectsWrongEndpointPlatformBeforeSelecting(t *testing.T) {
	router := NewAPIKeyGroupRouter(nil, nil)
	for _, pair := range []struct{ path, platform string }{{"/v1/images/generations", PlatformAnthropic}, {"/v1/embeddings", PlatformGrok}, {"/v1beta/models/gemini:generateContent", PlatformOpenAI}, {"/v1/videos", PlatformAnthropic}} {
		ok, err := router.Available(context.Background(), &Group{ID: 1, Platform: pair.platform, Status: StatusActive}, APIKeyGroupRouteRequest{Path: pair.path})
		require.NoError(t, err)
		require.False(t, ok, pair.path)
	}
	_, err := router.Available(context.Background(), &Group{ID: 1, Platform: PlatformOpenAI}, APIKeyGroupRouteRequest{Path: "/v1/responses"})
	require.Error(t, err, "missing scheduler dependency must not silently permit a group")
}

func TestAPIKeyGroupAvailabilityWebSocketTransport(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive}
	account := Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{7}, Concurrency: 1}
	req := APIKeyGroupRouteRequest{Path: "/v1/responses", Model: "gpt-5.1", WebSocket: true}
	for _, tc := range []struct {
		name, mode                string
		modeRouter, enabled, want bool
	}{
		{name: "legacy HTTP only rejected"},
		{name: "legacy WS enabled", enabled: true, want: true},
		{name: "HTTP bridge accepted", modeRouter: true, mode: OpenAIWSIngressModeHTTPBridge, want: true},
		{name: "context pool accepted", modeRouter: true, mode: OpenAIWSIngressModeCtxPool, want: true},
		{name: "explicit off rejected", modeRouter: true, enabled: true, mode: OpenAIWSIngressModeOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newSchedulerTestOpenAIWSV2Config()
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = tc.modeRouter
			candidate := account
			candidate.Extra = map[string]any{"openai_apikey_responses_websockets_v2_enabled": tc.enabled}
			if tc.mode != "" {
				candidate.Extra["openai_apikey_responses_websockets_v2_mode"] = tc.mode
			}
			svc := &OpenAIGatewayService{cfg: cfg, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{candidate}}}
			ok, err := NewAPIKeyGroupRouter(nil, svc).Available(context.Background(), group, req)
			require.NoError(t, err)
			require.Equal(t, tc.want, ok)
		})
	}
}

func TestAPIKeyGroupAvailabilityMessagesDispatchPermissionAndMapping(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive}
	account := Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, GroupIDs: []int64{7}, Concurrency: 1,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}}}
	req := APIKeyGroupRouteRequest{Path: "/v1/messages", Model: "claude-opus-4-6"}
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}}
	router := NewAPIKeyGroupRouter(nil, svc)
	ok, err := router.Available(context.Background(), group, req)
	require.NoError(t, err)
	require.False(t, ok, "OpenAI messages dispatch must be enabled")
	group.AllowMessagesDispatch = true
	ok, err = router.Available(context.Background(), group, req)
	require.NoError(t, err)
	require.True(t, ok, "Claude dispatch uses the group's mapped OpenAI model")
	group.MessagesDispatchModelConfig.OpusMappedModel = "not-allowed-by-account"
	ok, err = router.Available(context.Background(), group, req)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestAPIKeyGroupAvailabilityOAuthOnly(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			group := &Group{ID: 7, Platform: platform, Status: StatusActive, RequireOAuthOnly: true}
			account := Account{ID: 11, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
				GroupIDs: []int64{7}, AccountGroups: []AccountGroup{{AccountID: 11, GroupID: 7}}, Concurrency: 1}
			for _, typ := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
				account.Type = typ
				repo := schedulerTestOpenAIAccountRepo{accounts: []Account{account}}
				router := NewAPIKeyGroupRouter(&GatewayService{accountRepo: repo}, &OpenAIGatewayService{accountRepo: repo})
				// Force the concrete Anthropic path to avoid unrelated mixed-pool behavior.
				ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, platform)
				ok, err := router.Available(ctx, group, APIKeyGroupRouteRequest{Path: "/v1/responses"})
				require.NoError(t, err)
				require.Equal(t, typ == AccountTypeOAuth, ok, typ)
			}
		})
	}
}

func TestAPIKeyGroupAvailabilityCompactionCapabilities(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive}
	account := Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{7}, Concurrency: 1}
	for _, tc := range []struct {
		name, path, body         string
		responses, compact, want bool
	}{
		{"ordinary text may use chat compatibility", "/v1/responses", `{"model":"gpt-5.1"}`, false, false, true},
		{"native v2 rejects chat only", "/v1/responses", `{"stream":true,"input":[{"type":"compaction_trigger"}]}`, false, false, false},
		{"native v2 ignores legacy compact switch", "/v1/responses", `{"stream":true,"input":[{"type":"compaction_trigger"}]}`, true, false, true},
		{"nonstream trigger requires legacy compact", "/v1/responses", `{"stream":false,"input":[{"type":"compaction_trigger"}]}`, true, false, false},
		{"nonstream trigger accepts legacy compact", "/v1/responses", `{"input":[{"type":"compaction_trigger"}]}`, true, true, true},
		{"explicit legacy compact rejects unsupported", "/v1/responses/compact", `{}`, true, false, false},
		{"compact subpath requires capability", "/v1/responses/compact/retry", `{}`, true, false, false},
		{"explicit legacy compact accepts supported", "/v1/responses/compact", `{}`, true, true, true},
		{"nonresponses trigger does not promote", "/v1/chat/completions", `{"input":[{"type":"compaction_trigger"}]}`, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := account
			compactMode := OpenAICompactModeForceOff
			if tc.compact {
				compactMode = OpenAICompactModeForceOn
			}
			candidate.Extra = map[string]any{"openai_responses_supported": tc.responses, "openai_compact_mode": compactMode}
			svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{candidate}}}
			ok, err := NewAPIKeyGroupRouter(nil, svc).Available(context.Background(), group,
				APIKeyGroupRouteRequest{Path: tc.path, Model: "gpt-5.1", Body: []byte(tc.body)})
			require.NoError(t, err)
			require.Equal(t, tc.want, ok)
		})
	}
}

func TestAPIKeyGroupAvailabilityNativeImageAndSeedanceCapabilities(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive}
	account := Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{7}, Concurrency: 1}
	for _, tc := range []struct {
		name, path, model, body, accountType string
		credentials                          map[string]any
		want                                 bool
	}{
		{"default image model", "/v1/images/generations", "", `{"prompt":"draw a cat"}`, AccountTypeAPIKey, nil, true},
		{"native image options", "/v1/images/generations", "gpt-image-2", `{"model":"gpt-image-2","prompt":"draw a cat","quality":"high","partial_images":1}`, AccountTypeAPIKey, nil, true},
		{"compatible image requires apikey", "/v1/images/generations", "gemini-2.5-flash-image", `{"model":"gemini-2.5-flash-image","prompt":"draw a cat"}`, AccountTypeOAuth, nil, false},
		{"compatible image apikey accepted", "/v1/images/generations", "gemini-2.5-flash-image", `{"model":"gemini-2.5-flash-image","prompt":"draw a cat"}`, AccountTypeAPIKey, nil, true},
		{"seedance requires capability", "/v1/contents/generations/tasks", "seedance-1.5-pro", `{}`, AccountTypeAPIKey, map[string]any{"base_url": "https://example.test"}, false},
		{"seedance capability and origin", "/v1/contents/generations/tasks", "seedance-1.5-pro", `{}`, AccountTypeAPIKey, map[string]any{"base_url": "https://example.test", openAIEndpointCapabilitiesCredentialKey: []string{"seedance"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := account
			candidate.Type, candidate.Credentials = tc.accountType, tc.credentials
			svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{candidate}}}
			ok, err := NewAPIKeyGroupRouter(nil, svc).Available(context.Background(), group,
				APIKeyGroupRouteRequest{Path: tc.path, Model: tc.model, ContentType: "application/json", Body: []byte(tc.body)})
			require.NoError(t, err)
			require.Equal(t, tc.want, ok)
		})
	}
}

func TestAPIKeyGroupAvailabilityTokenCountingSkipsProfitGate(t *testing.T) {
	group := profitControlTestGroup(7, 0.5, 0)
	group.AllowMessagesDispatch = true
	account := upstreamCostTestAccount(11, UpstreamBillingProbeStatusOK, 0.8, time.Now().Add(-time.Minute), 30*time.Minute)
	profitControlTestAccountWithRate(account, 0.8)
	account.Status, account.Schedulable, account.GroupIDs, account.Concurrency = StatusActive, true, []int64{7}, 1
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/v1/responses", false},
		{"/v1/responses/input_tokens", true},
		{"/v1/messages/count_tokens", true},
	} {
		ok, err := NewAPIKeyGroupRouter(nil, svc).Available(context.Background(), group,
			APIKeyGroupRouteRequest{Path: tc.path, Model: "gpt-5.1"})
		require.NoError(t, err)
		require.Equal(t, tc.want, ok, tc.path)
	}
}

type apiKeyGroupCacheOwnerRepo struct {
	GeminiCachedContentRepository
	record *GeminiCachedContent
	err    error
}

func (r apiKeyGroupCacheOwnerRepo) GetForOwner(_ context.Context, keyID int64, publicID string) (*GeminiCachedContent, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.record == nil || r.record.APIKeyID != keyID || r.record.PublicID != publicID {
		return nil, ErrGeminiCachedContentNotFound
	}
	return r.record, nil
}

type apiKeyGroupVideoOwnerCache struct {
	GatewayCache
	groupID     int64
	sessionHash string
	err         error
}

func (c apiKeyGroupVideoOwnerCache) GetSessionAccountID(_ context.Context, groupID int64, sessionHash string) (int64, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.groupID == groupID && c.sessionHash == sessionHash {
		return 99, nil
	}
	return 0, ErrStickySessionNotFound
}

func TestAPIKeyGroupAvailabilityBoundResourceOwnership(t *testing.T) {
	ctx := context.Background()
	ownerGroup := &Group{ID: 7, Platform: PlatformGemini, Status: StatusActive}
	otherGroup := &Group{ID: 8, Platform: PlatformGemini, Status: StatusActive}
	cacheID := strings.Repeat("a", 40)
	record := &GeminiCachedContent{APIKeyID: 4, GroupID: 7, PublicID: cacheID, ExpireTime: time.Now().Add(time.Hour)}
	cached := NewGeminiCachedContentService(apiKeyGroupCacheOwnerRepo{record: record}, nil, nil)
	router := NewAPIKeyGroupRouter(nil, nil, cached)
	req := APIKeyGroupRouteRequest{ResourceKind: "gemini_cache", ResourceID: cacheID, APIKeyID: 4}
	ok, err := router.Available(ctx, ownerGroup, req)
	require.NoError(t, err)
	require.True(t, ok, "existing cache does not require a generation pool")
	ok, err = router.Available(ctx, otherGroup, req)
	require.NoError(t, err)
	require.False(t, ok)
	req.APIKeyID = 5
	ok, err = router.Available(ctx, ownerGroup, req)
	require.NoError(t, err)
	require.False(t, ok, "another key cannot claim the resource")
	cached.repo = apiKeyGroupCacheOwnerRepo{err: errors.New("database unavailable")}
	_, err = router.Available(ctx, ownerGroup, req)
	require.Error(t, err, "infrastructure failures must stop candidate fallback")

	openai := &OpenAIGatewayService{}
	videoID := "seedance:task1"
	openai.cache = apiKeyGroupVideoOwnerCache{groupID: 7, sessionHash: openai.openAISessionCacheKey(GrokMediaVideoRequestSessionHash(videoID, 3, 4))}
	router = NewAPIKeyGroupRouter(nil, openai)
	req = APIKeyGroupRouteRequest{ResourceKind: "video", ResourceID: videoID, UserID: 3, APIKeyID: 4}
	ok, err = router.Available(ctx, ownerGroup, req)
	require.NoError(t, err)
	require.True(t, ok, "task ownership is resolved independently from current account availability")
	ok, err = router.Available(ctx, otherGroup, req)
	require.NoError(t, err)
	require.False(t, ok)
	req.UserID = 6
	ok, err = router.Available(ctx, ownerGroup, req)
	require.NoError(t, err)
	require.False(t, ok)
	openai.cache = apiKeyGroupVideoOwnerCache{err: errors.New("cache unavailable")}
	_, err = router.Available(ctx, ownerGroup, req)
	require.Error(t, err)
}

type apiKeyGroupMixedAccountRepo struct{ schedulerTestOpenAIAccountRepo }

func (r apiKeyGroupMixedAccountRepo) ListSchedulableByGroupIDAndPlatforms(context.Context, int64, []string) ([]Account, error) {
	return append([]Account(nil), r.accounts...), nil
}

func TestAPIKeyGroupAvailabilityIdentityAndCacheCreate(t *testing.T) {
	router := NewAPIKeyGroupRouter(nil, nil)
	for _, tc := range []struct {
		path, platform string
		want           bool
	}{
		{"/v1beta/cachedContents", PlatformGemini, true},
		{"/v1beta/cachedContents", PlatformAntigravity, false},
		{"/v1beta/cachedContents", PlatformComposite, false},
		{"/v1/custom-voices/voice", PlatformGrok, true},
		{"/v1/custom-voices/voice", PlatformComposite, false},
	} {
		ok, err := router.Available(context.Background(), &Group{ID: 7, Platform: tc.platform}, APIKeyGroupRouteRequest{Path: tc.path, IdentityOnly: true})
		require.NoError(t, err)
		require.Equal(t, tc.want, ok, tc.platform)
	}
	group := &Group{ID: 7, Platform: PlatformGemini, Status: StatusActive}
	for _, typ := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		account := Account{ID: 11, Platform: PlatformGemini, Type: typ, Status: StatusActive, Schedulable: true,
			GroupIDs: []int64{7}, AccountGroups: []AccountGroup{{AccountID: 11, GroupID: 7}}, Concurrency: 1,
			Credentials: map[string]any{"api_key": "test-value"}}
		repo := apiKeyGroupMixedAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{account}}}
		router = NewAPIKeyGroupRouter(&GatewayService{accountRepo: repo}, nil)
		ok, err := router.Available(context.Background(), group, APIKeyGroupRouteRequest{Path: "/v1beta/cachedContents", Method: "POST"})
		require.NoError(t, err)
		require.Equal(t, typ == AccountTypeAPIKey, ok, "cache create must use explicit-cache-compatible accounts")
	}
}

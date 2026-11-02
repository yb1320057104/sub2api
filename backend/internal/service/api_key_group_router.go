package service

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// APIKeyGroupRouter only examines local scheduler state. It never sends a
// provider request, consumes a generation slot, or replays a dispatched call.
type APIKeyGroupRouter struct {
	gateway *GatewayService
	openai  *OpenAIGatewayService
	cached  *GeminiCachedContentService
}

func NewAPIKeyGroupRouter(gateway *GatewayService, openai *OpenAIGatewayService, cached ...*GeminiCachedContentService) *APIKeyGroupRouter {
	router := &APIKeyGroupRouter{gateway: gateway, openai: openai}
	if len(cached) > 0 {
		router.cached = cached[0]
	}
	return router
}

type APIKeyGroupRouteRequest struct {
	Path, Method, Model string
	ContentType         string
	Body                []byte
	Models              []string
	WebSocket           bool
	ResourceKind        string
	ResourceID          string
	APIKeyID, UserID    int64
	IdentityOnly        bool
}

func (r *APIKeyGroupRouter) Available(ctx context.Context, group *Group, request APIKeyGroupRouteRequest) (bool, error) {
	if r == nil || group == nil {
		return false, errors.New("group routing unavailable")
	}
	if request.Model != "" && group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(request.Model) {
		return false, nil
	}
	for _, model := range request.Models {
		if group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(model) {
			return false, nil
		}
	}
	// Existing resources retain their original group/account ownership even if
	// that group's generation pool is currently unavailable. The handler still
	// validates the bound account before any upstream operation.
	switch request.ResourceKind {
	case "gemini_cache":
		if r.cached == nil {
			return false, errors.New("cached content group routing unavailable")
		}
		_, err := r.cached.Resolve(ctx, request.APIKeyID, &group.ID, request.ResourceID)
		if errors.Is(err, ErrGeminiCachedContentNotFound) {
			return false, nil
		}
		return err == nil, err
	case "video":
		if r.openai == nil {
			return false, errors.New("video group routing unavailable")
		}
		accountID, err := r.openai.ResolveGrokMediaVideoRequestAccount(ctx, &group.ID, request.ResourceID, request.UserID, request.APIKeyID)
		if errors.Is(err, ErrStickySessionNotFound) {
			return false, nil
		}
		return err == nil && accountID > 0, err
	case "":
	default:
		return false, errors.New("unknown resource group routing")
	}
	if request.IdentityOnly {
		if strings.Contains(request.Path, "/custom-voices") {
			return group.Platform == PlatformGrok, nil
		}
		if strings.Contains(request.Path, "/cachedContents") {
			return group.Platform == PlatformGemini, nil
		}
		return keyGroupRoutePlatformAllowed(group.Platform, request), nil
	}
	ctx = context.WithValue(ctx, ctxkey.Group, group)
	platform, model := group.Platform, request.Model
	if forced, ok := ctx.Value(ctxkey.ForcePlatform).(string); ok && forced != "" {
		platform = forced
	} else if platform == PlatformComposite && model != "" {
		if r.gateway == nil || r.gateway.compositeResolver == nil {
			return false, errors.New("composite routing unavailable")
		}
		decision, err := r.gateway.compositeResolver.Resolve(ctx, group.ID, model, keyGroupRouteEndpoint(request.Path))
		if err != nil {
			return false, err
		}
		if decision.OAuthAmbiguous() {
			return false, errors.New("ambiguous composite model route")
		}
		if !decision.Matched {
			return false, nil
		}
		platform, model = decision.TargetPlatform, decision.UpstreamModel
		ctx = WithCompositeRouteDecision(ctx, decision)
	}
	if !keyGroupRoutePlatformAllowed(platform, request) {
		return false, nil
	}
	if strings.Contains(request.Path, "/messages") && domain.UsesOpenAIGateway(platform) &&
		platform != PlatformGrok && !IsMultiProtocolAPIKeyProvider(platform) && !group.AllowMessagesDispatch {
		return false, nil
	}
	if (strings.HasSuffix(request.Path, "/live") || strings.Contains(request.Path, "/realtime/calls")) && !group.AllowLive {
		return false, nil
	}
	if domain.UsesOpenAIGateway(platform) {
		if r.openai == nil {
			return false, errors.New("OpenAI group routing unavailable")
		}
		return r.openai.keyGroupHasAccount(ctx, group, platform, model, request)
	}
	if r.gateway == nil {
		return false, errors.New("group routing unavailable")
	}
	return r.gateway.keyGroupHasAccount(ctx, group, platform, model, request)
}

func (s *GatewayService) keyGroupHasAccount(ctx context.Context, group *Group, platform, model string, request APIKeyGroupRouteRequest) (bool, error) {
	cacheCreate := strings.HasSuffix(strings.TrimRight(request.Path, "/"), "/cachedContents")
	if cacheCreate && (platform != PlatformGemini || group.Platform == PlatformComposite) {
		return false, nil
	}
	forced, _ := ctx.Value(ctxkey.ForcePlatform).(string)
	if forced == "" && group.ClaudeCodeOnly && !IsClaudeCodeClient(ctx) {
		resolved, _, err := s.resolveGatewayGroup(ctx, &group.ID)
		if errors.Is(err, ErrClaudeCodeOnly) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		group, platform = resolved, resolved.Platform
		ctx = context.WithValue(ctx, ctxkey.Group, group)
	}
	ctx = s.withGatewayProfitControlGate(ctx, &group.ID)
	if s.checkChannelPricingRestriction(ctx, &group.ID, model) {
		return false, nil
	}
	accounts, mixed, err := s.listSchedulableAccounts(ctx, &group.ID, platform, forced != "")
	if err != nil {
		return false, err
	}
	ctx = s.withWindowCostPrefetch(ctx, accounts)
	ctx = s.withRPMPrefetch(ctx, accounts)
	for i := range accounts {
		account := &accounts[i]
		if !s.isAccountInGroup(account, &group.ID) || !s.isAccountSchedulableForSelection(account) || !s.isGatewayAccountProfitEligible(ctx, account) {
			continue
		}
		if group.RequirePrivacySet && !account.IsPrivacySet() {
			continue
		}
		if !keyGroupRouteAccountTypeAllowed(group, account) {
			continue
		}
		if cacheCreate && !IsGeminiExplicitCacheAccount(account) {
			continue
		}
		if account.Platform != platform && !(mixed && account.Platform == PlatformAntigravity && account.IsMixedSchedulingEnabled()) {
			continue
		}
		if model != "" && !s.isModelSupportedByAccountWithContext(ctx, account, model) {
			continue
		}
		if s.needsUpstreamChannelRestrictionCheck(ctx, &group.ID) && s.isUpstreamModelRestrictedByChannel(ctx, group.ID, account, model) {
			continue
		}
		if !s.isAccountSchedulableForModelSelection(ctx, account, model) || !s.isAccountSchedulableForQuota(account) || !s.isAccountSchedulableForWindowCost(ctx, account, false) || !s.isAccountSchedulableForRPM(ctx, account, false) {
			continue
		}
		return true, nil
	}
	return false, nil
}

func (s *OpenAIGatewayService) keyGroupHasAccount(ctx context.Context, group *Group, platform, model string, request APIKeyGroupRouteRequest) (bool, error) {
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, &group.ID)
	capability := OpenAIEndpointCapabilityChatCompletions
	imageCapability := OpenAIImagesCapability("")
	var imageRequest *OpenAIImagesRequest
	switch {
	case strings.Contains(request.Path, "/images/"):
		if platform == PlatformGrok {
			capability = OpenAIEndpointCapabilityGrokMediaGeneration
		} else {
			capability = ""
			parseContext := &gin.Context{Request: (&http.Request{
				URL: &url.URL{Path: request.Path}, Header: http.Header{"Content-Type": []string{request.ContentType}},
			}).WithContext(ctx)}
			var err error
			imageRequest, err = s.ParseOpenAIImagesRequest(parseContext, request.Body)
			if err != nil {
				return false, err
			}
			model = imageRequest.Model
			imageCapability = imageRequest.RequiredCapability
		}
	case strings.Contains(request.Path, "/contents/generations/tasks"):
		capability = OpenAIEndpointCapabilitySeedance
	case strings.Contains(request.Path, "/videos"):
		capability = OpenAIEndpointCapabilityGrokMediaGeneration
	case strings.Contains(request.Path, "/embeddings"):
		capability = OpenAIEndpointCapabilityEmbeddings
	case strings.Contains(request.Path, "/chat/completions"), strings.Contains(request.Path, "/messages"):
		capability = OpenAIEndpointCapabilityChatCompletions
	case strings.Contains(request.Path, "/alpha/search"):
		capability = OpenAIEndpointCapabilityAlphaSearch
	case strings.HasSuffix(request.Path, "/live"), strings.Contains(request.Path, "/realtime/calls"):
		capability = OpenAIEndpointCapabilityLive
	}
	imageIntent := IsExplicitImageGenerationIntent(request.Path, request.Model, request.Body)
	legacyCompact, nativeCompaction := keyGroupRouteCompaction(request)
	if (imageIntent || legacyCompact || nativeCompaction) && imageCapability == "" && platform == PlatformOpenAI {
		capability = OpenAIEndpointCapabilityResponses
	}
	if imageIntent {
		ctx = WithOpenAIImageGenerationIntent(ctx)
	}
	if imageRequest != nil || strings.Contains(request.Path, "/responses") || strings.Contains(request.Path, "/messages") || strings.Contains(request.Path, "/chat/completions") {
		mapping, restricted := s.ResolveChannelMappingAndRestrict(ctx, &group.ID, model)
		if restricted {
			return false, nil
		}
		if mapping.Mapped && mapping.MappedModel != "" {
			model = mapping.MappedModel
		}
	}
	if imageRequest != nil {
		imageCapability = imageRequest.RequiredCapabilityForModel(model)
	}
	if strings.Contains(request.Path, "/messages") {
		// Messages dispatch selects using the group's Claude-family mapping;
		// composite routes to Grok and multi-protocol providers bypass it.
		if group.Platform != PlatformComposite || (platform != PlatformGrok && !IsMultiProtocolAPIKeyProvider(platform)) {
			if mapped := group.ResolveMessagesDispatchModel(request.Model); mapped != "" {
				model = mapped
			}
		}
	}
	if imageCapability != "" || capability == OpenAIEndpointCapabilityGrokMediaGeneration || capability == OpenAIEndpointCapabilitySeedance || capability == OpenAIEndpointCapabilityLive ||
		strings.HasSuffix(request.Path, "/count_tokens") || strings.HasSuffix(request.Path, "/input_tokens") {
		ctx = WithOpenAIProfitControlSuppressed(ctx)
	} else {
		ctx = s.withOpenAIProfitControlGate(ctx, &group.ID)
	}
	if s.checkChannelPricingRestriction(ctx, &group.ID, model) {
		return false, nil
	}
	accounts, err := s.listSchedulableAccounts(ctx, &group.ID, platform)
	if err != nil {
		return false, err
	}
	requestSpec := OpenAIAccountScheduleRequest{GroupID: &group.ID, Platform: platform, RequestedModel: model,
		RequiredCapability: capability, RequiredImageCapability: imageCapability,
		RequireCompact: legacyCompact, RequirePrivacySet: group.RequirePrivacySet,
		RequiredTransport: OpenAIUpstreamTransportAny}
	if request.WebSocket && keyGroupRouteBareResponsesPath(request.Path) {
		requestSpec.RequiredTransport = OpenAIUpstreamTransportResponsesWebsocketV2Ingress
	}
	scheduler := &defaultOpenAIAccountScheduler{service: s}
	for i := range accounts {
		account := s.recheckSelectedOpenAIAccountFromDB(ctx, &accounts[i], &group.ID, platform, model, requestSpec.RequireCompact, capability)
		if account == nil || !s.openAIAccountMatchesSchedulingGroup(account, &group.ID) || !keyGroupRouteAccountTypeAllowed(group, account) {
			continue
		}
		if ok, _ := scheduler.isAccountRequestCompatibleReason(ctx, account, requestSpec); ok && scheduler.isAccountTransportCompatible(account, requestSpec.RequiredTransport) {
			return true, nil
		}
	}
	return false, nil
}

func keyGroupRouteAccountTypeAllowed(group *Group, account *Account) bool {
	return account != nil && (!group.RequireOAuthOnly || !groupSupportsOAuthOnlyFilter(group.Platform) || account.Type != AccountTypeAPIKey)
}

func keyGroupRouteBareResponsesPath(path string) bool {
	switch strings.TrimRight(strings.TrimSpace(path), "/") {
	case "/v1/responses", "/openai/v1/responses", "/responses", "/backend-api/codex/responses":
		return true
	default:
		return false
	}
}

// Match the handler's compact normalization without modifying the request.
// Native v2 requires Responses capability but deliberately keeps normal model
// mapping; only legacy compact uses the compact capability/model mapping gate.
func keyGroupRouteCompaction(request APIKeyGroupRouteRequest) (legacy, native bool) {
	path := strings.TrimRight(strings.TrimSpace(request.Path), "/")
	for _, base := range []string{"/v1/responses", "/openai/v1/responses", "/responses", "/backend-api/codex/responses"} {
		if path == base+"/compact" || strings.HasPrefix(path, base+"/compact/") {
			return true, false
		}
	}
	if !keyGroupRouteBareResponsesPath(path) || !HasCompactionTriggerInInput(request.Body) {
		return false, false
	}
	if request.WebSocket || gjson.GetBytes(request.Body, "stream").Type == gjson.True {
		return false, true
	}
	return true, false
}

func keyGroupRoutePlatformAllowed(platform string, r APIKeyGroupRouteRequest) bool {
	switch {
	case strings.Contains(r.Path, "/v1beta/"):
		return platform == PlatformGemini || platform == PlatformAntigravity || platform == PlatformComposite
	case strings.Contains(r.Path, "/images/"):
		return platform == PlatformOpenAI || platform == PlatformGrok || platform == PlatformComposite
	case strings.Contains(r.Path, "/videos"), strings.HasSuffix(r.Path, "/tts"), strings.HasSuffix(r.Path, "/stt"), strings.Contains(r.Path, "/custom-voices"), strings.HasSuffix(r.Path, "/realtime"), strings.HasSuffix(r.Path, "/web_search"), strings.HasSuffix(r.Path, "/x_search"):
		return platform == PlatformGrok || platform == PlatformComposite
	case strings.Contains(r.Path, "/embeddings"):
		return platform == PlatformOpenAI || platform == PlatformComposite
	case strings.Contains(r.Path, "/contents/generations/tasks"), strings.Contains(r.Path, "/alpha/search"), strings.HasSuffix(r.Path, "/live"), strings.Contains(r.Path, "/realtime/calls"):
		return platform == PlatformOpenAI || platform == PlatformComposite
	case r.WebSocket:
		return domain.UsesOpenAIGateway(platform) || platform == PlatformComposite
	default:
		return true
	}
}

func keyGroupRouteEndpoint(path string) string {
	switch {
	case strings.Contains(path, "/messages/count_tokens"):
		return CompositeRouteEndpointCountTokens
	case strings.Contains(path, "/messages"):
		return CompositeRouteEndpointMessages
	case strings.Contains(path, "/responses"), strings.Contains(path, "/alpha/search"), strings.Contains(path, "/realtime/calls"), strings.HasSuffix(path, "/live"):
		return CompositeRouteEndpointResponses
	case strings.Contains(path, "/chat/completions"):
		return CompositeRouteEndpointChatCompletions
	case strings.Contains(path, "/embeddings"):
		return CompositeRouteEndpointEmbeddings
	case strings.Contains(path, "/images/"):
		return CompositeRouteEndpointImages
	case strings.Contains(path, "/v1beta/"):
		return CompositeRouteEndpointGemini
	default:
		return CompositeRouteEndpointAny
	}
}

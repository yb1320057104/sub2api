package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func ensureCompositeTargetPlatform(c *gin.Context, apiKey *service.APIKey, model string) {
	if c == nil || c.Request == nil || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
		return
	}
	if _, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
		return
	}
	if platform, ok := service.DetectModelPlatform(model); ok {
		c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), platform))
	}
}

func compositeTargetPlatformAllowed(c *gin.Context, apiKey *service.APIKey, model string, allowed ...string) bool {
	if c == nil || c.Request == nil || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
		return true
	}
	ensureCompositeTargetPlatform(c, apiKey, model)
	platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	if !ok {
		return false
	}
	for _, allowedPlatform := range allowed {
		if platform == allowedPlatform {
			return true
		}
	}
	return false
}

func compositeTargetPlatformResolved(c *gin.Context, apiKey *service.APIKey, model string) bool {
	if c == nil || c.Request == nil || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
		return true
	}
	ensureCompositeTargetPlatform(c, apiKey, model)
	_, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	return ok
}

func effectiveAPIKeyPlatform(c *gin.Context, apiKey *service.APIKey) string {
	if c != nil && c.Request != nil {
		if platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
			return platform
		}
	}
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return apiKey.Group.Platform
}

func openAIReasoningEffortPolicyForRequest(c *gin.Context, apiKey *service.APIKey) (string, []service.ReasoningEffortMapping, string, bool) {
	if apiKey == nil || apiKey.Group == nil {
		return "", nil, "", false
	}
	if apiKey.Group.Platform != service.PlatformAnthropic && apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite {
		return "", nil, "", false
	}
	effectivePlatform := effectiveAPIKeyPlatform(c, apiKey)
	if effectivePlatform != service.PlatformAnthropic && effectivePlatform != service.PlatformOpenAI {
		return "", nil, "", false
	}
	maxEffort, mappings := apiKey.Group.MaxReasoningEffort, apiKey.Group.ReasoningEffortMappings
	if effectivePlatform == service.PlatformAnthropic {
		maxEffort, mappings = anthropicCompatibleReasoningEffortPolicy(maxEffort, mappings)
	}
	return maxEffort, mappings, apiKey.Group.MaxReasoningEffortOverLimit, true
}

func anthropicReasoningEffortPolicyForRequest(c *gin.Context, apiKey *service.APIKey) (string, []service.ReasoningEffortMapping, string, bool) {
	if apiKey == nil || apiKey.Group == nil {
		return "", nil, "", false
	}
	if apiKey.Group.Platform != service.PlatformAnthropic && apiKey.Group.Platform != service.PlatformComposite {
		return "", nil, "", false
	}
	if effectiveAPIKeyPlatform(c, apiKey) != service.PlatformAnthropic {
		return "", nil, "", false
	}
	maxEffort, mappings := anthropicCompatibleReasoningEffortPolicy(apiKey.Group.MaxReasoningEffort, apiKey.Group.ReasoningEffortMappings)
	return maxEffort, mappings, apiKey.Group.MaxReasoningEffortOverLimit, true
}

func anthropicCompatibleReasoningEffortPolicy(maxEffort string, mappings []service.ReasoningEffortMapping) (string, []service.ReasoningEffortMapping) {
	compatibleEffort := func(effort string) string {
		switch strings.ToLower(strings.TrimSpace(effort)) {
		case "ultra", "persistent":
			return "max"
		}
		if service.NormalizeMaxReasoningEffort(effort) == "minimal" {
			return "low"
		}
		return effort
	}
	maxEffort = compatibleEffort(maxEffort)
	normalizedMappings := append([]service.ReasoningEffortMapping(nil), mappings...)
	for i := range normalizedMappings {
		normalizedMappings[i].To = compatibleEffort(normalizedMappings[i].To)
	}
	return maxEffort, normalizedMappings
}

func bindRequestedReasoningEffort(c *gin.Context, body []byte, model string) {
	if c == nil || c.Request == nil {
		return
	}
	effort := service.CanonicalRequestedReasoningEffort(body, model)
	if effort == nil {
		return
	}
	c.Request = c.Request.WithContext(service.WithRequestedReasoningEffort(c.Request.Context(), *effort))
}

func stampOpenAIRequestedReasoningEffort(result *service.OpenAIForwardResult, c *gin.Context) {
	if result == nil || result.RequestedReasoningEffort != nil {
		return
	}
	if c == nil || c.Request == nil {
		return
	}
	result.RequestedReasoningEffort = service.RequestedReasoningEffortFromContext(c.Request.Context())
}

func stampForwardRequestedReasoningEffort(result *service.ForwardResult, requested *string) {
	if result == nil || result.RequestedReasoningEffort != nil {
		return
	}
	result.RequestedReasoningEffort = requested
}

func applyOpenAIReasoningEffortPolicyForRequest(c *gin.Context, apiKey *service.APIKey, body []byte) ([]byte, bool, error) {
	requestModel := clientRequestedModel(c, gjson.GetBytes(body, "model").String())
	bindRequestedReasoningEffort(c, body, requestModel)
	maxEffort, mappings, overLimit, ok := openAIReasoningEffortPolicyForRequest(c, apiKey)
	if !ok {
		return body, false, nil
	}
	forced := ""
	if apiKey != nil && effectiveAPIKeyPlatform(c, apiKey) == service.PlatformOpenAI {
		forced = apiKey.ReasoningEffort
	}
	if forced != "" {
		updated, changed, err := service.ApplyAPIKeyOpenAIReasoningEffortPolicy(body, forced, maxEffort, mappings, overLimit, requestModel)
		if err == nil && c != nil && c.Request != nil {
			// Fixed key preferences replace group remaps; retain the one-pass
			// marker for later account-specific wire adaptation.
			ctx := service.WithOpenAIReasoningEffortPolicy(c.Request.Context(), maxEffort, nil, overLimit, requestModel)
			ctx = service.WithAppliedOpenAIReasoningEffortPolicy(ctx, updated)
			c.Request = c.Request.WithContext(ctx)
		}
		return updated, changed, err
	}
	if c != nil && c.Request != nil {
		ctx := service.WithOpenAIReasoningEffortPolicy(c.Request.Context(), maxEffort, mappings, overLimit, requestModel)
		ctx = service.WithReasoningEffortRequestModel(ctx, requestModel)
		ctx = service.WithAppliedOpenAIReasoningEffortPolicy(ctx, body)
		c.Request = c.Request.WithContext(ctx)
		return service.ApplyOpenAIReasoningEffortPolicyAtIngress(ctx, body)
	}
	return service.ApplyOpenAIReasoningEffortPolicy(body, maxEffort, mappings, overLimit, requestModel)
}

func respondOpenAIReasoningEffortPolicyError(c *gin.Context, err error, write func(*gin.Context, int, string, string)) {
	if c == nil || err == nil || write == nil {
		return
	}
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	write(c, http.StatusForbidden, "permission_error", err.Error())
}

func applyAnthropicReasoningEffortPolicyForRequest(c *gin.Context, apiKey *service.APIKey, body []byte) ([]byte, bool, error) {
	maxEffort, mappings, overLimit, ok := anthropicReasoningEffortPolicyForRequest(c, apiKey)
	if !ok {
		return body, false, nil
	}
	return service.ApplyReasoningEffortPolicy(body, maxEffort, mappings, overLimit)
}

func prepareOpenAIReasoningEffortPolicyForMessagesRequest(c *gin.Context, apiKey *service.APIKey, body []byte) ([]byte, error) {
	if c == nil || c.Request == nil {
		return body, nil
	}
	requestModel := clientRequestedModel(c, gjson.GetBytes(body, "model").String())
	bindRequestedReasoningEffort(c, body, requestModel)
	maxEffort, mappings, overLimit, ok := openAIReasoningEffortPolicyForRequest(c, apiKey)
	if !ok {
		return body, nil
	}
	if apiKey.ReasoningEffort != "" && effectiveAPIKeyPlatform(c, apiKey) == service.PlatformOpenAI {
		c.Request = c.Request.WithContext(service.WithAPIKeyOpenAIReasoningEffortPolicy(c.Request.Context(), apiKey.ReasoningEffort, maxEffort, mappings, overLimit, requestModel))
		return body, nil
	}
	// The Messages bridge synthesizes a default OpenAI effort when
	// output_config.effort is omitted. Bind the group policy only for an
	// explicit client value so the ceiling does not alter that default.
	effort := gjson.GetBytes(body, "output_config.effort")
	if !effort.Exists() || effort.Type != gjson.String || strings.TrimSpace(effort.String()) == "" {
		// An explicit ultra model suffix is a client preference, not a bridge
		// default. Enforce it before conversion or account/model normalization.
		requested := service.CanonicalRequestedReasoningEffort(nil, requestModel)
		if requested == nil || *requested != "ultra" {
			return body, nil
		}
		payload, err := sjson.SetBytes(body, "output_config.effort", "ultra")
		if err != nil {
			return body, err
		}
		updated, changed, err := service.ApplyReasoningEffortPolicy(payload, maxEffort, mappings, overLimit, requestModel)
		if err != nil || !changed {
			return body, err
		}
		// Do not bind the policy again: a second mapping after conversion could
		// turn ultra -> high -> low instead of applying one mapping.
		return updated, nil
	}
	c.Request = c.Request.WithContext(service.WithOpenAIReasoningEffortPolicy(c.Request.Context(), maxEffort, mappings, overLimit, requestModel))
	return body, nil
}

package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const keyGroupRouterContextKey = "sub2api.key_group_router"
const keyGroupWSResolverContextKey = "sub2api.key_group_ws_resolver"
const keyResourceGroupsContextKey = "sub2api.key_resource_groups"

type keyGroupAvailability interface {
	Available(context.Context, *service.Group, service.APIKeyGroupRouteRequest) (bool, error)
}

func WithAPIKeyGroupRouter(router *service.APIKeyGroupRouter, auth gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if router != nil {
			c.Set(keyGroupRouterContextKey, keyGroupAvailability(router))
		}
		auth(c)
	}
}

func chooseAPIKeyRequestGroup(ctx context.Context, key *service.APIKey, request service.APIKeyGroupRouteRequest, router keyGroupAvailability, eligibility ...func(*service.Group) (bool, error)) (*service.APIKey, error) {
	if key == nil || len(key.SelectedGroupIDs()) <= 1 {
		return key, nil
	}
	if router == nil {
		return nil, errors.New("multi-group routing unavailable")
	}
	for _, id := range key.SelectedGroupIDs() {
		group := apiKeyCandidateGroup(key, id)
		if group == nil || !group.IsActive() {
			continue
		}
		// The handshake has no model yet. Only defer account/model selection;
		// keep its preliminary platform compatible with the Responses handler.
		if request.WebSocket && request.Model == "" && group.Platform != service.PlatformOpenAI && group.Platform != service.PlatformGrok && group.Platform != service.PlatformComposite {
			continue
		}
		if key.User == nil || (!group.IsSubscriptionType() && !key.User.CanBindGroup(group.ID, group.IsExclusive)) {
			continue
		}
		if len(eligibility) > 0 && eligibility[0] != nil {
			allowed, err := eligibility[0](group)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
		}
		available, err := true, error(nil)
		if !request.WebSocket || request.Model != "" {
			available, err = router.Available(ctx, group, request)
		}
		if err != nil {
			return nil, err
		}
		if !available {
			continue
		}
		copyKey := *key
		copyKey.GroupID, copyKey.Group = &group.ID, group
		copyUser := *key.User
		copyUser.UserGroupRPMOverride = nil // Reload the selected group's override.
		copyKey.User = &copyUser
		return &copyKey, nil
	}
	return nil, service.ErrNoAvailableAccounts
}

func routeAPIKeyRequestGroup(c *gin.Context, key *service.APIKey, subscriptions *service.SubscriptionService, cfg *config.Config) (*service.APIKey, bool) {
	if key == nil || len(key.SelectedGroupIDs()) <= 1 {
		return key, true
	}
	// Introspection retains the saved primary identity. Bound resource lookups
	// instead resolve the original owner group before its normal auth checks.
	path := c.Request.URL.Path
	ws := isResponsesWebSocketRoute(c)
	resourceRoute := strings.Contains(path, "/cachedContents") || strings.Contains(path, "/custom-voices") ||
		(strings.Contains(path, "/videos/") && c.Param("request_id") != "") ||
		(strings.Contains(path, "/contents/generations/tasks/") && c.Param("task_id") != "")
	if !ws && !resourceRoute && c.Request.Method != http.MethodPost && c.Request.Method != http.MethodPut && c.Request.Method != http.MethodPatch && !strings.HasSuffix(path, "/realtime") {
		return key, true
	}
	value, _ := c.Get(keyGroupRouterContextKey)
	router, _ := value.(keyGroupAvailability)
	request := service.APIKeyGroupRouteRequest{Path: path, Method: c.Request.Method, WebSocket: ws, ContentType: c.GetHeader("Content-Type"), APIKeyID: key.ID, UserID: key.UserID}
	if !ws && (c.Request.Method == http.MethodPost || c.Request.Method == http.MethodPut || c.Request.Method == http.MethodPatch) {
		models, ok := groupModelAllowlistModelsFromBody(c)
		if !ok {
			return nil, false
		}
		request.Models = models
		body, _ := httputil.ReadRequestBodyWithPrealloc(c.Request)
		request.Body = body
		requestmodel.ResetRequestBody(c.Request, body)
		request.Model = requestmodel.FromBodyForRoute(c.FullPath(), request.ContentType, body)
	}
	if !ws {
		if model := groupModelAllowlistModelFromParams(c); model != "" {
			request.Model, request.Models = model, []string{model}
		} else if request.Model == "" {
			request.Model = strings.TrimSpace(c.Query("model"))
			if request.Model != "" {
				request.Models = []string{request.Model}
			}
		}
		validator := service.NewClaudeCodeValidator()
		if validator.ValidateUserAgent(c.GetHeader("User-Agent")) {
			var payload map[string]any
			_ = json.Unmarshal(request.Body, &payload)
			c.Request = c.Request.WithContext(service.SetClaudeCodeClient(c.Request.Context(), validator.Validate(c.Request, payload)))
		}
	}
	cacheList := strings.HasSuffix(path, "/cachedContents") && c.Request.Method == http.MethodGet
	request.IdentityOnly = cacheList || strings.Contains(path, "/custom-voices")
	switch {
	case strings.Contains(path, "/cachedContents/") && c.Param("cacheID") != "":
		request.ResourceKind, request.ResourceID = "gemini_cache", c.Param("cacheID")
	case strings.Contains(path, "/videos/") && c.Param("request_id") != "":
		request.ResourceKind, request.ResourceID = "video", c.Param("request_id")
	case strings.Contains(path, "/contents/generations/tasks/") && c.Param("task_id") != "":
		request.ResourceKind, request.ResourceID = "video", service.SeedanceTaskKey(c.Param("task_id"))
	case strings.Contains(path, "/v1beta/") && len(request.Body) > 0:
		ref, err := service.FindGeminiCachedContentReference(request.Body)
		if err != nil {
			groupModelAllowlistErrorWriter(c)(c, http.StatusBadRequest, "Invalid cached content reference")
			c.Abort()
			return nil, false
		}
		if ref != nil {
			request.ResourceKind, request.ResourceID = "gemini_cache", ref.Name
		}
	}
	eligible := func(group *service.Group) (bool, error) {
		return eligibleAPIKeyGroupSubscription(c.Request.Context(), key, group, subscriptions, cfg)
	}
	if cacheList {
		ids, err := apiKeyResourceGroupIDs(c.Request.Context(), key, request, router, eligible)
		if err != nil {
			groupModelAllowlistErrorWriter(c)(c, http.StatusServiceUnavailable, "Failed to load resource permissions")
			c.Abort()
			return nil, false
		}
		c.Set(keyResourceGroupsContextKey, ids)
	}
	selected, err := chooseAPIKeyRequestGroup(c.Request.Context(), key, request, router, eligible)
	if err != nil {
		status, message := http.StatusServiceUnavailable, "No available accounts in the selected groups"
		if request.ResourceKind != "" && errors.Is(err, service.ErrNoAvailableAccounts) {
			status, message = http.StatusNotFound, "Resource not found or permission denied"
		}
		groupModelAllowlistErrorWriter(c)(c, status, message)
		c.Abort()
		return nil, false
	}
	SetOpsFallbackAPIKey(c, selected)
	if ws {
		// A Responses model is first known after upgrade. Re-evaluate before its
		// first upstream turn, including the selected group's billing authority.
		c.Set(keyGroupWSResolverContextKey, func(body []byte) (*service.APIKey, error) {
			request.Body = body
			request.Models = requestmodel.FromBodyCandidates("", "application/json", body)
			if len(request.Models) > 0 {
				request.Model = request.Models[0]
			}
			next, routeErr := chooseAPIKeyRequestGroup(c.Request.Context(), key, request, router, eligible)
			if routeErr != nil {
				return nil, routeErr
			}
			var subscription *service.UserSubscription
			if cfg == nil || cfg.RunMode != config.RunModeSimple {
				if next.Group.IsSubscriptionType() && subscriptions != nil {
					subscription, routeErr = subscriptions.GetActiveSubscription(c.Request.Context(), next.User.ID, next.Group.ID)
					if routeErr != nil {
						return nil, routeErr
					}
					maintenance, validationErr := subscriptions.ValidateAndCheckLimits(subscription, next.Group)
					if maintenance {
						subscription, routeErr = subscriptions.EnsureWindowMaintenance(c.Request.Context(), subscription)
						if routeErr != nil {
							return nil, routeErr
						}
						_, validationErr = subscriptions.ValidateAndCheckLimits(subscription, next.Group)
					}
					if validationErr != nil {
						return nil, validationErr
					}
				} else if apiKeyBalanceBelowAuthThreshold(next.User.Balance, cfg) {
					return nil, service.ErrInsufficientBalance
				}
			}
			c.Set(string(ContextKeySubscription), subscription)
			c.Set(string(ContextKeyAPIKey), next)
			SetOpsFallbackAPIKey(c, next)
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, next.Group))
			return next, nil
		})
	}
	return selected, true
}

func apiKeyCandidateGroup(key *service.APIKey, id int64) *service.Group {
	for _, group := range key.Groups {
		if group != nil && group.ID == id {
			return group
		}
	}
	if key.Group != nil && key.Group.ID == id {
		return key.Group
	}
	return nil
}

func apiKeyResourceGroupIDs(ctx context.Context, key *service.APIKey, request service.APIKeyGroupRouteRequest, router keyGroupAvailability, eligible func(*service.Group) (bool, error)) ([]int64, error) {
	if router == nil {
		return nil, errors.New("resource routing unavailable")
	}
	ids := []int64{}
	for _, id := range key.SelectedGroupIDs() {
		group := apiKeyCandidateGroup(key, id)
		if group == nil || !group.IsActive() || key.User == nil || (!group.IsSubscriptionType() && !key.User.CanBindGroup(id, group.IsExclusive)) {
			continue
		}
		allowed, err := eligible(group)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		available, err := router.Available(ctx, group, request)
		if err != nil {
			return nil, err
		}
		if available {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// APIKeyResourceGroupIDs exposes only groups admitted for this resource list.
// A caller without multi-group routing keeps its original single-group scope.
func APIKeyResourceGroupIDs(c *gin.Context, key *service.APIKey) []int64 {
	if value, exists := c.Get(keyResourceGroupsContextKey); exists {
		if ids, ok := value.([]int64); ok {
			return append([]int64{}, ids...)
		}
	}
	if key != nil && key.GroupID != nil {
		return []int64{*key.GroupID}
	}
	return nil
}

func ResolveAPIKeyWebSocketGroup(c *gin.Context, body []byte) (*service.APIKey, error) {
	value, _ := c.Get(keyGroupWSResolverContextKey)
	if resolve, ok := value.(func([]byte) (*service.APIKey, error)); ok {
		return resolve(body)
	}
	key, _ := GetAPIKeyFromContext(c)
	return key, nil
}

func deferAPIKeyWebSocketGroupBilling(c *gin.Context, key *service.APIKey) bool {
	return key != nil && len(key.SelectedGroupIDs()) > 1 && isResponsesWebSocketRoute(c)
}

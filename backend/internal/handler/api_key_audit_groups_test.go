package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type keyAuditRuleFixture struct {
	calls int
	err   error
}

func (r *keyAuditRuleFixture) PublicGroupAuditRules(context.Context) (map[int64]service.CyberPolicyGroupRule, error) {
	r.calls++
	return map[int64]service.CyberPolicyGroupRule{7: {GroupID: 7, Multiplier: "2.500000", AutoDeductEnabled: true}}, r.err
}

type keyAuditUserFixture struct{ service.UserRepository }

func (keyAuditUserFixture) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 1}, nil
}

type keyAuditGroupsFixture struct{ service.GroupRepository }

func (keyAuditGroupsFixture) ListActive(context.Context) ([]service.Group, error) {
	return []service.Group{{ID: 7, Name: "selected", Status: "active"}, {ID: 8, Name: "default", Status: "active"}}, nil
}

type keyAuditSubscriptionsFixture struct {
	service.UserSubscriptionRepository
}

func (keyAuditSubscriptionsFixture) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

type keyAuditKeysFixture struct{ service.APIKeyRepository }

func (keyAuditKeysFixture) ListByUserID(context.Context, int64, pagination.PaginationParams, service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	primary := &service.Group{ID: 7, Name: "selected"}
	return []service.APIKey{{ID: 1, UserID: 1, GroupID: &primary.ID, Group: primary, GroupIDs: []int64{7, 8}, Groups: []*service.Group{primary, {ID: 8, Name: "default"}}}}, &pagination.PaginationResult{Total: 1}, nil
}

func TestAPIKeyPublicAuditRulesDecorateAvailableAndOrderedGroupsOnce(t *testing.T) {
	for _, endpoint := range []string{"available", "keys"} {
		t.Run(endpoint, func(t *testing.T) {
			svc := service.NewAPIKeyService(keyAuditKeysFixture{}, keyAuditUserFixture{}, keyAuditGroupsFixture{}, keyAuditSubscriptionsFixture{}, nil, nil, &config.Config{})
			rules := &keyAuditRuleFixture{}
			h := NewAPIKeyHandler(svc)
			h.auditRules = rules
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/fixture", nil)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
			if endpoint == "available" {
				h.GetAvailableGroups(c)
			} else {
				h.List(c)
			}
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, 1, rules.calls)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
			var groups []any
			if endpoint == "available" {
				groups = payload["data"].([]any)
			} else {
				key := payload["data"].(map[string]any)["items"].([]any)[0].(map[string]any)
				groups = key["groups"].([]any)
				require.Equal(t, "2.500000", key["group"].(map[string]any)["security_audit_multiplier"])
			}
			require.Len(t, groups, 2)
			require.Equal(t, "2.500000", groups[0].(map[string]any)["security_audit_multiplier"])
			require.Equal(t, true, groups[0].(map[string]any)["security_audit_auto_deduct_enabled"])
			require.Equal(t, "1.000000", groups[1].(map[string]any)["security_audit_multiplier"])
			require.Equal(t, false, groups[1].(map[string]any)["security_audit_auto_deduct_enabled"])
			for _, private := range []string{"user_allowlist_ids", "group_rules", "auto_disable_threshold"} {
				require.NotContains(t, recorder.Body.String(), private)
			}
		})
	}
}

func TestAPIKeyPublicAuditRulesReadFailureDoesNotPublishFalseDefaults(t *testing.T) {
	svc := service.NewAPIKeyService(nil, keyAuditUserFixture{}, keyAuditGroupsFixture{}, keyAuditSubscriptionsFixture{}, nil, nil, &config.Config{})
	h := NewAPIKeyHandler(svc)
	h.auditRules = &keyAuditRuleFixture{err: errors.New("fixture unavailable")}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/fixture", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
	h.GetAvailableGroups(c)
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "security_audit_multiplier")
}

//go:build unit

package handler

// 槽位终检与生图跳门回归（handler 半程）：
//   - 槽位获取成功后的利润终检：越线账号释放槽位并要求调用方排除重选，
//     不写响应、不绑定粘连；
//   - openAIResponsesRequiredCapability 的请求能力映射覆盖生图与原生远程压缩。

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestGroupAntiDistillationPostQueueReleasesSlot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "new pause rejects without reschedule", true: "cache outage fails open"}[unavailable], func(t *testing.T) {
			checks, releases := 0, 0
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{UserID: 1, Group: &service.Group{ID: 1, Platform: service.PlatformOpenAI}})
			})
			r.Use(middleware.GroupAntiDistillation(func(context.Context, *service.APIKey) (time.Duration, error) {
				checks++
				if checks == 1 {
					return 0, nil
				}
				if unavailable {
					return 0, errors.New("cache offline")
				}
				return time.Minute, nil
			}))
			r.POST("/v1/responses", func(c *gin.Context) {
				h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
				selection := &service.AccountSelectionResult{Account: &service.Account{ID: 1}, Acquired: true, ReleaseFunc: func() { releases++ }}
				streamStarted := false
				release, outcome := h.acquireResponsesAccountSlot(c, nil, "", selection, false, &streamStarted, zap.NewNop())
				if unavailable {
					require.Equal(t, openAISlotAcquireOK, outcome)
					require.NotNil(t, release)
					release()
					c.Status(204)
				} else {
					require.Equal(t, openAISlotAcquireFailed, outcome, "must not return profit veto or cause account reselection")
					require.Nil(t, release)
				}
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/responses", nil))
			require.Equal(t, 2, checks)
			require.Equal(t, 1, releases)
			if unavailable {
				require.Equal(t, 204, w.Code)
			} else {
				require.Equal(t, 429, w.Code)
				require.Equal(t, "60", w.Header().Get("Retry-After"))
				require.Contains(t, w.Body.String(), middleware.GroupAntiDistillationPausedCode)
			}
		})
	}
}

func TestGroupAntiDistillationWSPauseDoesNotHarmAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{UserID: 1, Group: &service.Group{ID: 1, Platform: service.PlatformOpenAI}})
	})
	checks := 0
	r.Use(middleware.GroupAntiDistillation(func(context.Context, *service.APIKey) (time.Duration, error) {
		checks++
		if checks == 1 {
			return 0, nil
		}
		return time.Minute, nil
	}))
	r.GET("/responses", func(c *gin.Context) {
		err := checkOpenAIWSGroupAntiDistillation(c.Request.Context(), c)
		require.ErrorIs(t, err, errGroupAntiDistillationPaused)
		require.False(t, shouldReportOpenAIWSProxyAccountFailure(err))
		c.Status(204)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/responses", nil))
	require.Equal(t, 204, w.Code)
	require.Equal(t, 2, checks)
}

func TestGroupAntiDistillationMessagesAfterQueueUsesNativeError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[streaming], func(t *testing.T) {
			checks := 0
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{UserID: 1, Group: &service.Group{ID: 1, Platform: service.PlatformOpenAI}})
			})
			r.Use(middleware.GroupAntiDistillation(func(context.Context, *service.APIKey) (time.Duration, error) {
				checks++
				if checks == 1 {
					return 0, nil
				}
				return time.Minute, nil
			}))
			r.POST("/v1/messages", func(c *gin.Context) {
				h := &OpenAIGatewayHandler{}
				if streaming {
					c.Header("Content-Type", "text/event-stream")
					c.Writer.WriteHeaderNow()
				}
				require.False(t, h.groupAntiDistillationAdmitted(c, streaming, nil))
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", nil))
			require.Contains(t, w.Body.String(), `"type":"error"`)
			require.Contains(t, w.Body.String(), `"code":"group_anti_distillation_paused"`)
			if streaming {
				require.Contains(t, w.Body.String(), "event: error\n")
			} else {
				require.Equal(t, 429, w.Code)
			}
		})
	}
}

type profitCountingConcurrencyCache struct {
	fakeConcurrencyCache
	accountReleases atomic.Int64
}

func (c *profitCountingConcurrencyCache) ReleaseAccountSlot(context.Context, int64, string) error {
	c.accountReleases.Add(1)
	return nil
}

func profitSlotTestAccount(id int64, rate float64) *service.Account {
	now := time.Now()
	return &service.Account{
		ID:             id,
		Platform:       service.PlatformOpenAI,
		Type:           service.AccountTypeAPIKey,
		Status:         service.StatusActive,
		Schedulable:    true,
		Concurrency:    2,
		RateMultiplier: &rate,
		Extra: map[string]any{
			"upstream_billing_probe": map[string]any{
				"status":      service.UpstreamBillingProbeStatusOK,
				"received_at": now.Add(-time.Minute),
				"fresh_until": now.Add(30 * time.Minute),
				"data": map[string]any{
					"billing_scope":            "token",
					"resolved_rate_multiplier": rate,
					"peak_rate_enabled":        false,
				},
			},
		},
	}
}

func profitSlotTestContext(t *testing.T, gw *service.OpenAIGatewayService, groupID int64, suppress bool) context.Context {
	t.Helper()
	group := &service.Group{
		ID:                   groupID,
		Platform:             service.PlatformOpenAI,
		Status:               service.StatusActive,
		Hydrated:             true,
		RateMultiplier:       1.0,
		SubscriptionType:     service.SubscriptionTypeStandard,
		ProfitControlEnabled: true,
		ProfitMinMargin:      0.5,
	}
	base := context.WithValue(context.Background(), ctxkey.Group, group)
	if suppress {
		base = service.WithOpenAIProfitControlSuppressed(base)
	}
	ctx, pricingAt := gw.WithOpenAIRequestPricingContext(base, &groupID)
	require.False(t, pricingAt.IsZero())
	return ctx
}

func TestAcquireResponsesAccountSlotProfitRecheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gw := &service.OpenAIGatewayService{}
	groupID := int64(50)

	newHandler := func(cache *profitCountingConcurrencyCache) *OpenAIGatewayHandler {
		return &OpenAIGatewayHandler{
			gatewayService:    gw,
			concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatClaude, 0),
		}
	}
	newSelection := func(account *service.Account) *service.AccountSelectionResult {
		return &service.AccountSelectionResult{
			Account:  account,
			Acquired: false,
			WaitPlan: &service.AccountWaitPlan{AccountID: account.ID, MaxConcurrency: 2, Timeout: time.Second, MaxWaiting: 2},
		}
	}

	t.Run("veto releases slot and requests reschedule without writing response", func(t *testing.T) {
		cache := &profitCountingConcurrencyCache{}
		h := newHandler(cache)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(profitSlotTestContext(t, gw, groupID, false))
		streamStarted := false

		release, result := h.acquireResponsesAccountSlot(c, &groupID, "", newSelection(profitSlotTestAccount(1, 0.8)), false, &streamStarted, zap.NewNop())
		require.Equal(t, openAISlotAcquireProfitVetoed, result)
		require.Nil(t, release)
		require.Zero(t, w.Body.Len(), "利润终检否决不得写出任何响应")
		require.Equal(t, int64(1), cache.accountReleases.Load(), "否决后必须立即释放已获取的槽位")
	})

	t.Run("qualifying account acquires normally", func(t *testing.T) {
		cache := &profitCountingConcurrencyCache{}
		h := newHandler(cache)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(profitSlotTestContext(t, gw, groupID, false))
		streamStarted := false

		release, result := h.acquireResponsesAccountSlot(c, &groupID, "", newSelection(profitSlotTestAccount(2, 0.3)), false, &streamStarted, zap.NewNop())
		require.Equal(t, openAISlotAcquireOK, result)
		require.NotNil(t, release)
		release()
	})

	t.Run("image intent suppression keeps official behavior", func(t *testing.T) {
		cache := &profitCountingConcurrencyCache{}
		h := newHandler(cache)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(profitSlotTestContext(t, gw, groupID, true))
		streamStarted := false

		release, result := h.acquireResponsesAccountSlot(c, &groupID, "", newSelection(profitSlotTestAccount(3, 0.8)), false, &streamStarted, zap.NewNop())
		require.Equal(t, openAISlotAcquireOK, result, "生图意图跳门：过贵账号照常获取（图片边界不装门）")
		require.NotNil(t, release)
		release()
	})
}

func TestOpenAIResponsesRequiredCapabilityForRequest(t *testing.T) {
	require.Equal(t, service.OpenAIEndpointCapabilityResponses, openAIResponsesRequiredCapability(true, service.PlatformOpenAI))
	require.Equal(t, service.OpenAIEndpointCapabilityChatCompletions, openAIResponsesRequiredCapability(false, service.PlatformOpenAI))
	require.Equal(t, service.OpenAIEndpointCapabilityChatCompletions, openAIResponsesRequiredCapability(true, service.PlatformGrok))
	require.Equal(t, service.OpenAIEndpointCapabilityResponses, openAIResponsesRequiredCapabilityForRequest(false, true, service.PlatformOpenAI))
	require.Equal(t, service.OpenAIEndpointCapabilityChatCompletions, openAIResponsesRequiredCapabilityForRequest(false, true, service.PlatformGrok))
}

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Adapted from upstream PR #8549, commit 7e3b6c72faded2427585a6d528b977943184e0e5.
// Explicit identities remain isolated from anonymous transcript prefixes.
// Exercise the original 256/257/1000-item regression through HTTP admission
// with the real Redis repository backed by miniredis.
func TestRejectIfCyberSessionBlocked_Issue8519LongHistory(t *testing.T) {
	cache := testutil.NewRedisGatewayCache(t)
	h := newCyberIdentityAdmissionTestHandlerWithCache(map[string]string{
		service.SettingKeyCyberSessionBlockEnabled:          "true",
		service.SettingKeyCyberSessionIdentityStrictEnabled: "false",
	}, cache)
	apiKey := &service.APIKey{ID: 77, User: &service.User{ID: 9}}
	const clientIP, userAgent = "203.0.113.20", "Codex CLI 1.2.3"
	request := func(body []byte, session string) (*gin.Context, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(string(body)))
		c.Request.RemoteAddr = clientIP + ":12345"
		c.Request.Header.Set("User-Agent", userAgent)
		if session != "" {
			c.Request.Header.Set("session_id", session)
		}
		return c, recorder
	}
	hitBody := []byte(`{"input":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"trigger"}]}`)
	hitContext, _ := request(hitBody, "blocked-session")
	blockedKey := service.CyberSessionExplicitBlockKey(apiKey.ID, hitContext, hitBody)
	require.NotEmpty(t, blockedKey)
	store, ok := cache.(service.CyberSessionBlockStore)
	require.True(t, ok)
	// A real explicit-session write must not activate an anonymous source scope.
	legacyScope := service.CyberSessionScopeKey(apiKey.ID, clientIP, userAgent)
	identity := service.ResolveCyberSessionIdentity(apiKey.ID, hitContext, hitBody)
	h.gatewayService.MarkCyberSessionBlockedForRequestWithScope(context.Background(), apiKey.ID, identity, hitBody, legacyScope)
	active, err := store.IsCyberSessionScopeActive(context.Background(), legacyScope)
	require.NoError(t, err)
	require.False(t, active)

	for _, count := range []int{256, 257, 1000} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			items := make([]map[string]string, count)
			for i := range items {
				items[i] = map[string]string{"type": "function_call_output", "call_id": "call-" + strconv.Itoa(i), "output": "ordinary tool result"}
			}
			body, err := json.Marshal(map[string]any{"input": items, "prompt_cache_key": "shared-cache"})
			require.NoError(t, err)
			for _, tc := range []struct {
				name, session string
				blocked       bool
			}{
				{"another explicit session", "other-session", false},
				{"missing identity with shared source", "", false},
				{"blocked session with padded history", "blocked-session", true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, format := range []cyberSessionBlockFormat{cyberBlockFormatResponses, cyberBlockFormatChat, cyberBlockFormatAnthropic} {
						c, recorder := request(body, tc.session)
						require.Equal(t, tc.blocked, h.rejectIfCyberSessionBlocked(c, apiKey, body, "gpt-5", format))
						if tc.blocked {
							require.Equal(t, http.StatusForbidden, recorder.Code)
							require.Equal(t, "permission_error", gjson.Get(recorder.Body.String(), "error.type").String())
						} else {
							require.False(t, c.Writer.Written(), "unrelated long histories must pass local admission")
						}
					}
				})
			}

			t.Run("anonymous exact retry remains blocked", func(t *testing.T) {
				c, _ := request(body, "")
				identity := service.ResolveCyberSessionIdentity(apiKey.ID, c, body)
				h.gatewayService.MarkCyberSessionBlockedForRequest(context.Background(), apiKey.ID, identity, body)
				for _, session := range []string{"", "added-session"} {
					c, recorder := request(body, session)
					require.True(t, h.rejectIfCyberSessionBlocked(c, apiKey, body, "gpt-5", cyberBlockFormatResponses))
					require.Equal(t, http.StatusForbidden, recorder.Code)
				}
				items[0]["output"] = "different tool result"
				changed, err := json.Marshal(map[string]any{"input": items, "prompt_cache_key": "shared-cache"})
				require.NoError(t, err)
				c, _ = request(changed, "")
				require.False(t, h.rejectIfCyberSessionBlocked(c, apiKey, changed, "gpt-5", cyberBlockFormatResponses), "a fingerprint blocks identical content, not shared history length")
			})
		})
	}
}

func TestPKCyberAnonymousTranscriptRecordAndAdmission(t *testing.T) {
	cache := testutil.NewRedisGatewayCache(t)
	h := newCyberIdentityAdmissionTestHandlerWithCache(map[string]string{
		service.SettingKeyCyberSessionBlockEnabled: "true",
	}, cache)
	apiKey := &service.APIKey{ID: 78, User: &service.User{ID: 9}}
	request := func(body []byte, clientIP, session string) (*gin.Context, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(string(body)))
		c.Request.RemoteAddr = clientIP + ":12345"
		c.Request.Header.Set("User-Agent", "Codex CLI 1.2.3")
		if session != "" {
			c.Request.Header.Set("session_id", session)
		}
		return c, recorder
	}
	hitBody := []byte(`{"input":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"trigger"}]}`)
	c, _ := request(hitBody, "203.0.113.21", "")
	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Code: "cyber_policy", Message: "blocked", UpstreamStatus: http.StatusOK})
	h.recordCyberPolicyIfMarked(c, apiKey, nil, nil, "gpt-5", false, hitBody, service.ChannelUsageFields{}, "")
	changed := []byte(`{"input":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"rewritten follow-up"}]}`)
	for _, format := range []cyberSessionBlockFormat{cyberBlockFormatResponses, cyberBlockFormatChat, cyberBlockFormatAnthropic} {
		c, recorder := request(changed, "203.0.113.21", "")
		require.True(t, h.rejectIfCyberSessionBlocked(c, apiKey, changed, "gpt-5", format))
		require.Equal(t, http.StatusForbidden, recorder.Code)
	}
	for _, tc := range []struct{ ip, session string }{{"203.0.113.22", ""}, {"203.0.113.21", "different-explicit-session"}} {
		c, _ := request(changed, tc.ip, tc.session)
		require.False(t, h.rejectIfCyberSessionBlocked(c, apiKey, changed, "gpt-5", cyberBlockFormatResponses))
	}
	items := make([]map[string]string, 257)
	for i := range items {
		items[i] = map[string]string{"type": "function_call_output", "call_id": strconv.Itoa(i), "output": "tool result"}
	}
	longBody, err := json.Marshal(map[string]any{"input": items})
	require.NoError(t, err)
	c, _ = request(longBody, "203.0.113.21", "")
	require.True(t, h.rejectIfCyberSessionBlocked(c, apiKey, longBody, "gpt-5", cyberBlockFormatResponses), "an active anonymous scope retains the bounded transcript lookup protection")
	c, _ = request(longBody, "203.0.113.22", "")
	require.False(t, h.rejectIfCyberSessionBlocked(c, apiKey, longBody, "gpt-5", cyberBlockFormatResponses))
}

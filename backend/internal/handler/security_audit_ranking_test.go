package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rankingAuditRecorder struct {
	inputs []service.CyberPolicyAuditBlockInput
	err    error
}

func (r *rankingAuditRecorder) RecordAuditBlock(ctx context.Context, input service.CyberPolicyAuditBlockInput) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.inputs = append(r.inputs, input)
	return r.err
}

type rankingLegacyEngine struct{ result *securityaudit.LegacyDecision }

func (e rankingLegacyEngine) Check(context.Context, securityaudit.Request) (*securityaudit.LegacyDecision, error) {
	return e.result, nil
}

func newAuditRankingContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.RequestID, "same-client-request-id"))
	return c
}

func TestSecurityAuditRankingRecordsOnlyActualBlocks(t *testing.T) {
	block := &securityaudit.LegacyDecision{Blocked: true, Flagged: true, Action: service.ContentModerationActionKeywordBlock}
	for _, tc := range []struct {
		name   string
		legacy *securityaudit.LegacyDecision
		prompt securityaudit.DecisionKind
		source string
	}{
		{"content", block, securityaudit.DecisionAllow, "content_audit"},
		{"prompt", nil, securityaudit.DecisionBlock, "prompt_audit"},
		{"both-once", block, securityaudit.DecisionBlock, "combined_audit"},
		{"allow", nil, securityaudit.DecisionAllow, ""},
		{"warn", nil, securityaudit.DecisionFlag, ""},
		{"unavailable", nil, securityaudit.DecisionUnavailable, ""},
		{"invalid", nil, securityaudit.DecisionInvalid, ""},
		{"already-banned", &securityaudit.LegacyDecision{Blocked: true, Action: "banned"}, securityaudit.DecisionAllow, ""},
		{"audit-error", &securityaudit.LegacyDecision{Blocked: true, Action: service.ContentModerationActionError}, securityaudit.DecisionAllow, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &rankingAuditRecorder{}
			prompt := &turnCountingEngine{mode: securityaudit.ModeBlocking, decisions: []*securityaudit.PromptDecision{{Kind: tc.prompt}}}
			coordinator := securityaudit.NewCoordinator(rankingLegacyEngine{tc.legacy}, prompt)
			group := int64(9)
			c := newAuditRankingContext()
			result := runSecurityAudit(c, nil, coordinator, nil, &service.APIKey{ID: 3, GroupID: &group}, middleware2.AuthSubject{UserID: 7}, "openai_responses", "fixture-model", []byte(`{"input":"fixture"}`), "http", r)
			require.NotNil(t, result)
			if tc.source == "" {
				require.Empty(t, r.inputs)
				return
			}
			require.Len(t, r.inputs, 1)
			require.Equal(t, tc.source, r.inputs[0].Source)
			require.Equal(t, group, *r.inputs[0].GroupID)
			require.Equal(t, int64(7), r.inputs[0].UserID)
			require.Equal(t, "fixture-model", r.inputs[0].Model)
			require.NotContains(t, r.inputs[0].EventKey, "same-client-request-id")
			recordSecurityAuditBlock(c, r, nil, 7, "fixture-model", "http", result)
			require.Len(t, r.inputs, 1, "rechecking the same rejected request must not count twice")
		})
	}
}

func TestSecurityAuditRankingSeparatesTurnsAndClientRequestIDReuse(t *testing.T) {
	r := &rankingAuditRecorder{}
	decision := &securityaudit.Decision{Kind: securityaudit.DecisionBlock, Prompt: &securityaudit.PromptDecision{Kind: securityaudit.DecisionBlock}}
	c := newAuditRankingContext()
	recordSecurityAuditBlock(c, r, nil, 7, "fixture", "first_turn", decision)
	c.Set(securityAuditWSTurnContextKey, 1)
	recordSecurityAuditBlock(c, r, nil, 7, "fixture", "subsequent_turn", decision)
	require.Len(t, r.inputs, 1)
	c.Set(securityAuditWSTurnContextKey, 2)
	recordSecurityAuditBlock(c, r, nil, 7, "fixture", "subsequent_turn", decision)
	require.Len(t, r.inputs, 2)
	require.NotEqual(t, r.inputs[0].EventKey, r.inputs[1].EventKey)
	other := newAuditRankingContext()
	recordSecurityAuditBlock(other, r, nil, 7, "fixture", "first_turn", decision)
	require.Len(t, r.inputs, 3)
	require.NotEqual(t, r.inputs[0].EventKey, r.inputs[2].EventKey)
	require.Equal(t, r.inputs[0].RequestID, r.inputs[2].RequestID)
}

func TestSecurityAuditRankingPersistenceFailureRetainsDenialAndRetryIdentity(t *testing.T) {
	r := &rankingAuditRecorder{err: errors.New("fixture failure")}
	c := newAuditRankingContext()
	ctx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	cancel()
	d := &securityaudit.Decision{Kind: securityaudit.DecisionBlock, Prompt: &securityaudit.PromptDecision{Kind: securityaudit.DecisionBlock}}
	recordSecurityAuditBlock(c, r, nil, 7, "fixture", "http", d)
	require.Len(t, r.inputs, 1, "persistence survives client cancellation")
	require.Empty(t, c.GetString(cyberAuditRecordedKey))
	r.err = nil
	recordSecurityAuditBlock(c, r, nil, 7, "fixture", "http", d)
	require.Len(t, r.inputs, 2)
	require.Equal(t, r.inputs[0].EventKey, r.inputs[1].EventKey)
	require.Equal(t, securityaudit.DecisionBlock, d.Kind)
	require.False(t, d.AllowNextStage)
}

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rankingAuditRecorder struct {
	inputs      []service.CyberPolicyAuditBlockInput
	usageInputs []service.CyberPolicyAuditBlockInput
	err         error
}

func (r *rankingAuditRecorder) RecordAuditUsage(_ context.Context, input service.CyberPolicyAuditBlockInput, _ *service.UsageLog) error {
	r.usageInputs = append(r.usageInputs, input)
	return r.err
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

func TestSecurityAuditRankingChargeAllowDefersUntilUsageAndCachesSafely(t *testing.T) {
	r := &rankingAuditRecorder{}
	legacy := &securityaudit.LegacyDecision{Allowed: true, Flagged: true, Action: "allow", PolicyAction: "charge_allow", RiskScore: 45, RiskLevel: "medium"}
	prompt := &turnCountingEngine{mode: securityaudit.ModeBlocking, decisions: []*securityaudit.PromptDecision{{Kind: securityaudit.DecisionFlag, AllowNextStage: true, PolicyAction: "charge_allow", RiskScore: 68, RiskLevel: "high"}}}
	coordinator := securityaudit.NewCoordinator(rankingLegacyEngine{legacy}, prompt)
	group := int64(9)
	key := &service.APIKey{ID: 3, GroupID: &group}
	c := newAuditRankingContext()
	decision := runSecurityAudit(c, nil, coordinator, nil, key, middleware2.AuthSubject{UserID: 7}, "openai_responses", "fixture", []byte(`{"input":"fixture"}`), "http", r)
	require.True(t, decision.AllowNextStage)
	require.Empty(t, r.inputs, "charge_allow must not synchronously count or debit before forwarding")
	require.Empty(t, r.usageInputs)
	snapshot := service.CyberPolicyAuditUsageInput(c.Request.Context())
	require.NotNil(t, snapshot)
	require.Equal(t, "combined_audit", snapshot.Source)
	require.Equal(t, 68.0, *snapshot.RiskScore)
	require.NotContains(t, snapshot.EventKey, "same-client-request-id")
	// Same group cached completion returns nil but retains the pending decision.
	require.Nil(t, runSecurityAudit(c, nil, coordinator, nil, key, middleware2.AuthSubject{UserID: 7}, "openai_responses", "fixture", nil, "http", r))
	require.Equal(t, snapshot, service.CyberPolicyAuditUsageInput(c.Request.Context()))
	worker := usageRecordContext(c.Request.Context(), context.Background())
	// Switching to an allowed group clears the current request only.
	other := int64(10)
	allowCoordinator := securityaudit.NewCoordinator(nil, nil)
	runSecurityAudit(c, nil, allowCoordinator, nil, &service.APIKey{ID: 3, GroupID: &other}, middleware2.AuthSubject{UserID: 7}, "openai_responses", "fixture", nil, "http", r)
	require.Nil(t, service.CyberPolicyAuditUsageInput(c.Request.Context()))
	require.Equal(t, snapshot, service.CyberPolicyAuditUsageInput(worker))
}

func TestSecurityAuditRankingChargeAllowTurnIsolationAndBlockPriority(t *testing.T) {
	r := &rankingAuditRecorder{}
	c := newAuditRankingContext()
	group := int64(9)
	key := &service.APIKey{ID: 3, GroupID: &group}
	charge := &securityaudit.PromptDecision{Kind: securityaudit.DecisionFlag, AllowNextStage: true, PolicyAction: "charge_allow", RiskScore: 40, RiskLevel: "medium"}
	prompt := &turnCountingEngine{mode: securityaudit.ModeBlocking, decisions: []*securityaudit.PromptDecision{charge, {Kind: securityaudit.DecisionAllow, AllowNextStage: true, PolicyAction: "allow"}, charge}}
	coordinator := securityaudit.NewCoordinator(nil, prompt)
	for turn := 1; turn <= 3; turn++ {
		c.Set(securityAuditWSTurnContextKey, turn)
		runSecurityAudit(c, nil, coordinator, nil, key, middleware2.AuthSubject{UserID: 7}, "openai_responses", "fixture", []byte(`{"input":"fixture"}`), "subsequent_turn", r)
		snapshot := service.CyberPolicyAuditUsageInput(c.Request.Context())
		if turn == 2 {
			require.Nil(t, snapshot)
		} else {
			require.NotNil(t, snapshot)
		}
	}
	decision := &securityaudit.Decision{Kind: securityaudit.DecisionBlock, AllowNextStage: false,
		Legacy: &securityaudit.LegacyDecision{Blocked: true, Flagged: true, Action: service.ContentModerationActionKeywordBlock, PolicyAction: "block", RiskScore: 70, RiskLevel: "high"}, Prompt: charge}
	recordSecurityAuditBlock(c, r, key, 7, "fixture", "subsequent_turn", decision)
	require.Len(t, r.inputs, 1)
	require.Equal(t, "block", r.inputs[0].PolicyAction)
	require.Equal(t, "combined_audit", r.inputs[0].Source)
	require.Equal(t, 70.0, *r.inputs[0].RiskScore)
}

func TestSecurityAuditRankingConcurrentTurnBillingKeepsItsOwnDecision(t *testing.T) {
	var turns securityAuditUsageTurns
	r := &rankingAuditRecorder{}
	first := service.WithCyberPolicyAuditUsage(context.Background(), service.CyberPolicyAuditBlockInput{EventKey: "server:turn:1", UserID: 7, PolicyAction: "charge_allow"}, r)
	third := service.WithCyberPolicyAuditUsage(context.Background(), service.CyberPolicyAuditBlockInput{EventKey: "server:turn:3", UserID: 7, PolicyAction: "charge_allow"}, r)
	turns.capture(1, first)
	// The next request clears/changes Gin's current snapshot before turn 1
	// reaches the asynchronous usage callback.
	turns.capture(2, service.WithoutCyberPolicyAuditUsage(first))
	turns.capture(3, third)
	var wg sync.WaitGroup
	results := make([]context.Context, 3)
	for i := range results {
		wg.Add(1)
		go func(index int) { defer wg.Done(); results[index] = turns.take(index+1, third) }(i)
	}
	wg.Wait()
	require.Equal(t, "server:turn:1", service.CyberPolicyAuditUsageInput(results[0]).EventKey)
	require.Nil(t, service.CyberPolicyAuditUsageInput(results[1]))
	require.Equal(t, "server:turn:3", service.CyberPolicyAuditUsageInput(results[2]).EventKey)
	require.Nil(t, service.CyberPolicyAuditUsageInput(turns.take(1, third)), "missing or repeated completion must not inherit the connection snapshot")
}

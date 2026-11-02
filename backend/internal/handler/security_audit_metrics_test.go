package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSecurityAuditMetricsExcludeOnlyConfirmedPolicyBlocks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision *securityaudit.Decision
		code     string
		want     bool
	}{
		{"prompt", &securityaudit.Decision{Kind: securityaudit.DecisionBlock, ErrorCode: securityaudit.ErrorCodeBlocked, Prompt: &securityaudit.PromptDecision{Kind: securityaudit.DecisionBlock}}, securityaudit.ErrorCodeBlocked, true},
		{"keyword", &securityaudit.Decision{Kind: securityaudit.DecisionBlock, ErrorCode: "content_policy_violation", Legacy: &securityaudit.LegacyDecision{Blocked: true, Flagged: true, Action: service.ContentModerationActionKeywordBlock}}, "content_policy_violation", true},
		{"provider-403-same-code", nil, securityaudit.ErrorCodeBlocked, false},
		{"technical-failure", &securityaudit.Decision{Kind: securityaudit.DecisionUnavailable, ErrorCode: securityaudit.ErrorCodeUnavailable}, securityaudit.ErrorCodeUnavailable, false},
		{"legacy-failure", &securityaudit.Decision{Kind: securityaudit.DecisionBlock, ErrorCode: "content_policy_violation", Legacy: &securityaudit.LegacyDecision{Blocked: true, Action: service.ContentModerationActionError}}, "content_policy_violation", false},
		{"exempt", &securityaudit.Decision{Kind: securityaudit.DecisionFlag, AllowNextStage: true, Prompt: &securityaudit.PromptDecision{Kind: securityaudit.DecisionFlag}}, securityaudit.ErrorCodeBlocked, false},
		{"different-error", &securityaudit.Decision{Kind: securityaudit.DecisionBlock, ErrorCode: securityaudit.ErrorCodeBlocked, Prompt: &securityaudit.PromptDecision{Kind: securityaudit.DecisionBlock}}, "upstream_error", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			entry := &service.OpsInsertErrorLogInput{StatusCode: http.StatusForbidden, ErrorType: "permission_error", ErrorOwner: "provider", ErrorPhase: "upstream", ErrorSource: "upstream_http"}
			markSecurityAuditMetricBlock(c, tc.decision, "http")
			applySecurityAuditOpsClassification(c, entry, tc.code, 0)
			require.Equal(t, tc.want, entry.IsBusinessLimited)
			if tc.want {
				require.Equal(t, "security_audit_block", entry.ErrorType)
				require.Equal(t, "security_audit", entry.ErrorSource)
				require.Equal(t, "client", entry.ErrorOwner)
			} else {
				require.Equal(t, "upstream_http", entry.ErrorSource)
			}
		})
	}
}

func TestSecurityAuditMetricsActualHTTPRenderers(t *testing.T) {
	openAI := &OpenAIGatewayHandler{}
	gateway := &GatewayHandler{}
	for _, renderer := range []struct {
		name   string
		render func(*gin.Context, *securityaudit.Decision)
	}{
		{"openai", openAI.openAISecurityAuditError},
		{"openai-anthropic", openAI.anthropicSecurityAuditError},
		{"gateway-openai", gateway.openAISecurityAuditError},
		{"gateway-responses", gateway.responsesSecurityAuditError},
		{"gateway-anthropic", gateway.anthropicSecurityAuditError},
		{"google", googleSecurityAuditError},
	} {
		for _, legacy := range []bool{false, true} {
			name := renderer.name + "/prompt"
			if legacy {
				name = renderer.name + "/legacy"
			}
			t.Run(name, func(t *testing.T) {
				setupOpsErrorLogTestQueue(t, 2)
				decision := &securityaudit.Decision{Kind: securityaudit.DecisionBlock, HTTPStatus: http.StatusForbidden,
					ErrorCode: securityaudit.ErrorCodeBlocked, ClientMessage: "local policy rejected",
					Prompt: &securityaudit.PromptDecision{Kind: securityaudit.DecisionBlock}}
				if legacy {
					decision.ErrorCode = "content_policy_violation"
					decision.Prompt = nil
					decision.Legacy = &securityaudit.LegacyDecision{Blocked: true,
						Action: service.ContentModerationActionKeywordBlock, Message: "local policy rejected"}
				}
				ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
				router := gin.New()
				router.Use(OpsErrorLoggerMiddleware(ops))
				router.POST("/v1/responses", func(c *gin.Context) {
					markSecurityAuditMetricBlock(c, decision, "http")
					renderer.render(c, decision)
				})
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
				require.Equal(t, http.StatusForbidden, recorder.Code)
				require.Equal(t, int64(1), OpsErrorLogQueueLength())
				job := <-opsErrorLogQueue
				require.Equal(t, "security_audit_block", job.entry.ErrorType)
				require.Equal(t, "security_audit", job.entry.ErrorSource)
				require.True(t, job.entry.IsBusinessLimited)
				if legacy {
					var body struct {
						Error map[string]any `json:"error"`
					}
					require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
					switch renderer.name {
					case "google":
						require.Equal(t, float64(http.StatusForbidden), body.Error["code"])
					case "gateway-responses":
						require.Equal(t, "content_policy_violation", body.Error["code"])
					default:
						require.Equal(t, "content_policy_violation", body.Error["type"])
						require.NotContains(t, body.Error, "code", "legacy wire response remains unchanged")
					}
				}
			})
		}
	}
}

func TestSecurityAuditMetricsLegacyTypeRequiresLocalMarkerAndNoOtherCode(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		marker     bool
		want       bool
	}{
		{"local-type-only", "", true, true},
		{"provider-type-only", "", false, false},
		{"different-explicit-code", "provider_failure", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if tc.marker {
				markSecurityAuditMetricBlock(c, &securityaudit.Decision{Kind: securityaudit.DecisionBlock,
					ErrorCode: "content_policy_violation", Legacy: &securityaudit.LegacyDecision{
						Blocked: true, Action: service.ContentModerationActionBlock}}, "http")
			}
			entry := &service.OpsInsertErrorLogInput{StatusCode: http.StatusForbidden, ErrorType: "api_error"}
			applySecurityAuditOpsClassification(c, entry, tc.code, 0, "content_policy_violation")
			require.Equal(t, tc.want, entry.IsBusinessLimited)
		})
	}
}

func TestSecurityAuditMetricsWebSocketAuditRejectionLogsCurrentTurn(t *testing.T) {
	for _, tc := range []struct {
		name          string
		turn          int
		previousError bool
	}{
		{"first-turn", 1, false},
		{"success-then-rejection", 2, false},
		{"provider-failure-then-rejection", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 3)
			ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.GET("/v1/responses", func(c *gin.Context) {
				setOpenAIClientTransportWS(c)
				if tc.previousError {
					service.BeginOpsStreamTurn(c, 1)
					service.MarkOpsStreamFailure(c, "upstream_error", securityaudit.ErrorCodeBlocked,
						"provider rejected earlier turn", http.StatusForbidden)
				}
				if tc.turn > 1 {
					c.Set(securityAuditWSTurnContextKey, tc.turn)
					service.BeginOpsStreamTurn(c, tc.turn)
				}
				markSecurityAuditMetricBlock(c, &securityaudit.Decision{Kind: securityaudit.DecisionBlock,
					ErrorCode: securityaudit.ErrorCodeBlocked, HTTPStatus: http.StatusForbidden,
					ClientMessage: "local policy rejected", Prompt: &securityaudit.PromptDecision{Kind: securityaudit.DecisionBlock}},
					map[bool]string{false: "first_turn", true: "subsequent_turn"}[tc.turn > 1])
				c.Status(http.StatusSwitchingProtocols)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/responses", nil))
			require.Equal(t, http.StatusSwitchingProtocols, recorder.Code)
			if tc.previousError {
				require.Equal(t, int64(2), OpsErrorLogQueueLength())
				prior := <-opsErrorLogQueue
				require.Equal(t, "upstream_error", prior.entry.ErrorType)
				require.Equal(t, "provider", prior.entry.ErrorOwner)
				require.False(t, prior.entry.IsBusinessLimited)
			} else {
				require.Equal(t, int64(1), OpsErrorLogQueueLength())
			}
			local := <-opsErrorLogQueue
			require.Equal(t, http.StatusForbidden, local.entry.StatusCode)
			require.Equal(t, "security_audit_block", local.entry.ErrorType)
			require.Equal(t, "security_audit", local.entry.ErrorSource)
			require.True(t, local.entry.IsBusinessLimited)
		})
	}
}

func TestSecurityAuditMetricsDoNotHideEarlierWSTurnFailure(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(securityAuditWSTurnContextKey, 2)
	d := &securityaudit.Decision{Kind: securityaudit.DecisionBlock, ErrorCode: securityaudit.ErrorCodeBlocked, Prompt: &securityaudit.PromptDecision{Kind: securityaudit.DecisionBlock}}
	markSecurityAuditMetricBlock(c, d, "subsequent_turn")
	for _, turn := range []int{0, 1, 2} {
		entry := &service.OpsInsertErrorLogInput{StatusCode: 403}
		applySecurityAuditOpsClassification(c, entry, securityaudit.ErrorCodeBlocked, turn)
		require.Equal(t, turn == 2, entry.IsBusinessLimited)
	}
}

func TestSecurityAuditMetricsLocalWSTurnClearsOnlyStaleTopLevelAttribution(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(securityAuditWSTurnContextKey, 3)
	decision := &securityaudit.Decision{Kind: securityaudit.DecisionBlock, ErrorCode: securityaudit.ErrorCodeBlocked,
		Prompt: &securityaudit.PromptDecision{Kind: securityaudit.DecisionBlock}}
	markSecurityAuditMetricBlock(c, decision, "subsequent_turn")
	status, message, detail := 401, "earlier credential error", "earlier detail"
	previous := &service.OpsUpstreamErrorEvent{Stage: string(service.GatewayFailureStageAccountAuth), Message: message}
	entry := &service.OpsInsertErrorLogInput{StatusCode: 403, UpstreamStatusCode: &status,
		UpstreamErrorMessage: &message, UpstreamErrorDetail: &detail, UpstreamErrors: []*service.OpsUpstreamErrorEvent{previous}}
	applySecurityAuditOpsClassification(c, entry, securityaudit.ErrorCodeBlocked, 3)
	require.Equal(t, "security_audit", entry.ErrorSource)
	require.Nil(t, entry.UpstreamStatusCode)
	require.Nil(t, entry.UpstreamErrorMessage)
	require.Nil(t, entry.UpstreamErrorDetail)
	require.Equal(t, []*service.OpsUpstreamErrorEvent{previous}, entry.UpstreamErrors)
}

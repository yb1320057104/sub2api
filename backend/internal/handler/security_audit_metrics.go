package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const securityAuditMetricBlockKey = "sub2api.security_audit.metric_block"

type securityAuditMetricBlock struct {
	code string
	turn int
}

// Only an actual local policy decision establishes this marker. A provider's
// HTTP 403 or matching error text cannot exempt an upstream failure from SLA.
func markSecurityAuditMetricBlock(c *gin.Context, decision *securityaudit.Decision, stage string) {
	if c == nil || decision == nil || decision.Kind != securityaudit.DecisionBlock || decision.AllowNextStage {
		return
	}
	blocked := decision.Prompt != nil && decision.Prompt.Kind == securityaudit.DecisionBlock
	if d := decision.Legacy; d != nil && d.Blocked {
		switch d.Action {
		case service.ContentModerationActionBlock, service.ContentModerationActionHashBlock,
			service.ContentModerationActionKeywordBlock:
			blocked = true
		}
	}
	if !blocked {
		return
	}
	marker := securityAuditMetricBlock{code: securityAuditErrorCode(decision)}
	if isSecurityAuditWebSocketStage(stage) {
		marker.turn = 1
		if turn, ok := securityAuditWSTurn(c); ok && turn > 0 {
			marker.turn = turn
		}
	}
	c.Set(securityAuditMetricBlockKey, marker)
	if marker.turn > 0 {
		// The first audit runs before the upstream turn hooks. Set only the
		// turn number here; resetting attempt context would erase earlier
		// provider evidence on an established connection.
		c.Set(service.OpsStreamTurnKey, marker.turn)
		service.MarkOpsStreamErrorValue(c, service.OpsStreamError{
			ErrType: "permission_error", Code: marker.code,
			Message: securityAuditMessage(decision), IntendedStatus: securityAuditStatus(decision),
			RequestScoped: true,
		})
	}
}

func applySecurityAuditOpsClassification(c *gin.Context, entry *service.OpsInsertErrorLogInput, code string, turn int, rawErrorTypes ...string) {
	if c == nil || entry == nil || entry.StatusCode < 400 {
		return
	}
	value, _ := c.Get(securityAuditMetricBlockKey)
	marker, ok := value.(securityAuditMetricBlock)
	if !ok || marker.turn != turn {
		return
	}
	matched := code == marker.code
	// Legacy OpenAI/Anthropic policy errors expose the stable code only in
	// error.type. Match the original type before Ops normalizes it, and only
	// when no explicit code was returned. The local decision marker remains
	// mandatory, so an identical provider error is never exempted.
	if !matched && code == "" && len(rawErrorTypes) > 0 {
		matched = rawErrorTypes[0] == marker.code
	}
	if !matched {
		return
	}
	// Retain the error detail and audit trail, but exclude the local rejection
	// from request SLA and account health. Monitor v2 also excludes this exact
	// persisted type/source pair from its request denominator.
	entry.ErrorType = "security_audit_block"
	entry.ErrorPhase = "request"
	entry.ErrorOwner = "client"
	entry.ErrorSource = "security_audit"
	entry.IsBusinessLimited = true
	entry.UpstreamStatusCode = nil
	entry.UpstreamErrorMessage = nil
	entry.UpstreamErrorDetail = nil
}

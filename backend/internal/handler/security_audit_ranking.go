package handler

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type cyberAuditBlockRecorder interface {
	RecordAuditBlock(context.Context, service.CyberPolicyAuditBlockInput) error
}

const cyberAuditIdentityKey = "sub2api.security_audit.ranking_identity"
const cyberAuditRecordedKey = "sub2api.security_audit.ranking_recorded"

// Passthrough reads the next request and finishes the preceding response on
// separate goroutines. Bind the immutable audit decision to its turn before
// forwarding instead of reading the latest Gin request during billing.
type securityAuditUsageTurns struct {
	mu       sync.Mutex
	contexts map[int]context.Context
}

func (t *securityAuditUsageTurns) capture(turn int, parent context.Context) {
	snapshot := service.CopyCyberPolicyAuditUsage(parent, service.WithoutCyberPolicyAuditUsage(context.Background()))
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.contexts == nil {
		t.contexts = make(map[int]context.Context)
	}
	t.contexts[turn] = snapshot
}

func (t *securityAuditUsageTurns) take(turn int, target context.Context) context.Context {
	t.mu.Lock()
	snapshot := t.contexts[turn]
	delete(t.contexts, turn)
	t.mu.Unlock()
	return service.CopyCyberPolicyAuditUsage(snapshot, service.WithoutCyberPolicyAuditUsage(target))
}

func recordSecurityAuditBlock(c *gin.Context, recorder cyberAuditBlockRecorder, apiKey *service.APIKey, userID int64, model, stage string, decision *securityaudit.Decision) {
	if c == nil || c.Request == nil || recorder == nil || userID <= 0 || decision == nil {
		return
	}
	// A technical failure or an already-banned account is not a new violation.
	contentBlocked := false
	if d := decision.Legacy; d != nil && d.Blocked && d.Flagged {
		switch d.Action {
		case service.ContentModerationActionBlock, service.ContentModerationActionHashBlock, service.ContentModerationActionKeywordBlock:
			contentBlocked = true
		}
	}
	promptBlocked := decision.Prompt != nil && decision.Prompt.Kind == securityaudit.DecisionBlock
	contentCharge := decision.Legacy != nil && decision.Legacy.PolicyAction == "charge_allow"
	promptCharge := decision.Prompt != nil && decision.Prompt.PolicyAction == "charge_allow"
	action := "block"
	if decision.AllowNextStage {
		if !contentCharge && !promptCharge {
			return
		}
		action = "charge_allow"
	} else if decision.Kind != securityaudit.DecisionBlock || (!contentBlocked && !promptBlocked) {
		return
	}
	contentHit := contentBlocked || contentCharge
	promptHit := promptBlocked || promptCharge
	source := "content_audit"
	if promptHit {
		source = "prompt_audit"
		if contentHit {
			source = "combined_audit"
		}
	}
	identity := c.GetString(cyberAuditIdentityKey)
	if identity == "" {
		identity = uuid.NewString()
		c.Set(cyberAuditIdentityKey, identity)
	}
	eventKey := identity + ":http"
	if isSecurityAuditWebSocketStage(stage) {
		turn := 1 // The first frame is audited before the forwarding loop starts.
		if n, ok := securityAuditWSTurn(c); ok && n > 0 {
			turn = n
		}
		eventKey = identity + ":turn:" + strconv.Itoa(turn)
	}
	if c.GetString(cyberAuditRecordedKey) == eventKey {
		return
	}
	var groupID *int64
	if apiKey != nil {
		groupID = cloneSecurityAuditGroupID(apiKey.GroupID)
	}
	input := service.CyberPolicyAuditBlockInput{
		EventKey: eventKey, RequestID: contentModerationRequestID(c.Request.Context()), UserID: userID,
		GroupID: groupID, Model: clientRequestedModel(c, model), Source: source, PolicyAction: action,
	}
	// Only explicit scored-policy decisions receive scores. Historical legacy
	// actions stay unknown rather than inventing a confidence value.
	if d := decision.Legacy; contentHit && d != nil && d.PolicyAction != "" {
		score := d.RiskScore
		input.RiskScore, input.RiskLevel = &score, d.RiskLevel
	}
	if d := decision.Prompt; promptHit && d != nil && d.PolicyAction != "" && (input.RiskScore == nil || d.RiskScore > *input.RiskScore) {
		score := d.RiskScore
		input.RiskScore, input.RiskLevel = &score, d.RiskLevel
	}
	if action == "charge_allow" {
		if usageRecorder, ok := recorder.(service.CyberPolicyAuditUsageRecorder); ok {
			// No DB work before forwarding. The immutable snapshot follows this
			// request/turn only and is consumed after successful ordinary billing.
			c.Request = c.Request.WithContext(service.WithCyberPolicyAuditUsage(c.Request.Context(), input, usageRecorder))
		}
		return
	}
	// This short persistence boundary only runs for denied requests. It survives
	// the caller disconnecting, retains the original denial on database failure,
	// and has no effect on the first-token timer for allowed requests.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
	defer cancel()
	err := recorder.RecordAuditBlock(ctx, input)
	if err != nil {
		slog.Error("security_audit.ranking_record_failed", "user_id", userID, "source", source)
		return
	}
	c.Set(cyberAuditRecordedKey, eventKey)
}

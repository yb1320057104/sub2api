package handler

import (
	"context"
	"log/slog"
	"strconv"
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

func recordSecurityAuditBlock(c *gin.Context, recorder cyberAuditBlockRecorder, apiKey *service.APIKey, userID int64, model, stage string, decision *securityaudit.Decision) {
	if c == nil || c.Request == nil || recorder == nil || userID <= 0 || decision == nil ||
		decision.Kind != securityaudit.DecisionBlock || decision.AllowNextStage {
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
	if !contentBlocked && !promptBlocked {
		return
	}
	source := "content_audit"
	if promptBlocked {
		source = "prompt_audit"
		if contentBlocked {
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
	// This short persistence boundary only runs for denied requests. It survives
	// the caller disconnecting, retains the original denial on database failure,
	// and has no effect on the first-token timer for allowed requests.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
	defer cancel()
	err := recorder.RecordAuditBlock(ctx, service.CyberPolicyAuditBlockInput{
		EventKey: eventKey, RequestID: contentModerationRequestID(c.Request.Context()), UserID: userID,
		GroupID: groupID, Model: clientRequestedModel(c, model), Source: source,
	})
	if err != nil {
		slog.Error("security_audit.ranking_record_failed", "user_id", userID, "source", source)
		return
	}
	c.Set(cyberAuditRecordedKey, eventKey)
}

//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCyberPolicyUsageCompletionGatePreservesOriginalBill(t *testing.T) {
	for _, protocol := range []string{"gateway", "openai"} {
		for _, tc := range []struct {
			name                                                     string
			succeeded, disconnected, wsFailed, simple, billingFailed bool
			expected                                                 int
		}{
			{name: "success", succeeded: true, expected: 1},
			{name: "failed_partial"},
			{name: "client_disconnected", succeeded: true, disconnected: true},
			{name: "ws_failed_terminal", succeeded: true, wsFailed: true},
			{name: "simple_unbilled", succeeded: true, simple: true},
			{name: "billing_failed", succeeded: true, billingFailed: true},
		} {
			if protocol == "gateway" && tc.wsFailed {
				continue
			}
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				group := int64(9)
				recorder := &auditUsageRecorderStub{}
				writer := &auditUsageWriterStub{inserted: true}
				billing := &openAIRecordUsageBillingRepoStub{}
				if tc.billingFailed {
					billing.err = errors.New("simulated billing failure")
				}
				ctx := WithCyberPolicyAuditUsage(context.Background(), CyberPolicyAuditBlockInput{EventKey: "completion:http", UserID: 7, GroupID: &group, PolicyAction: "charge_allow"}, recorder)
				key := &APIKey{ID: 2, GroupID: &group}
				user := &User{ID: 7}
				account := &Account{ID: 3}
				var err error
				if protocol == "gateway" {
					svc := newGatewayRecordUsageServiceWithBillingRepoForTest(writer, billing, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
					if tc.simple {
						svc.cfg.RunMode = config.RunModeSimple
					}
					err = svc.RecordUsage(ctx, &RecordUsageInput{AuditRequestSucceeded: tc.succeeded, APIKey: key, User: user, Account: account,
						Result: &ForwardResult{RequestID: "completion", Model: "claude-sonnet-4", ClientDisconnect: tc.disconnected, Usage: ClaudeUsage{InputTokens: 10, OutputTokens: 6}}})
				} else {
					svc := newOpenAIRecordUsageServiceForTest(writer, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
					svc.usageBillingRepo = billing
					if tc.simple {
						svc.cfg.RunMode = config.RunModeSimple
					}
					result := &OpenAIForwardResult{RequestID: "completion", Model: "gpt-5.1", ClientDisconnect: tc.disconnected, Usage: OpenAIUsage{InputTokens: 10, OutputTokens: 6}}
					if tc.wsFailed {
						result.OpenAIWSMode = true
						result.UpstreamTerminalEvent = "response.failed"
					}
					err = svc.RecordUsage(ctx, &OpenAIRecordUsageInput{AuditRequestSucceeded: tc.succeeded, APIKey: key, User: user, Account: account, Result: result})
				}
				if tc.billingFailed {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				require.Len(t, recorder.usage, tc.expected)
				require.Equal(t, 1, writer.creates+writer.bestEfforts, "failed or interrupted requests still retain their original usage")
				if !tc.simple {
					require.Equal(t, 1, billing.calls, "original partial usage billing remains active")
				}
				if tc.expected > 0 {
					require.Positive(t, recorder.usage[0].ActualCost)
					require.NotEqual(t, RequestTypeCyberBlocked, recorder.usage[0].RequestType)
				}
			})
		}
	}
}

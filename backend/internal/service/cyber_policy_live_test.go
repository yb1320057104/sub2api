package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestCyberPolicyLiveOnlyExplicitUpstreamEndQualifies(t *testing.T) {
	for _, tc := range []struct {
		name      string
		frame     liveTestFrame
		completed bool
	}{
		{"closed_event", liveTestFrame{messageType: coderws.MessageText, payload: []byte(`{"type":"session.closed"}`)}, true},
		{"ended_event", liveTestFrame{messageType: coderws.MessageText, payload: []byte(`{"type":"session.ended"}`)}, true},
		{"normal_socket_close", liveTestFrame{err: coderws.CloseError{Code: coderws.StatusNormalClosure}}, false},
		{"cancelled", liveTestFrame{err: context.Canceled}, false},
		{"read_failure", liveTestFrame{err: context.DeadlineExceeded}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newLiveTestFrameConn()
			defer upstream.Close()
			upstream.reads <- tc.frame
			record := &LiveCallRecord{CallHash: "audit-live", Controller: LiveControllerObserver, ExpiresAt: time.Now().Add(time.Minute)}
			svc := &OpenAIGatewayService{cache: &liveTestStore{record: record}}
			err := svc.runLiveObserverConnection(record, upstream)
			if tc.completed {
				require.ErrorIs(t, err, errLiveAuditCompleted)
			} else {
				require.NotErrorIs(t, err, errLiveAuditCompleted)
			}
		})
	}
}

func TestCyberPolicyLiveCompletionKeepsZeroPriceAndCountsOnce(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "interrupted"
		if completed {
			name = "completed"
		}
		t.Run(name, func(t *testing.T) {
			group := int64(9)
			record := &LiveCallRecord{CallID: "live", CallHash: "audit-live", UserID: 7, GroupID: group, CreatedAt: time.Now(), Controller: LiveControllerPending,
				AuditAction: &CyberPolicyAuditBlockInput{EventKey: "server-live:http", UserID: 7, GroupID: &group, PolicyAction: "charge_allow"}}
			writer := &auditUsageWriterStub{inserted: true}
			recorder := &auditUsageRecorderStub{}
			svc := &OpenAIGatewayService{cache: &liveTestStore{record: record}, usageLogRepo: writer, cyberAuditUsageRecorder: recorder}
			svc.finalizeLiveCallWithAudit(record, completed)
			svc.finalizeLiveCallWithAudit(record, true)
			if completed {
				require.Len(t, recorder.usage, 1)
				require.Equal(t, RequestTypeLive, recorder.usage[0].RequestType)
				require.Zero(t, recorder.usage[0].ActualCost)
				require.Zero(t, recorder.usage[0].TotalCost)
				require.Equal(t, "audit-live", recorder.usage[0].RequestID)
			} else {
				require.Empty(t, recorder.usage)
				require.Equal(t, 1, writer.bestEfforts)
			}
			public, err := json.Marshal(record)
			require.NoError(t, err)
			require.NotContains(t, string(public), "server-live")
		})
	}
}

package service

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestGatewayRoleDoesNotStartGlobalMonitorOrSchedule(t *testing.T) {
	cfg := &config.Config{Runtime: config.RuntimeConfig{Role: config.RuntimeRoleGateway}}
	// Empty dependencies would fail if either provider performed its startup DB scan.
	monitor := ProvideChannelMonitorRunner(&ChannelMonitorService{}, &SettingService{}, nil, cfg)
	require.False(t, monitor.started)
	monitor.Stop()
	runner := ProvideScheduledTestRunnerService(nil, nil, nil, nil, cfg, &QualityJudgeService{}, nil, &PelicanGroupTestService{}, &ChannelMonitorV2Service{})
	require.Nil(t, runner.cron)
	runner.Stop()
	expiry := ProvideAccountExpiryService(nil, cfg)
	expiry.Stop()
}

type gatewayAccountOpsRepo struct {
	AccountOpsRepository
	recorded chan AccountOpsEvent
}

func (r *gatewayAccountOpsRepo) SuppressDisabled(context.Context, AccountOpsConfig) error { return nil }
func (r *gatewayAccountOpsRepo) Record(_ context.Context, event AccountOpsEvent) error {
	r.recorded <- event
	return nil
}

func TestGatewayRoleStillPersistsRequestObservations(t *testing.T) {
	c := defaultAccountOpsConfig()
	c.Enabled = true
	c.Recipient = "ops@example.com"
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	repo := &gatewayAccountOpsRepo{recorded: make(chan AccountOpsEvent, 1)}
	cfg := &config.Config{Runtime: config.RuntimeConfig{Role: config.RuntimeRoleGateway}}
	svc := ProvideAccountOpsService(&accountOpsSettingsStub{raw: string(raw)}, repo, nil, nil, nil, cfg, nil, nil, nil, nil, nil)
	defer svc.Stop()
	_, err = svc.GetConfig(context.Background())
	require.NoError(t, err)
	headers := http.Header{}
	headers.Set("x-codex-secondary-used-percent", "100")
	headers.Set("x-codex-secondary-window-minutes", "10080")
	svc.Observe(&Account{ID: 7, Platform: PlatformOpenAI}, 429, headers, nil)
	select {
	case event := <-repo.recorded:
		require.Equal(t, int64(7), event.AccountID)
		require.Equal(t, "weekly_quota", event.Kind)
	case <-time.After(time.Second):
		t.Fatal("gateway must drain its local account event queue")
	}
}

func TestGatewayRoleStillDrainsOllamaRequestProbeWakeups(t *testing.T) {
	cfg := &config.Config{Runtime: config.RuntimeConfig{Role: config.RuntimeRoleGateway}}
	svc := ProvideOllamaCloudUsageService(nil, nil, nil, nil, cfg, nil, nil)
	defer svc.Stop()
	svc.probeWake <- struct{}{}
	require.Eventually(t, func() bool { return len(svc.probeWake) == 0 }, time.Second, time.Millisecond)
}

func TestOllamaGlobalScanWaitsForActivationAndCannotStartAfterStop(t *testing.T) {
	oldGate := pendingBackgroundStarts
	gate := newBackgroundStartGate(filepath.Join(t.TempDir(), "activation"))
	pendingBackgroundStarts = gate
	defer func() { gate.stop(); pendingBackgroundStarts = oldGate }()
	cfg := &config.Config{}
	svc := ProvideOllamaCloudUsageService(nil, nil, nil, nil, cfg, nil, nil)
	defer svc.Stop()
	svc.probeWake <- struct{}{}
	require.Eventually(t, func() bool { return len(svc.probeWake) == 0 }, time.Second, time.Millisecond)
	svc.mu.Lock()
	require.True(t, svc.started)
	require.False(t, svc.backgroundStarted)
	svc.mu.Unlock()
	gate.mu.Lock()
	gate.activateLocked()
	gate.mu.Unlock()
	svc.mu.Lock()
	require.True(t, svc.backgroundStarted)
	svc.mu.Unlock()
	svc.Stop()
	svc.start(true)
	require.ErrorIs(t, svc.parentCtx.Err(), context.Canceled)
	other := NewOllamaCloudUsageService(nil, nil, nil, nil, false)
	other.start(false)
	other.Stop()
	other.start(true)
	require.False(t, other.backgroundStarted, "a delayed activation callback must not resurrect a stopped scanner")
}

type activationAuditRepository struct {
	AuditLogRepository
	written chan int
}

func (r *activationAuditRepository) BatchInsert(_ context.Context, rows []*AuditLog) (int64, error) {
	r.written <- len(rows)
	return int64(len(rows)), nil
}

func TestAuditRetentionWaitsForActivationWhileWriterRemainsLocal(t *testing.T) {
	oldGate := pendingBackgroundStarts
	gate := newBackgroundStartGate(filepath.Join(t.TempDir(), "activation"))
	pendingBackgroundStarts = gate
	defer func() { gate.stop(); pendingBackgroundStarts = oldGate }()
	repo := &activationAuditRepository{written: make(chan int, 1)}
	svc := ProvideAuditLogService(repo, nil, &config.Config{})
	defer svc.Stop()
	gate.mu.Lock()
	pending := append([]pendingBackgroundStart(nil), gate.pending...)
	gate.mu.Unlock()
	require.Len(t, pending, 1)
	require.Equal(t, "AuditLogRetention", pending[0].name)
	svc.Record(&AuditLog{Action: "activation-test"})
	svc.Stop()
	select {
	case count := <-repo.written:
		require.Equal(t, 1, count)
	default:
		t.Fatal("API audit writer must flush while global retention awaits activation")
	}
}

func TestCodexHarvesterConstructorWaitsForActivation(t *testing.T) {
	oldGate := pendingBackgroundStarts
	gate := newBackgroundStartGate(filepath.Join(t.TempDir(), "activation"))
	pendingBackgroundStarts = gate
	defer func() { gate.stop(); pendingBackgroundStarts = oldGate }()
	cfg := &config.Config{}
	svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	defer svc.StopOpenAICodexTicketHarvester()
	require.Nil(t, svc.openaiCodexTicketDone)
	gate.mu.Lock()
	pending := append([]pendingBackgroundStart(nil), gate.pending...)
	gate.mu.Unlock()
	require.Len(t, pending, 1)
	require.Equal(t, "OpenAICodexTicketHarvester", pending[0].name)
	gate.mu.Lock()
	gate.activateLocked()
	gate.mu.Unlock()
	require.NotNil(t, svc.openaiCodexTicketDone)
	svc.StopOpenAICodexTicketHarvester()
}

type activationFallbackMetrics struct {
	started chan struct{}
	stopped chan struct{}
}

func (r *activationFallbackMetrics) GetOpenAIAPIKeyFallbackMetric(ctx context.Context, _ string, _, _ time.Time) (*OpenAIAPIKeyFallbackObservation, error) {
	close(r.started)
	<-ctx.Done()
	close(r.stopped)
	return nil, ctx.Err()
}

func TestOpenAIAPIKeyFallbackActivationOwnsEvaluationAndShutdown(t *testing.T) {
	svc := apiKeyFallbackFixture(t, &apiKeyFallbackAccounts{}, &apiKeyFallbackMetrics{})
	svc.sharedJobsEnabled.Store(false)
	reader := &activationFallbackMetrics{started: make(chan struct{}), stopped: make(chan struct{})}
	svc.start(false)
	defer svc.Stop()
	svc.setOpenAIAPIKeyFallbackSLAReader(reader, nil)
	select {
	case <-reader.started:
		t.Fatal("reader construction must not evaluate before writer activation")
	default:
	}
	svc.enableSharedJobs()
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("activation must wake the owned loop without waiting for the ticker")
	}
	svc.Stop()
	select {
	case <-reader.stopped:
	default:
		t.Fatal("shutdown must cancel and join the in-flight evaluation")
	}
	svc.enableSharedJobs()
	svc.start(true)
	require.False(t, svc.sharedJobsEnabled.Load(), "delayed activation must not resurrect a stopped worker")
}

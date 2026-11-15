package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/reauthruntime"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestOpenAIOAuthReauthManagedWorkerNeedsNoEnvironmentToken(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "")
	t.Setenv("DATA_DIR", t.TempDir())
	s := &OpenAIOAuthReauthService{}
	cfg := &config.Config{}
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 4040
	s.configureWorker(cfg, BuildInfo{Version: "2.9.4"})
	defer s.stopWorker()
	require.NotNil(t, s.worker)
	require.Len(t, s.workerToken, 64)
	configured, valid := s.WorkerAuthentication(s.workerToken)
	require.True(t, configured)
	require.True(t, valid)
	_, valid = s.WorkerAuthentication(strings.Repeat("x", 64))
	require.False(t, valid)
	status := s.WorkerStatus()
	require.Equal(t, "managed", status.Mode)
	require.Equal(t, "idle", status.State)
	raw, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(raw), s.workerToken)
	require.Error(t, s.checkWorkerMode(OpenAIOAuthReauthModeEmailOTPURL))
}

func TestOpenAIOAuthReauthPreservesExternalWorker(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", strings.Repeat("a", 32))
	s := &OpenAIOAuthReauthService{}
	s.configureWorker(nil, BuildInfo{Version: "2.9.4"})
	require.Nil(t, s.worker)
	configured, valid := s.WorkerAuthentication(strings.Repeat("a", 32))
	require.True(t, configured)
	require.True(t, valid)
	require.Equal(t, "external_offline", s.WorkerStatus().Reason)
}

func TestOpenAIOAuthReauthWorkerHeartbeatKeepsExternalWorkerAvailable(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", strings.Repeat("a", 32))
	s := &OpenAIOAuthReauthService{}
	s.configureWorker(nil, BuildInfo{Version: "2.9.4"})
	s.MarkWorkerSeen()
	require.Equal(t, "running", s.WorkerStatus().State)
}

func TestOpenAIOAuthReauthShortExplicitTokenDoesNotStartCompetingWorker(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "short")
	s := &OpenAIOAuthReauthService{}
	s.configureWorker(nil, BuildInfo{Version: "2.9.4"})
	require.Nil(t, s.worker)
	configured, valid := s.WorkerAuthentication("short")
	require.False(t, configured)
	require.False(t, valid)
	require.Equal(t, "external_not_configured", s.WorkerStatus().Reason)
}

func TestOpenAIOAuthReauthReplicaUsesActiveWorkerStatusAndNeverStartsRuntime(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "")
	t.Setenv("SUB2API_BACKGROUND_MODE", "active")
	t.Setenv("DATA_DIR", t.TempDir())
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	active := &OpenAIOAuthReauthService{}
	active.configureWorker(nil, BuildInfo{Version: "0.2.19-pk.66"})
	active.configureWorkerStatus(client)
	active.publishWorkerStatus(context.Background())
	raw, err := mr.Get(reauthWorkerStatusKey)
	require.NoError(t, err)
	require.NotContains(t, raw, active.workerToken)
	require.InDelta(t, reauthWorkerStatusTTL.Seconds(), mr.TTL(reauthWorkerStatusKey).Seconds(), 1)

	var snapshot reauthWorkerStatusSnapshot
	require.NoError(t, json.Unmarshal([]byte(raw), &snapshot))
	snapshot.Status = reauthruntime.Status{Mode: "managed", State: "unavailable", Reason: "runtime_download_failed"}
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, client.Set(context.Background(), reauthWorkerStatusKey, encoded, reauthWorkerStatusTTL).Err())

	t.Setenv("SUB2API_BACKGROUND_MODE", "disabled")
	replica := &OpenAIOAuthReauthService{}
	replica.configureWorker(nil, BuildInfo{Version: "0.2.19-pk.66"})
	replica.configureWorkerStatus(client)
	replica.EnsureWorker()
	require.Equal(t, "idle", replica.WorkerStatus().State)
	require.Equal(t, snapshot.Status, replica.OperationalWorkerStatus(context.Background()))

	snapshot.Status = reauthruntime.Status{Mode: "managed", State: "idle"}
	encoded, err = json.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, client.Set(context.Background(), reauthWorkerStatusKey, encoded, reauthWorkerStatusTTL).Err())
	require.NoError(t, replica.checkWorkerMode(OpenAIOAuthReauthModePasswordTOTP), "an idle active runtime still accepts queued work")
	require.Equal(t, "idle", replica.WorkerStatus().State, "checking a mode must not start a replica runtime")
}

func TestOpenAIOAuthReauthSharedWorkerStatusExpiresAndRejectsUnsafeMetadata(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "")
	t.Setenv("SUB2API_BACKGROUND_MODE", "disabled")
	t.Setenv("DATA_DIR", t.TempDir())
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	replica := &OpenAIOAuthReauthService{}
	replica.configureWorker(nil, BuildInfo{Version: "0.2.19-pk.66"})
	replica.configureWorkerStatus(client)
	require.Equal(t, "background_offline", replica.OperationalWorkerStatus(context.Background()).Reason)
	write := func(snapshot reauthWorkerStatusSnapshot) {
		t.Helper()
		raw, err := json.Marshal(snapshot)
		require.NoError(t, err)
		require.NoError(t, client.Set(context.Background(), reauthWorkerStatusKey, raw, reauthWorkerStatusTTL).Err())
	}
	snapshot := reauthWorkerStatusSnapshot{Owner: "active", ObservedAt: time.Now().Unix(), Status: reauthruntime.Status{Mode: "managed", State: "running"}}
	write(snapshot)
	require.Equal(t, "running", replica.OperationalWorkerStatus(context.Background()).State)
	mr.FastForward(reauthWorkerStatusTTL + time.Second)
	require.Equal(t, "background_offline", replica.OperationalWorkerStatus(context.Background()).Reason)
	snapshot.ObservedAt = time.Now().Add(-2 * reauthWorkerStatusTTL).Unix()
	write(snapshot)
	require.Equal(t, "background_offline", replica.OperationalWorkerStatus(context.Background()).Reason)
	snapshot.ObservedAt = time.Now().Unix()
	snapshot.Status.Reason = "secret=unsafe-upstream-value"
	write(snapshot)
	status := replica.OperationalWorkerStatus(context.Background())
	require.Equal(t, "status_unavailable", status.Reason)
	require.NotContains(t, status.Reason, "secret")
	mr.Close()
	require.Equal(t, "status_unavailable", replica.OperationalWorkerStatus(context.Background()).Reason)
}

func TestOpenAIOAuthReauthReplicaUsesExternalWorkerHeartbeatFromActiveProcess(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", strings.Repeat("a", 32))
	t.Setenv("SUB2API_BACKGROUND_MODE", "active")
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	active := &OpenAIOAuthReauthService{}
	active.configureWorker(nil, BuildInfo{Version: "0.2.19-pk.66"})
	active.configureWorkerStatus(client)
	active.MarkWorkerSeen()
	active.publishWorkerStatus(context.Background())

	t.Setenv("SUB2API_BACKGROUND_MODE", "disabled")
	replica := &OpenAIOAuthReauthService{}
	replica.configureWorker(nil, BuildInfo{Version: "0.2.19-pk.66"})
	replica.configureWorkerStatus(client)
	require.Equal(t, "external_offline", replica.WorkerStatus().Reason)
	require.Equal(t, reauthruntime.Status{Mode: "external", State: "running"}, replica.OperationalWorkerStatus(context.Background()))
	mr.FastForward(reauthWorkerStatusTTL + time.Second)
	require.Equal(t, reauthruntime.Status{Mode: "external", State: "unavailable", Reason: "background_offline"}, replica.OperationalWorkerStatus(context.Background()))
}

func TestOpenAIOAuthReauthWorkerStatusOwnershipSurvivesOldShutdown(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "")
	t.Setenv("SUB2API_BACKGROUND_MODE", "active")
	t.Setenv("DATA_DIR", t.TempDir())
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	newService := func() *OpenAIOAuthReauthService {
		svc := &OpenAIOAuthReauthService{}
		svc.configureWorker(nil, BuildInfo{Version: "0.2.19-pk.66"})
		svc.configureWorkerStatus(client)
		return svc
	}
	old, current := newService(), newService()
	old.publishWorkerStatus(context.Background())
	oldRaw, err := mr.Get(reauthWorkerStatusKey)
	require.NoError(t, err)
	current.publishWorkerStatus(context.Background())
	stillOld, err := mr.Get(reauthWorkerStatusKey)
	require.NoError(t, err)
	require.Equal(t, oldRaw, stillOld, "a second process cannot replace a live owner")
	mr.FastForward(reauthWorkerStatusTTL + time.Second)
	current.publishWorkerStatus(context.Background())
	currentRaw, err := mr.Get(reauthWorkerStatusKey)
	require.NoError(t, err)
	require.NotEqual(t, oldRaw, currentRaw)
	old.publishWorkerStatus(context.Background())
	old.clearWorkerStatus()
	stillCurrent, err := mr.Get(reauthWorkerStatusKey)
	require.NoError(t, err)
	require.Equal(t, currentRaw, stillCurrent)
	current.clearWorkerStatus()
	require.False(t, mr.Exists(reauthWorkerStatusKey))
}

func TestOpenAIOAuthReauthWorkerStatusReporterStopsWithBackgroundLifecycle(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "")
	t.Setenv("SUB2API_BACKGROUND_MODE", "active")
	t.Setenv("DATA_DIR", t.TempDir())
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	svc := &OpenAIOAuthReauthService{}
	svc.configureWorker(nil, BuildInfo{Version: "0.2.19-pk.66"})
	svc.configureWorkerStatus(client)
	ctx, cancel := context.WithCancel(context.Background())
	done := svc.startWorkerStatusReporting(ctx)
	require.Eventually(t, func() bool { return mr.Exists(reauthWorkerStatusKey) }, time.Second, time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker status reporter did not stop")
	}
	require.False(t, mr.Exists(reauthWorkerStatusKey))
}

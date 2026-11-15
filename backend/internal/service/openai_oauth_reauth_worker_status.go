package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/reauthruntime"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	reauthWorkerStatusKey      = "openai:reauth:worker-status:v1"
	reauthWorkerStatusInterval = 15 * time.Second
	reauthWorkerStatusTTL      = time.Minute
	reauthWorkerStatusTimeout  = 500 * time.Millisecond
)

// This is diagnostic metadata only. It must never contain login material,
// worker authentication tokens, host names or raw preparation errors.
type reauthWorkerStatusSnapshot struct {
	Owner      string               `json:"owner"`
	ObservedAt int64                `json:"observed_at"`
	Status     reauthruntime.Status `json:"status"`
}

type reauthWorkerStatusReporter struct {
	client *redis.Client
	owner  string
}

func reauthBackgroundDisabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("SUB2API_BACKGROUND_MODE")), "disabled")
}

func (s *OpenAIOAuthReauthService) configureWorkerStatus(client *redis.Client) {
	if s != nil && client != nil {
		s.workerStatusReporter = &reauthWorkerStatusReporter{client: client, owner: uuid.NewString()}
	}
}

// Only the process executing background work publishes runtime state.
// A replica must not start a second runtime while displaying or queuing work.
func (s *OpenAIOAuthReauthService) OperationalWorkerStatus(ctx context.Context) reauthruntime.Status {
	if s == nil || !reauthBackgroundDisabled() {
		return s.WorkerStatus()
	}
	unavailable := reauthruntime.Status{Mode: s.WorkerStatus().Mode, State: "unavailable", Reason: "status_unavailable"}
	if s.workerStatusReporter == nil {
		return unavailable
	}
	bounded, cancel := context.WithTimeout(ctx, reauthWorkerStatusTimeout)
	defer cancel()
	raw, err := s.workerStatusReporter.client.Get(bounded, reauthWorkerStatusKey).Bytes()
	if errors.Is(err, redis.Nil) {
		unavailable.Reason = "background_offline"
		return unavailable
	}
	if err != nil || len(raw) > 1024 {
		return unavailable
	}
	var snapshot reauthWorkerStatusSnapshot
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || snapshot.Owner == "" || !validReauthWorkerStatus(snapshot.Status) {
		return unavailable
	}
	observed := time.Unix(snapshot.ObservedAt, 0)
	if time.Since(observed) > reauthWorkerStatusTTL || time.Until(observed) > reauthWorkerStatusTTL {
		unavailable.Reason = "background_offline"
		return unavailable
	}
	return snapshot.Status
}

func validReauthWorkerStatus(status reauthruntime.Status) bool {
	if status.Mode != "managed" && status.Mode != "external" {
		return false
	}
	switch status.State {
	case "idle", "preparing", "running", "unavailable", "stopped":
	default:
		return false
	}
	switch status.Reason {
	case "", "unsupported_platform", "release_required", "runtime_install_failed",
		"runtime_not_configured", "runtime_release_invalid", "runtime_checksum_mismatch",
		"runtime_download_failed", "runtime_cache_invalid", "runtime_archive_invalid", "runtime_self_check_failed",
		"worker_start_failed", "worker_exited", "external_not_configured", "external_offline", "api_unreachable":
		return true
	default:
		return false
	}
}

const publishReauthWorkerStatusScript = `
local current = redis.call('GET', KEYS[1])
if current then
  local ok, snapshot = pcall(cjson.decode, current)
  if not ok or snapshot.owner ~= ARGV[1] then return 0 end
end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return 1`

const clearReauthWorkerStatusScript = `
local current = redis.call('GET', KEYS[1])
if not current then return 0 end
local ok, snapshot = pcall(cjson.decode, current)
if ok and snapshot.owner == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`

func (s *OpenAIOAuthReauthService) publishWorkerStatus(ctx context.Context) {
	if s == nil || s.workerStatusReporter == nil || reauthBackgroundDisabled() {
		return
	}
	status := s.WorkerStatus()
	if !validReauthWorkerStatus(status) {
		status = reauthruntime.Status{Mode: "managed", State: "unavailable", Reason: "worker_start_failed"}
	}
	snapshot := reauthWorkerStatusSnapshot{Owner: s.workerStatusReporter.owner, ObservedAt: time.Now().Unix(), Status: status}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, reauthWorkerStatusTimeout)
	defer cancel()
	// Observability failures do not change authorization or account state.
	_ = s.workerStatusReporter.client.Eval(bounded, publishReauthWorkerStatusScript, []string{reauthWorkerStatusKey}, snapshot.Owner, raw, reauthWorkerStatusTTL.Milliseconds()).Err()
}

func (s *OpenAIOAuthReauthService) clearWorkerStatus() {
	if s == nil || s.workerStatusReporter == nil {
		return
	}
	bounded, cancel := context.WithTimeout(context.Background(), reauthWorkerStatusTimeout)
	defer cancel()
	_ = s.workerStatusReporter.client.Eval(bounded, clearReauthWorkerStatusScript, []string{reauthWorkerStatusKey}, s.workerStatusReporter.owner).Err()
}

func (s *OpenAIOAuthReauthService) startWorkerStatusReporting(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if s == nil || s.workerStatusReporter == nil || reauthBackgroundDisabled() {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		defer s.clearWorkerStatus()
		ticker := time.NewTicker(reauthWorkerStatusInterval)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			s.publishWorkerStatus(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type systemHandlerUpdateServiceStub struct {
	performErr            error
	updateInfo            *service.UpdateInfo
	checkErr              error
	checkForces           []bool
	performCall           int
	performCtxErr         error
	performHasDeadline    bool
	rollbackCall          int
	rollbackToCall        int
	rollbackToCtxErr      error
	rollbackToHasDeadline bool
	rollbackToVersions    []string
	rollbackToErr         error
	rollbackVersions      []service.RollbackVersion
	rollbackVersionsErr   error
	rollbackVersionsCall  int
	restartReservation    service.SystemRestartReservation
	prepareRestartErr     error
	prepareRestartCall    int
	prepareRestartCtx     context.Context
}

func (s *systemHandlerUpdateServiceStub) CheckUpdate(_ context.Context, force bool) (*service.UpdateInfo, error) {
	s.checkForces = append(s.checkForces, force)
	return s.updateInfo, s.checkErr
}

func (s *systemHandlerUpdateServiceStub) PerformUpdate(ctx context.Context) error {
	s.performCall++
	s.performCtxErr = ctx.Err()
	_, s.performHasDeadline = ctx.Deadline()
	return s.performErr
}

func (s *systemHandlerUpdateServiceStub) Rollback() error {
	s.rollbackCall++
	return nil
}

func (s *systemHandlerUpdateServiceStub) ListRollbackVersions(context.Context) ([]service.RollbackVersion, error) {
	s.rollbackVersionsCall++
	return s.rollbackVersions, s.rollbackVersionsErr
}

func (s *systemHandlerUpdateServiceStub) RollbackToVersion(ctx context.Context, version string) error {
	s.rollbackToCall++
	s.rollbackToCtxErr = ctx.Err()
	_, s.rollbackToHasDeadline = ctx.Deadline()
	s.rollbackToVersions = append(s.rollbackToVersions, version)
	return s.rollbackToErr
}

func (s *systemHandlerUpdateServiceStub) PrepareRestart(ctx context.Context) (service.SystemRestartReservation, error) {
	s.prepareRestartCall++
	s.prepareRestartCtx = ctx
	return s.restartReservation, s.prepareRestartErr
}

type systemRestartReservationStub struct {
	execute      func(context.Context, func() error) error
	executeCalls int
	cancelCalls  int
}

func (s *systemRestartReservationStub) Execute(ctx context.Context, signal func() error) error {
	s.executeCalls++
	if s.execute != nil {
		return s.execute(ctx, signal)
	}
	return signal()
}

func (s *systemRestartReservationStub) Cancel() { s.cancelCalls++ }

type systemUpdateResponseEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Message         string `json:"message"`
		AlreadyUpToDate bool   `json:"already_up_to_date"`
		CurrentVersion  string `json:"current_version"`
		LatestVersion   string `json:"latest_version"`
		OperationID     string `json:"operation_id"`
	} `json:"data"`
}

type systemUpdateErrorEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newSystemHandlerTestRouter(t *testing.T, updateSvc *systemHandlerUpdateServiceStub, repo *memoryIdempotencyRepoStub) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() {
		service.SetDefaultIdempotencyCoordinator(nil)
	})

	lockSvc := service.NewSystemOperationLockService(repo, service.IdempotencyConfig{
		ProcessingTimeout:  time.Second,
		SystemOperationTTL: time.Minute,
	})
	handler := NewSystemHandler(updateSvc, lockSvc)

	router := gin.New()
	router.POST("/api/v1/admin/system/update", handler.PerformUpdate)
	router.POST("/api/v1/admin/system/rollback", handler.Rollback)
	router.GET("/api/v1/admin/system/rollback-versions", handler.GetRollbackVersions)
	return router
}

func requireSystemLockStatus(t *testing.T, repo *memoryIdempotencyRepoStub, wantStatus string) {
	t.Helper()
	repo.mu.Lock()
	defer repo.mu.Unlock()

	for _, record := range repo.data {
		if record.Status == wantStatus {
			return
		}
	}
	t.Fatalf("system lock status %q not found in records: %#v", wantStatus, repo.data)
}

func TestSystemHandlerPerformUpdateAlreadyUpToDateReturnsOK(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		performErr: service.ErrNoUpdateAvailable,
		updateInfo: &service.UpdateInfo{
			CurrentVersion: "0.1.132",
			LatestVersion:  "0.1.132",
			HasUpdate:      false,
		},
	}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/update", nil)
	req.Header.Set("Idempotency-Key", "already-up-to-date")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, updateSvc.performCall)
	require.Equal(t, []bool{false}, updateSvc.checkForces)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)

	var body systemUpdateResponseEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Equal(t, "success", body.Message)
	require.Equal(t, "Already up to date", body.Data.Message)
	require.True(t, body.Data.AlreadyUpToDate)
	require.Equal(t, "0.1.132", body.Data.CurrentVersion)
	require.Equal(t, "0.1.132", body.Data.LatestVersion)
	require.NotEmpty(t, body.Data.OperationID)
}

func TestSystemHandlerPerformUpdateFailureStillReturnsInternalError(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		performErr: errors.New("download failed"),
	}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/update", nil)
	req.Header.Set("Idempotency-Key", "real-failure")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, 1, updateSvc.performCall)
	require.Empty(t, updateSvc.checkForces)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusFailedRetryable)

	var body systemUpdateErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, http.StatusInternalServerError, body.Code)
	require.Equal(t, "internal error", body.Message)
}

// TestSystemHandlerPerformUpdateSurvivesClientDisconnect reproduces #4504:
// the browser or a reverse proxy (axios 30s default, nginx proxy_read_timeout
// 60s) aborts the long-running update request and cancels the request
// context. The download must keep running on a detached, bounded context
// instead of dying with "download failed: context canceled".
func TestSystemHandlerPerformUpdateSurvivesClientDisconnect(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/update", nil)
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req = req.WithContext(canceledCtx)
	req.Header.Set("Idempotency-Key", "disconnected-update")
	router.ServeHTTP(rec, req)

	require.Equal(t, 1, updateSvc.performCall)
	require.NoError(t, updateSvc.performCtxErr,
		"update must not observe the canceled request context")
	require.True(t, updateSvc.performHasDeadline,
		"detached update context must still be bounded by a deadline")
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)
}

func TestSystemHandlerRollbackToVersionSurvivesClientDisconnect(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":"0.1.146"}`))
	req.Header.Set("Content-Type", "application/json")
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req = req.WithContext(canceledCtx)
	req.Header.Set("Idempotency-Key", "disconnected-rollback")
	router.ServeHTTP(rec, req)

	require.Equal(t, 1, updateSvc.rollbackToCall)
	require.NoError(t, updateSvc.rollbackToCtxErr,
		"versioned rollback must not observe the canceled request context")
	require.True(t, updateSvc.rollbackToHasDeadline,
		"detached rollback context must still be bounded by a deadline")
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)
}

func TestSystemHandlerRollbackWithoutBodyUsesLegacyBackup(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback", nil)
	req.Header.Set("Idempotency-Key", "legacy-rollback")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, updateSvc.rollbackCall)
	require.Equal(t, 0, updateSvc.rollbackToCall)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)
}

func TestSystemHandlerRollbackWithVersionCallsRollbackToVersion(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":"0.1.146"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "rollback-to-146")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 0, updateSvc.rollbackCall)
	require.Equal(t, 1, updateSvc.rollbackToCall)
	require.Equal(t, []string{"0.1.146"}, updateSvc.rollbackToVersions)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)

	var body systemUpdateResponseEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Equal(t, "Rollback completed. Please restart the service.", body.Data.Message)
}

func TestSystemHandlerRollbackChunkedJSONCallsVersionedRollback(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":"0.1.146-pk.1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = -1
	req.Header.Set("Transfer-Encoding", "chunked")
	req.Header.Set("Idempotency-Key", "chunked-rollback")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 0, updateSvc.rollbackCall)
	require.Equal(t, 1, updateSvc.rollbackToCall)
	require.Equal(t, []string{"0.1.146-pk.1"}, updateSvc.rollbackToVersions)
}

func TestSystemHandlerRollbackChunkedInvalidJSONDoesNotUseLocalBackup(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":`))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = -1
	req.Header.Set("Transfer-Encoding", "chunked")
	req.Header.Set("Idempotency-Key", "chunked-invalid-rollback")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 0, updateSvc.rollbackCall)
	require.Equal(t, 0, updateSvc.rollbackToCall)
}

func TestSystemHandlerRollbackWithDisallowedVersionReturnsBadRequest(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		rollbackToErr: service.ErrRollbackVersionNotAllowed,
	}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":"9.9.9"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "rollback-to-bad")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 1, updateSvc.rollbackToCall)
}

func TestSystemHandlerGetRollbackVersions(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		rollbackVersions: []service.RollbackVersion{
			{Version: "0.1.146", PublishedAt: "2026-07-07T00:00:00Z", HTMLURL: "https://example.com/v0.1.146"},
			{Version: "0.1.145", PublishedAt: "2026-07-06T00:00:00Z", HTMLURL: "https://example.com/v0.1.145"},
		},
	}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/rollback-versions", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, updateSvc.rollbackVersionsCall)

	var body struct {
		Code int `json:"code"`
		Data struct {
			Versions []service.RollbackVersion `json:"versions"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Len(t, body.Data.Versions, 2)
	require.Equal(t, "0.1.146", body.Data.Versions[0].Version)
}

func TestSystemHandlerGetRollbackVersionsError(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		rollbackVersionsErr: errors.New("github unavailable"),
	}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/rollback-versions", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func newSystemRestartTestRouter(t *testing.T, updateSvc *systemHandlerUpdateServiceStub) (*gin.Engine, *SystemHandler, *memoryIdempotencyRepoStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(nil) })
	repo := newMemoryIdempotencyRepoStub()
	lockSvc := service.NewSystemOperationLockService(repo, service.IdempotencyConfig{
		ProcessingTimeout: time.Minute, SystemOperationTTL: time.Hour,
	})
	handler := NewSystemHandler(updateSvc, lockSvc)
	// No test is allowed to use the real process signal or delayed scheduler.
	handler.restartSignal = func() error { t.Fatal("unexpected restart signal"); return nil }
	handler.restartSchedule = func(func()) { t.Fatal("unexpected restart scheduling") }
	router := gin.New()
	router.POST("/api/v1/admin/system/restart", handler.RestartService)
	return router, handler, repo
}

func systemRestartRequest(router *gin.Engine, key string, ctx context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/restart", nil)
	req.Header.Set("Idempotency-Key", key)
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestSystemHandlerRestartPreflightFailureDoesNotScheduleOrSignal(t *testing.T) {
	reservation := &systemRestartReservationStub{}
	updateSvc := &systemHandlerUpdateServiceStub{
		prepareRestartErr: errors.New("guard rejected"), restartReservation: reservation,
	}
	router, _, repo := newSystemRestartTestRouter(t, updateSvc)
	rec := systemRestartRequest(router, "preflight-failure", nil)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, 1, updateSvc.prepareRestartCall)
	require.Equal(t, 0, reservation.executeCalls)
	require.Equal(t, 1, reservation.cancelCalls)
	require.ErrorIs(t, updateSvc.prepareRestartCtx.Err(), context.Canceled)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusFailedRetryable)
}

func TestSystemHandlerRestartRetainsLockAndSurvivesDisconnectUntilSignal(t *testing.T) {
	reservation := &systemRestartReservationStub{}
	updateSvc := &systemHandlerUpdateServiceStub{restartReservation: reservation}
	router, handler, repo := newSystemRestartTestRouter(t, updateSvc)
	var scheduled func()
	handler.restartSchedule = func(run func()) { scheduled = run }
	signals := 0
	handler.restartSignal = func() error {
		signals++
		requireSystemLockStatus(t, repo, service.IdempotencyStatusProcessing)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	rec := systemRestartRequest(router, "accepted-restart", ctx)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, scheduled)
	require.Equal(t, 1, updateSvc.prepareRestartCall)
	require.Equal(t, 0, reservation.executeCalls)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusProcessing)
	cancel()
	require.NoError(t, updateSvc.prepareRestartCtx.Err())
	deadline, ok := updateSvc.prepareRestartCtx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(systemRestartTimeout), deadline, time.Second)
	reservation.execute = func(executeCtx context.Context, signal func() error) error {
		require.Same(t, updateSvc.prepareRestartCtx, executeCtx)
		require.NoError(t, executeCtx.Err())
		return signal()
	}
	scheduled()
	require.Equal(t, 1, signals)
	require.Equal(t, 1, reservation.executeCalls)
	require.Zero(t, reservation.cancelCalls, "successful restart must retain service pending state until exit")
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)
	require.ErrorIs(t, updateSvc.prepareRestartCtx.Err(), context.Canceled)
}

func TestSystemHandlerRestartExecutionFailureClearsReservationAndReleasesLock(t *testing.T) {
	for _, executionFailure := range []bool{false, true} {
		name := "signal_failure"
		if executionFailure {
			name = "guard_recheck_failure"
		}
		t.Run(name, func(t *testing.T) {
			reservation := &systemRestartReservationStub{}
			updateSvc := &systemHandlerUpdateServiceStub{restartReservation: reservation}
			router, handler, repo := newSystemRestartTestRouter(t, updateSvc)
			var scheduled func()
			handler.restartSchedule = func(run func()) { scheduled = run }
			signals := 0
			handler.restartSignal = func() error { signals++; return errors.New("signal denied") }
			if executionFailure {
				reservation.execute = func(context.Context, func() error) error { return errors.New("guard changed") }
			}
			rec := systemRestartRequest(router, name, nil)
			require.Equal(t, http.StatusOK, rec.Code)
			require.NotNil(t, scheduled)
			scheduled()
			require.Equal(t, 1, reservation.executeCalls)
			require.Equal(t, 1, reservation.cancelCalls)
			if executionFailure {
				require.Zero(t, signals)
			} else {
				require.Equal(t, 1, signals)
			}
			requireSystemLockStatus(t, repo, service.IdempotencyStatusFailedRetryable)
		})
	}
}

func TestSystemHandlerRestartDuplicateRequestsAndCallbackOnlySignalOnce(t *testing.T) {
	reservation := &systemRestartReservationStub{}
	updateSvc := &systemHandlerUpdateServiceStub{restartReservation: reservation}
	router, handler, repo := newSystemRestartTestRouter(t, updateSvc)
	var scheduled func()
	schedules, signals := 0, 0
	handler.restartSchedule = func(run func()) { schedules++; scheduled = run }
	handler.restartSignal = func() error { signals++; return nil }
	require.Equal(t, http.StatusOK, systemRestartRequest(router, "same-restart", nil).Code)
	require.Equal(t, http.StatusConflict, systemRestartRequest(router, "same-restart", nil).Code)
	require.Equal(t, http.StatusConflict, systemRestartRequest(router, "other-restart", nil).Code)
	require.Equal(t, 1, updateSvc.prepareRestartCall)
	require.Equal(t, 1, schedules)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusProcessing)
	scheduled()
	scheduled()
	require.Equal(t, 1, reservation.executeCalls)
	require.Equal(t, 1, signals)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)
}

func TestSystemHandlerRestartIdempotentReplayDoesNotReschedule(t *testing.T) {
	reservation := &systemRestartReservationStub{}
	updateSvc := &systemHandlerUpdateServiceStub{restartReservation: reservation}
	router, handler, repo := newSystemRestartTestRouter(t, updateSvc)
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(repo, service.DefaultIdempotencyConfig()))
	var scheduled func()
	schedules, signals := 0, 0
	handler.restartSchedule = func(run func()) { schedules++; scheduled = run }
	handler.restartSignal = func() error { signals++; return nil }
	require.Equal(t, http.StatusOK, systemRestartRequest(router, "replay-restart", nil).Code)
	replay := systemRestartRequest(router, "replay-restart", nil)
	require.Equal(t, http.StatusOK, replay.Code)
	require.Equal(t, "true", replay.Header().Get("X-Idempotency-Replayed"))
	require.Equal(t, 1, schedules)
	require.Equal(t, 1, updateSvc.prepareRestartCall)
	scheduled()
	require.Equal(t, 1, signals)
}

func TestSystemHandlerRestartSynchronousSchedulerRunsAfterSuccessResponse(t *testing.T) {
	reservation := &systemRestartReservationStub{}
	updateSvc := &systemHandlerUpdateServiceStub{restartReservation: reservation}
	router, handler, repo := newSystemRestartTestRouter(t, updateSvc)
	rec := httptest.NewRecorder()
	signals := 0
	handler.restartSchedule = func(run func()) {
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), "Service restart initiated")
		require.Equal(t, 1, updateSvc.prepareRestartCall)
		run()
	}
	handler.restartSignal = func() error { signals++; return nil }
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/restart", nil)
	req.Header.Set("Idempotency-Key", "synchronous-scheduler")
	router.ServeHTTP(rec, req)
	require.Equal(t, 1, signals)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)
}

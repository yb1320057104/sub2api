package admin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/sysutil"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// SystemHandler handles system-related operations
type SystemHandler struct {
	updateSvc       systemUpdateService
	lockSvc         *service.SystemOperationLockService
	restartSchedule func(func())
	restartSignal   func() error
}

// systemUpdateTimeout bounds a full in-place update or rollback: the release
// manifest fetch plus a large binary download over slow links. It must stay
// above the GitHub download client timeout (10 minutes) so the download owns
// its own deadline.
const systemUpdateTimeout = 15 * time.Minute
const systemRestartTimeout = 30 * time.Second

// systemUpdateContext detaches a long-running update/rollback from the HTTP
// request lifetime. Browsers and reverse proxies commonly abort idle requests
// after 30-60s (axios default, nginx proxy_read_timeout), which canceled
// c.Request.Context() mid-download and killed the update with
// "download failed: context canceled" (#4504). The swap keeps running after a
// client disconnect; a later retry then hits the system operation lock or
// reports "Already up to date".
func systemUpdateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, systemUpdateTimeout)
}

type systemUpdateService interface {
	CheckUpdate(ctx context.Context, force bool) (*service.UpdateInfo, error)
	PerformUpdate(ctx context.Context) error
	Rollback() error
	ListRollbackVersions(ctx context.Context) ([]service.RollbackVersion, error)
	RollbackToVersion(ctx context.Context, version string) error
	PrepareRestart(ctx context.Context) (service.SystemRestartReservation, error)
}

// NewSystemHandler creates a new SystemHandler
func NewSystemHandler(updateSvc systemUpdateService, lockSvc *service.SystemOperationLockService) *SystemHandler {
	return &SystemHandler{
		updateSvc:       updateSvc,
		lockSvc:         lockSvc,
		restartSchedule: func(run func()) { time.AfterFunc(500*time.Millisecond, run) },
		restartSignal:   sysutil.SignalServiceRestart,
	}
}

// GetVersion returns the current version
// GET /api/v1/admin/system/version
func (h *SystemHandler) GetVersion(c *gin.Context) {
	info, err := h.updateSvc.CheckUpdate(c.Request.Context(), false)
	if info == nil {
		if err == nil {
			err = errors.New("version information is unavailable")
		}
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, gin.H{
		"version": info.CurrentVersion,
	})
}

// CheckUpdates checks for available updates
// GET /api/v1/admin/system/check-updates
func (h *SystemHandler) CheckUpdates(c *gin.Context) {
	force := c.Query("force") == "true"
	info, err := h.updateSvc.CheckUpdate(c.Request.Context(), force)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, info)
}

// PerformUpdate downloads and applies the update
// POST /api/v1/admin/system/update
func (h *SystemHandler) PerformUpdate(c *gin.Context) {
	operationID := buildSystemOperationID(c, "update")
	payload := gin.H{"operation_id": operationID}
	executeAdminIdempotentJSON(c, "admin.system.update", payload, service.DefaultSystemOperationIdempotencyTTL(), func(ctx context.Context) (any, error) {
		lock, release, err := h.acquireSystemLock(ctx, operationID)
		if err != nil {
			return nil, err
		}
		var releaseReason string
		succeeded := false
		defer func() {
			release(releaseReason, succeeded)
		}()

		updateCtx, cancel := systemUpdateContext(ctx)
		defer cancel()

		if err := h.updateSvc.PerformUpdate(updateCtx); err != nil {
			if errors.Is(err, service.ErrNoUpdateAvailable) {
				info, checkErr := h.updateSvc.CheckUpdate(updateCtx, false)
				if checkErr != nil {
					releaseReason = "SYSTEM_UPDATE_FAILED"
					return nil, checkErr
				}
				succeeded = true
				return gin.H{
					"message":            "Already up to date",
					"already_up_to_date": true,
					"current_version":    info.CurrentVersion,
					"latest_version":     info.LatestVersion,
					"operation_id":       lock.OperationID(),
				}, nil
			}
			releaseReason = "SYSTEM_UPDATE_FAILED"
			return nil, err
		}
		succeeded = true

		return gin.H{
			"message":      "Update completed. Please restart the service.",
			"need_restart": true,
			"operation_id": lock.OperationID(),
		}, nil
	})
}

// GetRollbackVersions lists versions available for rollback
// GET /api/v1/admin/system/rollback-versions
func (h *SystemHandler) GetRollbackVersions(c *gin.Context) {
	versions, err := h.updateSvc.ListRollbackVersions(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, gin.H{
		"versions": versions,
	})
}

// Rollback restores a previous version.
// Without a body (or with an empty version) it restores the local .backup binary
// left by the last in-place update. With {"version": "x.y.z"} it downloads and
// installs that specific release (must be one of the recent rollback versions).
// POST /api/v1/admin/system/rollback
func (h *SystemHandler) Rollback(c *gin.Context) {
	var req struct {
		Version string `json:"version"`
	}
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			// An empty chunked request has an unknown ContentLength but is still
			// equivalent to the local-backup rollback form.
			if !errors.Is(err, io.EOF) {
				response.Error(c, http.StatusBadRequest, "invalid request body")
				return
			}
		}
	}
	targetVersion := strings.TrimSpace(req.Version)

	operation := "rollback"
	if targetVersion != "" {
		operation = "rollback:" + targetVersion
	}
	operationID := buildSystemOperationID(c, operation)
	payload := gin.H{"operation_id": operationID, "version": targetVersion}
	executeAdminIdempotentJSON(c, "admin.system.rollback", payload, service.DefaultSystemOperationIdempotencyTTL(), func(ctx context.Context) (any, error) {
		lock, release, err := h.acquireSystemLock(ctx, operationID)
		if err != nil {
			return nil, err
		}
		var releaseReason string
		succeeded := false
		defer func() {
			release(releaseReason, succeeded)
		}()

		if targetVersion != "" {
			// 指定版本回退同样要下载完整二进制，与更新一样和请求生命周期解耦。
			rollbackCtx, cancel := systemUpdateContext(ctx)
			defer cancel()
			err = h.updateSvc.RollbackToVersion(rollbackCtx, targetVersion)
		} else {
			err = h.updateSvc.Rollback()
		}
		if err != nil {
			releaseReason = "SYSTEM_ROLLBACK_FAILED"
			return nil, err
		}
		succeeded = true

		return gin.H{
			"message":      "Rollback completed. Please restart the service.",
			"need_restart": true,
			"version":      targetVersion,
			"operation_id": lock.OperationID(),
		}, nil
	})
}

// RestartService restarts the systemd service
// POST /api/v1/admin/system/restart
func (h *SystemHandler) RestartService(c *gin.Context) {
	operationID := buildSystemOperationID(c, "restart")
	payload := gin.H{"operation_id": operationID}
	var runRestart func()
	var cancelRestart func()
	executeAdminIdempotentJSON(c, "admin.system.restart", payload, service.DefaultSystemOperationIdempotencyTTL(), func(ctx context.Context) (any, error) {
		lock, release, err := h.acquireSystemLock(ctx, operationID)
		if err != nil {
			return nil, err
		}
		// Retain both the operation lock and the verified restart reservation
		// until the synchronous signal attempt finishes. Client disconnects must
		// not cancel an already accepted restart.
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), systemRestartTimeout)
		reservation, err := h.updateSvc.PrepareRestart(restartCtx)
		if err != nil || reservation == nil {
			if reservation != nil {
				reservation.Cancel()
			}
			cancel()
			release("SYSTEM_RESTART_PREFLIGHT_FAILED", false)
			if err == nil {
				err = errors.New("restart preparation is unavailable")
			}
			return nil, err
		}
		var once sync.Once
		cancelRestart = func() {
			once.Do(func() {
				cancel()
				reservation.Cancel()
				release("SYSTEM_RESTART_RESPONSE_FAILED", false)
			})
		}
		runRestart = func() {
			once.Do(func() {
				succeeded := false
				defer func() {
					cancel()
					if !succeeded {
						reservation.Cancel()
						slog.Error("SYSTEM_RESTART_EXECUTION_FAILED")
						release("SYSTEM_RESTART_EXECUTION_FAILED", false)
						return
					}
					release("", true)
				}()
				if err := restartCtx.Err(); err != nil {
					return
				}
				signal := h.restartSignal
				if signal == nil {
					signal = sysutil.SignalServiceRestart
				}
				if err := reservation.Execute(restartCtx, signal); err != nil {
					return
				}
				succeeded = true
			})
		}
		return gin.H{
			"message":      "Service restart initiated",
			"operation_id": lock.OperationID(),
		}, nil
	})
	// Idempotent replay never sets runRestart. Scheduling after the helper
	// returns also keeps injected synchronous schedulers behind the response.
	if runRestart != nil {
		if c.Writer.Status() >= http.StatusBadRequest {
			cancelRestart()
			return
		}
		if h.restartSchedule != nil {
			h.restartSchedule(runRestart)
		} else {
			time.AfterFunc(500*time.Millisecond, runRestart)
		}
	}
}

func (h *SystemHandler) acquireSystemLock(
	ctx context.Context,
	operationID string,
) (*service.SystemOperationLock, func(string, bool), error) {
	if h.lockSvc == nil {
		return nil, nil, service.ErrIdempotencyStoreUnavail
	}
	lock, err := h.lockSvc.Acquire(ctx, operationID)
	if err != nil {
		return nil, nil, err
	}
	release := func(reason string, succeeded bool) {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = h.lockSvc.Release(releaseCtx, lock, succeeded, reason)
	}
	return lock, release, nil
}

func buildSystemOperationID(c *gin.Context, operation string) string {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		return "sysop-" + operation + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	actorScope := "admin:0"
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok {
		actorScope = "admin:" + strconv.FormatInt(subject.UserID, 10)
	}
	seed := operation + "|" + actorScope + "|" + c.FullPath() + "|" + key
	hash := service.HashIdempotencyKey(seed)
	if len(hash) > 24 {
		hash = hash[:24]
	}
	return "sysop-" + hash
}

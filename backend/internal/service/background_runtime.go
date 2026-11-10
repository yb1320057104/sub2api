package service

import (
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
)

// Only global periodic jobs use this gate. Billing/audit/log queues, BPS exit
// warming, local cache invalidation, connection pools, and request-triggered work stay active
// on gateway replicas. The default/full node retains the existing behavior.
func startBackgroundService(cfg *config.Config, job interface{ Start() }) {
	if cfg.RunsBackgroundJobs() {
		startBackgroundAfterActivation(fmt.Sprintf("%T", job), job.Start)
	}
}

// StartBackgroundJob applies both the process role and rolling activation gate
// to global jobs whose lifecycle is not expressed by a Start method.
func StartBackgroundJob(cfg *config.Config, name string, start func()) {
	if cfg.RunsBackgroundJobs() {
		startBackgroundAfterActivation(name, start)
	}
}

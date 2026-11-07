// Package sysutil provides system-level utilities for process management.
package sysutil

import (
	"errors"
	"log"
	"os"
	"runtime"
	"syscall"
	"time"
)

var ErrRestartUnsupported = errors.New("service restart signaling is only supported on Linux")

// SignalServiceRestart synchronously sends SIGTERM to this process. The caller
// owns response scheduling and must finish restart preflight before calling it.
func SignalServiceRestart() error {
	return signalServiceRestart(runtime.GOOS, func() error {
		process, err := os.FindProcess(os.Getpid())
		if err != nil {
			return err
		}
		defer func() { _ = process.Release() }()
		return process.Signal(syscall.SIGTERM)
	})
}

func signalServiceRestart(goos string, signal func() error) error {
	if goos != "linux" {
		return ErrRestartUnsupported
	}
	return signal()
}

// RestartService signals this process so the main server can shut down and run
// its deferred cleanup before the supervisor restarts it. The setup server has
// no background workers and retains the signal's normal process-exit behavior.
//
// This relies on systemd's Restart=always or Docker's restart policy to start
// the service again after it exits. No privileged supervisor operation is used.
//
// Prerequisites:
//   - Linux OS
//   - A supervisor configured to restart the service after exit
func RestartService() error {
	if runtime.GOOS != "linux" {
		log.Println("Service restart via exit only works on Linux with systemd")
		return nil
	}

	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}

	log.Println("Initiating service restart by graceful shutdown (SIGTERM)...")
	log.Println("The service supervisor will restart the process after cleanup")

	// Give a moment for logs to flush and response to be sent
	go func() {
		defer func() { _ = process.Release() }()
		time.Sleep(100 * time.Millisecond)
		if err := process.Signal(syscall.SIGTERM); err != nil {
			log.Printf("Service restart failed: %v", err)
			log.Println("Please restart the service manually: sudo systemctl restart sub2api")
		}
	}()

	return nil
}

// RestartServiceAsync is a fire-and-forget version of RestartService.
// It logs errors instead of returning them, suitable for goroutine usage.
func RestartServiceAsync() {
	if err := RestartService(); err != nil {
		log.Printf("Service restart failed: %v", err)
		log.Println("Please restart the service manually: sudo systemctl restart sub2api")
	}
}

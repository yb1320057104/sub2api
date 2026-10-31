//go:build linux

package service

import (
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Chromium spawns helper processes (zygote, GPU, network service, crashpad).
// When the browser exits they are reparented to PID 1 — inside the container
// that is this process — and stay as zombies because nothing waits for them.
// A long-lived deployment therefore leaks a few entries per authorization.
//
// The counters below let a reaper collect them only between attempts, so it can
// never steal the exit status of a child that exec.Cmd is still waiting for.
var (
	authorizationChildLock    sync.Mutex
	authorizationChildRunning atomic.Int64
	authorizationReaperOnce   sync.Once
)

func authorizationChildStart() {
	authorizationChildLock.Lock()
	authorizationChildRunning.Add(1)
	authorizationChildLock.Unlock()
}

func authorizationChildDone() {
	authorizationChildRunning.Add(-1)
}

// StartReaper is called once per attempt; it installs the loop only when this
// process really is the container's PID 1, where orphan adoption happens.
func authorizationStartReaper() {
	authorizationReaperOnce.Do(func() {
		if os.Getpid() != 1 {
			return
		}
		go func() {
			for {
				time.Sleep(30 * time.Second)
				authorizationReapZombies()
			}
		}()
	})
}

func authorizationReapZombies() {
	authorizationChildLock.Lock()
	defer authorizationChildLock.Unlock()
	if authorizationChildRunning.Load() > 0 {
		return
	}
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if pid <= 0 || err != nil {
			return
		}
		authorizationTrace(nil, "reaped orphan child pid=%d", pid)
	}
}

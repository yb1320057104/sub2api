//go:build linux

package reauthruntime

import (
	"os/exec"
	"syscall"
	"time"
)

func configureProcess(cmd *exec.Cmd) {
	// SIGTERM means drain. Reserve a separate signal for an unexpected parent
	// death so the supervisor can still clean up orphan protocol descendants.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGUSR1}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
}

func stopProcess(cmd *exec.Cmd) error {
	return cmd.Process.Signal(syscall.SIGTERM)
}

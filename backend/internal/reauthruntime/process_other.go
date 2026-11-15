//go:build !linux

package reauthruntime

import (
	"os"
	"os/exec"
)

func configureProcess(cmd *exec.Cmd) {}

func stopProcess(cmd *exec.Cmd) error { return cmd.Process.Signal(os.Interrupt) }

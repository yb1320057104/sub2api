//go:build !linux

package service

// Orphan adoption by PID 1 is a POSIX/container concern; other platforms rely on
// the operating system to reap the browser's helper processes.
func authorizationChildStart() {}

func authorizationChildDone() {}

func authorizationStartReaper() {}

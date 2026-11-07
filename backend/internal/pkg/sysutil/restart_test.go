//go:build unit

package sysutil

import (
	"errors"
	"runtime"
	"testing"
)

func TestSignalServiceRestartSynchronousAndReportsFailure(t *testing.T) {
	want := errors.New("signal failed")
	called := false
	err := signalServiceRestart("linux", func() error { called = true; return want })
	if !called || !errors.Is(err, want) {
		t.Fatalf("signal must finish synchronously and return its error: called=%t err=%v", called, err)
	}
}

func TestSignalServiceRestartUnsupportedNeverSignals(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "freebsd"} {
		err := signalServiceRestart(goos, func() error { t.Fatal("unsupported platform signaled"); return nil })
		if !errors.Is(err, ErrRestartUnsupported) {
			t.Fatalf("%s: expected unsupported error, got %v", goos, err)
		}
	}
	if runtime.GOOS != "linux" {
		if !errors.Is(SignalServiceRestart(), ErrRestartUnsupported) {
			t.Fatal("public synchronous API must reject unsupported platform")
		}
	}
}

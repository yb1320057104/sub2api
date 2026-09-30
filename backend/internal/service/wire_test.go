package service

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/collection"
)

func TestProvideTimingWheelService_ReturnsError(t *testing.T) {
	original := newTimingWheel
	t.Cleanup(func() { newTimingWheel = original })

	newTimingWheel = func(_ time.Duration, _ int, _ collection.Execute) (*collection.TimingWheel, error) {
		return nil, errors.New("boom")
	}

	svc, err := ProvideTimingWheelService()
	if err == nil {
		t.Fatalf("期望返回 error，但得到 nil")
	}
	if svc != nil {
		t.Fatalf("期望返回 nil svc，但得到非空")
	}
}

func TestProvideTimingWheelService_Success(t *testing.T) {
	svc, err := ProvideTimingWheelService()
	if err != nil {
		t.Fatalf("期望 err 为 nil，但得到: %v", err)
	}
	if svc == nil {
		t.Fatalf("期望 svc 非空，但得到 nil")
	}
	svc.Stop()
}

func TestBackgroundStartGate_DefaultStartsSynchronously(t *testing.T) {
	gate := newBackgroundStartGate("")
	t.Cleanup(gate.stop)
	called := false
	gate.start("service", func() { called = true })
	gate.start("optional", nil)
	if !called {
		t.Fatal("default startup must complete before the provider returns")
	}
}

func TestBackgroundStartGate_ExistingMarkerStartsSynchronously(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	gate := newBackgroundStartGate(path)
	t.Cleanup(gate.stop)
	called := false
	gate.start("service", func() { called = true })
	if !called {
		t.Fatal("a persisted marker must restore synchronous startup after restart")
	}
}

func TestBackgroundStartGate_AtomicActivationStartsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active")
	gate := newBackgroundStartGate(path)
	gate.pollInterval = time.Millisecond
	t.Cleanup(gate.stop)
	started := make(chan string, 3)
	gate.start("first", func() { started <- "first" })
	gate.start("second", func() { started <- "second" })
	select {
	case name := <-started:
		t.Fatalf("%s started before activation", name)
	default:
	}
	if err := os.WriteFile(path+".tmp", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"first", "second"} {
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not start after activation", want)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// Activation remains effective, including registrations after the file is removed.
	gate.start("third", func() { started <- "third" })
	select {
	case got := <-started:
		if got != "third" {
			t.Fatalf("unexpected repeated startup: %q", got)
		}
	default:
		t.Fatal("an activated gate must start later providers synchronously")
	}
	gate.stop()
	select {
	case name := <-started:
		t.Fatalf("%s started more than once", name)
	default:
	}
}

func TestBackgroundStartGate_StopCancelsPendingAndFutureStarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active")
	gate := newBackgroundStartGate(path)
	gate.pollInterval = time.Millisecond
	t.Cleanup(gate.stop)
	var starts atomic.Int32
	gate.start("pending", func() { starts.Add(1) })
	gate.stop()
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	gate.start("late", func() { starts.Add(1) })
	if got := starts.Load(); got != 0 {
		t.Fatalf("shutdown must prevent pending and late starts, got %d", got)
	}
}

func TestBackgroundStartGate_StopWaitsForStartingCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active")
	gate := newBackgroundStartGate(path)
	gate.pollInterval = time.Millisecond
	entered := make(chan struct{})
	release := make(chan struct{})
	// Always release the callback before cleanup, including assertion failures.
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		gate.stop()
	})
	gate.start("blocking", func() {
		close(entered)
		<-release
	})
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("callback did not begin")
	}
	stopped := make(chan struct{})
	go func() {
		gate.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("shutdown returned while a Start callback was still running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish after Start returned")
	}
	var lateStarts atomic.Int32
	gate.start("after-cleanup", func() { lateStarts.Add(1) })
	if lateStarts.Load() != 0 {
		t.Fatal("service started after cleanup")
	}
}

func TestBackgroundStartGate_RejectsDirectoryActivation(t *testing.T) {
	gate := newBackgroundStartGate(t.TempDir())
	gate.pollInterval = time.Millisecond
	t.Cleanup(gate.stop)
	var starts atomic.Int32
	gate.start("service", func() { starts.Add(1) })
	gate.stop()
	if starts.Load() != 0 {
		t.Fatal("a directory must not activate background services")
	}
}

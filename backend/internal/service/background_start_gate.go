package service

import (
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

var pendingBackgroundStarts = newBackgroundStartGate(os.Getenv("SUB2API_BACKGROUND_ACTIVATION_FILE"))

type pendingBackgroundStart struct {
	name  string
	start func()
}

// backgroundStartGate delays selected periodic services during a rolling deployment.
// Activation is one-way: removing the file does not stop services already started.
type backgroundStartGate struct {
	mu           sync.Mutex
	path         string
	pollInterval time.Duration
	active       bool
	stopped      bool
	pending      []pendingBackgroundStart
	stopCh       chan struct{}
	doneCh       chan struct{}
	lastError    string
}

func newBackgroundStartGate(path string) *backgroundStartGate {
	path = strings.TrimSpace(path)
	return &backgroundStartGate{
		path:         path,
		pollInterval: time.Second,
		active:       path == "",
	}
}

// startBackgroundAfterActivation preserves synchronous startup unless deployment
// explicitly supplies an activation file. Request workers do not use this gate.
func startBackgroundAfterActivation(name string, start func()) {
	pendingBackgroundStarts.start(name, start)
}

// StopPendingBackgroundStarts must run before individual service Stop methods.
// It waits for activation already in progress and cancels starts still waiting for it.
func StopPendingBackgroundStarts() {
	pendingBackgroundStarts.stop()
}

func (g *backgroundStartGate) start(name string, start func()) {
	if start == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	if g.active {
		start()
		return
	}
	g.pending = append(g.pending, pendingBackgroundStart{name: name, start: start})
	if g.activationFileReadyLocked() {
		g.activateLocked()
		return
	}
	slog.Info("background_service_waiting_for_activation", "service", name)
	if g.doneCh == nil {
		g.stopCh = make(chan struct{})
		g.doneCh = make(chan struct{})
		go g.waitForActivation()
	}
}

func (g *backgroundStartGate) activationFileReadyLocked() bool {
	info, err := os.Lstat(g.path)
	if err == nil && info.Mode().IsRegular() {
		g.lastError = ""
		return true
	}
	if os.IsNotExist(err) {
		g.lastError = ""
		return false
	}
	message := "activation path must be a regular file"
	if err != nil {
		message = err.Error()
	}
	if message != g.lastError {
		slog.Error("background_activation_file_unavailable", "error", message)
		g.lastError = message
	}
	return false
}

// Holding mu through each callback serializes Start with stop. Once stop returns,
// callers may safely stop every service, including ones that never started.
func (g *backgroundStartGate) activateLocked() {
	g.active = true
	pending := g.pending
	g.pending = nil
	slog.Info("background_services_activated", "count", len(pending))
	for _, item := range pending {
		item.start()
		slog.Info("background_service_activation_complete", "service", item.name)
	}
}

func (g *backgroundStartGate) waitForActivation() {
	defer close(g.doneCh)
	ticker := time.NewTicker(g.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-g.stopCh:
			return
		case <-ticker.C:
			g.mu.Lock()
			if g.stopped || g.active {
				g.mu.Unlock()
				return
			}
			if g.activationFileReadyLocked() {
				g.activateLocked()
				g.mu.Unlock()
				return
			}
			g.mu.Unlock()
		}
	}
}

func (g *backgroundStartGate) stop() {
	g.mu.Lock()
	if !g.stopped {
		g.stopped = true
		g.pending = nil
		if g.stopCh != nil {
			close(g.stopCh)
		}
	}
	done := g.doneCh
	g.mu.Unlock()
	if done != nil {
		<-done
	}
}

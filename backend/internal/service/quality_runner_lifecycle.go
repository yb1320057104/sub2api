package service

import (
	"context"
	"sync"
)

// qualityRunnerLifecycle cancels admission while joining work that was already
// admitted. A worker transfer must not orphan an upstream probe or its result.
type qualityRunnerLifecycle struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	stopped bool
	runs    sync.WaitGroup
}

func (l *qualityRunnerLifecycle) contextLocked() context.Context {
	if l.ctx == nil {
		l.ctx, l.cancel = context.WithCancel(context.Background())
		if l.stopped {
			l.cancel()
		}
	}
	return l.ctx
}

func (l *qualityRunnerLifecycle) admissionContext() context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.contextLocked()
}

func (l *qualityRunnerLifecycle) begin() (context.Context, func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx := l.contextLocked()
	if l.stopped {
		return ctx, func() {}, false
	}
	l.runs.Add(1)
	return ctx, l.runs.Done, true
}

func (l *qualityRunnerLifecycle) stopAdmission() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = true
	l.contextLocked()
	l.cancel()
}

func (l *qualityRunnerLifecycle) wait() { l.runs.Wait() }

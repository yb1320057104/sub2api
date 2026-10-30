package service

import (
	"context"
	"time"
)

// One worker per selected group/mode makes the feature's concurrency grow with
// the selection: N groups can run 2N tests when both modes are enabled.
type intelligenceTestWorker struct {
	cfg         *IntelligenceTestConfig
	keyRevision int64
	cancel      context.CancelFunc
}

func (s *IntelligenceTestService) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	// Only this loop adds workers. Waiting after dispatch has stopped prevents
	// WaitGroup Add/Wait races; parent cancellation also reaches every worker.
	defer s.workers.Wait()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	case <-s.wake:
	}
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-s.wake:
		default:
		}
		s.dispatchIntelligenceTests(ctx)
		timer.Reset(5 * time.Second)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.wake:
		}
	}
}

// Planning never waits for model calls. Fast modes/groups keep their own cadence
// even while another pair is slow, cancelling, or held by a remote live lease.
func (s *IntelligenceTestService) dispatchIntelligenceTests(ctx context.Context) {
	cfg, keyRevision, jobs, err := s.planIntelligenceTests(ctx)
	s.reconcileIntelligenceTestWorkers(cfg, keyRevision, err != nil)
	if err != nil || ctx.Err() != nil {
		return
	}
	for _, job := range jobs {
		s.startIntelligenceTestWorker(ctx, job, cfg, keyRevision)
	}
}

func (s *IntelligenceTestService) reconcileIntelligenceTestWorkers(cfg *IntelligenceTestConfig, keyRevision int64, unavailable bool) {
	s.mu.Lock()
	workers := make([]*intelligenceTestWorker, 0, len(s.inflight))
	for _, worker := range s.inflight {
		workers = append(workers, worker)
	}
	s.mu.Unlock()
	// Workers dispatched together share a configuration snapshot. Compare each
	// snapshot once, without repeatedly serializing large prompts per group.
	matches := make(map[*IntelligenceTestConfig]bool)
	for _, worker := range workers {
		match, known := matches[worker.cfg]
		if !known {
			match = !unavailable && sameIntelligenceTestConfig(worker.cfg, cfg)
			matches[worker.cfg] = match
		}
		if !match || worker.keyRevision != keyRevision {
			worker.cancel()
		}
	}
}

// Called only by loop, before the corresponding goroutine exists. The durable
// pair lease handles other instances; this map also avoids duplicate local
// goroutines while a claim is still waiting for a database connection.
func (s *IntelligenceTestService) startIntelligenceTestWorker(parent context.Context, job intelligenceTestJob, cfg *IntelligenceTestConfig, keyRevision int64) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	s.mu.Lock()
	if parent.Err() != nil {
		s.mu.Unlock()
		return
	}
	if _, exists := s.inflight[job]; exists {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	worker := &intelligenceTestWorker{cfg: cfg, keyRevision: keyRevision, cancel: cancel}
	s.inflight[job] = worker
	s.workers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.workers.Done()
		claimed := false
		defer func() {
			// Explicit cancellation can unblock a just-saved generation before a
			// claim finished. Ordinary claim failures never trigger their own retry.
			wake := claimed || ctx.Err() != nil
			cancel()
			s.mu.Lock()
			if s.inflight[job] == worker {
				delete(s.inflight, job)
			}
			s.mu.Unlock()
			if wake {
				s.wakeIntelligenceTests()
			}
		}()
		claimed = s.runOne(ctx, job.groupID, job.mode, cfg)
	}()
}

package service

import (
	"context"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Pending is an exposure fence, never proof that delivery or consumption worked.
// Failed fences retain the original affinity TTL so a raced consumer cannot
// downgrade a failed/ambiguous delivery to an ordinary first-seen route.
type OpenAIAgentRecoveryDeliveryCache interface {
	OpenAIAgentRecoveryCache
	ReserveOpenAIAgentRecovery(context.Context, string, string, string, time.Duration) error
	FinishOpenAIAgentRecovery(context.Context, string, string, string, bool, time.Duration) error
}

type openAIAgentRecoveryDelivery struct {
	service *OpenAIGatewayService
	ctx     context.Context
	request *openAIAgentRecoveryRequest
	binding openAIAgentRecoveryBinding
	pending string
	keys    map[[32]byte]bool
	once    sync.Once
}

func newOpenAIAgentRecoveryDelivery(s *OpenAIGatewayService, ctx context.Context, r *openAIAgentRecoveryRequest, b openAIAgentRecoveryBinding) *openAIAgentRecoveryDelivery {
	return &openAIAgentRecoveryDelivery{service: s, ctx: ctx, request: r, binding: b, pending: "pending:" + uuid.NewString() + ":" + encodeOpenAIAgentRecoveryBinding(b), keys: make(map[[32]byte]bool)}
}

func (s *OpenAIGatewayService) awaitOpenAIAgentRecovery(ctx context.Context, shared OpenAIAgentRecoveryCache, state *openAIAgentRecoveryState, key [32]byte) (openAIAgentRecoveryBinding, bool, error) {
	bindings, found, err := s.awaitOpenAIAgentRecoveries(ctx, shared, state, [][32]byte{key})
	if err != nil {
		return openAIAgentRecoveryBinding{}, false, err
	}
	return bindings[0], found[0], err
}

func (s *OpenAIGatewayService) awaitOpenAIAgentRecoveries(ctx context.Context, shared OpenAIAgentRecoveryCache, state *openAIAgentRecoveryState, keys [][32]byte) ([]openAIAgentRecoveryBinding, []bool, error) {
	bindings := make([]openAIAgentRecoveryBinding, len(keys))
	found := make([]bool, len(keys))
	observedPending := make([]bool, len(keys))
	remaining := make([]int, len(keys))
	for i := range keys {
		remaining[i] = i
	}
	for len(remaining) > 0 {
		lookup := make([][32]byte, len(remaining))
		for i, index := range remaining {
			lookup[i] = keys[index]
		}
		var values []string
		var readErr error
		if shared != nil {
			values, readErr = readOpenAIAgentRecoveryRecords(ctx, shared, lookup)
		}
		next := make([]int, 0, len(remaining))
		for i, index := range remaining {
			b := openAIAgentRecoveryBinding{}
			present := false
			err := readErr
			if shared != nil && err == nil {
				b, present, err = decodeOpenAIAgentRecoveryBinding(values[i])
			} else if shared == nil {
				state.mu.Lock()
				b, present = state.bindings[keys[index]]
				state.mu.Unlock()
				present = present && time.Now().Before(b.expiresAt)
			}
			bindings[index], found[index] = b, present
			if err != nil {
				reason := "store_unavailable"
				if observedPending[index] && ctx.Err() != nil {
					reason = "delivery_pending"
				}
				return bindings, found, openAIAgentRecoveryError(reason)
			}
			if b.conflicted || (!present && !observedPending[index]) {
				continue
			}
			if b.failed || (!present && observedPending[index]) {
				return bindings, found, openAIAgentRecoveryError("owner_unavailable")
			}
			if b.pending != "" {
				observedPending[index] = true
				next = append(next, index)
			}
		}
		remaining = next
		if len(remaining) == 0 {
			break
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return bindings, found, openAIAgentRecoveryError("delivery_pending")
		case <-timer.C:
		}
	}
	return bindings, found, nil
}

func (d *openAIAgentRecoveryDelivery) reserve(items [][32]byte) error {
	if len(items) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(d.ctx, 3*time.Second)
	defer cancel()
	if batch, ok := d.service.cache.(OpenAIAgentRecoveryBatchCache); ok {
		keys := make([][32]byte, 0, len(items))
		for _, item := range items {
			key := d.request.itemKey(item)
			if !d.keys[key] {
				d.keys[key] = true // Track ambiguous writes for token-CAS cleanup.
				keys = append(keys, key)
			}
		}
		if len(keys) > 0 {
			if err := batch.ReserveOpenAIAgentRecoveries(ctx, encodeOpenAIAgentRecoveryKeys(keys), encodeOpenAIAgentRecoveryBinding(d.binding), d.pending, d.service.openAIWSSessionStickyTTL()); err != nil {
				return openAIAgentRecoveryError("store_unavailable")
			}
		}
		return nil
	}
	for _, item := range items {
		key := d.request.itemKey(item)
		if d.keys[key] {
			continue
		}
		// Track an ambiguous reserve too: the server may have applied it before a
		// network failure. Cleanup uses token CAS, so it cannot revoke another grant.
		d.keys[key] = true
		ttl := d.service.openAIWSSessionStickyTTL()
		route := encodeOpenAIAgentRecoveryBinding(d.binding)
		if shared, ok := d.service.cache.(OpenAIAgentRecoveryCache); ok {
			transactional, ok := shared.(OpenAIAgentRecoveryDeliveryCache)
			if !ok {
				return openAIAgentRecoveryError("store_unavailable")
			}
			if err := transactional.ReserveOpenAIAgentRecovery(ctx, hex.EncodeToString(key[:]), route, d.pending, ttl); err != nil {
				return openAIAgentRecoveryError("store_unavailable")
			}
		} else {
			state := &d.service.agentMessageRecovery
			state.mu.Lock()
			if state.bindings == nil {
				state.bindings = make(map[[32]byte]openAIAgentRecoveryBinding)
			}
			for k, b := range state.bindings {
				if !time.Now().Before(b.expiresAt) {
					delete(state.bindings, k)
				}
			}
			old, found := state.bindings[key]
			switch {
			case found && (old.conflicted || old.failed || old.pending != "" || !sameOpenAIAgentRecoveryRoute(old, d.binding)):
				old.conflicted = true
				state.bindings[key] = old
				state.mu.Unlock()
				return openAIAgentRecoveryError("owner_conflict")
			case found: // Existing completed compatibility survives a failed producer.
			case len(state.bindings) >= openAIAgentRecoveryMaxEntries:
				state.mu.Unlock()
				return openAIAgentRecoveryError("store_unavailable")
			default:
				b := d.binding
				b.pending = d.pending
				b.expiresAt = time.Now().Add(ttl)
				state.bindings[key] = b
			}
			state.mu.Unlock()
		}
	}
	return nil
}

func (d *openAIAgentRecoveryDelivery) finish(completed bool) {
	d.once.Do(func() {
		final := make(map[[32]byte]bool)
		if completed {
			for _, item := range d.request.outputs {
				final[d.request.itemKey(item)] = true
			}
		}
		// Canceled requests must still revoke their pending exposure fences.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(d.ctx), 3*time.Second)
		defer cancel()
		if batch, ok := d.service.cache.(OpenAIAgentRecoveryBatchCache); ok {
			keys := make(map[string]bool, len(d.keys))
			for key := range d.keys {
				keys[hex.EncodeToString(key[:])] = final[key]
			}
			if err := batch.FinishOpenAIAgentRecoveries(ctx, keys, encodeOpenAIAgentRecoveryBinding(d.binding), d.pending, d.service.openAIWSSessionStickyTTL()); err != nil {
				logger.L().Warn("openai.agent_recovery_commit_failed", zap.Int64("account_id", d.binding.accountID), zap.Error(err))
			}
			return
		}
		for key := range d.keys {
			commit := final[key]
			ttl := d.service.openAIWSSessionStickyTTL()
			if shared, ok := d.service.cache.(OpenAIAgentRecoveryDeliveryCache); ok {
				route := encodeOpenAIAgentRecoveryBinding(d.binding)
				if err := shared.FinishOpenAIAgentRecovery(ctx, hex.EncodeToString(key[:]), route, d.pending, commit, ttl); err != nil {
					// A failed/ambiguous commit leaves pending (or a completed CAS); it can
					// never create a miss which would authorize an unrelated route.
					logger.L().Warn("openai.agent_recovery_commit_failed", zap.Int64("account_id", d.binding.accountID), zap.Error(err))
				}
			} else if _, shared := d.service.cache.(OpenAIAgentRecoveryCache); !shared {
				state := &d.service.agentMessageRecovery
				state.mu.Lock()
				if b, found := state.bindings[key]; found && b.pending == d.pending && !b.conflicted {
					b.pending = ""
					b.failed = !commit
					b.expiresAt = time.Now().Add(ttl)
					state.bindings[key] = b
				}
				state.mu.Unlock()
			}
		}
	})
}

func decodeOpenAIAgentRecoveryPending(raw string) (string, string, bool) {
	if !strings.HasPrefix(raw, "pending:") {
		return "", "", false
	}
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 || parts[1] == "" {
		return "", "", false
	}
	return raw, parts[2], true
}

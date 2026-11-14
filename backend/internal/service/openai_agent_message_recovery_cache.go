package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// OpenAIAgentRecoveryCache stores only scoped item/route hashes. Production
// Redis implements it independently of ordinary mutable scheduler stickiness.
// Records survive application restarts/nodes while Redis retains them. Missing
// or expired records cannot establish compatibility for unknown old history.
type OpenAIAgentRecoveryCache interface {
	GetOpenAIAgentRecovery(ctx context.Context, key string) (string, error)
	PublishOpenAIAgentRecovery(ctx context.Context, key, route string, ttl time.Duration) error
}

// Optional batch operations preserve the per-item Redis CAS while avoiding one
// network round trip for every historical item on a remote Redis writer.
// Single-item adapters remain supported; production implements all operations.
type OpenAIAgentRecoveryBatchCache interface {
	GetOpenAIAgentRecoveries(context.Context, []string) ([]string, error)
	PublishOpenAIAgentRecoveries(context.Context, []string, string, time.Duration) error
	RefreshOpenAIAgentRecoveries(context.Context, []string, string, time.Duration) (bool, error)
	ReserveOpenAIAgentRecoveries(context.Context, []string, string, string, time.Duration) error
	FinishOpenAIAgentRecoveries(context.Context, map[string]bool, string, string, time.Duration) error
}

func readOpenAIAgentRecoveryRecords(ctx context.Context, cache OpenAIAgentRecoveryCache, keys [][32]byte) ([]string, error) {
	encoded := encodeOpenAIAgentRecoveryKeys(keys)
	if batch, ok := cache.(OpenAIAgentRecoveryBatchCache); ok {
		values, err := batch.GetOpenAIAgentRecoveries(ctx, encoded)
		if err == nil && len(values) != len(keys) {
			err = errors.New("incomplete encrypted agent ownership batch")
		}
		return values, err
	}
	values := make([]string, len(keys))
	for i, key := range encoded {
		var err error
		values[i], err = cache.GetOpenAIAgentRecovery(ctx, key)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func encodeOpenAIAgentRecoveryKeys(keys [][32]byte) []string {
	encoded := make([]string, len(keys))
	for i, key := range keys {
		encoded[i] = hex.EncodeToString(key[:])
	}
	return encoded
}

func encodeOpenAIAgentRecoveryBinding(b openAIAgentRecoveryBinding) string {
	value, _ := json.Marshal([]any{1, b.accountID, hex.EncodeToString(b.config[:]), hex.EncodeToString(b.wire[:])})
	return string(value)
}

func decodeOpenAIAgentRecoveryBinding(raw string) (openAIAgentRecoveryBinding, bool, error) {
	b := openAIAgentRecoveryBinding{expiresAt: time.Now().Add(time.Minute)}
	if raw == "" {
		return b, false, nil
	}
	if raw == "failed" {
		b.failed = true
		return b, true, nil
	}
	if pending, route, ok := decodeOpenAIAgentRecoveryPending(raw); ok {
		b, found, err := decodeOpenAIAgentRecoveryBinding(route)
		b.pending = pending
		return b, found, err
	}
	if raw == "conflict" {
		b.conflicted = true
		return b, true, nil
	}
	var record []json.RawMessage
	var version int
	var config, wire string
	invalid := errors.New("invalid encrypted agent ownership record")
	if json.Unmarshal([]byte(raw), &record) != nil || len(record) != 4 ||
		json.Unmarshal(record[0], &version) != nil || version != 1 ||
		json.Unmarshal(record[1], &b.accountID) != nil || b.accountID <= 0 ||
		json.Unmarshal(record[2], &config) != nil || json.Unmarshal(record[3], &wire) != nil {
		return b, false, invalid
	}
	configBytes, err := hex.DecodeString(config)
	if err != nil || len(configBytes) != 32 {
		return b, false, invalid
	}
	wireBytes, err := hex.DecodeString(wire)
	if err != nil || len(wireBytes) != 32 {
		return b, false, invalid
	}
	copy(b.config[:], configBytes)
	copy(b.wire[:], wireBytes)
	return b, true, nil
}

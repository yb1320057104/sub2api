package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheLiveCallIdentityAndController(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache, ok := NewGatewayCache(client).(service.LiveCallStore)
	require.True(t, ok)
	otherInstance, ok := NewGatewayCache(client).(service.LiveCallStore)
	require.True(t, ok)
	record := &service.LiveCallRecord{
		CallID:                "call_secret",
		CallHash:              HashLiveCallID("call_secret"),
		AccountID:             11,
		APIKeyID:              22,
		UserID:                33,
		GroupID:               44,
		LeaseID:               "lease",
		Model:                 "gpt-live-test",
		AttestationCiphertext: "encrypted-attestation",
		CreatedAt:             time.Now(),
		ExpiresAt:             time.Now().Add(time.Hour),
		Controller:            service.LiveControllerPending,
	}
	require.NoError(t, cache.SaveLiveCall(context.Background(), record, time.Hour))

	loaded, err := otherInstance.GetLiveCall(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Equal(t, record.CallID, loaded.CallID)
	require.Equal(t, record.AccountID, loaded.AccountID)
	require.Equal(t, record.AttestationCiphertext, loaded.AttestationCiphertext)

	claimed, err := cache.ClaimLiveController(context.Background(), record.CallHash, service.LiveControllerObserver, "observer-1")
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.ClaimLiveController(context.Background(), record.CallHash, service.LiveControllerProxy, "proxy-1")
	require.NoError(t, err)
	require.True(t, claimed)
	controller, err := cache.GetLiveController(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Equal(t, service.LiveControllerProxy, controller)

	released, err := cache.ReleaseLiveController(context.Background(), record.CallHash, "proxy-1")
	require.NoError(t, err)
	require.True(t, released)
	closed, err := cache.MarkLiveCallClosed(context.Background(), record.CallHash, time.Hour)
	require.NoError(t, err)
	require.True(t, closed)
	closed, err = cache.MarkLiveCallClosed(context.Background(), record.CallHash, time.Hour)
	require.NoError(t, err)
	require.False(t, closed)
}

func TestGatewayCacheLiveAuditSnapshotPrivateRoundTrip(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewGatewayCache(client).(service.LiveCallStore)
	group, score := int64(44), 65.0
	record := &service.LiveCallRecord{CallID: "call_audit", CallHash: HashLiveCallID("call_audit"), UserID: 33, GroupID: group,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		AuditAction: &service.CyberPolicyAuditBlockInput{EventKey: "server-private:http", UserID: 33, GroupID: &group, PolicyAction: "charge_allow", RiskScore: &score}}
	require.NoError(t, store.SaveLiveCall(context.Background(), record, time.Hour))
	loaded, err := store.GetLiveCall(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Equal(t, record.AuditAction, loaded.AuditAction)
	// A refreshed legacy record must not inherit previous optional evidence.
	record.AuditAction = nil
	require.NoError(t, store.SaveLiveCall(context.Background(), record, time.Hour))
	loaded, err = store.GetLiveCall(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Nil(t, loaded.AuditAction)
	// Invalid optional data does not prevent normal Live identity recovery.
	require.NoError(t, client.HSet(context.Background(), liveCallKey(record.CallHash), "audit_action", "malformed").Err())
	loaded, err = store.GetLiveCall(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Nil(t, loaded.AuditAction)
	require.Equal(t, record.UserID, loaded.UserID)
}

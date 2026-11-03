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
		ProxyID:               73,
		ProxyLeaseID:          "proxy-live-lease",
		ProxyLeaseManaged:     true,
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
	require.Equal(t, record.ProxyID, loaded.ProxyID)
	require.Equal(t, record.ProxyLeaseID, loaded.ProxyLeaseID)
	require.True(t, loaded.ProxyLeaseManaged)
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

func TestGatewayCacheLegacyLiveProxyUpgradePreservesControllerAndOnlyWinsOnce(t *testing.T) {
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewGatewayCache(client)
	store := cache.(service.LiveCallStore)
	upgrader := cache.(service.LiveProxyLeaseStore)
	record := &service.LiveCallRecord{CallID: "legacy", CallHash: "legacy-hash", Controller: service.LiveControllerProxy, ControllerOwner: "new-controller", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	require.NoError(t, store.SaveLiveCall(t.Context(), record, time.Minute))
	won, err := upgrader.AttachLiveProxyLease(t.Context(), record.CallHash, 7, "first-lease")
	require.NoError(t, err)
	require.True(t, won)
	won, err = upgrader.AttachLiveProxyLease(t.Context(), record.CallHash, 8, "loser-lease")
	require.NoError(t, err)
	require.False(t, won)
	loaded, err := store.GetLiveCall(t.Context(), record.CallHash)
	require.NoError(t, err)
	require.Equal(t, int64(7), loaded.ProxyID)
	require.Equal(t, "first-lease", loaded.ProxyLeaseID)
	require.True(t, loaded.ProxyLeaseManaged)
	require.Equal(t, service.LiveControllerProxy, loaded.Controller)
	require.Equal(t, "new-controller", loaded.ControllerOwner)
	_, err = store.MarkLiveCallClosed(t.Context(), record.CallHash, time.Minute)
	require.NoError(t, err)
	won, err = upgrader.AttachLiveProxyLease(t.Context(), record.CallHash, 9, "after-close")
	require.NoError(t, err)
	require.False(t, won)
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

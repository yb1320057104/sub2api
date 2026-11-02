//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAdminProxyPartialUpdatePreservesOmittedSettings(t *testing.T) {
	for _, input := range []*UpdateProxyInput{{Status: "inactive"}, {Name: "renamed"}, {Host: "new.example"}} {
		expiry := time.Now().Add(24 * time.Hour)
		backup := int64(10)
		original := &Proxy{ID: 9, Name: "original", Host: "old.example", Status: StatusActive, ExpiresAt: &expiry, FallbackMode: FallbackModeProxy, BackupProxyID: &backup, ExpiryWarnDays: 7}
		repo := &updatingProxyRepoStub{proxyRepoStub: &proxyRepoStub{}, proxy: original}
		svc := &adminServiceImpl{proxyRepo: repo}
		got, err := svc.UpdateProxy(context.Background(), 9, input)
		require.NoError(t, err)
		require.Equal(t, original.ExpiresAt, got.ExpiresAt)
		require.Equal(t, original.FallbackMode, got.FallbackMode)
		require.Equal(t, original.BackupProxyID, got.BackupProxyID)
		require.Equal(t, original.ExpiryWarnDays, got.ExpiryWarnDays)
		require.Equal(t, 1, repo.updateCalls)
	}
}

func TestAdminProxyPartialUpdateClearsAndSetsSettings(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	backup := int64(10)
	zero := 0
	repo := &updatingProxyRepoStub{proxyRepoStub: &proxyRepoStub{}, proxy: &Proxy{ID: 9, ExpiresAt: &expiry, FallbackMode: FallbackModeProxy, BackupProxyID: &backup, ExpiryWarnDays: 7}}
	svc := &adminServiceImpl{proxyRepo: repo}
	got, err := svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{
		ClearExpiresAt: true, FallbackMode: FallbackModeNone, ClearBackupID: true, ExpiryWarnDays: &zero,
	})
	require.NoError(t, err)
	require.Nil(t, got.ExpiresAt)
	require.Nil(t, got.BackupProxyID)
	require.Equal(t, FallbackModeNone, got.FallbackMode)
	require.Zero(t, got.ExpiryWarnDays)
	days := 3
	got, err = svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{
		ExpiresAt: &expiry, FallbackMode: FallbackModeProxy, BackupProxyID: &backup, ExpiryWarnDays: &days,
	})
	require.NoError(t, err)
	require.Equal(t, &expiry, got.ExpiresAt)
	require.Equal(t, &backup, got.BackupProxyID)
	require.Equal(t, FallbackModeProxy, got.FallbackMode)
	require.Equal(t, days, got.ExpiryWarnDays)
}

func TestAdminProxyPartialUpdateValidatesMergedFallback(t *testing.T) {
	backup := int64(10)
	self := int64(9)
	negative := -1
	for _, tc := range []struct {
		name      string
		input     UpdateProxyInput
		wantError bool
	}{
		{name: "reuse existing backup", input: UpdateProxyInput{FallbackMode: FallbackModeProxy}},
		{name: "clear required backup", input: UpdateProxyInput{ClearBackupID: true}, wantError: true},
		{name: "self backup", input: UpdateProxyInput{BackupProxyID: &self}, wantError: true},
		{name: "negative warning", input: UpdateProxyInput{ExpiryWarnDays: &negative}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &updatingProxyRepoStub{proxyRepoStub: &proxyRepoStub{}, proxy: &Proxy{ID: 9, FallbackMode: FallbackModeProxy, BackupProxyID: &backup}}
			svc := &adminServiceImpl{proxyRepo: repo}
			_, err := svc.UpdateProxy(context.Background(), 9, &tc.input)
			if tc.wantError {
				require.Error(t, err)
				require.Zero(t, repo.updateCalls)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type protocolProxyRepoStub struct {
	proxyRepoStub
	proxy       *Proxy
	protocolIDs []int64
	errors      map[int64]error
}

func (r *protocolProxyRepoStub) UpdateProtocol(_ context.Context, id int64, protocol string) error {
	r.protocolIDs = append(r.protocolIDs, id)
	if err := r.errors[id]; err != nil {
		return err
	}
	if r.proxy != nil {
		r.proxy.Protocol = protocol
	}
	return nil
}

type protocolLatencyCacheStub struct {
	ProxyLatencyCache
	deletedIDs []int64
	err        error
}

func (c *protocolLatencyCacheStub) DeleteProxyLatency(_ context.Context, id int64) error {
	c.deletedIDs = append(c.deletedIDs, id)
	return c.err
}

func TestAdminProxyBatchUpdateProtocolChangesOnlyProtocol(t *testing.T) {
	expiry := time.Now().Add(24 * time.Hour)
	backup := int64(12)
	repo := &protocolProxyRepoStub{
		proxy: &Proxy{
			ID: 9, Name: "kept", Protocol: "socks5", Host: "proxy.example", Port: 1080,
			Username: "user", Password: "pass", Status: StatusActive, ExpiresAt: &expiry,
			FallbackMode: FallbackModeProxy, BackupProxyID: &backup, ExpiryWarnDays: 3,
		},
	}
	cache := &protocolLatencyCacheStub{}
	svc := &adminServiceImpl{proxyRepo: repo, proxyLatencyCache: cache}
	result, err := svc.BatchUpdateProxyProtocol(context.Background(), []int64{9, 9}, "https")
	require.NoError(t, err)
	require.Equal(t, []int64{9}, result.UpdatedIDs)
	require.Empty(t, result.Skipped)
	require.Equal(t, []int64{9}, repo.protocolIDs)
	require.Equal(t, []int64{9}, cache.deletedIDs)
	require.Equal(t, "https", repo.proxy.Protocol)
	require.Equal(t, "kept", repo.proxy.Name)
	require.Equal(t, "proxy.example", repo.proxy.Host)
	require.Equal(t, 1080, repo.proxy.Port)
	require.Equal(t, "user", repo.proxy.Username)
	require.Equal(t, "pass", repo.proxy.Password)
	require.Equal(t, StatusActive, repo.proxy.Status)
	require.Equal(t, &backup, repo.proxy.BackupProxyID)
	require.Equal(t, 3, repo.proxy.ExpiryWarnDays)
	require.Equal(t, &expiry, repo.proxy.ExpiresAt)
}

func TestAdminProxyBatchUpdateProtocolValidatesSchemeAndLimit(t *testing.T) {
	repo := &protocolProxyRepoStub{proxy: &Proxy{ID: 9}}
	svc := &adminServiceImpl{proxyRepo: repo}
	_, err := svc.BatchUpdateProxyProtocol(context.Background(), []int64{9}, "socks5")
	require.Error(t, err)
	require.Empty(t, repo.protocolIDs)

	ids := make([]int64, ProxyBatchProtocolMaxIDs+1)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	_, err = svc.BatchUpdateProxyProtocol(context.Background(), ids, "http")
	require.Error(t, err)
	require.Empty(t, repo.protocolIDs)
	for _, invalid := range [][]int64{nil, {}, {0}, {-1}, {9, 0}} {
		_, err := svc.BatchUpdateProxyProtocol(context.Background(), invalid, "http")
		require.Error(t, err)
		require.Empty(t, repo.protocolIDs)
	}

	// Duplicate IDs do not consume the 500 distinct-proxy quota.
	ids = make([]int64, ProxyBatchProtocolMaxIDs+1)
	for i := range ids {
		ids[i] = 9
	}
	result, err := svc.BatchUpdateProxyProtocol(context.Background(), ids, "http")
	require.NoError(t, err)
	require.Equal(t, []int64{9}, result.UpdatedIDs)
	require.Equal(t, []int64{9}, repo.protocolIDs)
}

func TestAdminProxyBatchUpdateProtocolPartialFailureIsSanitized(t *testing.T) {
	repo := &protocolProxyRepoStub{errors: map[int64]error{
		2: ErrProxyNotFound,
		3: errors.New("database rejected socks5://secret-user:secret-password@private.example"),
		4: context.Canceled,
	}}
	cache := &protocolLatencyCacheStub{err: errors.New("cache unavailable")}
	svc := &adminServiceImpl{proxyRepo: repo, proxyLatencyCache: cache}
	result, err := svc.BatchUpdateProxyProtocol(context.Background(), []int64{1, 2, 3, 4, 5}, "http")
	require.NoError(t, err)
	require.Equal(t, []int64{1, 5}, result.UpdatedIDs)
	require.Equal(t, []int64{1, 5}, cache.deletedIDs)
	require.Equal(t, []ProxyBatchProtocolSkipped{
		{ID: 2, Reason: "proxy not found"},
		{ID: 3, Reason: "proxy protocol update failed"},
		{ID: 4, Reason: "proxy protocol update interrupted"},
	}, result.Skipped)
}

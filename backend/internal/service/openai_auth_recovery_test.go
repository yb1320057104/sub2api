//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type authRecoveryRepo struct {
	AccountRepository
	account   *Account
	marks     int
	recovered int
}

func (r *authRecoveryRepo) GetByID(context.Context, int64) (*Account, error) { return r.account, nil }
func (r *authRecoveryRepo) SetTempUnschedulable(_ context.Context, _ int64, until time.Time, reason string) error {
	r.account.TempUnschedulableUntil = &until
	r.account.TempUnschedulableReason = reason
	return nil
}
func (r *authRecoveryRepo) SetError(_ context.Context, _ int64, reason string) error {
	r.account.Status = StatusError
	r.account.ErrorMessage = reason
	return nil
}
func (r *authRecoveryRepo) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.account.Credentials = shallowCopyMap(credentials)
	return nil
}
func (r *authRecoveryRepo) MarkOpenAIAuthRejected(_ context.Context, _ int64, token string, terminal bool, _ time.Time) (bool, error) {
	if token != r.account.GetOpenAIAccessToken() {
		return false, nil
	}
	r.marks++
	r.account.Extra = map[string]any{OpenAIAuthRecoveryKey: map[string]any{"access_token_digest": OpenAIAccessTokenDigest(token), "requires_relogin": terminal}}
	return true, nil
}
func (r *authRecoveryRepo) RecoverOpenAIAuthIfUnchanged(_ context.Context, _ int64, token string) (bool, error) {
	if token != r.account.GetOpenAIAccessToken() {
		return false, nil
	}
	r.recovered++
	delete(r.account.Extra, OpenAIAuthRecoveryKey)
	return true, nil
}

type rejectedTokenExecutor struct {
	calls int
	err   error
}

func (e *rejectedTokenExecutor) CacheKey(a *Account) string { return OpenAITokenCacheKey(a) }
func (e *rejectedTokenExecutor) CanRefresh(a *Account) bool { return true }
func (e *rejectedTokenExecutor) NeedsRefresh(a *Account, _ time.Duration) bool {
	return OpenAIAuthRecoveryPending(a)
}
func (e *rejectedTokenExecutor) Refresh(_ context.Context, a *Account) (map[string]any, error) {
	e.calls++
	if e.err != nil {
		return nil, e.err
	}
	credentials := shallowCopyMap(a.Credentials)
	credentials["access_token"] = "new-accepted-token"
	return credentials, nil
}
func authRecoveryAccount() *Account {
	return &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"access_token": "rejected-token", "refresh_token": "synthetic-refresh", "expires_at": time.Now().Add(24 * time.Hour).Format(time.RFC3339)}, Extra: map[string]any{}}
}
func TestOpenAIAuthRecovery401RefreshesUnexpiredRejectedToken(t *testing.T) {
	a := authRecoveryAccount()
	repo := &authRecoveryRepo{account: a}
	rate := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	require.True(t, rate.HandleUpstreamError(context.Background(), a, http.StatusUnauthorized, http.Header{}, []byte(`{"error":{"code":"invalid_token"}}`)))
	require.Equal(t, 1, repo.marks)
	require.True(t, OpenAIAuthRecoveryPending(a))
	require.True(t, NewOpenAITokenRefresher(nil, repo).NeedsRefresh(a, time.Minute))
	cache := newOpenAITokenCacheStub()
	cache.tokens[OpenAITokenCacheKey(a)] = "rejected-token"
	provider := NewOpenAITokenProvider(repo, cache, nil)
	executor := &rejectedTokenExecutor{}
	provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), executor)
	token, err := provider.GetAccessToken(context.Background(), a)
	require.NoError(t, err)
	require.Equal(t, "new-accepted-token", token)
	require.Equal(t, 1, executor.calls)
	require.Equal(t, 1, repo.recovered)
	require.False(t, OpenAIAuthRecoveryPending(a))
}
func TestOpenAIAuthRecoveryNeverReusesRejectedTokenOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		lock bool
		err  error
	}{{"refresh_failed", true, errors.New("transport unavailable")}, {"lock_held", false, nil}} {
		t.Run(tc.name, func(t *testing.T) {
			a := authRecoveryAccount()
			repo := &authRecoveryRepo{account: a}
			_, err := repo.MarkOpenAIAuthRejected(context.Background(), 42, a.GetOpenAIAccessToken(), false, time.Now().Add(10*time.Minute))
			require.NoError(t, err)
			cache := newOpenAITokenCacheStub()
			cache.lockAcquired = tc.lock
			provider := NewOpenAITokenProvider(repo, cache, nil)
			provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), &rejectedTokenExecutor{err: tc.err})
			token, err := provider.GetAccessToken(context.Background(), a)
			require.Error(t, err)
			require.Empty(t, token)
		})
	}
}
func TestOpenAIAuthRecoveryLate401DoesNotQuarantineFreshToken(t *testing.T) {
	current := authRecoveryAccount()
	current.Credentials["access_token"] = "fresh-token"
	repo := &authRecoveryRepo{account: current}
	stale := authRecoveryAccount()
	rate := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	require.False(t, rate.HandleUpstreamError(context.Background(), stale, http.StatusUnauthorized, http.Header{}, []byte(`{"error":{"code":"token_revoked"}}`)))
	require.Equal(t, StatusActive, current.Status)
	require.Empty(t, current.ErrorMessage)
	require.Nil(t, current.TempUnschedulableUntil)

	// Token acquisition may have refreshed after selection. The attempt context
	// identifies the sent token instead of marking the old selection snapshot.
	ctx := withOpenAIUsedAccessToken(context.Background(), stale, "fresh-token")
	require.True(t, rate.HandleUpstreamError(ctx, stale, http.StatusUnauthorized, http.Header{}, []byte(`{"error":{"code":"invalid_token"}}`)))
	require.True(t, OpenAIAuthRecoveryPending(current))
}

func TestOpenAIAuthRecoverySignalBelongsToTokenVersion(t *testing.T) {
	a := authRecoveryAccount()
	repo := &authRecoveryRepo{account: a}
	_, err := repo.MarkOpenAIAuthRejected(context.Background(), 42, a.GetOpenAIAccessToken(), true, time.Now().Add(10*time.Minute))
	require.NoError(t, err)
	require.True(t, OpenAIAuthNeedsRelogin(a))
	a.Credentials["access_token"] = "concurrent-new-token"
	require.False(t, OpenAIAuthRecoveryPending(a))
	require.False(t, OpenAIAuthNeedsRelogin(a))
	marked, err := repo.MarkOpenAIAuthRejected(context.Background(), 42, "rejected-token", true, time.Now().Add(10*time.Minute))
	require.NoError(t, err)
	require.False(t, marked)
}

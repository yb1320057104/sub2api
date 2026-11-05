//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type pk52CandyStateRepo struct {
	openAIAccountTestRepo
	mu        sync.Mutex
	snapshots []AccountCandyCredentialStateSnapshot
	updated   bool
	err       error
	errors    []error
	ctxErr    error
}

func (r *pk52CandyStateRepo) SetAccountCandyCredentialErrorIfMatch(ctx context.Context, snapshot AccountCandyCredentialStateSnapshot) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshots = append(r.snapshots, snapshot)
	r.ctxErr = ctx.Err()
	err := r.err
	if len(r.errors) > 0 {
		err, r.errors = r.errors[0], r.errors[1:]
	}
	if err != nil {
		return false, err
	}
	updated := r.updated
	r.updated = false
	return updated, nil
}

type pk52CandyTokenInvalidator struct {
	mu  sync.Mutex
	ids []int64
	err error
}

func (i *pk52CandyTokenInvalidator) InvalidateToken(_ context.Context, account *Account) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.ids = append(i.ids, account.ID)
	return i.err
}

func pk52CandyAccount() *Account {
	return &Account{ID: 52, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "pk52-secret", "refresh_token": "pk52-refresh", "_token_version": 2}}
}

func TestPK52CandyCredentialProbeState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		updated bool
		dbErr   error
		want    string
		writes  int
		cache   int
	}{
		{"unauthorized", 401, `{"error":{"code":"token_invalidated","message":"pk52-secret invalidated oauth token"}}`, true, nil, "标为错误并暂停调度", 1, 1},
		{"stale", 401, `{"error":{"message":"unauthorized"}}`, false, nil, "保留当前账号状态", 1, 0},
		{"database_failure", 401, `{"error":{"message":"unauthorized"}}`, false, errors.New("private-database-secret"), "写入失败", 1, 0},
		{"forbidden", 403, `{"error":{"message":"401 invalidated oauth token"}}`, true, nil, "HTTP 403", 0, 0},
		{"rate_limit", 429, `{"error":{"message":"401 invalidated oauth token"}}`, true, nil, "HTTP 429", 0, 0},
		{"body_only", 200, `{"error":{"message":"HTTP 401 invalidated oauth token"}}`, true, nil, "HTTP 200", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &pk52CandyStateRepo{updated: tc.updated, err: tc.dbErr}
			cache := &pk52CandyTokenInvalidator{}
			upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(tc.status, tc.body)}}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}, candyTokenCacheInvalidator: cache}
			account := pk52CandyAccount()
			_, err := svc.runAccountCandyProbe(context.Background(), account, account, defaultAccountCandyConfig())
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), "pk52-secret")
			require.NotContains(t, err.Error(), "private-database-secret")
			require.Len(t, repo.snapshots, tc.writes)
			require.Len(t, cache.ids, tc.cache)
			require.Len(t, upstream.requests, 1)
			require.Zero(t, repo.setErrorID, "unconditional SetError must never be used")
		})
	}
}

func TestPK52CandyCredentialSnapshotAndTemporaryRoute(t *testing.T) {
	owner := pk52CandyAccount()
	originalProxy, temporaryProxy := int64(7), int64(8)
	owner.ProxyID = &originalProxy
	route := *owner
	route.ProxyID = &temporaryProxy
	snapshot := accountCandyCredentialSnapshot(&route, owner)
	require.NotNil(t, snapshot)
	require.Equal(t, int64(7), *snapshot.CredentialProxyID)
	owner.Credentials["access_token"] = "new-token"
	originalProxy = 99
	require.Contains(t, snapshot.CredentialsJSON, "pk52-secret")
	require.NotContains(t, snapshot.CredentialsJSON, "new-token")
	require.Equal(t, int64(7), *snapshot.CredentialProxyID)
	shadow := *pk52CandyAccount()
	shadow.ID = 53
	shadow.ParentAccountID = &owner.ID
	shadow.Credentials = map[string]any{}
	snapshot = accountCandyCredentialSnapshot(&shadow, owner)
	require.Equal(t, owner.ID, snapshot.CredentialAccountID)
	require.Equal(t, shadow.ID, snapshot.AccountID)
	other := int64(99)
	shadow.ParentAccountID = &other
	require.Nil(t, accountCandyCredentialSnapshot(&shadow, owner))
}

func TestPK52CandyCredentialConcurrentFailuresAndCancellation(t *testing.T) {
	account := pk52CandyAccount()
	repo := &pk52CandyStateRepo{updated: true}
	cache := &pk52CandyTokenInvalidator{}
	svc := &AccountTestService{accountRepo: repo, candyTokenCacheInvalidator: cache}
	snapshot := accountCandyCredentialSnapshot(account, account)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_ = svc.accountCandyCredentialFailure(ctx, snapshot, account, accountCandyHTTPFailure(401, nil))
		}()
	}
	workers.Wait()
	require.Len(t, repo.snapshots, 8)
	require.Len(t, cache.ids, 1)
	require.NoError(t, repo.ctxErr)
	require.Zero(t, repo.setErrorID)
}

func TestPK52CandyCredentialShadowCacheAndUnavailable(t *testing.T) {
	owner := pk52CandyAccount()
	shadow := *owner
	shadow.ID, shadow.ParentAccountID = 53, &owner.ID
	repo := &pk52CandyStateRepo{updated: true}
	cache := &pk52CandyTokenInvalidator{err: errors.New("cache-secret")}
	svc := &AccountTestService{accountRepo: repo, candyTokenCacheInvalidator: cache}
	err := svc.accountCandyCredentialFailure(context.Background(), accountCandyCredentialSnapshot(&shadow, owner), owner, accountCandyHTTPFailure(401, nil))
	require.ErrorContains(t, err, "凭据母账号")
	require.ErrorContains(t, err, "缓存清理暂未完成")
	require.NotContains(t, err.Error(), "cache-secret")
	require.Equal(t, []int64{owner.ID}, cache.ids)
	svc.accountRepo = &openAIAccountTestRepo{}
	err = svc.accountCandyCredentialFailure(context.Background(), accountCandyCredentialSnapshot(owner, owner), owner, accountCandyHTTPFailure(401, nil))
	require.ErrorContains(t, err, "未确认自动停调")
	plain := errors.New("HTTP 401 invalidated oauth token")
	require.Same(t, plain, svc.accountCandyCredentialFailure(context.Background(), nil, owner, plain))
}

type pk52CandySQLState string

func (e pk52CandySQLState) Error() string    { return "database failure" }
func (e pk52CandySQLState) SQLState() string { return string(e) }

func TestPK52CandyCredentialDeadlockOnlyRetries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		errors   []error
		attempts int
		updated  bool
	}{
		{"deadlock_then_success", []error{pk52CandySQLState("40P01"), nil}, 2, true},
		{"deadlock_exhausted", []error{pk52CandySQLState("40P01"), pk52CandySQLState("40P01"), pk52CandySQLState("40P01")}, 3, false},
		{"connection_unknown", []error{errors.New("connection reset")}, 1, false},
		{"other_sqlstate", []error{pk52CandySQLState("40001")}, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := pk52CandyAccount()
			repo := &pk52CandyStateRepo{updated: true, errors: tc.errors}
			cache := &pk52CandyTokenInvalidator{}
			svc := &AccountTestService{accountRepo: repo, candyTokenCacheInvalidator: cache}
			err := svc.accountCandyCredentialFailure(context.Background(), accountCandyCredentialSnapshot(account, account), account, accountCandyHTTPFailure(401, nil))
			require.Len(t, repo.snapshots, tc.attempts)
			if tc.updated {
				require.ErrorContains(t, err, "标为错误并暂停调度")
				require.Len(t, cache.ids, 1)
			} else {
				require.ErrorContains(t, err, "写入失败")
				require.Empty(t, cache.ids)
			}
		})
	}
}

func TestPK52CandyCredentialInactiveOwnerIsPreserved(t *testing.T) {
	for _, status := range []string{StatusError, "disabled"} {
		account := pk52CandyAccount()
		account.Status = status
		snapshot := accountCandyCredentialSnapshot(account, account)
		require.Nil(t, snapshot)
		repo := &pk52CandyStateRepo{updated: true}
		svc := &AccountTestService{accountRepo: repo}
		err := svc.accountCandyCredentialFailure(context.Background(), snapshot, account, accountCandyHTTPFailure(401, nil))
		require.ErrorContains(t, err, "未自动更改账号")
		require.Empty(t, repo.snapshots)
	}
}

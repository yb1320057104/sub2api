package service

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrProxyCapacity         = errors.New("account proxies are at concurrency capacity")
	ErrProxyUnavailable      = errors.New("account has no available bound proxy")
	ErrProxyLeaseUnavailable = errors.New("proxy concurrency service unavailable")
)

// ProxyConcurrencyCache provides cross-instance leases, including unlimited
// proxies so the administrator sees actual usage. Missing leases are never
// recreated by Refresh: an expired owner must stop its upstream request.
type ProxyConcurrencyCache interface {
	AcquireProxy(context.Context, int64, int, string) (bool, error)
	RefreshProxy(context.Context, int64, string) (bool, error)
	ReleaseProxy(context.Context, int64, string) error
	GetProxyConcurrencyBatch(context.Context, []int64) (map[int64]int, error)
}

type AccountProxyRouter struct {
	reader AccountProxyBindingReader
	cache  ProxyConcurrencyCache
}

func NewAccountProxyRouter(reader AccountProxyBindingReader, cache ProxyConcurrencyCache) *AccountProxyRouter {
	return &AccountProxyRouter{reader: reader, cache: cache}
}

func (r *AccountProxyRouter) GetProxyConcurrencyBatch(ctx context.Context, ids []int64) (map[int64]int, error) {
	if r == nil || r.cache == nil {
		return nil, ErrProxyLeaseUnavailable
	}
	return r.cache.GetProxyConcurrencyBatch(ctx, ids)
}

type accountProxyContextKey struct{}
type accountProxyRequest struct {
	mu       sync.Mutex
	selected map[int64]int64
}

// Enabled only on authenticated gateway requests. Authorization, radar and
// explicit administrator probes keep their deliberately selected egress.
func WithAccountProxyRouting(ctx context.Context) context.Context {
	if _, ok := ctx.Value(accountProxyContextKey{}).(*accountProxyRequest); ok {
		return ctx
	}
	return context.WithValue(ctx, accountProxyContextKey{}, &accountProxyRequest{selected: make(map[int64]int64)})
}

type AccountProxyRoute struct {
	Context context.Context
	URL     string
	ProxyID int64
	LeaseID string
	close   func()
	handoff func() bool
}

// Handoff stops the request-owned keeper without releasing its Redis slot.
// The persisted Live lifecycle must refresh/release that same slot from now on.
func (r *AccountProxyRoute) Handoff() bool {
	if r == nil {
		return false
	}
	if r.handoff == nil {
		return r.LeaseID == ""
	}
	return r.handoff()
}

func (r *AccountProxyRoute) Close() {
	if r != nil && r.close != nil {
		r.close()
	}
}

// Acquire chooses once per downstream request and keeps that choice for any
// retries. A supplied pinned proxy is used for an established WS continuation.
func (r *AccountProxyRouter) Acquire(ctx context.Context, accountID int64, fallback string, pinned int64) (*AccountProxyRoute, error) {
	return r.acquire(ctx, ctx, accountID, fallback, pinned)
}

func (r *AccountProxyRouter) acquire(ctx, routeParent context.Context, accountID int64, fallback string, pinned int64) (*AccountProxyRoute, error) {
	if borrowed, ok := ctx.Value(borrowedAccountProxyContextKey{}).(*borrowedAccountProxyRoute); ok && borrowed.accountID == accountID {
		if borrowed.route.Context.Err() != nil {
			return nil, ErrProxyLeaseUnavailable
		}
		borrowCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(borrowed.route.Context, cancel)
		return &AccountProxyRoute{Context: borrowCtx, URL: borrowed.route.URL, ProxyID: borrowed.route.ProxyID, LeaseID: borrowed.route.LeaseID, close: func() { stop(); cancel() }}, nil
	}
	state, enabled := ctx.Value(accountProxyContextKey{}).(*accountProxyRequest)
	if r == nil || !enabled || accountID <= 0 {
		return &AccountProxyRoute{Context: ctx, URL: fallback}, nil
	}
	if r.reader == nil || r.cache == nil {
		return nil, ErrProxyLeaseUnavailable
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	bindings, err := r.reader.GetAccountProxyBindings(ctx, accountID)
	if err != nil {
		return nil, ErrProxyLeaseUnavailable
	}
	if len(bindings) == 0 {
		if pinned > 0 || state.selected[accountID] > 0 {
			return nil, ErrProxyUnavailable
		}
		return &AccountProxyRoute{Context: ctx}, nil
	}
	selected := state.selected[accountID]
	if pinned > 0 {
		if selected > 0 && selected != pinned {
			return nil, ErrProxyUnavailable
		}
		selected = pinned
	}
	available := false
	leaseID := uuid.NewString()
	for _, i := range rand.Perm(len(bindings)) {
		binding := bindings[i]
		if selected > 0 && binding.ProxyID != selected {
			continue
		}
		if binding.Proxy == nil || !binding.Proxy.IsActive() || binding.Proxy.IsExpired(time.Now()) {
			continue
		}
		available = true
		operationCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		acquired, acquireErr := r.cache.AcquireProxy(operationCtx, binding.ProxyID, binding.MaxConcurrency, leaseID)
		cancel()
		if acquireErr != nil {
			return nil, ErrProxyLeaseUnavailable
		}
		if !acquired {
			continue
		}
		state.selected[accountID] = binding.ProxyID
		return r.newRoute(routeParent, binding, leaseID), nil
	}
	if available {
		return nil, ErrProxyCapacity
	}
	return nil, ErrProxyUnavailable
}

func (r *AccountProxyRouter) newRoute(ctx context.Context, binding AccountProxyBinding, leaseID string) *AccountProxyRoute {
	routeCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	stop := make(chan struct{})
	closeRoute := func() {
		once.Do(func() {
			cancel()
			close(stop)
			opCtx, opCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer opCancel()
			_ = r.cache.ReleaseProxy(opCtx, binding.ProxyID, leaseID)
		})
	}
	handoff := func() bool {
		transferred := false
		once.Do(func() {
			transferred = true
			close(stop)
			cancel()
		})
		return transferred
	}
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				closeRoute()
				return
			case <-ticker.C:
				opCtx, opCancel := context.WithTimeout(routeCtx, 3*time.Second)
				ok, err := r.cache.RefreshProxy(opCtx, binding.ProxyID, leaseID)
				opCancel()
				if err != nil || !ok {
					closeRoute()
					return
				}
			}
		}
	}()
	return &AccountProxyRoute{Context: routeCtx, URL: binding.Proxy.URL(), ProxyID: binding.ProxyID, LeaseID: leaseID, close: closeRoute, handoff: handoff}
}

// The transport owns the shared router; WS/Live paths obtain the same instance
// without adding optional state to every existing gateway constructor.
type accountProxyRouterProvider interface{ AccountProxyRouter() *AccountProxyRouter }

func (s *OpenAIGatewayService) accountProxyRouter() *AccountProxyRouter {
	if s == nil {
		return nil
	}
	if provider, ok := s.httpUpstream.(accountProxyRouterProvider); ok {
		return provider.AccountProxyRouter()
	}
	return nil
}

func (s *OpenAIGatewayService) acquireWSProxy(ctx context.Context, account *Account, preferred string) (*AccountProxyRoute, error) {
	var pinned int64
	if preferred != "" && s.accountProxyRouter() != nil {
		pool := s.getOpenAIWSConnPool()
		if pool != nil {
			ap := pool.getOrCreateAccountPool(account.ID)
			ap.mu.Lock()
			if conn := ap.conns[preferred]; conn != nil {
				pinned = conn.proxyID
			}
			ap.mu.Unlock()
		}
	}
	return s.accountProxyRouter().Acquire(ctx, account.ID, resolveAccountProxyURL(account), pinned)
}

func selectedAccountProxyID(ctx context.Context, accountID int64) int64 {
	if state, ok := ctx.Value(accountProxyContextKey{}).(*accountProxyRequest); ok {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.selected[accountID]
	}
	return 0
}

func proxyAdmissionFailover(err error) *UpstreamFailoverError {
	if !errors.Is(err, ErrProxyCapacity) && !errors.Is(err, ErrProxyUnavailable) && !errors.Is(err, ErrProxyLeaseUnavailable) {
		return nil
	}
	return &UpstreamFailoverError{StatusCode: 503, ResponseBody: []byte(`{"error":{"type":"proxy_capacity_unavailable","message":"No bound proxy capacity is currently available; retry later."}}`)}
}

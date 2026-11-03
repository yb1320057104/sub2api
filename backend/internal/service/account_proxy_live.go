package service

import (
	"context"
	"fmt"
	"time"
)

type borrowedAccountProxyContextKey struct{}
type borrowedAccountProxyRoute struct {
	accountID int64
	route     *AccountProxyRoute
}

func withBorrowedAccountProxyRoute(ctx context.Context, accountID int64, route *AccountProxyRoute) context.Context {
	return context.WithValue(ctx, borrowedAccountProxyContextKey{}, &borrowedAccountProxyRoute{accountID: accountID, route: route})
}

// AcquirePersistent reserves one slot for the entire Live call. HTTP response
// closure only releases borrowed handles. After persistence, Handoff stops the
// temporary keeper; the stored call lifecycle refreshes and releases the slot.
// A crashed process stops refreshing, so Redis removes abandoned leases by TTL.
func (r *AccountProxyRouter) AcquirePersistent(ctx context.Context, accountID int64, fallback string, pinned int64, lifetime time.Duration) (*AccountProxyRoute, error) {
	if lifetime <= 0 {
		return nil, ErrProxyUnavailable
	}
	leaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lifetime)
	route, err := r.acquire(ctx, leaseCtx, accountID, fallback, pinned)
	if err != nil {
		cancel()
		return nil, err
	}
	if route.LeaseID == "" {
		cancel()
	} else {
		context.AfterFunc(route.Context, cancel)
	}
	return route, nil
}

// ResumePersistent validates the fixed egress and refreshes only an existing
// lease. It must never acquire or resurrect a disappeared Live lease.
func (r *AccountProxyRouter) ResumePersistent(ctx context.Context, accountID, proxyID int64, leaseID string) (*AccountProxyRoute, error) {
	if proxyID == 0 && leaseID == "" {
		return &AccountProxyRoute{Context: ctx}, nil
	}
	if r == nil || r.reader == nil || r.cache == nil || proxyID <= 0 || leaseID == "" {
		return nil, ErrProxyLeaseUnavailable
	}
	bindings, err := r.reader.GetAccountProxyBindings(ctx, accountID)
	if err != nil {
		return nil, ErrProxyLeaseUnavailable
	}
	for _, binding := range bindings {
		if binding.ProxyID != proxyID || binding.Proxy == nil || !binding.Proxy.IsActive() || binding.Proxy.IsExpired(time.Now()) {
			continue
		}
		ok, err := r.cache.RefreshProxy(ctx, proxyID, leaseID)
		if err != nil || !ok {
			return nil, ErrProxyLeaseUnavailable
		}
		return &AccountProxyRoute{Context: ctx, URL: binding.Proxy.URL(), ProxyID: proxyID, LeaseID: leaseID}, nil
	}
	return nil, ErrProxyUnavailable
}

func (r *AccountProxyRouter) ReleasePersistent(ctx context.Context, proxyID int64, leaseID string) {
	if r != nil && r.cache != nil && proxyID > 0 && leaseID != "" {
		_ = r.cache.ReleaseProxy(ctx, proxyID, leaseID)
	}
}

// Existing records are upgraded once without overwriting concurrent controller
// ownership. A losing upgrade releases its new slot and uses the stored winner.
func (s *OpenAIGatewayService) ensureLiveProxyLease(ctx context.Context, record *LiveCallRecord, account *Account) error {
	if record.ProxyLeaseManaged || s.accountProxyRouter() == nil {
		return nil
	}
	store, err := s.liveStore()
	if err != nil {
		return err
	}
	upgrader, ok := store.(LiveProxyLeaseStore)
	if !ok {
		return ErrLiveUnavailable
	}
	pinned := record.ProxyID
	if pinned == 0 && account.ProxyID != nil {
		pinned = *account.ProxyID
	}
	route, err := s.accountProxyRouter().AcquirePersistent(WithAccountProxyRouting(ctx), account.ID, resolveAccountProxyURL(account), pinned, time.Until(record.ExpiresAt))
	if err != nil {
		// At capacity one, a concurrent upgrade may have consumed the only slot
		// between our stale record read and admission. Reuse its persisted lease.
		latest, getErr := store.GetLiveCall(ctx, record.CallHash)
		if getErr == nil && latest != nil && latest.ProxyLeaseManaged && latest.Controller != LiveControllerClosed {
			record.ProxyID, record.ProxyLeaseID, record.ProxyLeaseManaged = latest.ProxyID, latest.ProxyLeaseID, true
			return nil
		}
		return err
	}
	attached, attachErr := upgrader.AttachLiveProxyLease(ctx, record.CallHash, route.ProxyID, route.LeaseID)
	if attachErr != nil || !attached {
		route.Close()
		if attachErr != nil {
			return attachErr
		}
		latest, getErr := store.GetLiveCall(ctx, record.CallHash)
		if getErr != nil || latest == nil || !latest.ProxyLeaseManaged || latest.Controller == LiveControllerClosed {
			return fmt.Errorf("live proxy lease upgrade: %w", ErrLiveUnavailable)
		}
		record.ProxyID, record.ProxyLeaseID, record.ProxyLeaseManaged = latest.ProxyID, latest.ProxyLeaseID, true
		return nil
	}
	record.ProxyID, record.ProxyLeaseID, record.ProxyLeaseManaged = route.ProxyID, route.LeaseID, true
	if !route.Handoff() {
		return ErrProxyLeaseUnavailable
	}
	return nil
}

func (s *OpenAIGatewayService) releaseLiveProxyLease(record *LiveCallRecord) {
	if record == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveRedisOperationTimeout)
	defer cancel()
	s.accountProxyRouter().ReleasePersistent(ctx, record.ProxyID, record.ProxyLeaseID)
}

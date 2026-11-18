package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrKiloEgressUnavailable = errors.New("managed Kilo routing is unavailable; a registered assigned proxy is required")
	ErrKiloEgressConflict    = errors.New("public egress IP is already owned by another Kilo account")
	ErrKiloProxyOwned        = errors.New("proxy credentials are occupied by another Kilo account until its routes drain")
	ErrKiloEgressDraining    = errors.New("Kilo egress is draining; close in-flight requests and explicitly release the route")
	ErrKiloEgressCapacity    = errors.New("Kilo egress concurrency capacity reached")
	ErrKiloEgressUnstable    = errors.New("Kilo egress probe did not return the same public IP twice")
)

const (
	KiloEgressActive     = "active"
	KiloEgressUnverified = "unverified"
	KiloEgressDraining   = "draining"
	KiloEgressFailed     = "failed"
)

// KiloEgressRoute exposes proxy allocation without credentials. EgressIP is
// optional historical metadata; ownership and concurrency use proxy credentials.
type KiloEgressRoute struct {
	ID             int64             `json:"id"`
	AccountID      int64             `json:"account_id"`
	ProxyID        int64             `json:"proxy_id"`
	EgressIP       string            `json:"egress_ip"`
	State          string            `json:"state"`
	MaxConcurrency int               `json:"max_concurrency"`
	InFlight       int               `json:"in_flight"`
	ExpiredLeases  int               `json:"expired_leases"`
	Leases         []KiloEgressLease `json:"leases"`
	VerifiedAt     *time.Time        `json:"verified_at"`
	LastError      string            `json:"last_error"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	Purpose        string            `json:"purpose"`
}

// Expired leases remain owned until their response is known to be closed. This
// metadata supports recovery inspection without exposing proxy credentials.
type KiloEgressLease struct {
	ID        string    `json:"id"`
	RouteID   int64     `json:"route_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	Expired   bool      `json:"expired"`
}

// Verify registers the exact assigned proxy or fixed login proxy. Public IP is
// not an admission condition. Release closes new admission and retains current
// credential occupancy until all request leases close.
// IDs are Sub2API account IDs, not Kilo pool row IDs.
type KiloEgressManager interface {
	List(ctx context.Context, accountID int64) ([]KiloEgressRoute, error)
	Verify(ctx context.Context, accountID, proxyID int64) (*KiloEgressRoute, error)
	Release(ctx context.Context, routeID int64) error
}

type KiloEgressManagement = KiloEgressManager

// Acquire returns nil, nil for accounts that do not opt in with kilo_managed.
// Managed accounts use only an exact assigned proxy configuration. Each proxy
// credential has its own capacity; shared public IPs do not combine budgets.
type KiloEgressGuard interface {
	Acquire(ctx context.Context, accountID, proxyID int64, proxyURL string) (*KiloEgressPermit, error)
}

// KiloEgressPermit owns a database lease through response Body.Close. Context is
// cancelled when the keeper loses its lease, so callers must use it upstream.
type KiloEgressPermit struct {
	Context context.Context
	once    sync.Once
	close   func()
}

func NewKiloEgressPermit(ctx context.Context, close func()) *KiloEgressPermit {
	return &KiloEgressPermit{Context: ctx, close: close}
}

func (p *KiloEgressPermit) Close() {
	if p != nil {
		p.once.Do(func() {
			if p.close != nil {
				p.close()
			}
		})
	}
}

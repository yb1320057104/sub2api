package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

const assignedAuthProxyExtraKey = "assigned_auth_proxy_id"

var (
	errAssignedAuthProxyRequired    = errors.New("account has no assigned login proxy; assign a proxy before reauthorization")
	errAssignedAuthProxyUnavailable = errors.New("assigned account login proxy is unavailable; reauthorization was not sent")
	errAssignedAuthProxyPersistence = errors.New("assigned account login proxy could not be saved; reauthorization was not sent")
	errAssignedAuthGatewayInvalid   = errors.New("paired Kilo gateway login proxy is missing or no longer belongs to this account")
)

type assignedAuthProxyStore struct {
	account func(context.Context, int64) (*Account, error)
	proxy   func(context.Context, int64) (*Proxy, error)
	pin     func(context.Context, int64, map[string]any) error
}

type assignedAuthProxy struct {
	ID  int64
	URL string
}

func usableAssignedAuthProxy(proxy *Proxy) bool {
	return proxy != nil && proxy.ID > 0 && proxy.IsActive() && !proxy.IsExpired(time.Now()) && strings.TrimSpace(proxy.URL()) != ""
}

// A login keeps the account's assigned exit even when model requests use a pool.
// An ordinary account with no proxy assignment uses its existing direct route;
// a removed or unhealthy assigned exit never falls back to another identity.
// Kilo sources use only their reciprocal gateway's fixed login exit.
func resolveAssignedAuthProxy(ctx context.Context, account *Account, store assignedAuthProxyStore) (*assignedAuthProxy, error) {
	if account == nil || account.ID <= 0 || store.account == nil || store.proxy == nil {
		return nil, errAssignedAuthProxyRequired
	}
	fresh, err := store.account(ctx, account.ID)
	if err != nil || fresh == nil || fresh.ID != account.ID {
		return nil, errAssignedAuthProxyUnavailable
	}
	var proxyID int64
	if gatewayID := int64(ParseExtraInt(fresh.Extra["kilo_source_gateway_id"])); gatewayID > 0 {
		preparedID := int64(ParseExtraInt(fresh.Extra["kilo_source_prepared_id"]))
		gateway, readErr := store.account(ctx, gatewayID)
		if readErr != nil || gateway == nil || gateway.ID != gatewayID || gatewayID == fresh.ID || preparedID <= 0 || gateway.Extra[KiloManagedExtraKey] != true || int64(ParseExtraInt(gateway.Extra["kilo_prepared_id"])) != preparedID {
			return nil, errAssignedAuthGatewayInvalid
		}
		proxyID = int64(ParseExtraInt(gateway.Extra[KiloLoginProxyExtraKey]))
		if proxyID <= 0 {
			return nil, errAssignedAuthGatewayInvalid
		}
	} else if ParseExtraInt(fresh.Extra["kilo_source_prepared_id"]) > 0 {
		return nil, errAssignedAuthGatewayInvalid
	} else if pending := int64(ParseExtraInt(fresh.Extra["kilo_onboarding_proxy_id"])); pending > 0 {
		// Before a Gateway key exists, the source's quota-only OAuth login
		// already shares the onboarding task's fixed authentication exit.
		if fresh.Extra["kilo_quota_only"] != true || ParseExtraInt(fresh.Extra["kilo_onboarding_job_id"]) <= 0 {
			return nil, errAssignedAuthGatewayInvalid
		}
		if fresh.ProxyID != nil && *fresh.ProxyID != pending {
			return nil, errAssignedAuthGatewayInvalid
		}
		if previous := int64(ParseExtraInt(fresh.Extra[assignedAuthProxyExtraKey])); previous > 0 && previous != pending {
			return nil, errAssignedAuthGatewayInvalid
		}
		proxyID = pending
	} else if fresh.ProxyID != nil {
		proxyID = *fresh.ProxyID
	} else {
		pinned := int64(ParseExtraInt(fresh.Extra[assignedAuthProxyExtraKey]))
		candidates := make([]int64, 0, len(fresh.ProxyBindings))
		assigned := false
		for _, binding := range fresh.ProxyBindings {
			if binding.ProxyID <= 0 {
				continue
			}
			if pinned == binding.ProxyID {
				assigned = true
			}
			if binding.Proxy != nil && binding.Proxy.ID == binding.ProxyID && usableAssignedAuthProxy(binding.Proxy) {
				candidates = append(candidates, binding.ProxyID)
			}
		}
		if pinned > 0 {
			if !assigned {
				return nil, errAssignedAuthProxyUnavailable
			}
			proxyID = pinned
		} else {
			if len(candidates) == 0 {
				if len(fresh.ProxyBindings) == 0 && fresh.Extra[KiloManagedExtraKey] != true {
					return &assignedAuthProxy{}, nil
				}
				return nil, errAssignedAuthProxyRequired
			}
			// Stable selection makes simultaneous first claims choose the same
			// binding; persist it before any network request or worker handoff.
			sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })
			proxyID = candidates[0]
			if store.pin == nil || store.pin(ctx, fresh.ID, map[string]any{assignedAuthProxyExtraKey: proxyID}) != nil {
				return nil, errAssignedAuthProxyPersistence
			}
			current, readErr := store.account(ctx, fresh.ID)
			if readErr != nil || current == nil || int64(ParseExtraInt(current.Extra[assignedAuthProxyExtraKey])) != proxyID || current.ProxyID != nil || ParseExtraInt(current.Extra["kilo_source_gateway_id"]) > 0 {
				return nil, errAssignedAuthProxyPersistence
			}
			assigned = false
			for _, binding := range current.ProxyBindings {
				assigned = assigned || binding.ProxyID == proxyID
			}
			if !assigned {
				return nil, errAssignedAuthProxyUnavailable
			}
		}
	}
	if proxyID <= 0 {
		return nil, errAssignedAuthProxyRequired
	}
	proxy, err := store.proxy(ctx, proxyID)
	if err != nil || proxy == nil || proxy.ID != proxyID || !usableAssignedAuthProxy(proxy) {
		return nil, errAssignedAuthProxyUnavailable
	}
	return &assignedAuthProxy{ID: proxyID, URL: proxy.URL()}, nil
}

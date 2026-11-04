package service

import "context"

type ProxyLimits struct {
	MaxConcurrency int
	MaxAccounts    int
}

type ProxyLimitsReader interface {
	GetProxyLimits(context.Context, []int64) (map[int64]ProxyLimits, error)
}

func (s *adminServiceImpl) attachProxyLimits(ctx context.Context, proxies []*Proxy) error {
	reader, ok := s.proxyRepo.(ProxyLimitsReader)
	if !ok || len(proxies) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(proxies))
	for _, p := range proxies {
		if p != nil {
			ids = append(ids, p.ID)
		}
	}
	limits, err := reader.GetProxyLimits(ctx, ids)
	if err != nil {
		return err
	}
	groups := map[int64]int64{}
	if groupReader, ok := s.proxyRepo.(ProxyGroupRepository); ok {
		groups, err = groupReader.GetProxyGroupMemberships(ctx, ids)
		if err != nil {
			return err
		}
	}
	for _, p := range proxies {
		if p != nil {
			p.ProxyGroupID = nil
			if id := groups[p.ID]; id > 0 {
				p.ProxyGroupID = &id
			}
			p.MaxConcurrency = limits[p.ID].MaxConcurrency
			p.MaxAccounts = limits[p.ID].MaxAccounts
		}
	}
	return nil
}

func (s *adminServiceImpl) attachPlainProxyLimits(ctx context.Context, proxies []Proxy) error {
	refs := make([]*Proxy, 0, len(proxies))
	for i := range proxies {
		refs = append(refs, &proxies[i])
	}
	return s.attachProxyLimits(ctx, refs)
}

func (s *adminServiceImpl) attachCountedProxyLimits(ctx context.Context, proxies []ProxyWithAccountCount) error {
	refs := make([]*Proxy, 0, len(proxies))
	for i := range proxies {
		refs = append(refs, &proxies[i].Proxy)
	}
	return s.attachProxyLimits(ctx, refs)
}

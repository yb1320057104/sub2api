package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *proxyRepository) GetProxyLimits(ctx context.Context, ids []int64) (map[int64]service.ProxyLimits, error) {
	out := map[int64]service.ProxyLimits{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT proxy_id,max_concurrency,max_accounts FROM pk_proxy_limits WHERE proxy_id=ANY($1)`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var limits service.ProxyLimits
		if err = rows.Scan(&id, &limits.MaxConcurrency, &limits.MaxAccounts); err != nil {
			return nil, err
		}
		out[id] = limits
	}
	return out, rows.Err()
}

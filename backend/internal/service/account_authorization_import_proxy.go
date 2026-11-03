package service

import (
	"context"
	"sort"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const authorizationImportProxyLockID int64 = 70360005

func normalizeAuthorizationProxyOptions(settings *AuthorizationSubmit) error {
	if settings.AccountProxyMode == "" {
		settings.AccountProxyMode = "direct"
	}
	if settings.MaxAccountsPerProxy == 0 {
		settings.MaxAccountsPerProxy = 3
	}
	if settings.AccountProxyMode != "direct" && settings.AccountProxyMode != "managed" {
		return infraerrors.BadRequest("AUTHORIZATION_PROXY_MODE", "账号出口必须选择直连或IP管理代理")
	}
	if settings.ProxiesPerAccount == 0 {
		settings.ProxiesPerAccount = 1
	}
	if settings.ProxiesPerAccount < 1 || settings.ProxiesPerAccount > 100 {
		return infraerrors.BadRequest("AUTHORIZATION_PROXY_COUNT", "每个账号代理数须为1至100")
	}
	if settings.ProxyConcurrency != nil && (*settings.ProxyConcurrency < 1 || *settings.ProxyConcurrency > 10000) {
		return infraerrors.BadRequest("AUTHORIZATION_PROXY_CONCURRENCY", "每代理并发须为1至10000")
	}
	if settings.MaxAccountsPerProxy < 1 || settings.MaxAccountsPerProxy > 10000 {
		return infraerrors.BadRequest("AUTHORIZATION_PROXY_LIMIT", "每个IP最多绑定账号数须为1至10000")
	}
	return nil
}

// This is the native account egress pool, never the encrypted login-proxy pool.
// The global assignment lock and selected rows stay locked until import commits.
type authorizationAccountProxyPlan struct {
	PrimaryID     *int64
	Bindings      []AccountProxyBindingInput
	ApplyBindings bool
}

type authorizationProxyCandidate struct {
	ID             int64
	AccountCount   int
	MaxAccounts    int
	MaxConcurrency int
}

const authorizationImportProxyCandidatesQuery = `SELECT p.id,
 COALESCE(l.max_accounts,0),COALESCE(l.max_concurrency,0),
 (SELECT COUNT(*) FROM pk_effective_account_proxy_bindings b JOIN accounts a
 ON a.id=b.account_id AND a.parent_account_id IS NULL WHERE b.proxy_id=p.id)
 FROM proxies p LEFT JOIN pk_proxy_limits l ON l.proxy_id=p.id
 WHERE p.deleted_at IS NULL AND p.status='active' AND (p.expires_at IS NULL OR p.expires_at>NOW())
 ORDER BY p.id FOR UPDATE OF p`

func authorizationImportProxy(ctx context.Context, tx *dbent.Tx, settings *AuthorizationSubmit) (*authorizationAccountProxyPlan, error) {
	if err := normalizeAuthorizationProxyOptions(settings); err != nil {
		return nil, err
	}
	if settings.AccountProxyMode == "direct" {
		return &authorizationAccountProxyPlan{}, nil
	}
	if _, err := tx.Client().ExecContext(ctx, "SELECT pg_advisory_xact_lock(70440001)"); err != nil {
		return nil, err
	}
	if _, err := tx.Client().ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", authorizationImportProxyLockID); err != nil {
		return nil, err
	}
	rows, err := tx.Client().QueryContext(ctx, authorizationImportProxyCandidatesQuery)
	if err != nil {
		return nil, err
	}
	var candidates []authorizationProxyCandidate
	for rows.Next() {
		var p authorizationProxyCandidate
		if err = rows.Scan(&p.ID, &p.MaxAccounts, &p.MaxConcurrency, &p.AccountCount); err != nil {
			break
		}
		candidates = append(candidates, p)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	return planAuthorizationImportProxies(settings, candidates)
}

func planAuthorizationImportProxies(settings *AuthorizationSubmit, candidates []authorizationProxyCandidate) (*authorizationAccountProxyPlan, error) {
	if err := normalizeAuthorizationProxyOptions(settings); err != nil {
		return nil, err
	}
	plan := &authorizationAccountProxyPlan{}
	if settings.AccountProxyMode == "direct" {
		return plan, nil
	}
	// A missing concurrency field is the old single-egress contract: choose an
	// available legacy primary without mutating any global settings. New explicit
	// inputs use the same multi-binding repository as account editing.
	plan.ApplyBindings = settings.ProxyConcurrency != nil || settings.ProxiesPerAccount > 1
	pool := append([]authorizationProxyCandidate(nil), candidates...)
	sort.Slice(pool, func(i, j int) bool {
		if plan.ApplyBindings && pool[i].AccountCount != pool[j].AccountCount {
			return pool[i].AccountCount < pool[j].AccountCount
		}
		return pool[i].ID < pool[j].ID
	})
	seen := map[int64]bool{}
	for _, p := range pool {
		if p.ID <= 0 || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		capacity := settings.MaxAccountsPerProxy
		if p.MaxAccounts > 0 && p.MaxAccounts < capacity {
			capacity = p.MaxAccounts
		}
		if p.AccountCount >= capacity {
			continue
		}
		concurrency := p.MaxConcurrency
		if settings.ProxyConcurrency != nil {
			concurrency = *settings.ProxyConcurrency
			if p.MaxConcurrency > 0 && p.MaxConcurrency < concurrency {
				concurrency = p.MaxConcurrency
			}
		}
		plan.Bindings = append(plan.Bindings, AccountProxyBindingInput{ProxyID: p.ID, MaxAccounts: capacity, MaxConcurrency: concurrency})
		if len(plan.Bindings) == settings.ProxiesPerAccount {
			break
		}
	}
	if len(plan.Bindings) != settings.ProxiesPerAccount {
		return nil, authorizationBrowserFailure("account_proxy_capacity", "IP管理中可用且未达上限的不同代理不足，未入库；请增加代理或调整每账号代理数及容量后重新提交")
	}
	primary := plan.Bindings[0].ProxyID
	plan.PrimaryID = &primary
	return plan, nil
}

func applyAuthorizationImportProxyPlan(ctx context.Context, accounts AccountRepository, accountID int64, plan *authorizationAccountProxyPlan) error {
	if plan == nil || !plan.ApplyBindings {
		return nil
	}
	repo, ok := accounts.(AccountProxyBindingRepository)
	if !ok {
		return authorizationBrowserFailure("account_proxy_unavailable", "多代理分配服务不可用，未入库")
	}
	return repo.ReplaceAccountProxyBindings(ctx, []int64{accountID}, plan.Bindings)
}

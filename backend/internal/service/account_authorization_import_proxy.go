package service

import (
	"context"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const authorizationImportProxyLockID int64 = 70360005

func normalizeAuthorizationProxyOptions(settings *AuthorizationSubmit) error {
	if settings.AccountProxyMode == "" {
		settings.AccountProxyMode = "direct"
	}
	if settings.MaxAccountsPerProxy == 0 {
		settings.MaxAccountsPerProxy = 1
	}
	if settings.AccountProxyMode != "direct" && settings.AccountProxyMode != "managed" {
		return infraerrors.BadRequest("AUTHORIZATION_PROXY_MODE", "账号出口必须选择直连或IP管理代理")
	}
	if settings.MaxAccountsPerProxy < 1 || settings.MaxAccountsPerProxy > 1000 {
		return infraerrors.BadRequest("AUTHORIZATION_PROXY_LIMIT", "每个IP最多绑定账号数须为1至1000")
	}
	return nil
}

// This is the native account egress pool, never the encrypted login-proxy pool.
// The assignment lock and proxy row lock last until account creation commits.
func authorizationImportProxy(ctx context.Context, tx *dbent.Tx, settings *AuthorizationSubmit) (*int64, error) {
	if err := normalizeAuthorizationProxyOptions(settings); err != nil {
		return nil, err
	}
	if settings.AccountProxyMode == "direct" {
		return nil, nil
	}
	if _, err := tx.Client().ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", authorizationImportProxyLockID); err != nil {
		return nil, err
	}
	var after int64
	for {
		rows, err := tx.Client().QueryContext(ctx, `SELECT p.id FROM proxies p WHERE p.id>$1 AND p.deleted_at IS NULL AND p.status='active' AND (p.expires_at IS NULL OR p.expires_at>NOW()) AND (SELECT COUNT(*) FROM accounts a WHERE a.proxy_id=p.id AND a.deleted_at IS NULL)<$2 ORDER BY p.id LIMIT 1 FOR UPDATE OF p`, after, settings.MaxAccountsPerProxy)
		if err != nil {
			return nil, err
		}
		var id int64
		found := rows.Next()
		if found {
			err = rows.Scan(&id)
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, authorizationBrowserFailure("account_proxy_capacity", "IP管理中没有可用且未达绑定上限的代理，未入库；请添加可用IP或调整绑定上限后重新提交")
		}
		// Recount after obtaining the row lock: a native account edit may have committed
		// while the SELECT was waiting. Never use a pre-lock capacity count.
		rows, err = tx.Client().QueryContext(ctx, `SELECT COUNT(*) FROM accounts WHERE proxy_id=$1 AND deleted_at IS NULL`, id)
		if err != nil {
			return nil, err
		}
		var count int
		if !rows.Next() {
			err = rows.Err()
			_ = rows.Close()
			if err != nil {
				return nil, err
			}
			return nil, authorizationBrowserFailure("account_proxy_capacity", "无法确认IP绑定容量，未入库")
		}
		err = rows.Scan(&count)
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		if count < settings.MaxAccountsPerProxy {
			return &id, nil
		}
		after = id
	}
}

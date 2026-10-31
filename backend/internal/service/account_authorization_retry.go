package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

const authorizationPassedProxyLimit = 5

// Only transient transport/navigation failures can change exits. Credential
// rejection, bans, throttling and security challenges keep their own outcomes.
// A submitted password is never silently replayed by this browser-error path.
func authorizationMayChangeProxy(err error) bool {
	var failure *authorizationBrowserError
	if !errors.As(err, &failure) || failure.noReplay || failure.needsInput {
		return false
	}
	switch failure.stage {
	case "proxy_precheck_failed", "proxy_failed", "proxy_navigation_timeout", "proxy_network_failed", "proxy_http_failed", "proxy_precheck_timeout", "login_failed", "login_timeout":
		return true
	}
	return false
}

func (s *AccountAuthorizationService) authorizeWithProxyRotation(ctx context.Context, job *authorizationJob, secret *AuthorizationSecret, proxies []authorizationProxy, progress func(string, string) error) (*OpenAITokenInfo, error) {
	passed, scanned := 0, 0
	var last error
	// One pool snapshot, at most one visit per ID. Disabled/deleted candidates are
	// checked again before use. A failed precheck does not consume a passed slot.
	for i := 0; i < len(proxies) && passed < authorizationPassedProxyLimit; i++ {
		if ctx.Err() != nil {
			break
		}
		proxy := proxies[(int(job.id)+i)%len(proxies)]
		var enabled bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pk_authorization_proxies WHERE id=$1 AND enabled)`, proxy.id).Scan(&enabled); err != nil {
			return nil, err
		}
		if !enabled {
			continue
		}
		scanned++
		record := func() error {
			r, e := s.db.ExecContext(ctx, `UPDATE pk_authorization_jobs SET settings=settings||jsonb_build_object('authorization_proxy_id',$3::bigint,'authorization_proxy_scanned',$4::integer,'authorization_proxy_passed',$5::integer,'authorization_proxy_limit',$6::integer) WHERE id=$1 AND owner_token=$2 AND status='running'`, job.id, job.token, proxy.id, scanned, passed, authorizationPassedProxyLimit)
			if e != nil {
				return e
			}
			n, e := r.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return context.Canceled
			}
			return nil
		}
		if err := progress("proxy_switch", fmt.Sprintf("正在尝试授权代理 #%d；已有 %d/%d 条代理预检通过", proxy.id, passed, authorizationPassedProxyLimit)); err != nil {
			return nil, err
		}
		if err := record(); err != nil {
			return nil, err
		}
		session, err := s.oauth.generateWorkbenchAuthURL(ctx, proxy.raw)
		if err != nil {
			return nil, authorizationBrowserFailure("session_failed", "无法生成授权链接")
		}
		prechecked, credentialsSubmitted := false, false
		attemptProgress := func(stage, message string) error {
			if stage == "password" || stage == "mfa" || stage == "consent" {
				credentialsSubmitted = true
			}
			if stage == "login" && !prechecked {
				prechecked = true
				passed++
				if _, e := s.db.ExecContext(ctx, `UPDATE pk_authorization_proxies SET last_checked_at=NOW(),last_status='passed',last_message='chatgpt.com GET预检通过' WHERE id=$1 AND enabled`, proxy.id); e != nil {
					return e
				}
				if e := record(); e != nil {
					return e
				}
			}
			return progress(stage, message)
		}
		callback, err := runAuthorizationBrowser(ctx, proxy.raw, session.AuthURL, secret, attemptProgress)
		if err != nil {
			s.oauth.sessionStore.Delete(session.SessionID)
			last = err
			var failure *authorizationBrowserError
			if !prechecked && errors.As(err, &failure) && authorizationMayChangeProxy(err) {
				if _, e := s.db.ExecContext(ctx, `UPDATE pk_authorization_proxies SET last_checked_at=NOW(),last_status='failed',last_message=$2 WHERE id=$1 AND enabled`, proxy.id, failure.message); e != nil {
					return nil, e
				}
			}
			if ctx.Err() != nil {
				break
			}
			if credentialsSubmitted || !authorizationMayChangeProxy(err) {
				return nil, err
			}
			if e := progress("proxy_switch", fmt.Sprintf("当前代理未完成授权，正在换下一条；预检通过 %d/%d 条", passed, authorizationPassedProxyLimit)); e != nil {
				return nil, e
			}
			continue
		}
		u, err := url.Parse(callback)
		if err != nil {
			s.oauth.sessionStore.Delete(session.SessionID)
			return nil, authorizationBrowserFailure("callback_invalid", "授权回调格式无效，未写入凭据")
		}
		if err = progress("exchange", "正在交换并校验授权凭据"); err != nil {
			s.oauth.sessionStore.Delete(session.SessionID)
			return nil, err
		}
		token, err := s.oauth.ExchangeCode(ctx, &OpenAIExchangeCodeInput{SessionID: session.SessionID, Code: u.Query().Get("code"), State: u.Query().Get("state")})
		if err != nil && authorizationExchangeRetryable(err) {
			authorizationTrace(secret, "exchange retry after transport failure: %v", err)
			token, err = s.oauth.ExchangeCode(ctx, &OpenAIExchangeCodeInput{SessionID: session.SessionID, Code: u.Query().Get("code"), State: u.Query().Get("state")})
		}
		s.oauth.sessionStore.Delete(session.SessionID)
		if err == nil {
			authorizationTrace(secret, "exchange completed")
			return token, nil
		}
		authorizationTrace(secret, "exchange failed: %v", err)
		if authorizationBanText(err.Error()) {
			return nil, authorizationBannedFailure()
		}
		// The callback follows password/MFA submission. Keep the existing
		// no-replay boundary: exchange failure cannot restart login on a new exit.
		return nil, authorizationBrowserFailure("exchange_failed", "授权码交换失败"+authorizationExchangeDetail(err)+"；已提交账号凭据，不自动重放登录，可检查后手动重试")
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, context.Canceled
	}
	message := fmt.Sprintf("已检查 %d 条授权代理，其中 %d 条预检通过，仍未完成授权；可点击重试重新执行", scanned, passed)
	stage := "proxy_pool_exhausted"
	if passed >= authorizationPassedProxyLimit {
		stage = "proxy_attempts_exhausted"
		message = "已使用5条不同的预检通过代理，均未完成授权；可点击重试重新执行"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		stage = "authorization_timeout"
		message = fmt.Sprintf("任务达到总时限；已检查%d条代理、%d条预检通过，可点击重试继续", scanned, passed)
	}
	var failure *authorizationBrowserError
	if errors.As(last, &failure) {
		message += "。最后原因：" + failure.message
	}
	return nil, authorizationBrowserFailure(stage, message)
}

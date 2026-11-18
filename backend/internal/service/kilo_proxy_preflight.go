package service

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

// KiloProxyCheck contains transport diagnostics, never the proxy URL or an
// upstream response body. A challenge is reported separately from TLS failure.
type KiloProxyCheck struct {
	Target    string `json:"target"`
	Reachable bool   `json:"reachable"`
	Status    int    `json:"status,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	Challenge bool   `json:"challenge,omitempty"`
	Error     string `json:"error,omitempty"`
}

type KiloProxyPreflight struct {
	ProxyID   int64            `json:"proxy_id"`
	Reachable bool             `json:"reachable"`
	Checks    []KiloProxyCheck `json:"checks"`
}

// KiloLoginProxyProber tests only public authentication endpoints. It must not
// log in, acquire an identity or silently fall back to a direct connection.
type KiloLoginProxyProber interface {
	ProbeLoginProxy(context.Context, string) ([]KiloProxyCheck, error)
}

func (s *KiloService) PreflightLoginProxy(ctx context.Context, id int64) (*KiloProxyPreflight, error) {
	if id <= 0 {
		return nil, kiloBad("KILO_LOGIN_PROXY_REQUIRED", "请选择可用的登录代理")
	}
	proxy, err := s.proxies.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !usableAssignedAuthProxy(proxy) {
		return nil, kiloBad("KILO_PROXY_INVALID", "登录代理已停用、过期或配置无效")
	}
	prober, ok := s.remote.(KiloLoginProxyProber)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("KILO_PROXY_PREFLIGHT_UNAVAILABLE", "登录代理预检暂不可用")
	}
	checks, err := prober.ProbeLoginProxy(ctx, proxy.URL())
	if err != nil {
		return nil, err
	}
	result := &KiloProxyPreflight{ProxyID: id, Reachable: len(checks) == len(kiloLoginProbeTargets), Checks: checks}
	for _, check := range checks {
		result.Reachable = result.Reachable && check.Reachable
	}
	return result, nil
}

// Preflight runs before account creation and before any transaction takes the
// global onboarding lock. Ownership is still checked by the reservation owner.
func (s *KiloService) preflightOnboardingProxies(ctx context.Context, input KiloOnboardingBatchInput) error {
	seen := make(map[int64]bool)
	for _, item := range input.Items {
		proxyID := item.LoginProxyID
		if item.PreparedID > 0 {
			if err := s.db.QueryRowContext(ctx, `SELECT login_proxy_id FROM pk_kilo_prepared_accounts WHERE id=$1`, item.PreparedID).Scan(&proxyID); err != nil {
				return err
			}
		}
		// An automatically selected idle identity is preflighted by its worker
		// after selection; no new login identity is created without a proxy.
		if proxyID == 0 && input.BindingStrategy == KiloBindingPreferIdle {
			continue
		}
		if seen[proxyID] {
			continue
		}
		seen[proxyID] = true
		result, err := s.PreflightLoginProxy(ctx, proxyID)
		if err != nil {
			return err
		}
		if !result.Reachable {
			return kiloBad("KILO_LOGIN_PROXY_UNREACHABLE", "登录代理无法连接认证服务，请更换代理后重试；尚未创建账号或固定登录身份")
		}
	}
	return nil
}

var kiloLoginProbeTargets = [...]struct {
	name, endpoint string
}{
	{"openai", "https://auth.openai.com/"},
	{"kilo_api", kiloRemoteAPIBase + "/api/profile"},
	{"kilo_login", kiloRemoteAppBase + "/api/auth/csrf"},
}

func (c *kiloRemoteClient) ProbeLoginProxy(ctx context.Context, proxyURL string) ([]KiloProxyCheck, error) {
	if strings.TrimSpace(proxyURL) == "" {
		return nil, kiloBad("KILO_LOGIN_PROXY_REQUIRED", "登录代理不能为空，预检不会回退直连")
	}
	client, err := c.getClient(httpclient.Options{ProxyURL: proxyURL, Timeout: kiloRemoteTimeout, ValidateResolvedIP: true})
	if err != nil {
		return nil, kiloBad("KILO_PROXY_INVALID", "登录代理配置无效，预检不会回退直连")
	}
	isolated := *client
	isolated.Timeout = 12 * time.Second
	isolated.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	checks := make([]KiloProxyCheck, len(kiloLoginProbeTargets))
	var wg sync.WaitGroup
	for i, target := range kiloLoginProbeTargets {
		wg.Add(1)
		go func(i int, name, endpoint string) {
			defer wg.Done()
			started := time.Now()
			check := KiloProxyCheck{Target: name}
			defer func() {
				check.LatencyMS = time.Since(started).Milliseconds()
				checks[i] = check
			}()
			req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if requestErr != nil {
				check.Error = "request_invalid"
				return
			}
			req.Header.Set("Accept", "application/json,text/html;q=0.8")
			response, requestErr := isolated.Do(req)
			if requestErr != nil {
				check.Error = kiloProxyProbeError(requestErr)
				return
			}
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			check.Status = response.StatusCode
			check.Challenge = response.Header.Get("Cf-Mitigated") == "challenge"
			check.Reachable = check.Status >= 200 && check.Status < 400 ||
				name == "kilo_api" && check.Status == http.StatusUnauthorized ||
				check.Status == http.StatusForbidden && (name == "openai" || check.Challenge)
			if !check.Reachable {
				check.Error = "authentication_endpoint_unavailable"
			}
		}(i, target.name, target.endpoint)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return checks, nil
}

func kiloProxyProbeError(err error) string {
	var certificateErr *url.Error
	if errors.As(err, &certificateErr) {
		err = certificateErr.Err
	}
	var unknownAuthority x509.UnknownAuthorityError
	var invalidCertificate x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &unknownAuthority) || errors.As(err, &invalidCertificate) || errors.As(err, &hostname) {
		return "tls_failed"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	return "connection_failed"
}

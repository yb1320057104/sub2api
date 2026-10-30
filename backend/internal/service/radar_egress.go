package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
)

// A bound but missing/unusable proxy must never silently become direct traffic.
func accountCandyProxyURL(account *Account) (string, error) {
	if account == nil {
		return "", candyProbeFailure("账号不存在；未发出测试请求。")
	}
	if account.ProxyID == nil {
		return "", nil
	}
	if account.Proxy == nil || account.Proxy.ID != *account.ProxyID || account.Proxy.Host == "" || account.Proxy.Port <= 0 || account.Proxy.Port > 65535 {
		return "", candyProbeFailure("账号配置的代理不可用；已阻止直连，未转组。")
	}
	route := account.Proxy.URL()
	if _, parsed, err := proxyurl.Parse(route); err != nil || parsed == nil {
		return "", candyProbeFailure("账号代理协议或地址无效；已阻止直连，未转组。")
	}
	return route, nil
}

// The observed IP belongs to a separate request through the same configured
// route. Rotating/destination-dependent proxies can use a different IP per request.
// No account/model credentials are sent to this fixed, public IP-only endpoint.
func probeRadarEgress(ctx context.Context, account *Account) (string, *time.Time, string) {
	proxyURL, err := accountCandyProxyURL(account)
	if err != nil {
		return "", nil, "账号代理配置不可用，禁止回退直连"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client, err := httpclient.GetClient(httpclient.Options{ProxyURL: proxyURL, Timeout: 12 * time.Second, ValidateResolvedIP: true})
	if err != nil {
		return "", nil, "出口探测连接配置失败"
	}
	// Copy the cached client before applying per-probe redirect policy.
	isolated := *client
	isolated.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "https://api64.ipify.org?format=json", nil)
	if err != nil {
		return "", nil, "无法创建出口探测请求"
	}
	resp, err := isolated.Do(req)
	if err != nil {
		return "", nil, "出口IP探测失败；未切换代理或回退直连"
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", nil, "出口IP探测服务暂不可用"
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(body) > 4096 {
		return "", nil, "出口IP探测响应无效"
	}
	var result struct {
		IP string `json:"ip"`
	}
	if json.Unmarshal(body, &result) != nil {
		return "", nil, "出口IP探测响应无效"
	}
	ip, err := netip.ParseAddr(result.IP)
	if err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return "", nil, "未取得有效的公网出口IP"
	}
	now := time.Now()
	return ip.Unmap().String(), &now, ""
}

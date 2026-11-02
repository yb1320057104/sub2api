package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Only this fixed message is exposed when an encrypted route cannot be
// loaded. Neither the URL nor a transport/decryption error reaches a log/API.
var errBinanceAccountProxy = errors.New("binance: account proxy unavailable")

func validateBinanceAccountProxy(proxyURL string, preflight bool) error {
	if strings.TrimSpace(proxyURL) == "" {
		return errBinanceAccountProxy
	}
	client, err := binancepay.NewClientWithOptions(binancepay.ClientOptions{ProxyURL: proxyURL, ProxyPreflight: preflight})
	if err != nil {
		if errors.Is(err, binancepay.ErrProxyPreflightConfiguration) {
			return binancepay.ErrProxyPreflightConfiguration
		}
		var invalid *binancepay.ProxyConfigurationError
		if errors.As(err, &invalid) {
			return fmt.Errorf("%w: %w", errBinanceAccountProxy, invalid)
		}
		return errBinanceAccountProxy
	}
	client.CloseIdleConnections()
	return nil
}

func binanceProxyInputError(err error) error {
	message := "币安专属代理格式无效，请填写完整的协议、主机和端口"
	var invalid *binancepay.ProxyConfigurationError
	if errors.As(err, &invalid) {
		switch invalid.Reason {
		case "empty":
			message = "币安专属代理没有有效地址，请重新粘贴完整代理地址"
		case "link":
			message = "请粘贴代理链接的完整地址，不要包含 Markdown 链接标题"
		case "whitespace":
			message = "币安专属代理地址内部含空格、换行或隐藏字符，请重新复制完整地址；密码中的特殊字符需使用 URL 编码"
		case "scheme":
			message = "币安专属代理须以 http://、https://、socks5:// 或 socks5h:// 开头"
		case "host":
			message = "币安专属代理缺少主机地址，请填写协议后的代理域名或 IP"
		case "port":
			message = "币安专属代理端口无效，端口须为 1 至 65535 的整数"
		case "suffix":
			message = "币安专属代理不能包含路径、查询参数或片段；账号密码中的 /、?、# 等特殊字符需使用 URL 编码"
		case "syntax":
			message = "币安专属代理 URL 格式无效，请检查主机、端口及账号密码中的特殊字符编码"
		}
	}
	return infraerrors.BadRequest("BINANCE_PROXY_INVALID", message)
}

// accountClient fixes the route for the complete operation, including time
// sync, permission checks and every history page. A saved per-account proxy
// always wins over the startup client; errors must not fall back to that client
// or a direct route. Unspecialized legacy accounts retain their startup route.
func (s *BinanceRechargeService) accountClient(a *BinanceRechargeAccount) (BinanceRechargeClient, func(), error) {
	if a == nil {
		return nil, nil, errBinanceAccountProxy
	}
	if a.proxyURLCiphertext == "" {
		if a.ProxyPreflight || s.client == nil {
			return nil, nil, errBinanceAccountProxy
		}
		return s.client, func() {}, nil
	}
	if s.encryptor == nil {
		return nil, nil, errBinanceAccountProxy
	}
	proxyURL, err := s.encryptor.Decrypt(a.proxyURLCiphertext)
	if err != nil || strings.TrimSpace(proxyURL) == "" {
		return nil, nil, errBinanceAccountProxy
	}
	client, err := binancepay.NewClientWithOptions(binancepay.ClientOptions{ProxyURL: proxyURL, ProxyPreflight: a.ProxyPreflight})
	if err != nil {
		return nil, nil, errBinanceAccountProxy
	}
	return client, client.CloseIdleConnections, nil
}

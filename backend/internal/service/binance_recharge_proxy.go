package service

import (
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
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
		// Only this known configuration hint may reach callers. Keep other
		// parser/transport details behind the generic safe error.
		if errors.Is(err, binancepay.ErrProxyPreflightConfiguration) {
			return binancepay.ErrProxyPreflightConfiguration
		}
		return errBinanceAccountProxy
	}
	client.CloseIdleConnections()
	return nil
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

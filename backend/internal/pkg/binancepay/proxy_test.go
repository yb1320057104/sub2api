package binancepay

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestBinanceProxyPasteNormalizationPreservesCredentialsAndRoute(t *testing.T) {
	const user = "fixture123-region-HK-sid-Ab12Cd-t-5"
	const password = "fixturePASS123"
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		raw := scheme + "://" + user + ":" + password + "@proxy.invalid:3000"
		for _, supplied := range []string{raw, " \uFEFF\u200B" + raw + "\u2060\u00A0", "[" + raw + "](" + raw + ")", "<" + raw + ">", "`" + raw + "`"} {
			normalized, err := NormalizeProxyURL(supplied)
			if err != nil || normalized != raw {
				t.Fatalf("%s paste normalization changed the URL or failed: %v", scheme, err)
			}
			client, err := NewClientWithOptions(ClientOptions{ProxyURL: supplied})
			if err != nil {
				t.Fatalf("valid %s dedicated route rejected: %v", scheme, err)
			}
			transport := client.http.Transport.(*http.Transport)
			proxy, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "api.binance.com"}})
			if err != nil || proxy == nil || proxy.Scheme != scheme || proxy.Host != "proxy.invalid:3000" || proxy.User.Username() != user {
				t.Fatal("dedicated proxy selection changed or fell back to direct access")
			}
			actualPassword, exists := proxy.User.Password()
			if !exists || actualPassword != password {
				t.Fatal("proxy authentication changed")
			}
			client.CloseIdleConnections()
		}
	}
	encoded := "http://fixture%40user:p%40ss%23word%3F%2F%25@proxy.invalid:3000"
	normalized, err := NormalizeProxyURL(encoded)
	if err != nil || normalized != encoded {
		t.Fatal("URL-encoded authentication must remain byte-for-byte unchanged")
	}
}

func TestBinanceProxyInvalidInputGivesSafeReasonAndNeverDirect(t *testing.T) {
	for _, tc := range []struct{ raw, reason string }{
		{"\u200B\uFEFF", "empty"},
		{"[a different host](http://fixture:private-secret@proxy.invalid:80)", "link"},
		{"http://fixture:private-secret@proxy.\u200Binvalid:80", "whitespace"},
		{"http://fixture:private-secret@proxy.invalid:\n80", "whitespace"},
		{"proxy.invalid:80", "scheme"},
		{"ftp://fixture:private-secret@proxy.invalid:80", "scheme"},
		{"http://", "host"},
		{"http://fixture:private-secret@proxy.invalid:", "port"},
		{"http://fixture:private-secret@proxy.invalid:abc", "port"},
		{"http://fixture:private-secret@proxy.invalid:0", "port"},
		{"http://fixture:private-secret@proxy.invalid:65536", "port"},
		{"http://fixture:private-secret@proxy.invalid:80/path", "suffix"},
		{"http://fixture:private-secret@proxy.invalid:80?x=y", "suffix"},
		{"http://fixture:private-secret@proxy.invalid:80#", "suffix"},
		{"http://fixture:private-secret%zz@proxy.invalid:80", "syntax"},
	} {
		normalized, err := NormalizeProxyURL(tc.raw)
		var invalid *ProxyConfigurationError
		if normalized != "" || !errors.As(err, &invalid) || invalid.Reason != tc.reason {
			t.Fatalf("expected safe reason %s, got %v", tc.reason, err)
		}
		if strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "proxy.invalid") || strings.Contains(err.Error(), tc.raw) {
			t.Fatal("proxy parser exposed supplied URL or credentials")
		}
		client, err := NewClientWithOptions(ClientOptions{ProxyURL: tc.raw})
		if err == nil || client != nil {
			t.Fatal("invalid dedicated route must never return a direct client")
		}
	}
}

func TestBinanceProxyNormalizationPreservesPreflightRules(t *testing.T) {
	for _, raw := range []string{"socks5://fixture:secret@proxy.invalid:3000", "http://proxy.invalid:3000"} {
		client, err := NewClientWithOptions(ClientOptions{ProxyURL: "\uFEFF" + raw, ProxyPreflight: true})
		if client != nil || !errors.Is(err, ErrProxyPreflightConfiguration) {
			t.Fatal("paste normalization must preserve the authenticated HTTP preflight requirement")
		}
	}
}

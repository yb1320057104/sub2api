package binancepay

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// ProxyConfigurationError carries a fixed reason only, never the supplied URL,
// userinfo, or the error returned by net/url (which may contain credentials).
type ProxyConfigurationError struct{ Reason string }

func (e *ProxyConfigurationError) Error() string {
	return "binance: invalid proxy configuration (" + e.Reason + ")"
}

func proxyInputError(reason string) error { return &ProxyConfigurationError{Reason: reason} }

func proxyBoundary(r rune) bool {
	return unicode.IsSpace(r) || r == '\uFEFF' || r == '\u200B' || r == '\u2060'
}

// NormalizeProxyURL removes only surrounding paste artifacts. Credentials are
// neither decoded/re-encoded nor repaired; internal whitespace is rejected.
// An empty input is invalid here, so normalization cannot clear a saved route.
func NormalizeProxyURL(raw string) (string, error) {
	cleaned, _, err := validatedProxyURL(raw)
	return cleaned, err
}

func parseProxyURL(raw string) (*url.URL, error) {
	_, proxy, err := validatedProxyURL(raw)
	return proxy, err
}

func validatedProxyURL(raw string) (string, *url.URL, error) {
	cleaned := strings.TrimFunc(raw, proxyBoundary)
	// Accept one unambiguous Markdown URL, including the duplicated URL format
	// copied from chat. A different link label must not silently choose a route.
	if strings.HasPrefix(cleaned, "[") && strings.HasSuffix(cleaned, ")") {
		if split := strings.Index(cleaned, "]("); split > 0 {
			label := strings.TrimFunc(cleaned[1:split], proxyBoundary)
			target := strings.TrimFunc(cleaned[split+2:len(cleaned)-1], proxyBoundary)
			if label != target {
				return "", nil, proxyInputError("link")
			}
			cleaned = target
		}
	}
	if len(cleaned) >= 2 && ((cleaned[0] == '<' && cleaned[len(cleaned)-1] == '>') ||
		(cleaned[0] == '`' && cleaned[len(cleaned)-1] == '`')) {
		cleaned = strings.TrimFunc(cleaned[1:len(cleaned)-1], proxyBoundary)
	}
	if cleaned == "" {
		return "", nil, proxyInputError("empty")
	}
	if strings.IndexFunc(cleaned, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
		return "", nil, proxyInputError("whitespace")
	}
	scheme, _, found := strings.Cut(cleaned, "://")
	if !found {
		return "", nil, proxyInputError("scheme")
	}
	switch strings.ToLower(scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", nil, proxyInputError("scheme")
	}
	proxy, err := url.Parse(cleaned)
	if err != nil {
		// Classify only a fixed parser category; never return its raw text.
		if strings.Contains(err.Error(), "invalid port") {
			return "", nil, proxyInputError("port")
		}
		return "", nil, proxyInputError("syntax")
	}
	if proxy.Hostname() == "" || proxy.Opaque != "" {
		return "", nil, proxyInputError("host")
	}
	if (proxy.Path != "" && proxy.Path != "/") || proxy.RawQuery != "" || proxy.ForceQuery || proxy.Fragment != "" || strings.Contains(cleaned, "#") {
		return "", nil, proxyInputError("suffix")
	}
	if strings.HasSuffix(proxy.Host, ":") {
		return "", nil, proxyInputError("port")
	}
	if port := proxy.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", nil, proxyInputError("port")
		}
	}
	return cleaned, proxy, nil
}

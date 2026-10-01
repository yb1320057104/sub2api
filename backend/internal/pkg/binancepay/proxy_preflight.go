package binancepay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	proxyPreflightPath    = "/_binance/preflight"
	proxyRouteHeader      = "X-Binance-Route-Token"
	proxyPreflightTimeout = 15 * time.Second
	maxPreflightResponse  = 4096
)

// ErrProxyPreflight is safe to expose without the gateway URL, authentication,
// route token, provider response, or any underlying transport error.
var ErrProxyPreflight = errors.New("binance: proxy route preflight failed")

// beginOperation selects a fresh route exactly once per top-level operation.
// Each operation owns a separate transport, so concurrent accounts and later
// preflights cannot switch the route used by time sync or history pagination.
func (c *Client) beginOperation(ctx context.Context) (*Client, func(), error) {
	if c == nil || c.http == nil {
		return nil, nil, errors.New("binance: client is not configured")
	}
	if c.preflightHTTP == nil {
		return c, func() {}, nil
	}
	token, err := c.preflight(ctx)
	if err != nil {
		return nil, nil, err
	}
	transport := c.transportTemplate.Clone()
	// ProxyConnectHeader is sent only to the authenticated CONNECT gateway,
	// never inside the TLS request to Binance. The gateway must bind this
	// opaque token to one tested node and reject unknown/expired tokens.
	transport.ProxyConnectHeader = http.Header{proxyRouteHeader: []string{token}}
	transport.GetProxyConnectHeader = nil
	operation := NewClientWithTransport(transport)
	return operation, operation.CloseIdleConnections, nil
}

func (c *Client) preflight(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, proxyPreflightTimeout)
	defer cancel()
	endpoint := *c.proxyURL
	endpoint.User = nil
	endpoint.Path = proxyPreflightPath
	endpoint.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), nil)
	if err != nil {
		return "", ErrProxyPreflight
	}
	password, _ := c.proxyURL.User.Password()
	req.SetBasicAuth(c.proxyURL.User.Username(), password)
	req.Header.Set("Accept", "application/json")
	response, err := c.preflightHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrProxyPreflight
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w (HTTP %d)", ErrProxyPreflight, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPreflightResponse+1))
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrProxyPreflight
	}
	var result struct {
		RouteToken string `json:"route_token"`
	}
	if len(body) > maxPreflightResponse || json.Unmarshal(body, &result) != nil || !validRouteToken(result.RouteToken) {
		return "", ErrProxyPreflight
	}
	return result.RouteToken, nil
}

func validRouteToken(token string) bool {
	if len(token) == 0 || len(token) > 256 {
		return false
	}
	for _, char := range token {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

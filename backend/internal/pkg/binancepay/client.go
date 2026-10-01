package binancepay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	apiBase         = "https://api.binance.com"
	historyPath     = "/sapi/v1/pay/transactions"
	requestTimeout  = 15 * time.Second
	historyTimeout  = 90 * time.Second
	maxResponseSize = 8 << 20
	maxHistoryCalls = 64
	maxWindowMillis = int64(89 * 24 * time.Hour / time.Millisecond)
)

// Receipt contains only incoming C2C/PAY USDT transfers to the requested UID.
// Time is the provider transaction timestamp, never the time a poll saw it.
type Receipt struct {
	TransactionID string
	AmountUnits   int64
	Time          time.Time
}

// RateLimitError tells the scheduler when it may retry. The client never retries
// a 429 or 418 itself, and never exposes an upstream response or signed URL.
type RateLimitError struct {
	RetryAfter time.Duration
	StatusCode int
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("binance: rate limited (HTTP %d), retry after %d seconds", e.StatusCode, int64(e.RetryAfter/time.Second))
}

// Client has no mutable signing/clock/route state and may be shared by account
// workers. Binance requests are GET against a fixed origin; redirects are rejected.
type Client struct {
	http              *http.Client
	preflightHTTP     *http.Client
	proxyURL          *url.URL
	transportTemplate *http.Transport
}

// ClientOptions configures only this Binance client. Transport is an optional
// trusted template (for custom TLS roots or tests); it is cloned, never modified.
// Environment proxy settings are never inherited.
type ClientOptions struct {
	ProxyURL       string
	ProxyPreflight bool
	Transport      *http.Transport
}

// NewClient accepts an optional dedicated HTTP(S) or SOCKS5 proxy. An invalid
// proxy is an error and never silently falls back to a direct connection.
func NewClient(proxyURL string) (*Client, error) {
	return NewClientWithOptions(ClientOptions{ProxyURL: proxyURL})
}

func NewClientWithOptions(options ClientOptions) (*Client, error) {
	transport := cloneDirectTransport()
	if options.Transport != nil {
		transport = options.Transport.Clone()
		transport.Proxy = nil
	}
	var proxy *url.URL
	if strings.TrimSpace(options.ProxyURL) != "" {
		var err error
		proxy, err = parseProxyURL(options.ProxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	client := NewClientWithTransport(transport)
	if options.ProxyPreflight {
		if proxy == nil || (proxy.Scheme != "http" && proxy.Scheme != "https") || proxy.User == nil || proxy.User.Username() == "" {
			return nil, errors.New("binance: proxy preflight requires an authenticated HTTP(S) proxy")
		}
		if password, _ := proxy.User.Password(); password == "" {
			return nil, errors.New("binance: proxy preflight requires an authenticated HTTP(S) proxy")
		}
		// The gateway is reached directly, not through itself or a global proxy.
		preflightTransport := transport.Clone()
		preflightTransport.Proxy = nil
		client.preflightHTTP = newHTTPClient(preflightTransport)
		client.proxyURL = proxy
		client.transportTemplate = transport
	}
	return client, nil
}

// NewClientWithTransport permits a mocked transport without making the Binance
// origin configurable. Callers must not supply an untrusted transport.
func NewClientWithTransport(transport http.RoundTripper) *Client {
	if transport == nil {
		transport = cloneDirectTransport()
	}
	return &Client{http: newHTTPClient(transport)}
}

func newHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func cloneDirectTransport() *http.Transport {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok && transport != nil {
		clone := transport.Clone()
		clone.Proxy = nil
		return clone
	}
	// A replaced global transport must not remove timeouts or enable an
	// environment proxy when this client is configured for direct access.
	return &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func (c *Client) CloseIdleConnections() {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
	if c != nil && c.preflightHTTP != nil {
		c.preflightHTTP.CloseIdleConnections()
	}
}

// History reads the complete requested millisecond interval or returns an
// error with no receipts. A full page is recursively divided in time, because
// this endpoint has no offset cursor. Partial/saturated scans never settle funds.
func (c *Client) History(ctx context.Context, apiKey, secret, receiverUID string, from, to time.Time) ([]Receipt, error) {
	if err := validateCredentials(apiKey, secret); err != nil {
		return nil, err
	}
	if !validUID(receiverUID) {
		return nil, errors.New("binance: invalid receiving UID")
	}
	if from.IsZero() || to.IsZero() || from.UnixMilli() < 0 || to.Before(from) {
		return nil, errors.New("binance: invalid history interval")
	}
	ctx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()
	operation, finish, err := c.beginOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	offset, err := operation.syncTime(ctx)
	if err != nil {
		return nil, err
	}
	start, end := from.UnixMilli(), to.UnixMilli()
	serverNow := time.Now().UnixMilli() + offset
	if end > serverNow {
		end = serverNow
	}
	if start > end {
		return nil, errors.New("binance: history starts after provider time")
	}
	budget := maxHistoryCalls
	unique := make(map[string]Receipt)
	for start <= end {
		windowEnd := end
		if end-start > maxWindowMillis {
			windowEnd = start + maxWindowMillis
		}
		if err := operation.historyWindow(ctx, apiKey, secret, receiverUID, offset, start, windowEnd, 0, &budget, unique); err != nil {
			return nil, err
		}
		if windowEnd == end {
			break
		}
		start = windowEnd + 1
	}
	result := make([]Receipt, 0, len(unique))
	for _, receipt := range unique {
		result = append(result, receipt)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Time.Equal(result[j].Time) {
			return result[i].TransactionID < result[j].TransactionID
		}
		return result[i].Time.Before(result[j].Time)
	})
	return result, nil
}

type historyResponse struct {
	Code    string            `json:"code"`
	Success bool              `json:"success"`
	Data    []json.RawMessage `json:"data"`
}

func (c *Client) historyWindow(ctx context.Context, apiKey, secret, receiverUID string, offset, from, to int64, depth int, budget *int, receipts map[string]Receipt) error {
	if depth > 48 || *budget <= 0 {
		return errors.New("binance: history scan limit reached")
	}
	*budget = *budget - 1
	var response historyResponse
	params := url.Values{
		"startTime": {strconv.FormatInt(from, 10)},
		"endTime":   {strconv.FormatInt(to, 10)},
		"limit":     {"100"},
	}
	if err := c.get(ctx, historyPath, params, apiKey, secret, offset, &response); err != nil {
		return err
	}
	if response.Code != "000000" || !response.Success {
		return errors.New("binance: Pay history query rejected")
	}
	if len(response.Data) >= 100 {
		if from >= to {
			return errors.New("binance: history timestamp contains too many transfers")
		}
		middle := from + (to-from)/2
		if err := c.historyWindow(ctx, apiKey, secret, receiverUID, offset, from, middle, depth+1, budget, receipts); err != nil {
			return err
		}
		return c.historyWindow(ctx, apiKey, secret, receiverUID, offset, middle+1, to, depth+1, budget, receipts)
	}
	for _, body := range response.Data {
		var transfer struct {
			ID       string `json:"transactionId"`
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
			Time     int64  `json:"transactionTime"`
			Kind     string `json:"orderType"`
			Receiver struct {
				UID textID `json:"binanceId"`
			} `json:"receiverInfo"`
		}
		if err := json.Unmarshal(body, &transfer); err != nil {
			return errors.New("binance: invalid transfer response")
		}
		if transfer.Currency != "USDT" || string(transfer.Receiver.UID) != receiverUID ||
			(transfer.Kind != "C2C" && transfer.Kind != "PAY") || transfer.Time <= 0 ||
			transfer.Time < from || transfer.Time > to || transfer.ID == "" || len(transfer.ID) > 160 {
			continue
		}
		amount, err := ParseAmount(transfer.Amount)
		if err != nil || amount <= 0 {
			// Outgoing, malformed and over-precision transfers cannot match a quote.
			continue
		}
		receipt := Receipt{TransactionID: transfer.ID, AmountUnits: amount, Time: time.UnixMilli(transfer.Time).UTC()}
		if prior, exists := receipts[receipt.TransactionID]; exists {
			if prior.AmountUnits != receipt.AmountUnits || !prior.Time.Equal(receipt.Time) {
				return errors.New("binance: conflicting transfer data")
			}
			continue
		}
		receipts[receipt.TransactionID] = receipt
	}
	return nil
}

// Probe verifies read permission and access to Pay history, without trading,
// transferring funds, or returning any account/credential data.
func (c *Client) Probe(ctx context.Context, apiKey, secret string) error {
	if err := validateCredentials(apiKey, secret); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	operation, finish, err := c.beginOperation(ctx)
	if err != nil {
		return err
	}
	defer finish()
	offset, err := operation.syncTime(ctx)
	if err != nil {
		return err
	}
	var permissions struct {
		EnableReading bool `json:"enableReading"`
	}
	if err := operation.get(ctx, "/sapi/v1/account/apiRestrictions", nil, apiKey, secret, offset, &permissions); err != nil {
		return err
	}
	if !permissions.EnableReading {
		return errors.New("binance: API reading permission is not enabled")
	}
	now := time.Now().UnixMilli() + offset
	var response historyResponse
	params := url.Values{"startTime": {strconv.FormatInt(now-int64(24*time.Hour/time.Millisecond), 10)}, "endTime": {strconv.FormatInt(now, 10)}, "limit": {"1"}}
	if err := operation.get(ctx, historyPath, params, apiKey, secret, offset, &response); err != nil {
		return err
	}
	if response.Code != "000000" || !response.Success {
		return errors.New("binance: Pay history query rejected")
	}
	return nil
}

func (c *Client) syncTime(ctx context.Context) (int64, error) {
	var response struct {
		ServerTime int64 `json:"serverTime"`
	}
	before := time.Now().UnixMilli()
	if err := c.get(ctx, "/api/v3/time", nil, "", "", 0, &response); err != nil {
		return 0, err
	}
	after := time.Now().UnixMilli()
	if response.ServerTime <= 0 {
		return 0, errors.New("binance: invalid provider time")
	}
	return response.ServerTime - (before + (after-before)/2), nil
}

func (c *Client) get(ctx context.Context, path string, params url.Values, apiKey, secret string, offset int64, target any) error {
	if c == nil || c.http == nil {
		return errors.New("binance: client is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	query := make(url.Values, len(params)+3)
	for key, values := range params {
		query[key] = append([]string(nil), values...)
	}
	if apiKey != "" {
		query.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli()+offset, 10))
		query.Set("recvWindow", "10000")
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = io.WriteString(mac, query.Encode())
		query.Set("signature", hex.EncodeToString(mac.Sum(nil)))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+path+"?"+query.Encode(), nil)
	if err != nil {
		return errors.New("binance: could not create request")
	}
	if apiKey != "" {
		req.Header.Set("X-MBX-APIKEY", apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// net/http errors can contain a signed URL or proxy credentials.
		return errors.New("binance: network request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusTeapot {
		return &RateLimitError{RetryAfter: retryDelay(resp.Header.Get("Retry-After"), resp.StatusCode), StatusCode: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binance: request failed (HTTP %d)", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("binance: response could not be read")
	}
	if len(body) > maxResponseSize {
		return errors.New("binance: response exceeds size limit")
	}
	// Spot error codes are numeric; Pay success codes are strings. Only a
	// numeric code is included in errors, never the upstream message or body.
	var apiError struct {
		Code json.RawMessage `json:"code"`
	}
	if json.Unmarshal(body, &apiError) == nil {
		var code int
		if json.Unmarshal(apiError.Code, &code) == nil && code < 0 {
			return fmt.Errorf("binance: request rejected (code %d)", code)
		}
	}
	if err := json.Unmarshal(body, target); err != nil {
		return errors.New("binance: invalid response format")
	}
	return nil
}

func validateCredentials(apiKey, secret string) error {
	if apiKey == "" || secret == "" || len(apiKey) > 512 || len(secret) > 512 {
		return errors.New("binance: API credentials are required")
	}
	for _, ch := range apiKey {
		if ch < 33 || ch > 126 {
			return errors.New("binance: invalid API key")
		}
	}
	return nil
}

func validUID(value string) bool {
	if value == "" || len(value) > 80 {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// IDs are sometimes encoded as JSON numbers. Preserve every digit rather than
// decoding via float64, which would round UIDs larger than 2^53.
type textID string

func (value *textID) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(raw, []byte("null")) {
		*value = ""
		return nil
	}
	var text string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return errors.New("binance: invalid receiver UID")
		}
	} else {
		text = string(raw)
	}
	if text != "" && !validUID(text) {
		return errors.New("binance: invalid receiver UID")
	}
	*value = textID(text)
	return nil
}

func parseProxyURL(raw string) (*url.URL, error) {
	proxy, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || proxy.Hostname() == "" || proxy.Opaque != "" || (proxy.Path != "" && proxy.Path != "/") || proxy.RawQuery != "" || proxy.ForceQuery || proxy.Fragment != "" {
		return nil, errors.New("binance: invalid proxy configuration")
	}
	switch proxy.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("binance: unsupported proxy scheme")
	}
	if port := proxy.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, errors.New("binance: invalid proxy port")
		}
	}
	return proxy, nil
}

func retryDelay(header string, status int) time.Duration {
	delay := 2 * time.Minute
	if status == http.StatusTeapot {
		delay = 24 * time.Hour
	}
	if seconds, err := strconv.ParseInt(strings.TrimSpace(header), 10, 64); err == nil && seconds > 0 {
		// Avoid overflow on an invalid upstream header.
		if seconds > int64(7*24*time.Hour/time.Second) {
			return 7 * 24 * time.Hour
		}
		candidate := time.Duration(seconds) * time.Second
		if candidate > delay {
			delay = candidate
		}
	} else if until, err := http.ParseTime(header); err == nil {
		if candidate := time.Until(until); candidate > delay {
			delay = candidate
		}
	}
	return delay
}

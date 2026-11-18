package repository

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const excelBPSPhysicalRequestTimeout = 10 * time.Minute

var _ service.HTTPUpstreamSession = (*httpUpstreamService)(nil)
var _ service.HTTPUpstreamSession = (*astraRoutingUpstream)(nil)
var _ service.HTTPUpstream = (*excelBPSHTTPSession)(nil)

// NewExcelBPSSession keeps the original plugin's client lifetime local to a
// logical request. Continuations reuse the same proxy's transport; unrelated
// requests never inherit its connections, protocol feedback or idle PING policy.
func (s *httpUpstreamService) NewExcelBPSSession() (service.HTTPUpstream, func()) {
	session := &excelBPSHTTPSession{parent: s, clients: make(map[string]*http.Client)}
	return session, session.close
}

func (s *astraRoutingUpstream) NewExcelBPSSession() (service.HTTPUpstream, func()) {
	if provider, ok := s.delegate.(service.HTTPUpstreamSession); ok {
		return provider.NewExcelBPSSession()
	}
	return s, func() {}
}

type excelBPSHTTPSession struct {
	parent  *httpUpstreamService
	mu      sync.Mutex
	clients map[string]*http.Client
	closed  bool
}

func (s *excelBPSHTTPSession) client(proxyURL string) (*http.Client, error) {
	key, proxy, err := normalizeProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("Excel BPS HTTP session is closed")
	}
	if client := s.clients[key]; client != nil {
		return client, nil
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("Excel BPS requires a standard default HTTP transport")
	}
	transport := base.Clone()
	// Original SO New (0x41e440): +272 is ResponseHeaderTimeout, not
	// IdleConnTimeout (+264), which is inherited from DefaultTransport.
	transport.Proxy = http.ProxyURL(proxy)
	transport.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.DisableCompression = true
	transport.ForceAttemptHTTP2 = true
	transport.TLSHandshakeTimeout = 15 * time.Second
	transport.ResponseHeaderTimeout = 120 * time.Second
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 8
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	s.clients[key] = client
	return client, nil
}

func (s *excelBPSHTTPSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for _, client := range s.clients {
		client.CloseIdleConnections()
	}
	s.clients = nil
}

func (s *excelBPSHTTPSession) Do(req *http.Request, proxyURL string, accountID int64, _ int) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("nil upstream request")
	}
	if err := s.parent.proxyRouter.GuardKiloInferenceRequest(req, accountID); err != nil {
		return nil, err
	}
	requestcapture.FromContext(req.Context()).BindAccount(accountID)
	req = req.WithContext(requestcapture.WithAccount(req.Context(), accountID))
	if err := s.parent.validateRequestHost(req); err != nil {
		requestcapture.FromContext(req.Context()).SelectionFailed(accountID, 0, nil, nil, err)
		return nil, err
	}
	ctx, cancel := context.WithTimeout(req.Context(), excelBPSPhysicalRequestTimeout)
	// The logical BPS owner already chose and leased its egress. Re-entering
	// ordinary account routing would replace that proxy (or explicit direct)
	// with unrelated account bindings. Keep the route handle body-owned while
	// the inference and host guards above still run for every physical send.
	route := &service.AccountProxyRoute{Context: ctx, URL: proxyURL}
	req = req.WithContext(route.Context)
	transferred := false
	defer func() {
		if !transferred {
			cancel()
			route.Close()
		}
	}()
	client, err := s.client(route.URL)
	if err != nil {
		requestcapture.FromContext(req.Context()).SelectionFailed(accountID, 0, nil, nil, err)
		return nil, err
	}
	// Original Client.Forward opts out of content compression. Clone the
	// header map so the caller's request remains reusable for another endpoint.
	req = req.Clone(req.Context())
	// The original Forward clears GetBody before Client.Do, preventing
	// transport retries from rewinding a generation or attachment upload.
	req.GetBody = nil
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := doUpstreamRequest(client, req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return resp, err
	}
	resp.Body = &excelBPSSessionBody{ReadCloser: resp.Body, cancel: cancel, release: route.Close}
	transferred = true
	return resp, nil
}

// BPS uses the original standard TLS transport even through the TLS-capable port.
func (s *excelBPSHTTPSession) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, concurrency)
}

type excelBPSSessionBody struct {
	io.ReadCloser
	cancel   context.CancelFunc
	release  func()
	once     sync.Once
	close    sync.Once
	closeErr error
}

func (b *excelBPSSessionBody) finish() {
	b.once.Do(func() {
		b.cancel()
		b.release()
	})
}

func (b *excelBPSSessionBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.finish()
	}
	return n, err
}

func (b *excelBPSSessionBody) Close() error {
	// Cancel first so a concurrent Read leaves the connection before its body
	// or decompressor is closed, matching the shared transport's close ordering.
	b.finish()
	b.close.Do(func() { b.closeErr = b.ReadCloser.Close() })
	return b.closeErr
}

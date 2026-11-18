package repository

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestExcelBPSHTTPSessionOriginalTransportAndPoolLifetime(t *testing.T) {
	parent := &httpUpstreamService{}
	upstream, closeSession := parent.NewExcelBPSSession()
	defer closeSession()
	session := upstream.(*excelBPSHTTPSession)
	require.Empty(t, session.clients)
	client, err := session.client("")
	require.NoError(t, err)
	transport := client.Transport.(*http.Transport)
	base := http.DefaultTransport.(*http.Transport)
	require.NotSame(t, base, transport)
	require.True(t, transport.DisableCompression)
	require.True(t, transport.ForceAttemptHTTP2)
	require.Equal(t, 15*time.Second, transport.TLSHandshakeTimeout)
	require.Equal(t, 120*time.Second, transport.ResponseHeaderTimeout)
	require.Equal(t, base.IdleConnTimeout, transport.IdleConnTimeout)
	require.Equal(t, base.MaxConnsPerHost, transport.MaxConnsPerHost)
	require.Equal(t, 32, transport.MaxIdleConns)
	require.Equal(t, 8, transport.MaxIdleConnsPerHost)
	// These are named-field assertions rather than assuming the Go 1.26
	// binary's offsets remain identical in the current compiler. Original SO
	// offsets: 233=DisableCompression, 369=ForceAttemptHTTP2, 272=header timeout,
	// 264=idle timeout. Only the header timeout was overwritten with 120s.
	fields := reflect.ValueOf(transport).Elem()
	for name, expected := range map[string]any{
		"DisableCompression": true, "ForceAttemptHTTP2": true,
		"TLSHandshakeTimeout": 15 * time.Second, "ResponseHeaderTimeout": 120 * time.Second,
		"IdleConnTimeout": base.IdleConnTimeout, "MaxIdleConns": 32, "MaxIdleConnsPerHost": 8,
	} {
		field := fields.FieldByName(name)
		require.True(t, field.IsValid(), name)
		require.Equal(t, expected, field.Interface(), name)
	}
	require.Zero(t, client.Timeout)
	require.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	r, err := http.NewRequest(http.MethodGet, "https://bps.openai.com", nil)
	require.NoError(t, err)
	proxy, err := transport.Proxy(r)
	require.NoError(t, err)
	require.Nil(t, proxy, "direct mode must not inherit the host's environment proxy")
	again, err := session.client("")
	require.NoError(t, err)
	require.Same(t, client, again)
	proxyClient, err := session.client("http://EXAMPLE.test:80/")
	require.NoError(t, err)
	normalized, err := session.client("http://example.test")
	require.NoError(t, err)
	require.Same(t, proxyClient, normalized)
	require.NotSame(t, client, proxyClient)
	otherUpstream, closeOther := parent.NewExcelBPSSession()
	defer closeOther()
	otherClient, err := otherUpstream.(*excelBPSHTTPSession).client("")
	require.NoError(t, err)
	require.NotSame(t, transport, otherClient.Transport)

	closeSession()
	closeSession()
	require.Nil(t, session.clients, "finished logical sessions release their client references")
	stillOpen, err := otherUpstream.(*excelBPSHTTPSession).client("")
	require.NoError(t, err)
	require.Same(t, otherClient, stillOpen, "closing one session cannot retire another session")
	_, err = session.client("")
	require.Error(t, err)
	require.Empty(t, parent.clients, "session clients must not enter the shared cache")
	wrapped := &astraRoutingUpstream{delegate: parent}
	wrappedSession, closeWrapped := wrapped.NewExcelBPSSession()
	defer closeWrapped()
	require.IsType(t, session, wrappedSession)
}

func TestExcelBPSHTTPSessionWireReuseAndCloseDoesNotCancelStream(t *testing.T) {
	var connections atomic.Int32
	type wireHeaders struct{ encoding, authorization string }
	seenHeaders := make(chan wireHeaders, 3)
	upstreamServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders <- wireHeaders{r.Header.Get("Accept-Encoding"), r.Header.Get("Authorization")}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/unexpected", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "complete")
	}))
	upstreamServer.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	upstreamServer.Start()
	defer upstreamServer.Close()
	session, finish := (&httpUpstreamService{}).NewExcelBPSSession()
	defer finish()
	for range 2 {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, upstreamServer.URL, strings.NewReader("synthetic"))
		require.NoError(t, err)
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("Authorization", "Bearer synthetic")
		resp, err := session.Do(req, "", 7, 1)
		require.NoError(t, err)
		require.Nil(t, resp.Request.GetBody, "physical generation body cannot be replayed")
		require.NotNil(t, req.GetBody, "session must not mutate the caller's rewind contract")
		deadline, ok := resp.Request.Context().Deadline()
		require.True(t, ok)
		require.InDelta(t, excelBPSPhysicalRequestTimeout.Seconds(), time.Until(deadline).Seconds(), 2)
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "complete", string(data))
		headers := <-seenHeaders
		require.Equal(t, "identity", headers.encoding)
		require.Equal(t, "Bearer synthetic", headers.authorization)
		require.ErrorIs(t, resp.Request.Context().Err(), context.Canceled, "EOF retires the physical context")
		require.NoError(t, resp.Body.Close())
		require.Equal(t, "gzip", req.Header.Get("Accept-Encoding"), "wire preparation must not mutate the caller's headers")
	}
	require.EqualValues(t, 1, connections.Load(), "same logical request reuses its transport connection")
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, upstreamServer.URL+"/redirect", strings.NewReader("synthetic"))
	require.NoError(t, err)
	resp, err := session.Do(req, "", 7, 1)
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first-")
		_ = http.NewResponseController(w).Flush()
		select {
		case <-release:
			_, _ = io.WriteString(w, "last")
		case <-r.Context().Done():
		}
	}))
	defer stream.Close()
	defer unblock()
	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, stream.URL, nil)
	require.NoError(t, err)
	resp, err = session.DoWithTLS(req, "", 7, 1, &tlsfingerprint.Profile{Name: "not-used-by-bps"})
	require.NoError(t, err)
	defer resp.Body.Close()
	finish()
	require.NoError(t, resp.Request.Context().Err(), "closing the session only closes idle connections")
	unblock()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "first-last", string(data))
}

func TestExcelBPSHTTPSessionPreservesSelectedEgress(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "direct")
	}))
	defer origin.Close()
	mihomo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "mihomo")
	}))
	defer mihomo.Close()
	static := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "static")
	}))
	defer static.Close()
	var unrelatedCalls atomic.Int32
	unrelated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unrelatedCalls.Add(1)
		_, _ = io.WriteString(w, "ordinary-account-proxy")
	}))
	defer unrelated.Close()
	ordinaryURL, err := url.Parse(unrelated.URL)
	require.NoError(t, err)
	ordinaryPort, err := strconv.Atoi(ordinaryURL.Port())
	require.NoError(t, err)
	for _, withBindings := range []bool{false, true} {
		bindingName := "without-account-bindings"
		if withBindings {
			bindingName = "with-account-bindings"
		}
		for _, egress := range []struct{ name, proxy string }{{"mihomo", mihomo.URL}, {"static", static.URL}, {"direct", ""}} {
			t.Run(bindingName+"/"+egress.name, func(t *testing.T) {
				server := miniredis.RunT(t)
				rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
				defer rdb.Close()
				bindings := routingBindingFixture{}
				if withBindings {
					bindings[9] = []service.AccountProxyBinding{{ProxyID: 4, MaxConcurrency: 1, Proxy: &service.Proxy{ID: 4, Status: service.StatusActive, Protocol: "http", Host: ordinaryURL.Hostname(), Port: ordinaryPort}}}
				}
				router := service.NewAccountProxyRouter(bindings, NewProxyConcurrencyCache(rdb))
				upstream, finish := (&httpUpstreamService{proxyRouter: router}).NewExcelBPSSession()
				defer finish()
				ctx := service.WithAccountProxyRouting(t.Context())
				for range 2 {
					req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin.URL, strings.NewReader("synthetic"))
					require.NoError(t, err)
					resp, err := upstream.Do(req, egress.proxy, 9, 1)
					require.NoError(t, err)
					body, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					require.NoError(t, resp.Body.Close())
					require.Equal(t, egress.name, string(body), "ordinary routing cannot replace the selected BPS egress")
				}
				counts, err := router.GetProxyConcurrencyBatch(t.Context(), []int64{4})
				require.NoError(t, err)
				require.Zero(t, counts[4])
			})
		}
	}
	require.Zero(t, unrelatedCalls.Load())
}

func TestExcelBPSHTTPSessionParentCancellationAndRouteLease(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(proxyURL.Port())
	require.NoError(t, err)
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer redisClient.Close()
	router := service.NewAccountProxyRouter(routingBindingFixture{9: {{ProxyID: 4, MaxConcurrency: 1, Proxy: &service.Proxy{ID: 4, Status: service.StatusActive, Protocol: "http", Host: proxyURL.Hostname(), Port: port}}}}, NewProxyConcurrencyCache(redisClient))
	upstream, finish := (&httpUpstreamService{proxyRouter: router}).NewExcelBPSSession()
	defer finish()
	ctx, cancel := context.WithCancel(service.WithAccountProxyRouting(t.Context()))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://bps.invalid/stream", nil)
	require.NoError(t, err)
	resp, err := upstream.Do(req, proxy.URL, 9, 1)
	require.NoError(t, err)
	defer resp.Body.Close()
	counts, err := router.GetProxyConcurrencyBatch(t.Context(), []int64{4})
	require.NoError(t, err)
	require.Zero(t, counts[4], "the selected BPS route does not reacquire an ordinary proxy slot")
	cancel()
	_, err = io.ReadAll(resp.Body)
	require.Error(t, err)
	require.NoError(t, resp.Body.Close())
	counts, err = router.GetProxyConcurrencyBatch(t.Context(), []int64{4})
	require.NoError(t, err)
	require.Zero(t, counts[4])
}

func TestExcelBPSHTTPSessionGuardsAndNoApplicationRetry(t *testing.T) {
	var calls atomic.Int32
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer broken.Close()
	session, finish := (&httpUpstreamService{}).NewExcelBPSSession()
	defer finish()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, broken.URL, strings.NewReader("synthetic"))
	require.NoError(t, err)
	_, err = session.Do(req, "", 8, 1)
	require.Error(t, err)
	require.EqualValues(t, 1, calls.Load())

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = true
	guarded, closeGuarded := (&httpUpstreamService{cfg: cfg}).NewExcelBPSSession()
	defer closeGuarded()
	_, err = guarded.Do(req.WithContext(service.WithAccountProxyRouting(t.Context())), "", 8, 1)
	require.Error(t, err)
	require.EqualValues(t, 1, calls.Load(), "private-host denial happens before dialing")
	_, err = session.Do(req, "://invalid", 8, 1)
	require.Error(t, err)
	require.EqualValues(t, 1, calls.Load(), "invalid explicit proxy never falls back to direct")

	kilo := service.NewAccountProxyRouter(excelBPSSessionKiloReader{}, nil)
	blocked, closeBlocked := (&httpUpstreamService{proxyRouter: kilo}).NewExcelBPSSession()
	defer closeBlocked()
	_, err = blocked.Do(req.WithContext(service.WithAccountProxyRouting(t.Context())), "", 8, 1)
	require.Error(t, err)
	require.EqualValues(t, 1, calls.Load(), "Kilo isolation remains in force")
}

func TestExcelBPSHTTPSessionBodyCancellationContract(t *testing.T) {
	for _, readErr := range []error{io.EOF, io.ErrUnexpectedEOF} {
		t.Run(readErr.Error(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			var released atomic.Int32
			body := &excelBPSSessionReadCloser{readErr: readErr}
			wrapped := &excelBPSSessionBody{ReadCloser: body, cancel: cancel, release: func() { released.Add(1) }}
			_, err := wrapped.Read(make([]byte, 8))
			require.ErrorIs(t, err, readErr)
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			require.EqualValues(t, 1, released.Load())
			require.NoError(t, wrapped.Close())
			require.NoError(t, wrapped.Close())
			require.EqualValues(t, 1, body.closed.Load())
			require.EqualValues(t, 1, released.Load())
		})
	}
	t.Run("close cancels pending read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var released atomic.Int32
		body := &excelBPSSessionReadCloser{ctx: ctx, started: make(chan struct{})}
		wrapped := &excelBPSSessionBody{ReadCloser: body, cancel: cancel, release: func() { released.Add(1) }}
		finished := make(chan error, 1)
		go func() { _, err := wrapped.Read(make([]byte, 8)); finished <- err }()
		<-body.started
		require.NoError(t, wrapped.Close())
		select {
		case err := <-finished:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("closing the body did not cancel its pending read")
		}
		require.EqualValues(t, 1, released.Load())
		require.EqualValues(t, 1, body.closed.Load())
	})
}

type excelBPSSessionReadCloser struct {
	ctx     context.Context
	started chan struct{}
	readErr error
	closed  atomic.Int32
}

func (b *excelBPSSessionReadCloser) Read([]byte) (int, error) {
	if b.started != nil {
		close(b.started)
		<-b.ctx.Done()
		return 0, b.ctx.Err()
	}
	return 0, b.readErr
}

func (b *excelBPSSessionReadCloser) Close() error { b.closed.Add(1); return nil }

type excelBPSSessionKiloReader struct{}

func (excelBPSSessionKiloReader) GetAccountProxyBindings(context.Context, int64) ([]service.AccountProxyBinding, error) {
	return nil, nil
}

func (excelBPSSessionKiloReader) GetKiloInferenceAccount(context.Context, int64) (*service.Account, error) {
	return nil, errors.New("synthetic unavailable identity")
}

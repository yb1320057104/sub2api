package service

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestKiloProxyPreflightUsesSelectedProxyWithoutCredentials(t *testing.T) {
	var calls atomic.Int32
	client := kiloRemoteTestClient(t, func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "https", req.URL.Scheme)
		require.Empty(t, req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("Cookie"))
		switch req.URL.Host {
		case "auth.openai.com":
			response := kiloRemoteTestResponse(http.StatusForbidden, "challenge")
			response.Header.Set("Cf-Mitigated", "challenge")
			return response, nil
		case "api.kilo.ai":
			return kiloRemoteTestResponse(http.StatusUnauthorized, "unauthenticated"), nil
		case "app.kilo.ai":
			return kiloRemoteTestResponse(http.StatusOK, `{}`), nil
		default:
			t.Errorf("unexpected preflight target %s", req.URL.Host)
			return nil, errors.New("unexpected target")
		}
	})
	checks, err := client.ProbeLoginProxy(context.Background(), "socks5://selected-proxy.invalid:1080")
	require.NoError(t, err)
	require.Len(t, checks, 3)
	require.EqualValues(t, 3, calls.Load())
	for _, check := range checks {
		require.True(t, check.Reachable)
		require.Empty(t, check.Error)
	}
	require.True(t, checks[0].Challenge)
}

func TestKiloProxyPreflightRejectsTLSFailureAndNeverFallsBack(t *testing.T) {
	var calls atomic.Int32
	client := kiloRemoteTestClient(t, func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, &url.Error{Op: "Get", URL: "https://secret-user:secret-password@proxy.invalid", Err: x509.UnknownAuthorityError{}}
	})
	checks, err := client.ProbeLoginProxy(context.Background(), "socks5://selected-proxy.invalid:1080")
	require.NoError(t, err)
	require.Len(t, checks, 3)
	for _, check := range checks {
		require.False(t, check.Reachable)
		require.Equal(t, "tls_failed", check.Error)
		require.NotContains(t, check.Error, "secret")
	}
	_, err = client.ProbeLoginProxy(context.Background(), "")
	require.Equal(t, "KILO_LOGIN_PROXY_REQUIRED", infraerrors.Reason(err))
	require.EqualValues(t, 3, calls.Load())
}

type kiloPreflightProxyRepo struct{ ProxyRepository }

func (kiloPreflightProxyRepo) GetByID(_ context.Context, id int64) (*Proxy, error) {
	return &Proxy{ID: id, Protocol: "http", Host: "fixed.invalid", Port: 8080, Status: StatusActive, CreatedAt: time.Now()}, nil
}

type kiloPreflightRemote struct {
	KiloRemote
	calls int
	ok    bool
}

func (r *kiloPreflightRemote) ProbeLoginProxy(_ context.Context, _ string) ([]KiloProxyCheck, error) {
	r.calls++
	return []KiloProxyCheck{{Target: "openai", Reachable: r.ok}, {Target: "kilo_api", Reachable: r.ok}, {Target: "kilo_login", Reachable: r.ok}}, nil
}

func TestKiloProxyPreflightRunsBeforeAnyImportMutation(t *testing.T) {
	svc, _, accounts := kiloPoolFixture(t)
	remote := &kiloPreflightRemote{}
	svc.remote, svc.proxies = remote, kiloPreflightProxyRepo{}
	err := svc.preflightOnboardingProxies(context.Background(), KiloOnboardingBatchInput{Items: []KiloOnboardingItem{{LoginProxyID: 15}}})
	require.Equal(t, "KILO_LOGIN_PROXY_UNREACHABLE", infraerrors.Reason(err))
	require.Equal(t, 1, remote.calls)
	require.Empty(t, accounts.writes)
}

func TestKiloProxyPreflightUsesExistingSlotsPinnedProxy(t *testing.T) {
	svc, mock, _ := kiloPoolFixture(t)
	remote := &kiloPreflightRemote{ok: true}
	svc.remote, svc.proxies = remote, kiloPreflightProxyRepo{}
	mock.ExpectQuery("SELECT login_proxy_id FROM pk_kilo_prepared_accounts").WithArgs(int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"proxy"}).AddRow(13))
	err := svc.preflightOnboardingProxies(context.Background(), KiloOnboardingBatchInput{Items: []KiloOnboardingItem{{PreparedID: 2, LoginProxyID: 15}, {LoginProxyID: 13}}})
	require.NoError(t, err)
	require.Equal(t, 1, remote.calls, "a pinned proxy shared by the requested slot is checked once")
}

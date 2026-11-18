package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestKiloRemotePoolDisconnectScopeAndConfirmation(t *testing.T) {
	for _, organization := range []string{"", "aa52a13e-1af9-4b3c-977b-66eae64d4b8d"} {
		t.Run(organization, func(t *testing.T) {
			calls := 0
			client := kiloRemoteTestClient(t, func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, http.MethodPost, req.Method)
				require.Equal(t, "https", req.URL.Scheme)
				require.Equal(t, "api.kilo.ai", req.URL.Host)
				require.Equal(t, "/api/trpc/openAiChatGpt.disconnect", req.URL.Path)
				require.Empty(t, req.URL.RawQuery)
				require.Equal(t, "Bearer synthetic-kilo-key", req.Header.Get("Authorization"))
				require.Equal(t, "application/json", req.Header.Get("Content-Type"))
				raw, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				var input map[string]any
				require.NoError(t, json.Unmarshal(raw, &input))
				want := map[string]any{}
				if organization != "" {
					want["organizationId"] = organization
				}
				require.Equal(t, want, input, "identity transformer requires an unwrapped organization input")
				return kiloRemoteTestResponse(http.StatusOK, `{"result":{"data":{"state":"disconnected"}}}`), nil
			})
			require.NoError(t, client.Disconnect(context.Background(), "Bearer synthetic-kilo-key", organization, "socks5://selected-proxy.invalid:1080"))
			require.Equal(t, 1, calls)
		})
	}
}

func TestKiloRemotePoolDisconnectRejectsUnconfirmedResults(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"unknown state", `{"result":{"data":{"state":"unknown"}}}`, 200},
		{"still connected", `{"result":{"data":{"state":"connected"}}}`, 200},
		{"missing result", `{"result":null}`, 200},
		{"conflicting error", `{"result":{"data":{"state":"disconnected"}},"error":{"data":{"code":"FORBIDDEN"}}}`, 200},
		{"accepted is not confirmed", `{"result":{"data":{"state":"disconnected"}}}`, 202},
		{"permission denial", `{"error":{"code":"account_banned"}}`, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := kiloRemoteTestClient(t, func(*http.Request) (*http.Response, error) {
				return kiloRemoteTestResponse(tc.status, tc.body), nil
			})
			err := client.Disconnect(context.Background(), "synthetic-kilo-key", "", "socks5://selected-proxy.invalid:1080")
			require.Error(t, err)
			if tc.status == http.StatusOK {
				require.Equal(t, "KILO_REMOTE_RESPONSE_INVALID", infraerrors.Reason(err))
			}
			require.NotEqual(t, KiloIdentityForbiddenReason, infraerrors.Reason(err), "a disconnect permission denial cannot establish an identity ban")
		})
	}
}

type kiloPoolDisconnectAccounts struct {
	AccountRepository
	gateway *Account
}

func (f kiloPoolDisconnectAccounts) GetByID(context.Context, int64) (*Account, error) {
	return f.gateway, nil
}

type kiloPoolDisconnectProxies struct{ ProxyRepository }

func (kiloPoolDisconnectProxies) GetByID(context.Context, int64) (*Proxy, error) {
	return &Proxy{ID: 77, Protocol: "socks5", Host: "selected-proxy.invalid", Port: 1080, Status: StatusActive}, nil
}

type kiloPoolDisconnectEgress struct {
	KiloEgressManager
	calls int
}

func (f *kiloPoolDisconnectEgress) List(context.Context, int64) ([]KiloEgressRoute, error) {
	f.calls++
	return nil, errors.New("unconfirmed disconnect attempted egress release")
}

func (f *kiloPoolDisconnectEgress) Release(context.Context, int64) error {
	f.calls++
	return errors.New("unconfirmed disconnect attempted egress release")
}

func TestKiloRemotePoolUnconfirmedDisconnectKeepsSlot(t *testing.T) {
	for _, tc := range []struct {
		name, mutationState, finalState, reason string
		statusCalls                             int
	}{
		{"unknown mutation", "unknown", "", "KILO_REMOTE_RESPONSE_INVALID", 1},
		{"confirmed mutation but still connected", "disconnected", "connected", "KILO_CONFLICT", 2},
		{"confirmed mutation but unknown verification", "disconnected", "unknown", "KILO_REMOTE_RESPONSE_INVALID", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			sourceID, gatewayID := int64(122), int64(124)
			job := &kiloClaim{ID: 11, PreparedID: 2, SourceID: &sourceID, AccountID: &gatewayID, BindingRevision: 5, Lease: "synthetic-lease"}
			lock := job.PreparedID ^ int64(-5424871661416316928)
			mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_lock($1)")).WithArgs(lock).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT EXISTS.*FROM pk_kilo_jobs").WithArgs(job.ID, job.Lease).
				WillReturnRows(sqlmock.NewRows([]string{"owned"}).AddRow(true))
			mock.ExpectQuery("SELECT p.source_account_id,p.binding_revision,p.remote_user_id.*binding_state='unbinding'").WithArgs(job.PreparedID, job.ID).
				WillReturnRows(sqlmock.NewRows([]string{"source", "revision", "remote_id", "email", "identity"}).AddRow(sourceID, job.BindingRevision, "kilo-user", "source@example.invalid", "active"))
			mock.ExpectQuery("SELECT email,identity_credential_encrypted").WithArgs(job.PreparedID, sourceID, job.BindingRevision).
				WillReturnRows(sqlmock.NewRows([]string{"email", "credential"}).AddRow("original-kilo@example.invalid", "already-saved"))
			cipher, err := (kiloWorkerCipher{}).Encrypt("synthetic-kilo-key")
			require.NoError(t, err)
			mock.ExpectQuery("SELECT credential_encrypted,organization_id FROM pk_kilo_prepared_accounts").WithArgs(job.PreparedID).
				WillReturnRows(sqlmock.NewRows([]string{"cipher", "organization"}).AddRow(cipher, ""))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).WithArgs(lock).
				WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
			statusCalls, mutations, loginClosed := 0, 0, 0
			remote := kiloRemoteTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/api/profile":
					return kiloRemoteTestResponse(200, `{"user":{"id":"kilo-user","email":"kilo@example.invalid"},"hasPersonalAccount":true}`), nil
				case "/api/trpc/openAiChatGpt.status":
					statusCalls++
					state := "connected"
					if statusCalls > 1 {
						state = tc.finalState
					}
					return kiloRemoteTestResponse(200, `{"result":{"data":{"state":"`+state+`","email":"source@example.invalid","subject":"source-subject"}}}`), nil
				case "/api/trpc/openAiChatGpt.disconnect":
					mutations++
					return kiloRemoteTestResponse(200, `{"result":{"data":{"state":"`+tc.mutationState+`"}}}`), nil
				default:
					t.Fatalf("unexpected request path: %s", req.URL.Path)
					return nil, errors.New("unexpected request")
				}
			})
			egress := &kiloPoolDisconnectEgress{}
			guard := kiloRouterGuardFixture{acquire: func(ctx context.Context, accountID, proxyID int64, proxyURL string) (*KiloEgressPermit, error) {
				require.Equal(t, gatewayID, accountID)
				require.EqualValues(t, 77, proxyID)
				return NewKiloEgressPermit(ctx, func() { loginClosed++ }), nil
			}}
			svc := &KiloService{db: db, remote: remote, encryptor: kiloWorkerCipher{}, egress: egress,
				accounts: kiloPoolDisconnectAccounts{gateway: &Account{ID: gatewayID, Extra: map[string]any{KiloLoginProxyExtraKey: 77}}},
				proxies:  kiloPoolDisconnectProxies{}, router: NewAccountProxyRouter(nil, nil, guard)}
			err = svc.processKiloUnbind(context.Background(), job)
			require.Equal(t, tc.reason, infraerrors.Reason(err))
			require.Equal(t, tc.statusCalls, statusCalls)
			require.Equal(t, 1, mutations)
			require.Equal(t, 1, loginClosed, "the request lease closes without releasing the fixed login pin")
			require.Zero(t, egress.calls, "unknown outcomes retain the request exits and slot")
			require.NoError(t, mock.ExpectationsWereMet(), "no transaction or empty-slot publication is allowed before remote confirmation")
		})
	}
}

package binancepay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type binanceFixtureTransport func(*http.Request) (*http.Response, error)

type binanceUnreadErrorBody struct{ reads int }

func (body *binanceUnreadErrorBody) Read([]byte) (int, error) {
	body.reads++
	return 0, errors.New("private-message")
}

func (*binanceUnreadErrorBody) Close() error { return nil }

func (f binanceFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func fixtureResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

func requireRequestFailure(t *testing.T, err error, stage, reason string, status int, code string) {
	t.Helper()
	var failure *RequestError
	if !errors.As(err, &failure) {
		t.Fatalf("expected safe request failure, got %T", err)
	}
	gotStage, gotReason, gotStatus, gotCode := failure.SafeDiagnostic()
	if gotStage != stage || gotReason != reason || gotStatus != status || gotCode != code {
		t.Fatalf("unexpected safe diagnostic: %s/%s/%d/%s", gotStage, gotReason, gotStatus, gotCode)
	}
	for _, private := range []string{"fixture-secret", "private-message", "proxy.invalid", "signature=", "X-MBX"} {
		if strings.Contains(err.Error(), private) {
			t.Fatal("private transport or response data escaped")
		}
	}
}

func TestBinanceRequestErrorHTTPAndProviderCodes(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason, code string
		status                   int
	}{
		{"ip_allowlist", `{"code":-2015,"msg":"private-message fixture-secret"}`, "api_key_ip_permission", "-2015", 401},
		{"timestamp", `{"code":-1021,"msg":"private-message"}`, "timestamp", "-1021", 400},
		{"signature", `{"code":-1022,"msg":"private-message"}`, "signature", "-1022", 400},
		{"key_format", `{"code":-2014}`, "api_key_invalid", "-2014", 400},
		{"forbidden_html", `<html>private-message</html>`, "access_denied", "", 403},
		{"oversized_waf_page", strings.Repeat("private-message", maxErrorSize), "access_denied", "", 403},
		{"region", `{"msg":"private-message"}`, "region_restricted", "", 451},
		{"upstream", `{"msg":"private-message"}`, "provider_unavailable", "", 503},
		{"numeric_200", `{"code":-2015}`, "api_key_ip_permission", "-2015", 200},
		{"string_200", `{"code":"-2015"}`, "api_key_ip_permission", "-2015", 200},
		{"unsafe_code", `{"code":"private-message fixture-secret"}`, "http_error", "", 400},
		{"oversized_code", `{"code":-12345678901234567890}`, "http_error", "", 400},
		{"invalid_json", `<html>private-message fixture-secret</html>`, "response_invalid", "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := NewClientWithTransport(binanceFixtureTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				return fixtureResponse(request, tc.status, tc.body), nil
			}))
			var response historyResponse
			err := client.get(context.Background(), historyPath, nil, "fixture-key", "fixture-secret", 0, &response)
			requireRequestFailure(t, err, "pay_history", tc.reason, tc.status, tc.code)
			if calls != 1 {
				t.Fatal("failed query was replayed")
			}
		})
	}
}

func TestBinanceRequestErrorPreservesRateLimitWithoutReadingOrRetry(t *testing.T) {
	for _, status := range []int{429, 418} {
		calls := 0
		body := &binanceUnreadErrorBody{}
		client := NewClientWithTransport(binanceFixtureTransport(func(request *http.Request) (*http.Response, error) {
			calls++
			response := fixtureResponse(request, status, `{"code":-2015,"msg":"private-message"}`)
			response.Body = body
			response.Header.Set("Retry-After", "3600")
			return response, nil
		}))
		err := client.Probe(context.Background(), "fixture-key", "fixture-secret")
		var limited *RateLimitError
		if !errors.As(err, &limited) || limited.StatusCode != status || calls != 1 || body.reads != 0 {
			t.Fatal("provider rate-limit identity or no-retry contract changed")
		}
		minimum := time.Hour
		if status == 418 {
			minimum = 24 * time.Hour
		}
		if limited.RetryAfter < minimum {
			t.Fatal("provider cooldown was shortened")
		}
	}
}

func TestBinanceRequestErrorClassifiesTransportWithoutKeepingPrivateCause(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		reason string
	}{
		{"dns", &net.DNSError{Name: "proxy.invalid", Err: "private-message", IsNotFound: true}, "dns"},
		{"refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "connection_refused"},
		{"reset", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, "connection_reset"},
		{"tls", x509.UnknownAuthorityError{}, "tls"},
		{"timeout", context.DeadlineExceeded, "timeout"},
		{"cancelled", context.Canceled, "cancelled"},
		{"unknown", errors.New("private-message fixture-secret"), "network"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClientWithTransport(binanceFixtureTransport(func(request *http.Request) (*http.Response, error) {
				return nil, &url.Error{Op: "GET", URL: "https://proxy.invalid/?signature=fixture-secret", Err: tc.err}
			}))
			err := client.Probe(context.Background(), "fixture-key", "fixture-secret")
			requireRequestFailure(t, err, "time_sync", tc.reason, 0, "")
			if tc.reason == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("timeout identity lost")
			}
		})
	}
}

func TestBinanceRequestErrorProxyConnectStatusDoesNotReachBinance(t *testing.T) {
	for _, status := range []int{407, 403, 502} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var calls atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.Method != http.MethodConnect || request.Host != "api.binance.com:443" {
					t.Error("unexpected proxy request")
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "private-message fixture-secret")
			}))
			defer proxy.Close()
			client, err := NewClient(proxy.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			err = client.Probe(context.Background(), "fixture-key", "fixture-secret")
			reason := "proxy_connect"
			if status == 407 {
				reason = "proxy_auth"
			}
			requireRequestFailure(t, err, "time_sync", reason, status, "")
			if calls.Load() != 1 {
				t.Fatal("proxy rejection was retried or bypassed")
			}
		})
	}
}

func TestBinanceRequestEndpointSigningAndUIDFilteringRemainCompatible(t *testing.T) {
	for _, operation := range []string{"probe", "history", "bills"} {
		t.Run(operation, func(t *testing.T) {
			var paths []string
			now := time.Now().UTC()
			client := NewClientWithTransport(binanceFixtureTransport(func(request *http.Request) (*http.Response, error) {
				paths = append(paths, request.URL.Path)
				if request.Method != "GET" || request.URL.Host != "api.binance.com" || request.URL.Scheme != "https" {
					t.Fatal("request escaped fixed read-only endpoint")
				}
				if request.URL.Path == "/api/v3/time" {
					if request.Header.Get("X-MBX-APIKEY") != "" || request.URL.Query().Get("signature") != "" {
						t.Fatal("public time request received credentials")
					}
					return fixtureResponse(request, 200, fmt.Sprintf(`{"serverTime":%d}`, now.Add(2*time.Minute).UnixMilli())), nil
				}
				query := request.URL.Query()
				signature := query.Get("signature")
				query.Del("signature")
				mac := hmac.New(sha256.New, []byte("fixture-secret"))
				_, _ = io.WriteString(mac, query.Encode())
				if signature != hex.EncodeToString(mac.Sum(nil)) || request.Header.Get("X-MBX-APIKEY") != "fixture-key" || query.Get("recvWindow") != "10000" {
					t.Fatal("signed read parameters changed")
				}
				stamp, _ := strconv.ParseInt(query.Get("timestamp"), 10, 64)
				if delta := time.UnixMilli(stamp).Sub(now.Add(2 * time.Minute)); delta < -time.Second || delta > time.Second {
					t.Fatal("provider clock offset not used")
				}
				if request.URL.Path == "/sapi/v1/account/apiRestrictions" {
					return fixtureResponse(request, 200, `{"enableReading":true}`), nil
				}
				if request.URL.Path != historyPath || query.Get("startTime") == "" || query.Get("endTime") == "" || query.Get("limit") == "" || query.Get("uid") != "" {
					t.Fatal("Pay transaction parameters changed")
				}
				body := fmt.Sprintf(`{"code":"000000","success":true,"data":[{"transactionId":"incoming","amount":"1.00000","currency":"USDT","transactionTime":%d,"orderType":"PAY","receiverInfo":{"binanceId":"12345"}},{"transactionId":"other-uid","amount":"2.00000","currency":"USDT","transactionTime":%d,"orderType":"PAY","receiverInfo":{"binanceId":"98765"}}]}`, now.Add(-time.Minute).UnixMilli(), now.Add(-time.Minute).UnixMilli())
				return fixtureResponse(request, 200, body), nil
			}))
			switch operation {
			case "probe":
				if err := client.Probe(context.Background(), "fixture-key", "fixture-secret"); err != nil {
					t.Fatal(err)
				}
			case "history":
				rows, err := client.History(context.Background(), "fixture-key", "fixture-secret", "12345", now.Add(-time.Hour), now)
				if err != nil || len(rows) != 1 || rows[0].TransactionID != "incoming" || rows[0].AmountUnits != 100000 {
					t.Fatal("history UID or money filter changed")
				}
			case "bills":
				page, err := client.Bills(context.Background(), "fixture-key", "fixture-secret", "12345", now.Add(-time.Hour), now)
				if err != nil || page == nil || page.Total != 2 {
					t.Fatal("read-only bills compatibility changed")
				}
			}
			want := "/api/v3/time," + historyPath
			if operation == "probe" {
				want = "/api/v3/time,/sapi/v1/account/apiRestrictions," + historyPath
			}
			if strings.Join(paths, ",") != want {
				t.Fatal("unexpected extra endpoint or retry")
			}
		})
	}
}

func TestBinanceRequestErrorPayBusinessRejectionAndPermissionStage(t *testing.T) {
	for _, permission := range []bool{false, true} {
		client := NewClientWithTransport(binanceFixtureTransport(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Path {
			case "/api/v3/time":
				return fixtureResponse(request, 200, fmt.Sprintf(`{"serverTime":%d}`, time.Now().UnixMilli())), nil
			case "/sapi/v1/account/apiRestrictions":
				return fixtureResponse(request, 200, fmt.Sprintf(`{"enableReading":%t}`, permission)), nil
			default:
				return fixtureResponse(request, 200, `{"code":"400001","success":false,"message":"private-message fixture-secret"}`), nil
			}
		}))
		err := client.Probe(context.Background(), "fixture-key", "fixture-secret")
		if permission {
			requireRequestFailure(t, err, "pay_history", "pay_rejected", 200, "400001")
		} else {
			requireRequestFailure(t, err, "permission_check", "reading_disabled", 0, "")
		}
	}
}

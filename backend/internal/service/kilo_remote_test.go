package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

type kiloRemoteTestTransport func(*http.Request) (*http.Response, error)

func (f kiloRemoteTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func kiloRemoteTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func kiloRemoteTestClient(t *testing.T, handler kiloRemoteTestTransport) *kiloRemoteClient {
	t.Helper()
	return &kiloRemoteClient{getClient: func(opts httpclient.Options) (*http.Client, error) {
		if opts.ProxyURL != "socks5://selected-proxy.invalid:1080" || opts.Timeout != 20*time.Second ||
			opts.InsecureSkipVerify || !opts.ValidateResolvedIP {
			t.Fatalf("unexpected HTTP boundary options: %#v", opts)
		}
		return &http.Client{Transport: handler}, nil
	}}
}

func TestKiloRemoteConnectionScopeAndIdentity(t *testing.T) {
	const org = "aa52a13e-1af9-4b3c-977b-66eae64d4b8d"
	for _, organizationID := range []string{"", org} {
		t.Run(organizationID, func(t *testing.T) {
			client := kiloRemoteTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "app.kilo.ai" ||
					req.URL.Path != "/api/trpc/openAiChatGpt.status" || req.Header.Get("Authorization") != "Bearer fake-kilo-key" {
					t.Fatal("wrong connection endpoint or authorization")
				}
				var input map[string]json.RawMessage
				if err := json.Unmarshal([]byte(req.URL.Query().Get("input")), &input); err != nil {
					t.Fatal(err)
				}
				if _, wrapped := input["json"]; wrapped {
					t.Fatal("JSON transformer wrapper would silently select the personal owner")
				}
				var gotOrg string
				if field := input["organizationId"]; len(field) > 0 {
					if err := json.Unmarshal(field, &gotOrg); err != nil {
						t.Fatal(err)
					}
				}
				if gotOrg != organizationID || len(input) > 1 {
					t.Fatalf("wrong account context: %s", req.URL.Query().Get("input"))
				}
				return kiloRemoteTestResponse(200, `{"result":{"data":{"state":"connected","email":" source@example.invalid ","subject":"oauth-subject","connectedAt":"2026-11-12T12:00:00Z","usageLimit":{"reachedAt":"2026-11-12T13:00:00Z","resetsAt":null,"code":null}}}}`), nil
			})
			got, err := client.Connection(context.Background(), "Bearer fake-kilo-key", organizationID, "socks5://selected-proxy.invalid:1080")
			if err != nil {
				t.Fatal(err)
			}
			if !got.Connected || got.State != "connected" || got.Email != "source@example.invalid" || got.AccountID != "" || got.PlanType != "" {
				t.Fatalf("unexpected connection identity: %#v", got)
			}
		})
	}
}

func TestKiloRemoteConnectionUsageLimit(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(6 * time.Hour).Format(time.RFC3339Nano)
	past := now.Add(-time.Hour).Format(time.RFC3339Nano)
	reached := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	cases := []struct {
		name      string
		limit     any
		wantLimit bool
		wantReset string
		wantError bool
	}{
		{name: "absent"},
		{name: "unknown-reset", limit: map[string]any{"reachedAt": reached, "resetsAt": nil, "code": "usage_limit_reached"}, wantLimit: true},
		{name: "future-reset", limit: map[string]any{"reachedAt": reached, "resetsAt": future, "code": nil}, wantLimit: true, wantReset: future},
		{name: "expired-reset", limit: map[string]any{"reachedAt": reached, "resetsAt": past, "code": nil}, wantReset: past},
		{name: "malformed-reset", limit: map[string]any{"reachedAt": reached, "resetsAt": "not-a-time", "code": nil}, wantError: true},
		{name: "empty-reset", limit: map[string]any{"reachedAt": reached, "resetsAt": "", "code": nil}, wantError: true},
		{name: "malformed-reached", limit: map[string]any{"reachedAt": "not-a-time", "resetsAt": future, "code": nil}, wantError: true},
		{name: "missing-reached", limit: map[string]any{"resetsAt": future}, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			connection := map[string]any{"state": "connected", "subject": "oauth-subject", "email": "source@example.invalid"}
			if tc.limit != nil {
				connection["usageLimit"] = tc.limit
			}
			body, err := json.Marshal(map[string]any{"result": map[string]any{"data": connection}})
			if err != nil {
				t.Fatal(err)
			}
			client := kiloRemoteTestClient(t, func(*http.Request) (*http.Response, error) {
				return kiloRemoteTestResponse(200, string(body)), nil
			})
			got, err := client.Connection(context.Background(), "fake-kilo-key", "", "socks5://selected-proxy.invalid:1080")
			if tc.wantError {
				if err == nil || got != nil || infraerrors.Reason(err) != "KILO_REMOTE_RESPONSE_INVALID" {
					t.Fatalf("invalid limit did not fail closed: result=%#v error=%v", got, err)
				}
				return
			}
			if err != nil || got == nil || !got.Connected || got.UsageLimitReached != tc.wantLimit || got.AccountID != "" || got.PlanType != "" {
				t.Fatalf("unexpected quota state: result=%#v error=%v", got, err)
			}
			if tc.wantReset == "" {
				if got.UsageResetsAt != nil {
					t.Fatal("invented a reset time")
				}
			} else if got.UsageResetsAt == nil || got.UsageResetsAt.Format(time.RFC3339Nano) != tc.wantReset {
				t.Fatalf("real reset time was lost: result=%#v", got)
			}
		})
	}
}

func TestKiloRemoteConnectionFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"no-envelope", `{"state":"connected","email":"source@example.invalid"}`, 200},
		{"null-data", `{"result":{"data":null}}`, 200},
		{"wrong-transformer", `{"result":{"data":{"json":{"state":"connected","subject":"s","email":"source@example.invalid"}}}}`, 200},
		{"unknown-state", `{"result":{"data":{"state":"ready","subject":"s"}}}`, 200},
		{"wrong-state-type", `{"result":{"data":{"state":true,"subject":"s"}}}`, 200},
		{"missing-subject", `{"result":{"data":{"state":"connected","email":"source@example.invalid"}}}`, 200},
		{"null-email", `{"result":{"data":{"state":"connected","subject":"s","email":42}}}`, 200},
		{"bad-usage-limit", `{"result":{"data":{"state":"connected","subject":"s","usageLimit":false}}}`, 200},
		{"batched", `[{"result":{"data":{"state":"connected","subject":"s"}}}]`, 200},
		{"html", `<html>fake-upstream-secret</html>`, 200},
		{"body-limit", strings.Repeat(" ", kiloRemoteMaxBody+1), 200},
		{"unauthorized", `{"token":"fake-upstream-secret"}`, 401},
		{"forbidden", `{"message":"fake-upstream-secret"}`, 403},
		{"rate-limit", `{"message":"fake-upstream-secret"}`, 429},
		{"trpc-auth", `{"error":{"message":"fake-upstream-secret","data":{"code":"UNAUTHORIZED","httpStatus":401}}}`, 200},
		{"trpc-conflict", `{"result":{"data":{"state":"connected","subject":"s"}},"error":{"message":"fake-upstream-secret"}}`, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := kiloRemoteTestClient(t, func(*http.Request) (*http.Response, error) {
				return kiloRemoteTestResponse(tc.status, tc.body), nil
			})
			got, err := client.Connection(context.Background(), "fake-kilo-key", "", "socks5://selected-proxy.invalid:1080")
			if err == nil || got != nil {
				t.Fatalf("unverified response accepted: result=%#v error=%v", got, err)
			}
			if strings.Contains(err.Error(), "fake-upstream-secret") || strings.Contains(err.Error(), "fake-kilo-key") || infraerrors.Reason(err) == "" {
				t.Fatal("remote error leaked a credential/body or was not actionable")
			}
		})
	}
	for _, state := range []string{"disconnected", "error"} {
		t.Run(state, func(t *testing.T) {
			client := kiloRemoteTestClient(t, func(*http.Request) (*http.Response, error) {
				return kiloRemoteTestResponse(200, `{"result":{"data":{"state":"`+state+`","subject":"s"}}}`), nil
			})
			got, err := client.Connection(context.Background(), "fake-kilo-key", "", "socks5://selected-proxy.invalid:1080")
			if err != nil || got == nil || got.Connected || got.State != state {
				t.Fatalf("nonconnected state was lost: result=%#v error=%v", got, err)
			}
		})
	}
	client := kiloRemoteTestClient(t, func(*http.Request) (*http.Response, error) {
		return kiloRemoteTestResponse(200, `{"result":{"data":{"state":"connected","subject":"s"}}}`), nil
	})
	got, err := client.Connection(context.Background(), "fake-kilo-key", "", "socks5://selected-proxy.invalid:1080")
	if err != nil || got == nil || got.Email != "" || got.AccountID != "" {
		t.Fatalf("missing source identity was fabricated: result=%#v error=%v", got, err)
	}
}

func TestKiloRemoteForbiddenIdentityBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, body string
		status               int
		identity             bool
	}{
		{"profile identity", "profile", `{"error":{"code":"account_banned"}}`, 403, true},
		{"profile canonical identity", "profile", `{"code":"KILO_IDENTITY_FORBIDDEN"}`, 403, true},
		{"profile permission", "profile", `{"error":{"code":"insufficient_permissions"}}`, 403, false},
		{"profile WAF", "profile", `<html>Access denied</html>`, 403, false},
		{"profile unknown JSON", "profile", `{"message":"Forbidden"}`, 403, false},
		{"non403 identity code", "profile", `{"error":{"code":"account_banned"}}`, 401, false},
		{"connection organization permission", "connection", `{"error":{"data":{"code":"FORBIDDEN","httpStatus":403}}}`, 403, false},
		{"connection HTTP200 error", "connection", `{"error":{"data":{"code":"FORBIDDEN","httpStatus":403}}}`, 200, false},
		{"models upstream account", "models", `{"error":{"code":"account_banned"}}`, 403, false},
		{"device denial", "device", `{"error":{"code":"account_banned"}}`, 403, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := kiloRemoteTestClient(t, func(*http.Request) (*http.Response, error) {
				return kiloRemoteTestResponse(tc.status, tc.body), nil
			})
			const proxy = "socks5://selected-proxy.invalid:1080"
			var err error
			switch tc.endpoint {
			case "profile":
				_, err = client.Profile(context.Background(), "synthetic-kilo-key", proxy)
			case "connection":
				_, err = client.Connection(context.Background(), "synthetic-kilo-key", "", proxy)
			case "models":
				_, err = client.Models(context.Background(), "synthetic-kilo-key", "", proxy)
			case "device":
				_, err = client.StartDevice(context.Background(), proxy)
			}
			if err == nil || (infraerrors.Reason(err) == KiloIdentityForbiddenReason) != tc.identity {
				t.Fatalf("wrong identity attribution: identity=%t reason=%q error=%v", tc.identity, infraerrors.Reason(err), err)
			}
			if strings.Contains(err.Error(), "synthetic-kilo-key") || strings.Contains(err.Error(), tc.body) {
				t.Fatal("remote failure exposed the request credential or raw body")
			}
		})
	}
}

func TestKiloRemoteProfileAndDeviceFlow(t *testing.T) {
	var calls int
	client := kiloRemoteTestClient(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Scheme != "https" || req.URL.Host != "api.kilo.ai" {
			t.Fatal("device/profile request left the official API host")
		}
		switch calls {
		case 1:
			if req.Method != http.MethodGet || req.URL.Path != "/api/profile" || req.Header.Get("Authorization") != "Bearer fake-kilo-key" {
				t.Fatal("wrong profile request")
			}
			return kiloRemoteTestResponse(200, `{"user":{"id":"kilo-user","email":"kilo@example.invalid"},"organizations":[{"id":"org-id","name":"Organization"}],"hasPersonalAccount":true}`), nil
		case 2:
			body, err := io.ReadAll(req.Body)
			if err != nil || string(body) != "{}" || req.Method != http.MethodPost || req.URL.Path != "/api/device-auth/codes" || req.Header.Get("Authorization") != "" {
				t.Fatal("wrong device authorization start")
			}
			return kiloRemoteTestResponse(200, `{"code":"private-device-code","user_code":"USER-CODE","verificationUrl":"https://app.kilo.ai/device-auth?code=USER-CODE","expiresIn":900}`), nil
		case 3, 4, 5, 6:
			if req.Method != http.MethodGet || req.URL.Path != "/api/device-auth/codes/private-device-code" || req.Header.Get("Authorization") != "" {
				t.Fatal("wrong device authorization poll")
			}
			responses := map[int]struct {
				status int
				body   string
			}{
				3: {202, `{"status":"pending"}`},
				4: {200, `{"status":"approved","token":"fake-device-token","userId":"kilo-user","userEmail":"kilo@example.invalid"}`},
				5: {403, `{"status":"denied"}`},
				6: {410, `{"status":"expired"}`},
			}
			response := responses[calls]
			return kiloRemoteTestResponse(response.status, response.body), nil
		default:
			t.Fatal("unexpected request")
			return nil, errors.New("unexpected request")
		}
	})
	proxyURL := "socks5://selected-proxy.invalid:1080"
	profile, err := client.Profile(context.Background(), "fake-kilo-key", proxyURL)
	if err != nil || profile == nil || profile.ID != "kilo-user" || profile.Email != "kilo@example.invalid" || !profile.HasPersonalAccount || len(profile.Organizations) != 1 {
		t.Fatalf("unexpected profile: result=%#v error=%v", profile, err)
	}
	device, err := client.StartDevice(context.Background(), proxyURL)
	if err != nil || device == nil || device.Status != "pending" || device.ExpiresIn != 15*time.Minute || device.UserCode != "USER-CODE" || device.Code != "private-device-code" {
		t.Fatalf("unexpected device start: result=%#v error=%v", device, err)
	}
	for _, state := range []string{"pending", "approved", "denied", "expired"} {
		device, err = client.PollDevice(context.Background(), "private-device-code", proxyURL)
		if err != nil || device == nil || device.Status != state {
			t.Fatalf("unexpected device poll: result=%#v error=%v", device, err)
		}
		if state == "approved" && (device.Token != "fake-device-token" || device.Email != "kilo@example.invalid") {
			t.Fatal("approved credentials missing")
		}
		if state != "approved" && device.Token != "" {
			t.Fatal("unapproved device exposed a token")
		}
	}
}

func TestKiloRemoteRejectsInvalidProfileAndDevice(t *testing.T) {
	for name, body := range map[string]string{
		"missing-user":           `{}`,
		"missing-personal-state": `{"user":{"id":"u","email":"e@example.invalid"}}`,
		"bad-organizations":      `{"user":{"id":"u","email":"e@example.invalid"},"hasPersonalAccount":true,"organizations":[{}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			client := kiloRemoteTestClient(t, func(*http.Request) (*http.Response, error) { return kiloRemoteTestResponse(200, body), nil })
			if got, err := client.Profile(context.Background(), "fake-key", "socks5://selected-proxy.invalid:1080"); err == nil || got != nil {
				t.Fatal("invalid profile accepted")
			}
		})
	}
	for name, verificationURL := range map[string]string{
		"http":       "http://app.kilo.ai/device-auth",
		"unofficial": "https://app.kilo.ai.evil.invalid/device-auth",
		"userinfo":   "https://fake-user@app.kilo.ai/device-auth",
		"port":       "https://app.kilo.ai:8443/device-auth",
	} {
		t.Run(name, func(t *testing.T) {
			client := kiloRemoteTestClient(t, func(*http.Request) (*http.Response, error) {
				body, _ := json.Marshal(map[string]any{"code": "private-code", "user_code": "USER", "verificationUrl": verificationURL, "expiresIn": 900})
				return kiloRemoteTestResponse(200, string(body)), nil
			})
			if got, err := client.StartDevice(context.Background(), "socks5://selected-proxy.invalid:1080"); err == nil || got != nil {
				t.Fatal("unsafe authorization URL accepted")
			}
		})
	}
	for _, response := range []struct {
		status int
		body   string
	}{
		{200, `{"status":"pending"}`},
		{200, `{"status":"approved","token":"fake-token"}`},
		{200, `{"status":"approved","token":"bad\ntoken","userEmail":"kilo@example.invalid"}`},
		{202, `{"status":"approved","token":"fake-token","userEmail":"kilo@example.invalid"}`},
	} {
		client := kiloRemoteTestClient(t, func(*http.Request) (*http.Response, error) {
			return kiloRemoteTestResponse(response.status, response.body), nil
		})
		if got, err := client.PollDevice(context.Background(), "code", "socks5://selected-proxy.invalid:1080"); err == nil || got != nil {
			t.Fatal("unverified device authorization accepted")
		}
	}
}

func TestKiloRemoteTransportIsolation(t *testing.T) {
	var requests atomic.Int32
	base := &http.Client{Transport: kiloRemoteTestTransport(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		resp := kiloRemoteTestResponse(http.StatusFound, "fake-upstream-secret")
		resp.Header.Set("Location", "https://untrusted.invalid/collect")
		return resp, nil
	})}
	client := &kiloRemoteClient{getClient: func(httpclient.Options) (*http.Client, error) { return base, nil }}
	got, err := client.Connection(context.Background(), "fake-key", "", "")
	if err == nil || got != nil || requests.Load() != 1 || base.CheckRedirect != nil || base.Timeout != 0 {
		t.Fatal("redirect was followed or shared client was mutated")
	}
	if strings.Contains(err.Error(), "fake-upstream-secret") {
		t.Fatal("redirect body leaked")
	}
	for _, transportErr := range []error{
		errors.New("proxy socks5://fake-user:fake-password@proxy.invalid refused fake-key"),
		tls.RecordHeaderError{Msg: "fake-upstream-secret"},
	} {
		client := &kiloRemoteClient{getClient: func(httpclient.Options) (*http.Client, error) {
			return &http.Client{Transport: kiloRemoteTestTransport(func(*http.Request) (*http.Response, error) { return nil, transportErr })}, nil
		}}
		_, err := client.Profile(context.Background(), "fake-key", "")
		if err == nil || strings.Contains(err.Error(), "fake-") {
			t.Fatal("transport failure leaked credentials")
		}
	}
	client = &kiloRemoteClient{getClient: func(httpclient.Options) (*http.Client, error) {
		return nil, errors.New("fake-proxy-password")
	}}
	if _, err := client.Profile(context.Background(), "fake-key", "broken://proxy"); infraerrors.Reason(err) != "KILO_PROXY_INVALID" || strings.Contains(err.Error(), "fake-proxy-password") {
		t.Fatal("invalid proxy did not fail closed")
	}
	client = &kiloRemoteClient{getClient: func(httpclient.Options) (*http.Client, error) {
		t.Fatal("malformed credential/code reached HTTP boundary")
		return nil, errors.New("unexpected request")
	}}
	if _, err := client.Profile(context.Background(), "fake\r\nkey", ""); err == nil {
		t.Fatal("invalid bearer accepted")
	}
	if _, err := client.PollDevice(context.Background(), "../profile", ""); err == nil {
		t.Fatal("invalid device path accepted")
	}
}

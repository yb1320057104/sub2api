package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestKiloRemoteAuthProvidersUsesOriginalKey(t *testing.T) {
	client := kiloRemoteTestClient(t, func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "https", req.URL.Scheme)
		require.Equal(t, "api.kilo.ai", req.URL.Host)
		require.Equal(t, "/api/trpc/user.getAuthProviders", req.URL.Path)
		require.Empty(t, req.URL.RawQuery)
		require.Equal(t, "Bearer original-kilo-key", req.Header.Get("Authorization"))
		return kiloRemoteTestResponse(200, `{"result":{"data":{"success":true,"providers":[{"provider":"openai","email":"same@example.invalid","avatar_url":null},{"provider":"email","email":"same@example.invalid"},{"provider":"github","email":null}]}}}`), nil
	})
	providers, err := client.AuthProviders(context.Background(), "Bearer original-kilo-key", "socks5://selected-proxy.invalid:1080")
	require.NoError(t, err)
	require.Equal(t, []KiloAuthProvider{{Provider: "openai", Email: "same@example.invalid"}, {Provider: "email", Email: "same@example.invalid"}, {Provider: "github"}}, providers)
	require.Equal(t, KiloIdentityModeIndependent, ClassifyKiloIdentityProviders(providers))
}

func TestKiloRemoteAuthProvidersRejectsUnsupportedShape(t *testing.T) {
	for _, test := range []struct {
		name, body, reason string
		status             int
	}{
		{"empty-body", `{}`, "KILO_REMOTE_RESPONSE_INVALID", 200},
		{"null-data", `{"result":{"data":null}}`, "KILO_REMOTE_RESPONSE_INVALID", 200},
		{"null-providers", `{"result":{"data":{"success":true,"providers":null}}}`, "KILO_REMOTE_RESPONSE_INVALID", 200},
		{"missing-success", `{"result":{"data":{"providers":[]}}}`, "KILO_REMOTE_RESPONSE_INVALID", 200},
		{"false-success", `{"result":{"data":{"success":false,"providers":[]}}}`, "KILO_REMOTE_RESPONSE_INVALID", 200},
		{"wrapped", `{"result":{"data":{"json":{"success":true,"providers":[]}}}}`, "KILO_REMOTE_RESPONSE_INVALID", 200},
		{"batched", `[{"result":{"data":{"success":true,"providers":[]}}}]`, "KILO_REMOTE_RESPONSE_INVALID", 200},
		{"missing-provider", `{"result":{"data":{"success":true,"providers":[{"email":"private@example.invalid"}]}}}`, "KILO_REMOTE_RESPONSE_INVALID", 200},
		{"missing-email", `{"result":{"data":{"success":true,"providers":[{"provider":"openai"}]}}}`, "KILO_REMOTE_RESPONSE_INVALID", 200},
		{"numeric-email", `{"result":{"data":{"success":true,"providers":[{"provider":"openai","email":1}]}}}`, "KILO_REMOTE_RESPONSE_INVALID", 200},
		{"bad-provider", `{"result":{"data":{"success":true,"providers":[{"provider":" openai","email":null}]}}}`, "KILO_REMOTE_RESPONSE_INVALID", 200},
		{"http-auth", `{"message":"private@example.invalid original-kilo-key"}`, "KILO_AUTH_INVALID", 401},
		{"http-forbidden", `{"error":{"code":"account_banned"}}`, "KILO_ACCESS_DENIED", 403},
		{"trpc-auth", `{"error":{"message":"private@example.invalid","data":{"code":"UNAUTHORIZED","httpStatus":401}}}`, "KILO_AUTH_INVALID", 200},
		{"trpc-forbidden", `{"error":{"data":{"code":"FORBIDDEN","httpStatus":403}}}`, "KILO_ACCESS_DENIED", 200},
		{"conflicting-error", `{"result":{"data":{"success":true,"providers":[]}},"error":{"message":"private@example.invalid"}}`, "KILO_REMOTE_RESPONSE_INVALID", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := kiloRemoteTestClient(t, func(*http.Request) (*http.Response, error) {
				return kiloRemoteTestResponse(test.status, test.body), nil
			})
			providers, err := client.AuthProviders(context.Background(), "original-kilo-key", "socks5://selected-proxy.invalid:1080")
			require.Nil(t, providers)
			require.Equal(t, test.reason, infraerrors.Reason(err))
			require.NotContains(t, err.Error(), "private@example.invalid")
			require.NotContains(t, err.Error(), "original-kilo-key")
		})
	}
}

func TestKiloRemoteUnlinkOpenAIGuardsAndConfirmsIndependentLogin(t *testing.T) {
	for _, test := range []struct {
		name          string
		before, after []KiloAuthProvider
		postBody      string
		postStatus    int
		wantCalls     int
		wantReason    string
	}{
		{name: "only-openai", before: []KiloAuthProvider{{Provider: "openai"}}, wantCalls: 1, wantReason: "KILO_IDENTITY_COUPLED"},
		{name: "passkey-not-independent", before: []KiloAuthProvider{{Provider: "openai"}, {Provider: "passkey"}}, wantCalls: 1, wantReason: "KILO_IDENTITY_COUPLED"},
		{name: "fake-login-not-independent", before: []KiloAuthProvider{{Provider: "openai"}, {Provider: "fake-login"}}, wantCalls: 1, wantReason: "KILO_IDENTITY_COUPLED"},
		{name: "unknown-not-independent", before: []KiloAuthProvider{{Provider: "openai"}, {Provider: "future-provider"}}, wantCalls: 1, wantReason: "KILO_IDENTITY_COUPLED"},
		{name: "already-absent", before: []KiloAuthProvider{{Provider: "email", Email: "same@example.invalid"}}, wantCalls: 1},
		{name: "absent-empty", before: []KiloAuthProvider{}, wantCalls: 1, wantReason: "KILO_IDENTITY_COUPLED"},
		{name: "same-email-independent", before: []KiloAuthProvider{{Provider: "openai", Email: "same@example.invalid"}, {Provider: "email", Email: "same@example.invalid"}}, after: []KiloAuthProvider{{Provider: "email", Email: "SAME@example.invalid"}}, wantCalls: 3},
		{name: "openai-still-present", before: []KiloAuthProvider{{Provider: "openai"}, {Provider: "google"}}, after: []KiloAuthProvider{{Provider: "openai"}, {Provider: "google"}}, wantCalls: 3, wantReason: "KILO_IDENTITY_UNLINK_UNCONFIRMED"},
		{name: "independent-lost", before: []KiloAuthProvider{{Provider: "openai"}, {Provider: "google"}}, after: []KiloAuthProvider{{Provider: "passkey"}}, wantCalls: 3, wantReason: "KILO_IDENTITY_UNLINK_UNCONFIRMED"},
		{name: "independent-replaced", before: []KiloAuthProvider{{Provider: "openai"}, {Provider: "email", Email: "before@example.invalid"}}, after: []KiloAuthProvider{{Provider: "email", Email: "different@example.invalid"}}, wantCalls: 3, wantReason: "KILO_IDENTITY_UNLINK_UNCONFIRMED"},
		{name: "post-false-success", before: []KiloAuthProvider{{Provider: "openai"}, {Provider: "github"}}, postBody: `{"result":{"data":{"success":false}}}`, wantCalls: 2, wantReason: "KILO_REMOTE_RESPONSE_INVALID"},
		{name: "post-missing-success", before: []KiloAuthProvider{{Provider: "openai"}, {Provider: "github"}}, postBody: `{"result":{"data":{}}}`, wantCalls: 2, wantReason: "KILO_REMOTE_RESPONSE_INVALID"},
		{name: "post-forbidden", before: []KiloAuthProvider{{Provider: "openai"}, {Provider: "github"}}, postStatus: 403, postBody: `{"error":{"code":"account_banned"}}`, wantCalls: 2, wantReason: "KILO_ACCESS_DENIED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := kiloRemoteTestClient(t, func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "api.kilo.ai", req.URL.Host)
				require.Equal(t, "Bearer original-kilo-key", req.Header.Get("Authorization"))
				if calls == 2 {
					require.Equal(t, http.MethodPost, req.Method)
					require.Equal(t, "/api/trpc/user.unlinkAuthProvider", req.URL.Path)
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.JSONEq(t, `{"provider":"openai"}`, string(body))
					status := test.postStatus
					if status == 0 {
						status = 200
					}
					response := test.postBody
					if response == "" {
						response = `{"result":{"data":{"success":true}}}`
					}
					return kiloRemoteTestResponse(status, response), nil
				}
				require.LessOrEqual(t, calls, 3)
				require.Equal(t, http.MethodGet, req.Method)
				require.Equal(t, "/api/trpc/user.getAuthProviders", req.URL.Path)
				providers := test.before
				if calls == 3 {
					providers = test.after
				}
				body, err := json.Marshal(map[string]any{"result": map[string]any{"data": map[string]any{"success": true, "providers": providers}}})
				require.NoError(t, err)
				return kiloRemoteTestResponse(200, string(body)), nil
			})
			err := client.UnlinkOpenAI(context.Background(), "original-kilo-key", "socks5://selected-proxy.invalid:1080")
			require.Equal(t, test.wantCalls, calls)
			if test.wantReason == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Equal(t, test.wantReason, infraerrors.Reason(err))
				require.False(t, strings.Contains(err.Error(), "example.invalid"))
			}
		})
	}
}

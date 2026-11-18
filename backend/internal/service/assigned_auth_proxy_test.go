package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type assignedProxyFixture struct {
	accounts map[int64]*Account
	proxies  map[int64]*Proxy
	writes   int
	writeErr error
}

func (f *assignedProxyFixture) store() assignedAuthProxyStore {
	return assignedAuthProxyStore{
		account: func(_ context.Context, id int64) (*Account, error) { return f.accounts[id], nil },
		proxy:   func(_ context.Context, id int64) (*Proxy, error) { return f.proxies[id], nil },
		pin: func(_ context.Context, id int64, extra map[string]any) error {
			f.writes++
			if f.writeErr != nil {
				return f.writeErr
			}
			if f.accounts[id].Extra == nil {
				f.accounts[id].Extra = map[string]any{}
			}
			for key, value := range extra {
				f.accounts[id].Extra[key] = value
			}
			return nil
		},
	}
}

func TestAssignedAuthProxyPersistsBindingAndNeverRotates(t *testing.T) {
	ctx := context.Background()
	low := &Proxy{ID: 7, Protocol: "http", Host: "203.0.113.7", Port: 8080, Status: StatusActive}
	high := &Proxy{ID: 9, Protocol: "http", Host: "203.0.113.9", Port: 8080, Status: StatusActive}
	account := &Account{ID: 106, ProxyBindings: []AccountProxyBinding{{ProxyID: 9, Proxy: high}, {ProxyID: 7, Proxy: low}}}
	fixture := &assignedProxyFixture{accounts: map[int64]*Account{106: account}, proxies: map[int64]*Proxy{7: low, 9: high}}
	route, err := resolveAssignedAuthProxy(ctx, account, fixture.store())
	require.NoError(t, err)
	require.Equal(t, int64(7), route.ID)
	require.Equal(t, 7, ParseExtraInt(account.Extra[assignedAuthProxyExtraKey]))
	require.Equal(t, 1, fixture.writes)
	account.ProxyBindings[0], account.ProxyBindings[1] = account.ProxyBindings[1], account.ProxyBindings[0]
	route, err = resolveAssignedAuthProxy(ctx, account, fixture.store())
	require.NoError(t, err)
	require.Equal(t, int64(7), route.ID)
	require.Equal(t, 1, fixture.writes)
	low.Status = StatusDisabled
	_, err = resolveAssignedAuthProxy(ctx, account, fixture.store())
	require.ErrorIs(t, err, errAssignedAuthProxyUnavailable)
	account.ProxyBindings = []AccountProxyBinding{{ProxyID: 9, Proxy: high}}
	_, err = resolveAssignedAuthProxy(ctx, account, fixture.store())
	require.ErrorIs(t, err, errAssignedAuthProxyUnavailable)
}

func TestAssignedAuthProxyUnassignedUsesDirectWithoutMutation(t *testing.T) {
	account := &Account{ID: 106}
	fixture := &assignedProxyFixture{accounts: map[int64]*Account{106: account}}
	route, err := resolveAssignedAuthProxy(context.Background(), account, fixture.store())
	require.NoError(t, err)
	require.Zero(t, route.ID)
	require.Empty(t, route.URL)
	require.Zero(t, fixture.writes)
	require.Equal(t, "direct", authorizationProxyEndpoint(route.URL))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = authorizationProxyDial(cancelled, route.URL, "127.0.0.1:443")
	require.ErrorIs(t, err, context.Canceled, "the restricted bridge must route an unassigned account directly")
	account.Extra = map[string]any{assignedAuthProxyExtraKey: int64(10)}
	_, err = resolveAssignedAuthProxy(context.Background(), account, fixture.store())
	require.ErrorIs(t, err, errAssignedAuthProxyUnavailable, "a removed fixed binding must not switch to direct")
}

func TestAssignedAuthProxyRequiresOwnedUsableExit(t *testing.T) {
	ctx := context.Background()
	proxy := &Proxy{ID: 10, Protocol: "http", Host: "203.0.113.10", Port: 8080, Status: StatusActive}
	for _, name := range []string{"missing", "inactive", "expired", "persist_failed", "unpaired_gateway", "paired_gateway", "gateway_fixed_missing", "explicit_primary"} {
		t.Run(name, func(t *testing.T) {
			p := *proxy
			account := &Account{ID: 106, Extra: map[string]any{}}
			gateway := &Account{ID: 118, Extra: map[string]any{KiloManagedExtraKey: true, "kilo_prepared_id": int64(1), KiloLoginProxyExtraKey: int64(10)}}
			fixture := &assignedProxyFixture{accounts: map[int64]*Account{106: account, 118: gateway}, proxies: map[int64]*Proxy{10: &p}}
			want := errAssignedAuthProxyRequired
			switch name {
			case "missing":
				account.ProxyBindings = []AccountProxyBinding{{ProxyID: 10}}
			case "inactive", "expired", "explicit_primary":
				id := int64(10)
				account.ProxyID = &id
				if name == "inactive" {
					p.Status = StatusDisabled
				}
				if name == "expired" {
					expired := time.Now().Add(-time.Hour)
					p.ExpiresAt = &expired
				}
				want = errAssignedAuthProxyUnavailable
			case "persist_failed":
				account.ProxyBindings = []AccountProxyBinding{{ProxyID: 10, Proxy: &p}}
				fixture.writeErr = errors.New("save failed")
				want = errAssignedAuthProxyPersistence
			case "unpaired_gateway", "paired_gateway", "gateway_fixed_missing":
				account.Extra["kilo_source_prepared_id"] = int64(1)
				account.Extra["kilo_source_gateway_id"] = int64(118)
				want = errAssignedAuthGatewayInvalid
				if name == "unpaired_gateway" {
					gateway.Extra["kilo_prepared_id"] = int64(2)
				}
				if name == "gateway_fixed_missing" {
					delete(gateway.Extra, KiloLoginProxyExtraKey)
				}
			}
			route, err := resolveAssignedAuthProxy(ctx, account, fixture.store())
			if name == "paired_gateway" || name == "explicit_primary" {
				require.NoError(t, err)
				require.Equal(t, int64(10), route.ID)
			} else {
				require.ErrorIs(t, err, want)
				require.Nil(t, route)
			}
		})
	}
}

func TestAuthorizationRecoveryUsesCurrentCredentialGeneration(t *testing.T) {
	for _, name := range []string{"current", "changed_token", "refreshable", "projected", "missing_refresh", "excel", "disabled_code", "string_boolean"} {
		t.Run(name, func(t *testing.T) {
			account := &Account{ID: 106, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
				Credentials: map[string]any{"access_token": "rejected", "refresh_token": "refresh"},
				Extra:       map[string]any{OpenAIAuthRecoveryKey: map[string]any{"access_token_digest": OpenAIAccessTokenDigest("rejected"), "requires_relogin": true}}}
			cfg := &AuthorizationConfig{ReauthStatusCodes: []int{401}}
			state := account.Extra[OpenAIAuthRecoveryKey].(map[string]any)
			want := 401
			switch name {
			case "changed_token":
				account.Credentials["access_token"] = "fresh"
				want = 0
			case "refreshable":
				state["requires_relogin"] = false
				want = 0
			case "projected":
				delete(account.Credentials, "access_token")
				account.Credentials["_openai_access_token_digest"] = OpenAIAccessTokenDigest("rejected")
			case "missing_refresh":
				delete(account.Credentials, "refresh_token")
				state["requires_relogin"] = false
			case "excel":
				account.Credentials["client_id"] = openai.ExcelClientID
				want = 0
			case "disabled_code":
				cfg.ReauthStatusCodes = []int{403}
				want = 0
			case "string_boolean":
				state["requires_relogin"] = "true"
				want = 0
			}
			require.Equal(t, want, authorizationAccountErrorCode(account, cfg))
		})
	}
}

func TestOpenAIOAuthReauthAssignedProxyOverridesLegacyPools(t *testing.T) {
	for _, source := range []string{OpenAIOAuthReauthProxySourceAccount, OpenAIOAuthReauthProxySourceManagedProxy, OpenAIOAuthReauthProxySourceMihomo} {
		t.Run(source, func(t *testing.T) {
			svc, reader, repo, _, _, _ := newReauthTestService("acct-1")
			ctx := context.Background()
			cfg := reauthEmailConfig(42, "user@example.com", "encrypted:https://mail.example.com/code")
			cfg.ProxySource = source
			if source == OpenAIOAuthReauthProxySourceManagedProxy {
				id := int64(99)
				cfg.ProxyID = &id
			}
			require.NoError(t, repo.UpsertConfig(ctx, cfg))
			svc.acquireMihomoProxy = func(context.Context, string) (string, func(), error) {
				t.Fatal("independent proxy pool was used")
				return "", nil, nil
			}
			_, err := svc.CreateTask(ctx, 42)
			require.NoError(t, err)
			claim, err := svc.ClaimTask(ctx, "worker-a")
			require.NoError(t, err)
			require.NotNil(t, claim)
			require.Equal(t, reader.proxy.URL(), claim.ProxyURL)
		})
	}
}

func TestOpenAIOAuthReauthUnassignedAccountUsesDirectRoute(t *testing.T) {
	svc, reader, repo, _, _, _ := newReauthTestService("acct-1")
	reader.account.ProxyID = nil
	ctx := context.Background()
	require.NoError(t, repo.UpsertConfig(ctx, reauthEmailConfig(42, "user@example.com", "encrypted:https://mail.example.com/code")))
	_, err := svc.CreateTask(ctx, 42)
	require.NoError(t, err)
	claim, err := svc.ClaimTask(ctx, "worker-a")
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.Empty(t, claim.ProxyURL, "an unassigned ordinary account must retain its direct route")
	require.Equal(t, OpenAIOAuthReauthStatusRunning, repo.task.Status)
}

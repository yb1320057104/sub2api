package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// KiloAuthProvider contains only the fields needed to verify login independence.
// Provider emails are transient evidence and must not be copied into account extras.
type KiloAuthProvider struct {
	Provider string `json:"provider"`
	Email    string `json:"email"`
}

// KiloIdentityRemote is optional so existing KiloRemote implementations retain
// their contract. UnlinkOpenAI removes a login association, not just BYOK access.
type KiloIdentityRemote interface {
	AuthProviders(context.Context, string, string) ([]KiloAuthProvider, error)
	UnlinkOpenAI(context.Context, string, string) error
}

func (c *kiloRemoteClient) AuthProviders(ctx context.Context, key, proxyURL string) ([]KiloAuthProvider, error) {
	key, err := kiloRemoteCredential(key)
	if err != nil {
		return nil, err
	}
	status, body, err := c.request(ctx, http.MethodGet, kiloRemoteAPIBase+"/api/trpc/user.getAuthProviders", key, proxyURL, "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, kiloRemoteResponseError(status, body, false)
	}
	data, err := kiloIdentityResultData(body)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Success   *bool `json:"success"`
		Providers *[]struct {
			Provider *string         `json:"provider"`
			Email    json.RawMessage `json:"email"`
		} `json:"providers"`
	}
	if json.Unmarshal(data, &payload) != nil || payload.Success == nil || !*payload.Success || payload.Providers == nil {
		return nil, kiloRemoteShapeError()
	}
	providers := make([]KiloAuthProvider, 0, len(*payload.Providers))
	for _, provider := range *payload.Providers {
		if provider.Provider == nil || *provider.Provider == "" || strings.TrimSpace(*provider.Provider) != *provider.Provider || len(provider.Email) == 0 {
			return nil, kiloRemoteShapeError()
		}
		var email *string
		if json.Unmarshal(provider.Email, &email) != nil {
			return nil, kiloRemoteShapeError()
		}
		item := KiloAuthProvider{Provider: *provider.Provider}
		if email != nil {
			item.Email = strings.TrimSpace(*email)
		}
		providers = append(providers, item)
	}
	return providers, nil
}

// UnlinkOpenAI keeps at least one previously observed independent login method.
// The official last-provider guard does not count passkeys from a separate table.
func (c *kiloRemoteClient) UnlinkOpenAI(ctx context.Context, key, proxyURL string) error {
	key, err := kiloRemoteCredential(key)
	if err != nil {
		return err
	}
	before, err := c.AuthProviders(ctx, key, proxyURL)
	if err != nil {
		return err
	}
	hasOpenAI := false
	independent := make(map[KiloAuthProvider]struct{})
	for _, provider := range before {
		if provider.Provider == "openai" {
			hasOpenAI = true
		}
		if kiloIndependentAuthProvider(provider.Provider) {
			independent[kiloIdentityProviderKey(provider)] = struct{}{}
		}
	}
	if len(independent) == 0 {
		return infraerrors.Conflict("KILO_IDENTITY_COUPLED", "Kilo尚无已确认的独立登录方式，不能解除OpenAI登录关联")
	}
	if !hasOpenAI {
		return nil
	}
	status, body, err := c.request(ctx, http.MethodPost, kiloRemoteAPIBase+"/api/trpc/user.unlinkAuthProvider", key, proxyURL, `{"provider":"openai"}`)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return kiloRemoteResponseError(status, body, false)
	}
	data, err := kiloIdentityResultData(body)
	if err != nil {
		return err
	}
	var payload struct {
		Success *bool `json:"success"`
	}
	if json.Unmarshal(data, &payload) != nil || payload.Success == nil || !*payload.Success {
		return kiloRemoteShapeError()
	}
	after, err := c.AuthProviders(ctx, key, proxyURL)
	if err != nil {
		return err
	}
	retained := false
	for _, provider := range after {
		if provider.Provider == "openai" {
			return kiloIdentityUnlinkUnconfirmed()
		}
		if _, exists := independent[kiloIdentityProviderKey(provider)]; exists {
			retained = true
		}
	}
	if !retained {
		return kiloIdentityUnlinkUnconfirmed()
	}
	return nil
}

func kiloIdentityProviderKey(provider KiloAuthProvider) KiloAuthProvider {
	provider.Email = strings.ToLower(strings.TrimSpace(provider.Email))
	return provider
}

func kiloIdentityUnlinkUnconfirmed() error {
	return infraerrors.Conflict("KILO_IDENTITY_UNLINK_UNCONFIRMED", "尚未确认OpenAI登录关联已解除且原独立登录方式仍保留，请刷新身份状态")
}

// Both endpoints use the identity tRPC transformer. Reject batched, wrapped or
// unsuccessful envelopes rather than treating an unrecognized payload as empty.
func kiloIdentityResultData(body []byte) (json.RawMessage, error) {
	var envelope struct {
		Result *struct {
			Data json.RawMessage `json:"data"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, kiloRemoteShapeError()
	}
	if len(envelope.Error) != 0 && strings.TrimSpace(string(envelope.Error)) != "null" {
		var failure struct {
			Data struct {
				Code       string `json:"code"`
				HTTPStatus int    `json:"httpStatus"`
			} `json:"data"`
		}
		if json.Unmarshal(envelope.Error, &failure) == nil {
			switch failure.Data.Code {
			case "UNAUTHORIZED":
				return nil, kiloRemoteResponseError(http.StatusUnauthorized, body, false)
			case "FORBIDDEN":
				return nil, kiloRemoteResponseError(http.StatusForbidden, body, false)
			case "TOO_MANY_REQUESTS":
				return nil, kiloRemoteResponseError(http.StatusTooManyRequests, body, false)
			}
			if failure.Data.HTTPStatus >= 400 {
				return nil, kiloRemoteResponseError(failure.Data.HTTPStatus, body, false)
			}
		}
		return nil, kiloRemoteShapeError()
	}
	if envelope.Result == nil || len(envelope.Result.Data) == 0 || strings.TrimSpace(string(envelope.Result.Data)) == "null" {
		return nil, kiloRemoteShapeError()
	}
	return envelope.Result.Data, nil
}

var _ KiloIdentityRemote = (*kiloRemoteClient)(nil)

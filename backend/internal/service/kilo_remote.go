package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const (
	kiloRemoteAPIBase = "https://api.kilo.ai"
	kiloRemoteAppBase = "https://app.kilo.ai"
	kiloRemoteTimeout = 20 * time.Second
	kiloRemoteMaxBody = 1 << 20

	// KiloIdentityForbiddenReason is reserved for a confirmed Kilo identity
	// rejection. HTTP 403 alone can also mean an organization or WAF denial.
	KiloIdentityForbiddenReason = "KILO_IDENTITY_FORBIDDEN"
)

type kiloRemoteClient struct {
	getClient func(httpclient.Options) (*http.Client, error)
}

// NewKiloRemoteClient uses the official profile, device authorization and BYOK
// status endpoints. It never imports a ChatGPT token or makes an inference call.
func NewKiloRemoteClient() KiloRemote {
	return &kiloRemoteClient{getClient: httpclient.GetClient}
}

func (c *kiloRemoteClient) Profile(ctx context.Context, key, proxyURL string) (*KiloRemoteProfile, error) {
	key, err := kiloRemoteCredential(key)
	if err != nil {
		return nil, err
	}
	status, body, err := c.request(ctx, http.MethodGet, kiloRemoteAPIBase+"/api/profile", key, proxyURL, "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, kiloRemoteResponseError(status, body, true)
	}
	var profile struct {
		User *struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
		HasPersonalAccount *bool              `json:"hasPersonalAccount"`
		Organizations      []KiloOrganization `json:"organizations"`
	}
	if json.Unmarshal(body, &profile) != nil || profile.User == nil || profile.HasPersonalAccount == nil ||
		strings.TrimSpace(profile.User.ID) == "" || strings.TrimSpace(profile.User.Email) == "" {
		return nil, kiloRemoteShapeError()
	}
	for _, org := range profile.Organizations {
		if strings.TrimSpace(org.ID) == "" || strings.TrimSpace(org.Name) == "" {
			return nil, kiloRemoteShapeError()
		}
	}
	return &KiloRemoteProfile{
		ID:                 profile.User.ID,
		Email:              profile.User.Email,
		HasPersonalAccount: *profile.HasPersonalAccount,
		Organizations:      profile.Organizations,
	}, nil
}

func (c *kiloRemoteClient) Connection(ctx context.Context, key, organizationID, proxyURL string) (*KiloConnectionStatus, error) {
	key, err := kiloRemoteCredential(key)
	if err != nil {
		return nil, err
	}
	input := struct {
		OrganizationID string `json:"organizationId,omitempty"`
	}{OrganizationID: strings.TrimSpace(organizationID)}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, kiloRemoteShapeError()
	}
	// cloud@eaa7598 uses tRPC's identity transformer. A {"json": ...}
	// wrapper would hide organizationId from its Zod input and select personal.
	query := url.Values{"input": {string(payload)}}
	endpoint := kiloRemoteAppBase + "/api/trpc/openAiChatGpt.status?" + query.Encode()
	status, body, err := c.request(ctx, http.MethodGet, endpoint, key, proxyURL, "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, kiloRemoteResponseError(status, body, false)
	}
	var envelope struct {
		Result *struct {
			Data json.RawMessage `json:"data"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, kiloRemoteShapeError()
	}
	if len(envelope.Error) != 0 && string(envelope.Error) != "null" {
		var failure struct {
			Data struct {
				Code       string `json:"code"`
				HTTPStatus int    `json:"httpStatus"`
			} `json:"data"`
		}
		if json.Unmarshal(envelope.Error, &failure) == nil {
			switch failure.Data.Code {
			case "UNAUTHORIZED":
				return nil, kiloRemoteHTTPError(http.StatusUnauthorized)
			case "FORBIDDEN":
				return nil, kiloRemoteAccessDenied()
			case "TOO_MANY_REQUESTS":
				return nil, kiloRemoteHTTPError(http.StatusTooManyRequests)
			}
			if failure.Data.HTTPStatus >= 400 {
				return nil, kiloRemoteResponseError(failure.Data.HTTPStatus, body, false)
			}
		}
		return nil, kiloRemoteShapeError()
	}
	if envelope.Result == nil {
		return nil, kiloRemoteShapeError()
	}
	var connection struct {
		State        string  `json:"state"`
		Email        *string `json:"email"`
		Subject      *string `json:"subject"`
		ConnectedAt  *string `json:"connectedAt"`
		ErrorMessage *string `json:"errorMessage"`
		UsageLimit   *struct {
			ReachedAt string  `json:"reachedAt"`
			ResetsAt  *string `json:"resetsAt"`
			Code      *string `json:"code"`
		} `json:"usageLimit"`
	}
	if json.Unmarshal(envelope.Result.Data, &connection) != nil {
		return nil, kiloRemoteShapeError()
	}
	switch connection.State {
	case "connected", "error":
		// The resolver always returns a verified subject for a stored row.
		// Missing email remains unmatchable to a local source; never substitute
		// this OAuth client's subject for the Codex chatgpt_account_id.
		if connection.Subject == nil || strings.TrimSpace(*connection.Subject) == "" {
			return nil, kiloRemoteShapeError()
		}
	case "disconnected":
	default:
		return nil, kiloRemoteShapeError()
	}
	result := &KiloConnectionStatus{State: connection.State, Connected: connection.State == "connected"}
	if connection.Email != nil {
		result.Email = strings.TrimSpace(*connection.Email)
	}
	if limit := connection.UsageLimit; limit != nil {
		if !result.Connected {
			return nil, kiloRemoteShapeError()
		}
		if _, err := time.Parse(time.RFC3339Nano, limit.ReachedAt); err != nil {
			return nil, kiloRemoteShapeError()
		}
		// An app-specific limit can have no reset time. Keep it active until
		// Kilo clears its recorded limit; never invent a weekly reset.
		result.UsageLimitReached = true
		if limit.ResetsAt != nil {
			reset, err := time.Parse(time.RFC3339Nano, *limit.ResetsAt)
			if err != nil {
				return nil, kiloRemoteShapeError()
			}
			result.UsageResetsAt = &reset
			result.UsageLimitReached = time.Now().Before(reset)
		}
	}
	return result, nil
}

func (c *kiloRemoteClient) StartDevice(ctx context.Context, proxyURL string) (*KiloRemoteDevice, error) {
	status, body, err := c.request(ctx, http.MethodPost, kiloRemoteAPIBase+"/api/device-auth/codes", "", proxyURL, "{}")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, kiloRemoteResponseError(status, body, false)
	}
	var device struct {
		Code            string `json:"code"`
		UserCode        string `json:"user_code"`
		VerificationURL string `json:"verificationUrl"`
		ExpiresIn       int64  `json:"expiresIn"`
	}
	if json.Unmarshal(body, &device) != nil || !kiloRemoteDeviceCode(device.Code) ||
		strings.TrimSpace(device.UserCode) == "" || device.ExpiresIn <= 0 || device.ExpiresIn > 86400 ||
		!kiloRemoteVerificationURL(device.VerificationURL) {
		return nil, kiloRemoteShapeError()
	}
	return &KiloRemoteDevice{
		Code:            device.Code,
		UserCode:        device.UserCode,
		VerificationURL: device.VerificationURL,
		ExpiresIn:       time.Duration(device.ExpiresIn) * time.Second,
		Status:          "pending",
	}, nil
}

func (c *kiloRemoteClient) PollDevice(ctx context.Context, code, proxyURL string) (*KiloRemoteDevice, error) {
	if !kiloRemoteDeviceCode(code) {
		return nil, infraerrors.BadRequest("KILO_DEVICE_CODE_INVALID", "Kilo设备授权码无效，请重新发起设备授权")
	}
	status, body, err := c.request(ctx, http.MethodGet, kiloRemoteAPIBase+"/api/device-auth/codes/"+url.PathEscape(code), "", proxyURL, "")
	if err != nil {
		return nil, err
	}
	var expected string
	switch status {
	case http.StatusOK:
		expected = "approved"
	case http.StatusAccepted:
		expected = "pending"
	case http.StatusForbidden:
		expected = "denied"
	case http.StatusGone:
		expected = "expired"
	default:
		return nil, kiloRemoteResponseError(status, body, false)
	}
	var device struct {
		Status string `json:"status"`
		Token  string `json:"token"`
		Email  string `json:"userEmail"`
	}
	if json.Unmarshal(body, &device) != nil || device.Status != expected {
		return nil, kiloRemoteShapeError()
	}
	result := &KiloRemoteDevice{Code: code, Status: expected}
	if expected == "approved" {
		token, tokenErr := kiloRemoteCredential(device.Token)
		if tokenErr != nil || strings.TrimSpace(device.Email) == "" {
			return nil, kiloRemoteShapeError()
		}
		result.Token = token
		result.Email = device.Email
	}
	return result, nil
}

func (c *kiloRemoteClient) Models(ctx context.Context, key, organization, proxyURL string) ([]string, error) {
	key, err := kiloRemoteCredential(key)
	if err != nil {
		return nil, err
	}
	if !kiloValidOrganization(organization) {
		return nil, kiloBad("ORGANIZATION_DENIED", "Kilo组织编号无效")
	}
	endpoint := KiloGatewayBaseURL
	headers := make(http.Header)
	if organization != "" {
		endpoint += "/organizations/" + url.PathEscape(organization)
		headers.Set("X-KILOCODE-ORGANIZATIONID", organization)
	}
	status, body, err := c.requestWithHeaders(ctx, http.MethodGet, endpoint+"/models", key, proxyURL, "", headers, 4<<20)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		// Model/organization permissions do not establish an identity ban.
		return nil, kiloRemoteResponseError(status, body, false)
	}
	return parseKiloAuthenticatedCatalog(body)
}

func (c *kiloRemoteClient) request(ctx context.Context, method, endpoint, key, proxyURL, body string) (int, []byte, error) {
	return c.requestWithHeaders(ctx, method, endpoint, key, proxyURL, body, nil, kiloRemoteMaxBody)
}

func (c *kiloRemoteClient) requestWithHeaders(ctx context.Context, method, endpoint, key, proxyURL, body string, headers http.Header, maximum int64) (int, []byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, kiloRemoteTimeout)
	defer cancel()
	client, err := c.getClient(httpclient.Options{
		ProxyURL: proxyURL, Timeout: kiloRemoteTimeout, ValidateResolvedIP: true,
	})
	if err != nil {
		return 0, nil, infraerrors.BadRequest("KILO_PROXY_INVALID", "Kilo代理配置不可用，请修正代理后重试；未回退直连")
	}
	// The pool owns the cached client; redirect and timeout policies are local
	// to this boundary so credentials never follow an upstream redirect.
	isolated := *client
	isolated.Timeout = kiloRemoteTimeout
	isolated.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(requestCtx, method, endpoint, strings.NewReader(body))
	if err != nil {
		return 0, nil, kiloRemoteShapeError()
	}
	for name, values := range headers {
		req.Header[name] = append([]string(nil), values...)
	}
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := isolated.Do(req)
	if err != nil {
		switch {
		case errors.Is(requestCtx.Err(), context.Canceled):
			return 0, nil, infraerrors.ClientClosed("KILO_REQUEST_CANCELLED", "Kilo请求已取消")
		case errors.Is(requestCtx.Err(), context.DeadlineExceeded):
			return 0, nil, infraerrors.GatewayTimeout("KILO_REMOTE_TIMEOUT", "Kilo官方服务请求超时，请检查代理后重试")
		default:
			return 0, nil, infraerrors.ServiceUnavailable("KILO_REMOTE_UNAVAILABLE", "无法连接Kilo官方服务，请检查选定代理后重试")
		}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maximum+1))
	if err != nil {
		return 0, nil, infraerrors.ServiceUnavailable("KILO_REMOTE_READ_FAILED", "读取Kilo官方响应失败，请稍后重试")
	}
	if int64(len(data)) > maximum {
		return 0, nil, kiloRemoteShapeError()
	}
	return resp.StatusCode, data, nil
}

func kiloRemoteCredential(key string) (string, error) {
	key = strings.TrimSpace(key)
	if len(key) >= 7 && strings.EqualFold(key[:7], "Bearer ") {
		key = strings.TrimSpace(key[7:])
	}
	if key != "" {
		valid := true
		for _, ch := range key {
			if ch <= ' ' || ch > '~' {
				valid = false
				break
			}
		}
		if valid {
			return key, nil
		}
	}
	return "", infraerrors.BadRequest("KILO_CREDENTIAL_INVALID", "Kilo凭据为空或格式无效，请重新导入API Key或完成设备授权")
}

func kiloRemoteDeviceCode(code string) bool {
	if len(code) == 0 || len(code) > 512 {
		return false
	}
	for _, ch := range code {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}

func kiloRemoteVerificationURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" && u.Port() != "443" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "kilo.ai" || strings.HasSuffix(host, ".kilo.ai")
}

func kiloRemoteShapeError() error {
	return infraerrors.New(http.StatusBadGateway, "KILO_REMOTE_RESPONSE_INVALID", "Kilo官方响应格式不受支持，未确认连接状态；请稍后重试或检查官方BYOK页面")
}

func kiloRemoteAccessDenied() error {
	return infraerrors.Forbidden("KILO_ACCESS_DENIED", "Kilo拒绝访问，请检查代理、账号状态及所选组织权限；尚未确认身份封禁")
}

func kiloRemoteResponseError(status int, body []byte, profile bool) error {
	if status == http.StatusForbidden && (!profile || !kiloProfileIdentityForbidden(body)) {
		return kiloRemoteAccessDenied()
	}
	return kiloRemoteHTTPError(status)
}

// Only the unscoped profile can establish which identity the rejection belongs
// to. Gateway/provider, organization and device authorization failures cannot.
func kiloProfileIdentityForbidden(body []byte) bool {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Code string `json:"code"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	code := envelope.Error.Code
	if code == "" {
		code = envelope.Code
	}
	switch code {
	case KiloIdentityForbiddenReason, "account_banned", "account_suspended", "account_disabled", "user_banned", "user_suspended", "user_disabled":
		return true
	default:
		return false
	}
}

func kiloRemoteHTTPError(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return infraerrors.BadRequest("KILO_AUTH_INVALID", "Kilo凭据已失效或被撤销，请重新导入API Key或完成设备授权")
	case http.StatusForbidden:
		return infraerrors.Forbidden(KiloIdentityForbiddenReason, "Kilo身份被拒绝访问，请检查身份封禁状态")
	case http.StatusTooManyRequests:
		return infraerrors.TooManyRequests("KILO_RATE_LIMITED", "Kilo官方请求过多，请稍后重试")
	case http.StatusNotFound, http.StatusGone:
		return infraerrors.BadRequest("KILO_AUTHORIZATION_EXPIRED", "Kilo授权已失效，请重新发起授权")
	case http.StatusBadRequest:
		return infraerrors.BadRequest("KILO_REMOTE_REQUEST_INVALID", "Kilo拒绝请求，请核对组织与授权参数")
	default:
		return infraerrors.ServiceUnavailable("KILO_REMOTE_UNAVAILABLE", "Kilo官方服务暂不可用，请检查代理后重试")
	}
}

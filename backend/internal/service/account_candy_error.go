package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const accountCandyErrorDetailLimit = 2048

var (
	candySecretKey   = regexp.MustCompile(`(?i)token|secret|password|authorization|cookie|api[-_]?key|session|task_id`)
	candyAuthValue   = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[a-z0-9._~+/=-]+`)
	candyHeaderValue = regexp.MustCompile(`(?im)\b(authorization|proxy-authorization|cookie|set-cookie)\s*[:=]\s*[^\r\n]+`)
	candyURLUser     = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@]+@`)
	candyTokenValue  = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]+|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)
)

// Only structured diagnostic fields are retained, never raw bodies or headers.
func accountCandyUpstreamFailure(prefix string, raw []byte) error {
	detail := accountCandyUpstreamDetail(raw, 0)
	if detail == "" {
		return candyProbeFailure(prefix + "（上游未提供可用的结构化错误详情）；未转组。")
	}
	return candyProbeFailure(prefix + "：" + detail + "；未转组。")
}

func accountCandyHTTPFailure(status int, raw []byte) error {
	reason := "上游拒绝请求"
	switch status {
	case http.StatusBadRequest:
		reason = "请求参数被拒绝"
	case http.StatusUnauthorized:
		reason = "上游未授权，请检查账号凭据"
	case http.StatusPaymentRequired:
		reason = "上游要求付费，请检查余额或额度"
	case http.StatusForbidden:
		reason = "上游拒绝访问，请检查权限或访问限制"
	case http.StatusNotFound:
		reason = "上游接口或模型不存在"
	case http.StatusProxyAuthRequired:
		reason = "代理认证失败"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		reason = "上游请求超时"
	case http.StatusTooManyRequests:
		reason = "上游限流，请检查速率或额度限制"
	default:
		if status >= 500 {
			reason = "上游服务异常"
		}
	}
	if detail := strings.TrimSpace(accountCandyUpstreamDetail(raw, 0)); detail != "" {
		// Exact account-secret redaction must run before length limiting: a
		// truncated secret can no longer be matched against its saved value.
		lower := strings.ToLower(detail)
		if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
			detail = "上游返回了网页错误内容"
		}
		reason += "；" + detail
	}
	return &accountCandyProbeError{httpStatus: status, message: reason + "；未转组。"}
}

// Do not expose a transport error's signed URL, proxy credentials or raw
// response. Retry markers keep their type so the caller can enforce its policy.
func accountCandyTransportFailure(err error, response *http.Response) error {
	if response != nil {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		return &accountCandyProbeError{httpStatus: response.StatusCode, message: "已收到上游响应，但请求传输未完成；未转组。"}
	}
	if accountCandyCanChangeProxy(err) {
		return err
	}
	reason := "请求发送失败，请检查账号出口与网络"
	var dns *net.DNSError
	var operation *net.OpError
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		reason = "请求已取消"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout()):
		reason = "连接或等待上游响应超时"
	case errors.As(err, &dns):
		reason = "域名解析失败"
	case errors.As(err, &operation):
		reason = "代理或网络连接失败"
	}
	return candyProbeFailure("未收到 HTTP 响应：" + reason + "；未转组。")
}

func accountCandyUpstreamDetail(raw []byte, depth int) string {
	if depth > 4 || !candyHasJSONValue(raw) {
		return ""
	}
	var message string
	if json.Unmarshal(raw, &message) == nil {
		return message
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	for _, key := range []string{"error", "response"} {
		if detail := accountCandyUpstreamDetail(fields[key], depth+1); detail != "" {
			return detail
		}
	}
	parts := make([]string, 0, 4)
	for _, key := range []string{"message", "detail"} {
		if json.Unmarshal(fields[key], &message) == nil {
			if text := strings.TrimSpace(message); text != "" {
				parts = append(parts, text)
				break
			}
		}
	}
	for _, field := range []struct{ key, label string }{{"code", "错误码"}, {"type", "类型"}, {"param", "参数"}} {
		var value any
		if json.Unmarshal(fields[field.key], &value) != nil {
			continue
		}
		var text string
		switch v := value.(type) {
		case string:
			text = v
		case float64:
			text = fmt.Sprint(v)
		}
		if text = strings.TrimSpace(text); text != "" {
			parts = append(parts, field.label+"="+text)
		}
	}
	return strings.Join(parts, "；")
}

func limitCandyErrorText(text string) string {
	runes := []rune(text)
	if len(runes) > accountCandyErrorDetailLimit {
		return string(runes[:accountCandyErrorDetailLimit-1]) + "…"
	}
	return text
}

func sanitizeCandyErrorText(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
		return "上游返回了网页错误内容"
	}
	text = candyAuthValue.ReplaceAllString(text, "$1 [REDACTED]")
	text = candyHeaderValue.ReplaceAllString(text, "$1: [REDACTED]")
	text = candyURLUser.ReplaceAllString(text, "$1[REDACTED]@")
	text = candyTokenValue.ReplaceAllString(text, "[REDACTED]")
	text = logredact.RedactText(text, "api_key", "api-key", "authorization", "proxy-authorization", "cookie", "set-cookie", "token", "secret", "session", "session_token")
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, text)
	return limitCandyErrorText(strings.Join(strings.Fields(text), " "))
}

// Known credential values are also removed when an upstream echoes them without
// a recognizable field name. Model names/mappings remain useful diagnostics.
func redactCandyAccountError(message string, accounts ...*Account) string {
	redact := func(secret string) {
		if secret == "" {
			return
		}
		for _, value := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
			message = strings.ReplaceAll(message, value, "[REDACTED]")
		}
	}
	var visit func(any, bool, int)
	visit = func(value any, sensitive bool, depth int) {
		if depth > 8 {
			return
		}
		switch v := value.(type) {
		case string:
			if sensitive {
				redact(v)
			}
		case map[string]any:
			for key, item := range v {
				visit(item, sensitive || candySecretKey.MatchString(key), depth+1)
			}
		case map[string]string:
			for key, item := range v {
				visit(item, sensitive || candySecretKey.MatchString(key), depth+1)
			}
		case []any:
			for _, item := range v {
				visit(item, sensitive, depth+1)
			}
		}
	}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		visit(account.Credentials, false, 0)
		for _, value := range account.GetHeaderOverrides() {
			redact(value)
		}
		if account.Proxy != nil {
			redact(account.Proxy.Username)
			redact(account.Proxy.Password)
		}
	}
	return sanitizeCandyErrorText(message)
}

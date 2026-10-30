package service

import (
	"encoding/json"
	"fmt"
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

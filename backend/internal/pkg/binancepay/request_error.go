package binancepay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
)

// RequestError contains only fixed categories and bounded protocol codes. It
// deliberately retains neither the signed URL nor an upstream error/body.
type RequestError struct {
	Stage        string
	Reason       string
	HTTPStatus   int
	ProviderCode string
}

func (e *RequestError) Error() string {
	stage, reason, status, code := e.SafeDiagnostic()
	return fmt.Sprintf("binance: %s failed (%s; HTTP %d; code %s)", stage, reason, status, code)
}

// SafeDiagnostic is also used by user-facing messages; even manually created
// RequestErrors cannot inject an arbitrary stage, reason, message or code.
func (e *RequestError) SafeDiagnostic() (stage, reason string, status int, code string) {
	stage = "request"
	switch e.Stage {
	case "time_sync", "permission_check", "pay_history", "request", "credentials", "local_validation", "proxy_preflight":
		stage = e.Stage
	}
	reason = "unknown"
	switch e.Reason {
	case "proxy_auth", "proxy_connect", "dns", "connection_refused", "connection_reset", "tls", "network", "timeout", "cancelled",
		"credentials", "reading_disabled", "api_key_invalid", "api_key_ip_permission", "signature", "timestamp", "region_restricted",
		"access_denied", "unauthorized", "provider_unavailable", "http_error", "provider_rejected", "pay_rejected",
		"response_read", "response_oversized", "response_invalid", "provider_time_invalid", "history_incomplete", "request_invalid":
		reason = e.Reason
	}
	if e.HTTPStatus >= 100 && e.HTTPStatus <= 599 {
		status = e.HTTPStatus
	}
	code = safeProviderCode(e.ProviderCode)
	return
}

func (e *RequestError) Unwrap() error {
	switch e.Reason {
	case "timeout":
		return context.DeadlineExceeded
	case "cancelled":
		return context.Canceled
	default:
		return nil
	}
}

func requestStage(path string) string {
	switch path {
	case "/api/v3/time":
		return "time_sync"
	case "/sapi/v1/account/apiRestrictions":
		return "permission_check"
	case historyPath:
		return "pay_history"
	default:
		return "request"
	}
}

func safeProviderCode(value string) string {
	if value == "" || len(value) > 9 {
		return ""
	}
	digits := strings.TrimPrefix(value, "-")
	if digits == "" || len(digits) > 8 {
		return ""
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return value
}

func responseProviderCode(body []byte) string {
	var response struct {
		Code json.RawMessage `json:"code"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Code) == 0 {
		return ""
	}
	var text string
	if response.Code[0] == '"' {
		if json.Unmarshal(response.Code, &text) != nil {
			return ""
		}
	} else {
		text = string(response.Code)
	}
	return safeProviderCode(text)
}

func responseRequestError(stage string, status int, code string) *RequestError {
	reason := "provider_rejected"
	switch status {
	case http.StatusUnauthorized:
		reason = "unauthorized"
	case http.StatusForbidden:
		reason = "access_denied"
	case http.StatusUnavailableForLegalReasons:
		reason = "region_restricted"
	default:
		if status >= 500 {
			reason = "provider_unavailable"
		} else if status != http.StatusOK {
			reason = "http_error"
		}
	}
	// Only documented fixed codes refine the category. Messages from Binance,
	// proxies and WAF pages are never used as presentation text.
	switch code {
	case "-2014":
		reason = "api_key_invalid"
	case "-2015":
		reason = "api_key_ip_permission"
	case "-1021":
		reason = "timestamp"
	case "-1022":
		reason = "signature"
	case "-1002":
		reason = "unauthorized"
	}
	return &RequestError{Stage: stage, Reason: reason, HTTPStatus: status, ProviderCode: safeProviderCode(code)}
}

func transportRequestError(stage string, ctx context.Context, err error) *RequestError {
	var known *RequestError
	if errors.As(err, &known) {
		_, reason, status, code := known.SafeDiagnostic()
		return &RequestError{Stage: stage, Reason: reason, HTTPStatus: status, ProviderCode: code}
	}
	reason := "network"
	var dns *net.DNSError
	var network net.Error
	var certificate *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var tlsRecord tls.RecordHeaderError
	switch {
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		reason = "cancelled"
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		reason = "timeout"
	case errors.As(err, &dns):
		reason = "dns"
	case errors.As(err, &network) && network.Timeout():
		reason = "timeout"
	case errors.As(err, &certificate), errors.As(err, &authority), errors.As(err, &hostname), errors.As(err, &invalid), errors.As(err, &tlsRecord):
		reason = "tls"
	case errors.Is(err, syscall.ECONNREFUSED):
		reason = "connection_refused"
	case errors.Is(err, syscall.ECONNRESET):
		reason = "connection_reset"
	}
	return &RequestError{Stage: stage, Reason: reason}
}

func negativeProviderCode(code string) bool {
	value, err := strconv.ParseInt(code, 10, 32)
	return err == nil && value < 0
}

// Only transport failures may rotate the configured route. A provider
// rejection, permission/allowlist failure or rate limit is never bypassed.
func IsRetryableNetworkError(err error) bool {
	var request *RequestError
	if !errors.As(err, &request) {
		return false
	}
	_, reason, status, _ := request.SafeDiagnostic()
	switch reason {
	case "dns", "connection_refused", "connection_reset", "network", "timeout", "response_read":
		return status == 0 || (reason == "response_read" && status == http.StatusOK)
	default:
		return false
	}
}

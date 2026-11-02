package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
)

func TestPKBinanceDiagnosticMessagesSeparateProxyPermissionAndClock(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want []string
	}{
		{&binancepay.RequestError{Stage: "time_sync", Reason: "proxy_auth", HTTPStatus: 407}, []string{"代理认证失败", "校时", "HTTP=407"}},
		{&binancepay.RequestError{Stage: "pay_history", Reason: "api_key_ip_permission", HTTPStatus: 401, ProviderCode: "-2015"}, []string{"实际出口 IP", "白名单", "动态出口", "Pay账单", "代码=-2015"}},
		{&binancepay.RequestError{Stage: "pay_history", Reason: "signature", HTTPStatus: 400, ProviderCode: "-1022"}, []string{"签名", "Key 配对", "代码=-1022"}},
		{&binancepay.RequestError{Stage: "time_sync", Reason: "region_restricted", HTTPStatus: 451}, []string{"出口地区", "HTTP=451"}},
		{&binancepay.RequestError{Stage: "pay_history", Reason: "timestamp", HTTPStatus: 400, ProviderCode: "-1021"}, []string{"服务器时间", "代理延迟", "代码=-1021"}},
		{&binancepay.RequestError{Stage: "permission_check", Reason: "reading_disabled"}, []string{"读取权限", "阶段=读取权限"}},
		{&binancepay.RequestError{Stage: "time_sync", Reason: "timeout"}, []string{"网络超时", "阶段=校时"}},
		{errBinanceCredentialDecrypt, []string{"无法解密", "重新保存"}},
		{errBinanceLocalProcessing, []string{"账单已获取", "本地核对"}},
	} {
		message := binanceRechargePollError(tc.err)
		for _, want := range tc.want {
			if !strings.Contains(message, want) {
				t.Fatalf("expected fixed diagnostic fragment %q", want)
			}
		}
		if strings.Contains(message, "香港") {
			t.Fatal("diagnostic incorrectly assumes a working Hong Kong route")
		}
	}
}

func TestPKBinanceDiagnosticMessagesNeverExposeUntrustedErrorFields(t *testing.T) {
	const private = "fixture-secret https://proxy.invalid?signature=hidden"
	for _, err := range []error{
		errors.New(private),
		&binancepay.RequestError{Stage: private, Reason: private, HTTPStatus: 9999, ProviderCode: private},
	} {
		message := binanceRechargePollError(err)
		if strings.Contains(message, "fixture-secret") || strings.Contains(message, "proxy.invalid") || strings.Contains(message, "signature=") || strings.Contains(message, "9999") {
			t.Fatal("untrusted diagnostic fields escaped")
		}
	}
}

func TestPKBinanceDiagnosticRecoveryKeepsRateLimitBoundary(t *testing.T) {
	for _, reason := range []string{"proxy_auth", "api_key_ip_permission", "signature", "network", "timestamp", "timeout"} {
		message := binanceRechargePollError(&binancepay.RequestError{Stage: "pay_history", Reason: reason, HTTPStatus: 400, ProviderCode: "-2015"})
		if !binanceRechargeRecoverableError(message) {
			t.Fatal("saved connection repair no longer recognizes safe diagnostics")
		}
	}
	for _, status := range []int{429, 418} {
		message := binanceRechargePollError(&binancepay.RateLimitError{StatusCode: status, RetryAfter: time.Hour})
		if message != binanceRechargeRateLimitMessage || binanceRechargeRecoverableError(message) {
			t.Fatal("provider rate-limit recovery boundary changed")
		}
	}
	if binanceRechargeRecoverableError("unknown old private error") || binanceRechargeRecoverableError(binanceRechargePollError(errBinanceLocalProcessing)) {
		t.Fatal("connection editing can erase unknown or local-processing failures")
	}
	if !errors.Is(&binancepay.RequestError{Reason: "timeout"}, context.DeadlineExceeded) {
		t.Fatal("typed timeout no longer supports cancellation semantics")
	}
}

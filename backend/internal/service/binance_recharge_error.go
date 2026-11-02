package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
)

var (
	errBinanceCredentialDecrypt = errors.New("binance: stored credentials unavailable")
	errBinanceLocalProcessing   = errors.New("binance: local receipt processing failed")
)

var binanceRequestErrorMessages = map[string]string{
	"proxy_auth":            "代理认证失败，请检查专属代理用户名、密码或供应商认证方式",
	"proxy_connect":         "代理拒绝建立币安连接，请检查代理协议及是否允许访问币安",
	"dns":                   "查询域名解析失败，请检查代理或服务器 DNS",
	"connection_refused":    "查询连接被拒绝，请检查代理地址、端口及服务状态",
	"connection_reset":      "查询连接被重置，请检查代理稳定性及出口限制",
	"tls":                   "查询 TLS 握手或证书校验失败，请检查代理协议、证书及网络",
	"network":               "查询网络连接失败，请检查代理连通性及出口限制",
	"timeout":               "查询网络超时，稍后自动重试",
	"cancelled":             "查询已取消，未完成本次查账",
	"credentials":           "API 凭据缺失或格式无效，请重新配置 API Key 和 Secret",
	"reading_disabled":      "API 未启用读取权限，请在币安 API 管理中开启读取",
	"api_key_invalid":       "API Key 格式无效，请检查是否复制了正确的币安 API Key",
	"api_key_ip_permission": "币安拒绝 API Key、出口 IP 或权限，请核对 Key 和读取权限，并将专属代理的实际出口 IP 加入 API 白名单；动态出口变化后需重新核对",
	"signature":             "币安签名校验失败，请核对 API Secret 是否与 Key 配对",
	"timestamp":             "币安时间戳校验失败，请检查服务器时间及代理延迟",
	"region_restricted":     "币安拒绝当前出口地区访问，请核对出口位置及币安地区限制",
	"access_denied":         "币安拒绝访问，请检查出口 IP、访问策略和 API 权限",
	"unauthorized":          "币安身份或权限校验失败，请检查 API 凭据及权限",
	"provider_unavailable":  "币安服务暂时不可用，稍后自动重试",
	"http_error":            "币安接口返回异常 HTTP 状态，请根据状态码检查网络及访问策略",
	"provider_rejected":     "币安接口拒绝查询，请根据错误代码核对 API 配置",
	"pay_rejected":          "币安 Pay 账单接口拒绝查询，请核对账户的 Pay 接口使用权限",
	"response_read":         "币安响应读取中断，未使用不完整账单，稍后自动重试",
	"response_oversized":    "币安响应超过安全上限，未使用不完整账单",
	"response_invalid":      "币安响应格式无效，请检查代理是否返回了拦截页面及接口兼容性",
	"provider_time_invalid": "币安校时响应无效，请检查服务器时间及代理出口返回内容",
	"history_incomplete":    "币安账单数量超过完整扫描范围，本轮未进行入账",
	"request_invalid":       "查询参数无效，请检查收款 UID 和查询时间范围",
}

func binanceRequestErrorMessage(err *binancepay.RequestError) string {
	stage, reason, status, code := err.SafeDiagnostic()
	message := binanceRequestErrorMessages[reason]
	if message == "" {
		message = "查询失败，未取得可确认的失败类别"
	}
	stages := map[string]string{"time_sync": "校时", "permission_check": "读取权限", "pay_history": "Pay账单",
		"credentials": "凭据校验", "local_validation": "参数校验", "request": "查询"}
	diagnostic := "阶段=" + stages[stage]
	if status != 0 {
		diagnostic += fmt.Sprintf("; HTTP=%d", status)
	}
	if code != "" {
		diagnostic += "; 代码=" + code
	}
	return "币安" + message + " [" + diagnostic + "]"
}

func binanceTypedErrorRecoverable(message string) bool {
	base, suffix, found := strings.Cut(message, " [阶段=")
	if !found || !strings.HasSuffix(suffix, "]") {
		return false
	}
	for _, known := range binanceRequestErrorMessages {
		if base == "币安"+known {
			return true
		}
	}
	return false
}

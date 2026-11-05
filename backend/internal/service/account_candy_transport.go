package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const accountCandyTLSHandshakeAttempts = 3

type accountCandyProxyRetryKey struct{}
type radarCandyProxyRetryKey struct{}

// Radar explicitly retries interrupted requests with no complete model answer.
// Other candy callers keep their original pre-send-only retry boundary.
type radarCandyRetryableNetworkError struct{ HTTPStatus int }

func (e *radarCandyRetryableNetworkError) Error() string {
	if e.HTTPStatus > 0 {
		return fmt.Sprintf("HTTP %d：响应传输中断，尚未取得完整回复；未转组。", e.HTTPStatus)
	}
	return "未收到 HTTP 响应：代理或网络连接中断，尚未取得完整回复；未转组。"
}

// Only this marker may admit another proxy. Its creation is kept at the send
// boundary where request/response evidence is still available.
type accountCandyRetryableNetworkError struct{}

func (*accountCandyRetryableNetworkError) Error() string {
	return "账号测试连接失败，尚未发送请求；未转组。"
}

func accountCandyCanChangeProxy(err error) bool {
	var failure *accountCandyRetryableNetworkError
	var radarFailure *radarCandyRetryableNetworkError
	return errors.As(err, &failure) || errors.As(err, &radarFailure)
}

// doAccountCandyTLSHandshakeRetry retries only a transport's TLS handshake
// timeout before any HTTP request was written. It never retries an HTTP
// response, a response-body error, or an untyped plugin error. The sender owns
// a fixed account, proxy and TLS profile for this probe; failed handshakes are
// discarded by the transport without clearing other requests' shared pools.
func doAccountCandyTLSHandshakeRetry(
	request *http.Request,
	send func(*http.Request) (*http.Response, error),
) (*http.Response, error) {
	// All current candy payloads use bytes.Reader and therefore have GetBody.
	// Preserve a single attempt if a future caller supplies a non-replayable body.
	if request.Body != nil && request.Body != http.NoBody && request.GetBody == nil {
		return send(request)
	}
	ctx := request.Context()
	for attempt := 1; attempt <= accountCandyTLSHandshakeAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if attempt == 1 && request.Body != nil {
				_ = request.Body.Close()
			}
			return nil, err
		}
		var requestWritten atomic.Bool
		trace := &httptrace.ClientTrace{
			WroteHeaders:         func() { requestWritten.Store(true) },
			WroteRequest:         func(httptrace.WroteRequestInfo) { requestWritten.Store(true) },
			GotFirstResponseByte: func() { requestWritten.Store(true) },
		}
		attemptRequest := request.Clone(httptrace.WithClientTrace(ctx, trace))
		if attempt > 1 && request.GetBody != nil {
			body, err := request.GetBody()
			if err != nil {
				return nil, candyProbeFailure("账号测试请求无法重新构建，已停止重试；未转组。")
			}
			attemptRequest.Body = body
		}
		response, err := send(attemptRequest)
		if err == nil || response != nil {
			if err == nil && attempt > 1 {
				logger.FromContext(ctx).Info("account_candy.tls_handshake_recovered",
					zap.Int("attempt", attempt),
					zap.Int("max_attempts", accountCandyTLSHandshakeAttempts))
			}
			return response, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if enabled, _ := ctx.Value(accountCandyProxyRetryKey{}).(bool); enabled {
			if _, recovering := ctx.Value(accountCandyRecoveryKey{}).(*accountCandyRecovery); recovering && !requestWritten.Load() && isAccountCandyConnectFailure(err) {
				return nil, &accountCandyRetryableNetworkError{}
			}
			if radar, _ := ctx.Value(radarCandyProxyRetryKey{}).(bool); radar && isRadarCandyNetworkFailure(err) {
				return nil, &radarCandyRetryableNetworkError{}
			}
			if !requestWritten.Load() && isAccountCandyConnectFailure(err) {
				return nil, &accountCandyRetryableNetworkError{}
			}
			return nil, err
		}
		if requestWritten.Load() || !isAccountCandyTLSHandshakeTimeout(err) {
			return nil, err
		}
		if attempt == accountCandyTLSHandshakeAttempts {
			logger.FromContext(ctx).Warn("account_candy.tls_handshake_exhausted",
				zap.Int("attempts", attempt))
			return nil, candyProbeFailure("账号测试建立 TLS 连接超时，已尝试3次握手；未转组。")
		}
		delay := time.Duration(attempt) * time.Second
		logger.FromContext(ctx).Warn("account_candy.tls_handshake_retry",
			zap.Int("next_attempt", attempt+1),
			zap.Int("max_attempts", accountCandyTLSHandshakeAttempts),
			zap.Duration("backoff", delay))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, candyProbeFailure("账号测试建立 TLS 连接超时，已停止重试；未转组。")
}

func isRadarCandyNetworkFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		return true
	}
	var network net.Error
	return errors.As(err, &network) && (network.Timeout() || isAccountCandyConnectFailure(err))
}

func isAccountCandyConnectFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if isAccountCandyTLSHandshakeTimeout(err) {
		return true
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var operation *net.OpError
	// Dial/HTTP CONNECT/SOCKS CONNECT failure proves no application request reached the
	// target even for transports that do not implement httptrace. Read/write
	// failures do not carry that guarantee and are never replayed here.
	return errors.As(err, &operation) && (operation.Op == "dial" || operation.Op == "proxyconnect" || operation.Op == "socks connect")
}

func isAccountCandyTLSHandshakeTimeout(err error) bool {
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		return false
	}
	// net/http's handshake timeout type is private. Match its exact leaf
	// diagnostic through transport wrappers, never a model's response text or
	// an arbitrary string containing "timeout". Plugin errors lack this typed
	// transport evidence and keep their existing no-replay behavior.
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if cause.Error() == "net/http: TLS handshake timeout" {
			return true
		}
	}
	return false
}

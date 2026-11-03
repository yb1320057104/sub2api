package service

import (
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	if s.pluginManager != nil && s.pluginManager.ShouldRouteOpenAIOAuth(account) {
		route, routeErr := s.accountProxyRouter().Acquire(request.Context(), account.ID, proxyURL, 0)
		if routeErr != nil {
			return nil, routeErr
		}
		// The plugin may use its own transport; hold the same global proxy slot
		// until its complete response is consumed or closed.
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(route.Context, request.WithContext(route.Context), route.URL, account)
		if handled {
			if err != nil || response == nil || response.Body == nil {
				route.Close()
			} else {
				response.Body = &accountProxyResponseBody{ReadCloser: response.Body, route: route}
			}
			return response, err
		}
		route.Close()
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}

type accountProxyResponseBody struct {
	io.ReadCloser
	route *AccountProxyRoute
}

func (b *accountProxyResponseBody) Close() error {
	b.route.Close()
	return b.ReadCloser.Close()
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	return s.openAIAccountTestSender(proxyURL, account, useTLSFallback)(request)
}

func (s *AccountTestService) doOpenAIAccountCandyUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	return doAccountCandyTLSHandshakeRetry(request, s.openAIAccountTestSender(proxyURL, account, true))
}

func (s *AccountTestService) openAIAccountTestSender(proxyURL string, account *Account, useTLSFallback bool) func(*http.Request) (*http.Response, error) {
	var profile *tlsfingerprint.Profile
	profileResolved := false
	return func(request *http.Request) (*http.Response, error) {
		if s.pluginManager != nil {
			response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
			if handled {
				return response, err
			}
		}
		if useTLSFallback {
			// Resolve even a random profile only once across handshake retries.
			if !profileResolved {
				profile = s.tlsFPProfileService.ResolveTLSProfile(account)
				profileResolved = true
			}
			return s.httpUpstream.DoWithTLS(request, proxyURL, account.ID, account.Concurrency, profile)
		}
		return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
	}
}

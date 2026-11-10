package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (result *http.Response, resultErr error) {
	if s.excelOAuthReauth != nil {
		if err := s.excelOAuthReauth.validateAuthConnectionPolicy(request.Context(), account); err != nil {
			return nil, err
		}
	}
	if account != nil && account.AuthConnectionID != "" {
		if s.excelOAuthReauth == nil {
			return nil, fmt.Errorf("authorization connection service unavailable")
		}
		if err := s.excelOAuthReauth.ValidateAuthConnectionSend(request.Context(), account); err != nil {
			return nil, err
		}
	}
	var phaseGuard *openAIHTTPPhaseGuard
	request, phaseGuard = s.guardOpenAIHTTPRequest(request, account)
	request, finishHeaderTimeout := guardOpenAIResponseHeaderTimeout(request, account)
	defer func() {
		if result != nil && result.Request == nil {
			result.Request = request
		}
		result, resultErr = phaseGuard.finish(result, resultErr)
		resultErr = finishHeaderTimeout(result, resultErr)
	}()
	if phaseGuard != nil && phaseGuard.ctx.Err() != nil {
		return nil, context.Cause(phaseGuard.ctx)
	}
	request, safetyInfo := s.applySafetyPromptToRequest(request, account)
	defer func() {
		if result != nil {
			result = s.wrapSafetyPromptResponse(result, safetyInfo)
		}
	}()
	if account.IsSIWCEnabled() {
		if request.URL == nil || request.URL.Scheme != "https" || request.URL.Host != "api.openai.com" || request.URL.Path != "/v1/responses" {
			return nil, fmt.Errorf("SIWC request must use the official Responses endpoint")
		}
		// Compatibility adapters can add internal headers after building a request.
		// Enforce SIWC headers at the final send boundary as well.
		authorization := request.Header.Get("Authorization")
		applySIWCRequestHeaders(request)
		request.Host = ""
		if err := s.priorityLatencyBeforeSend(request.Context(), account); err != nil {
			return nil, err
		}
		if err := s.fenceAuthConnectionEgress(request.Context(), request, account); err != nil {
			return nil, err
		}
		observeAttempt := s.observePriorityAttempt(request, account)
		resp, err := s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
		observeAttempt(resp, err)
		if err == nil && resp != nil && resp.StatusCode == http.StatusUnauthorized && s.excelOAuthReauth != nil {
			var cache OpenAITokenCache
			if s.openAITokenProvider != nil {
				cache = s.openAITokenProvider.tokenCache
			}
			s.excelOAuthReauth.rejectSIWCAccessToken(request.Context(), account, strings.TrimPrefix(authorization, "Bearer "), cache)
		}
		return resp, err
	}
	request = s.stickBoundCodexTicketRequest(request, account)
	boundTicket := s.codexTicketRequestBound(request, account)
	observationTicket := s.boundCodexTicket(request, account)
	observationHeaders := request.Header.Clone()
	releaseChat := func() {}
	if boundTicket {
		releaseChat = s.holdCodexTicketChat(account)
	}
	proxyURL, release, err := s.pinCodexTicketEgress(request, account, proxyURL)
	if err != nil {
		releaseChat()
		return nil, err
	}
	defer func() {
		observe := resultErr == nil && result != nil && result.StatusCode == http.StatusOK
		result, resultErr = attachCodexTicketEgressRelease(result, resultErr, func() {
			release()
			releaseChat()
			if observe {
				s.observeScheduledAstraCandy(request.Context(), observationHeaders, account, observationTicket)
			}
		})
	}()
	defer func() {
		if resultErr == nil {
			// Fast admission may have installed the ticket inside the upstream
			// adapter. Capture the binding before observation can revoke it.
			observedRequest := request
			if result != nil && result.Request != nil && s.UnifiedAstraTicketsEnabled(request.Context()) && s.codexTicketRequestBound(result.Request, account) {
				// Timing/fallback layers clone request headers before the routing
				// adapter installs a newly acquired bundle. Observe the sent copy.
				observedRequest = result.Request
			}
			if ticket := s.boundCodexTicket(observedRequest, account); ticket != nil {
				observationTicket = ticket
				observationHeaders = observedRequest.Header.Clone()
			}
			responseBound := boundTicket || s.codexTicketRequestBound(observedRequest, account)
			s.observeCodexTicketResponse(observedRequest, result, account)
			if responseBound && codexResponseMismatches(observedRequest, result, account) && s.settingService.CodexTicketStrictResponse(request.Context()) {
				if result.Body != nil {
					_ = result.Body.Close()
				}
				result = nil
				resultErr = ErrCodexTicketResponseRejected
			}
		}
	}()
	// Keep ticket observation/strict validation around the final response,
	// while every egress attempt retains its own plugin routing and trace.
	return s.doUpstreamWithProxyFallback(request.Context(), request, account, proxyURL)
}

type accountProxyResponseBody struct {
	io.ReadCloser
	route *AccountProxyRoute
}

func (b *accountProxyResponseBody) Close() error {
	b.route.Close()
	return b.ReadCloser.Close()
}

// doOpenAIAccountTestUpstream applies the same stored ticket and transport lifecycle
// as production forwarding. Use the final payload model, after mapping/normalization;
// a self-harvested ticket must not depend on membership in the gateway-reuse target list.
func (s *AccountTestService) doOpenAIAccountTestUpstream(request *http.Request, proxyURL string, account *Account, useTLSFallback bool, body []byte) (*http.Response, error) {
	return s.openAIAccountTestSender(proxyURL, account, useTLSFallback, body)(request)
}

type accountCandyFixedEgressKey struct{}

func (s *AccountTestService) doOpenAIAccountCandyUpstream(request *http.Request, proxyURL string, account *Account, body []byte) (*http.Response, error) {
	// The PK probe owns proxy rotation. A gateway ticket must not introduce an
	// independent fallback chain inside each of its three handshake attempts.
	request = request.WithContext(context.WithValue(request.Context(), accountCandyFixedEgressKey{}, true))
	return doAccountCandyTLSHandshakeRetry(request, s.openAIAccountTestSender(proxyURL, account, true, body))
}

func (s *AccountTestService) openAIAccountTestSender(proxyURL string, account *Account, useTLSFallback bool, body []byte) func(*http.Request) (*http.Response, error) {
	var profile *tlsfingerprint.Profile
	profileResolved := false
	return func(request *http.Request) (*http.Response, error) {
		gateway := s.openaiGatewayService
		model := extractOpenAICodexTicketModel(body)
		if gateway != nil && !IsAstraSourceAcquisition(request.Context()) && request.URL != nil && request.URL.String() == chatgptCodexAPIURL &&
			isOpenAICodexTicketAccount(account, model) && gateway.openAICodexTicketEnabledContext(request.Context()) &&
			gateway.openAICodexTicketGatedModel(model) {
			if err := gateway.applyOpenAICodexTicket(request.Context(), account, model, request.Header, "sse"); err != nil {
				return nil, err
			}
			// This restores the complete cookie/egress bundle, holds it until body close,
			// observes replacements and enforces the configured strict response policy.
			request = request.WithContext(context.WithValue(request.Context(), astraCandySkipObservationKey{}, true))
			return gateway.doOpenAIUpstream(request, proxyURL, account)
		}
		if s.pluginManager != nil {
			response, handled, err := roundTripOpenAIPluginWithProxySlot(s.pluginManager, s.httpUpstream, request, proxyURL, account)
			if handled {
				if options, ok := pelicanTestOptionsFromContext(request.Context()); ok && options.nativeOAuthTransport != nil && !IsAstraSourceAcquisition(request.Context()) {
					*options.nativeOAuthTransport = false
				}
				return response, err
			}
		}
		// Ticket admission (including donor tickets) and handled plugin requests
		// returned above. Only this native transport may certify an own OAuth 401.
		if options, ok := pelicanTestOptionsFromContext(request.Context()); ok && options.nativeOAuthTransport != nil && !IsAstraSourceAcquisition(request.Context()) {
			*options.nativeOAuthTransport = true
		}
		if useTLSFallback {
			// Resolve a random fingerprint once for the entire probe, including retries.
			if !profileResolved {
				profile = s.tlsFPProfileService.ResolveTLSProfile(account)
				profileResolved = true
			}
			return timedAccountTestUpstream(s.httpUpstream).DoWithTLS(request, proxyURL, account.ID, account.Concurrency, profile)
		}
		return timedAccountTestUpstream(s.httpUpstream).Do(request, proxyURL, account.ID, account.Concurrency)
	}
}

// Plugins own their HTTP client, so they must hold the same shared proxy lease
// as the native adapter until the response body closes. Administrative probes
// have no routing context and retain their explicitly chosen egress.
func roundTripOpenAIPluginWithProxySlot(manager *PluginManager, upstream HTTPUpstream, request *http.Request, proxyURL string, account *Account) (*http.Response, bool, error) {
	if manager == nil || !manager.ShouldRouteOpenAIOAuth(account) {
		return nil, false, nil
	}
	var router *AccountProxyRouter
	if provider, ok := upstream.(accountProxyRouterProvider); ok {
		router = provider.AccountProxyRouter()
	}
	route, err := router.Acquire(request.Context(), account.ID, proxyURL, 0)
	if err != nil {
		return nil, true, err
	}
	response, handled, err := manager.RoundTripOpenAIOAuth(route.Context, request.WithContext(route.Context), route.URL, account)
	if !handled || err != nil || response == nil || response.Body == nil {
		route.Close()
	} else {
		response.Body = &accountProxyResponseBody{ReadCloser: response.Body, route: route}
	}
	return response, handled, err
}

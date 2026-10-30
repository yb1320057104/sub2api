package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
)

const authorizationProbeKey = "pk_authorization_probe"

type authorizationProbeResult struct{ httpStatus int }

func authorizationProbe(c *gin.Context) *authorizationProbeResult {
	value, ok := c.Get(authorizationProbeKey)
	if !ok {
		return nil
	}
	p, _ := value.(*authorizationProbeResult)
	return p
}

// Reuse native credentials, model mapping, request identity, TLS, and account proxy.
// State writes are deferred to a CAS transaction after the network request.
func (s *AccountTestService) probeAuthorization(ctx context.Context, account *Account) (int, bool) {
	if _, err := accountCandyProxyURL(account); err != nil {
		return 0, false
	}
	if isOpenAIImageModel(account.GetMappedModel(openai.DefaultTestModel)) {
		return 0, false
	}
	p := &authorizationProbeResult{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = (&http.Request{Header: make(http.Header)}).WithContext(ctx)
	c.Set(authorizationProbeKey, p)
	c.Set(accountTestBackgroundKey, true)
	err := s.testOpenAIAccountConnection(c, account, openai.DefaultTestModel, "hi", AccountTestModeDefault)
	return p.httpStatus, err == nil && ctx.Err() == nil
}

func (s *OpenAIOAuthService) generateWorkbenchAuthURL(ctx context.Context, proxyURL string) (*OpenAIAuthURLResult, error) {
	result, err := s.GenerateAuthURL(ctx, nil, "", PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	session, ok := s.sessionStore.Get(result.SessionID)
	if !ok || session == nil {
		return nil, errors.New("authorization session expired")
	}
	copySession := *session
	copySession.ProxyURL = proxyURL
	s.sessionStore.Set(result.SessionID, &copySession)
	return result, nil
}

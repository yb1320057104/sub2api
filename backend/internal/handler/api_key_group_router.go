package handler

import "github.com/Wei-Shaw/sub2api/internal/service"

func (h *GatewayHandler) APIKeyGroupRouter() *service.APIKeyGroupRouter {
	if h == nil {
		return nil
	}
	return service.NewAPIKeyGroupRouter(h.gatewayService, h.openAIGatewayService, h.geminiCachedContentService)
}

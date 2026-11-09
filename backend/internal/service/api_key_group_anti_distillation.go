package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// ProvideAPIKeyServiceWithGroupProtection keeps existing constructors compatible
// while sharing the same protection service with the admin and gateway paths.
func ProvideAPIKeyServiceWithGroupProtection(
	apiKeyRepo APIKeyRepository,
	userRepo UserRepository,
	groupRepo GroupRepository,
	userSubRepo UserSubscriptionRepository,
	userGroupRateRepo UserGroupRateRepository,
	cache APIKeyCache,
	cfg *config.Config,
	billingCacheService *BillingCacheService,
	concurrencyService *ConcurrencyService,
	protection *GroupAntiDistillationService,
) *APIKeyService {
	svc := ProvideAPIKeyService(apiKeyRepo, userRepo, groupRepo, userSubRepo, userGroupRateRepo, cache, cfg, billingCacheService, concurrencyService)
	svc.groupAntiDistillation = protection
	return svc
}

// CheckGroupAntiDistillation uses the effective customer billing group and
// never changes the upstream account state or routes to a more expensive group.
func (s *APIKeyService) CheckGroupAntiDistillation(ctx context.Context, key *APIKey) (time.Duration, error) {
	if s == nil || s.groupAntiDistillation == nil || key == nil || key.Group == nil || key.Group.Platform != PlatformOpenAI {
		return 0, nil
	}
	return s.groupAntiDistillation.Check(ctx, key.UserID, key.Group.ID)
}

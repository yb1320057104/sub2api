package middleware

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Subscription membership is admission, whereas its balance/usage limits are
// enforced by the existing auth path after selection and never cause rerouting.
func eligibleAPIKeyGroupSubscription(ctx context.Context, key *service.APIKey, group *service.Group, subscriptions *service.SubscriptionService, cfg *config.Config) (bool, error) {
	return eligibleAPIKeyGroupSubscriptionWithLookup(ctx, key, group, subscriptions, cfg)
}

type keyGroupSubscriptionLookup interface {
	GetActiveSubscription(context.Context, int64, int64) (*service.UserSubscription, error)
}

func eligibleAPIKeyGroupSubscriptionWithLookup(ctx context.Context, key *service.APIKey, group *service.Group, subscriptions keyGroupSubscriptionLookup, cfg *config.Config) (bool, error) {
	if key == nil || key.User == nil || group == nil {
		return false, errors.New("group subscription admission unavailable")
	}
	if !group.IsSubscriptionType() || (cfg != nil && cfg.RunMode == config.RunModeSimple) {
		return true, nil
	}
	if subscriptions == nil {
		return false, errors.New("subscription service unavailable")
	}
	if concrete, ok := subscriptions.(*service.SubscriptionService); ok && concrete == nil {
		return false, errors.New("subscription service unavailable")
	}
	subscription, err := subscriptions.GetActiveSubscription(ctx, key.User.ID, group.ID)
	if errors.Is(err, service.ErrSubscriptionNotFound) || errors.Is(err, service.ErrSubscriptionExpired) || errors.Is(err, service.ErrSubscriptionSuspended) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if subscription == nil {
		return false, errors.New("subscription service returned no subscription")
	}
	if subscription.UserID != key.User.ID || subscription.GroupID != group.ID {
		return false, errors.New("subscription identity mismatch")
	}
	if subscription.DeletedAt != nil || !subscription.IsActive() {
		return false, nil
	}
	return true, nil
}

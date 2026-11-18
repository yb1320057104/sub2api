package service

import "context"

// KiloAccountView is an administrative identity projection. It never carries
// credentials and does not change the physical account or scheduler domain.
type KiloAccountView struct {
	PreparedID     *int64                 `json:"prepared_id,omitempty"`
	Name           string                 `json:"name"`
	Email          string                 `json:"email"`
	OrganizationID string                 `json:"organization_id"`
	HasKey         bool                   `json:"has_key"`
	Source         *KiloAccountSourceView `json:"source,omitempty"`
}

// KiloAccountSourceView describes only a persisted, reciprocal quota source.
type KiloAccountSourceView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	PlanType    string `json:"plan_type"`
	WorkspaceID string `json:"workspace_id"`
	Status      string `json:"status"`
	QuotaOnly   bool   `json:"quota_only"`
}

// KiloAccountViewReader is optional so physical account repositories and admin
// implementations need not implement a presentation-only dependency.
type KiloAccountViewReader interface {
	GetKiloAccountViews(context.Context, []int64) (map[int64]KiloAccountView, error)
}

type kiloAccountListContextKey struct{}

// WithKiloAccountList opts the admin list into logical identities before count
// and pagination. Export, credentials and scheduler reads remain physical.
func WithKiloAccountList(ctx context.Context) context.Context {
	return context.WithValue(ctx, kiloAccountListContextKey{}, true)
}

func IsKiloAccountList(ctx context.Context) bool {
	logical, _ := ctx.Value(kiloAccountListContextKey{}).(bool)
	return logical
}

func (s *adminServiceImpl) GetKiloAccountViews(ctx context.Context, ids []int64) (map[int64]KiloAccountView, error) {
	if s == nil {
		return nil, nil
	}
	if reader, ok := s.accountRepo.(KiloAccountViewReader); ok {
		return reader.GetKiloAccountViews(ctx, ids)
	}
	return nil, nil
}

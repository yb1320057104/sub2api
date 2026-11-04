package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type ProxyGroup struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	ProxyIDs         []int64   `json:"proxy_ids"`
	ProxyCount       int       `json:"proxy_count"`
	ActiveProxyCount int       `json:"active_proxy_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ProxyGroupInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ProxyGroupMembersInput struct {
	ProxyIDs     []int64 `json:"proxy_ids"`
	ProxyGroupID *int64  `json:"proxy_group_id"`
}

func (in *ProxyGroupMembersInput) UnmarshalJSON(data []byte) error {
	type plain ProxyGroupMembersInput
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, ok := fields["proxy_group_id"]; !ok {
		return fmt.Errorf("proxy_group_id must be supplied; use null to clear")
	}
	*in = ProxyGroupMembersInput(value)
	return nil
}

type GroupProxyPriorityPolicy struct {
	GroupID        int64   `json:"group_id"`
	StrictPriority bool    `json:"strict_priority"`
	ProxyGroupID   *int64  `json:"proxy_group_id"`
	ProxyIDs       []int64 `json:"-"`
}

type GroupProxyPriorityPolicyReader interface {
	GetGroupProxyPriorityPolicy(context.Context, int64) (*GroupProxyPriorityPolicy, error)
}

type GroupProxyPriorityUpdateInput struct {
	GroupIDs         []int64 `json:"group_ids"`
	StrictPriority   bool    `json:"strict_priority"`
	ProxyGroupID     *int64  `json:"proxy_group_id,omitempty"`
	ProxyGroupIDSet  bool    `json:"-"`
	ProxyConcurrency int     `json:"proxy_concurrency,omitempty"`
	AssignProxies    bool    `json:"assign_proxies"`
}

func (in *GroupProxyPriorityUpdateInput) UnmarshalJSON(data []byte) error {
	type plain GroupProxyPriorityUpdateInput
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if value, ok := fields["strict_priority"]; !ok || string(value) == "null" {
		return fmt.Errorf("strict_priority must be explicitly true or false")
	}
	*in = GroupProxyPriorityUpdateInput(value)
	_, in.ProxyGroupIDSet = fields["proxy_group_id"]
	return nil
}

type AccountProxyGroupAssignmentInput struct {
	AccountIDs       []int64 `json:"account_ids"`
	ProxyGroupID     int64   `json:"proxy_group_id"`
	ProxyConcurrency int     `json:"proxy_concurrency"`
}

type GroupProxyPriorityUpdateResult struct {
	GroupCount int `json:"group_count"`
	AccountProxyAssignmentResult
}

type ProxyGroupRepository interface {
	ListProxyGroups(context.Context) ([]ProxyGroup, error)
	SaveProxyGroup(context.Context, int64, ProxyGroupInput) (*ProxyGroup, error)
	DeleteProxyGroup(context.Context, int64) error
	SetProxyGroupMembers(context.Context, ProxyGroupMembersInput) (int, error)
	GetProxyGroupMemberships(context.Context, []int64) (map[int64]int64, error)
}

type GroupProxyPolicyRepository interface {
	GroupProxyPriorityPolicyReader
	ListGroupProxyPriorityPolicies(context.Context) ([]GroupProxyPriorityPolicy, error)
}

type AccountProxyGroupRepository interface {
	AssignAccountProxyGroup(context.Context, AccountProxyGroupAssignmentInput) (*AccountProxyAssignmentResult, error)
	UpdateGroupProxyPriorityPolicies(context.Context, GroupProxyPriorityUpdateInput) (*GroupProxyPriorityUpdateResult, error)
}

type ProxyGroupManagementService interface {
	ListProxyGroups(context.Context) ([]ProxyGroup, error)
	SaveProxyGroup(context.Context, int64, ProxyGroupInput) (*ProxyGroup, error)
	DeleteProxyGroup(context.Context, int64) error
	SetProxyGroupMembers(context.Context, ProxyGroupMembersInput) (int, error)
	ListGroupProxyPriorityPolicies(context.Context) ([]GroupProxyPriorityPolicy, error)
	UpdateGroupProxyPriorityPolicies(context.Context, GroupProxyPriorityUpdateInput) (*GroupProxyPriorityUpdateResult, error)
	AssignAccountProxyGroup(context.Context, AccountProxyGroupAssignmentInput) (*AccountProxyAssignmentResult, error)
}

type proxyGroupFilterKey struct{}

func WithProxyGroupFilter(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, proxyGroupFilterKey{}, id)
}
func ProxyGroupFilterFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(proxyGroupFilterKey{}).(int64)
	return id, ok
}

func ValidateProxyGroupInput(input *ProxyGroupInput) error {
	input.Name, input.Description = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 100 || utf8.RuneCountInString(input.Description) > 1000 {
		return infraerrors.BadRequest("PROXY_GROUP_INVALID", "IP分组名称须为1至100字，说明最多1000字")
	}
	return nil
}

func validateProxyGroupIDs(ids []int64, maximum int) error {
	if len(ids) < 1 || len(ids) > maximum {
		return infraerrors.BadRequest("PROXY_GROUP_SELECTION_INVALID", fmt.Sprintf("请选择1至%d项", maximum))
	}
	for _, id := range ids {
		if id <= 0 {
			return infraerrors.BadRequest("PROXY_GROUP_SELECTION_INVALID", "选择的编号无效")
		}
	}
	return nil
}

func (s *adminServiceImpl) ListProxyGroups(ctx context.Context) ([]ProxyGroup, error) {
	repo, ok := s.proxyRepo.(ProxyGroupRepository)
	if !ok {
		return nil, fmt.Errorf("proxy groups unavailable")
	}
	return repo.ListProxyGroups(ctx)
}
func (s *adminServiceImpl) SaveProxyGroup(ctx context.Context, id int64, input ProxyGroupInput) (*ProxyGroup, error) {
	if err := ValidateProxyGroupInput(&input); err != nil {
		return nil, err
	}
	repo, ok := s.proxyRepo.(ProxyGroupRepository)
	if !ok {
		return nil, fmt.Errorf("proxy groups unavailable")
	}
	return repo.SaveProxyGroup(ctx, id, input)
}
func (s *adminServiceImpl) DeleteProxyGroup(ctx context.Context, id int64) error {
	repo, ok := s.proxyRepo.(ProxyGroupRepository)
	if !ok {
		return fmt.Errorf("proxy groups unavailable")
	}
	return repo.DeleteProxyGroup(ctx, id)
}
func (s *adminServiceImpl) SetProxyGroupMembers(ctx context.Context, input ProxyGroupMembersInput) (int, error) {
	if err := validateProxyGroupIDs(input.ProxyIDs, 5000); err != nil {
		return 0, err
	}
	if input.ProxyGroupID != nil && *input.ProxyGroupID <= 0 {
		return 0, infraerrors.BadRequest("PROXY_GROUP_INVALID", "IP分组编号无效")
	}
	repo, ok := s.proxyRepo.(ProxyGroupRepository)
	if !ok {
		return 0, fmt.Errorf("proxy groups unavailable")
	}
	return repo.SetProxyGroupMembers(ctx, input)
}
func (s *adminServiceImpl) ListGroupProxyPriorityPolicies(ctx context.Context) ([]GroupProxyPriorityPolicy, error) {
	repo, ok := s.groupRepo.(GroupProxyPolicyRepository)
	if !ok {
		return nil, fmt.Errorf("proxy group policies unavailable")
	}
	return repo.ListGroupProxyPriorityPolicies(ctx)
}
func (s *adminServiceImpl) UpdateGroupProxyPriorityPolicies(ctx context.Context, input GroupProxyPriorityUpdateInput) (*GroupProxyPriorityUpdateResult, error) {
	if err := validateProxyGroupIDs(input.GroupIDs, 500); err != nil {
		return nil, err
	}
	if input.ProxyGroupID != nil && *input.ProxyGroupID <= 0 {
		return nil, infraerrors.BadRequest("PROXY_GROUP_INVALID", "IP分组编号无效")
	}
	if input.AssignProxies && (input.ProxyGroupID == nil || input.ProxyConcurrency < 1 || input.ProxyConcurrency > 10000) {
		return nil, infraerrors.BadRequest("PROXY_GROUP_ASSIGNMENT_INVALID", "请选择IP分组，每IP并发须为1至10000")
	}
	repo, ok := s.accountRepo.(AccountProxyGroupRepository)
	if !ok {
		return nil, fmt.Errorf("proxy group assignment unavailable")
	}
	return repo.UpdateGroupProxyPriorityPolicies(ctx, input)
}
func (s *adminServiceImpl) AssignAccountProxyGroup(ctx context.Context, input AccountProxyGroupAssignmentInput) (*AccountProxyAssignmentResult, error) {
	if err := validateProxyGroupIDs(input.AccountIDs, 5000); err != nil {
		return nil, err
	}
	if input.ProxyGroupID <= 0 || input.ProxyConcurrency < 1 || input.ProxyConcurrency > 10000 {
		return nil, infraerrors.BadRequest("PROXY_GROUP_ASSIGNMENT_INVALID", "请选择IP分组，每IP并发须为1至10000")
	}
	repo, ok := s.accountRepo.(AccountProxyGroupRepository)
	if !ok {
		return nil, fmt.Errorf("proxy group assignment unavailable")
	}
	return repo.AssignAccountProxyGroup(ctx, input)
}

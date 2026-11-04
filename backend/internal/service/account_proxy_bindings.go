package service

import (
	"context"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// AccountProxyBinding describes an account's effective egress. Limits are global
// per proxy, never multiplied by the number of accounts using that proxy.
type AccountProxyBinding struct {
	ProxyGroupID   int64
	ProxyID        int64
	Proxy          *Proxy
	MaxConcurrency int
	MaxAccounts    int
}

type AccountProxyBindingInput struct {
	ProxyID        int64 `json:"proxy_id"`
	MaxConcurrency int   `json:"max_concurrency"`
	MaxAccounts    int   `json:"max_accounts"`
}

type AccountProxyBindingReader interface {
	GetAccountProxyBindings(context.Context, int64) ([]AccountProxyBinding, error)
}

type AccountProxyBindingRepository interface {
	AccountProxyBindingReader
	ReplaceAccountProxyBindings(context.Context, []int64, []AccountProxyBindingInput) error
	AssignAccountProxies(context.Context, AccountProxyAssignmentInput) (*AccountProxyAssignmentResult, error)
}

type AccountProxyAssignmentInput struct {
	AccountIDs          []int64 `json:"account_ids"`
	ProxiesPerAccount   int     `json:"proxies_per_account"`
	MaxAccountsPerProxy int     `json:"max_accounts_per_proxy"`
	ProxyConcurrency    int     `json:"proxy_concurrency"`
}

type AccountProxyAssignmentResult struct {
	AccountCount int `json:"account_count"`
	BindingCount int `json:"binding_count"`
	ProxyCount   int `json:"proxy_count"`
}

// Dedicated capability keeps existing AdminService test doubles compatible.
type AccountProxyAssignmentService interface {
	AssignAccountProxies(context.Context, AccountProxyAssignmentInput) (*AccountProxyAssignmentResult, error)
}

func ValidateAccountProxyBindings(items []AccountProxyBindingInput) error {
	if len(items) > 100 {
		return infraerrors.BadRequest("PROXY_BINDINGS_LIMIT", "每个账号最多绑定100个代理")
	}
	seen := map[int64]bool{}
	for _, item := range items {
		if item.ProxyID <= 0 || seen[item.ProxyID] {
			return infraerrors.BadRequest("PROXY_BINDINGS_INVALID", "代理编号无效或重复")
		}
		if item.MaxConcurrency < 0 || item.MaxConcurrency > 10000 || item.MaxAccounts < 0 || item.MaxAccounts > 10000 {
			return infraerrors.BadRequest("PROXY_LIMIT_INVALID", "代理并发和绑定上限须为0至10000，0表示不限")
		}
		seen[item.ProxyID] = true
	}
	return nil
}

func (s *adminServiceImpl) updateAccountWithProxyBindings(ctx context.Context, id int64, input *UpdateAccountInput) (*Account, error) {
	if err := ValidateAccountProxyBindings(*input.ProxyBindings); err != nil {
		return nil, err
	}
	repo, ok := s.accountRepo.(AccountProxyBindingRepository)
	if !ok || s.entClient == nil {
		return nil, fmt.Errorf("account proxy bindings unavailable")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	if err = repo.ReplaceAccountProxyBindings(txctx, []int64{id}, *input.ProxyBindings); err != nil {
		return nil, err
	}
	copyInput := *input
	copyInput.ProxyBindings, copyInput.ProxyID = nil, nil
	if _, err = s.UpdateAccount(txctx, id, &copyInput); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.accountRepo.GetByID(ctx, id)
}

func (s *adminServiceImpl) bulkUpdateAccountsWithProxyBindings(ctx context.Context, input *BulkUpdateAccountsInput) (*BulkUpdateAccountsResult, error) {
	if err := ValidateAccountProxyBindings(*input.ProxyBindings); err != nil {
		return nil, err
	}
	repo, ok := s.accountRepo.(AccountProxyBindingRepository)
	if !ok || s.entClient == nil {
		return nil, fmt.Errorf("account proxy bindings unavailable")
	}
	ids := input.AccountIDs
	if len(ids) == 0 && input.Filters != nil {
		var err error
		ids, err = s.resolveBulkUpdateTargetIDs(ctx, input.Filters)
		if err != nil {
			return nil, err
		}
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	if err = repo.ReplaceAccountProxyBindings(txctx, ids, *input.ProxyBindings); err != nil {
		return nil, err
	}
	copyInput := *input
	copyInput.AccountIDs, copyInput.Filters = ids, nil
	copyInput.ProxyBindings, copyInput.ProxyID = nil, nil
	result, err := s.BulkUpdateAccounts(txctx, &copyInput)
	if err != nil {
		return nil, err
	}
	if len(result.FailedIDs) > 0 {
		return nil, infraerrors.BadRequest("PROXY_BATCH_ABORTED", "账号批量修改失败，代理分配未保存")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *adminServiceImpl) AssignAccountProxies(ctx context.Context, input AccountProxyAssignmentInput) (*AccountProxyAssignmentResult, error) {
	if len(input.AccountIDs) < 1 || len(input.AccountIDs) > 5000 || input.ProxiesPerAccount < 1 || input.ProxiesPerAccount > 100 || input.MaxAccountsPerProxy < 1 || input.MaxAccountsPerProxy > 10000 || input.ProxyConcurrency < 1 || input.ProxyConcurrency > 10000 {
		return nil, infraerrors.BadRequest("PROXY_ASSIGNMENT_INVALID", "请选择1至5000个账号；每账号代理数为1至100，代理绑定上限和并发为1至10000")
	}
	repo, ok := s.accountRepo.(AccountProxyBindingRepository)
	if !ok {
		return nil, fmt.Errorf("account proxy assignment unavailable")
	}
	return repo.AssignAccountProxies(ctx, input)
}

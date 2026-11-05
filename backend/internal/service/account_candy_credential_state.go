package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const AccountCandyCredentialErrorMessage = "糖果测试认证失败（HTTP 401），账号凭据无效，请重新授权；已暂停调度。"

// AccountCandyCredentialStateSnapshot is private request evidence. Credentials
// are compared inside the database, never persisted to results or logged.
type AccountCandyCredentialStateSnapshot struct {
	AccountID             int64
	AccountType           string
	AccountStatus         string
	AccountSchedulable    bool
	AccountParentID       *int64
	CredentialAccountID   int64
	CredentialsJSON       string
	CredentialType        string
	CredentialStatus      string
	CredentialSchedulable bool
	CredentialProxyID     *int64
}

type accountCandyCredentialStateRepository interface {
	SetAccountCandyCredentialErrorIfMatch(context.Context, AccountCandyCredentialStateSnapshot) (bool, error)
}

func accountCandyCredentialSnapshot(account, owner *Account) *AccountCandyCredentialStateSnapshot {
	if account == nil || owner == nil || account.ID <= 0 || owner.ID <= 0 ||
		account.Platform != PlatformOpenAI || owner.Platform != PlatformOpenAI ||
		account.Status != StatusActive || owner.Status != StatusActive || owner.ParentAccountID != nil {
		return nil
	}
	if account.ParentAccountID == nil && account.ID != owner.ID ||
		account.ParentAccountID != nil && (*account.ParentAccountID != owner.ID || !owner.IsOpenAIOAuth()) {
		return nil
	}
	credentials := owner.Credentials
	if credentials == nil {
		credentials = map[string]any{}
	}
	raw, err := json.Marshal(credentials)
	if err != nil {
		return nil
	}
	cloneID := func(id *int64) *int64 {
		if id == nil {
			return nil
		}
		value := *id
		return &value
	}
	return &AccountCandyCredentialStateSnapshot{
		AccountID: account.ID, AccountType: account.Type, AccountStatus: account.Status,
		AccountSchedulable: account.Schedulable, AccountParentID: cloneID(account.ParentAccountID),
		CredentialAccountID: owner.ID, CredentialsJSON: string(raw), CredentialType: owner.Type,
		CredentialStatus: owner.Status, CredentialSchedulable: owner.Schedulable,
		// The credential owner is the original saved account. account.ProxyID
		// may instead be a temporary retry route and must never be used for CAS.
		CredentialProxyID: cloneID(owner.ProxyID),
	}
}

func (s *AccountTestService) accountCandyCredentialFailure(ctx context.Context, snapshot *AccountCandyCredentialStateSnapshot, owner *Account, callErr error) error {
	var failure *accountCandyProbeError
	if !errors.As(callErr, &failure) || failure.httpStatus != http.StatusUnauthorized {
		return callErr
	}
	result := *failure
	appendMessage := func(message string) error {
		result.message += " " + message
		return &result
	}
	if snapshot == nil {
		return appendMessage("账号身份或状态不适用，未自动更改账号；请检查账号状态。")
	}
	repo, ok := s.accountRepo.(accountCandyCredentialStateRepository)
	if !ok {
		return appendMessage("账号状态更新不可用，未确认自动停调；请检查账号状态。")
	}
	// Once a real 401 arrived, request cancellation must not discard the
	// credential failure. A bounded independent write still checks the snapshot.
	mutationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	updated, err := applyAccountCandyCredentialState(mutationCtx, repo, *snapshot)
	if err != nil {
		return appendMessage("账号状态写入失败，未确认自动停调；请检查账号状态。")
	}
	if !updated {
		return appendMessage("账号凭据、关联或状态已变化，保留当前账号状态。")
	}
	cacheFailed := false
	if s.candyTokenCacheInvalidator != nil {
		if strict, ok := s.candyTokenCacheInvalidator.(interface {
			InvalidateTokenStrict(context.Context, *Account) error
		}); ok {
			cacheFailed = strict.InvalidateTokenStrict(mutationCtx, owner) != nil
		} else {
			cacheFailed = s.candyTokenCacheInvalidator.InvalidateToken(mutationCtx, owner) != nil
		}
	}
	message := "已将账号标为错误并暂停调度，请重新授权。"
	if snapshot.AccountID != snapshot.CredentialAccountID {
		message = "已将凭据母账号标为错误并暂停调度，关联影子账号停止使用该凭据，请重新授权。"
	}
	if cacheFailed {
		message += " 凭据缓存清理暂未完成。"
	}
	return appendMessage(message)
}

func applyAccountCandyCredentialState(ctx context.Context, repo accountCandyCredentialStateRepository, snapshot AccountCandyCredentialStateSnapshot) (bool, error) {
	for attempt := 0; ; attempt++ {
		updated, err := repo.SetAccountCandyCredentialErrorIfMatch(ctx, snapshot)
		var state interface{ SQLState() string }
		// PostgreSQL 40P01 guarantees this statement's transaction rolled back.
		// Never retry a connection failure or another ambiguous commit outcome.
		if err == nil || attempt == 2 || !errors.As(err, &state) || state.SQLState() != "40P01" {
			return updated, err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}

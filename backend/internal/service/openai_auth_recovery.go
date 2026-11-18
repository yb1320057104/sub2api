package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

const OpenAIAuthRecoveryKey = "openai_auth_recovery"

// OpenAIAuthRecoveryState exposes only recovery belonging to current credentials.
// Historical token digests and rejection generations remain internal.
type OpenAIAuthRecoveryState struct {
	Pending         bool `json:"pending"`
	RequiresRelogin bool `json:"requires_relogin"`
}

func OpenAIAuthRecoveryStateFromAccount(account *Account) *OpenAIAuthRecoveryState {
	if !OpenAIAuthRecoveryPending(account) {
		return nil
	}
	return &OpenAIAuthRecoveryState{Pending: true, RequiresRelogin: OpenAIAuthNeedsRelogin(account)}
}

type openAIUsedAccessTokenKey struct{}
type openAIUsedAccessToken struct {
	accountID int64
	token     string
}

// Keep the actual token used by this attempt, even when GetAccessToken refreshed
// a separate durable snapshot after the scheduler selected the account.
func withOpenAIUsedAccessToken(ctx context.Context, account *Account, token string) context.Context {
	if account == nil || account.Platform != PlatformOpenAI {
		return ctx
	}
	id := account.ID
	if account.IsCredentialShadow() && account.ParentAccountID != nil {
		id = *account.ParentAccountID
	}
	return context.WithValue(ctx, openAIUsedAccessTokenKey{}, openAIUsedAccessToken{accountID: id, token: token})
}

// The signal belongs to the rejected access token, not an account snapshot.
// Refreshing or reauthorizing the account makes old rejection signals inert.
type OpenAIAuthRecoveryRepository interface {
	MarkOpenAIAuthRejected(context.Context, int64, string, bool, time.Time) (bool, error)
	RecoverOpenAIAuthIfUnchanged(context.Context, int64, string) (bool, error)
}

// OpenAICredentialLifecycleRepository keeps provider results and their recovery
// effects under the same durable credential comparison. Probe recovery also
// compares the exact rejection generation observed before the probe started.
type OpenAICredentialLifecycleRepository interface {
	ApplyOpenAIRefresh(context.Context, int64, map[string]any, *int64, map[string]any) (bool, error)
	MarkOpenAIRefreshFailure(context.Context, int64, map[string]any, *int64, bool, time.Time, string) (bool, error)
	RecoverVerifiedOpenAIAuth(context.Context, int64, map[string]any, string) (bool, error)
}

func OpenAIAuthRecoveryGeneration(account *Account) string {
	if account == nil {
		return ""
	}
	state, _ := account.Extra[OpenAIAuthRecoveryKey].(map[string]any)
	id, _ := state["generation"].(string)
	return id
}

func OpenAIAccessTokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func OpenAIAuthRecoveryPending(account *Account) bool {
	if account == nil || !isOpenAIOAuthAccount(account) || account.IsCredentialShadow() || account.IsOpenAIPersonalAccessToken() || account.IsOpenAIAgentIdentity() || account.IsExcelOAuth() {
		return false
	}
	state, _ := account.Extra[OpenAIAuthRecoveryKey].(map[string]any)
	digest, _ := state["access_token_digest"].(string)
	if digest == "" {
		return false
	}
	if token := account.GetOpenAIAccessToken(); token != "" {
		return digest == OpenAIAccessTokenDigest(token)
	}
	projected, _ := account.Credentials["_openai_access_token_digest"].(string)
	return projected != "" && digest == projected
}

func OpenAIAuthNeedsRelogin(account *Account) bool {
	if !OpenAIAuthRecoveryPending(account) {
		return false
	}
	state, _ := account.Extra[OpenAIAuthRecoveryKey].(map[string]any)
	terminal, _ := state["requires_relogin"].(bool)
	return terminal || strings.TrimSpace(account.GetOpenAIRefreshToken()) == ""
}

func markOpenAIAuthRejected(ctx context.Context, repo AccountRepository, account *Account, terminal bool, cooldown ...time.Time) (bool, error) {
	if account == nil || !isOpenAIOAuthAccount(account) || account.IsCredentialShadow() || account.IsOpenAIPersonalAccessToken() || account.IsOpenAIAgentIdentity() || account.IsExcelOAuth() {
		return false, nil
	}
	owner, ok := repo.(OpenAIAuthRecoveryRepository)
	if !ok {
		return false, nil
	}
	token := account.GetOpenAIAccessToken()
	if used, ok := ctx.Value(openAIUsedAccessTokenKey{}).(openAIUsedAccessToken); ok && used.accountID == account.ID {
		token = used.token
	}
	until := time.Now().Add(10 * time.Minute)
	if len(cooldown) > 0 {
		until = cooldown[0]
	}
	return owner.MarkOpenAIAuthRejected(ctx, account.ID, token, terminal, until)
}

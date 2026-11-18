package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

const KiloAuthenticatedModelScope = "kilo_bound_chatgpt_models_v1"

// Frozen from kilo2api v0.5.2 (3f72c8a8). This is only the channel's
// compatibility ceiling; eligibility still comes from the bound GPT account.
var kiloChatGPTChannelModels = map[string]bool{
	"openai/gpt-5.6-sol": true, "openai/gpt-5.6-terra": true,
	"openai/gpt-6-astra": true, "openai/gpt-6.1-sol": true,
	"openai/gpt-6.1-terra": true, "openai/gpt-6-luna": true,
	"openai/gpt-5.6-luna": true, "openai/gpt-6.2-sol": true,
	"openai/gpt-6.2-astra": true,
}

// KiloSubscriptionModelReader verifies the actual bound OAuth account without
// generating a response or treating a cached public/BYOK list as entitlement.
type KiloSubscriptionModelReader interface {
	FetchOpenAIModelsListFresh(context.Context, *Account) (*OpenAIModelsResponse, error)
}

// KiloModelCatalogReader reads the same authenticated, space-specific catalog
// used by Kilo's client BYOK badge. The public catalog is not an entitlement.
type KiloModelCatalogReader interface {
	Models(context.Context, string, string, string) ([]string, error)
}

// KiloModelScopeRepository commits mapping and its scope proof together. A
// rotated Key, edited source, or changed pair cannot publish stale discovery.
type KiloModelScopeRepository interface {
	UpdateKiloModelScopeIfUnchanged(context.Context, *Account, *Account, []string, time.Time) (bool, error)
}

type KiloModelScopeResult struct {
	AccountID      int64     `json:"account_id"`
	Models         []string  `json:"models"`
	Source         string    `json:"source"`
	OrganizationID string    `json:"organization_id"`
	VerifiedAt     time.Time `json:"verified_at"`
	Applied        bool      `json:"applied"`
}

// KiloSubscriptionSourceIdentity binds a scope to the same normalized GPT
// identity used by onboarding and the final send guard.
func KiloSubscriptionSourceIdentity(source *Account) string { return kiloSourceIdentity(source) }

func kiloModelKeyFingerprint(key string) string {
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])
}

// Optional administrator selection narrows the verified intersection. Empty
// selects that intersection, never the generic gateway BYOK or free catalog.
func selectKiloCatalogModels(requested, available []string) ([]string, error) {
	if _, err := kiloOnboardingModelMapping(available); err != nil {
		return nil, kiloBad("KILO_MODEL_CATALOG_EMPTY", "绑定GPT实际模型与Kilo订阅通道没有共同可用模型，未开放通用或免费目录")
	}
	allowed := make(map[string]bool, len(available))
	for _, model := range available {
		allowed[model] = true
	}
	selected := requested
	if len(selected) == 0 {
		selected = available
	}
	if _, err := kiloOnboardingModelMapping(selected); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(selected))
	for _, model := range selected {
		if !allowed[model] {
			return nil, kiloBad("KILO_MODEL_NOT_AVAILABLE", "所选模型不在绑定GPT与Kilo订阅通道的共同范围，请刷新订阅模型")
		}
		out = append(out, model)
	}
	sort.Strings(out)
	return out, nil
}

func kiloCatalogScopeExtra(account *Account, models []string, observed time.Time, source ...*Account) map[string]any {
	extra := map[string]any{
		"kilo_subscription_models":             append([]string(nil), models...),
		"kilo_subscription_scope_source":       KiloAuthenticatedModelScope,
		"kilo_subscription_scope_verified_at":  observed.UTC().Format(time.RFC3339),
		"kilo_subscription_scope_key":          kiloModelKeyFingerprint(account.GetCredential("api_key")),
		"kilo_subscription_scope_organization": account.GetCredential(KiloGatewayOrganizationIDCredential),
	}
	if len(source) > 0 && source[0] != nil {
		extra["kilo_subscription_scope_source_identity"] = kiloSourceIdentity(source[0])
	}
	return extra
}

func (s *KiloService) discoverKiloModels(ctx context.Context, source *Account, key, organization, proxy string, requested []string) ([]string, error) {
	if source == nil || !source.IsOpenAIOAuth() || !source.IsActive() || OpenAIAuthRecoveryPending(source) || strings.TrimSpace(source.GetCredential("chatgpt_account_id")) == "" {
		return nil, kiloBad("KILO_GPT_AUTH_UNAVAILABLE", "绑定GPT授权或工作区不可用，请先恢复来源授权")
	}
	if s.modelSource == nil {
		return nil, kiloBad("KILO_MODEL_DISCOVERY_UNAVAILABLE", "绑定GPT的官方模型目录暂不可用，未使用Kilo通用或免费目录代替")
	}
	manifest, err := s.modelSource.FetchOpenAIModelsListFresh(ctx, source)
	if err != nil {
		return nil, err
	}
	if manifest == nil || manifest.NotModified {
		return nil, kiloRemoteShapeError()
	}
	var official struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(manifest.Body, &official) != nil || len(official.Data) == 0 {
		return nil, kiloRemoteShapeError()
	}
	eligible := make(map[string]bool, len(official.Data))
	for _, model := range official.Data {
		id := canonicalKiloSubscriptionModel(model.ID)
		if kiloChatGPTChannelModels[id] {
			eligible[id] = true
		}
	}
	reader, ok := s.remote.(KiloModelCatalogReader)
	if !ok {
		return nil, kiloBad("KILO_MODEL_DISCOVERY_UNAVAILABLE", "Kilo认证模型目录读取暂不可用，不能用公开总目录代替")
	}
	available, err := reader.Models(ctx, key, organization, proxy)
	if err != nil {
		return nil, err
	}
	intersection := make([]string, 0, len(eligible))
	for _, model := range available {
		if eligible[model] {
			intersection = append(intersection, model)
		}
	}
	return selectKiloCatalogModels(requested, intersection)
}

// RefreshModels updates the Key's logical account without relogin or inference.
// Its reciprocal GPT manifest establishes eligibility. The generic BYOK badge
// is only a second availability filter, never evidence of subscription billing.
func (s *KiloService) RefreshModels(ctx context.Context, preparedID int64) (*KiloModelScopeResult, error) {
	operation, unlock, err := s.lockPreparedOperation(ctx, preparedID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	ctx = operation
	accountID, err := s.preparedAccountID(ctx, preparedID)
	if err != nil {
		return nil, err
	}
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !account.IsKiloGatewayAccount() || int64(ParseExtraInt(account.Extra["kilo_prepared_id"])) != preparedID {
		return nil, kiloConflict("Kilo网关归属已改变，请刷新账号")
	}
	source, err := resolveKiloQuotaSource(ctx, s.accounts, account)
	if err != nil {
		return nil, err
	}
	route, err := s.acquireLoginRoute(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	defer route.Close()
	credential, organization, err := s.preparedCredential(route.Context, preparedID)
	if err != nil {
		return nil, err
	}
	if credential != account.GetCredential("api_key") || organization != account.GetCredential(KiloGatewayOrganizationIDCredential) {
		return nil, kiloConflict("Kilo Key或空间已改变，请刷新账号")
	}
	connection, err := s.remote.Connection(route.Context, credential, organization, route.URL)
	if err != nil {
		return nil, err
	}
	if connection == nil || !connection.Connected || !strings.EqualFold(strings.TrimSpace(connection.Email), kiloSourceEmail(source)) {
		return nil, kiloBad("BYOK_NOT_CONNECTED", "当前Kilo空间的ChatGPT连接与绑定身份不一致，保留原模型范围")
	}
	models, err := s.discoverKiloModels(route.Context, source, credential, organization, route.URL, nil)
	if err != nil {
		return nil, err
	}
	store, ok := s.accounts.(KiloModelScopeRepository)
	if !ok {
		return nil, kiloBad("KILO_MODEL_DISCOVERY_UNAVAILABLE", "模型范围保存暂不可用")
	}
	now := time.Now().UTC()
	applied, err := store.UpdateKiloModelScopeIfUnchanged(ctx, account, source, models, now)
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, kiloConflict("模型读取期间Key、配对或来源身份已改变，请重新刷新")
	}
	return &KiloModelScopeResult{AccountID: account.ID, Models: models, Source: KiloAuthenticatedModelScope, OrganizationID: organization, VerifiedAt: now, Applied: true}, nil
}

func parseKiloAuthenticatedCatalog(body []byte) ([]string, error) {
	var catalog struct {
		Data []struct {
			ID      string `json:"id"`
			HasBYOK *bool  `json:"hasUserByokAvailable"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &catalog) != nil || len(catalog.Data) == 0 {
		return nil, kiloRemoteShapeError()
	}
	models := make([]string, 0)
	seen := make(map[string]bool)
	for _, model := range catalog.Data {
		// Kilo's unversioned ~ aliases are not exact grants. Non-OpenAI BYOK
		// providers also cannot consume this connection's ChatGPT allowance.
		if !strings.HasPrefix(model.ID, "openai/") || model.HasBYOK == nil || !*model.HasBYOK {
			continue
		}
		if _, err := kiloOnboardingModelMapping([]string{model.ID}); err != nil || seen[model.ID] {
			return nil, kiloRemoteShapeError()
		}
		seen[model.ID] = true
		models = append(models, model.ID)
	}
	if len(models) == 0 {
		return nil, kiloBad("KILO_MODEL_CATALOG_EMPTY", "当前Kilo空间没有标记为BYOK可用的OpenAI模型，保留原范围")
	}
	sort.Strings(models)
	return models, nil
}

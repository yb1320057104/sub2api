package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
)

// KiloInferenceAccountReader reads current routing credentials at the actual
// send boundary. It deliberately does not share the scheduler's admission cache.
type KiloInferenceAccountReader interface {
	GetKiloInferenceAccount(context.Context, int64) (*Account, error)
}

// KiloGatewaySourceReader resolves the persisted reciprocal pairing, never an
// email match or an arbitrary OAuth account from the gateway's group.
type KiloGatewaySourceReader interface {
	GetKiloSourceForGateway(context.Context, int64, int64) (*Account, error)
}

func kiloInferenceDenied(code, message string) error {
	return infraerrors.New(http.StatusConflict, code, message)
}

// KiloInferencePolicyError returns only safe fixed policy messages for API
// responses. Callers must not flatten these failures into an opaque retryable 502.
func KiloInferencePolicyError(err error) (code, message string, ok bool) {
	if err == nil {
		return "", "", false
	}
	app := infraerrors.FromError(err)
	if app == nil || !strings.HasPrefix(app.Reason, "KILO_") {
		return "", "", false
	}
	return app.Reason, app.Message, true
}

// A partial or malformed marker must not restore direct OAuth inference.
func kiloBindingMarker(extra map[string]any, key string) bool {
	value, exists := extra[key]
	if !exists {
		return false
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	return text != "" && text != "0" && text != "0.0" && text != "false"
}

// IsKiloQuotaOnly applies as soon as onboarding is submitted, before a key or
// reciprocal gateway exists. Pair markers preserve the same rule for older rows.
func (a *Account) IsKiloQuotaOnly() bool {
	return a != nil && (kiloBindingMarker(a.Extra, "kilo_quota_only") || kiloBindingMarker(a.Extra, "kilo_source_prepared_id") || kiloBindingMarker(a.Extra, "kilo_source_gateway_id"))
}

func (a *Account) IsKiloSourceAccount() bool { return a.IsKiloQuotaOnly() }

// IsKiloGatewayAccount also recognizes damaged managed rows. Removing just the
// bridge toggle or changing the platform cannot opt a managed account out.
func (a *Account) IsKiloGatewayAccount() bool {
	return a != nil && (a.Extra[KiloManagedExtraKey] == true || kiloBindingMarker(a.Extra, "kilo_prepared_id") || kiloBindingMarker(a.Extra, "kilo_onboarding_source_id"))
}

func (a *Account) RequiresKiloInference() bool {
	return a.IsKiloSourceAccount() || a.IsKiloGatewayAccount()
}

// New onboarding carries a scope proof. Its absence is only compatible with
// older manually prepared gateways that never had an onboarding/scope marker.
func (a *Account) hasKiloSubscriptionScope() bool {
	if a == nil || !a.IsKiloGatewayAccount() {
		return false
	}
	_, scopeExists := a.Extra["kilo_subscription_models"]
	_, proofExists := a.Extra["kilo_subscription_scope_source"]
	return scopeExists || proofExists || kiloBindingMarker(a.Extra, "kilo_onboarding_source_id") || kiloBindingMarker(a.Extra, "kilo_onboarding_job_id")
}

func canonicalKiloSubscriptionModel(model string) string {
	model = strings.TrimSpace(model)
	if !strings.HasPrefix(model, "openai/") {
		model = "openai/" + model
	}
	return model
}

func (a *Account) kiloSubscriptionModelSet() (map[string]bool, error) {
	if !a.hasKiloSubscriptionScope() {
		return nil, nil
	}
	scopeSource, _ := a.Extra["kilo_subscription_scope_source"].(string)
	if scopeSource != "administrator_verified_exact_models" && scopeSource != KiloAuthenticatedModelScope {
		return nil, kiloInferenceDenied("KILO_MODEL_SCOPE_UNVERIFIED", "Kilo 订阅模型范围尚未核实，禁止生成或自动扩展模型")
	}
	if scopeSource == KiloAuthenticatedModelScope && (a.Extra["kilo_subscription_scope_key"] != kiloModelKeyFingerprint(a.GetCredential("api_key")) || a.Extra["kilo_subscription_scope_organization"] != a.GetCredential(KiloGatewayOrganizationIDCredential)) {
		return nil, kiloInferenceDenied("KILO_MODEL_SCOPE_UNVERIFIED", "Kilo Key或空间已改变，请重新读取认证模型范围")
	}
	verified, _ := a.Extra["kilo_subscription_scope_verified_at"].(string)
	if _, err := time.Parse(time.RFC3339, verified); err != nil {
		return nil, kiloInferenceDenied("KILO_MODEL_SCOPE_UNVERIFIED", "Kilo 订阅模型范围缺少有效核验记录")
	}
	var models []string
	switch raw := a.Extra["kilo_subscription_models"].(type) {
	case []string:
		models = raw
	case []any:
		for _, value := range raw {
			model, ok := value.(string)
			if !ok {
				return nil, kiloInferenceDenied("KILO_MODEL_SCOPE_INVALID", "Kilo 订阅模型范围格式无效")
			}
			models = append(models, model)
		}
	}
	if len(models) == 0 || len(models) > 200 {
		return nil, kiloInferenceDenied("KILO_MODEL_SCOPE_EMPTY", "Kilo 订阅模型范围为空，禁止以空映射或透传放行全部模型")
	}
	result := make(map[string]bool, len(models))
	for _, model := range models {
		if model != strings.TrimSpace(model) || !strings.HasPrefix(model, "openai/") || len(model) <= len("openai/") || len(model) > 256 || strings.ContainsAny(model, "*?[]\\") || strings.ContainsFunc(model, unicode.IsSpace) || strings.ContainsFunc(model, unicode.IsControl) || result[model] {
			return nil, kiloInferenceDenied("KILO_MODEL_SCOPE_INVALID", "Kilo 订阅模型范围必须是已核实、无重复的精确 openai/ 模型编号")
		}
		result[model] = true
	}
	return result, nil
}

func (a *Account) kiloSubscriptionAllowsWireModel(model string) bool {
	if !a.hasKiloSubscriptionScope() {
		return true
	}
	allowed, err := a.kiloSubscriptionModelSet()
	return err == nil && allowed[canonicalKiloSubscriptionModel(model)]
}

func (a *Account) kiloSubscriptionAllowsRequestModel(model string) bool {
	if !a.hasKiloSubscriptionScope() {
		return true
	}
	if !a.kiloSubscriptionAllowsWireModel(model) {
		return false
	}
	canonical := canonicalKiloSubscriptionModel(model)
	for source, target := range rawUpstreamModelMapping(a) {
		if source == canonical || source == strings.TrimPrefix(canonical, "openai/") {
			return a.kiloSubscriptionAllowsWireModel(target)
		}
	}
	return false // Exact stored mappings are required even in passthrough mode.
}

func filterKiloSubscriptionCatalog(account *Account, models []string) []string {
	if !account.hasKiloSubscriptionScope() {
		return models
	}
	filtered := make([]string, 0, len(models))
	for _, model := range models {
		if account.kiloSubscriptionAllowsWireModel(model) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func filterKiloSubscriptionModelsBody(account *Account, body []byte, codex bool) ([]byte, error) {
	if !account.hasKiloSubscriptionScope() {
		return body, nil
	}
	if _, err := account.kiloSubscriptionModelSet(); err != nil {
		return nil, err
	}
	field, idField := "data", "id"
	if codex {
		field, idField = "models", "slug"
	}
	envelope, entries, err := modelCatalogEntries(body, field)
	if err != nil {
		return nil, err
	}
	filtered := make([]json.RawMessage, 0, len(entries))
	for _, raw := range entries {
		if account.kiloSubscriptionAllowsWireModel(gjson.GetBytes(raw, idField).String()) {
			filtered = append(filtered, raw)
		}
	}
	envelope[field], err = json.Marshal(filtered)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope)
}

// ValidateKiloInferenceAccount is inference-only. Official OAuth metadata and
// token refresh must keep using their own read/authentication services.
func ValidateKiloInferenceAccount(account *Account) error {
	if account.IsKiloSourceAccount() {
		return kiloInferenceDenied("KILO_SOURCE_INFERENCE_BLOCKED", "Kilo 来源 OAuth 仅用于官方账号信息和额度查询；生成请求必须使用已配对的 Kilo 网关")
	}
	if !account.IsKiloGatewayAccount() {
		return nil
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey || account.ParentAccountID != nil || !account.IsKiloBridgeEnabled() {
		return kiloInferenceDenied("KILO_INFERENCE_CONFIG_INVALID", "Kilo 生成请求必须使用 OpenAI API Key 类型的 Kilo 桥接网关；禁止使用官方 OAuth 凭据")
	}
	if account.Extra[KiloManagedExtraKey] == true || kiloBindingMarker(account.Extra, "kilo_prepared_id") || kiloBindingMarker(account.Extra, "kilo_onboarding_source_id") {
		if account.Extra[KiloManagedExtraKey] != true || account.Extra[KiloGatewayProfileExtra] != KiloGatewayCLIProfile || ((kiloBindingMarker(account.Extra, "kilo_prepared_id") || kiloBindingMarker(account.Extra, "kilo_onboarding_source_id")) && ParseExtraInt(account.Extra["kilo_prepared_id"]) <= 0) {
			return kiloInferenceDenied("KILO_INFERENCE_CONFIG_INVALID", "Kilo 托管网关身份不完整，禁止回退到官方推理")
		}
	}
	base, err := url.Parse(strings.TrimSpace(account.GetCredential("base_url")))
	allowedBase := false
	if err == nil && base != nil {
		allowedBase = base.Scheme == "https" && base.Host == "ai-gateway.kilo.ai" && strings.TrimRight(base.Path, "/") == "/api/v1" && base.RawPath == "" && base.RawQuery == "" && base.Fragment == "" && base.User == nil
	}
	if !allowedBase {
		return kiloInferenceDenied("KILO_INFERENCE_ENDPOINT_INVALID", "Kilo 生成请求只能使用 https://ai-gateway.kilo.ai/api/v1 网关；禁止官方直发或其他上游回退")
	}
	if _, err := account.kiloSubscriptionModelSet(); err != nil {
		return err
	}
	key := account.GetCredential("api_key")
	if strings.TrimSpace(key) != key || !kiloValidKey(key) || key == account.GetCredential("access_token") || key == account.GetCredential("refresh_token") || key == account.GetCredential("id_token") {
		return kiloInferenceDenied("KILO_INFERENCE_KEY_MISSING", "Kilo Gateway Key 不可用，生成请求已停止；不会使用官方 OAuth 回退")
	}
	return nil
}

func kiloReadOnlyRequest(request *http.Request) bool {
	// A WebSocket handshake is GET but can start official inference. It must
	// never inherit the metadata-read exception.
	return request != nil && !strings.EqualFold(strings.TrimSpace(request.Header.Get("Upgrade")), "websocket") && (request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions)
}

// ValidateKiloInferenceRequest checks the final wire URL and bearer after all
// arbitrary account/header overrides. Kilo keys are opaque: only the stored
// gateway api_key may authenticate this request, never a source OAuth bearer.
func ValidateKiloInferenceRequest(request *http.Request, account *Account) error {
	if kiloReadOnlyRequest(request) {
		return nil
	}
	if err := ValidateKiloInferenceAccount(account); err != nil {
		return err
	}
	if !account.IsKiloGatewayAccount() {
		return nil
	}
	if request == nil || request.URL == nil || request.Method != http.MethodPost {
		return kiloInferenceDenied("KILO_INFERENCE_ENDPOINT_INVALID", "Kilo 生成请求无效，禁止切换上游")
	}
	if request.URL.Host == "api.kilo.ai" && request.URL.Path == "/api/exa/search" {
		return ValidateKiloAlphaSearchRequest(request, account)
	}
	u := request.URL
	if u.Scheme != "https" || u.Host != "ai-gateway.kilo.ai" || (u.Path != "/api/v1/responses" && u.Path != "/api/v1/responses/compact") || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || (request.Host != "" && request.Host != "ai-gateway.kilo.ai") {
		return kiloInferenceDenied("KILO_INFERENCE_ENDPOINT_INVALID", "Kilo 生成请求只能发送到 Kilo Gateway Responses；禁止官方直发或静默回退")
	}
	var authorization []string
	for name, values := range request.Header {
		if strings.EqualFold(name, "Authorization") {
			authorization = append(authorization, values...)
		}
	}
	if len(authorization) != 1 || authorization[0] != "Bearer "+account.GetCredential("api_key") {
		return kiloInferenceDenied("KILO_INFERENCE_AUTH_INVALID", "Kilo 生成请求必须使用本网关保存的 Gateway Key；禁止来源 OAuth 或其他凭据回退")
	}
	if account.hasKiloSubscriptionScope() {
		if request.GetBody == nil {
			return kiloInferenceDenied("KILO_MODEL_SCOPE_UNVERIFIED", "无法核对 Kilo 生成载荷的实际模型，生成已停止")
		}
		body, err := request.GetBody()
		if err != nil {
			return kiloInferenceDenied("KILO_MODEL_SCOPE_UNVERIFIED", "无法读取 Kilo 生成载荷的实际模型")
		}
		wire, readErr := io.ReadAll(io.LimitReader(body, 64<<20+1))
		_ = body.Close()
		if readErr != nil || len(wire) > 64<<20 || !gjson.ValidBytes(wire) || !account.kiloSubscriptionAllowsWireModel(gjson.GetBytes(wire, "model").String()) {
			return kiloInferenceDenied("KILO_MODEL_NOT_IN_SUBSCRIPTION", "请求模型不在已核实的 Kilo ChatGPT 订阅范围内，禁止切换其他供应商或官方回退")
		}
	}
	return nil
}

func kiloInferenceReader(repo AccountRepository) KiloInferenceAccountReader {
	reader, _ := repo.(KiloInferenceAccountReader)
	return reader
}

// GuardKiloInferenceRequest also catches a source bound after a test/harvest
// selected its snapshot, and shadows that still hold the parent's OAuth token.
func GuardKiloInferenceRequest(ctx context.Context, reader KiloInferenceAccountReader, request *http.Request, selected *Account, accountID int64) error {
	if kiloReadOnlyRequest(request) {
		return nil
	}
	if err := ValidateKiloInferenceRequest(request, selected); err != nil {
		return err
	}
	if reader == nil || accountID <= 0 {
		return nil
	}
	latest, err := reader.GetKiloInferenceAccount(ctx, accountID)
	if err != nil || latest == nil || latest.ID != accountID {
		return kiloInferenceDenied("KILO_INFERENCE_STATE_UNAVAILABLE", "无法核对当前账号的 Kilo 推理隔离状态，请稍后重试")
	}
	if selected.IsKiloGatewayAccount() && !latest.IsKiloGatewayAccount() {
		return kiloInferenceDenied("KILO_INFERENCE_BINDING_CHANGED", "Kilo 网关配置已更改，请重新选择；禁止切换为官方推理")
	}
	if err := ValidateKiloInferenceRequest(request, latest); err != nil {
		return err
	}
	if latest.IsKiloGatewayAccount() {
		sourceReader, _ := reader.(KiloGatewaySourceReader)
		if err := validateKiloBoundSource(ctx, sourceReader, latest); err != nil {
			return err
		}
	}
	if latest.IsKiloGatewayAccount() && (!latest.IsActive() || !latest.Schedulable) {
		return kiloInferenceDenied("KILO_INFERENCE_UNAVAILABLE", "Kilo 网关已停用或不可调度，生成请求已停止；不会回退到官方推理")
	}
	if latest.ParentAccountID != nil {
		parent, readErr := reader.GetKiloInferenceAccount(ctx, *latest.ParentAccountID)
		if readErr != nil || parent == nil {
			return kiloInferenceDenied("KILO_INFERENCE_STATE_UNAVAILABLE", "无法核对凭据来源的 Kilo 推理隔离状态")
		}
		if parent.RequiresKiloInference() {
			return kiloInferenceDenied("KILO_SOURCE_INFERENCE_BLOCKED", "该影子的来源已绑定 Kilo；禁止借用来源 OAuth 生成，请使用 Kilo 网关")
		}
	}
	return nil
}

// A bound source owns authentication health, not inference scheduling. Its
// quota-only flag deliberately makes IsSchedulable false even while healthy.
func validateKiloBoundSource(ctx context.Context, reader KiloGatewaySourceReader, gateway *Account) error {
	if gateway == nil || !gateway.IsKiloGatewayAccount() || ParseExtraInt(gateway.Extra["kilo_onboarding_source_id"]) <= 0 {
		return nil
	}
	preparedID := int64(ParseExtraInt(gateway.Extra["kilo_prepared_id"]))
	if reader == nil || preparedID <= 0 {
		return kiloInferenceDenied("KILO_GPT_AUTH_UNAVAILABLE", "无法核对绑定GPT授权，生成已停止")
	}
	source, err := reader.GetKiloSourceForGateway(ctx, gateway.ID, preparedID)
	if err != nil || source == nil || source.ID != int64(ParseExtraInt(gateway.Extra["kilo_onboarding_source_id"])) || !source.IsOpenAIOAuth() || !source.IsActive() || OpenAIAuthRecoveryPending(source) {
		return kiloInferenceDenied("KILO_GPT_AUTH_UNAVAILABLE", "绑定GPT授权已失效或工作区不可用，生成与测试已停止，请先恢复来源授权")
	}
	if expected, _ := gateway.Extra["kilo_subscription_scope_source_identity"].(string); expected != "" && expected != kiloSourceIdentity(source) {
		return kiloInferenceDenied("KILO_MODEL_SCOPE_UNVERIFIED", "绑定GPT身份或工作区已改变，请重新核验订阅模型范围")
	}
	return nil
}

// resolveKiloTestAccount allows explicit source tests without ever giving a
// source token to the generator. An unavailable or broken pairing is terminal.
func resolveKiloTestAccount(ctx context.Context, repo AccountRepository, selected *Account) (*Account, error) {
	if selected == nil {
		return nil, kiloInferenceDenied("KILO_INFERENCE_ACCOUNT_MISSING", "测试账号不存在")
	}
	account := selected
	if repo != nil && account.ID > 0 && account.RequiresKiloInference() {
		fresh, err := repo.GetByID(ctx, account.ID)
		if err != nil || fresh == nil || fresh.ID != account.ID {
			return nil, kiloInferenceDenied("KILO_INFERENCE_STATE_UNAVAILABLE", "无法核对 Kilo 测试账号状态")
		}
		if !fresh.RequiresKiloInference() {
			return nil, kiloInferenceDenied("KILO_INFERENCE_BINDING_CHANGED", "Kilo 配对已更改，请重新发起测试")
		}
		account = fresh
	}
	if account.IsCredentialShadow() && repo != nil {
		parent, err := resolveCredentialAccount(ctx, repo, account)
		if err != nil {
			return nil, err
		}
		if parent.RequiresKiloInference() {
			account = parent
		}
	}
	if account.IsKiloSourceAccount() {
		preparedID := int64(ParseExtraInt(account.Extra["kilo_source_prepared_id"]))
		gatewayID := int64(ParseExtraInt(account.Extra["kilo_source_gateway_id"]))
		if repo == nil || preparedID <= 0 || gatewayID <= 0 || gatewayID == account.ID {
			return nil, kiloInferenceDenied("KILO_INFERENCE_PAIRING_INVALID", "Kilo 来源配对不完整，测试已停止；不会使用来源 OAuth 生成")
		}
		gateway, err := repo.GetByID(ctx, gatewayID)
		if err != nil || gateway == nil || gateway.ID != gatewayID || gateway.Extra[KiloManagedExtraKey] != true || int64(ParseExtraInt(gateway.Extra["kilo_prepared_id"])) != preparedID {
			return nil, kiloInferenceDenied("KILO_INFERENCE_PAIRING_INVALID", "已配对的 Kilo 网关不可用，测试已停止；不会回退到官方推理")
		}
		if reader, ok := repo.(KiloGatewaySourceReader); ok {
			source, readErr := reader.GetKiloSourceForGateway(ctx, gatewayID, preparedID)
			if readErr != nil || source == nil || source.ID != account.ID {
				return nil, kiloInferenceDenied("KILO_INFERENCE_PAIRING_INVALID", "Kilo 来源与网关的持久配对已更改，测试已停止")
			}
		}
		account = gateway
	}
	if err := ValidateKiloInferenceAccount(account); err != nil {
		return nil, err
	}
	if account.IsKiloGatewayAccount() {
		sourceReader, _ := repo.(KiloGatewaySourceReader)
		if err := validateKiloBoundSource(ctx, sourceReader, account); err != nil {
			return nil, err
		}
	}
	if account.IsKiloGatewayAccount() && (!account.IsActive() || !account.Schedulable) {
		return nil, kiloInferenceDenied("KILO_INFERENCE_UNAVAILABLE", "Kilo 网关已停用或不可调度，测试已停止；不会回退到官方推理")
	}
	return account, nil
}

// resolveKiloQuotaSource is read-only and does not make the source schedulable.
// An unpaired gateway cannot borrow an arbitrary OAuth account for its quota UI.
func resolveKiloQuotaSource(ctx context.Context, repo AccountRepository, account *Account) (*Account, error) {
	if !account.IsKiloGatewayAccount() {
		return account, nil
	}
	reader, ok := repo.(KiloGatewaySourceReader)
	preparedID := int64(ParseExtraInt(account.Extra["kilo_prepared_id"]))
	if !ok || preparedID <= 0 || account.Extra[KiloManagedExtraKey] != true {
		return nil, kiloInferenceDenied("KILO_QUOTA_SOURCE_UNAVAILABLE", "Kilo 网关尚未绑定可查询额度的官方 OAuth 来源")
	}
	source, err := reader.GetKiloSourceForGateway(ctx, account.ID, preparedID)
	if err != nil || source == nil || !source.IsOpenAIOAuth() || source.ParentAccountID != nil || int64(ParseExtraInt(source.Extra["kilo_source_gateway_id"])) != account.ID || int64(ParseExtraInt(source.Extra["kilo_source_prepared_id"])) != preparedID {
		return nil, kiloInferenceDenied("KILO_QUOTA_SOURCE_UNAVAILABLE", "Kilo 网关的官方 OAuth 来源配对不可用，无法查询额度")
	}
	return source, nil
}

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	KiloGatewayCLIVersion               = "7.8.27"
	KiloGatewayManagedExtra             = "kilo_managed"
	KiloGatewayProfileExtra             = "kilo_gateway_profile"
	KiloGatewayCLIProfile               = "cli"
	KiloGatewayMachineIDCredential      = "kilo_machine_id"
	KiloGatewayOrganizationIDCredential = "kilo_organization_id"

	kiloGatewaySessionContextKey = "kilo_gateway_session"
	kiloGatewaySessionTTL        = 24 * time.Hour
	kiloGatewaySessionLimit      = 16384
)

func isKiloCLIGatewayAccount(account *Account) bool {
	if !account.IsKiloBridgeEnabled() {
		return false
	}
	managed, _ := account.Extra[KiloGatewayManagedExtra].(bool)
	profile, _ := account.Extra[KiloGatewayProfileExtra].(string)
	return managed && profile == KiloGatewayCLIProfile
}

func kiloGatewayUpstreamModel(account *Account, model string) string {
	if isKiloCLIGatewayAccount(account) && model != "" && !strings.HasPrefix(model, "openai/") {
		return "openai/" + model
	}
	return model
}

func kiloGatewayClientModel(account *Account, model string) string {
	if isKiloCLIGatewayAccount(account) {
		return strings.TrimPrefix(model, "openai/")
	}
	return model
}

// KiloGatewaySessionStore shares only response-to-wire-session metadata across
// workers. A miss returns an empty session and nil error. Implementations must
// scope every key by accountID and apiKeyID and expire it using the supplied TTL;
// account selection and response authorization remain in their existing stores.
type KiloGatewaySessionStore interface {
	GetKiloGatewayResponseSession(ctx context.Context, accountID, apiKeyID int64, responseID string) (string, error)
	SetKiloGatewayResponseSession(ctx context.Context, accountID, apiKeyID int64, responseID, sessionID string, ttl time.Duration) error
}

type kiloGatewaySessionContext struct {
	accountID int64
	apiKeyID  int64
	sessionID string
	model     string
	store     KiloGatewaySessionStore
}

type kiloGatewaySessionBinding struct {
	sessionID string
	expiresAt time.Time
}

// This cache records only Kilo wire-session metadata. Account/user ownership
// remains in the existing scheduler and HTTP response-owner store. A missing
// continuation binding must never silently assign a fresh random session.
var kiloGatewaySessions = struct {
	sync.Mutex
	bindings map[string]kiloGatewaySessionBinding
}{bindings: make(map[string]kiloGatewaySessionBinding)}

type kiloGatewaySessionContextKeyType struct{}
type kiloGatewaySessionStoreContextKey struct{}

// WithKiloGatewaySessionStore supplies shared wire-session metadata to a gateway
// request. It does not change the scheduler's account or response ownership.
func WithKiloGatewaySessionStore(ctx context.Context, store KiloGatewaySessionStore) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if store == nil {
		return ctx
	}
	return context.WithValue(ctx, kiloGatewaySessionStoreContextKey{}, store)
}

func kiloGatewaySessionStoreFromContext(ctx context.Context) KiloGatewaySessionStore {
	if ctx == nil {
		return nil
	}
	store, _ := ctx.Value(kiloGatewaySessionStoreContextKey{}).(KiloGatewaySessionStore)
	return store
}

func kiloGatewayResponseSessionKey(accountID, apiKeyID int64, responseID string) string {
	return strconv.FormatInt(accountID, 10) + ":" + strconv.FormatInt(apiKeyID, 10) + ":" + responseID
}

func lookupKiloGatewayResponseSession(accountID, apiKeyID int64, responseID string) string {
	key := kiloGatewayResponseSessionKey(accountID, apiKeyID, responseID)
	kiloGatewaySessions.Lock()
	defer kiloGatewaySessions.Unlock()
	binding, ok := kiloGatewaySessions.bindings[key]
	if !ok {
		return ""
	}
	if !time.Now().Before(binding.expiresAt) {
		delete(kiloGatewaySessions.bindings, key)
		return ""
	}
	return binding.sessionID
}

func bindKiloGatewayResponseSession(ctx context.Context, result *OpenAIForwardResult) error {
	if ctx == nil || result == nil || result.ResponseID == "" {
		return nil
	}
	identity, ok := ctx.Value(kiloGatewaySessionContextKeyType{}).(kiloGatewaySessionContext)
	if !ok || identity.sessionID == "" {
		return nil
	}
	if identity.store != nil {
		// A completed model request cannot be replayed when metadata storage
		// fails. Detach client cancellation and bound only the cache operation.
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		return identity.store.SetKiloGatewayResponseSession(writeCtx, identity.accountID, identity.apiKeyID, result.ResponseID, identity.sessionID, kiloGatewaySessionTTL)
	}
	now := time.Now()
	key := kiloGatewayResponseSessionKey(identity.accountID, identity.apiKeyID, result.ResponseID)
	kiloGatewaySessions.Lock()
	defer kiloGatewaySessions.Unlock()
	if len(kiloGatewaySessions.bindings) >= kiloGatewaySessionLimit {
		oldestKey := ""
		var oldest time.Time
		for candidate, binding := range kiloGatewaySessions.bindings {
			if !now.Before(binding.expiresAt) {
				delete(kiloGatewaySessions.bindings, candidate)
				continue
			}
			if oldestKey == "" || binding.expiresAt.Before(oldest) {
				oldestKey, oldest = candidate, binding.expiresAt
			}
		}
		if len(kiloGatewaySessions.bindings) >= kiloGatewaySessionLimit {
			delete(kiloGatewaySessions.bindings, oldestKey)
		}
	}
	kiloGatewaySessions.bindings[key] = kiloGatewaySessionBinding{sessionID: identity.sessionID, expiresAt: now.Add(kiloGatewaySessionTTL)}
	return nil
}

func resolveKiloGatewaySession(c *gin.Context, account *Account, body []byte, promptCacheKey string, stores ...KiloGatewaySessionStore) (kiloGatewaySessionContext, error) {
	identity := kiloGatewaySessionContext{accountID: account.ID, apiKeyID: getAPIKeyIDFromContext(c), model: gjson.GetBytes(body, "model").String()}
	if c != nil && c.Request != nil {
		identity.store = kiloGatewaySessionStoreFromContext(c.Request.Context())
	}
	if len(stores) > 0 && stores[0] != nil {
		identity.store = stores[0]
	}
	if c != nil {
		if raw, ok := c.Get(kiloGatewaySessionContextKey); ok {
			if staged, ok := raw.(kiloGatewaySessionContext); ok && staged.accountID == identity.accountID && staged.apiKeyID == identity.apiKeyID {
				identity.sessionID = staged.sessionID
			}
		}
	}
	previous := gjson.GetBytes(body, "previous_response_id")
	if previous.Type == gjson.String && previous.String() != "" {
		var bound string
		if identity.store != nil {
			lookupCtx := context.Background()
			if c != nil && c.Request != nil {
				lookupCtx = c.Request.Context()
			}
			readCtx, cancel := context.WithTimeout(lookupCtx, 3*time.Second)
			var err error
			bound, err = identity.store.GetKiloGatewayResponseSession(readCtx, identity.accountID, identity.apiKeyID, previous.String())
			cancel()
			if err != nil {
				return identity, fmt.Errorf("read shared Kilo continuation session: %w", err)
			}
		} else {
			bound = lookupKiloGatewayResponseSession(identity.accountID, identity.apiKeyID, previous.String())
		}
		if bound != "" {
			identity.sessionID = bound
		}
	}
	if identity.sessionID == "" {
		seed := ExtractOpenAIClientSessionID(c, body)
		if seed == "" && c != nil && c.Request != nil {
			for _, name := range []string{"X-Session-Id", "X-Session-Affinity"} {
				if seed = sanitizeSessionID(c.GetHeader(name)); seed != "" {
					break
				}
			}
		}
		if seed == "" {
			seed = strings.TrimSpace(promptCacheKey)
		}
		if seed == "" {
			if key := gjson.GetBytes(body, "prompt_cache_key"); key.Type == gjson.String {
				seed = strings.TrimSpace(key.String())
			}
		}
		if seed != "" {
			digest := sha256.Sum256([]byte(strconv.FormatInt(identity.apiKeyID, 10) + "\x00" + seed))
			identity.sessionID = "ses_" + hex.EncodeToString(digest[:13])
		} else if previous.Type == gjson.String && previous.String() != "" {
			return identity, fmt.Errorf("Kilo continuation session is unavailable; retain the original session identity or prompt_cache_key")
		} else {
			identity.sessionID = uuid.NewString()
		}
	}
	if c != nil {
		c.Set(kiloGatewaySessionContextKey, identity)
		if c.Request != nil {
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), kiloGatewaySessionContextKeyType{}, identity))
		}
	}
	return identity, nil
}

// Apply the managed CLI profile last: neither downstream identity headers nor
// account-level arbitrary overrides may replace the pool-owned org or session.
func (s *OpenAIGatewayService) applyKiloGatewayIdentity(req *http.Request, c *gin.Context, account *Account, body []byte, promptCacheKey string) error {
	// Production GatewayCache implementations can supply the shared contract
	// without changing the existing constructor or introducing a second owner.
	var store KiloGatewaySessionStore
	if s != nil {
		store, _ = s.cache.(KiloGatewaySessionStore)
	}
	if store == nil {
		store = kiloGatewaySessionStoreFromContext(req.Context())
	}
	if store == nil && c != nil && c.Request != nil {
		store = kiloGatewaySessionStoreFromContext(c.Request.Context())
	}
	if store != nil && isKiloCLIGatewayAccount(account) {
		*req = *req.WithContext(WithKiloGatewaySessionStore(req.Context(), store))
		if c != nil && c.Request != nil {
			c.Request = c.Request.WithContext(WithKiloGatewaySessionStore(c.Request.Context(), store))
		}
	}
	return applyKiloGatewayIdentity(req, c, account, body, promptCacheKey, store)
}

func applyKiloGatewayIdentity(req *http.Request, c *gin.Context, account *Account, body []byte, promptCacheKey string, stores ...KiloGatewaySessionStore) error {
	if !isKiloCLIGatewayAccount(account) {
		return nil
	}
	organizationID := strings.TrimSpace(account.GetCredential(KiloGatewayOrganizationIDCredential))
	if organizationID != "" && sanitizeSessionID(organizationID) == "" {
		return fmt.Errorf("managed Kilo account has an invalid kilo_organization_id")
	}
	identity, err := resolveKiloGatewaySession(c, account, body, promptCacheKey, stores...)
	if err != nil {
		return err
	}
	ownedHeaders := map[string]string{
		"User-Agent":            "Kilo-Code/" + KiloGatewayCLIVersion + " ai-sdk/provider-utils/4.0.40 runtime/bun/1.4.4",
		"X-KILOCODE-EDITORNAME": "Kilo CLI " + KiloGatewayCLIVersion,
		"X-KILOCODE-FEATURE":    "cli",
		// Retired upstream on 2026-11-12; retain stored IDs but strip every wire variant.
		"X-KILOCODE-MACHINEID":      "",
		"X-KILOCODE-TASKID":         identity.sessionID,
		"X-Session-Id":              identity.sessionID,
		"X-Session-Affinity":        identity.sessionID,
		"X-KILOCODE-MODE":           "code",
		"X-KiloCode-OrganizationId": organizationID,
		"Accept":                    "*/*",
		"Accept-Encoding":           "identity",
	}
	for name, value := range ownedHeaders {
		for existing := range req.Header {
			if strings.EqualFold(existing, name) {
				delete(req.Header, existing)
			}
		}
		if value != "" {
			req.Header.Set(name, value)
		}
	}
	*req = *req.WithContext(context.WithValue(req.Context(), kiloGatewaySessionContextKeyType{}, identity))
	return nil
}

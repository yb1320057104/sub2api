package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const AuditSettlementEnabledExtraKey = "audit_settlement_enabled"
const auditImportCapabilityHeader = "X-Sub2api-Audit-Settlement"
const auditImportReceiptHeader = "X-Sub2api-Audit-Receipt"

type auditSettlementNegotiatedKey struct{}

// Receipt URLs are derived only from the administrator-configured upstream.
// No URL, credential or multiplier from a model response is trusted.
func auditSettlementAccountIdentity(a *Account) (identity, baseURL string, ok bool) {
	if a == nil || (a.Type != AccountTypeAPIKey && a.Type != AccountTypeUpstream) ||
		(a.Platform != PlatformOpenAI && a.Platform != PlatformAnthropic) || a.Extra[AuditSettlementEnabledExtraKey] != true {
		return "", "", false
	}
	baseURL = strings.TrimRight(strings.TrimSpace(a.GetCredential("base_url")), "/")
	key := a.GetCredential("api_key")
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/v1") ||
		(u.Scheme != "http" && u.Scheme != "https") || key == "" {
		return "", "", false
	}
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	hash := sha256.Sum256([]byte(baseURL + "\x00" + key))
	return hex.EncodeToString(hash[:]), baseURL, true
}

// Called after administrator header overrides, so clients cannot negotiate on
// behalf of an account whose explicit trust switch is off.
func prepareAuditSettlementRequest(req *http.Request, account *Account) {
	if req == nil {
		return
	}
	req.Header.Del(auditImportCapabilityHeader)
	req.Header.Del(auditImportReceiptHeader)
	_, base, enabled := auditSettlementAccountIdentity(account)
	if !enabled || req.Method != http.MethodPost || req.URL == nil {
		return
	}
	key := account.GetCredential("api_key")
	authorization, apiKeyHeader := req.Header.Get("Authorization"), req.Header.Get("X-Api-Key")
	if (authorization == "" && apiKeyHeader == "") || (authorization != "" && authorization != "Bearer "+key) || (apiKeyHeader != "" && apiKeyHeader != key) {
		return
	}
	for _, endpoint := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		actualURL := *req.URL
		// The existing Anthropic API-key path appends this protocol flag. No
		// caller-controlled query or alternate endpoint may gain negotiation.
		if endpoint == "/v1/messages" && actualURL.RawQuery == "beta=true" {
			actualURL.RawQuery = ""
		}
		if actualURL.String() == buildOpenAIEndpointURL(base, endpoint) {
			req.Header.Set(auditImportCapabilityHeader, "v1")
			*req = *req.WithContext(context.WithValue(WithHTTPUpstreamRedirectsDisabled(req.Context()), auditSettlementNegotiatedKey{}, true))
			return
		}
	}
}

// Only server-owned outgoing negotiation plus an opted-in account and a real
// receipt can suppress this site's duplicate punishment of a remote event.
func negotiatedAuditSettlementReceipt(account *Account, headers http.Header, negotiated bool) bool {
	if !negotiated || headers.Get(auditImportCapabilityHeader) != "v1" {
		return false
	}
	if _, _, enabled := auditSettlementAccountIdentity(account); !enabled {
		return false
	}
	id, err := uuid.Parse(headers.Get(auditImportReceiptHeader))
	return err == nil && id != uuid.Nil
}

type AuditSettlementImportInput struct {
	UsageID          int64
	UsageCreatedAt   time.Time
	AccountID        int64
	UserID           int64
	APIKeyID         int64
	GroupID          *int64
	UpstreamIdentity string
	ReceiptID        string
}

type auditSettlementImportRegistrar interface {
	RegisterAuditSettlementImport(context.Context, AuditSettlementImportInput) error
}

func withUpstreamAuditSettlementImport(ctx context.Context, repo UsageLogRepository, account *Account, headers http.Header, negotiated bool) context.Context {
	if !negotiated {
		return ctx
	}
	identity, _, enabled := auditSettlementAccountIdentity(account)
	if !enabled || headers.Get(auditImportCapabilityHeader) != "v1" {
		return ctx
	}
	receipt, err := uuid.Parse(headers.Get(auditImportReceiptHeader))
	if err != nil || receipt == uuid.Nil {
		return WithAuditSettlementUsageObserver(ctx, func(context.Context, *UsageLog) error {
			return errors.New("invalid negotiated audit settlement receipt")
		})
	}
	accountID := account.ID
	registrar, ok := repo.(auditSettlementImportRegistrar)
	return WithAuditSettlementUsageObserver(ctx, func(callCtx context.Context, usage *UsageLog) error {
		if !ok {
			return errors.New("audit settlement persistence unavailable")
		}
		if usage == nil || usage.ID <= 0 || usage.AccountID != accountID {
			return errors.New("audit settlement usage identity unavailable")
		}
		if usage.EffectiveRequestType() == RequestTypeCyberBlocked && !usage.AuditSettlementImportedEvent {
			return errors.New("remote audit settlement event was not durably identified")
		}
		return registrar.RegisterAuditSettlementImport(callCtx, AuditSettlementImportInput{
			UsageID: usage.ID, UsageCreatedAt: usage.CreatedAt, AccountID: usage.AccountID, UserID: usage.UserID,
			APIKeyID: usage.APIKeyID, GroupID: usage.GroupID, UpstreamIdentity: identity, ReceiptID: receipt.String(),
		})
	})
}

func auditSettlementResponseHeaders(headers http.Header) http.Header {
	if headers.Get(auditImportCapabilityHeader) != "v1" {
		return nil
	}
	return http.Header{auditImportCapabilityHeader: []string{"v1"}, auditImportReceiptHeader: []string{headers.Get(auditImportReceiptHeader)}}
}

func auditSettlementWasNegotiated(resp *http.Response) bool {
	return resp != nil && resp.Request != nil && resp.Request.Context().Value(auditSettlementNegotiatedKey{}) == true && resp.Request.Header.Get(auditImportCapabilityHeader) == "v1"
}

package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const intelligenceTestKeyPrefix = "sk-iq-"
const intelligenceTestTokenHeader = "X-Sub2api-Intelligence-Token"

// Up to 2,000 selected groups can run both modes together. Leave room for
// authorizations belonging to a cancelling generation until its requests exit.
const intelligenceTestPendingAuthorizationLimit = 8192

// These one-use capabilities exist only in this process. A loopback reverse
// proxy alone cannot authenticate a browser request, even with a leaked key.
var intelligenceTestCapabilities = struct {
	sync.Mutex
	items map[[32]byte]intelligenceTestCapability
}{items: make(map[[32]byte]intelligenceTestCapability)}

type intelligenceTestCapability struct {
	keyHash [32]byte
	expires time.Time
}

func IsIntelligenceTestKey(key string) bool {
	return strings.HasPrefix(key, intelligenceTestKeyPrefix)
}

func intelligenceTestGenerationRequest(req *http.Request) bool {
	return req != nil && req.URL != nil && req.Method == http.MethodPost &&
		req.URL.Path == "/v1/responses" && req.URL.EscapedPath() == "/v1/responses" &&
		req.URL.RawQuery == "" && !req.URL.ForceQuery && req.URL.Fragment == ""
}

// AuthorizeIntelligenceTestRequest is called only by the server scheduler,
// immediately before sending its fixed loopback Responses request. It grants
// no billing, group, moderation or account authorization exemptions.
func AuthorizeIntelligenceTestRequest(req *http.Request) error {
	if !intelligenceTestGenerationRequest(req) || req.URL.Scheme != "http" ||
		req.URL.User != nil || !net.ParseIP(req.URL.Hostname()).IsLoopback() {
		return errors.New("invalid intelligence test internal endpoint")
	}
	key := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !IsIntelligenceTestKey(key) {
		return errors.New("intelligence test requires a managed key")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errors.New("cannot create intelligence test authorization")
	}
	token := hex.EncodeToString(random[:])
	now := time.Now()
	intelligenceTestCapabilities.Lock()
	defer intelligenceTestCapabilities.Unlock()
	for hash, capability := range intelligenceTestCapabilities.items {
		if !now.Before(capability.expires) {
			delete(intelligenceTestCapabilities.items, hash)
		}
	}
	if len(intelligenceTestCapabilities.items) >= intelligenceTestPendingAuthorizationLimit {
		return errors.New("too many pending intelligence test authorizations")
	}
	intelligenceTestCapabilities.items[sha256.Sum256([]byte(token))] = intelligenceTestCapability{
		keyHash: sha256.Sum256([]byte(key)), expires: now.Add(time.Minute),
	}
	req.Header.Set(intelligenceTestTokenHeader, token)
	return nil
}

// Release a capability that never reached gateway authentication, for example
// after a connection failure or cancellation. Consumed capabilities are already
// absent, so every scheduled request can defer this without changing validation.
func releaseIntelligenceTestRequest(req *http.Request) {
	if req == nil {
		return
	}
	token := req.Header.Get(intelligenceTestTokenHeader)
	if token == "" {
		return
	}
	hash := sha256.Sum256([]byte(token))
	intelligenceTestCapabilities.Lock()
	delete(intelligenceTestCapabilities.items, hash)
	intelligenceTestCapabilities.Unlock()
}

// ValidateIntelligenceTestRequest runs on every gateway authentication, before
// any key cache lookup. Never trust forwarded headers for this extra boundary.
// The token is consumed once and removed before gateway forwarding or logging.
func ValidateIntelligenceTestRequest(req *http.Request, key string) bool {
	if req == nil {
		return false
	}
	values := req.Header.Values(intelligenceTestTokenHeader)
	req.Header.Del(intelligenceTestTokenHeader)
	if !IsIntelligenceTestKey(key) {
		return true
	}
	if !intelligenceTestGenerationRequest(req) || len(values) != 1 || len(values[0]) != 64 {
		return false
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return false
	}
	hash := sha256.Sum256([]byte(values[0]))
	intelligenceTestCapabilities.Lock()
	capability, ok := intelligenceTestCapabilities.items[hash]
	delete(intelligenceTestCapabilities.items, hash)
	intelligenceTestCapabilities.Unlock()
	return ok && time.Now().Before(capability.expires) && capability.keyHash == sha256.Sum256([]byte(key))
}

func revokeIntelligenceTestCapabilities(keys []string) {
	revoked := make(map[[32]byte]struct{}, len(keys))
	for _, key := range keys {
		revoked[sha256.Sum256([]byte(key))] = struct{}{}
	}
	intelligenceTestCapabilities.Lock()
	defer intelligenceTestCapabilities.Unlock()
	for token, capability := range intelligenceTestCapabilities.items {
		if _, ok := revoked[capability.keyHash]; ok {
			delete(intelligenceTestCapabilities.items, token)
		}
	}
}

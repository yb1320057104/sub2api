package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const openAIRelayWebSocket403Message = "failed to WebSocket dial: expected handshake response status code 101 but got 403"

const openAIPoolRelayWebSocketHandshakeReason = GatewayFailureReason("openai_pool_relay_websocket_handshake")

// isOpenAIRequestScoped403 recognizes explicit request/transport failures from
// OpenAI-compatible relays. Neither is evidence that the local credential is
// invalid, even when the relay reports HTTP 403. Keep this narrow so genuine
// account and workspace denials retain their existing handling.
func isOpenAIRequestScoped403(account *Account, statusCode int, body []byte) bool {
	if account == nil || account.Platform != PlatformOpenAI || statusCode != http.StatusForbidden {
		return false
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	// Some relays append response.failed SSE after their JSON error. Decode the
	// first complete object, trusting only its error fields, never echoed input
	// or a later event in the body.
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&payload); err != nil {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(payload.Error.Code))
	errorType := strings.ToLower(strings.TrimSpace(payload.Error.Type))
	if code == "content_policy_violation" || (code == "" && errorType == "content_policy_violation") {
		return true
	}
	return errorType == "upstream_error" && (code == "" || code == "upstream_error") &&
		strings.EqualFold(strings.TrimSpace(payload.Error.Message),
			openAIRelayWebSocket403Message)
}

// A relay pool may wrap its selected backend's failed WebSocket handshake in
// HTTP 502. This exact pre-handshake failure does not invalidate every backend
// behind the local pool credential. Unlike the legacy 403 exemption above,
// retry eligibility requires one complete error-only JSON envelope: never
// infer replay safety from echoed input, partial output or a later SSE event.
func isOpenAIPoolRelayWebSocketHandshakeError(account *Account, statusCode int, body []byte) bool {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey ||
		!account.IsPoolMode() || statusCode != http.StatusBadGateway {
		return false
	}
	var payload struct {
		Error struct {
			Code    string          `json:"code"`
			Type    string          `json:"type"`
			Message string          `json:"message"`
			Param   json.RawMessage `json:"param"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(payload.Error.Code))
	return strings.EqualFold(strings.TrimSpace(payload.Error.Type), "upstream_error") &&
		(code == "" || code == "upstream_error") &&
		strings.EqualFold(strings.TrimSpace(payload.Error.Message), openAIRelayWebSocket403Message)
}

// A completed error envelope from a relay describes one interrupted backend
// stream, not the health of every backend behind its pool credential. This
// classification only suppresses the generic model cooldown: the request may
// already have executed, so it must not grant any additional retry permission.
func isOpenAIPoolRelayStreamIncompleteError(account *Account, statusCode int, body []byte) bool {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey ||
		!account.IsPoolMode() || statusCode != http.StatusBadGateway {
		return false
	}
	var payload map[string]map[string]string
	if err := json.Unmarshal(body, &payload); err != nil || len(payload) != 1 {
		return false
	}
	err := payload["error"]
	return len(err) == 3 && err["type"] == "upstream_error" &&
		err["code"] == OpenAIWSSSEIncompleteCode && err["message"] == openAIWSSSEIncompleteMessage
}

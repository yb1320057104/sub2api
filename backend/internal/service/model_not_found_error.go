package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

var upstreamModelNotFoundKeywords = []string{"model not found", "unknown model", "not found"}

func isUpstreamModelNotFoundError(statusCode int, body []byte) bool {
	if statusCode != http.StatusNotFound {
		return false
	}
	// Only the provider's error fields are evidence. Scanning the complete
	// JSON also matched echoed request models beside an unrelated route/auth
	// "not found", which could cool a healthy account/model pair.
	if gjson.ValidBytes(body) {
		code := strings.TrimSpace(extractUpstreamErrorCode(body))
		if code == "" {
			code = strings.TrimSpace(gjson.GetBytes(body, "code").String())
		}
		// Google-style envelopes use the HTTP status as a numeric code;
		// it does not identify the error's subject.
		if code != "" && code != "404" {
			return strings.EqualFold(code, "model_not_found")
		}
		for _, path := range []string{"error.type", "type"} {
			typ := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, path).String()))
			if typ == "model_not_found" {
				return true
			}
			if typ != "" && typ != "error" && typ != "invalid_request_error" && typ != "not_found_error" && typ != "not_found" {
				return false
			}
		}
	}
	message := extractUpstreamErrorMessage(body)
	if message == "" && !gjson.ValidBytes(body) {
		message = string(body)
	}
	message = strings.ToLower(strings.TrimSpace(message))
	if strings.HasPrefix(message, "model not found") || strings.HasPrefix(message, "unknown model") ||
		strings.HasPrefix(message, "unknown provider for model") {
		return true
	}
	if isSingleModelNotFoundMessage(message) {
		return true
	}
	if !strings.HasPrefix(message, "model ") && !strings.HasPrefix(message, "models/") && !strings.HasPrefix(message, "the model ") &&
		!strings.HasPrefix(message, "the requested model ") {
		return false
	}
	return strings.Contains(message, " was not found") || strings.Contains(message, " is not found") || strings.Contains(message, " does not exist") ||
		strings.Contains(message, " is not supported by any configured account in this group")
}

// Some compatible providers omit "is" in "Model <identifier> not found".
// Match that complete sentence, not an arbitrary subject or a URL endpoint.
func isSingleModelNotFoundMessage(message string) bool {
	subject, ok := strings.CutSuffix(strings.TrimRight(message, ".!?"), " not found")
	if !ok {
		return false
	}
	identifier := ""
	for _, prefix := range []string{"model ", "the model ", "the requested model "} {
		if value, matched := strings.CutPrefix(subject, prefix); matched {
			identifier = value
			break
		}
	}
	if len(identifier) >= 2 && strings.ContainsRune("\"'`", rune(identifier[0])) {
		if identifier[len(identifier)-1] != identifier[0] {
			return false
		}
		identifier = identifier[1 : len(identifier)-1]
	}
	parts := strings.Split(identifier, "/")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || !((part[0] >= 'a' && part[0] <= 'z') || (part[0] >= '0' && part[0] <= '9')) {
			return false
		}
		switch part {
		case "api", "endpoint", "endpoints", "path", "paths", "registry", "registries", "route", "routes", "models":
			return false
		}
		for _, char := range part {
			if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || strings.ContainsRune("-_.:", char)) {
				return false
			}
		}
	}
	return true
}

func isModelNotFoundError(statusCode int, body []byte) bool {
	return isUpstreamModelNotFoundError(statusCode, body) || statusCode == http.StatusNotFound
}

// openAICodexPlanGatedModelPhrase matches the deterministic Codex 400 returned
// when a ChatGPT OAuth account's plan cannot serve the requested model, e.g.
// {"detail":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account."}
// The phrase is compared against the normalized body (lowercased, "_"/"-"
// folded to spaces), so it also matches the same message embedded in
// error.message-style payloads.
const openAICodexPlanGatedModelPhrase = "model is not supported when using codex"

// isOpenAICodexPlanGatedModelError reports whether the upstream response is the
// deterministic Codex rejection of a plan-gated model on a ChatGPT account.
// Unlike transient failures, retrying the same account cannot succeed until the
// account's plan changes, so callers should treat it like model-not-found and
// cool the (account, model) pair down instead of re-selecting the account.
func isOpenAICodexPlanGatedModelError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	normalized := normalizeModelNotFoundBody(body)
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, openAICodexPlanGatedModelPhrase)
}

func containsModelNotFoundKeyword(normalizedBody string) bool {
	if normalizedBody == "" {
		return false
	}
	for _, keyword := range upstreamModelNotFoundKeywords {
		if strings.Contains(normalizedBody, keyword) {
			return true
		}
	}
	return false
}

func normalizeModelNotFoundBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	normalized := strings.ToLower(string(body))
	normalized = strings.NewReplacer("_", " ", "-", " ", "\n", " ", "\r", " ", "\t", " ").Replace(normalized)
	return strings.Join(strings.Fields(normalized), " ")
}

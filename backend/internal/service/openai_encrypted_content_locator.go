package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIEncryptedErrorRedacted = "Upstream encrypted content error (details redacted)"

const openAIEncryptedHistoryRecoveryGuidance = "Preserve this task and its complete context. Verify the original compatible account and the outcome of any previous tool execution. If recovery requires a new conversation, reconstruct it from the complete original plaintext history; do not discard opaque items or repeat completed tool actions."

// Inspect actual input items, never words inside user messages. A plaintext
// summary and ordinary encrypted reasoning do not require this replay guard.
func openAIOpaqueHistoryKind(body []byte) (kind string, toolResult bool) {
	visit := func(item gjson.Result) {
		itemType := item.Get("type").String()
		toolResult = toolResult || isCodexToolCallOutputItemType(itemType)
		if itemType != "agent_message" && itemType != "compaction" && itemType != "compaction_summary" {
			return
		}
		encrypted := item.Get("encrypted_content")
		opaque := encrypted.Type == gjson.String && encrypted.String() != ""
		content := item.Get("content")
		if content.IsArray() {
			content.ForEach(func(_, part gjson.Result) bool {
				value := part.Get("encrypted_content")
				opaque = opaque || (part.Get("type").String() == "encrypted_content" && value.Type == gjson.String && value.String() != "")
				return true
			})
		}
		if opaque && kind == "" {
			kind = "opaque_" + itemType
		}
	}
	input := gjson.GetBytes(body, "input")
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool { visit(item); return true })
	} else if input.IsObject() {
		visit(input)
	}
	return kind, toolResult
}

// OpenAIUnverifiedOpaqueHistoryRecovery is consulted only after a failed
// attempt, never during first admission. Proven owners retain their existing
// route checks. Without one, sending intact opaque history to a different
// account cannot establish compatibility. Tool results also preclude replay
// after an ambiguous submission to the same account.
func OpenAIUnverifiedOpaqueHistoryRecovery(ctx context.Context, body []byte) (*OpenAIAgentRecoveryError, bool) {
	if request := openAIAgentRecoveryFromContext(ctx); request != nil && request.owner != nil {
		return nil, false
	}
	kind, toolResult := openAIOpaqueHistoryKind(body)
	if kind == "" {
		return nil, false
	}
	return &OpenAIAgentRecoveryError{Reason: "unverified_history"}, toolResult
}

func openAIEncryptedRecoverySkipReason(body []byte) string {
	if kind, _ := openAIOpaqueHistoryKind(body); kind != "" {
		return kind
	}
	return "no_uniquely_verified_reasoning"
}

var openAIEncryptedRelayTrace = regexp.MustCompile(`^（traceid: [0-9a-fA-F]{32}）$`)

// A locator alone proves nothing. Only an explicit encryption rejection, this
// exact diagnostic and one safe matching item together allow recovery.
var openAIEncryptedLocator = regexp.MustCompile(`^The encrypted content ([A-Za-z0-9_-]{4})\.\.\.([A-Za-z0-9_-]{4}) could not be verified\. Reason: Encrypted content could not be decrypted or parsed\.$`)

func openAIEncryptedRejectionFields(rejection []byte) (string, string) {
	if len(rejection) > 65536 || !openAIEncryptedJSONUnambiguous(rejection) {
		return "", ""
	}
	root := gjson.ParseBytes(rejection)
	if root.Get("error").IsObject() && root.Get("response.error").IsObject() {
		return "", ""
	}
	if (root.Get("error").IsObject() || root.Get("response.error").IsObject()) && (root.Get("code").Exists() || root.Get("message").Exists()) {
		return "", ""
	}
	if root.Get("response.error").IsObject() {
		root = root.Get("response.error")
	} else if root.Get("error").IsObject() {
		root = root.Get("error")
	}
	// Some relays encode one structured error inside error.message. Only
	// unwrap a code-less invalid_request_error envelope, never arbitrary text
	// or a conflicting explicit code. Validate inner keys separately too.
	if root.Get("code").Type == gjson.Null && root.Get("type").String() == "invalid_request_error" {
		message := root.Get("message")
		if message.Type != gjson.String || !strings.HasPrefix(message.String(), "{") ||
			(root.Get("param").Exists() && root.Get("param").Type != gjson.Null) {
			return "", ""
		}
		decoder := json.NewDecoder(strings.NewReader(message.String()))
		var inner json.RawMessage
		if err := decoder.Decode(&inner); err != nil || !openAIEncryptedJSONUnambiguous(inner) {
			return "", ""
		}
		suffix := strings.TrimSpace(message.String()[decoder.InputOffset():])
		if suffix != "" && !openAIEncryptedRelayTrace.MatchString(suffix) {
			return "", ""
		}
		envelope := gjson.ParseBytes(inner)
		if !envelope.Get("error").IsObject() || envelope.Get("response").Exists() ||
			envelope.Get("code").Exists() || envelope.Get("message").Exists() {
			return "", ""
		}
		root = envelope.Get("error") // One layer only; nested wrappers stay invalid.
	}
	if root.Get("code").Type != gjson.String || (root.Get("message").Exists() && root.Get("message").Type != gjson.String) {
		return "", ""
	}
	return root.Get("code").String(), root.Get("message").String()
}

// Only called on a rejection, never on the successful request hot path.
func openAIRejectedEncryptedDigests(body, rejection []byte) []string {
	code, message := openAIEncryptedRejectionFields(rejection)
	if !isOpenAIInvalidEncryptedContentError(code, message) || !openAIEncryptedJSONUnambiguous(body) {
		return nil
	}
	match := openAIEncryptedLocator.FindStringSubmatch(message)
	// Preserve the legacy single-item generic-code recovery. If a locator is
	// present but malformed, do not silently fall back to that policy.
	genericDiagnostic := message == "The encrypted content could not be verified" ||
		message == "The encrypted content could not be verified." ||
		message == "The encrypted content could not be verified. Reason: Encrypted content could not be decrypted or parsed."
	if len(match) == 0 && !genericDiagnostic && (strings.Contains(message, "...") || strings.ContainsAny(message, "…\r\n") ||
		strings.Contains(strings.ToLower(message), "the encrypted content") || strings.EqualFold(strings.TrimSpace(code), "thinking_signature_invalid")) {
		return nil
	}
	input := gjson.GetBytes(body, "input")
	var candidates []string
	unsafe := false
	ids := map[string]bool{}
	visit := func(item gjson.Result) {
		if !item.IsObject() {
			unsafe = true
			return
		}
		kind := strings.TrimSpace(item.Get("type").String())
		if kind == "compaction" || kind == "compaction_summary" || kind == "agent_message" {
			unsafe = true
		}
		if id := item.Get("id").String(); id != "" {
			if ids[id] {
				unsafe = true
			}
			ids[id] = true
		}
		e := item.Get("encrypted_content")
		if !e.Exists() || e.Type == gjson.Null {
			return
		}
		if e.Type != gjson.String || e.String() == "" || kind != "reasoning" {
			unsafe = true
			return
		}
		candidates = append(candidates, e.String())
	}
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool { visit(item); return true })
	} else if input.IsObject() {
		visit(input)
	}
	if unsafe {
		return nil
	}
	if len(match) == 0 {
		if len(candidates) == 1 {
			return []string{openAIEncryptedContentDigest(candidates[0])}
		}
		return nil
	}
	found := ""
	count := 0
	for _, candidate := range candidates {
		if len(candidate) > 8 && strings.HasPrefix(candidate, match[1]) && strings.HasSuffix(candidate, match[2]) {
			count++
			found = candidate
		}
	}
	if count != 1 {
		return nil
	}
	return []string{openAIEncryptedContentDigest(found)}
}

func openAIEncryptedDigestSet(digests []string) map[string]struct{} {
	invalid := make(map[string]struct{}, len(digests))
	for _, digest := range digests {
		invalid[digest] = struct{}{}
	}
	return invalid
}

// Version the namespace: pre-fix session-wide entries do not prove an error on
// this account. Group remains the outer state-store scope.
func openAIEncryptedLineageKey(c *gin.Context, accountID int64, sessionHash string) string {
	if sessionHash == "" {
		return ""
	}
	return fmt.Sprintf("encrypted:v2:%d:%d:%s", getAPIKeyIDFromContext(c), accountID, sessionHash)
}

// Keep raw error evidence only for this attempt, out of returned error strings
// and production logs. The bool API is retained for legacy stream call sites.
const openAIStreamEncryptedRejectionContextKey = "openai_stream_encrypted_rejection"

func openAIStreamEncryptedRejection(c *gin.Context) []byte {
	if c == nil {
		return nil
	}
	value, _ := c.Get(openAIStreamEncryptedRejectionContextKey)
	payload, _ := value.([]byte)
	return payload
}

func openAIEncryptedAttemptBody(body map[string]any) []byte {
	payload, _ := json.Marshal(body)
	return payload
}

// Duplicate JSON keys make parser agreement unsafe (including duplicate error
// codes and encrypted_content fields). Bound recursion on untrusted errors.
func openAIEncryptedJSONUnambiguous(payload []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 128 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delimiter {
		case '{':
			keys := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || keys[name] {
					return false
				}
				keys[name] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for decoder.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !value(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func redactOpenAIEncryptedDiagnostic(message string) string {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "encrypted content") || strings.Contains(lower, `"encrypted_content"`) || strings.Contains(lower, "invalid_encrypted_content") || strings.Contains(lower, "thinking_signature_invalid") {
		return openAIEncryptedErrorRedacted
	}

	if strings.HasPrefix(strings.TrimSpace(message), "{") && gjson.Valid(message) {
		for _, path := range []string{"error.message", "response.error.message", "message", "error.code", "response.error.code", "code"} {
			decoded := gjson.Get(message, path).String()
			if decoded != "" && decoded != message && redactOpenAIEncryptedDiagnostic(decoded) != decoded {
				return openAIEncryptedErrorRedacted
			}
		}
	}
	return message
}

func openAIEncryptedInputCounts(input any) map[string]int {
	counts := map[string]int{}
	visit := func(item any) {
		if item, ok := item.(map[string]any); ok {
			if encrypted, ok := item["encrypted_content"].(string); ok {
				counts[encrypted]++
			}
		}
	}
	switch input := input.(type) {
	case []any:
		for _, item := range input {
			visit(item)
		}
	case map[string]any:
		visit(input)
	}
	return counts
}

// Log redaction is deliberately broader than recovery classification: malformed
// diagnostics can contain ciphertext too, without being safe recovery evidence.
func openAIEncryptedErrorCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_encrypted_content", "thinking_signature_invalid":
		return true
	default:
		return false
	}
}

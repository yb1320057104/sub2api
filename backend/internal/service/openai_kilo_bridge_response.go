package service

import (
	"bufio"
	"bytes"
	"context"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const kiloBridgeResponseEffortKey = "pk_kilo_bridge_response_effort"
const kiloBridgeErrorPrivacyKey = "pk_kilo_bridge_error_privacy"

var kiloBridgeMaxErrorWord = regexp.MustCompile(`(?i)\bmax\b`)

// Redact only errors that describe the internal effort/routing translation.
// Ordinary validation failures (including max_tokens) keep their useful detail.
func sanitizeKiloBridgeClientErrorMessage(c *gin.Context, message string) string {
	if c == nil || !c.GetBool(kiloBridgeErrorPrivacyKey) || !kiloBridgeMaxErrorWord.MatchString(message) {
		return message
	}
	lower := strings.ToLower(message)
	for _, marker := range []string{"reasoning", "effort", "rout", "mapped", "mapping", "ultra", "persistent", "强度", "路由", "映射"} {
		if strings.Contains(lower, marker) {
			return "Upstream request failed"
		}
	}
	return message
}

// Capture the client value before the bridge changes the upstream request.
// Responses metadata should describe the client's request, while the forwarding
// result separately retains the actual upstream effort for administrator audit.
func bindKiloBridgeResponseEffort(ctx context.Context, c *gin.Context, account *Account, body []byte) {
	if c == nil {
		return
	}
	c.Set(kiloBridgeResponseEffortKey, "")
	if !account.IsKiloBridgeEnabled() {
		return
	}
	if c.Request != nil {
		ctx = c.Request.Context()
	}
	requested := explicitRequestedReasoningEffortFromBody(body)
	if original := RequestedReasoningEffortFromContext(ctx); original != nil {
		requested = *original
	}
	if kiloUpstreamReasoningEffort(requested) != requested {
		c.Set(kiloBridgeResponseEffortKey, requested)
		// Keep this across account failover so recovered error records cannot
		// expose a previous Kilo attempt through the final account's context.
		c.Set(kiloBridgeErrorPrivacyKey, true)
	}
}

func kiloBridgeResponseEffort(c *gin.Context, account *Account) string {
	if c == nil || !account.IsKiloBridgeEnabled() {
		return ""
	}
	return c.GetString(kiloBridgeResponseEffortKey)
}

// Rewrite only protocol metadata, never generated text, tool input, or usage.
func rewriteKiloBridgeResponseJSON(c *gin.Context, account *Account, body []byte) ([]byte, error) {
	requested := kiloBridgeResponseEffort(c, account)
	if requested == "" || !gjson.ValidBytes(body) {
		return body, nil
	}
	for _, path := range []string{"reasoning.effort", "reasoning_effort", "response.reasoning.effort", "response.reasoning_effort"} {
		value := gjson.GetBytes(body, path)
		if value.Type != gjson.String || value.String() != kiloUpstreamReasoningEffort(requested) {
			continue
		}
		var err error
		body, err = sjson.SetBytes(body, path, requested)
		if err != nil {
			return nil, err
		}
	}
	return sanitizeKiloBridgeClientErrorJSON(c, body)
}

func sanitizeKiloBridgeClientErrorJSON(c *gin.Context, body []byte) ([]byte, error) {
	if c == nil || !c.GetBool(kiloBridgeErrorPrivacyKey) || !gjson.ValidBytes(body) {
		return body, nil
	}
	paths := []string{"error.message", "response.error.message"}
	if gjson.GetBytes(body, "type").String() == "error" {
		paths = append(paths, "message")
	}
	for _, path := range paths {
		value := gjson.GetBytes(body, path)
		if value.Type != gjson.String {
			continue
		}
		message := sanitizeKiloBridgeClientErrorMessage(c, value.String())
		if message == value.String() {
			continue
		}
		var err error
		body, err = sjson.SetBytes(body, path, message)
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

// SanitizeKiloBridgeClientErrorLog protects the fields returned by the user's
// error history. UpstreamErrorMessage, UpstreamErrorDetail and UpstreamErrors
// remain untouched for administrator troubleshooting.
func SanitizeKiloBridgeClientErrorLog(c *gin.Context, entry *OpsInsertErrorLogInput) {
	if c == nil || entry == nil || !c.GetBool(kiloBridgeErrorPrivacyKey) {
		return
	}
	entry.ErrorMessage = sanitizeKiloBridgeClientErrorMessage(c, entry.ErrorMessage)
	body := []byte(entry.ErrorBody)
	if !bodyHasSSEFraming(body) {
		if converted, err := sanitizeKiloBridgeClientErrorJSON(c, body); err == nil {
			entry.ErrorBody = string(converted)
		}
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), maxOpenAIConcatenatedJSONBytes)
	frames := newOpenAICacheConversionSSEScanner(newOpenAISSEJSONDocumentScanner(scanner), true)
	var rewritten strings.Builder
	for frames.Scan() {
		line := frames.Text()
		if data, ok := extractOpenAISSEDataLine(line); ok {
			converted, err := sanitizeKiloBridgeClientErrorJSON(c, []byte(data))
			if err != nil {
				return
			}
			line = "data: " + string(converted)
		}
		_, _ = rewritten.WriteString(line)
		_ = rewritten.WriteByte('\n')
	}
	if frames.Err() == nil {
		entry.ErrorBody = rewritten.String()
	}
}

func rewriteKiloBridgeResponseHTTPBody(c *gin.Context, account *Account, body []byte) ([]byte, error) {
	if kiloBridgeResponseEffort(c, account) == "" {
		return body, nil
	}
	if !bodyHasSSEFraming(body) {
		return rewriteKiloBridgeResponseJSON(c, account, body)
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), maxOpenAIConcatenatedJSONBytes)
	frames := newOpenAICacheConversionSSEScanner(newOpenAISSEJSONDocumentScanner(scanner), true)
	var rewritten strings.Builder
	for frames.Scan() {
		line := frames.Text()
		if data, ok := extractOpenAISSEDataLine(line); ok {
			converted, err := rewriteKiloBridgeResponseJSON(c, account, []byte(data))
			if err != nil {
				return nil, err
			}
			if !bytes.Equal(converted, []byte(data)) {
				line = "data: " + string(converted)
			}
		}
		_, _ = rewritten.WriteString(line)
		_ = rewritten.WriteByte('\n')
	}
	if err := frames.Err(); err != nil {
		return nil, err
	}
	return []byte(rewritten.String()), nil
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// buildAccountResponsesURL keeps the configured host/path and account proxy.
// Kilo's gateway base ends in /api/gateway rather than /v1.
func buildAccountResponsesURL(account *Account, base string) string {
	if account.IsKiloBridgeEnabled() {
		return buildOpenAIEndpointURL(base, "/responses")
	}
	return buildOpenAIResponsesURLForPlatform(account.Platform, base)
}

// translateKiloResponsesRequest adapts only the request wire format. In
// particular, model and opaque tool arguments/results are never remapped or
// scrubbed here. The latter may contain an encrypted subagent task.
func translateKiloResponsesRequest(account *Account, body []byte) ([]byte, error) {
	if !account.IsKiloBridgeEnabled() {
		return body, nil
	}
	var request map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &request); err != nil {
		return nil, fmt.Errorf("decode Kilo bridge request: %w", err)
	}
	if request == nil {
		return nil, fmt.Errorf("kilo bridge request must be an object")
	}
	if reasoning, ok := request["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok {
			reasoning["effort"] = kiloUpstreamReasoningEffort(effort)
		}
	}
	if effort, ok := request["reasoning_effort"].(string); ok {
		request["reasoning_effort"] = kiloUpstreamReasoningEffort(effort)
	}
	if include, ok := request["include"].([]any); ok {
		filtered := make([]any, 0, len(include))
		for _, value := range include {
			if field, ok := value.(string); !ok || !strings.Contains(field, "encrypted_content") {
				filtered = append(filtered, value)
			}
		}
		if len(filtered) == 0 {
			delete(request, "include")
		} else {
			request["include"] = filtered
		}
	}
	if input, ok := request["input"].([]any); ok {
		for index, raw := range input {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch item["type"] {
			case "function_call":
				item["type"] = "custom_tool_call"
				rewriteKiloItemID(item, "fc_", "ctc_")
				if arguments, exists := item["arguments"]; exists {
					item["input"] = arguments
					delete(item, "arguments")
				}
			case "function_call_output":
				item["type"] = "custom_tool_call_output"
				rewriteKiloItemID(item, "fco_", "ctco_")
				if output, exists := item["output"]; exists {
					if _, array := output.([]any); !array {
						text, isString := output.(string)
						if !isString {
							encoded, err := json.Marshal(output)
							if err != nil {
								return nil, fmt.Errorf("encode Kilo tool output: %w", err)
							}
							text = string(encoded)
						}
						item["output"] = []any{map[string]any{"type": "input_text", "text": text}}
					}
				}
			case "agent_message":
				content := make([]any, 0)
				if parts, ok := item["content"].([]any); ok {
					for _, part := range parts {
						if block, ok := part.(map[string]any); ok && block["type"] == "input_text" {
							content = append(content, block)
						}
					}
				}
				input[index] = map[string]any{"type": "message", "role": "user", "content": content}
			case "reasoning":
				delete(item, "encrypted_content")
			}
		}
	}
	return marshalOpenAIUpstreamJSON(request)
}

func rewriteKiloItemID(item map[string]any, from, to string) {
	if id, ok := item["id"].(string); ok && strings.HasPrefix(id, from) {
		item["id"] = to + strings.TrimPrefix(id, from)
	}
}

// Kilo accepts its own replay IDs and reasoning summaries without ciphertext.
// Standard OpenAI replay cleanup would discard them before translation.
func sanitizeOpenAIInputItemIDsForAccount(account *Account, body []byte) ([]byte, bool, error) {
	if account.IsKiloBridgeEnabled() {
		return body, false, nil
	}
	return sanitizeOpenAIResponsesInputItemIDs(body)
}

func normalizeOpenAIAPIKeyReasoningReplayForAccount(account *Account, body []byte, knownStoreFalse bool) ([]byte, bool, error) {
	if account.IsKiloBridgeEnabled() {
		return body, false, nil
	}
	return normalizeOpenAIAPIKeyStoreFalseReasoningReplay(body, knownStoreFalse)
}

func kiloUpstreamReasoningEffort(effort string) string {
	if effort == "ultra" || effort == "persistent" {
		return "max"
	}
	return effort
}

// Keep the requested value for customer pricing/display and the actual upstream
// value for upstream accounting. The usage DTO exposes these separately.
func stampKiloBridgeUsage(ctx context.Context, account *Account, inbound []byte, result *OpenAIForwardResult) {
	if !account.IsKiloBridgeEnabled() || result == nil {
		return
	}
	if requested := RequestedReasoningEffortFromContext(ctx); requested != nil {
		result.RequestedReasoningEffort = requested
	} else if result.RequestedReasoningEffort == nil {
		for _, path := range []string{"reasoning.effort", "reasoning_effort", "output_config.effort"} {
			if effort := gjson.GetBytes(inbound, path); effort.Type == gjson.String && effort.String() != "" {
				requested := effort.String()
				result.RequestedReasoningEffort = &requested
				break
			}
		}
	}
	if result.ReasoningEffort != nil {
		forwarded := kiloUpstreamReasoningEffort(*result.ReasoningEffort)
		result.ReasoningEffort = &forwarded
	}
}

// Kilo's wire adaptation must not select the customer's effort multiplier.
// Use a pricing-only copy so stored upstream metadata and account cost rules
// continue to describe the request actually sent to the provider.
func openAICustomerBillingResult(account *Account, result *OpenAIForwardResult) *OpenAIForwardResult {
	if result == nil || !account.IsKiloBridgeEnabled() || result.RequestedReasoningEffort == nil {
		return result
	}
	if strings.TrimSpace(*result.RequestedReasoningEffort) == "" {
		return result
	}
	billingResult := *result
	billingResult.ReasoningEffort = result.RequestedReasoningEffort
	return &billingResult
}

func extractOpenAIReasoningEffortForAccount(account *Account, body []byte, models ...string) *string {
	if account.IsKiloBridgeEnabled() {
		for _, path := range []string{"reasoning.effort", "reasoning_effort"} {
			if effort := gjson.GetBytes(body, path); effort.Type == gjson.String && effort.String() != "" {
				value := kiloUpstreamReasoningEffort(effort.String())
				return &value
			}
		}
	}
	return extractOpenAIReasoningEffortFromBody(body, models...)
}

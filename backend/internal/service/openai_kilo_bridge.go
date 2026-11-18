package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
)

// buildAccountResponsesURL keeps the configured host/path and account proxy.
// Kilo gateway profiles keep their own configured API path.
func buildAccountResponsesURL(account *Account, base string) string {
	if account.IsKiloBridgeEnabled() {
		return buildOpenAIEndpointURL(base, "/responses")
	}
	return buildOpenAIResponsesURLForPlatform(account.Platform, base)
}

// translateKiloResponsesRequest adapts the request wire format. Only the
// managed CLI profile adds a provider model prefix; opaque tool arguments and
// results remain intact because they may contain an encrypted subagent task.
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
	if model, ok := request["model"].(string); ok {
		request["model"] = kiloGatewayUpstreamModel(account, model)
	}
	if input, ok := request["input"].([]any); ok {
		var err error
		input, err = adaptKiloAgentMessages(input, isKiloCLIGatewayAccount(account))
		if err != nil {
			return nil, err
		}
		request["input"] = input
		for _, raw := range input {
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
			case "reasoning":
				delete(item, "encrypted_content")
			}
		}
	}
	return marshalOpenAIUpstreamJSON(request)
}

// Agent communications are received history, not new tool requests. Pair each
// recorded transport call with its result before appending the readable message;
// the bridge never registers or executes collaboration tools.
func adaptKiloAgentMessages(input []any, strictRouting bool) ([]any, error) {
	adapted := make([]any, 0, len(input))
	for index, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || item["type"] != "agent_message" {
			adapted = append(adapted, raw)
			continue
		}
		author, authorOK := item["author"].(string)
		recipient, recipientOK := item["recipient"].(string)
		parts, ok := item["content"].([]any)
		if !ok {
			return nil, fmt.Errorf("unsupported agent_message content at input[%d]: expected an array; message was not forwarded or discarded", index)
		}
		if !authorOK || !recipientOK {
			// Legacy plaintext history predates agent routing fields. Only that
			// shape is accepted without a route; ciphertext must remain routable.
			_, hasAuthor := item["author"]
			_, hasRecipient := item["recipient"]
			if strictRouting || hasAuthor || hasRecipient {
				return nil, fmt.Errorf("agent_message at input[%d] requires author and recipient strings", index)
			}
			for _, part := range parts {
				block, ok := part.(map[string]any)
				if !ok || block["type"] != "input_text" {
					return nil, fmt.Errorf("agent_message at input[%d] requires author and recipient strings for non-plaintext content", index)
				}
			}
		}
		routing := make(map[string]any, len(item)-2)
		for key, value := range item {
			if key != "type" && key != "content" {
				routing[key] = value
			}
		}
		routingJSON, err := json.Marshal(routing)
		if err != nil {
			return nil, fmt.Errorf("encode Kilo agent routing: %w", err)
		}
		message := make([]any, 0, len(parts)+1)
		if len(routing) != 0 {
			message = append(message, map[string]any{"type": "input_text", "text": "Received agent communication:\n" + string(routingJSON)})
		}
		for partIndex, part := range parts {
			block, ok := part.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("unsupported agent_message content at input[%d].content[%d]: expected an object; message was not forwarded or discarded", index, partIndex)
			}
			switch block["type"] {
			case "input_text":
				if _, ok := block["text"].(string); !ok {
					return nil, fmt.Errorf("unsupported agent_message input_text at input[%d].content[%d]: expected text string", index, partIndex)
				}
				message = append(message, block)
			case "encrypted_content":
				ciphertext, ok := block["encrypted_content"].(string)
				if !ok {
					return nil, fmt.Errorf("unsupported agent_message encrypted_content at input[%d].content[%d]: expected ciphertext string", index, partIndex)
				}
				identity, err := json.Marshal([]any{index, partIndex, item})
				if err != nil {
					return nil, fmt.Errorf("encode Kilo agent history identity: %w", err)
				}
				digest := sha256.Sum256(identity)
				callID := "call_kilo_agent_" + hex.EncodeToString(digest[:16])
				arguments, err := json.Marshal(map[string]any{"target": recipient, "message": ciphertext})
				if err != nil {
					return nil, fmt.Errorf("encode Kilo agent transport arguments: %w", err)
				}
				result := map[string]any{"transport": "received_agent_message", "author": author, "recipient": recipient, "content_index": partIndex}
				// Preserve extension metadata without emitting native encrypted input.
				metadata := make(map[string]any)
				for key, value := range block {
					if key != "type" && key != "encrypted_content" {
						metadata[key] = value
					}
				}
				if len(metadata) != 0 {
					result["content_metadata"] = metadata
				}
				output, err := json.Marshal(result)
				if err != nil {
					return nil, fmt.Errorf("encode Kilo agent transport result: %w", err)
				}
				adapted = append(adapted,
					map[string]any{"type": "function_call", "call_id": callID, "namespace": "collaboration", "name": "send_message", "arguments": string(arguments)},
					map[string]any{"type": "function_call_output", "call_id": callID, "output": string(output)},
				)
				message = append(message, map[string]any{"type": "input_text", "text": fmt.Sprintf("The content of this part is the message argument of recorded transport call %s.", callID)})
			default:
				return nil, fmt.Errorf("unsupported agent_message content type at input[%d].content[%d]; message was not forwarded or discarded", index, partIndex)
			}
		}
		adapted = append(adapted, map[string]any{"type": "message", "role": "user", "content": message})
	}
	return adapted, nil
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
	if isKiloCLIGatewayAccount(account) {
		if err := bindKiloGatewayResponseSession(ctx, result); err != nil {
			// The upstream turn has already completed. Surface the metadata
			// failure operationally, without replaying or altering usage.
			logger.LegacyPrintf("service.openai_gateway", "[Kilo] Shared wire-session binding failed (account_id=%d)", account.ID)
		}
		if identity, ok := ctx.Value(kiloGatewaySessionContextKeyType{}).(kiloGatewaySessionContext); ok && identity.accountID == account.ID && identity.model != "" {
			result.UpstreamModel = identity.model
		}
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

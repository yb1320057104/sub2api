package service

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
)

const kiloToolSearchFunctionName = "__kilo2api_tool_search"

// KiloToolSearchError is safe to expose at the compatibility boundary. It never
// contains the client's tool definitions or the provider's generated arguments.
type KiloToolSearchError struct {
	Status  int
	Code    string
	Param   string
	Message string
}

func (e *KiloToolSearchError) Error() string { return e.Message }

func invalidKiloToolSearch() error {
	return &KiloToolSearchError{Status: 400, Code: "invalid_tool_search", Param: "tools", Message: "Invalid client tool search definition or history."}
}

func invalidKiloToolSearchResponse() error {
	return &KiloToolSearchError{Status: 502, Code: "invalid_upstream_response", Param: "tools", Message: "Invalid upstream tool search response."}
}

func invalidKiloToolSearchArguments() error {
	return &KiloToolSearchError{Status: 502, Code: "invalid_tool_search_arguments", Param: "tools", Message: "Invalid upstream tool search arguments."}
}

// KiloToolSearchAdapter belongs to one forwarding attempt. Feed complete SSE
// events in wire order; do not share its pending-item state across attempts.
type KiloToolSearchAdapter struct {
	active   bool
	original map[string]any
	items    map[string]struct{}
}

func (a *KiloToolSearchAdapter) Active() bool { return a != nil && a.active }

// PrepareKiloToolSearch lowers only explicit client-executed tool search. Run it
// after translateKiloResponsesRequest so its compatibility function history is
// not retyped as ordinary Kilo custom-tool history. The input bytes are never
// modified, and an inactive request is returned byte-for-byte.
func PrepareKiloToolSearch(body []byte) ([]byte, *KiloToolSearchAdapter, error) {
	value, err := decodeKiloToolSearchJSON(body)
	request, ok := value.(map[string]any)
	if err != nil || !ok || request == nil {
		return nil, nil, invalidKiloToolSearch()
	}
	a := &KiloToolSearchAdapter{items: make(map[string]struct{})}
	tools := []any{}
	if raw, exists := request["tools"]; exists {
		tools, ok = raw.([]any)
		if !ok {
			return body, a, nil
		}
	}
	inputs, _ := request["input"].([]any)
	declarations := append([]any(nil), tools...)
	for _, raw := range inputs {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["type"] == "additional_tools" || item["type"] == "tool_search_output" {
			if discovered, ok := item["tools"].([]any); ok {
				declarations = append(declarations, discovered...)
			}
		}
		if (item["type"] == "tool_search_call" || item["type"] == "tool_search_output") && item["execution"] == "client" {
			a.active = true
		}
	}
	for _, raw := range declarations {
		if tool, ok := raw.(map[string]any); ok && tool["type"] == "tool_search" && tool["execution"] == "client" {
			a.active = true
		}
	}
	if !a.active {
		return body, a, nil
	}
	if err := checkKiloToolSearchNames(declarations); err != nil {
		return nil, nil, err
	}
	lowered, err := a.lowerTools(tools)
	if err != nil {
		return nil, nil, err
	}
	request["tools"] = lowered
	if choice, ok := request["tool_choice"].(map[string]any); ok {
		switch choice["type"] {
		case "tool_search":
			request["tool_choice"] = map[string]any{"type": "function", "name": kiloToolSearchFunctionName}
		case "allowed_tools":
			if allowed, ok := choice["tools"].([]any); ok {
				for i, raw := range allowed {
					tool, ok := raw.(map[string]any)
					if !ok {
						return nil, nil, invalidKiloToolSearch()
					}
					if tool["type"] == "tool_search" {
						allowed[i] = map[string]any{"type": "function", "name": kiloToolSearchFunctionName}
					}
				}
			}
		}
	}
	if inputs != nil {
		converted := make([]any, 0, len(inputs))
		calls := make(map[string]bool)
		for _, raw := range inputs {
			item, ok := raw.(map[string]any)
			if !ok {
				converted = append(converted, raw)
				continue
			}
			kind, _ := item["type"].(string)
			switch {
			case kind == "function_call" && item["name"] == kiloToolSearchFunctionName:
				return nil, nil, invalidKiloToolSearch()
			case kind == "tool_search_call" && item["execution"] == "client":
				callID, ok := item["call_id"].(string)
				arguments, object := item["arguments"].(map[string]any)
				if !ok || callID == "" || calls[callID] || !object {
					return nil, nil, invalidKiloToolSearch()
				}
				calls[callID] = true
				encoded, err := marshalKiloToolSearchJSON(arguments)
				if err != nil {
					return nil, nil, invalidKiloToolSearch()
				}
				mapped := map[string]any{"type": "function_call", "name": kiloToolSearchFunctionName, "call_id": callID, "arguments": string(encoded)}
				// Native search item IDs are invalid on a function-call item.
				if status, exists := item["status"]; exists {
					mapped["status"] = status
				}
				converted = append(converted, mapped)
			case kind == "tool_search_output" && item["execution"] == "client":
				callID, ok := item["call_id"].(string)
				discovered, list := item["tools"].([]any)
				if !ok || !calls[callID] || !list {
					return nil, nil, invalidKiloToolSearch()
				}
				delete(calls, callID)
				loaded, err := a.lowerTools(discovered)
				if err != nil {
					return nil, nil, err
				}
				activateKiloToolSearchTools(loaded)
				encoded, err := marshalKiloToolSearchJSON(map[string]any{"tools": loaded})
				if err != nil {
					return nil, nil, invalidKiloToolSearch()
				}
				converted = append(converted, map[string]any{"type": "function_call_output", "call_id": callID, "output": string(encoded)})
				if len(loaded) != 0 {
					converted = append(converted, map[string]any{"type": "additional_tools", "role": "developer", "tools": loaded})
				}
			case kind == "additional_tools":
				if declared, ok := item["tools"].([]any); ok {
					adapted, err := a.lowerTools(declared)
					if err != nil {
						return nil, nil, err
					}
					item["tools"] = adapted
				}
				converted = append(converted, item)
			default:
				converted = append(converted, item)
			}
		}
		request["input"] = converted
	}
	patched, err := marshalKiloToolSearchJSON(request)
	if err != nil {
		return nil, nil, invalidKiloToolSearch()
	}
	return patched, a, nil
}

func checkKiloToolSearchNames(items []any) error {
	for _, raw := range items {
		tool, ok := raw.(map[string]any)
		if !ok || tool["name"] == kiloToolSearchFunctionName {
			return invalidKiloToolSearch()
		}
		if tool["type"] == "namespace" {
			children := []any{}
			if raw, exists := tool["tools"]; exists {
				children, ok = raw.([]any)
				if !ok {
					return invalidKiloToolSearch()
				}
			}
			if err := checkKiloToolSearchNames(children); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *KiloToolSearchAdapter) lowerTools(tools []any) ([]any, error) {
	converted := make([]any, 0, len(tools))
	found := false
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			return nil, invalidKiloToolSearch()
		}
		if tool["type"] != "tool_search" || tool["execution"] != "client" {
			converted = append(converted, tool)
			continue
		}
		parameters, ok := tool["parameters"].(map[string]any)
		if found || !ok || a.original != nil && !reflect.DeepEqual(tool, a.original) {
			return nil, invalidKiloToolSearch()
		}
		found = true
		a.original = cloneKiloToolSearchValue(tool).(map[string]any)
		description, exists := tool["description"]
		if !exists {
			description = "Search for available tools."
		}
		converted = append(converted, map[string]any{"type": "function", "name": kiloToolSearchFunctionName, "description": description, "parameters": cloneKiloToolSearchValue(parameters), "strict": false})
	}
	return converted, nil
}

func activateKiloToolSearchTools(tools []any) {
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		delete(tool, "defer_loading")
		if tool["type"] == "namespace" {
			if children, ok := tool["tools"].([]any); ok {
				activateKiloToolSearchTools(children)
			}
		}
	}
}

// TransformJSON restores a complete non-streaming Responses object. Invalid
// completed search arguments produce a 502 error, never an executable call.
func (a *KiloToolSearchAdapter) TransformJSON(body []byte) ([]byte, error) {
	if !a.Active() {
		return body, nil
	}
	value, err := decodeKiloToolSearchJSON(body)
	if err != nil {
		return nil, invalidKiloToolSearchResponse()
	}
	response, ok := value.(map[string]any)
	if !ok {
		return body, nil
	}
	changed, err := a.restoreResponse(response)
	if err != nil {
		return nil, err
	}
	if !changed {
		return body, nil
	}
	return marshalKiloToolSearchJSON(response)
}

func (a *KiloToolSearchAdapter) restoreItem(raw any, pending bool) (any, bool, error) {
	item, ok := raw.(map[string]any)
	if !ok || item["type"] != "function_call" || item["name"] != kiloToolSearchFunctionName || kiloToolSearchTruthy(item["namespace"]) {
		return raw, false, nil
	}
	arguments := map[string]any{}
	if !pending {
		value := item["arguments"]
		if text, ok := value.(string); ok {
			var err error
			value, err = decodeKiloToolSearchJSON([]byte(text))
			if err != nil {
				return nil, false, invalidKiloToolSearchArguments()
			}
		}
		var object bool
		arguments, object = value.(map[string]any)
		if !object || arguments == nil {
			return nil, false, invalidKiloToolSearchArguments()
		}
	}
	mapped := map[string]any{"type": "tool_search_call", "execution": "client", "arguments": arguments}
	for _, key := range []string{"id", "call_id", "status"} {
		if value, exists := item[key]; exists {
			mapped[key] = value
		}
	}
	if pending {
		mapped["status"] = "in_progress"
	}
	return mapped, true, nil
}

func (a *KiloToolSearchAdapter) restoreResponse(response map[string]any) (bool, error) {
	changed := false
	if outputs, ok := response["output"].([]any); ok {
		for i, raw := range outputs {
			mapped, restored, err := a.restoreItem(raw, false)
			if err != nil {
				return false, err
			}
			outputs[i] = mapped
			changed = changed || restored
		}
	}
	if tools, ok := response["tools"].([]any); ok && a.original != nil {
		for i, raw := range tools {
			if tool, ok := raw.(map[string]any); ok && tool["type"] == "function" && tool["name"] == kiloToolSearchFunctionName {
				tools[i] = cloneKiloToolSearchValue(a.original)
				changed = true
			}
		}
	}
	if choice, ok := response["tool_choice"].(map[string]any); ok {
		if choice["type"] == "function" && choice["name"] == kiloToolSearchFunctionName {
			response["tool_choice"] = map[string]any{"type": "tool_search"}
			changed = true
		} else if choice["type"] == "allowed_tools" {
			if allowed, ok := choice["tools"].([]any); ok {
				for i, raw := range allowed {
					if tool, ok := raw.(map[string]any); ok && tool["type"] == "function" && tool["name"] == kiloToolSearchFunctionName {
						allowed[i] = map[string]any{"type": "tool_search"}
						changed = true
					}
				}
			}
		}
	}
	return changed, nil
}

// TransformSSE accepts either one complete SSE frame or its decoded JSON data
// payload and returns the same wire form. A nil result means suppress this
// event. Call it before writing: partial function arguments must stay private
// until output_item.done delivers a validated object. This is not a network
// chunk parser; the caller must assemble an entire event first.
func (a *KiloToolSearchAdapter) TransformSSE(raw []byte) ([]byte, error) {
	if !a.Active() {
		return raw, nil
	}
	data := bytes.TrimSpace(raw)
	framed := !bytes.HasPrefix(data, []byte("{"))
	eventName := ""
	if framed {
		var parts [][]byte
		lines := bytes.ReplaceAll(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
		for _, line := range bytes.Split(lines, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data:")) {
				parts = append(parts, bytes.TrimLeft(line[5:], " "))
			} else if bytes.HasPrefix(line, []byte("event:")) {
				eventName = strings.TrimPrefix(string(line[6:]), " ")
			}
		}
		data = bytes.Join(parts, []byte("\n"))
		if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			return raw, nil
		}
	}
	value, err := decodeKiloToolSearchJSON(data)
	event, ok := value.(map[string]any)
	if err != nil || !ok || event == nil {
		return nil, invalidKiloToolSearchResponse()
	}
	if eventName == "error" {
		event["type"] = "error"
	} else if eventName != "" && !kiloToolSearchTruthy(event["type"]) {
		event["type"] = eventName
	}
	kind, _ := event["type"].(string)
	changed := false
	if kind == "response.output_item.added" || kind == "response.output_item.done" {
		if item, ok := event["item"].(map[string]any); ok && item["type"] == "function_call" && item["name"] == kiloToolSearchFunctionName && !kiloToolSearchTruthy(item["namespace"]) {
			if id, ok := item["id"].(string); ok {
				a.items[id] = struct{}{}
			}
			mapped, _, err := a.restoreItem(item, kind == "response.output_item.added")
			if err != nil {
				return nil, err
			}
			event["item"] = mapped
			changed = true
		}
	}
	if kind == "response.function_call_arguments.delta" || kind == "response.function_call_arguments.done" {
		if id, ok := event["item_id"].(string); ok {
			if _, tracked := a.items[id]; tracked {
				return nil, nil
			}
		}
	}
	if response, ok := event["response"].(map[string]any); ok {
		restored, err := a.restoreResponse(response)
		if err != nil {
			return nil, err
		}
		changed = changed || restored
	}
	if !changed {
		return raw, nil
	}
	encoded, err := marshalKiloToolSearchJSON(event)
	if err != nil {
		return nil, invalidKiloToolSearchResponse()
	}
	if !framed {
		return encoded, nil
	}
	var result bytes.Buffer
	if kind != "" {
		result.WriteString("event: " + kind + "\n")
	}
	result.WriteString("data: ")
	result.Write(encoded)
	result.WriteString("\n\n")
	return result.Bytes(), nil
}

func decodeKiloToolSearchJSON(body []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, invalidKiloToolSearchResponse()
		}
		return nil, err
	}
	return value, nil
}

func marshalKiloToolSearchJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func cloneKiloToolSearchValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(typed))
		for key, child := range typed {
			copy[key] = cloneKiloToolSearchValue(child)
		}
		return copy
	case []any:
		copy := make([]any, len(typed))
		for i, child := range typed {
			copy[i] = cloneKiloToolSearchValue(child)
		}
		return copy
	default:
		return value
	}
}

func kiloToolSearchTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case json.Number:
		number, err := typed.Float64()
		return err != nil || number != 0
	case []any:
		return len(typed) != 0
	case map[string]any:
		return len(typed) != 0
	default:
		return true
	}
}

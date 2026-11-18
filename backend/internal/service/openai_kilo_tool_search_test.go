package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func kiloToolSearchTestDefinition() map[string]any {
	return map[string]any{"type": "tool_search", "execution": "client", "description": "Find tools 原文", "parameters": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []any{"query"}}}
}

func kiloToolSearchTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func kiloToolSearchTestObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return value
}

func kiloToolSearchTestPrepare(t *testing.T, body any) ([]byte, *KiloToolSearchAdapter) {
	t.Helper()
	patched, adapter, err := PrepareKiloToolSearch(kiloToolSearchTestJSON(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if !adapter.Active() {
		t.Fatal("client search adapter is inactive")
	}
	return patched, adapter
}

func TestKiloToolSearchDeclarationsAndChoiceRoundTrip(t *testing.T) {
	for _, choice := range []any{
		map[string]any{"type": "tool_search"},
		map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{map[string]any{"type": "tool_search"}, map[string]any{"type": "function", "name": "example"}}},
	} {
		t.Run(kiloToolSearchTestObject(t, kiloToolSearchTestJSON(t, choice))["type"].(string), func(t *testing.T) {
			function := map[string]any{"type": "function", "name": "example", "parameters": map[string]any{"type": "object"}}
			search := kiloToolSearchTestDefinition()
			search["parameters"].(map[string]any)["x-large"] = json.Number("900719925474099312345")
			tools := []any{function, search, map[string]any{"type": "custom", "name": "custom", "format": map[string]any{"type": "text"}}, map[string]any{"type": "namespace", "name": "ns", "tools": []any{function}}, map[string]any{"type": "web_search"}, map[string]any{"type": "tool_search", "execution": "server"}}
			body := kiloToolSearchTestJSON(t, map[string]any{"tools": tools, "tool_choice": choice, "parallel_tool_calls": true, "input": "unchanged context", "nonce": json.Number("900719925474099312346")})
			before := append([]byte(nil), body...)
			patched, adapter, err := PrepareKiloToolSearch(body)
			if err != nil || !adapter.Active() {
				t.Fatalf("prepare: active=%v error=%v", adapter.Active(), err)
			}
			if !bytes.Equal(body, before) || !bytes.Contains(patched, []byte("900719925474099312346")) || !bytes.Contains(patched, []byte("900719925474099312345")) {
				t.Fatal("source mutated or a JSON number lost precision")
			}
			got := kiloToolSearchTestObject(t, patched)
			original := kiloToolSearchTestObject(t, body)
			lowered := got["tools"].([]any)
			if len(lowered) != len(tools) {
				t.Fatal("unrelated tool declaration was removed")
			}
			proxy := lowered[1].(map[string]any)
			if proxy["name"] != kiloToolSearchFunctionName || proxy["type"] != "function" || proxy["strict"] != false || !reflect.DeepEqual(proxy["parameters"], original["tools"].([]any)[1].(map[string]any)["parameters"]) {
				t.Fatal("original client search schema was not retained")
			}
			for _, index := range []int{0, 2, 3, 4, 5} {
				if !reflect.DeepEqual(lowered[index], original["tools"].([]any)[index]) {
					t.Fatalf("unrelated/custom/namespace/hosted tool %d changed", index)
				}
			}
			restored, err := adapter.TransformJSON(kiloToolSearchTestJSON(t, map[string]any{"output": []any{}, "tools": lowered, "tool_choice": got["tool_choice"], "usage": map[string]any{"input_tokens": 7}}))
			if err != nil {
				t.Fatal(err)
			}
			response := kiloToolSearchTestObject(t, restored)
			if !reflect.DeepEqual(response["tools"], original["tools"]) || !reflect.DeepEqual(response["tool_choice"], original["tool_choice"]) || response["usage"].(map[string]any)["input_tokens"] != float64(7) {
				t.Fatal("response echo did not reverse the adapter or usage changed")
			}
		})
	}
}

func TestKiloToolSearchInactiveRequestsStayByteExact(t *testing.T) {
	for _, body := range []string{
		"{ \"tools\": [{\"type\":\"tool_search\",\"execution\":\"server\"}], \"input\": [] }",
		`{"tools":[{"type":"tool_search"}]}`,
		`{"tools":[{"type":"function","name":"tool_search","parameters":{}}]}`,
		`{"tools":null,"input":[{"type":"tool_search_call","execution":"client"}]}`,
	} {
		patched, adapter, err := PrepareKiloToolSearch([]byte(body))
		if err != nil || adapter.Active() || string(patched) != body {
			t.Fatalf("inactive request changed: active=%v error=%v", adapter.Active(), err)
		}
		if out, err := adapter.TransformJSON([]byte("not parsed while inactive")); err != nil || string(out) != "not parsed while inactive" {
			t.Fatal("inactive response was parsed or changed")
		}
		if out, err := adapter.TransformSSE([]byte(": keepalive\n\n")); err != nil || string(out) != ": keepalive\n\n" {
			t.Fatal("inactive stream was changed")
		}
	}
}

func TestKiloToolSearchHistoryAndDynamicDefinitions(t *testing.T) {
	search := kiloToolSearchTestDefinition()
	loaded := []any{map[string]any{"type": "namespace", "name": "ns", "defer_loading": true, "tools": []any{map[string]any{"type": "function", "name": "example", "defer_loading": true, "parameters": map[string]any{"type": "object"}}}}}
	body := kiloToolSearchTestJSON(t, map[string]any{"tools": []any{search}, "input": []any{
		map[string]any{"role": "user", "content": "unchanged"},
		map[string]any{"type": "tool_search_call", "id": "tsc_old", "execution": "client", "call_id": "call_a", "status": "completed", "arguments": map[string]any{"query": "test"}},
		map[string]any{"type": "tool_search_output", "execution": "client", "call_id": "call_a", "tools": loaded},
		map[string]any{"type": "function_call", "name": "example", "call_id": "next", "arguments": "{}"},
	}})
	before := append([]byte(nil), body...)
	patched, _, err := PrepareKiloToolSearch(body)
	if err != nil || !bytes.Equal(body, before) {
		t.Fatalf("history preparation failed or mutated source: %v", err)
	}
	input := kiloToolSearchTestObject(t, patched)["input"].([]any)
	if len(input) != 5 || input[0].(map[string]any)["content"] != "unchanged" {
		t.Fatal("history order was not preserved")
	}
	call := input[1].(map[string]any)
	if call["type"] != "function_call" || call["name"] != kiloToolSearchFunctionName || call["call_id"] != "call_a" || call["status"] != "completed" {
		t.Fatal("search history call not lowered")
	}
	if _, exists := call["id"]; exists {
		t.Fatal("native search item ID leaked onto function call")
	}
	output := input[2].(map[string]any)
	definitions := kiloToolSearchTestObject(t, []byte(output["output"].(string)))["tools"]
	additional := input[3].(map[string]any)
	if output["type"] != "function_call_output" || output["call_id"] != "call_a" || additional["type"] != "additional_tools" || additional["role"] != "developer" || !reflect.DeepEqual(definitions, additional["tools"]) {
		t.Fatal("loaded tools were not paired and activated through additional_tools")
	}
	namespace := additional["tools"].([]any)[0].(map[string]any)
	if _, exists := namespace["defer_loading"]; exists {
		t.Fatal("discovered namespace remained deferred")
	}
	if _, exists := namespace["tools"].([]any)[0].(map[string]any)["defer_loading"]; exists {
		t.Fatal("discovered child remained deferred")
	}
	if input[4].(map[string]any)["name"] != "example" {
		t.Fatal("ordinary function history was changed")
	}
	for _, history := range []any{
		[]any{map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{search}}},
		[]any{map[string]any{"type": "tool_search_call", "execution": "client", "call_id": "c", "arguments": map[string]any{}}, map[string]any{"type": "tool_search_output", "execution": "client", "call_id": "c", "tools": []any{search}}},
	} {
		patched, _ := kiloToolSearchTestPrepare(t, map[string]any{"input": history})
		if !bytes.Contains(patched, []byte(kiloToolSearchFunctionName)) {
			t.Fatal("history-only dynamic search was not adapted")
		}
	}
}

func TestKiloToolSearchInvalidDefinitionsAndHistoryFailClosed(t *testing.T) {
	search := kiloToolSearchTestDefinition()
	cases := []struct {
		name string
		body map[string]any
	}{
		{"reserved name", map[string]any{"tools": []any{search, map[string]any{"type": "function", "name": kiloToolSearchFunctionName}}}},
		{"nested collision", map[string]any{"tools": []any{search, map[string]any{"type": "namespace", "tools": []any{map[string]any{"type": "custom", "name": kiloToolSearchFunctionName}}}}}},
		{"duplicate search", map[string]any{"tools": []any{search, search}}},
		{"invalid parameters", map[string]any{"tools": []any{map[string]any{"type": "tool_search", "execution": "client", "parameters": nil}}}},
		{"orphan output", map[string]any{"tools": []any{search}, "input": []any{map[string]any{"type": "tool_search_output", "execution": "client", "call_id": "orphan", "tools": []any{}}}}},
		{"nonobject arguments", map[string]any{"tools": []any{search}, "input": []any{map[string]any{"type": "tool_search_call", "execution": "client", "call_id": "c", "arguments": "{}"}}}},
		{"duplicate outstanding call", map[string]any{"tools": []any{search}, "input": []any{map[string]any{"type": "tool_search_call", "execution": "client", "call_id": "c", "arguments": map[string]any{}}, map[string]any{"type": "tool_search_call", "execution": "client", "call_id": "c", "arguments": map[string]any{}}}}},
		{"reserved history call", map[string]any{"tools": []any{search}, "input": []any{map[string]any{"type": "function_call", "name": kiloToolSearchFunctionName, "call_id": "c", "arguments": "{}"}}}},
		{"conflicting dynamic search", map[string]any{"tools": []any{search}, "input": []any{map[string]any{"type": "additional_tools", "tools": []any{map[string]any{"type": "tool_search", "execution": "client", "parameters": map[string]any{}, "description": "different"}}}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out, adapter, err := PrepareKiloToolSearch(kiloToolSearchTestJSON(t, test.body))
			var typed *KiloToolSearchError
			if !errors.As(err, &typed) || typed.Status != 400 || typed.Code != "invalid_tool_search" || typed.Param != "tools" || out != nil || adapter != nil {
				t.Fatalf("request did not fail closed: out=%v error=%v", out != nil, err)
			}
		})
	}
}

func TestKiloToolSearchSSEPendingAndCompletedCalls(t *testing.T) {
	_, adapter := kiloToolSearchTestPrepare(t, map[string]any{"tools": []any{kiloToolSearchTestDefinition()}})
	added := []byte("id: untouched-upstream-id\r\nevent: response.output_item.added\r\ndata: {\"item\":{\"id\":\"fc_a\",\"type\":\"function_call\",\"name\":\"__kilo2api_tool_search\",\"call_id\":\"call_a\",\"status\":\"completed\",\"arguments\":\"{\"}}\r\n\r\n")
	out, err := adapter.TransformSSE(added)
	if err != nil || !bytes.Contains(out, []byte(`"arguments":{}`)) || !bytes.Contains(out, []byte(`"status":"in_progress"`)) || !bytes.Contains(out, []byte(`"type":"tool_search_call"`)) || bytes.Contains(out, []byte(kiloToolSearchFunctionName)) {
		t.Fatalf("pending arguments leaked or early call completed: %v", err)
	}
	for _, kind := range []string{"delta", "done"} {
		raw := []byte(`{"type":"response.function_call_arguments.` + kind + `","item_id":"fc_a","delta":"private partial"}`)
		out, err := adapter.TransformSSE(raw)
		if err != nil || out != nil {
			t.Fatalf("search argument fragment was delivered: %v", err)
		}
		other := []byte(strings.ReplaceAll(string(raw), "fc_a", "fc_other"))
		out, err = adapter.TransformSSE(other)
		if err != nil || !bytes.Equal(out, other) {
			t.Fatal("ordinary function arguments were suppressed")
		}
	}
	done := []byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_a","type":"function_call","name":"__kilo2api_tool_search","call_id":"call_a","status":"completed","arguments":"{\"query\":\"hello\",\"n\":900719925474099312345}"}}`)
	out, err = adapter.TransformSSE(done)
	if err != nil || !bytes.Contains(out, []byte("900719925474099312345")) {
		t.Fatalf("completed arguments lost precision: %v", err)
	}
	item := kiloToolSearchTestObject(t, out)["item"].(map[string]any)
	if item["arguments"].(map[string]any)["query"] != "hello" || item["execution"] != "client" || item["call_id"] != "call_a" || item["status"] != "completed" {
		t.Fatal("completed object call was not restored")
	}
	final := kiloToolSearchTestJSON(t, map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{map[string]any{"type": "function_call", "name": kiloToolSearchFunctionName, "arguments": map[string]any{"query": "hello"}}}}})
	out, err = adapter.TransformSSE(append(append([]byte("event: response.completed\ndata: "), final...), []byte("\n\n")...))
	if err != nil || !bytes.Contains(out, []byte(`"type":"tool_search_call"`)) || bytes.Contains(out, []byte(kiloToolSearchFunctionName)) {
		t.Fatalf("terminal response was not restored: %v", err)
	}
	for _, raw := range [][]byte{[]byte(": keepalive\r\n\r\n"), []byte("data: [DONE]\n\n"), []byte(`{"type":"response.output_text.delta","delta":"unchanged"}`)} {
		out, err := adapter.TransformSSE(raw)
		if err != nil || !bytes.Equal(out, raw) {
			t.Fatal("unrelated SSE content changed")
		}
	}
}

func TestKiloToolSearchInvalidCompletedArgumentsNeverDelivered(t *testing.T) {
	_, adapter := kiloToolSearchTestPrepare(t, map[string]any{"tools": []any{kiloToolSearchTestDefinition()}})
	for _, arguments := range []any{"{", "[]", "null", "NaN", "{} {}", nil, []any{}} {
		call := map[string]any{"type": "function_call", "name": kiloToolSearchFunctionName, "arguments": arguments}
		for _, stream := range []bool{false, true} {
			var out []byte
			var err error
			if stream {
				out, err = adapter.TransformSSE(kiloToolSearchTestJSON(t, map[string]any{"type": "response.output_item.done", "item": call}))
			} else {
				out, err = adapter.TransformJSON(kiloToolSearchTestJSON(t, map[string]any{"output": []any{call}}))
			}
			var typed *KiloToolSearchError
			if out != nil || !errors.As(err, &typed) || typed.Status != 502 || typed.Code != "invalid_tool_search_arguments" {
				t.Fatalf("invalid completed call delivered: stream=%v error=%v", stream, err)
			}
		}
	}
	for _, call := range []any{
		map[string]any{"type": "function_call", "name": "tool_search", "arguments": "{"},
		map[string]any{"type": "function_call", "name": kiloToolSearchFunctionName, "namespace": "real_namespace", "arguments": "{"},
	} {
		raw := kiloToolSearchTestJSON(t, map[string]any{"output": []any{call}})
		out, err := adapter.TransformJSON(raw)
		if err != nil || !bytes.Equal(out, raw) {
			t.Fatal("ordinary or namespaced function was misidentified as compatibility search")
		}
	}
}

func TestKiloToolSearchAttemptsHaveIndependentState(t *testing.T) {
	body := kiloToolSearchTestJSON(t, map[string]any{"tools": []any{kiloToolSearchTestDefinition()}})
	firstBody, first, err := PrepareKiloToolSearch(body)
	if err != nil {
		t.Fatal(err)
	}
	added := []byte(`{"type":"response.output_item.added","item":{"id":"fc_first","type":"function_call","name":"__kilo2api_tool_search","arguments":"{"}}`)
	if _, err := first.TransformSSE(added); err != nil {
		t.Fatal(err)
	}
	secondBody, second, err := PrepareKiloToolSearch(body)
	if err != nil || !bytes.Equal(firstBody, secondBody) {
		t.Fatal("fresh attempt did not reconstruct the original search definition")
	}
	fragment := []byte(`{"type":"response.function_call_arguments.delta","item_id":"fc_first","delta":"not second-attempt search"}`)
	out, err := second.TransformSSE(fragment)
	if err != nil || !bytes.Equal(out, fragment) {
		t.Fatal("search item tracking leaked across attempts")
	}
}

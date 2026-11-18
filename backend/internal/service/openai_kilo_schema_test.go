package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const kiloSchemaTestRejection = `{"error":{"type":"invalid_request_error","code":"invalid_json_schema","param":"text.format.schema","message":"PRIVATE"}}`
const kiloSchemaTestBody = `{"model":"openai/gpt-6.1-sol","instructions":"Keep original instruction.","input":[{"role":"user","content":"synthetic"}],"previous_response_id":"resp_previous","tools":[{"type":"function","name":"f","parameters":{}}],"stream":true,"text":{"verbosity":"low","format":{"type":"json_schema","name":"sample","description":"synthetic format","schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}}}}`

func kiloSchemaTestEncode(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func kiloSchemaTestConstraint(t *testing.T) *kiloSchemaConstraint {
	t.Helper()
	_, constraint, err := prepareKiloSchemaRetry([]byte(kiloSchemaTestBody), http.StatusBadRequest, []byte(kiloSchemaTestRejection))
	if err != nil || constraint == nil {
		t.Fatalf("prepare compatibility constraint: %v", err)
	}
	return constraint
}

func kiloSchemaTestFinal(text string) map[string]any {
	return map[string]any{
		"id": "resp_test", "status": "completed", "model": "openai/gpt-6.1-sol",
		"usage":  map[string]any{"output_tokens": 7},
		"output": []any{map[string]any{"id": "msg_test", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}},
	}
}

func kiloSchemaTestEvent(t *testing.T, value map[string]any) []byte {
	t.Helper()
	kind, _ := value["type"].(string)
	return []byte("event: " + kind + "\ndata: " + string(kiloSchemaTestEncode(t, value)) + "\n\n")
}

func kiloSchemaTestError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var failure *kiloSchemaError
	if !errors.As(err, &failure) || failure.Status != status || failure.Code != code || failure.Param != kiloSchemaParam {
		t.Fatalf("error = %#v, want %d %s", err, status, code)
	}
	if strings.Contains(failure.Message, "PRIVATE") || strings.Contains(failure.Message, "UNVALIDATED") {
		t.Fatal("private upstream content appeared in the public diagnostic")
	}
}

func TestKiloSchemaCompatExactRejectionAndSingleRetry(t *testing.T) {
	body := []byte(kiloSchemaTestBody)
	original := bytes.Clone(body)
	for _, tc := range []struct {
		name   string
		status int
		error  string
	}{
		{"success", 200, kiloSchemaTestRejection},
		{"server_error", 500, kiloSchemaTestRejection},
		{"different_param", 400, `{"error":{"code":"invalid_json_schema","param":"tools"}}`},
		{"different_code", 400, `{"error":{"code":"other","param":"text.format.schema"}}`},
		{"malformed_error", 400, `{"error":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patched, constraint, err := prepareKiloSchemaRetry(body, tc.status, []byte(tc.error))
			if err != nil || constraint != nil || !bytes.Equal(patched, body) {
				t.Fatalf("unexpected retry: %v", err)
			}
		})
	}
	for _, rejection := range [][]byte{
		[]byte(kiloSchemaTestRejection),
		[]byte("event: error\r\ndata: " + kiloSchemaTestRejection + "\r\n\r\n"),
	} {
		patched, constraint, err := prepareKiloSchemaRetry(body, 400, rejection)
		if err != nil || constraint == nil || !bytes.Equal(body, original) {
			t.Fatalf("exact rejection failed or original changed: %v", err)
		}
		sent, _ := kiloSchemaDecodeObject(patched)
		before, _ := kiloSchemaDecodeObject(body)
		for _, key := range []string{"model", "input", "previous_response_id", "tools", "stream"} {
			if !reflect.DeepEqual(sent[key], before[key]) {
				t.Fatalf("retry modified %s", key)
			}
		}
		text := sent["text"].(map[string]any)
		if text["verbosity"] != "low" || !reflect.DeepEqual(text["format"], map[string]any{"type": "text"}) {
			t.Fatal("retry changed sibling text fields")
		}
		instructions := sent["instructions"].(string)
		if !strings.HasPrefix(instructions, "Keep original instruction.") || !strings.Contains(instructions, "exactly one JSON value") || !strings.Contains(instructions, "Output format description: synthetic format") {
			t.Fatal("retry did not preserve and append the schema instructions")
		}
		again, second, err := prepareKiloSchemaRetry(patched, 400, rejection)
		if err != nil || second != nil || !bytes.Equal(again, patched) {
			t.Fatalf("compatibility body produced a second retry: %v", err)
		}
	}
}

func TestKiloSchemaCompatInvalidSchemaNeverProducesRetry(t *testing.T) {
	for _, schema := range []string{
		`{"type":"not_a_type"}`, `true`, `null`, `{"$schema":42}`, `{"$schema":"not a URI"}`,
		`{"$ref":"https://example.invalid/schema"}`,
		`{"$defs":{"unused":{"$ref":"file:///private/schema"}},"type":"object"}`,
		`{"type":"number","minimum":1e1000000000}`,
	} {
		t.Run(schema, func(t *testing.T) {
			request, _ := kiloSchemaDecodeObject([]byte(kiloSchemaTestBody))
			value, err := kiloSchemaDecode([]byte(schema))
			if err != nil {
				t.Fatal(err)
			}
			request["text"].(map[string]any)["format"].(map[string]any)["schema"] = value
			patched, constraint, err := prepareKiloSchemaRetry(kiloSchemaTestEncode(t, request), 400, []byte(kiloSchemaTestRejection))
			if patched != nil || constraint != nil {
				t.Fatal("invalid schema could cause a generation retry")
			}
			kiloSchemaTestError(t, err, 400, "invalid_json_schema")
		})
	}
	request, _ := kiloSchemaDecodeObject([]byte(kiloSchemaTestBody))
	request["instructions"] = []any{"not a string"}
	_, constraint, err := prepareKiloSchemaRetry(kiloSchemaTestEncode(t, request), 400, []byte(kiloSchemaTestRejection))
	var failure *kiloSchemaError
	if constraint != nil || !errors.As(err, &failure) || failure.Status != 400 || failure.Param != "instructions" {
		t.Fatalf("invalid instructions were retried: %v", err)
	}
}

func TestKiloSchemaCompatDraft202012AndExactNumbers(t *testing.T) {
	for _, tc := range []struct {
		name, schema, valid, invalid string
	}{
		{"local_ref_and_unevaluated", `{"$defs":{"obj":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}},"$ref":"#/$defs/obj","unevaluatedProperties":false}`, `{"value":"ok"}`, `{"value":"ok","extra":true}`},
		{"tuple_and_contains", `{"type":"array","prefixItems":[{"const":"tag"}],"items":{"type":"integer"},"contains":{"minimum":3,"type":"integer"},"minContains":1,"maxContains":1}`, `["tag",1,3]`, `["tag",3,4]`},
		{"dependent_schema", `{"type":"object","dependentSchemas":{"a":{"required":["b"]}}}`, `{"a":1,"b":2}`, `{"a":1}`},
		{"regex_lookaround", `{"type":"string","pattern":"^(?=a)a+$"}`, `"aaa"`, `"baa"`},
		{"exact_integer", `{"type":"integer","const":9007199254740993}`, `9007199254740993`, `9007199254740992`},
		{"format_annotation", `{"type":"string","format":"email","minLength":1}`, `"not an email"`, `""`},
		{"declared_older_draft_still_202012", `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","unevaluatedProperties":false}`, `{}`, `{"extra":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := kiloSchemaDecodeObject([]byte(tc.schema))
			if err != nil {
				t.Fatal(err)
			}
			constraint, err := newKiloSchemaConstraint(map[string]any{"type": "json_schema", "schema": schema})
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range []struct {
				text string
				pass bool
			}{{tc.valid, true}, {tc.invalid, false}} {
				_, err := constraint.validateJSON(kiloSchemaTestEncode(t, kiloSchemaTestFinal(input.text)))
				if (err == nil) != input.pass {
					t.Fatalf("schema validation(%s), expected pass=%v, error=%v", input.text, input.pass, err)
				}
			}
		})
	}
}

func TestKiloSchemaCompatInvalidOutputNeverDelivered(t *testing.T) {
	constraint := kiloSchemaTestConstraint(t)
	for _, text := range []string{
		`{"value":3}`, "```json\n{\"value\":\"ok\"}\n```", `{"value":"a","value":"b"}`,
		`{"value":{"nested":1,"nested":2}}`, `NaN`, `Infinity`, `-Infinity`,
		`{"value":"ok"} {}`, ``, `{"value":"PRIVATE"`,
	} {
		t.Run(text, func(t *testing.T) {
			result, err := constraint.validateJSON(kiloSchemaTestEncode(t, kiloSchemaTestFinal(text)))
			if result != nil {
				t.Fatal("unchecked result was delivered")
			}
			kiloSchemaTestError(t, err, 502, "structured_output_validation_failed")
		})
	}
	for _, status := range []string{"incomplete", "failed", "in_progress", ""} {
		response := kiloSchemaTestFinal(`{"value":"ok"}`)
		response["status"] = status
		_, err := constraint.validateJSON(kiloSchemaTestEncode(t, response))
		kiloSchemaTestError(t, err, 502, "structured_output_validation_failed")
	}
	response := kiloSchemaTestFinal(`{"value":"ok"}`)
	response["error"] = map[string]any{"message": "PRIVATE"}
	_, err := constraint.validateJSON(kiloSchemaTestEncode(t, response))
	kiloSchemaTestError(t, err, 502, "structured_output_validation_failed")
}

func TestKiloSchemaCompatToolsRefusalsAndRestoredFormat(t *testing.T) {
	constraint := kiloSchemaTestConstraint(t)
	for _, item := range []map[string]any{
		{"type": "function_call", "call_id": "call_test", "name": "f", "arguments": "{}"},
		{"type": "custom_tool_call", "call_id": "call_test", "name": "f", "input": "{}"},
		{"type": "message", "content": []any{map[string]any{"type": "refusal", "refusal": "Cannot comply"}}},
	} {
		response := kiloSchemaTestFinal(`{"value":"ok"}`)
		response["output"] = []any{item}
		raw, err := constraint.validateJSON(kiloSchemaTestEncode(t, response))
		if err != nil {
			t.Fatal(err)
		}
		delivered, _ := kiloSchemaDecodeObject(raw)
		if !reflect.DeepEqual(delivered["text"].(map[string]any)["format"], constraint.format) || !bytes.Equal(kiloSchemaTestEncode(t, delivered["output"]), kiloSchemaTestEncode(t, response["output"])) {
			t.Fatal("tool/refusal or original format changed")
		}
		// A tool call may not exempt any accompanying assistant text.
		response["output"] = append(response["output"].([]any), kiloSchemaTestFinal("UNVALIDATED")["output"].([]any)...)
		_, err = constraint.validateJSON(kiloSchemaTestEncode(t, response))
		kiloSchemaTestError(t, err, 502, "structured_output_validation_failed")
	}
	response := kiloSchemaTestFinal(`{"value":"ok"}`)
	response["text"] = map[string]any{"verbosity": "low", "format": map[string]any{"type": "text"}}
	raw, err := constraint.validateJSON(kiloSchemaTestEncode(t, response))
	if err != nil {
		t.Fatal(err)
	}
	delivered, _ := kiloSchemaDecodeObject(raw)
	if delivered["text"].(map[string]any)["verbosity"] != "low" || !bytes.Equal(kiloSchemaTestEncode(t, delivered["usage"]), kiloSchemaTestEncode(t, response["usage"])) {
		t.Fatal("validation changed usage or sibling text metadata")
	}
}

func TestKiloSchemaCompatSSEValidatesEveryTextCarrier(t *testing.T) {
	constraint := kiloSchemaTestConstraint(t)
	terminal := kiloSchemaTestEvent(t, map[string]any{"type": "response.completed", "response": kiloSchemaTestFinal(`{"value":"ok"}`)})
	for _, event := range []map[string]any{
		{"type": "response.output_text.delta", "delta": "UNVALIDATED"},
		{"type": "response.output_text.done", "text": "UNVALIDATED"},
		{"type": "response.content_part.added", "part": map[string]any{"type": "output_text", "text": "UNVALIDATED"}},
		{"type": "response.content_part.done", "part": map[string]any{"type": "output_text", "text": "UNVALIDATED"}},
		{"type": "response.output_item.added", "item": map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "UNVALIDATED"}}}},
		{"type": "response.output_item.done", "item": map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "UNVALIDATED"}}}},
		{"type": "response.output_text.delta", "delta": 3},
		{"type": "response.content_part.done", "part": map[string]any{"type": "output_text", "text": nil}},
	} {
		event["output_index"], event["content_index"] = 0, 0
		raw := append(kiloSchemaTestEvent(t, event), terminal...)
		delivered, err := constraint.validateSSE(bytes.NewReader(raw))
		if delivered != nil {
			t.Fatalf("%s exposed unchecked text", event["type"])
		}
		kiloSchemaTestError(t, err, 502, "structured_output_validation_failed")
	}
	for _, prefix := range [][]byte{
		nil,
		kiloSchemaTestEvent(t, map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": `{"value":"ok"}`}),
	} {
		raw := append(bytes.Clone(prefix), terminal...)
		for _, delimiter := range []string{"\n", "\r", "\r\n"} {
			framed := bytes.ReplaceAll(raw, []byte("\n"), []byte(delimiter))
			delivered, err := constraint.validateSSE(bytes.NewReader(framed))
			if err != nil || !bytes.Contains(delivered, []byte(`"type":"json_schema"`)) || !bytes.Contains(delivered, []byte(`"output_tokens":7`)) {
				t.Fatalf("valid stream with %q framing failed: %v", delimiter, err)
			}
		}
	}
}

type kiloSchemaTrackedBody struct {
	io.Reader
	closed bool
}

func (b *kiloSchemaTrackedBody) Close() error { b.closed = true; return nil }

func TestKiloSchemaCompatBufferHoldsOutputAndClosesUpstream(t *testing.T) {
	constraint := kiloSchemaTestConstraint(t)
	terminal := kiloSchemaTestEvent(t, map[string]any{"type": "response.completed", "response": kiloSchemaTestFinal(`{"value":"ok"}`)})
	for _, tc := range []struct {
		name string
		raw  []byte
		sse  bool
		pass bool
	}{
		{"json", kiloSchemaTestEncode(t, kiloSchemaTestFinal(`{"value":"ok"}`)), false, true},
		{"sse", terminal, true, true},
		{"missing_terminal", []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"), true, false},
		{"incomplete", kiloSchemaTestEvent(t, map[string]any{"type": "response.incomplete", "response": kiloSchemaTestFinal(`{"value":"ok"}`)}), true, false},
		{"error", []byte("event: error\ndata: {\"message\":\"PRIVATE\"}\n\n"), true, false},
		{"json_limit", bytes.Repeat([]byte(" "), kiloSchemaMaxBufferBytes+1), false, false},
		{"sse_limit", append(bytes.Repeat(append([]byte(":"), append(bytes.Repeat([]byte("x"), kiloSchemaMaxBufferBytes/2), '\n', '\n')...), 2), terminal...), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &kiloSchemaTrackedBody{Reader: bytes.NewReader(tc.raw)}
			replay, err := constraint.bufferResponse(upstream, tc.sse)
			if !upstream.closed || (err == nil) != tc.pass {
				t.Fatalf("closed=%v, expected pass=%v, error=%v", upstream.closed, tc.pass, err)
			}
			if !tc.pass {
				if replay != nil {
					t.Fatal("a failed result exposed a readable body")
				}
				kiloSchemaTestError(t, err, 502, "structured_output_validation_failed")
				return
			}
			defer replay.Close()
			raw, err := io.ReadAll(replay)
			if err != nil || !bytes.Contains(raw, []byte(`"type":"json_schema"`)) {
				t.Fatalf("validated replay lost the original schema format: %v", err)
			}
		})
	}
}

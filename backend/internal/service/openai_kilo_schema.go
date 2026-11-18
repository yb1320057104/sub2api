package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dlclark/regexp2/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const kiloSchemaMaxBufferBytes = 16 * 1024 * 1024
const kiloSchemaResourceURL = "https://kilo-schema.invalid/response.json"
const kiloSchemaParam = "text.format.schema"

// kiloSchemaError contains only public, bounded diagnostics. Neither schema
// values, generated output nor the upstream error message may escape here.
type kiloSchemaError struct {
	Status  int
	Code    string
	Message string
	Param   string
}

func (e *kiloSchemaError) Error() string { return e.Message }

func kiloInvalidSchema() error {
	return &kiloSchemaError{http.StatusBadRequest, "invalid_json_schema", "输出 JSON Schema 无效。", kiloSchemaParam}
}

func kiloInvalidStructuredOutput(message string) error {
	return &kiloSchemaError{http.StatusBadGateway, "structured_output_validation_failed", message, kiloSchemaParam}
}

// prepareKiloSchemaRetry is opt-in by an exact upstream rejection. A nil
// constraint means no retry. The caller owns the one-attempt guard, must close
// the rejected response before sending patched, and must reuse the same account.
// Transport errors, administrative probes and account failover are not handled
// here. Passing patched again cannot produce a second schema retry.
func prepareKiloSchemaRetry(body []byte, status int, errorBody []byte) ([]byte, *kiloSchemaConstraint, error) {
	if status != http.StatusBadRequest || !kiloSchemaRejected(errorBody) {
		return body, nil, nil
	}
	request, err := kiloSchemaDecodeObject(body)
	if err != nil {
		return nil, nil, kiloInvalidSchema()
	}
	text, _ := request["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" {
		return body, nil, nil
	}
	constraint, err := newKiloSchemaConstraint(format)
	if err != nil {
		return nil, nil, err
	}
	instructions := ""
	if value := request["instructions"]; kiloSchemaTruthy(value) {
		var ok bool
		instructions, ok = value.(string)
		if !ok {
			return nil, nil, &kiloSchemaError{http.StatusBadRequest, "invalid_json_schema", "结构化输出兼容要求 instructions 为字符串。", "instructions"}
		}
	}
	encoded, err := json.Marshal(format["schema"])
	if err != nil {
		return nil, nil, kiloInvalidSchema()
	}
	request["instructions"] = instructions + "\n\nFor this response, when returning assistant text, emit exactly one JSON value " +
		"matching the following JSON Schema, without Markdown fences or other text. " +
		"If tools are needed, issue tool calls without accompanying assistant text first. " +
		"This output requirement applies only to this response.\n" + string(encoded)
	if description, ok := format["description"].(string); ok {
		request["instructions"] = request["instructions"].(string) + "\nOutput format description: " + description
	}
	text["format"] = map[string]any{"type": "text"}
	patched, err := json.Marshal(request)
	if err != nil {
		return nil, nil, kiloInvalidSchema()
	}
	return patched, constraint, nil
}

func kiloSchemaRejected(raw []byte) bool {
	matches := func(value map[string]any) bool {
		if response, ok := value["response"].(map[string]any); ok {
			value = response
		}
		if upstreamError, ok := value["error"].(map[string]any); ok {
			value = upstreamError
		}
		return value["code"] == "invalid_json_schema" && value["param"] == kiloSchemaParam
	}
	if value, err := kiloSchemaDecodeObject(raw); err == nil {
		return matches(value)
	}
	if len(raw) > kiloSchemaMaxBufferBytes {
		return false
	}
	scanner := kiloSchemaSSEScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		value, err := kiloSchemaParseEvent(scanner.Bytes())
		if err != nil {
			return false
		}
		if value != nil && matches(value) {
			return true
		}
	}
	return false
}

type kiloSchemaConstraint struct {
	format map[string]any
	schema *jsonschema.Schema
}

type kiloSchemaLocalLoader struct{}

func (kiloSchemaLocalLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("external Kilo schema resources are not supported")
}

// Use the existing ECMAScript-capable engine for lookarounds/backreferences;
// Go's RE2 syntax alone does not cover JSON Schema regular expressions.
type kiloSchemaRegexp struct{ re *regexp2.Regexp }
type kiloSchemaRegexpFailure struct{}

func (r kiloSchemaRegexp) String() string { return r.re.String() }
func (r kiloSchemaRegexp) MatchString(value string) bool {
	matched, err := r.re.MatchString(value)
	if err != nil {
		// A timeout must fail the entire validation, including under not/oneOf.
		panic(kiloSchemaRegexpFailure{})
	}
	return matched
}

func kiloSchemaCompileRegexp(pattern string) (jsonschema.Regexp, error) {
	re, err := regexp2.Compile(pattern, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	re.MatchTimeout = time.Second
	return kiloSchemaRegexp{re}, nil
}

func newKiloSchemaConstraint(format map[string]any) (constraint *kiloSchemaConstraint, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			if _, ok := failure.(kiloSchemaRegexpFailure); !ok {
				panic(failure)
			}
			constraint, err = nil, kiloInvalidSchema()
		}
	}()
	// Decode a private copy with exact numbers. The request and its original
	// format remain immutable even if the caller keeps or mutates its input.
	encoded, marshalErr := json.Marshal(format)
	if marshalErr != nil {
		return nil, kiloInvalidSchema()
	}
	privateFormat, decodeErr := kiloSchemaDecodeObject(encoded)
	if decodeErr != nil {
		return nil, kiloInvalidSchema()
	}
	schema, ok := privateFormat["schema"].(map[string]any)
	if !ok || schema == nil || kiloSchemaNumberBudget(schema) != nil || kiloSchemaCheckReferences(schema) != nil {
		return nil, kiloInvalidSchema()
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(kiloSchemaLocalLoader{})
	compiler.UseRegexpEngine(kiloSchemaCompileRegexp)
	// Draft 2020-12 format/content annotations are not assertions, matching
	// Draft202012Validator without a FormatChecker in the frozen upstream.
	// Check the original document against the complete 2020-12 metaschema
	// before fixing its dialect. Overwriting $schema first would hide an
	// invalid declaration and could trigger a retry for an invalid schema.
	metaCompiler := jsonschema.NewCompiler()
	metaCompiler.DefaultDraft(jsonschema.Draft2020)
	metaCompiler.UseLoader(kiloSchemaLocalLoader{})
	metaCompiler.UseRegexpEngine(kiloSchemaCompileRegexp)
	metaCompiler.AssertFormat()
	meta, metaErr := metaCompiler.Compile("https://json-schema.org/draft/2020-12/schema")
	if metaErr != nil || meta.Validate(schema) != nil {
		return nil, kiloInvalidSchema()
	}
	// The upstream explicitly chooses Draft202012Validator even when the
	// document names an older dialect. Preserve the public format, but compile
	// the root under that same fixed draft rather than DefaultDraft alone.
	compilationSchema := make(map[string]any, len(schema)+1)
	for key, value := range schema {
		compilationSchema[key] = value
	}
	compilationSchema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	if compiler.AddResource(kiloSchemaResourceURL, compilationSchema) != nil {
		return nil, kiloInvalidSchema()
	}
	compiled, compileErr := compiler.Compile(kiloSchemaResourceURL)
	if compileErr != nil {
		return nil, kiloInvalidSchema()
	}
	return &kiloSchemaConstraint{format: privateFormat, schema: compiled}, nil
}

// Reject external references even in an unused $defs entry, before any retry.
// Embedded resources with their own $id are still local. Traverse schema
// positions only: a literal $ref inside const/examples is ordinary user data.
func kiloSchemaCheckReferences(schema map[string]any) error {
	type reference struct{ base, target string }
	resources := map[string]bool{kiloSchemaResourceURL: true}
	var references []reference
	var visit func(map[string]any, string) error
	visit = func(node map[string]any, base string) error {
		resolve := func(value string) (string, error) {
			baseURL, err := url.Parse(base)
			if err != nil {
				return "", err
			}
			relative, err := url.Parse(value)
			if err != nil {
				return "", err
			}
			resolved := baseURL.ResolveReference(relative)
			resolved.Fragment, resolved.RawFragment = "", ""
			return resolved.String(), nil
		}
		if id, ok := node["$id"].(string); ok {
			resolved, err := resolve(id)
			if err != nil {
				return err
			}
			base = resolved
			resources[base] = true
		}
		for _, keyword := range []string{"$ref", "$dynamicRef", "$recursiveRef"} {
			if target, ok := node[keyword].(string); ok {
				references = append(references, reference{base, target})
			}
		}
		for _, keyword := range []string{"additionalProperties", "unevaluatedProperties", "propertyNames", "items", "contains", "unevaluatedItems", "not", "if", "then", "else", "contentSchema", "additionalItems"} {
			if child, ok := node[keyword].(map[string]any); ok {
				if err := visit(child, base); err != nil {
					return err
				}
			}
		}
		for _, keyword := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas", "dependencies"} {
			if children, ok := node[keyword].(map[string]any); ok {
				for _, raw := range children {
					if child, ok := raw.(map[string]any); ok {
						if err := visit(child, base); err != nil {
							return err
						}
					}
				}
			}
		}
		for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems", "items"} {
			if children, ok := node[keyword].([]any); ok {
				for _, raw := range children {
					if child, ok := raw.(map[string]any); ok {
						if err := visit(child, base); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	}
	if err := visit(schema, kiloSchemaResourceURL); err != nil {
		return err
	}
	for _, ref := range references {
		base, err := url.Parse(ref.base)
		if err != nil {
			return err
		}
		target, err := url.Parse(ref.target)
		if err != nil {
			return err
		}
		resolved := base.ResolveReference(target)
		resolved.Fragment, resolved.RawFragment = "", ""
		if !resources[resolved.String()] {
			return fmt.Errorf("external Kilo schema reference")
		}
	}
	return nil
}

// bufferResponse consumes and closes the successful compatibility response.
// No output becomes readable until the completed snapshot and every buffered
// text event pass validation. The replacement preserves the upstream body type,
// so the normal JSON/SSE handler can retain usage, tools and metadata handling.
func (c *kiloSchemaConstraint) bufferResponse(body io.ReadCloser, upstreamSSE bool) (io.ReadCloser, error) {
	defer body.Close()
	var validated []byte
	var err error
	if upstreamSSE {
		validated, err = c.validateSSE(body)
	} else {
		var raw []byte
		raw, err = io.ReadAll(io.LimitReader(body, kiloSchemaMaxBufferBytes+1))
		if err == nil {
			validated, err = c.validateJSON(raw)
		}
	}
	if err != nil {
		if _, ok := err.(*kiloSchemaError); ok {
			return nil, err
		}
		return nil, kiloInvalidStructuredOutput("结构化响应不完整或超过缓冲上限。")
	}
	return io.NopCloser(bytes.NewReader(validated)), nil
}

func (c *kiloSchemaConstraint) validateJSON(raw []byte) ([]byte, error) {
	if len(raw) > kiloSchemaMaxBufferBytes {
		return nil, kiloInvalidStructuredOutput("结构化响应不完整或超过缓冲上限。")
	}
	response, err := kiloSchemaDecodeObject(raw)
	if err != nil || c.validate(response) != nil {
		return nil, kiloInvalidStructuredOutput("模型结果未通过输出格式校验，请调整请求后重试。")
	}
	c.restoreFormat(response)
	return json.Marshal(response)
}

func (c *kiloSchemaConstraint) validate(response map[string]any) (err error) {
	invalid := func() error {
		return kiloInvalidStructuredOutput("模型结果未通过输出格式校验，请调整请求后重试。")
	}
	defer func() {
		if failure := recover(); failure != nil {
			if _, ok := failure.(kiloSchemaRegexpFailure); !ok {
				panic(failure)
			}
			err = invalid()
		}
	}()
	if response["status"] != "completed" || kiloSchemaTruthy(response["error"]) {
		return invalid()
	}
	output, ok := response["output"].([]any)
	if !ok {
		return invalid()
	}
	var text strings.Builder
	hasCall, hasRefusal := false, false
	for _, raw := range output {
		item, ok := raw.(map[string]any)
		if !ok {
			return invalid()
		}
		hasCall = hasCall || item["type"] == "function_call" || item["type"] == "custom_tool_call"
		if item["type"] != "message" {
			continue
		}
		parts, ok := item["content"].([]any)
		if !ok {
			return invalid()
		}
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok {
				return invalid()
			}
			switch part["type"] {
			case "output_text":
				value, ok := part["text"].(string)
				if !ok || len(value) > kiloSchemaMaxBufferBytes-text.Len() {
					return invalid()
				}
				text.WriteString(value)
			case "refusal":
				hasRefusal = true
			}
		}
	}
	if strings.TrimSpace(text.String()) == "" && (hasCall || hasRefusal) {
		return nil
	}
	value, decodeErr := kiloSchemaDecode([]byte(text.String()))
	if decodeErr != nil || kiloSchemaNumberBudget(value) != nil || c.schema.Validate(value) != nil {
		return invalid()
	}
	return nil
}

func (c *kiloSchemaConstraint) restoreFormat(response map[string]any) {
	text, _ := response["text"].(map[string]any)
	if text == nil {
		text = make(map[string]any)
	}
	text["format"] = c.format
	response["text"] = text
}

type kiloSchemaBufferedEvent struct {
	raw   []byte
	value map[string]any
}

func (c *kiloSchemaConstraint) validateSSE(reader io.Reader) ([]byte, error) {
	invalid := func() error {
		return kiloInvalidStructuredOutput("结构化响应不完整或超过缓冲上限。")
	}
	scanner := kiloSchemaSSEScanner(reader)
	var events []kiloSchemaBufferedEvent
	var response map[string]any
	received := 0
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) > kiloSchemaMaxBufferBytes-received {
			return nil, invalid()
		}
		received += len(raw)
		value, err := kiloSchemaParseEvent(raw)
		if err != nil {
			return nil, invalid()
		}
		if value != nil && (value["type"] == "error" || kiloSchemaTruthy(value["error"]) || (value["code"] != nil && value["message"] != nil)) {
			return nil, invalid()
		}
		events = append(events, kiloSchemaBufferedEvent{bytes.Clone(raw), value})
		if value != nil {
			switch value["type"] {
			case "response.completed", "response.failed", "response.incomplete":
				response, _ = value["response"].(map[string]any)
				if value["type"] != "response.completed" || response == nil {
					return nil, invalid()
				}
			}
		}
		if response != nil {
			// A completed frame, rather than EOF, ends the upstream turn.
			break
		}
	}
	if scanner.Err() != nil || response == nil {
		return nil, invalid()
	}
	if err := c.validate(response); err != nil {
		return nil, err
	}
	if err := kiloSchemaValidateStreamText(events, response); err != nil {
		return nil, err
	}
	var replay bytes.Buffer
	for _, event := range events {
		raw := event.raw
		if nested, ok := event.value["response"].(map[string]any); ok {
			c.restoreFormat(nested)
			encoded, err := json.Marshal(event.value)
			if err != nil {
				return nil, invalid()
			}
			name, _ := event.value["type"].(string)
			raw = []byte("event: " + name + "\ndata: " + string(encoded) + "\n\n")
		}
		replay.Write(raw)
	}
	return replay.Bytes(), nil
}

func kiloSchemaValidateStreamText(events []kiloSchemaBufferedEvent, response map[string]any) error {
	invalid := func() error {
		return kiloInvalidStructuredOutput("结构化流与最终结果不一致。")
	}
	type position struct{ output, content int }
	expected, deltas := map[position]string{}, map[position]string{}
	output, _ := response["output"].([]any)
	for i, raw := range output {
		item, _ := raw.(map[string]any)
		if item["type"] != "message" {
			continue
		}
		content, _ := item["content"].([]any)
		for j, rawPart := range content {
			part, _ := rawPart.(map[string]any)
			if part["type"] == "output_text" {
				expected[position{i, j}], _ = part["text"].(string)
			}
		}
	}
	index := func(raw any) (int, bool) {
		number, ok := raw.(json.Number)
		if !ok {
			return 0, false
		}
		value, err := strconv.Atoi(number.String())
		return value, err == nil && value >= 0
	}
	for _, event := range events {
		value := event.value
		kind, _ := value["type"].(string)
		if kind != "response.output_text.delta" && kind != "response.output_text.done" && kind != "response.content_part.added" && kind != "response.content_part.done" && kind != "response.output_item.added" && kind != "response.output_item.done" {
			continue
		}
		oi, outputOK := index(value["output_index"])
		ci, contentOK := index(value["content_index"])
		key := position{oi, ci}
		switch kind {
		case "response.output_text.delta", "response.output_text.done":
			want, exists := expected[key]
			if !outputOK || !contentOK || !exists {
				return invalid()
			}
			if kind == "response.output_text.delta" {
				delta, ok := value["delta"].(string)
				if !ok || len(delta) > len(want)-len(deltas[key]) {
					return invalid()
				}
				deltas[key] += delta
			} else if text, ok := value["text"].(string); !ok || text != want {
				return invalid()
			}
		case "response.content_part.added", "response.content_part.done":
			part, _ := value["part"].(map[string]any)
			if part["type"] == "output_text" {
				want, exists := expected[key]
				text, ok := part["text"].(string)
				if _, exists := part["text"]; !exists {
					text, ok = "", true
				}
				if !outputOK || !contentOK || !exists || !ok || !strings.HasPrefix(want, text) {
					return invalid()
				}
			}
		case "response.output_item.added", "response.output_item.done":
			item, _ := value["item"].(map[string]any)
			parts, _ := item["content"].([]any)
			for i, rawPart := range parts {
				part, _ := rawPart.(map[string]any)
				if part["type"] == "output_text" {
					want, exists := expected[position{oi, i}]
					text, ok := part["text"].(string)
					if _, exists := part["text"]; !exists {
						text, ok = "", true
					}
					if !outputOK || !exists || !ok || !strings.HasPrefix(want, text) {
						return invalid()
					}
				}
			}
		}
	}
	for key, text := range deltas {
		if text != expected[key] {
			return invalid()
		}
	}
	return nil
}

func kiloSchemaSSEScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), kiloSchemaMaxBufferBytes+1)
	scanner.Split(kiloSchemaSSESplit)
	return scanner
}

// Keep complete frames and their original bytes, including CR-only and split
// CRLF framing. Replay is buffered; no downstream write happens during parsing.
func kiloSchemaSSESplit(data []byte, atEOF bool) (int, []byte, error) {
	lineStart := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\r' && data[i] != '\n' {
			continue
		}
		if data[i] == '\r' && i+1 == len(data) && !atEOF {
			return 0, nil, nil
		}
		end := i + 1
		if data[i] == '\r' && end < len(data) && data[end] == '\n' {
			end++
		}
		if i == lineStart {
			return end, data[:end], nil
		}
		lineStart, i = end, end-1
	}
	if atEOF && len(data) != 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func kiloSchemaParseEvent(raw []byte) (map[string]any, error) {
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	raw = bytes.ReplaceAll(raw, []byte("\r"), []byte("\n"))
	var data [][]byte
	name := ""
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("data:")) {
			data = append(data, bytes.TrimLeft(line[5:], " "))
		} else if bytes.HasPrefix(line, []byte("event:")) {
			name = strings.TrimPrefix(string(line[6:]), " ")
		}
	}
	payload := bytes.Join(data, []byte("\n"))
	if len(payload) == 0 || bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
		return nil, nil
	}
	value, err := kiloSchemaDecodeObject(payload)
	if err != nil {
		return nil, err
	}
	if name == "error" || (name != "" && !kiloSchemaTruthy(value["type"])) {
		value["type"] = name
	}
	return value, nil
}

func kiloSchemaDecodeObject(raw []byte) (map[string]any, error) {
	value, err := kiloSchemaDecode(raw)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	return object, nil
}

// encoding/json normally accepts duplicate keys and replaces invalid UTF-8.
// Walk tokens instead, retaining exact numbers and rejecting those ambiguities
// at every depth as well as trailing JSON, NaN and Infinity.
func kiloSchemaDecode(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		if depth > 10000 {
			return nil, fmt.Errorf("JSON nesting limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch token {
		case json.Delim('{'):
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("invalid JSON object key")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate JSON object key")
				}
				value, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				object[key] = value
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("invalid JSON object")
			}
			return object, nil
		case json.Delim('['):
			array := make([]any, 0)
			for decoder.More() {
				value, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("invalid JSON array")
			}
			return array, nil
		default:
			if _, delimiter := token.(json.Delim); delimiter {
				return nil, fmt.Errorf("unexpected JSON delimiter")
			}
			return token, nil
		}
	}
	value, err := read(0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one JSON value")
	}
	return value, nil
}

// The validator uses big.Rat; bound exponent expansion before invoking it so
// a tiny untrusted number cannot allocate unbounded integers. These are resource
// limits, not a replacement for any JSON Schema assertion.
func kiloSchemaNumberBudget(value any) error {
	budget := 65536
	var visit func(any) error
	visit = func(value any) error {
		switch v := value.(type) {
		case json.Number:
			raw, digits := v.String(), len(v.String())
			if digits > 4096 {
				return fmt.Errorf("JSON number precision limit")
			}
			if at := strings.IndexAny(raw, "eE"); at >= 0 {
				exponent, err := strconv.Atoi(raw[at+1:])
				if err != nil || exponent < -4096 || exponent > 4096 {
					return fmt.Errorf("JSON number exponent limit")
				}
				if exponent < 0 {
					exponent = -exponent
				}
				digits += exponent
			}
			budget -= digits
			if budget < 0 {
				return fmt.Errorf("JSON number validation budget")
			}
		case map[string]any:
			for _, child := range v {
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(value)
}

func kiloSchemaTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		parsed, err := v.Float64()
		return err != nil || parsed != 0
	case map[string]any:
		return len(v) != 0
	case []any:
		return len(v) != 0
	default:
		return true
	}
}

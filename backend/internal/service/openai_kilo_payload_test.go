package service

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"reflect"
	"strings"
	"testing"
)

func kiloPayloadTestEncode(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := encodeKiloPayload(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func kiloPayloadTestDecode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	doc, err := decodeKiloPayload(body)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func kiloPayloadTestError(t *testing.T, payload []byte, err error, status int, code string) {
	t.Helper()
	var payloadErr *kiloPayloadError
	if !errors.As(err, &payloadErr) || payloadErr.StatusCode != status || payloadErr.Code != code || payload != nil {
		t.Fatalf("payload/error = %d bytes, %v; want nil payload and %d/%s", len(payload), err, status, code)
	}
}

func TestKiloPayloadFinalWireBoundary(t *testing.T) {
	settings := defaultKiloImageSettings()
	settings.Enabled = false
	options := kiloPayloadOptions{ImageSettings: &settings}
	for _, compact := range []bool{false, true} {
		t.Run(map[bool]string{false: "responses", true: "compact"}[compact], func(t *testing.T) {
			options.Compact = compact
			doc := map[string]any{"model": "m", "input": "中文<&", "number": json.Number("9007199254740993"), "stream": false}
			wireDoc := map[string]any{"model": "m", "input": "", "number": json.Number("9007199254740993"), "stream": !compact}
			overhead := len(kiloPayloadTestEncode(t, wireDoc))
			doc["input"] = strings.Repeat("x", kiloPayloadMaxBytes-overhead-len("中文<&")) + "中文<&"
			body := kiloPayloadTestEncode(t, doc)
			before := bytes.Clone(body)
			payload, err := prepareKiloRequestPayloadWithOptions(body, options)
			if err != nil || len(payload) != kiloPayloadMaxBytes {
				t.Fatalf("exact final wire boundary: bytes=%d, err=%v", len(payload), err)
			}
			if !bytes.Equal(body, before) || !bytes.Contains(payload, []byte(`"number":9007199254740993`)) || !bytes.Contains(payload, []byte("中文<&")) {
				t.Fatal("input bytes, JSON number, or UTF-8 value changed")
			}
			got := kiloPayloadTestDecode(t, payload)
			if got["stream"] != !compact {
				t.Fatalf("wire stream = %v", got["stream"])
			}
			doc["input"] = doc["input"].(string) + "x"
			payload, err = prepareKiloRequestPayloadWithOptions(kiloPayloadTestEncode(t, doc), options)
			kiloPayloadTestError(t, payload, err, 413, "request_too_large")
			var detail *kiloPayloadError
			errors.As(err, &detail)
			if detail.PayloadBytes != kiloPayloadMaxBytes+1 {
				t.Fatalf("observed wire size=%d", detail.PayloadBytes)
			}
		})
	}
}

func TestKiloPayloadInvalidJSONAndIngress(t *testing.T) {
	for _, body := range [][]byte{nil, []byte(`null`), []byte(`[]`), []byte(`{"input":1}{}`), []byte(`{"input":`), {'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}} {
		payload, err := prepareKiloRequestPayload(body, false, "preserve")
		kiloPayloadTestError(t, payload, err, 400, "invalid_request")
	}
	payload, err := prepareKiloRequestPayload(bytes.Repeat([]byte{' '}, kiloPayloadIngressMax+1), false, "preserve")
	kiloPayloadTestError(t, payload, err, 413, "request_too_large")
}

func TestKiloPayloadImageSettingsValidation(t *testing.T) {
	for _, mutate := range []func(*kiloImageSettings){
		func(s *kiloImageSettings) { s.Quality = 0 },
		func(s *kiloImageSettings) { s.Quality = 101 },
		func(s *kiloImageSettings) { s.MaxEdge = 255 },
		func(s *kiloImageSettings) { s.MaxEdge = 8193 },
		func(s *kiloImageSettings) { s.MinEdge = 255 },
		func(s *kiloImageSettings) { s.MinEdge = 8193 },
		func(s *kiloImageSettings) { s.MinEdge = s.MaxEdge + 1 },
		func(s *kiloImageSettings) { s.TargetImageKiB = 63 },
		func(s *kiloImageSettings) { s.TargetImageKiB = 10241 },
	} {
		settings := defaultKiloImageSettings()
		mutate(&settings)
		payload, err := prepareKiloRequestPayloadWithOptions([]byte(`{"input":"hi"}`), kiloPayloadOptions{ImageSettings: &settings})
		kiloPayloadTestError(t, payload, err, 400, "invalid_image_settings")
	}
}

func kiloPayloadTestSample() map[string]any {
	return map[string]any{"instructions": "keep", "tools": []any{}, "input": []any{
		map[string]any{"role": "system", "content": "system first"},
		map[string]any{"role": "developer", "content": "developer first"},
		map[string]any{"role": "user", "content": "old context"},
		map[string]any{"type": "compaction", "encrypted_content": "old opaque"},
		map[string]any{"type": "additional_tools", "tools": []any{map[string]any{"type": "function", "name": "keep"}}},
		map[string]any{"type": "message", "role": "developer", "content": "developer later"},
		map[string]any{"type": "compaction", "encrypted_content": "latest opaque"},
		map[string]any{"role": "user", "content": "continue"},
		map[string]any{"type": "custom_tool_call", "call_id": "c", "name": "f", "input": "test"},
		map[string]any{"type": "custom_tool_call_output", "call_id": "c", "output": "result"},
	}}
}

func TestKiloPayloadCompactionOnlyOversizeExplicitLatest(t *testing.T) {
	doc := kiloPayloadTestSample()
	body := kiloPayloadTestEncode(t, doc)
	payload, err := prepareKiloRequestPayload(body, false, "latest")
	if err != nil || !reflect.DeepEqual(kiloPayloadTestDecode(t, payload)["input"], kiloPayloadTestDecode(t, body)["input"]) {
		t.Fatalf("under-limit history should be retained: %v", err)
	}
	items := doc["input"].([]any)
	items[2].(map[string]any)["content"] = strings.Repeat("x", kiloPayloadMaxBytes)
	body = kiloPayloadTestEncode(t, doc)
	for _, mode := range []string{"", "preserve", "typo", "LATEST", " latest "} {
		payload, err = prepareKiloRequestPayload(body, false, mode)
		kiloPayloadTestError(t, payload, err, 413, "request_too_large")
	}
	payload, err = prepareKiloRequestPayload(body, true, "latest")
	kiloPayloadTestError(t, payload, err, 413, "request_too_large")
	before := bytes.Clone(body)
	payload, err = prepareKiloRequestPayload(body, false, "latest")
	if err != nil || len(payload) > kiloPayloadMaxBytes || !bytes.Equal(body, before) {
		t.Fatalf("latest compaction failed or modified source: %v", err)
	}
	got := kiloPayloadTestDecode(t, payload)
	expected := append([]any{items[0], items[1], items[4], items[5]}, items[6:]...)
	if !reflect.DeepEqual(got["input"], expected) || got["instructions"] != "keep" {
		t.Fatal("instruction order, dynamic tools, ciphertext, or tail changed")
	}
	items[len(items)-1].(map[string]any)["output"] = strings.Repeat("x", kiloPayloadMaxBytes)
	payload, err = prepareKiloRequestPayload(kiloPayloadTestEncode(t, doc), false, "latest")
	kiloPayloadTestError(t, payload, err, 413, "request_too_large")
}

func TestKiloPayloadCompactionRejectsUnsafeDependencies(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"previous", func(d map[string]any) { d["previous_response_id"] = "r" }},
		{"single", func(d map[string]any) { d["input"] = d["input"].([]any)[4:] }},
		{"empty_ciphertext", func(d map[string]any) { d["input"].([]any)[6].(map[string]any)["encrypted_content"] = "" }},
		{"wrong_ciphertext", func(d map[string]any) { d["input"].([]any)[6].(map[string]any)["encrypted_content"] = 1 }},
		{"reference_tail", func(d map[string]any) {
			d["input"] = append(d["input"].([]any), map[string]any{"type": "item_reference", "id": "old"})
		}},
		{"reference_prefix", func(d map[string]any) { d["input"].([]any)[2] = map[string]any{"type": "item_reference", "id": "old"} }},
		{"unknown_prefix", func(d map[string]any) { d["input"].([]any)[2] = map[string]any{"type": "future_type"} }},
		{"unknown_tail", func(d map[string]any) {
			d["input"] = append(d["input"].([]any), map[string]any{"type": "computer_call_output"})
		}},
		{"primitive_prefix", func(d map[string]any) { d["input"].([]any)[2] = "unknown" }},
		{"primitive_tail", func(d map[string]any) { d["input"] = append(d["input"].([]any), nil) }},
		{"pending", func(d map[string]any) { d["input"] = d["input"].([]any)[:9] }},
		{"invalid_id", func(d map[string]any) { d["input"].([]any)[8].(map[string]any)["call_id"] = "" }},
		{"duplicate_output", func(d map[string]any) { d["input"] = append(d["input"].([]any), d["input"].([]any)[9]) }},
		{"duplicate_call", func(d map[string]any) { d["input"] = append(d["input"].([]any), d["input"].([]any)[8]) }},
		{"cross_boundary", func(d map[string]any) {
			items := d["input"].([]any)
			items[2] = items[8]
			d["input"] = append(items[:8], items[9])
		}},
		{"pending_prefix", func(d map[string]any) {
			d["input"].([]any)[2] = map[string]any{"type": "function_call", "call_id": "lost"}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			doc := kiloPayloadTestSample()
			test.mutate(doc)
			before := kiloPayloadTestEncode(t, doc)
			if pruneKiloCompaction(doc) || !bytes.Equal(before, kiloPayloadTestEncode(t, doc)) {
				t.Fatal("unsafe compaction was pruned or mutated")
			}
		})
	}
	for _, family := range []string{"function", "custom_tool", "tool_search"} {
		doc := kiloPayloadTestSample()
		items := doc["input"].([]any)
		items[8].(map[string]any)["type"] = family + "_call"
		outputKind := family + "_call_output"
		if family == "tool_search" {
			outputKind = "tool_search_output"
		}
		items[9].(map[string]any)["type"] = outputKind
		if !pruneKiloCompaction(doc) {
			t.Fatalf("paired %s chain was blocked", family)
		}
	}
	doc := kiloPayloadTestSample()
	doc["input"] = append(doc["input"].([]any), map[string]any{"type": "tool_search_call", "execution": "server"}, map[string]any{"type": "tool_search_output", "execution": "server"})
	if !pruneKiloCompaction(doc) {
		t.Fatal("hosted tool search unexpectedly required a client call id")
	}
}

func kiloPayloadTestNoise(size int, alpha bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	state := uint32(1234567)
	for index := range img.Pix {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		img.Pix[index] = byte(state)
		if index%4 == 3 && !alpha {
			img.Pix[index] = 255
		}
	}
	return img
}

func kiloPayloadTestImageURL(t *testing.T, img image.Image, format string) string {
	t.Helper()
	var encoded bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&encoded, img)
	} else {
		err = jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 100})
	}
	if err != nil {
		t.Fatal(err)
	}
	return "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}

func kiloPayloadTestImageBytes(t *testing.T, url string) []byte {
	t.Helper()
	_, data, _ := strings.Cut(url, ",")
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestKiloPayloadStructuredImageBudgetAndContent(t *testing.T) {
	url := kiloPayloadTestImageURL(t, kiloPayloadTestNoise(512, false), "png")
	settings := defaultKiloImageSettings()
	settings.MaxEdge, settings.MinEdge, settings.TargetImageKiB = 256, 256, 64
	doc := map[string]any{"input": []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": url, "detail": "high"}}},
	}}
	for _, kind := range []string{"function_call_output", "custom_tool_call_output", "tool_search_output"} {
		doc["input"] = append(doc["input"].([]any), map[string]any{"type": kind, "call_id": kind, "output": []any{
			map[string]any{"type": "input_text", "text": "keep result"},
			map[string]any{"type": "input_image", "image_url": url, "detail": "high"},
			map[string]any{"type": "input_image", "image_url": "https://example.invalid/image.png"},
		}})
	}
	doc["input"] = append(doc["input"].([]any),
		map[string]any{"type": "custom_tool_call_output", "call_id": "text", "output": "data:image/png;base64,unstructured"},
		map[string]any{"type": "reasoning", "encrypted_content": "keep", "content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,unrelated"}}})
	body := kiloPayloadTestEncode(t, doc)
	before := bytes.Clone(body)
	payload, err := prepareKiloRequestPayloadWithOptions(body, kiloPayloadOptions{ImageSettings: &settings})
	if err != nil || !bytes.Equal(body, before) || len(payload) >= len(body) {
		t.Fatalf("structured images were not compressed without side effects: %v", err)
	}
	got := kiloPayloadTestDecode(t, payload)
	images := kiloInlineInputImages(got)
	if len(images) != 4 {
		t.Fatalf("budget omitted structured images: got=%d", len(images))
	}
	for _, part := range images {
		gotURL := part["image_url"].(string)
		if len(gotURL) >= len(url) || !strings.HasPrefix(gotURL, "data:image/png;base64,") || part["detail"] != "high" {
			t.Fatal("PNG format, image content, or detail was lost")
		}
		config, _, decodeErr := image.DecodeConfig(bytes.NewReader(kiloPayloadTestImageBytes(t, gotURL)))
		if decodeErr != nil || config.Width != 256 || config.Height != 256 {
			t.Fatalf("resolution does not respect settings: %v, %v", config, decodeErr)
		}
	}
	items := got["input"].([]any)
	for _, item := range items[1:4] {
		entry := item.(map[string]any)
		parts := entry["output"].([]any)
		if entry["call_id"] != entry["type"] || parts[0].(map[string]any)["text"] != "keep result" || parts[2].(map[string]any)["image_url"] != "https://example.invalid/image.png" {
			t.Fatal("tool result pairing, text or remote image changed")
		}
	}
	if !reflect.DeepEqual(items[4:], doc["input"].([]any)[4:]) {
		t.Fatal("unstructured tool text or unrelated encrypted item was rewritten")
	}
	settings.Enabled = false
	payload, err = prepareKiloRequestPayloadWithOptions(body, kiloPayloadOptions{ImageSettings: &settings})
	if err != nil || !reflect.DeepEqual(kiloPayloadTestDecode(t, payload)["input"], doc["input"]) {
		t.Fatalf("disabled compression changed input: %v", err)
	}
}

func TestKiloPayloadAlphaAndFixedJPEGQuality(t *testing.T) {
	settings := defaultKiloImageSettings()
	settings.MaxEdge, settings.MinEdge = 512, 512
	source := kiloPayloadTestNoise(512, true)
	url := kiloPayloadTestImageURL(t, source, "png")
	result, err := compressKiloInlineImage(url, 1, settings)
	if err != nil || !strings.HasPrefix(result, "data:image/png;base64,") {
		t.Fatalf("alpha conversion failed: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(kiloPayloadTestImageBytes(t, result)))
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []image.Point{{10, 10}, {200, 300}, {0, 0}} {
		got := color.NRGBAModel.Convert(decoded.At(point.X, point.Y))
		if got != source.NRGBAAt(point.X, point.Y) {
			t.Fatalf("alpha or color changed at %v: got=%v want=%v", point, got, source.NRGBAAt(point.X, point.Y))
		}
	}
	jpegURL := kiloPayloadTestImageURL(t, kiloPayloadTestNoise(512, false), "jpeg")
	outputs := make([]string, 0, 2)
	for _, quality := range []int{30, 90} {
		settings.Quality = quality
		output, compressErr := compressKiloInlineImage(jpegURL, 1, settings)
		if compressErr != nil || !strings.HasPrefix(output, "data:image/jpeg;base64,") {
			t.Fatalf("JPEG compression failed: %v", compressErr)
		}
		config, decodeErr := jpeg.DecodeConfig(bytes.NewReader(kiloPayloadTestImageBytes(t, output)))
		if decodeErr != nil || config.Width != 512 || config.Height != 512 {
			t.Fatalf("quality compression resized despite floor: %v, %v", config, decodeErr)
		}
		outputs = append(outputs, output)
	}
	if len(outputs[0]) >= len(outputs[1]) {
		t.Fatal("configured fixed JPEG quality was ignored")
	}
}

func kiloPayloadTestPNGChunk(kind string, data []byte) []byte {
	chunk := make([]byte, 12+len(data))
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(data)))
	copy(chunk[4:8], kind)
	copy(chunk[8:], data)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	return chunk
}

func TestKiloPayloadImageSafetyAndMetadata(t *testing.T) {
	settings := defaultKiloImageSettings()
	for _, url := range []string{"data:image/png;base64,!!!!", "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("not an image")), "data:image/png;base64,YQ==\n"} {
		_, err := compressKiloInlineImage(url, 1, settings)
		var detail *kiloPayloadError
		if !errors.As(err, &detail) || detail.Code != "invalid_image" {
			t.Fatalf("invalid image not rejected: %v", err)
		}
	}
	for _, url := range []string{"https://example.invalid/a.png", "data:image/webp;base64,unchanged", "data:image/gif;base64,unchanged"} {
		result, err := compressKiloInlineImage(url, 1, settings)
		if err != nil || result != url {
			t.Fatalf("remote or unsupported image changed: %v", err)
		}
	}
	pngURL := kiloPayloadTestImageURL(t, image.NewNRGBA(image.Rect(0, 0, 1, 1)), "png")
	pngBytes := kiloPayloadTestImageBytes(t, pngURL)
	for _, kind := range []string{"acTL", "eXIf", "iTXt", "iCCP", "gAMA"} {
		raw := append(bytes.Clone(pngBytes[:33]), kiloPayloadTestPNGChunk(kind, make([]byte, 8))...)
		raw = append(raw, pngBytes[33:]...)
		url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
		result, err := compressKiloInlineImage(url, 1, settings)
		if err != nil || result != url {
			t.Fatalf("PNG %s animation/metadata was discarded: %v", kind, err)
		}
	}
	jpegURL := kiloPayloadTestImageURL(t, image.NewRGBA(image.Rect(0, 0, 2, 3)), "jpeg")
	jpegBytes := kiloPayloadTestImageBytes(t, jpegURL)
	for _, marker := range []byte{0xe1, 0xe2} {
		raw := append(bytes.Clone(jpegBytes[:2]), []byte{0xff, marker, 0, 8, 'E', 'x', 'i', 'f', 0, 0}...)
		raw = append(raw, jpegBytes[2:]...)
		url := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw)
		result, err := compressKiloInlineImage(url, 1, settings)
		if err != nil || result != url {
			t.Fatalf("JPEG EXIF/ICC metadata was discarded: %v", err)
		}
	}
	overPixels := bytes.Clone(pngBytes)
	binary.BigEndian.PutUint32(overPixels[16:20], 8000)
	binary.BigEndian.PutUint32(overPixels[20:24], 5001)
	binary.BigEndian.PutUint32(overPixels[29:33], crc32.ChecksumIEEE(overPixels[12:29]))
	_, err := compressKiloInlineImage("data:image/png;base64,"+base64.StdEncoding.EncodeToString(overPixels), 1, settings)
	var detail *kiloPayloadError
	if !errors.As(err, &detail) || detail.Code != "image_too_large" {
		t.Fatalf("pixel bomb not rejected before decode: %v", err)
	}
}

func TestKiloPayloadHistoryFilterAndPreciseImageRetry(t *testing.T) {
	function := map[string]any{"type": "function", "name": "image_generation", "parameters": map[string]any{}}
	doc := map[string]any{"input": []any{
		map[string]any{"role": "user", "content": "image_generation_call"},
		map[string]any{"type": "image_generation_call", "result": "REMOVE_ONLY_THIS_HISTORY"},
		map[string]any{"type": "reasoning", "encrypted_content": "keep"},
	}, "tools": []any{map[string]any{"type": "namespace", "name": "n", "tools": []any{map[string]any{"type": "image_generation"}, function}}},
		"tool_choice": map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{map[string]any{"type": "image_generation"}, function}}}
	body := kiloPayloadTestEncode(t, doc)
	original := bytes.Clone(body)
	prepared, err := prepareKiloRequestPayload(body, false, "preserve")
	if err != nil || bytes.Contains(prepared, []byte("REMOVE_ONLY_THIS_HISTORY")) || !bytes.Equal(body, original) {
		t.Fatalf("history filter failed or mutated caller: %v", err)
	}
	preparedDoc := kiloPayloadTestDecode(t, prepared)
	if !reflect.DeepEqual(preparedDoc["tools"], doc["tools"]) || len(preparedDoc["input"].([]any)) != 2 {
		t.Fatal("initial preparation removed tool declarations or unrelated context")
	}
	rejection := []byte(`{"error":{"code":"subscription_sharing_unsupported_capability","param":"tools","message":"PRIVATE"}}`)
	candidate, retry, err := retryKiloImageTools(prepared, 400, rejection)
	if err != nil || !retry {
		t.Fatalf("explicit tool rejection was not retried: retry=%v err=%v", retry, err)
	}
	got := kiloPayloadTestDecode(t, candidate)
	wantTools := []any{map[string]any{"type": "namespace", "name": "n", "tools": []any{function}}}
	wantChoice := map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{function}}
	if !reflect.DeepEqual(got["tools"], wantTools) || !reflect.DeepEqual(got["tool_choice"], wantChoice) || !reflect.DeepEqual(got["input"], preparedDoc["input"]) {
		t.Fatal("retry changed non-image tools, choice metadata, or history")
	}
	if !reflect.DeepEqual(kiloPayloadTestDecode(t, prepared), preparedDoc) || !bytes.Contains(rejection, []byte("PRIVATE")) {
		t.Fatal("retry modified request or error bytes")
	}
	second, retry, err := retryKiloImageTools(candidate, 400, rejection)
	if err != nil || retry || second != nil {
		t.Fatalf("retry candidate was eligible a second time: %v, %v", retry, err)
	}
	for _, test := range []struct {
		status  int
		details string
	}{
		{403, string(rejection)}, {200, string(rejection)},
		{400, `{"error":{"code":"subscription_sharing_unsupported_capability","param":"input"}}`},
		{400, `{"error":{"code":"other","param":"tools"}}`},
		{400, `{"message":"subscription_sharing_unsupported_capability tools"}`},
		{400, `{`}, {400, `{"error":{"code":1,"param":"tools"}}`},
	} {
		candidate, retry, err := retryKiloImageTools(prepared, test.status, []byte(test.details))
		if err != nil || retry || candidate != nil {
			t.Fatalf("non-target rejection retried: status=%d details=%s err=%v", test.status, test.details, err)
		}
	}
	candidate, retry, err = retryKiloImageTools([]byte(`{"input":"image_generation_call","tools":[{"type":"function","name":"image_generation"}]}`), 400, rejection)
	if err != nil || retry || candidate != nil {
		t.Fatal("function merely named image_generation was removed")
	}
}

func TestKiloPayloadImageRetryChoiceAndLimit(t *testing.T) {
	rejection := []byte(`{"error":{"code":"subscription_sharing_unsupported_capability","param":"tools"}}`)
	for _, choice := range []any{"required", map[string]any{"type": "image_generation"}, map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{map[string]any{"type": "image_generation"}}}} {
		body := kiloPayloadTestEncode(t, map[string]any{"tools": []any{map[string]any{"type": "namespace", "name": "images", "tools": []any{map[string]any{"type": "image_generation"}}}}, "tool_choice": choice})
		candidate, retry, err := retryKiloImageTools(body, 400, rejection)
		if err != nil || !retry {
			t.Fatalf("choice retry failed: %v", err)
		}
		got := kiloPayloadTestDecode(t, candidate)
		if got["tool_choice"] != "auto" || len(got["tools"].([]any)) != 0 {
			t.Fatalf("dangling required image choice: %v", got)
		}
	}
	body := kiloPayloadTestEncode(t, map[string]any{"input": strings.Repeat("x", kiloPayloadMaxBytes), "tools": []any{map[string]any{"type": "image_generation"}}})
	candidate, retry, err := retryKiloImageTools(body, 400, rejection)
	kiloPayloadTestError(t, candidate, err, 413, "request_too_large")
	if retry {
		t.Fatal("oversize retry candidate escaped send gate")
	}
}

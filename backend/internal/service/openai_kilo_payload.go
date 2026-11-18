package service

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	stddraw "image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"unicode/utf8"

	xdraw "golang.org/x/image/draw"
)

const (
	kiloPayloadMaxBytes    = 20 << 20
	kiloPayloadTargetBytes = 19 << 20
	kiloPayloadIngressMax  = 64 << 20
	kiloImageMaxPixels     = 40_000_000
	kiloImageMinBudget     = 64 << 10
)

// kiloPayloadError 不持有请求正文、图片或上游错误原文，可交由网关现有错误边界处理。
type kiloPayloadError struct {
	StatusCode   int
	Code         string
	Param        string
	Message      string
	PayloadBytes int
}

func (e *kiloPayloadError) Error() string { return e.Message }

// kiloImageSettings 是单次请求的配置快照；helper 不读取环境、账号或持久化设置。
// PNG 保持 PNG 和 alpha，JPEG 使用固定质量；不支持的格式、动画和受保护元数据保持原字节。
type kiloImageSettings struct {
	Enabled        bool
	Quality        int
	MaxEdge        int
	MinEdge        int
	TargetImageKiB int
}

func defaultKiloImageSettings() kiloImageSettings {
	return kiloImageSettings{Enabled: true, Quality: 90, MaxEdge: 2048, MinEdge: 1024, TargetImageKiB: 1024}
}

type kiloPayloadOptions struct {
	Compact        bool
	CompactionMode string
	ImageSettings  *kiloImageSettings
}

// prepareKiloRequestPayload 只准备出站字节，不发送请求、不记录正文、不修改 body。
// 普通 Responses 与冻结上游一致使用 stream=true；独立 /responses/compact 不改 stream、强制 preserve。
// mode 只有精确的 "latest" 才能在压缩后仍超过 20 MiB 时启用安全裁剪。
func prepareKiloRequestPayload(body []byte, compact bool, mode string) ([]byte, error) {
	return prepareKiloRequestPayloadWithOptions(body, kiloPayloadOptions{Compact: compact, CompactionMode: mode})
}

// prepareKiloRequestPayloadWithOptions 允许调用方传入已验证来源的图片配置快照。
// nil 使用 v0.5.2 默认值；关闭压缩时仍保留最终 20 MiB 门禁。
func prepareKiloRequestPayloadWithOptions(body []byte, options kiloPayloadOptions) ([]byte, error) {
	settings := defaultKiloImageSettings()
	if options.ImageSettings != nil {
		settings = *options.ImageSettings
	}
	if err := validateKiloImageSettings(settings); err != nil {
		return nil, err
	}
	doc, err := decodeKiloPayload(body)
	if err != nil {
		return nil, err
	}
	if !options.Compact {
		doc["stream"] = true
	}
	removeKiloImageGenerationHistory(doc)
	payload, err := encodeKiloPayload(doc)
	if err != nil {
		return nil, err
	}

	// 先尝试配置允许的图像缩减，避免可用图像压缩解决的请求损失历史。
	images := kiloInlineInputImages(doc)
	if settings.Enabled && len(images) != 0 {
		nonimageBytes := len(payload)
		for _, part := range images {
			encodedURL, encodeErr := encodeKiloPayload(part["image_url"])
			if encodeErr != nil {
				return nil, encodeErr
			}
			nonimageBytes -= len(encodedURL) - 2
		}
		budget := (kiloPayloadTargetBytes - nonimageBytes) / len(images)
		if budget > settings.TargetImageKiB*1024 {
			budget = settings.TargetImageKiB * 1024
		}
		if budget < kiloImageMinBudget {
			budget = kiloImageMinBudget
		}
		changed := false
		for _, part := range images {
			old := part["image_url"].(string)
			if len(old) <= budget {
				continue
			}
			replacement, compressErr := compressKiloInlineImage(old, budget, settings)
			if compressErr != nil {
				return nil, compressErr
			}
			if len(replacement) < len(old) {
				part["image_url"] = replacement
				changed = true
			}
		}
		if changed {
			payload, err = encodeKiloPayload(doc)
			if err != nil {
				return nil, err
			}
		}
	}

	if len(payload) > kiloPayloadMaxBytes && !options.Compact && options.CompactionMode == "latest" && pruneKiloCompaction(doc) {
		payload, err = encodeKiloPayload(doc)
		if err != nil {
			return nil, err
		}
	}
	if len(payload) > kiloPayloadMaxBytes {
		return nil, kiloPayloadTooLarge(len(payload))
	}
	return payload, nil
}

func validateKiloImageSettings(settings kiloImageSettings) error {
	if settings.Quality < 1 || settings.Quality > 100 || settings.MaxEdge < 256 || settings.MaxEdge > 8192 ||
		settings.MinEdge < 256 || settings.MinEdge > 8192 || settings.MinEdge > settings.MaxEdge ||
		settings.TargetImageKiB < 64 || settings.TargetImageKiB > 10240 {
		return &kiloPayloadError{StatusCode: 400, Code: "invalid_image_settings", Param: "input", Message: "Invalid Kilo image compression settings."}
	}
	return nil
}

func decodeKiloPayload(body []byte) (map[string]any, error) {
	if len(body) > kiloPayloadIngressMax {
		return nil, kiloPayloadTooLarge(len(body))
	}
	if !utf8.Valid(body) {
		return nil, kiloPayloadInvalidJSON()
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil || doc == nil {
		return nil, kiloPayloadInvalidJSON()
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, kiloPayloadInvalidJSON()
	}
	return doc, nil
}

func encodeKiloPayload(value any) ([]byte, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, kiloPayloadInvalidJSON()
	}
	return bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'}), nil
}

func kiloPayloadInvalidJSON() *kiloPayloadError {
	return &kiloPayloadError{StatusCode: 400, Code: "invalid_request", Message: "Kilo request body must be a single valid UTF-8 JSON object."}
}

func kiloPayloadTooLarge(size int) *kiloPayloadError {
	return &kiloPayloadError{StatusCode: 413, Code: "request_too_large", Param: "input", Message: "Kilo request exceeds the 20 MiB payload limit; reduce context or attachments before retrying.", PayloadBytes: size}
}

func kiloPayloadInvalidImage() *kiloPayloadError {
	return &kiloPayloadError{StatusCode: 400, Code: "invalid_image", Param: "input", Message: "Unable to decode the inline Kilo input image."}
}

func removeKiloImageGenerationHistory(doc map[string]any) {
	items, ok := doc["input"].([]any)
	if !ok {
		return
	}
	kept := make([]any, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if ok && entry["type"] == "image_generation_call" {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) != len(items) {
		doc["input"] = kept
	}
}

func kiloItemKind(item map[string]any) string {
	if value, exists := item["type"]; exists {
		kind, _ := value.(string)
		return kind
	}
	return "message"
}

func kiloInlineInputImages(doc map[string]any) []map[string]any {
	items, _ := doc["input"].([]any)
	var images []map[string]any
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var content any
		switch kiloItemKind(entry) {
		case "message":
			content = entry["content"]
		case "function_call_output", "custom_tool_call_output", "tool_search_output":
			content = entry["output"]
		default:
			continue
		}
		parts, _ := content.([]any)
		for _, part := range parts {
			imagePart, ok := part.(map[string]any)
			if !ok || imagePart["type"] != "input_image" {
				continue
			}
			url, ok := imagePart["image_url"].(string)
			if ok && strings.HasPrefix(strings.ToLower(url), "data:image/") && strings.Contains(strings.ToLower(url), ";base64,") {
				images = append(images, imagePart)
			}
		}
	}
	return images
}

// compressKiloInlineImage 只解码本地 PNG/JPEG；远程 URL、WebP/GIF 等格式保持原样。
// 配置像素上限在完整解码前检查。APNG、EXIF/XMP/ICC 等元数据保持原图，避免悄悄转平动画或改变方向/色彩。
func compressKiloInlineImage(url string, budget int, settings kiloImageSettings) (string, error) {
	prefix, data, ok := strings.Cut(url, ",")
	if !ok {
		return url, nil
	}
	format := ""
	switch strings.ToLower(prefix) {
	case "data:image/png;base64":
		format = "png"
	case "data:image/jpeg;base64", "data:image/jpg;base64":
		format = "jpeg"
	default:
		return url, nil
	}
	if strings.ContainsAny(data, "\r\n") {
		return "", kiloPayloadInvalidImage()
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(data)
	if err != nil {
		return "", kiloPayloadInvalidImage()
	}
	var config image.Config
	if format == "png" {
		config, err = png.DecodeConfig(bytes.NewReader(raw))
	} else {
		config, err = jpeg.DecodeConfig(bytes.NewReader(raw))
	}
	if err != nil || config.Width < 1 || config.Height < 1 {
		return "", kiloPayloadInvalidImage()
	}
	if config.Width > kiloImageMaxPixels/config.Height {
		return "", &kiloPayloadError{StatusCode: 400, Code: "image_too_large", Param: "input", Message: "Kilo inline image exceeds the 40 million pixel limit."}
	}
	protected, err := kiloImageHasProtectedMetadata(raw, format)
	if err != nil {
		return "", kiloPayloadInvalidImage()
	}
	if protected {
		return url, nil
	}
	var source image.Image
	if format == "png" {
		source, err = png.Decode(bytes.NewReader(raw))
	} else {
		source, err = jpeg.Decode(bytes.NewReader(raw))
	}
	if err != nil {
		return "", kiloPayloadInvalidImage()
	}

	best := url
	edges := []int{settings.MaxEdge, max(settings.MinEdge, settings.MaxEdge*3/4), settings.MinEdge}
	previous := 0
	for _, edge := range edges {
		if edge == previous {
			continue
		}
		previous = edge
		width, height := kiloImageDimensions(config.Width, config.Height, edge)
		candidate := source
		if width != config.Width || height != config.Height {
			resized := image.NewNRGBA(image.Rect(0, 0, width, height))
			xdraw.CatmullRom.Scale(resized, resized.Bounds(), source, source.Bounds(), stddraw.Src, nil)
			candidate = resized
		}
		var encoded bytes.Buffer
		if format == "png" {
			encoder := png.Encoder{CompressionLevel: png.BestCompression}
			err = encoder.Encode(&encoded, candidate)
		} else {
			err = jpeg.Encode(&encoded, candidate, &jpeg.Options{Quality: settings.Quality})
		}
		if err != nil {
			return "", kiloPayloadInvalidImage()
		}
		mime := "image/" + format
		encodedLength := len("data:"+mime+";base64,") + base64.StdEncoding.EncodedLen(encoded.Len())
		if encodedLength < len(best) {
			best = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
		}
		if len(best) <= budget {
			break
		}
	}
	return best, nil
}

func kiloImageDimensions(width, height, edge int) (int, int) {
	longest := max(width, height)
	if longest <= edge {
		return width, height
	}
	return max(1, int(int64(width)*int64(edge)/int64(longest))), max(1, int(int64(height)*int64(edge)/int64(longest)))
}

// PNG 元数据按 chunk 边界识别，JPEG 按扫描数据前的 marker 边界识别，禁止在像素流里做子串推断。
func kiloImageHasProtectedMetadata(raw []byte, format string) (bool, error) {
	if format == "png" {
		if len(raw) < 8 || !bytes.Equal(raw[:8], []byte("\x89PNG\r\n\x1a\n")) {
			return false, fmt.Errorf("invalid PNG header")
		}
		protected := false
		for offset := 8; offset < len(raw); {
			if len(raw)-offset < 12 {
				return false, fmt.Errorf("invalid PNG chunk")
			}
			size := uint64(binary.BigEndian.Uint32(raw[offset : offset+4]))
			if size+12 > uint64(len(raw)-offset) {
				return false, fmt.Errorf("invalid PNG chunk size")
			}
			kind := string(raw[offset+4 : offset+8])
			switch kind {
			case "acTL", "eXIf", "iCCP", "gAMA", "cHRM", "sRGB", "sBIT", "pHYs", "tEXt", "zTXt", "iTXt", "bKGD", "hIST", "sPLT", "tIME":
				protected = true
			case "IEND":
				return protected, nil
			}
			offset += int(size) + 12
		}
		return false, fmt.Errorf("missing PNG end")
	}
	if len(raw) < 2 || raw[0] != 0xff || raw[1] != 0xd8 {
		return false, fmt.Errorf("invalid JPEG header")
	}
	protected := false
	for offset := 2; offset < len(raw); {
		if raw[offset] != 0xff {
			return false, fmt.Errorf("invalid JPEG marker")
		}
		for offset < len(raw) && raw[offset] == 0xff {
			offset++
		}
		if offset >= len(raw) {
			break
		}
		marker := raw[offset]
		offset++
		if marker == 0xda || marker == 0xd9 {
			return protected, nil
		}
		if marker == 0x01 || marker == 0xd8 || (marker >= 0xd0 && marker <= 0xd7) {
			continue
		}
		if marker == 0x00 || len(raw)-offset < 2 {
			return false, fmt.Errorf("invalid JPEG segment")
		}
		size := int(binary.BigEndian.Uint16(raw[offset : offset+2]))
		if size < 2 || size > len(raw)-offset {
			return false, fmt.Errorf("invalid JPEG segment size")
		}
		if marker == 0xe1 || marker == 0xe2 {
			protected = true
		}
		offset += size
	}
	return false, fmt.Errorf("missing JPEG scan")
}

// pruneKiloCompaction 只在调用方确认超限且显式 latest 时调用。
// 除冻结上游的保留窗口校验外，还拒绝原窗口内任何未知条目、引用或未完成工具链，避免静默丢弃未知语义。
func pruneKiloCompaction(doc map[string]any) bool {
	if previous, exists := doc["previous_response_id"]; exists && previous != nil {
		id, ok := previous.(string)
		if !ok || id != "" {
			return false
		}
	}
	items, ok := doc["input"].([]any)
	if !ok {
		return false
	}
	boundary, count := -1, 0
	for index, item := range items {
		entry, ok := item.(map[string]any)
		if ok && entry["type"] == "compaction" {
			boundary = index
			count++
		}
	}
	if count < 2 || !kiloCompactionWindowSafe(items) {
		return false
	}
	latest := items[boundary].(map[string]any)
	if encrypted, ok := latest["encrypted_content"].(string); !ok || encrypted == "" {
		return false
	}
	kept := make([]any, 0, len(items))
	for _, item := range items[:boundary] {
		entry := item.(map[string]any)
		role, _ := entry["role"].(string)
		if kiloItemKind(entry) == "additional_tools" || (kiloItemKind(entry) == "message" && (role == "system" || role == "developer")) {
			kept = append(kept, item)
		}
	}
	kept = append(kept, items[boundary:]...)
	if !kiloCompactionWindowSafe(kept) {
		return false
	}
	doc["input"] = kept
	return true
}

func kiloCompactionWindowSafe(items []any) bool {
	calls := make(map[string]bool)
	outputs := make(map[string]bool)
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return false
		}
		kind := kiloItemKind(entry)
		call, output := false, false
		switch kind {
		case "message", "reasoning", "compaction", "additional_tools", "web_search_call":
			continue
		case "function_call", "custom_tool_call", "tool_search_call":
			call = true
		case "function_call_output", "custom_tool_call_output", "tool_search_output":
			output = true
		default:
			return false
		}
		if (kind == "tool_search_call" || kind == "tool_search_output") && entry["execution"] == "server" {
			continue
		}
		id, ok := entry["call_id"].(string)
		if !ok || id == "" {
			return false
		}
		if call {
			if calls[id] {
				return false
			}
			calls[id] = true
		} else if output {
			if !calls[id] || outputs[id] {
				return false
			}
			outputs[id] = true
		}
	}
	return len(calls) == len(outputs)
}

// retryKiloImageTools 只为明确的 HTTP 400 capability/tools 拒绝生成候选，不发请求。
// 调用方负责将候选至多发送一次；候选已无目标声明，再次调用不会继续重试。
// 非目标工具及所有历史内容保留，原 body 与 errorBody 均不修改。
func retryKiloImageTools(body []byte, status int, errorBody []byte) ([]byte, bool, error) {
	if status != 400 || len(errorBody) > 1<<20 {
		return nil, false, nil
	}
	var upstream struct {
		Error *struct {
			Code  string `json:"code"`
			Param string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errorBody, &upstream); err != nil || upstream.Error == nil ||
		upstream.Error.Code != "subscription_sharing_unsupported_capability" || upstream.Error.Param != "tools" {
		return nil, false, nil
	}
	doc, err := decodeKiloPayload(body)
	if err != nil {
		return nil, false, err
	}
	removed := 0
	if tools, ok := doc["tools"].([]any); ok {
		var count int
		doc["tools"], count = filterKiloImageTools(tools)
		removed += count
	}
	if choice, ok := doc["tool_choice"].(map[string]any); ok {
		switch choice["type"] {
		case "image_generation":
			doc["tool_choice"] = "auto"
			removed++
		case "allowed_tools":
			if allowed, ok := choice["tools"].([]any); ok {
				filtered, count := filterKiloImageTools(allowed)
				removed += count
				if count > 0 {
					if len(filtered) == 0 {
						doc["tool_choice"] = "auto"
					} else {
						choice["tools"] = filtered
					}
				}
			}
		}
	}
	if removed == 0 {
		return nil, false, nil
	}
	if doc["tool_choice"] == "required" {
		tools, _ := doc["tools"].([]any)
		if len(tools) == 0 {
			doc["tool_choice"] = "auto"
		}
	}
	payload, err := encodeKiloPayload(doc)
	if err != nil {
		return nil, false, err
	}
	if len(payload) > kiloPayloadMaxBytes {
		return nil, false, kiloPayloadTooLarge(len(payload))
	}
	return payload, true, nil
}

func filterKiloImageTools(tools []any) ([]any, int) {
	kept := make([]any, 0, len(tools))
	removed := 0
	for _, tool := range tools {
		entry, ok := tool.(map[string]any)
		if ok && entry["type"] == "image_generation" {
			removed++
			continue
		}
		if ok && entry["type"] == "namespace" {
			if children, ok := entry["tools"].([]any); ok {
				filtered, count := filterKiloImageTools(children)
				removed += count
				if count > 0 {
					if len(filtered) == 0 {
						continue
					}
					entry["tools"] = filtered
				}
			}
		}
		kept = append(kept, tool)
	}
	return kept, removed
}

package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Only the terminal assistant answer is extracted. Reasoning events, deltas,
// headers and raw responses are never persisted or exposed. Failure diagnostics
// use only bounded, sanitized JSON message fields and fixed status categories.
type intelligenceTestResponse struct {
	Status            string          `json:"status"`
	Error             json.RawMessage `json:"error"`
	IncompleteDetails struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Phase   string `json:"phase"`
		Status  string `json:"status"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func (s *IntelligenceTestService) callModel(ctx context.Context, key, mode string, settings IntelligenceTestModeConfig) (string, error) {
	endpoint, err := s.intelligenceEndpoint()
	if err != nil {
		return "", intelligenceTestFailure("invalid_local_endpoint", "本站内部请求端口配置无效。")
	}
	payload := map[string]any{
		"model": settings.Model, "input": []any{map[string]any{"role": "user", "content": settings.Prompt}},
		"stream": true, "store": false, "max_output_tokens": 16384,
	}
	if settings.Effort != "" {
		payload["reasoning"] = map[string]any{"effort": settings.Effort}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", intelligenceTestFailure("request_encoding_failed", "测试请求无法编码。")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", intelligenceTestFailure("request_creation_failed", "无法创建本站内部测试请求。")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "DP-API-Intelligence-Test/1.0")
	if err := AuthorizeIntelligenceTestRequest(req); err != nil {
		return "", intelligenceTestFailure("internal_authorization_failed", "未能建立本次测试的内部调用授权。")
	}
	defer releaseIntelligenceTestRequest(req)
	response, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		code, detail := intelligenceTestErrorInfo(err)
		if code == "request_timeout" {
			return "", intelligenceTestFailure(code, detail)
		}
		return "", intelligenceTestFailure("gateway_connection_failed", "无法连接本站内部模型网关，或连接在响应前中断。")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		data, readErr := io.ReadAll(io.LimitReader(response.Body, intelligenceTestErrorBodyLimit+1))
		detail := "HTTP " + strconv.Itoa(response.StatusCode) + "：本站网关拒绝了测试请求。"
		if readErr == nil {
			if message := intelligenceTestJSONErrorMessage(data, key); message != "" {
				detail += " " + message
			}
		}
		return "", intelligenceTestFailure("gateway_http_"+strconv.Itoa(response.StatusCode), detail)
	}
	limited := &io.LimitedReader{R: response.Body, N: intelligenceTestResponseLimit + 1}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") {
		data, err := io.ReadAll(limited)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", intelligenceTestFailure("response_read_failed", "读取最终响应时连接中断。")
		}
		if len(data) > intelligenceTestResponseLimit {
			return "", intelligenceTestFailure("response_too_large", "网关响应超过测试允许的大小上限。")
		}
		if !utf8.Valid(data) {
			return "", intelligenceTestFailure("invalid_response_encoding", "网关响应包含无效 UTF-8 编码，未保存被替换的文本。")
		}
		var result intelligenceTestResponse
		if err := json.Unmarshal(data, &result); err != nil {
			return "", intelligenceTestFailure("invalid_response_json", "网关未返回有效的最终响应 JSON。")
		}
		return intelligenceTestFinalTextWithDiagnosis(&result, key)
	}
	if !strings.Contains(contentType, "text/event-stream") {
		return "", intelligenceTestFailure("unsupported_response_type", "网关返回的内容类型不是 JSON 或 SSE。")
	}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var data strings.Builder
	consume := func() (string, bool, error) {
		if data.Len() == 0 {
			return "", false, nil
		}
		value := strings.TrimSpace(data.String())
		data.Reset()
		if value == "[DONE]" {
			return "", false, intelligenceTestFailure("stream_missing_completed", "响应流已结束，但未收到 response.completed 最终完成事件。")
		}
		if !utf8.ValidString(value) {
			return "", false, intelligenceTestFailure("invalid_response_encoding", "网关 SSE 事件包含无效 UTF-8 编码，未保存被替换的文本。")
		}
		var event struct {
			Type     string                    `json:"type"`
			Response *intelligenceTestResponse `json:"response"`
		}
		if err := json.Unmarshal([]byte(value), &event); err != nil {
			return "", false, intelligenceTestFailure("invalid_response_event", "网关返回了无法解析的 SSE 事件。")
		}
		switch event.Type {
		case "error", "response.failed", "response.incomplete", "response.canceled", "response.cancelled":
			code := "gateway_stream_error"
			detail := "网关在响应流中报告了测试失败。"
			switch event.Type {
			case "response.failed":
				code, detail = "response_failed", "网关报告本次响应失败。"
			case "response.incomplete":
				code, detail = "response_incomplete", intelligenceTestIncompleteDescription(event.Response)
			case "response.canceled", "response.cancelled":
				code, detail = "request_cancelled", "网关取消了本次响应。"
			}
			message := intelligenceTestJSONErrorMessage([]byte(value), key)
			if event.Response != nil {
				if responseMessage := intelligenceTestJSONErrorMessage(event.Response.Error, key); responseMessage != "" {
					message = responseMessage
				}
			}
			if message != "" {
				detail += " " + message
			}
			return "", false, intelligenceTestFailure(code, detail)
		case "response.completed":
			text, err := intelligenceTestFinalTextWithDiagnosis(event.Response, key)
			return text, true, err
		default:
			return "", false, nil
		}
	}
	for scanner.Scan() {
		if limited.N <= 0 {
			return "", intelligenceTestFailure("response_too_large", "网关响应超过测试允许的大小上限。")
		}
		line := strings.TrimPrefix(strings.TrimSuffix(scanner.Text(), "\r"), "\ufeff")
		if line == "" {
			text, completed, err := consume()
			if err != nil || completed {
				return text, err
			}
		} else if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				_ = data.WriteByte('\n')
			}
			_, _ = data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			if data.Len() > 1024*1024 {
				return "", intelligenceTestFailure("response_event_too_large", "单个 SSE 事件超过测试允许的大小上限。")
			}
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if limited.N <= 0 {
		return "", intelligenceTestFailure("response_too_large", "网关响应超过测试允许的大小上限。")
	}
	if scanner.Err() != nil {
		return "", intelligenceTestFailure("stream_interrupted", "响应流读取中断，或单行数据超过允许的大小上限。")
	}
	text, completed, err := consume()
	if err != nil {
		return "", err
	}
	if !completed {
		return "", intelligenceTestFailure("stream_missing_completed", "响应流已断开，未收到 response.completed 最终完成事件。")
	}
	return text, nil
}

func intelligenceTestFinalText(response *intelligenceTestResponse) (string, error) {
	if response == nil || response.Status != "completed" || (len(response.Error) > 0 && string(response.Error) != "null") {
		return "", intelligenceTestFailure("response_not_completed", "网关响应未达到完整完成状态。")
	}
	var text strings.Builder
	for _, item := range response.Output {
		if item.Type != "message" || item.Role != "assistant" || item.Phase == "commentary" {
			continue
		}
		if item.Status != "" && item.Status != "completed" {
			return "", intelligenceTestFailure("answer_not_completed", "最终助手消息未达到完整完成状态。")
		}
		for _, part := range item.Content {
			if part.Type != "output_text" {
				continue
			}
			if text.Len() > 0 {
				_ = text.WriteByte('\n')
			}
			_, _ = text.WriteString(part.Text)
			if text.Len() > intelligenceTestArtifactLimit {
				return "", intelligenceTestFailure("answer_too_large", "最终助手回复超过 256 KiB 保存上限。")
			}
		}
	}
	answer := strings.TrimSpace(text.String())
	if answer == "" {
		return "", intelligenceTestFailure("empty_final_answer", "网关完成响应，但没有可保存的最终助手文字。")
	}
	return answer, nil
}

func intelligenceTestFinalTextWithDiagnosis(response *intelligenceTestResponse, key string) (string, error) {
	text, err := intelligenceTestFinalText(response)
	if err == nil || response == nil {
		return text, err
	}
	code, detail := intelligenceTestErrorInfo(err)
	switch response.Status {
	case "incomplete":
		code, detail = "response_incomplete", intelligenceTestIncompleteDescription(response)
	case "failed":
		code, detail = "response_failed", "网关报告本次响应失败。"
	case "canceled", "cancelled":
		code, detail = "request_cancelled", "网关取消了本次响应。"
	}
	if message := intelligenceTestJSONErrorMessage(response.Error, key); message != "" {
		detail += " " + message
	}
	return "", intelligenceTestFailure(code, detail)
}

func intelligenceTestIncompleteDescription(response *intelligenceTestResponse) string {
	if response != nil {
		switch response.IncompleteDetails.Reason {
		case "max_output_tokens":
			return "模型输出达到 max_output_tokens 限制，未形成完整最终回复。"
		case "content_filter":
			return "网关因内容限制终止响应，未形成完整最终回复。"
		}
	}
	return "网关报告响应未完成，未保存未完成的回答。"
}

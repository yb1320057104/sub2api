package service

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const accountCandyProbeKey = "pk_account_candy_probe"

type accountCandyProbeError struct{ message string }

func (e *accountCandyProbeError) Error() string { return e.message }
func candyProbeFailure(message string) error    { return &accountCandyProbeError{message: message} }

type accountCandyProbe struct {
	prompt, effort, text string
	credentialAccount    *Account
}

func candyProbe(c *gin.Context) *accountCandyProbe {
	value, ok := c.Get(accountCandyProbeKey)
	if !ok {
		return nil
	}
	probe, _ := value.(*accountCandyProbe)
	return probe
}

func (s *AccountTestService) runAccountCandyProbe(ctx context.Context, account, credentialAccount *Account, cfg *AccountCandyTestConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if account == nil || credentialAccount == nil {
		return "", candyProbeFailure("测试账号不存在。")
	}
	if account.Platform != PlatformOpenAI || account.IsSyntheticUITest() {
		return "", candyProbeFailure("糖果测试仅支持真实 OpenAI 账号。")
	}
	if _, err := accountCandyProxyURL(account); err != nil {
		return "", err
	}
	if isOpenAIImageModel(account.GetMappedModel(cfg.Model)) {
		return "", candyProbeFailure("该账号将测试模型映射到了图片模型，请选择文本模型。")
	}
	// The existing account probe owns identity, model mapping, proxy, TLS,
	// credential shadow resolution and Kilo translation. This private context
	// changes only the prompt and strict final-answer decoder for this feature.
	probe := &accountCandyProbe{prompt: intelligenceTestCandyPrompt + "\n\n请给出你的解答，并在最后单独一行输出 <final_answer>整数</final_answer>，标签中只填写你求得的最少糖果数目。", effort: cfg.Effort, credentialAccount: credentialAccount}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = (&http.Request{Header: make(http.Header)}).WithContext(ctx)
	c.Set(accountCandyProbeKey, probe)
	c.Set(accountTestBackgroundKey, true)
	initAccountTestLogger(c, account.ID, cfg.Model, AccountTestModeDefault)
	bindAccountTestPlatform(c, account)
	err := s.testOpenAIAccountConnection(c, account, cfg.Model, probe.prompt, AccountTestModeDefault)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(probe.text) == "" {
		return "", candyProbeFailure("未取得完整的最终回复；未转组。")
	}
	return probe.text, nil
}

func applyAccountCandyPayload(c *gin.Context, payload map[string]any, chat, isOAuth bool) {
	probe := candyProbe(c)
	if probe == nil {
		return
	}
	if chat {
		payload["messages"] = []map[string]any{{"role": "user", "content": probe.prompt}}
		payload["max_completion_tokens"] = 16384
		if probe.effort != "" {
			payload["reasoning_effort"] = probe.effort
		}
	} else {
		payload["input"] = []map[string]any{{"role": "user", "content": []map[string]any{{"type": "input_text", "text": probe.prompt}}}}
		if !isOAuth {
			payload["max_output_tokens"] = 16384
		}
		if probe.effort != "" {
			payload["reasoning"] = map[string]any{"effort": probe.effort}
		}
	}
}

func finishAccountCandyProbe(c *gin.Context, response *http.Response, chat bool) error {
	probe := candyProbe(c)
	text, err := readAccountCandyReply(response, chat)
	if err != nil {
		return err
	}
	probe.text = text
	return nil
}

type accountCandyChatResponse struct {
	Error   json.RawMessage `json:"error"`
	Choices []struct {
		Index        int     `json:"index"`
		FinishReason *string `json:"finish_reason"`
		Message      struct {
			Role, Content string
			ToolCalls     json.RawMessage `json:"tool_calls"`
			FunctionCall  json.RawMessage `json:"function_call"`
		} `json:"message"`
		Delta struct {
			Role, Content string
			ToolCalls     json.RawMessage `json:"tool_calls"`
			FunctionCall  json.RawMessage `json:"function_call"`
		} `json:"delta"`
	} `json:"choices"`
}

func candyHasJSONValue(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null" && string(raw) != "[]"
}

func accountCandyFinalText(response *intelligenceTestResponse) (string, error) {
	if response == nil || response.IncompleteDetails.Reason != "" {
		return "", candyProbeFailure("模型响应不完整；未转组。")
	}
	for _, item := range response.Output {
		if item.Status != "" && item.Status != "completed" {
			return "", candyProbeFailure("模型输出尚未完整结束；未转组。")
		}
		// No tools are supplied to this probe. Unknown output and tool calls
		// must not be mistaken for a final answer even if text accompanies them.
		if item.Type != "message" && item.Type != "reasoning" {
			return "", candyProbeFailure("模型返回了工具请求或不支持的输出；未转组。")
		}
		if item.Type == "message" && item.Role != "assistant" {
			return "", candyProbeFailure("模型消息角色无效；未转组。")
		}
	}
	return intelligenceTestFinalText(response)
}

func readAccountCandyReply(response *http.Response, chat bool) (string, error) {
	fail := func(message string) (string, error) {
		return "", candyProbeFailure(message)
	}
	if response == nil || response.Body == nil {
		return fail("响应正文为空或类型不受支持；未转组。")
	}
	limited := &io.LimitedReader{R: response.Body, N: intelligenceTestResponseLimit + 1}
	reader := bufio.NewReader(limited)
	// Some upstreams omit or mislabel Content-Type. Detect the wire format
	// without waiting for EOF, while retaining whitespace for SSE line limits.
	var prefix strings.Builder
	var first rune
	sawBOM := false
	for {
		r, size, err := reader.ReadRune()
		if limited.N <= 0 {
			return fail("模型响应超过 4 MiB；未转组。")
		}
		if err == io.EOF {
			return fail("响应正文为空或类型不受支持；未转组。")
		}
		if err != nil {
			return fail("读取模型响应失败；未转组。")
		}
		if r == utf8.RuneError && size == 1 {
			return fail("响应包含无效文本编码；未转组。")
		}
		if r == '\ufeff' && !sawBOM {
			sawBOM = true
			_, _ = prefix.WriteRune(r)
			continue
		}
		if strings.ContainsRune(" \t\r\n", r) {
			_, _ = prefix.WriteRune(r)
			continue
		}
		first = r
		if err := reader.UnreadRune(); err != nil {
			return fail("读取模型响应失败；未转组。")
		}
		break
	}
	prefixText := prefix.String()
	if first == '{' || first == '[' {
		prefixText = strings.Replace(prefixText, "\ufeff", "", 1)
	}
	body := io.MultiReader(strings.NewReader(prefixText), reader)
	if first == '{' || first == '[' {
		data, err := io.ReadAll(body)
		if limited.N <= 0 || len(data) > intelligenceTestResponseLimit {
			return fail("模型响应超过 4 MiB；未转组。")
		}
		if err != nil {
			return fail("读取模型响应失败；未转组。")
		}
		if !utf8.Valid(data) {
			return fail("响应包含无效文本编码；未转组。")
		}
		if !chat {
			var result intelligenceTestResponse
			if json.Unmarshal(data, &result) != nil {
				return fail("JSON 响应格式无效；未转组。")
			}
			text, err := accountCandyFinalText(&result)
			if err != nil {
				return fail("JSON 响应未完整结束或缺少有效最终回复；未转组。")
			}
			return text, nil
		}
		var result accountCandyChatResponse
		if json.Unmarshal(data, &result) != nil {
			return fail("JSON 响应格式无效；未转组。")
		}
		if candyHasJSONValue(result.Error) || len(result.Choices) != 1 {
			return fail("JSON 响应未完整结束或缺少有效最终回复；未转组。")
		}
		choice := result.Choices[0]
		if choice.FinishReason == nil || *choice.FinishReason != "stop" || choice.Message.Role != "assistant" || candyHasJSONValue(choice.Message.ToolCalls) || candyHasJSONValue(choice.Message.FunctionCall) {
			return fail("JSON 响应未完整结束或缺少有效最终回复；未转组。")
		}
		text := strings.TrimSpace(choice.Message.Content)
		if text == "" || len(text) > intelligenceTestArtifactLimit {
			return fail("JSON 响应未完整结束或缺少有效最终回复；未转组。")
		}
		return text, nil
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var event, text strings.Builder
	finished, assistant, sawData := false, false, false
	consume := func() (string, bool, error) {
		if event.Len() == 0 {
			return "", false, nil
		}
		data := strings.TrimSpace(event.String())
		event.Reset()
		if data == "[DONE]" {
			if chat && finished && assistant && strings.TrimSpace(text.String()) != "" {
				return strings.TrimSpace(text.String()), true, nil
			}
			return "", false, candyProbeFailure("响应流缺少完整最终回复；未转组。")
		}
		if !utf8.ValidString(data) {
			return "", false, candyProbeFailure("响应包含无效文本编码；未转组。")
		}
		if !chat {
			var message struct {
				Type     string                    `json:"type"`
				Response *intelligenceTestResponse `json:"response"`
				Error    json.RawMessage           `json:"error"`
				Item     struct {
					Type string `json:"type"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(data), &message) != nil {
				return "", false, candyProbeFailure("响应事件格式无效；未转组。")
			}
			if candyHasJSONValue(message.Error) || (message.Response != nil && candyHasJSONValue(message.Response.Error)) {
				return "", false, candyProbeFailure("模型返回错误；未转组。")
			}
			if strings.Contains(message.Type, "_call") || strings.HasPrefix(message.Type, "response.mcp_list_tools.") ||
				(message.Item.Type != "" && message.Item.Type != "message" && message.Item.Type != "reasoning") {
				return "", false, candyProbeFailure("模型返回了工具请求或不支持的输出；未转组。")
			}
			switch message.Type {
			case "response.completed", "response.done":
				final, err := accountCandyFinalText(message.Response)
				if err != nil {
					return "", false, candyProbeFailure("模型没有完整完成最终回复；未转组。")
				}
				return final, true, nil
			case "error", "response.failed", "response.incomplete", "response.canceled", "response.cancelled":
				return "", false, candyProbeFailure("模型响应失败或未完成；未转组。")
			default:
				return "", false, nil
			}
		}
		var message accountCandyChatResponse
		if json.Unmarshal([]byte(data), &message) != nil || candyHasJSONValue(message.Error) {
			return "", false, candyProbeFailure("模型响应事件无效；未转组。")
		}
		for _, choice := range message.Choices {
			if choice.Index != 0 || candyHasJSONValue(choice.Delta.ToolCalls) || candyHasJSONValue(choice.Delta.FunctionCall) {
				return "", false, candyProbeFailure("模型返回了工具调用而非完整最终回复；未转组。")
			}
			if choice.Delta.Role != "" {
				if choice.Delta.Role != "assistant" {
					return "", false, candyProbeFailure("模型消息角色无效；未转组。")
				}
				assistant = true
			}
			if choice.Delta.Content != "" {
				if finished {
					return "", false, candyProbeFailure("模型结束后的回复内容无效；未转组。")
				}
				assistant = true
				_, _ = text.WriteString(choice.Delta.Content)
				if text.Len() > intelligenceTestArtifactLimit {
					return "", false, candyProbeFailure("最终回复超过 256 KiB；未转组。")
				}
			}
			if choice.FinishReason != nil {
				if finished || *choice.FinishReason != "stop" {
					return "", false, candyProbeFailure("模型回复被截断或没有正常结束；未转组。")
				}
				finished = true
			}
		}
		return "", false, nil
	}
	for scanner.Scan() {
		if limited.N <= 0 {
			return fail("模型响应超过 4 MiB；未转组。")
		}
		// Retain trailing payload whitespace until after the event-size check.
		line := strings.TrimLeftFunc(scanner.Text(), unicode.IsSpace)
		line = strings.TrimLeftFunc(strings.TrimPrefix(line, "\ufeff"), unicode.IsSpace)
		if strings.TrimSpace(line) == "" {
			result, done, err := consume()
			if err != nil || done {
				return result, err
			}
		} else if strings.HasPrefix(line, "data:") {
			sawData = true
			if event.Len() > 0 {
				_ = event.WriteByte('\n')
			}
			_, _ = event.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			if event.Len() > 1024*1024 {
				return fail("响应事件超过 1 MiB；未转组。")
			}
		}
	}
	if limited.N <= 0 {
		return fail("模型响应超过 4 MiB；未转组。")
	}
	if scanner.Err() == bufio.ErrTooLong {
		return fail("响应事件行超过 1 MiB；未转组。")
	}
	if scanner.Err() != nil {
		return fail("读取模型响应失败；未转组。")
	}
	result, done, err := consume()
	if err != nil {
		return "", err
	}
	if done {
		return result, nil
	}
	if !sawData {
		return fail("响应正文为空或类型不受支持；未转组。")
	}
	return fail("响应流缺少完整成功结束标记；未转组。")
}

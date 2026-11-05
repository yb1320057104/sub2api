package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const accountCandyProbeKey = "pk_account_candy_probe"

type accountCandyProbeError struct {
	message     string
	httpStatus  int
	networkRead bool
	recoverable bool
}

func (e *accountCandyProbeError) Error() string {
	if e.httpStatus >= 100 && e.httpStatus <= 599 {
		return fmt.Sprintf("HTTP %d：%s", e.httpStatus, e.message)
	}
	return e.message
}
func candyProbeFailure(message string) error { return &accountCandyProbeError{message: message} }

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
	credentialSnapshot := accountCandyCredentialSnapshot(account, credentialAccount)
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
	err = s.accountCandyCredentialFailure(ctx, credentialSnapshot, credentialAccount, err)
	var authFailure *accountCandyProbeError
	if errors.As(err, &authFailure) && authFailure.httpStatus == http.StatusUnauthorized {
		result := *authFailure
		result.message = redactCandyAccountError(result.message, account, credentialAccount)
		return "", &result
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		var failure *accountCandyProbeError
		if errors.As(err, &failure) {
			result := *failure
			result.message = redactCandyAccountError(result.message, account, credentialAccount)
			return "", &result
		}
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
		var failure *accountCandyProbeError
		_, recovering := c.Request.Context().Value(accountCandyRecoveryKey{}).(*accountCandyRecovery)
		if radar, _ := c.Request.Context().Value(radarCandyProxyRetryKey{}).(bool); radar && !recovering && c.Request.Context().Err() == nil && errors.As(err, &failure) && failure.networkRead {
			return &radarCandyRetryableNetworkError{HTTPStatus: failure.httpStatus}
		}
		return err
	}
	probe.text = text
	return nil
}

type accountCandyChatResponse struct {
	Type    string          `json:"type"`
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
	if response != nil && candyHasJSONValue(response.Error) {
		return "", accountCandyUpstreamFailure("模型返回错误", response.Error)
	}
	if response == nil || response.IncompleteDetails.Reason != "" {
		return "", candyProbeFailure("模型响应不完整；未转组。")
	}
	if response.Status == "completed" && len(response.Output) == 0 {
		return "", &accountCandyProbeError{message: "模型响应未完整结束或缺少有效最终回复；未转组。", recoverable: true}
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
	text, err := intelligenceTestFinalText(response)
	if err != nil {
		return "", candyProbeFailure("模型响应未完整结束或缺少有效最终回复；未转组。")
	}
	return text, nil
}

func readAccountCandyReply(response *http.Response, chat bool) (text string, resultErr error) {
	// Keep the wire status separate from untrusted diagnostic text. Redaction
	// may replace the entire message (for example HTML), never the actual code.
	defer func() {
		var failure *accountCandyProbeError
		if response != nil && errors.As(resultErr, &failure) {
			failure.httpStatus = response.StatusCode
		}
	}()
	fail := func(message string) (string, error) {
		return "", candyProbeFailure(message)
	}
	readFail := func(err error) (string, error) {
		network := isRadarCandyNetworkFailure(err)
		return "", &accountCandyProbeError{message: "读取模型响应失败；未转组。", networkRead: network, recoverable: network}
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
			return readFail(err)
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
			return readFail(err)
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
		// A transport may report a truncated HTTP body after delivering a whole
		// JSON document (for example an incorrect Content-Length). Its complete
		// answer or structured error must not be replayed as a network failure.
		if err != nil && !json.Valid(data) {
			return readFail(err)
		}
		if !utf8.Valid(data) {
			return fail("响应包含无效文本编码；未转组。")
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Type == "error" {
			return "", accountCandyUpstreamFailure("模型返回错误", data)
		}
		if !chat {
			var result intelligenceTestResponse
			if json.Unmarshal(data, &result) != nil {
				return fail("JSON 响应格式无效；未转组。")
			}
			text, err := accountCandyFinalText(&result)
			if err != nil {
				return "", err
			}
			return text, nil
		}
		var result accountCandyChatResponse
		if json.Unmarshal(data, &result) != nil {
			return fail("JSON 响应格式无效；未转组。")
		}
		if candyHasJSONValue(result.Error) {
			return "", accountCandyUpstreamFailure("模型返回错误", result.Error)
		}
		if len(result.Choices) != 1 {
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
	var event, reply strings.Builder
	var eventName string
	doneItems := newAccountCandyDoneItems()
	finished, assistant, sawData := false, false, false
	sawProgress := false
	consume := func() (string, bool, error) {
		name := eventName
		eventName = ""
		if event.Len() == 0 {
			return "", false, nil
		}
		data := strings.TrimSpace(event.String())
		event.Reset()
		if data == "[DONE]" {
			if chat && finished && assistant && strings.TrimSpace(reply.String()) != "" {
				return strings.TrimSpace(reply.String()), true, nil
			}
			return "", false, &accountCandyProbeError{message: "响应流缺少完整最终回复；未转组。", recoverable: sawProgress}
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
			if name != "" && message.Type != "" && name != message.Type {
				return "", false, candyProbeFailure("响应事件类型冲突；未转组。")
			}
			if message.Type == "" {
				message.Type = name
			}
			if candyHasJSONValue(message.Error) || (message.Response != nil && candyHasJSONValue(message.Response.Error)) || message.Type == "error" || message.Type == "response.failed" {
				return "", false, accountCandyUpstreamFailure("模型返回错误", []byte(data))
			}
			if strings.Contains(message.Type, "_call") || strings.HasPrefix(message.Type, "response.mcp_list_tools.") ||
				(message.Item.Type != "" && message.Item.Type != "message" && message.Item.Type != "reasoning") {
				return "", false, candyProbeFailure("模型返回了工具请求或不支持的输出；未转组。")
			}
			switch message.Type {
			case "response.completed", "response.done":
				if err := doneItems.complete(message.Response, []byte(data)); err != nil {
					return "", false, err
				}
				final, err := accountCandyFinalText(message.Response)
				if err != nil {
					return "", false, err
				}
				return final, true, nil
			case "error", "response.failed", "response.incomplete", "response.canceled", "response.cancelled":
				return "", false, candyProbeFailure("模型响应失败或未完成；未转组。")
			case "response.output_item.done":
				sawProgress = true
				return "", false, doneItems.observe([]byte(data))
			default:
				if strings.HasPrefix(message.Type, "response.") {
					sawProgress = true
				}
				return "", false, nil
			}
		}
		var message accountCandyChatResponse
		if json.Unmarshal([]byte(data), &message) != nil {
			return "", false, candyProbeFailure("模型响应事件无效；未转组。")
		}
		if candyHasJSONValue(message.Error) || message.Type == "error" {
			return "", false, accountCandyUpstreamFailure("模型返回错误", []byte(data))
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
				sawProgress = true
				if finished {
					return "", false, candyProbeFailure("模型结束后的回复内容无效；未转组。")
				}
				assistant = true
				_, _ = reply.WriteString(choice.Delta.Content)
				if reply.Len() > intelligenceTestArtifactLimit {
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
		} else if strings.HasPrefix(line, "event:") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			if eventName != "" && name != eventName {
				return fail("响应事件类型冲突；未转组。")
			}
			eventName = name
		}
	}
	if limited.N <= 0 {
		return fail("模型响应超过 4 MiB；未转组。")
	}
	if scanner.Err() == bufio.ErrTooLong {
		return fail("响应事件行超过 1 MiB；未转组。")
	}
	// A complete buffered terminal frame remains authoritative even if the
	// transport reports a short body after its last byte. Truncated JSON must
	// retain the typed transport failure so only that request can be retried.
	if scanner.Err() != nil && !json.Valid([]byte(strings.TrimSpace(event.String()))) && strings.TrimSpace(event.String()) != "[DONE]" {
		return readFail(scanner.Err())
	}
	result, done, err := consume()
	if err != nil {
		return "", err
	}
	if done {
		return result, nil
	}
	if scanner.Err() != nil {
		return readFail(scanner.Err())
	}
	if !sawData {
		return fail("响应正文为空或类型不受支持；未转组。")
	}
	return "", &accountCandyProbeError{message: "响应流缺少完整成功结束标记；未转组。", recoverable: sawProgress}
}

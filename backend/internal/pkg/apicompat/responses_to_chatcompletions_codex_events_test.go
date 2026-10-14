package apicompat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// custom_tool_call（custom/freeform 工具，如新版 apply_patch）应像 function_call 一样
// 注册为工具调用，其 *_input.delta 增量映射到正确的工具索引。
func TestResponsesEventToChatChunks_CustomToolCallInputDelta(t *testing.T) {
	state := NewResponsesEventToChatState()
	state.Model = "gpt-5-codex"
	state.SentRole = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 1,
		Item: &ResponsesOutput{
			Type:   "custom_tool_call",
			CallID: "call_patch",
			Name:   "apply_patch",
		},
	}, state)
	require.Len(t, chunks, 1)
	require.Len(t, chunks[0].Choices[0].Delta.ToolCalls, 1)
	tc := chunks[0].Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "call_patch", tc.ID)
	assert.Equal(t, "apply_patch", tc.Function.Name)

	chunks = ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.custom_tool_call_input.delta",
		OutputIndex: 1,
		Delta:       "*** Begin Patch",
	}, state)
	require.Len(t, chunks, 1)
	tc = chunks[0].Choices[0].Delta.ToolCalls[0]
	require.NotNil(t, tc.Index)
	assert.Equal(t, 0, *tc.Index)
	assert.Equal(t, "*** Begin Patch", tc.Function.Arguments)
}

// 原始推理文本增量 reasoning_text.delta 应像 reasoning_summary_text.delta 一样
// 映射为 reasoning_content。
func TestResponsesEventToChatChunks_ReasoningTextDelta(t *testing.T) {
	state := NewResponsesEventToChatState()
	state.Model = "gpt-5-codex"
	state.SentRole = true

	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:  "response.reasoning_text.delta",
		Delta: "thinking step",
	}, state)
	require.Len(t, chunks, 1)
	require.NotNil(t, chunks[0].Choices[0].Delta.ReasoningContent)
	assert.Equal(t, "thinking step", *chunks[0].Choices[0].Delta.ReasoningContent)
}

// 缓冲（非流式）累加器同样需识别两类新事件。
func TestBufferedResponseAccumulator_CodexEvents(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: 0,
		Item:        &ResponsesOutput{Type: "custom_tool_call", CallID: "c1", Name: "apply_patch"},
	})
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:        "response.custom_tool_call_input.delta",
		OutputIndex: 0,
		Delta:       "patch-body",
	})
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type:  "response.reasoning_text.delta",
		Delta: "raw-reasoning",
	})
	require.True(t, acc.HasContent())
}

func TestPKKiloCustomToolCompletedOutput(t *testing.T) {
	input := `{"message":"gAAAA_task","model":"user/model"}`
	response := &ResponsesResponse{ID: "resp_kilo", Output: []ResponsesOutput{{Type: "custom_tool_call", CallID: "call_task", Name: "spawn_agent", Input: input}}}
	chat := ResponsesToChatCompletions(response, "model")
	require.Len(t, chat.Choices, 1)
	require.Len(t, chat.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "call_task", chat.Choices[0].Message.ToolCalls[0].ID)
	require.Equal(t, input, chat.Choices[0].Message.ToolCalls[0].Function.Arguments)
	anthropic := ResponsesToAnthropic(response, "model")
	require.Len(t, anthropic.Content, 1)
	require.Equal(t, "tool_use", anthropic.Content[0].Type)
	require.JSONEq(t, input, string(anthropic.Content[0].Input))
}

package service

import (
	"encoding/json"
	"reflect"
)

// Only done items can fill an omitted terminal output. Deltas never establish
// completion, and conflicting or incomplete item identities fail closed.
type accountCandyDoneItems struct {
	items map[int]json.RawMessage
	ids   map[string]int
}

func newAccountCandyDoneItems() *accountCandyDoneItems {
	return &accountCandyDoneItems{items: make(map[int]json.RawMessage), ids: make(map[string]int)}
}

func (d *accountCandyDoneItems) observe(data []byte) error {
	var event struct {
		Index *int            `json:"output_index"`
		Item  json.RawMessage `json:"item"`
	}
	if json.Unmarshal(data, &event) != nil || event.Index == nil || *event.Index < 0 || *event.Index > 1024 {
		return candyProbeFailure("已完成输出的索引无效；未转组。")
	}
	var item struct {
		ID, Type, Status, Role string
	}
	if json.Unmarshal(event.Item, &item) != nil || (item.Type != "message" && item.Type != "reasoning") ||
		(item.Status != "" && item.Status != "completed") || (item.Type == "message" && item.Role != "assistant") {
		return candyProbeFailure("已完成输出的类型、角色或状态无效；未转组。")
	}
	if old, exists := d.items[*event.Index]; exists && !accountCandySameItem(old, event.Item) {
		return candyProbeFailure("已完成输出的内容冲突；未转组。")
	}
	if item.ID != "" {
		if index, exists := d.ids[item.ID]; exists && index != *event.Index {
			return candyProbeFailure("已完成输出的身份冲突；未转组。")
		}
		d.ids[item.ID] = *event.Index
	}
	d.items[*event.Index] = event.Item
	return nil
}

func accountCandySameItem(a, b json.RawMessage) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}

func (d *accountCandyDoneItems) complete(response *intelligenceTestResponse, data []byte) error {
	if len(d.items) == 0 || response == nil || response.Status != "completed" || candyHasJSONValue(response.Error) || response.IncompleteDetails.Reason != "" {
		return nil
	}
	var terminal struct {
		Response struct {
			Output []json.RawMessage `json:"output"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &terminal) != nil {
		return candyProbeFailure("最终输出格式无效；未转组。")
	}
	output := terminal.Response.Output
	for index, item := range d.items {
		if index < len(output) && !accountCandySameItem(item, output[index]) {
			return candyProbeFailure("最终输出与已完成输出冲突；未转组。")
		}
	}
	for index := len(output); ; index++ {
		item, exists := d.items[index]
		if !exists {
			break
		}
		output = append(output, item)
	}
	for index := range d.items {
		if index >= len(output) {
			return candyProbeFailure("已完成输出的索引不连续；未转组。")
		}
	}
	encoded, err := json.Marshal(output)
	if err != nil || json.Unmarshal(encoded, &response.Output) != nil {
		return candyProbeFailure("最终输出无法读取；未转组。")
	}
	return nil
}

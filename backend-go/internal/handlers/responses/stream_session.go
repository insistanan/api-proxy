package responses

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
)

// responsesStreamSessionCollector 只收集可重放的 Responses item。
//
// SSE 中的正文 delta、工具参数 delta、reasoning summary delta 和
// response.completed.output 是不同层次的数据，不能共用一个字符串缓冲区。
// 这个收集器的输出会进入代理自己的 session；reasoning 保持为 reasoning item，
// 默认转换器会跳过它，只有显式 IncludeHistoryThinking 时才会回灌。
type responsesStreamSessionCollector struct {
	text      strings.Builder
	reasoning strings.Builder

	streamItems     map[string]*types.ResponsesItem
	streamItemOrder []string
	completedOutput []types.ResponsesItem
	completed       bool
	completedTool   bool
	err             error
}

func newResponsesStreamSessionCollector() *responsesStreamSessionCollector {
	return &responsesStreamSessionCollector{
		streamItems: make(map[string]*types.ResponsesItem),
	}
}

func (c *responsesStreamSessionCollector) consumeEvent(event string) {
	if c == nil {
		return
	}
	for lineIndex, line := range strings.Split(event, "\n") {
		payload, isData := utils.SSEDataJSON(line)
		if !isData {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)
		switch eventType {
		case "response.output_text.delta":
			if delta, ok := data["delta"].(string); ok {
				c.text.WriteString(delta)
			}
		case "response.output_text.done":
			// done 通常重复此前的 delta；只在没有 delta 的上游回退时使用。
			if c.text.Len() == 0 {
				if text, ok := data["text"].(string); ok {
					c.text.WriteString(text)
				}
			}
		case "response.reasoning_summary_text.delta":
			if delta, ok := data["delta"].(string); ok {
				c.reasoning.WriteString(delta)
			}
		case "response.reasoning_summary_text.done":
			// done 可能是完整 summary，不能和已有 delta 再拼一次。
			if c.reasoning.Len() == 0 {
				if text, ok := data["text"].(string); ok {
					c.reasoning.WriteString(text)
				}
			}
		case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
			c.captureToolArgumentDelta(data, eventType, lineIndex)
		case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
			c.captureToolArgumentDone(data, eventType, lineIndex)
		case "response.output_item.added", "response.output_item.done":
			if itemMap, ok := data["item"].(map[string]interface{}); ok {
				item := parseResponsesItemMap(itemMap)
				c.upsertItem(item, responsesStreamItemKey(data, itemMap, lineIndex))
				if eventType == "response.output_item.done" &&
					(item.Type == "function_call" || item.Type == "custom_tool_call") {
					c.completedTool = true
				}
			}
		case "response.completed":
			c.captureCompletedOutput(data)
			if c.err == nil {
				c.completed = true
			}
		}
	}
}

func (c *responsesStreamSessionCollector) captureToolArgumentDelta(data map[string]interface{}, eventType string, fallback int) {
	key := responsesStreamItemKey(data, nil, fallback)
	if key == "" {
		return
	}
	itemType := "function_call"
	if eventType == "response.custom_tool_call_input.delta" {
		itemType = "custom_tool_call"
	}
	item := types.ResponsesItem{
		Type:    itemType,
		ID:      stringValue(data["item_id"]),
		CallID:  stringValue(data["call_id"]),
		Name:    stringValue(data["name"]),
		Content: nil,
	}
	if itemType == "function_call" {
		item.Arguments = stringValue(data["delta"])
	} else {
		item.Content = stringValue(data["delta"])
	}
	if previous := c.streamItems[key]; previous != nil {
		if item.CallID != "" {
			previous.CallID = item.CallID
		}
		if item.Name != "" {
			previous.Name = item.Name
		}
		if itemType == "function_call" {
			previous.Arguments += item.Arguments
		} else {
			previousContent, _ := previous.Content.(string)
			previous.Content = previousContent + item.Content.(string)
		}
		return
	}
	c.upsertItem(item, key)
}

func (c *responsesStreamSessionCollector) captureToolArgumentDone(data map[string]interface{}, eventType string, fallback int) {
	key := responsesStreamItemKey(data, nil, fallback)
	if key == "" {
		return
	}
	itemType := "function_call"
	if eventType == "response.custom_tool_call_input.done" {
		itemType = "custom_tool_call"
	}
	item := types.ResponsesItem{
		Type:   itemType,
		ID:     stringValue(data["item_id"]),
		CallID: stringValue(data["call_id"]),
		Name:   stringValue(data["name"]),
	}
	if itemType == "function_call" {
		item.Arguments = stringValue(data["arguments"])
	} else {
		item.Content = data["input"]
		if item.Content == nil {
			item.Content = data["delta"]
		}
	}
	c.upsertItem(item, key)
	if previous := c.streamItems[key]; previous != nil {
		if item.Arguments != "" {
			previous.Arguments = item.Arguments
		}
		if item.Content != nil {
			previous.Content = item.Content
		}
	}
}

func (c *responsesStreamSessionCollector) upsertItem(item types.ResponsesItem, key string) {
	if c == nil || key == "" || item.Type == "" {
		return
	}
	previous, exists := c.streamItems[key]
	if !exists || previous == nil {
		copyItem := item
		c.streamItems[key] = &copyItem
		c.streamItemOrder = append(c.streamItemOrder, key)
		return
	}

	if item.ID != "" {
		previous.ID = item.ID
	}
	if item.Type != "" {
		previous.Type = item.Type
	}
	if item.Status != "" {
		previous.Status = item.Status
	}
	if item.Role != "" {
		previous.Role = item.Role
	}
	if item.CallID != "" {
		previous.CallID = item.CallID
	}
	if item.Name != "" {
		previous.Name = item.Name
	}
	if item.Namespace != "" {
		previous.Namespace = item.Namespace
	}
	if item.Execution != "" {
		previous.Execution = item.Execution
	}
	if item.EncryptedContent != nil {
		previous.EncryptedContent = item.EncryptedContent
	}
	if item.Signature != "" {
		previous.Signature = item.Signature
	}
	if item.Content != nil {
		if previous.Type == "custom_tool_call" {
			if previousText, ok := previous.Content.(string); ok {
				if itemText, itemOK := item.Content.(string); itemOK && itemText != "" && itemText != previousText {
					previous.Content = itemText
					return
				}
			}
		}
		previous.Content = item.Content
	}
	if item.Arguments != "" {
		// output_item.done 携带完整 arguments；delta 的拼接在
		// captureToolArgumentDelta 中单独处理。
		previous.Arguments = item.Arguments
	}
}

func (c *responsesStreamSessionCollector) captureCompletedOutput(data map[string]interface{}) {
	response, ok := data["response"].(map[string]interface{})
	if !ok {
		c.setError(fmt.Errorf("response.completed 缺少 response 对象"))
		return
	}
	output, ok := response["output"].([]interface{})
	if !ok {
		if _, exists := response["output"]; exists {
			c.setError(fmt.Errorf("response.completed 的 output 不是数组"))
		}
		return
	}
	if len(output) == 0 {
		return
	}
	items := make([]types.ResponsesItem, 0, len(output))
	for index, rawItem := range output {
		itemMap, ok := rawItem.(map[string]interface{})
		if !ok {
			c.setError(fmt.Errorf("response.completed output 第 %d 项不是对象", index))
			return
		}
		item := parseResponsesItemMap(itemMap)
		if item.Type == "" {
			c.setError(fmt.Errorf("response.completed output 第 %d 项缺少 type", index))
			return
		}
		items = append(items, item)
	}
	if len(items) > 0 {
		c.completedOutput = items
	}
}

func (c *responsesStreamSessionCollector) setError(err error) {
	if c == nil || err == nil || c.err != nil {
		return
	}
	c.err = err
}

// Err 返回收集器在完整 output 解析阶段遇到的错误。流已经开始向客户端
// 输出后不能再安全切换渠道，因此调用方应跳过本轮 session 持久化并留下日志，
// 避免把半截历史当成可重放上下文。
func (c *responsesStreamSessionCollector) Err() error {
	if c == nil {
		return nil
	}
	return c.err
}

// Completed 表示已经收到并解析了 response.completed。没有这个事件时，流可能
// 只是因为上游断开或客户端取消而结束；除非已经完整交付工具调用，否则不能把
// 半截正文/参数当成下一轮可重放历史。
func (c *responsesStreamSessionCollector) Completed() bool {
	return c != nil && c.completed
}

// CompletedToolCall 表示至少有一个 function_call/custom_tool_call 已收到
// response.output_item.done。客户端在工具调用交付后主动结束 SSE 是合法流程，
// 这类场景无需等待 response.completed 才能保存工具调用回合。
func (c *responsesStreamSessionCollector) CompletedToolCall() bool {
	return c != nil && c.completedTool
}

// Items 返回一轮应写入 session 的 output。完整 output 优先于 delta 重建，
// 避免把同一正文写两遍；若上游不提供完整 output，则使用已归并的 stream item。
func (c *responsesStreamSessionCollector) Items() []types.ResponsesItem {
	if c == nil {
		return nil
	}
	if len(c.completedOutput) > 0 {
		return append([]types.ResponsesItem(nil), c.completedOutput...)
	}

	items := make([]types.ResponsesItem, 0, len(c.streamItemOrder)+2)
	for _, key := range c.streamItemOrder {
		if item := c.streamItems[key]; item != nil {
			items = append(items, *item)
		}
	}
	if c.text.Len() > 0 && !hasResponsesMessageText(items) {
		items = append(items, responsesAssistantTextItem(c.text.String()))
	}
	if c.reasoning.Len() > 0 && !hasResponsesReasoning(items) {
		items = append(items, types.ResponsesItem{
			Type:   "reasoning",
			Status: "completed",
			Summary: []interface{}{map[string]interface{}{
				"type": "summary_text",
				"text": c.reasoning.String(),
			}},
		})
	}
	return items
}

func hasResponsesMessageText(items []types.ResponsesItem) bool {
	for _, item := range items {
		if item.Type != "message" && item.Type != "text" {
			continue
		}
		for _, block := range utils.NormalizeContentBlocks(item.Content) {
			if text, ok := utils.ExtractTextFromBlock(block); ok && text != "" {
				return true
			}
		}
		if text, ok := item.Content.(string); ok && text != "" {
			return true
		}
	}
	return false
}

func hasResponsesReasoning(items []types.ResponsesItem) bool {
	for _, item := range items {
		if item.Type == "reasoning" {
			return true
		}
	}
	return false
}

func responsesAssistantTextItem(text string) types.ResponsesItem {
	return types.ResponsesItem{
		Type:   "message",
		Status: "completed",
		Role:   "assistant",
		Content: []interface{}{map[string]interface{}{
			"type": "output_text",
			"text": text,
		}},
	}
}

func responsesStreamItemKey(data map[string]interface{}, itemMap map[string]interface{}, fallback int) string {
	for _, value := range []interface{}{
		itemMapValue(itemMap, "id"),
		data["item_id"],
		itemMapValue(itemMap, "call_id"),
		data["call_id"],
		data["output_index"],
	} {
		if key := strings.TrimSpace(fmt.Sprint(value)); key != "" && key != "<nil>" {
			return "responses.item." + key
		}
	}
	return fmt.Sprintf("responses.item.fallback.%d", fallback)
}

func itemMapValue(itemMap map[string]interface{}, key string) interface{} {
	if itemMap == nil {
		return nil
	}
	return itemMap[key]
}

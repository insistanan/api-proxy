package respitems

import (
	"fmt"
	"sort"

	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/types"
)

// ResponsesHistoryOutput 对普通响应和 SSE 终态使用相同的可恢复历史规则。
// 截断的文本仍可续聊；未完成工具及其后的条目不能成为下一轮执行历史。
func ResponsesHistoryOutput(output []types.ResponsesItem, incomplete bool) ([]types.ResponsesItem, error) {
	result := make([]types.ResponsesItem, 0, len(output))
	for _, item := range output {
		if item.Status != "" && item.Status != "completed" {
			if !incomplete {
				return nil, fmt.Errorf("已完成的 Responses 输出包含 %s 状态条目", item.Status)
			}
			if item.Type != "message" || item.Status != "incomplete" {
				break
			}
		}
		if err := validateCollectedItem(item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func validateCollectedItem(item types.ResponsesItem) error {
	switch item.Type {
	case "function_call":
		return contract.RequestToolCall(item.CallID, item.Name, item.Arguments)
	case "custom_tool_call":
		if item.CallID == "" || item.Name == "" {
			return fmt.Errorf("完整 custom_tool_call 缺少调用身份")
		}
		if _, ok := item.Content.(string); !ok {
			return fmt.Errorf("完整 custom_tool_call 的 input 必须是字符串")
		}
	}
	return nil
}

// Items 未终止时仅返回连续完成的前缀，不能因某个工具完成而带上其他半截条目。
func (c *ResponsesOutputCollector) Items() ([]types.ResponsesItem, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.output != nil {
		return append([]types.ResponsesItem(nil), c.output...), nil
	}
	entries := append([]*responseOutputEntry(nil), c.entries...)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].hasIndex && entries[j].hasIndex && entries[i].index < entries[j].index
	})
	result := make([]types.ResponsesItem, 0, len(entries))
	for index, entry := range entries {
		if entry.hasIndex && entry.index != index {
			break
		}
		if !entry.done {
			break
		}
		item, err := entry.item()
		if err != nil {
			return nil, err
		}
		if err = validateCollectedItem(item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// Snapshot 只供用量估计，未完成条目不得经此入口进入持久化。
func (c *ResponsesOutputCollector) Snapshot() ([]types.ResponsesItem, error) {
	if c.err != nil {
		return nil, c.err
	}
	result := make([]types.ResponsesItem, 0, len(c.entries))
	for _, entry := range c.entries {
		item, err := entry.item()
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// HasCompletedTool 只报告经过完整参数校验的 done 条目，调用者负责先确认交付成功。
func (c *ResponsesOutputCollector) HasCompletedTool() bool {
	if c.err != nil {
		return false
	}
	for _, entry := range c.entries {
		if entry.done && (entry.raw["type"] == "function_call" || entry.raw["type"] == "custom_tool_call") {
			return true
		}
	}
	return false
}

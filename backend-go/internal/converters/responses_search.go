package converters

import (
	"fmt"
	"log"
	"maps"
	"strings"

	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
	"github.com/tidwall/sjson"
)

// downgradeResponsesHostedSearch 只修改本次转换的副本；原始请求和会话仍可用于原生渠道。
// 非原生协议不提供内置联网能力，但不能因此阻断普通对话和客户端工具。
func downgradeResponsesHostedSearch(serviceType string, body []byte, sess *types.Session, req *types.ResponsesRequest) ([]byte, *types.Session, *types.ResponsesRequest, error) {
	cloned := *req
	tools, toolsChanged, err := filterResponsesHostedSearchTools(req.Tools)
	if err != nil {
		return nil, nil, nil, &types.InvalidRequestError{Err: err}
	}
	var items []types.ResponsesItem
	if req.Input != nil {
		items, err = parseResponsesInput(req.Input)
		if err != nil {
			return nil, nil, nil, &types.InvalidRequestError{Err: err}
		}
	}
	if sess != nil {
		items = utils.MergeResponsesItemsDedup(sess.Messages, items)
	}
	input, inputChanged, err := filterResponsesHostedSearchItems(items)
	if err != nil {
		return nil, nil, nil, &types.InvalidRequestError{Err: err}
	}
	cloned.Tools = tools
	if inputChanged {
		// 已在 item 层合并历史；只供本次转换使用，后续转换器不能再次追加 session。
		cloned.Input = input
		sess = nil
	}

	choiceChanged := false
	if choice, ok := req.ToolChoice.(map[string]interface{}); ok {
		toolType, _ := choice["type"].(string)
		if toolType = strings.TrimSpace(toolType); toolType == "web_search" || toolType == "web_search_preview" {
			cloned.ToolChoice = nil
			choiceChanged = true
		}
	}
	if !toolsChanged && !inputChanged && !choiceChanged {
		return body, sess, req, nil
	}

	// 搜索是唯一工具时，required 等约束与并行开关也必须撤掉。
	effectiveTools, err := collectResponsesToolDefinitions(cloned.Tools, cloned.Input, sessionResponseItems(sess))
	if err != nil {
		return nil, nil, nil, &types.InvalidRequestError{Err: err}
	}
	if len(effectiveTools) == 0 {
		// 非法 tool_choice 留给协议校验报错，不能被搜索降级掩盖。
		if choice, ok := cloned.ToolChoice.(string); ok && (choice == "auto" || choice == "none" || choice == "required") {
			cloned.ToolChoice = nil
			choiceChanged = true
		}
		if choice, ok := cloned.ToolChoice.(map[string]interface{}); ok {
			if toolType, _ := choice["type"].(string); toolType == "auto" || toolType == "none" || toolType == "required" || toolType == "any" {
				cloned.ToolChoice = nil
				choiceChanged = true
			}
		}
		cloned.ParallelToolCalls = nil
	}
	for _, field := range []struct {
		name    string
		value   interface{}
		changed bool
	}{
		{"tools", cloned.Tools, toolsChanged},
		{"input", cloned.Input, inputChanged},
		{"tool_choice", cloned.ToolChoice, choiceChanged},
		{"parallel_tool_calls", nil, len(effectiveTools) == 0},
	} {
		if !field.changed {
			continue
		}
		if field.value == nil {
			body, err = sjson.DeleteBytes(body, field.name)
		} else {
			body, err = sjson.SetBytes(body, field.name, field.value)
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("降级 Responses 内置搜索时更新 %s 失败: %w", field.name, err)
		}
	}
	log.Printf("[Responses-SearchDowngrade] %s 上游不执行 Responses 内置联网搜索，已移除搜索声明及调用元数据，继续普通对话和客户端工具", serviceType)
	return body, sess, &cloned, nil
}

// filterResponsesHostedSearchTools 按 type 区分服务端搜索与同名客户端 function。
func filterResponsesHostedSearchTools(raw interface{}) ([]map[string]interface{}, bool, error) {
	tools, err := normalizeMapSlice(raw)
	if err != nil {
		return nil, false, fmt.Errorf("Responses tools 非法: %w", err)
	}
	filtered := make([]map[string]interface{}, 0, len(tools))
	changed := false
	for _, tool := range tools {
		switch normalizeResponsesToolDefinition(tool).ToolType {
		case "web_search", "web_search_preview":
			changed = true
			continue
		case "namespace":
			field := "tools"
			if tool[field] == nil {
				field = "children"
			}
			children, childChanged, err := filterResponsesHostedSearchTools(tool[field])
			if err != nil {
				return nil, false, err
			}
			if childChanged {
				changed = true
				if len(children) == 0 {
					continue
				}
				tool = maps.Clone(tool)
				tool[field] = children
			}
		}
		filtered = append(filtered, tool)
	}
	if !changed {
		return tools, false, nil
	}
	return filtered, true, nil
}

func filterResponsesHostedSearchItems(items []types.ResponsesItem) ([]types.ResponsesItem, bool, error) {
	filtered := make([]types.ResponsesItem, 0, len(items))
	removedSearchCalls := make(map[string]bool)
	changed := false
	for _, item := range items {
		switch item.Type {
		case "web_search_call":
			changed = true
			continue
		case "tool_search_output":
			tools, toolsChanged, err := filterResponsesHostedSearchTools(item.Tools)
			if err != nil {
				return nil, false, err
			}
			if toolsChanged {
				changed = true
				if len(tools) == 0 {
					removedSearchCalls[item.CallID] = true
					continue
				}
				item.Tools = tools
			}
		}
		filtered = append(filtered, item)
	}
	if !changed {
		return items, false, nil
	}
	// 动态加载只返回内置搜索时，成对移除调用与结果，避免留下孤立工具回合。
	result := make([]types.ResponsesItem, 0, len(filtered))
	for _, item := range filtered {
		if item.Type == "tool_search_call" && item.CallID != "" && removedSearchCalls[item.CallID] {
			continue
		}
		// 字符串 input 合并历史后会成为 text item，Chat 数组入口要求明确的消息角色。
		if item.Type == "text" {
			item.Type = "message"
			if item.Role == "" {
				item.Role = "user"
			}
		}
		result = append(result, item)
	}
	return result, true, nil
}

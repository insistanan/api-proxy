package converters

import (
	"fmt"

	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func restoreResponseToolItem(item types.ResponsesItem, tools map[string]responseToolSpec) (types.ResponsesItem, error) {
	if item.Type != "function_call" {
		return item, nil
	}
	arguments, err := contract.Arguments(item.Arguments, item.CallID)
	if err != nil {
		return item, err
	}
	item = restoreResponseToolIdentity(item, tools)
	if item.Type == "custom_tool_call" {
		input, ok := arguments["input"].(string)
		if !ok {
			return item, fmt.Errorf("custom 工具 %q 的 input 必须是字符串", item.Name)
		}
		item.Content, item.Arguments = input, ""
	}
	return item, nil
}

// 身份还原不依赖参数完整度，普通响应和流式条目开始／完成共用同一份工具声明。
func restoreResponseToolIdentity(item types.ResponsesItem, tools map[string]responseToolSpec) types.ResponsesItem {
	spec, exists := tools[item.Name]
	if !exists {
		return item
	}
	item.Name, item.Namespace = spec.OriginalNameOr(item.Name), spec.Namespace
	switch spec.Kind {
	case responseToolKindCustom:
		item.Type = "custom_tool_call"
	case responseToolKindToolSearch:
		item.Type, item.Name, item.Execution = "tool_search_call", "", spec.ExecutionOrDefault()
	}
	return item
}

func applyResponseCompletion(response *types.ResponsesResponse, completion contract.Completion) {
	response.Status = completion.Status
	if completion.Reason != "" {
		response.IncompleteDetails = map[string]interface{}{"reason": completion.Reason}
		for i := range response.Output {
			response.Output[i].Status = completion.Status
		}
	}
}

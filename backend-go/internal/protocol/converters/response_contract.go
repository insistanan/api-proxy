package converters

import (
	"encoding/json"
	"fmt"

	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/tidwall/gjson"
)

func validateUpstreamResponse(protocol string, data []byte) error {
	root, err := contract.Object(data, "上游 "+protocol+" 响应")
	if err != nil {
		return err
	}
	if failure := root["error"]; failure != nil {
		return fmt.Errorf("上游 %s 返回错误: %v", protocol, failure)
	}
	switch protocol {
	case "openai":
		choices := gjson.GetBytes(data, "choices")
		if !choices.IsArray() || len(choices.Array()) != 1 {
			return fmt.Errorf("转换 Chat 响应需要恰好一个 choice")
		}
		if !choices.Array()[0].Get("message").IsObject() {
			return fmt.Errorf("Chat choice 缺少 message 对象")
		}
		for _, call := range choices.Array()[0].Get("message.tool_calls").Array() {
			if _, err := contract.Arguments(call.Get("function.arguments").String(), call.Get("id").String()); err != nil {
				return err
			}
		}
	case "gemini":
		candidates := gjson.GetBytes(data, "candidates")
		if !candidates.IsArray() || len(candidates.Array()) != 1 {
			return fmt.Errorf("转换 Gemini 响应需要恰好一个 candidate")
		}
	case "claude":
		if !gjson.GetBytes(data, "content").IsArray() {
			return fmt.Errorf("Claude 响应缺少 content 数组")
		}
	}
	return nil
}

func restoreResponseToolItem(item types.ResponsesItem, tools map[string]responseToolSpec) (types.ResponsesItem, error) {
	if item.Type != "function_call" {
		return item, nil
	}
	if _, err := contract.Arguments(item.Arguments, item.CallID); err != nil {
		return item, err
	}
	spec, exists := tools[item.Name]
	if !exists {
		return item, nil
	}
	item.Name, item.Namespace = spec.OriginalNameOr(item.Name), spec.Namespace
	switch spec.Kind {
	case responseToolKindCustom:
		var arguments map[string]interface{}
		if err := json.Unmarshal([]byte(item.Arguments), &arguments); err != nil {
			return item, err
		}
		input, ok := arguments["input"].(string)
		if !ok {
			return item, fmt.Errorf("custom 工具 %q 的 input 必须是字符串", item.Name)
		}
		item.Type, item.Content, item.Arguments = "custom_tool_call", input, ""
	case responseToolKindToolSearch:
		item.Type, item.Execution = "tool_search_call", spec.ExecutionOrDefault()
	}
	return item, nil
}

func applyResponseCompletion(response *types.ResponsesResponse, completion contract.Completion) {
	response.Status = completion.Status
	if completion.Reason != "" {
		if response.Extra == nil {
			response.Extra = make(map[string]interface{})
		}
		response.Extra["incomplete_details"] = map[string]interface{}{"reason": completion.Reason}
		for i := range response.Output {
			response.Output[i].Status = completion.Status
		}
	}
}

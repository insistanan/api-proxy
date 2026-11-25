package converters

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/types"
)

func validateResponsesConversion(req *types.ResponsesRequest, sess *types.Session, body []byte, target string) error {
	if err := validateSamplingAndBudget(body, target); err != nil {
		return err
	}
	if format := gjson.GetBytes(body, "text.format"); format.Exists() {
		return contract.Unsupported("Responses → %s 尚不支持 text.format", target)
	}
	if target == "gemini" && req.ParallelToolCalls != nil && !*req.ParallelToolCalls {
		return contract.Unsupported("Gemini 无法保证 parallel_tool_calls=false")
	}
	var items []types.ResponsesItem
	var err error
	if req.Input != nil {
		items, err = parseResponsesInput(req.Input)
		if err != nil {
			return contract.Invalid("Responses input 无效: %v", err)
		}
	}
	if sess != nil {
		items = append(append([]types.ResponsesItem(nil), sess.Messages...), items...)
	}
	for _, item := range items {
		switch item.Type {
		case "message", "text":
			switch item.Role {
			case "", "user", "assistant", "system", "developer":
			default:
				return contract.Invalid("Responses 消息 role 无效: %q", item.Role)
			}
			content, err := json.Marshal(item.Content)
			if err != nil {
				return err
			}
			if err := validateContentBlocks(gjson.ParseBytes(content), target, false); err != nil {
				return err
			}
		case "function_call_output", "custom_tool_call_output":
			raw, err := json.Marshal(item.Content)
			if err != nil {
				return err
			}
			for _, block := range arrayContentBlocks(gjson.ParseBytes(raw)) {
				if typ := block.Get("type").String(); typ != "" && typ != "text" && typ != "input_text" && typ != "output_text" {
					return contract.Unsupported("Responses → %s 尚不支持多模态工具输出 %q", target, typ)
				}
			}
		case "function_call", "tool_search_call":
			name := item.Name
			if item.Type == "tool_search_call" {
				name = openAIChatToolSearchName
			}
			if err := contract.RequestToolCall(item.CallID, name, item.Arguments); err != nil {
				return err
			}
		case "custom_tool_call":
			if _, ok := item.Content.(string); !ok || item.CallID == "" || item.Name == "" {
				return contract.Invalid("custom_tool_call 必须包含 call_id、name 和字符串 input")
			}
		case "tool_search_output", "reasoning", "compaction", "tool_call", "tool_result":
		case "web_search_call":
			return contract.Unsupported("%s 无法重放内置 web_search 历史", target)
		default:
			return contract.Unsupported("Responses → %s 不支持 item type %q", target, item.Type)
		}
	}
	definitions, err := collectResponsesToolDefinitions(req.Tools, req.Input, sessionResponseItems(sess))
	if err != nil {
		return contract.Invalid("Responses 工具定义无效: %v", err)
	}
	if err := validateToolIdentities(definitions, target); err != nil {
		return err
	}
	choice, err := ResponsesToolChoiceToClaude(req.ToolChoice)
	if err != nil {
		return err
	}
	names := make(map[string]bool)
	for name := range buildResponseToolContext(definitions, nil) {
		names[name] = true
	}
	return contract.ToolChoice(choice, names)
}

func extractResponsesSystem(items []types.ResponsesItem, instructions string) ([]types.ResponsesItem, string, error) {
	filtered := make([]types.ResponsesItem, 0, len(items))
	texts := []string{}
	if instructions != "" {
		texts = append(texts, instructions)
	}
	for _, item := range items {
		if (item.Type == "message" || item.Type == "text") && (item.Role == "system" || item.Role == "developer") {
			raw, err := json.Marshal(item.Content)
			if err != nil {
				return nil, "", err
			}
			for _, block := range arrayContentBlocks(gjson.ParseBytes(raw)) {
				if typ := block.Get("type").String(); typ != "text" && typ != "input_text" && typ != "output_text" {
					return nil, "", contract.Unsupported("系统指令不能包含非文本内容块 %q", typ)
				}
			}
			text := extractTextFromContent(item.Content)
			if text == "" {
				return nil, "", contract.Unsupported("目标系统指令只能接收文本内容")
			}
			texts = append(texts, text)
		} else {
			filtered = append(filtered, item)
		}
	}
	return filtered, strings.Join(texts, "\n\n"), nil
}

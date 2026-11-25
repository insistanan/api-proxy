package converters

import (
	"fmt"
	"maps"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// collectResponsesToolDefinitions 只读取工具声明和顶层 tool_search_output，
// 按工具身份合并动态发现，避免把业务结果里的同名对象误认为工具定义。
func collectResponsesToolDefinitions(toolsRaw interface{}, inputRaws ...interface{}) ([]map[string]interface{}, error) {
	definitions, err := normalizeMapSlice(toolsRaw)
	if err != nil {
		return nil, err
	}
	for _, raw := range inputRaws {
		var items []types.ResponsesItem
		switch input := raw.(type) {
		case nil, string:
			continue
		case []types.ResponsesItem:
			items = input
		default:
			items, err = parseResponsesInput(input)
			if err != nil {
				return nil, err
			}
		}
		for _, item := range items {
			if item.Type != "tool_search_output" {
				continue
			}
			found, err := normalizeMapSlice(item.Tools)
			if err != nil {
				return nil, fmt.Errorf("tool_search_output %s 的 tools 无效: %w", item.CallID, err)
			}
			definitions = append(definitions, found...)
		}
	}
	return mergeResponseToolDefinitions(definitions)
}

func mergeResponseToolDefinitions(definitions []map[string]interface{}) ([]map[string]interface{}, error) {
	result := make([]map[string]interface{}, 0, len(definitions))
	indices := make(map[string]int)
	for _, definition := range definitions {
		key := responseToolDefinitionKey(definition)
		position, exists := indices[key]
		if !exists {
			indices[key] = len(result)
			result = append(result, maps.Clone(definition))
			continue
		}
		if normalizeResponsesToolDefinition(definition).ToolType == "namespace" {
			left, err := namespaceChildren(result[position])
			if err != nil {
				return nil, err
			}
			right, err := namespaceChildren(definition)
			if err != nil {
				return nil, err
			}
			merged, err := mergeResponseToolDefinitions(append(left, right...))
			if err != nil {
				return nil, err
			}
			result[position]["tools"] = merged
			delete(result[position], "children")
		} else if codec.CanonicalJSON(result[position]) != codec.CanonicalJSON(definition) {
			return nil, fmt.Errorf("工具 %s 的重复定义不一致", key)
		}
	}
	return result, nil
}

func namespaceChildren(tool map[string]interface{}) ([]map[string]interface{}, error) {
	raw := tool["tools"]
	if raw == nil {
		raw = tool["children"]
	}
	return normalizeMapSlice(raw)
}

func validateToolIdentities(definitions []map[string]interface{}, target string) error {
	seen := make(map[string]string)
	register := func(name, identity string) error {
		if name == "" {
			return contract.Invalid("工具缺少 name")
		}
		if previous, exists := seen[name]; exists && previous != identity {
			return contract.Unsupported("转换到 %s 后工具名 %q 冲突: %s / %s", target, name, previous, identity)
		}
		seen[name] = identity
		return nil
	}
	for _, tool := range definitions {
		spec := normalizeResponsesToolDefinition(tool)
		switch spec.ToolType {
		case "web_search", "web_search_preview":
			return contract.Unsupported("%s 无法执行 Responses 内置联网搜索，请选择原生 Responses 渠道", target)
		case "namespace":
			children, err := namespaceChildren(tool)
			if err != nil {
				return contract.Invalid("namespace %s 无效: %v", spec.Name, err)
			}
			for _, child := range children {
				childSpec := normalizeResponsesToolDefinition(child)
				if childSpec.ToolType != "" && childSpec.ToolType != "function" && childSpec.ToolType != "custom" {
					return contract.Unsupported("%s 不支持 namespace 子工具 %q", target, childSpec.ToolType)
				}
				if err := register(flattenNamespaceToolName(spec.Name, childSpec.Name), spec.Name+"."+childSpec.ToolType+":"+childSpec.Name); err != nil {
					return err
				}
			}
		case "tool_search":
			if execution, _ := tool["execution"].(string); execution != "" && execution != "client" {
				return contract.Unsupported("%s 不能执行服务器 tool_search", target)
			}
			if err := register(openAIChatToolSearchName, "tool_search"); err != nil {
				return err
			}
		default:
			if err := register(spec.Name, spec.ToolType+":"+spec.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func responseToolDefinitionKey(tool map[string]interface{}) string {
	spec := normalizeResponsesToolDefinition(tool)
	typ := spec.ToolType
	if typ == "" {
		typ = "function"
	}
	return typ + ":" + strings.TrimSpace(spec.Name)
}

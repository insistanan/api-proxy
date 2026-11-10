package chat

import (
	"encoding/json"
	"testing"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/types"
)

func TestChatHostedWebSearchDoesNotBecomeClientFunction(t *testing.T) {
	for _, toolType := range []string{"web_search", "web_search_preview", "function"} {
		t.Run(toolType, func(t *testing.T) {
			body, err := json.Marshal(map[string]interface{}{
				"model": "test-model", "messages": []map[string]interface{}{{"role": "user", "content": "搜索"}},
				"tools": []map[string]interface{}{{"type": toolType, "name": "web_search"}},
				"tool_choice": map[string]interface{}{"type": toolType, "name": "web_search"},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = applyChatModelMapping(body, &config.UpstreamConfig{})
			if toolType == "function" {
				if err != nil {
					t.Fatalf("客户端声明的普通同名函数应保留: %v", err)
				}
			} else if !types.IsUnsupportedProtocol(err) {
				t.Fatalf("内置搜索应明确为候选协议不支持: %v", err)
			}
		})
	}
}

func TestApplyChatModelMapping_DowngradesCustomTool(t *testing.T) {
	input := []byte(`{
		"model":"gpt-4o",
		"stream":true,
		"messages":[{"role":"user","content":"run code"}],
		"tools":[{"type":"custom","name":"code_exec","description":"Execute raw code"}],
		"tool_choice":{"type":"custom","name":"code_exec"}
	}`)

	out, err := applyChatModelMapping(input, &config.UpstreamConfig{})
	if err != nil {
		t.Fatalf("applyChatModelMapping 不应报错: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}

	tools, ok := payload["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools 结果异常: %#v", payload["tools"])
	}
	tool, ok := tools[0].(map[string]interface{})
	if !ok {
		t.Fatalf("tool 类型异常: %#v", tools[0])
	}
	if tool["type"] != "function" {
		t.Fatalf("custom tool 应降级为 function，实际: %#v", tool["type"])
	}
	function, ok := tool["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("function 结构异常: %#v", tool["function"])
	}
	if function["name"] != "code_exec" {
		t.Fatalf("function.name 不匹配: %#v", function["name"])
	}
	parameters, ok := function["parameters"].(map[string]interface{})
	if !ok {
		t.Fatalf("parameters 结构异常: %#v", function["parameters"])
	}
	properties, ok := parameters["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties 结构异常: %#v", parameters["properties"])
	}
	inputProp, ok := properties["input"].(map[string]interface{})
	if !ok || inputProp["type"] != "string" {
		t.Fatalf("custom tool 应补 input:string schema，实际: %#v", properties["input"])
	}

	toolChoice, ok := payload["tool_choice"].(map[string]interface{})
	if !ok {
		t.Fatalf("tool_choice 类型异常: %#v", payload["tool_choice"])
	}
	if toolChoice["type"] != "function" {
		t.Fatalf("tool_choice.type 应为 function，实际: %#v", toolChoice["type"])
	}
	tcFunction, ok := toolChoice["function"].(map[string]interface{})
	if !ok || tcFunction["name"] != "code_exec" {
		t.Fatalf("tool_choice.function.name 不匹配: %#v", toolChoice["function"])
	}

	streamOptions, ok := payload["stream_options"].(map[string]interface{})
	if !ok || streamOptions["include_usage"] != true {
		t.Fatalf("stream_options.include_usage 应自动补齐，实际: %#v", payload["stream_options"])
	}
}

func TestApplyChatModelMapping_PrunesToolControlsWithoutTools(t *testing.T) {
	input := []byte(`{
		"model":"gpt-4o",
		"messages":[{"role":"user","content":"hi"}],
		"tool_choice":{"type":"function","name":"lookup"},
		"parallel_tool_calls":true
	}`)

	out, err := applyChatModelMapping(input, &config.UpstreamConfig{})
	if err != nil {
		t.Fatalf("applyChatModelMapping 不应报错: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}

	if _, exists := payload["tool_choice"]; exists {
		t.Fatalf("没有 tools 时应移除 tool_choice: %#v", payload["tool_choice"])
	}
	if _, exists := payload["parallel_tool_calls"]; exists {
		t.Fatalf("没有 tools 时应移除 parallel_tool_calls: %#v", payload["parallel_tool_calls"])
	}
}

func TestApplyChatModelMapping_NormalizesReasoningEffort(t *testing.T) {
	input := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"ultra"}`)
	out, err := applyChatModelMapping(input, &config.UpstreamConfig{})
	if err != nil {
		t.Fatalf("applyChatModelMapping 不应报错: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	if got := payload["reasoning_effort"]; got != "max" {
		t.Fatalf("reasoning_effort = %#v, 期望 max", got)
	}
}

func TestApplyChatModelMapping_RejectsInvalidReasoningEffort(t *testing.T) {
	input := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"future"}`)
	if _, err := applyChatModelMapping(input, &config.UpstreamConfig{}); err == nil {
		t.Fatal("非法 reasoning_effort 应返回错误")
	}
}

func TestApplyChatModelMapping_NormalizesModalitiesObjectToArray(t *testing.T) {
	input := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"modalities":{"text":true,"audio":true}}`)
	out, err := applyChatModelMapping(input, &config.UpstreamConfig{})
	if err != nil {
		t.Fatalf("applyChatModelMapping 不应报错: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}

	modalities, ok := payload["modalities"].([]interface{})
	if !ok {
		t.Fatalf("modalities 应归一化为数组，实际: %#v", payload["modalities"])
	}
	if len(modalities) != 2 {
		t.Fatalf("modalities 应有 2 个元素，实际: %d", len(modalities))
	}
	if modalities[0] != "text" || modalities[1] != "audio" {
		t.Fatalf("modalities 应为 [text, audio]，实际: %v", modalities)
	}
}

func TestApplyChatModelMapping_KeepsModalitiesArrayAsIs(t *testing.T) {
	input := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"modalities":["text"]}`)
	out, err := applyChatModelMapping(input, &config.UpstreamConfig{})
	if err != nil {
		t.Fatalf("applyChatModelMapping 不应报错: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}

	modalities, ok := payload["modalities"].([]interface{})
	if !ok || len(modalities) != 1 || modalities[0] != "text" {
		t.Fatalf("modalities 数组应原样保留，实际: %#v", payload["modalities"])
	}
}

func TestApplyChatModelMapping_RemovesEmptyModalities(t *testing.T) {
	input := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"modalities":{"text":false,"audio":false}}`)
	out, err := applyChatModelMapping(input, &config.UpstreamConfig{})
	if err != nil {
		t.Fatalf("applyChatModelMapping 不应报错: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}

	if _, exists := payload["modalities"]; exists {
		t.Fatalf("modalities 全 false 时应删除，实际: %#v", payload["modalities"])
	}
}

package chat

import (
	"encoding/json"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func TestChatHostedWebSearchDoesNotBecomeClientFunction(t *testing.T) {
	for _, toolType := range []string{"web_search", "web_search_preview", "function"} {
		t.Run(toolType, func(t *testing.T) {
			body, err := json.Marshal(map[string]interface{}{
				"model": "test-model", "messages": []map[string]interface{}{{"role": "user", "content": "搜索"}},
				"tools":       []map[string]interface{}{{"type": toolType, "name": "web_search"}},
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

func TestApplyChatModelMapping_RejectsAsymmetricCustomTool(t *testing.T) {
	_, err := applyChatModelMapping([]byte(`{"model":"m","messages":[{"role":"developer","content":"hi"}],"tools":[{"type":"custom","name":"run"}]}`), &config.UpstreamConfig{})
	if _, ok := err.(*types.UnsupportedProtocolError); !ok {
		t.Fatalf("应明确拒绝单向工具适配: %v", err)
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

func TestApplyChatModelMapping_RejectsInvalidModalities(t *testing.T) {
	_, err := applyChatModelMapping([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"modalities":{"text":true}}`), &config.UpstreamConfig{})
	if err == nil {
		t.Fatal("不能将非法 modalities 静默修改")
	}
}

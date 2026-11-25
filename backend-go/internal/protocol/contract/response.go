package contract

import (
	"fmt"

	"github.com/tidwall/gjson"
)

// ValidateResponse 校验转换前必需的响应结构，原生未知字段不受影响。
func ValidateResponse(protocol string, data []byte) error {
	root, err := Object(data, "上游 "+protocol+" 响应")
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
		if err := responseFinish(protocol, choices.Array()[0].Get("finish_reason")); err != nil {
			return err
		}
		for _, call := range choices.Array()[0].Get("message.tool_calls").Array() {
			if _, err := Arguments(call.Get("function.arguments").String(), call.Get("id").String()); err != nil {
				return err
			}
		}
	case "gemini":
		candidates := gjson.GetBytes(data, "candidates")
		if !candidates.IsArray() || len(candidates.Array()) != 1 {
			return fmt.Errorf("转换 Gemini 响应需要恰好一个 candidate")
		}
		candidate := candidates.Array()[0]
		if !candidate.IsObject() {
			return fmt.Errorf("Gemini candidate 必须是对象")
		}
		if content := candidate.Get("content"); content.Exists() {
			if !content.IsObject() {
				return fmt.Errorf("Gemini candidate.content 必须是对象")
			}
			if parts := content.Get("parts"); parts.Exists() && !parts.IsArray() {
				return fmt.Errorf("Gemini candidate.content.parts 必须是数组")
			}
		}
		if err := responseFinish(protocol, candidate.Get("finishReason")); err != nil {
			return err
		}
	case "claude":
		if !gjson.GetBytes(data, "content").IsArray() {
			return fmt.Errorf("Claude 响应缺少 content 数组")
		}
		if err := responseFinish(protocol, gjson.GetBytes(data, "stop_reason")); err != nil {
			return err
		}
	}
	if protocol == "responses" {
		return nil
	}
	return ResponseContent(protocol, data)
}

// 普通响应必须自带完成证据；流式增量可以暂缺终态，不能套用此校验。
func responseFinish(protocol string, reason gjson.Result) error {
	if reason.Type != gjson.String || reason.String() == "" {
		return fmt.Errorf("上游 %s 普通响应缺少有效结束原因", protocol)
	}
	_, err := Finish(protocol, reason.String())
	return err
}

package contract

import (
	"fmt"

	"github.com/tidwall/gjson"
)

// ResponseContent 校验跨协议转换实际读取的内容块。原生透传不调用，扩展字段仍保留。
// 请求选项不能保证上游输出一定可表达，因此普通响应和每个流式载荷都需要检查。
func ResponseContent(protocol string, body []byte) error {
	root := gjson.ParseBytes(body)
	switch protocol {
	case "gemini":
		for _, candidate := range root.Get("candidates").Array() {
			for _, part := range candidate.Get("content.parts").Array() {
				if !part.IsObject() {
					return fmt.Errorf("Gemini 响应 part 必须是对象")
				}
				for key := range part.Map() {
					switch key {
					case "text", "thought", "thoughtSignature", "thought_signature", "functionCall":
					default:
						return Unsupported("跨协议响应无法表示 Gemini part 字段 %q", key)
					}
				}
				if call := part.Get("functionCall"); call.Exists() {
					if call.Get("name").String() == "" {
						return fmt.Errorf("Gemini 响应工具缺少名称")
					}
					if _, err := Arguments(call.Get("args").Raw, call.Get("name").String()); err != nil {
						return err
					}
					if call.Get("partialArgs").Exists() || call.Get("willContinue").Bool() {
						return Unsupported("跨协议响应尚不支持 Gemini 增量 functionCall")
					}
				}
			}
		}
	case "claude":
		blocks := root.Get("content").Array()
		if root.Get("type").String() == "content_block_start" {
			blocks = []gjson.Result{root.Get("content_block")}
		}
		for _, block := range blocks {
			switch block.Get("type").String() {
			case "text", "thinking", "redacted_thinking", "tool_use":
			default:
				return Unsupported("跨协议响应无法表示 Claude 内容块 %q", block.Get("type").String())
			}
		}
	case "openai":
		for _, choice := range root.Get("choices").Array() {
			message := choice.Get("message")
			if !message.Exists() {
				message = choice.Get("delta")
			}
			if content := message.Get("content"); content.Exists() && content.Type != gjson.Null && content.Type != gjson.String {
				return Unsupported("跨协议响应尚不支持 Chat 非文本 content")
			}
			for _, key := range []string{"audio", "images", "function_call", "refusal"} {
				if value := message.Get(key); value.Exists() && value.Type != gjson.Null {
					if value.Type == gjson.String && value.String() == "" {
						continue
					}
					return Unsupported("跨协议响应尚不支持 Chat %s", key)
				}
			}
		}
	case "responses":
		output := root.Get("output").Array()
		if root.Get("response").Exists() {
			output = root.Get("response.output").Array()
		}
		if item := root.Get("item"); item.IsObject() {
			output = append(output, item)
		}
		for _, item := range output {
			switch item.Get("type").String() {
			case "reasoning", "function_call", "custom_tool_call":
			case "message":
				for _, block := range item.Get("content").Array() {
					switch block.Get("type").String() {
					case "text", "input_text", "output_text", "refusal":
					default:
						return Unsupported("Messages 无法表示 Responses 内容块 %q", block.Get("type").String())
					}
				}
			default:
				return Unsupported("Messages 无法表示 Responses 输出条目 %q", item.Get("type").String())
			}
		}
	}
	return nil
}

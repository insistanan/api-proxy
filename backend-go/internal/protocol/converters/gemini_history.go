package converters

import (
	"encoding/json"
	"fmt"
	"strings"

	"insistanan/api-proxy/internal/protocol/types"
)

type geminiHistoryCalls struct {
	next    int
	names   map[string]string
	pending map[string]string
}

func newGeminiHistoryCalls() *geminiHistoryCalls {
	return &geminiHistoryCalls{names: make(map[string]string), pending: make(map[string]string)}
}

func (s *geminiHistoryCalls) call(call *types.GeminiFunctionCall) (string, error) {
	if call.Name == "" || call.Args == nil {
		return "", &types.InvalidRequestError{Err: fmt.Errorf("Gemini functionCall 必须包含 name 和对象 args")}
	}
	id := call.ID
	if id == "" {
		for {
			id = fmt.Sprintf("gemini_call_%d", s.next)
			s.next++
			if _, exists := s.names[id]; !exists {
				break
			}
		}
	}
	if _, exists := s.names[id]; exists {
		return "", &types.InvalidRequestError{Err: fmt.Errorf("Gemini 工具调用 ID 重复: %s", id)}
	}
	s.names[id], s.pending[id] = call.Name, call.Name
	return id, nil
}

func (s *geminiHistoryCalls) result(result *types.GeminiFunctionResponse) (string, error) {
	id := result.ID
	if id == "" {
		for candidate, name := range s.pending {
			if name != result.Name {
				continue
			}
			if id != "" {
				return "", &types.UnsupportedProtocolError{Err: fmt.Errorf("Gemini 同名并行工具 %q 的结果缺少 ID，无法唯一关联", result.Name)}
			}
			id = candidate
		}
	}
	name, exists := s.pending[id]
	if !exists || name != result.Name {
		return "", &types.InvalidRequestError{Err: fmt.Errorf("Gemini 工具结果没有对应调用: id=%q name=%q", result.ID, result.Name)}
	}
	delete(s.pending, id)
	return id, nil
}

// convertGeminiHistory 在整个请求内关联调用，允许一条 Content 展开多个工具结果。
func convertGeminiHistory(contents []types.GeminiContent, target string) ([]map[string]interface{}, error) {
	state := newGeminiHistoryCalls()
	messages := make([]map[string]interface{}, 0, len(contents))
	for _, content := range contents {
		role := content.Role
		switch role {
		case "", "user":
			role = "user"
		case "model":
			role = "assistant"
		default:
			return nil, &types.InvalidRequestError{Err: fmt.Errorf("Gemini contents role 无效: %q", role)}
		}
		blocks := []map[string]interface{}{}
		calls := []map[string]interface{}{}
		flush := func() {
			if len(blocks) == 0 && len(calls) == 0 {
				return
			}
			message := map[string]interface{}{"role": role, "content": blocks}
			if len(calls) > 0 {
				message["tool_calls"] = calls
			}
			messages = append(messages, message)
			blocks, calls = nil, nil
		}
		for _, part := range content.Parts {
			if part.Thought {
				continue
			}
			if part.Text != "" {
				blocks = append(blocks, map[string]interface{}{"type": "text", "text": part.Text})
			}
			if part.InlineData != nil || part.FileData != nil {
				media, err := geminiHistoryMedia(part, target)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, media)
			}
			if call := part.FunctionCall; call != nil {
				if role != "assistant" {
					return nil, &types.InvalidRequestError{Err: fmt.Errorf("Gemini functionCall 必须位于 model 内容中")}
				}
				id, err := state.call(call)
				if err != nil {
					return nil, err
				}
				if target == "claude" {
					blocks = append(blocks, map[string]interface{}{"type": "tool_use", "id": id, "name": call.Name, "input": call.Args})
				} else {
					args, err := json.Marshal(call.Args)
					if err != nil {
						return nil, fmt.Errorf("编码工具 %s 参数失败: %w", id, err)
					}
					calls = append(calls, map[string]interface{}{"id": id, "type": "function", "function": map[string]interface{}{"name": call.Name, "arguments": string(args)}})
				}
			}
			if result := part.FunctionResponse; result != nil {
				if role != "user" {
					return nil, &types.InvalidRequestError{Err: fmt.Errorf("Gemini functionResponse 必须位于 user 内容中")}
				}
				id, err := state.result(result)
				if err != nil {
					return nil, err
				}
				encoded, err := json.Marshal(result.Response)
				if err != nil {
					return nil, fmt.Errorf("编码工具 %s 结果失败: %w", id, err)
				}
				if target == "claude" {
					blocks = append(blocks, map[string]interface{}{"type": "tool_result", "tool_use_id": id, "content": string(encoded)})
				} else {
					messages = append(messages, map[string]interface{}{"role": "tool", "tool_call_id": id, "content": string(encoded)})
				}
			}
		}
		flush()
	}
	return messages, nil
}

// convertGeminiHistoryToResponses 将 Gemini contents 转为 Responses input 条目列表，
// 保持 user/assistant 文本、图片以及 function_call / function_call_output 的顺序与调用 ID 关联。
func convertGeminiHistoryToResponses(contents []types.GeminiContent) ([]interface{}, error) {
	state := newGeminiHistoryCalls()
	items := make([]interface{}, 0, len(contents))
	for _, content := range contents {
		role := content.Role
		switch role {
		case "", "user":
			role = "user"
		case "model":
			role = "assistant"
		default:
			return nil, &types.InvalidRequestError{Err: fmt.Errorf("Gemini contents role 无效: %q", role)}
		}
		blocks := []map[string]interface{}{}
		flush := func() {
			if len(blocks) == 0 {
				return
			}
			items = append(items, map[string]interface{}{"type": "message", "role": role, "content": blocks})
			blocks = nil
		}
		textType := "input_text"
		if role == "assistant" {
			textType = "output_text"
		}
		for _, part := range content.Parts {
			if part.Thought {
				continue
			}
			if part.Text != "" {
				blocks = append(blocks, map[string]interface{}{"type": textType, "text": part.Text})
			}
			if part.InlineData != nil || part.FileData != nil {
				if role != "user" {
					return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Gemini → responses 不支持模型消息中的图片")}
				}
				media, err := geminiHistoryMedia(part, "responses")
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, media)
			}
			if call := part.FunctionCall; call != nil {
				if role != "assistant" {
					return nil, &types.InvalidRequestError{Err: fmt.Errorf("Gemini functionCall 必须位于 model 内容中")}
				}
				flush()
				id, err := state.call(call)
				if err != nil {
					return nil, err
				}
				args, err := json.Marshal(call.Args)
				if err != nil {
					return nil, fmt.Errorf("编码工具 %s 参数失败: %w", id, err)
				}
				items = append(items, map[string]interface{}{
					"type":      "function_call",
					"call_id":   id,
					"name":      call.Name,
					"arguments": string(args),
				})
			}
			if result := part.FunctionResponse; result != nil {
				if role != "user" {
					return nil, &types.InvalidRequestError{Err: fmt.Errorf("Gemini functionResponse 必须位于 user 内容中")}
				}
				flush()
				id, err := state.result(result)
				if err != nil {
					return nil, err
				}
				encoded, err := json.Marshal(result.Response)
				if err != nil {
					return nil, fmt.Errorf("编码工具 %s 结果失败: %w", id, err)
				}
				items = append(items, map[string]interface{}{
					"type":    "function_call_output",
					"call_id": id,
					"output":  string(encoded),
				})
			}
		}
		flush()
	}
	return items, nil
}

func geminiHistoryMedia(part types.GeminiPart, target string) (map[string]interface{}, error) {
	var mimeType, url string
	if data := part.InlineData; data != nil {
		mimeType, url = data.MimeType, "data:"+data.MimeType+";base64,"+data.Data
	} else {
		mimeType, url = part.FileData.MimeType, part.FileData.FileURI
		if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
			return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("%s 无法访问 Gemini 文件资源 %q", target, url)}
		}
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Gemini → %s 暂不支持媒体类型 %q", target, mimeType)}
	}
	if target == "responses" {
		return map[string]interface{}{"type": "input_image", "image_url": url}, nil
	}
	if target == "openai" {
		return map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": url}}, nil
	}
	source := map[string]interface{}{"type": "url", "url": url}
	if part.InlineData != nil {
		source = map[string]interface{}{"type": "base64", "media_type": mimeType, "data": part.InlineData.Data}
	}
	return map[string]interface{}{"type": "image", "source": source}, nil
}

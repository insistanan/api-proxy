// OpenAI 非流式响应与思考内容转换。
package providers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/types"
)

// ConvertToClaudeResponse 转换为 Claude 响应
func (p *OpenAIProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	// 少数 OpenAI 兼容网关会接受 Chat Completions 请求却返回 Responses
	// 结构。先识别这类成功响应，避免图片理解层因没有 choices 而丢失结果。
	var responseShape struct {
		Choices json.RawMessage `json:"choices"`
		Output  json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(providerResp.Body, &responseShape); err != nil {
		return nil, err
	}
	if len(responseShape.Output) > 0 && string(responseShape.Output) != "null" &&
		(len(responseShape.Choices) == 0 || string(responseShape.Choices) == "null" || string(responseShape.Choices) == "[]") {
		var responsesResp types.ResponsesResponse
		if err := json.Unmarshal(providerResp.Body, &responsesResp); err != nil {
			return nil, err
		}
		return responsesResponseToClaude(&responsesResp), nil
	}

	var openaiResp types.OpenAIResponse
	if err := json.Unmarshal(providerResp.Body, &openaiResp); err != nil {
		return nil, err
	}
	populateOpenAIReasoningContent(&openaiResp, providerResp.Body)

	var usageEnvelope openAIUsageEnvelope
	if err := json.Unmarshal(providerResp.Body, &usageEnvelope); err != nil {
		return nil, err
	}

	claudeResp := &types.ClaudeResponse{
		ID:      generateID(),
		Type:    "message",
		Role:    "assistant",
		Content: []types.ClaudeContent{},
	}

	if len(openaiResp.Choices) > 0 {
		choice := openaiResp.Choices[0]
		msg := choice.Message
		visibleContent := ""
		thinkingContent := ""
		if content, ok := msg.Content.(string); ok {
			visibleContent = content
			var embeddedReasoning string
			if p.parseEmbeddedReasoning {
				var err error
				visibleContent, embeddedReasoning, err = splitEmbeddedReasoning(content)
				if err != nil {
					return nil, err
				}
			}
			thinkingContent = msg.ReasoningContent
			if thinkingContent == "" {
				thinkingContent = embeddedReasoning
			}
		} else {
			thinkingContent = msg.ReasoningContent
		}

		// 正文与推理属于独立字段，不能仅凭相同内容或前缀推断正文是重复推理。

		// d70d8be 的隐藏行为改为显式 hide 模式；默认独立保留思考，不污染正文。
		if thinkingContent != "" {
			cacheMessage := msg
			cacheMessage.Content = visibleContent
			storeReasoningForAssistantMessage(cacheMessage, thinkingContent, p.reasoningScope)
			if !p.hideReasoning {
				claudeResp.Content = append(claudeResp.Content, types.ClaudeContent{
					Type:     "thinking",
					Thinking: thinkingContent,
				})
			}
		}

		// 添加文本内容
		if visibleContent != "" {
			claudeResp.Content = append(claudeResp.Content, types.ClaudeContent{
				Type: "text",
				Text: visibleContent,
			})
		}

		// 添加工具调用
		for _, toolCall := range msg.ToolCalls {
			var input interface{}
			json.Unmarshal([]byte(toolCall.Function.Arguments), &input)

			claudeResp.Content = append(claudeResp.Content, types.ClaudeContent{
				Type:  "tool_use",
				ID:    toolCall.ID,
				Name:  toolCall.Function.Name,
				Input: input,
			})
		}

		// 设置停止原因
		if len(msg.ToolCalls) > 0 {
			claudeResp.StopReason = "tool_use"
		} else if choice.FinishReason == "length" {
			claudeResp.StopReason = "max_tokens"
		} else {
			claudeResp.StopReason = "end_turn"
		}
	}

	// 添加使用统计
	if openaiResp.Usage != nil {
		normalizedUsage := normalizeOpenAIUsage(openaiResp.Usage, usageEnvelope.Usage)

		claudeResp.Usage = &types.Usage{
			InputTokens:                normalizedUsage.InputTokens,
			OutputTokens:               normalizedUsage.OutputTokens,
			CacheCreationInputTokens:   normalizedUsage.CacheCreationInputTokens,
			CacheReadInputTokens:       normalizedUsage.CacheReadInputTokens,
			CacheCreation5mInputTokens: normalizedUsage.CacheCreation5mInputTokens,
			CacheCreation1hInputTokens: normalizedUsage.CacheCreation1hInputTokens,
			CacheTTL:                   normalizedUsage.CacheTTL,
		}
	}

	return claudeResp, nil
}

func generateID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}

// extractOpenAIReasoningContent 兼容部分 Chat 网关使用的非标准推理字段。
// 对外仍统一发送 reasoning_content，满足严格渠道的历史消息校验。
func extractOpenAIReasoningContent(fields map[string]interface{}) string {
	for _, key := range []string{"reasoning_content", "reasoning", "thinking"} {
		if value, ok := fields[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

// splitEmbeddedReasoning 解析渠道明确使用的嵌入思考格式；未闭合时显式报错。
func splitEmbeddedReasoning(content string) (visible, reasoning string, err error) {
	visibleBuilder := strings.Builder{}
	reasoningBuilder := strings.Builder{}
	parser := embeddedReasoningParser{}
	parts := parser.feed(content)
	tail, err := parser.finish()
	if err != nil {
		return "", "", err
	}
	for _, part := range append(parts, tail...) {
		if part.thinking {
			reasoningBuilder.WriteString(part.text)
		} else {
			visibleBuilder.WriteString(part.text)
		}
	}
	return visibleBuilder.String(), reasoningBuilder.String(), nil
}

func populateOpenAIReasoningContent(response *types.OpenAIResponse, body []byte) {
	if response == nil || len(response.Choices) == 0 {
		return
	}
	var envelope struct {
		Choices []struct {
			Message map[string]interface{} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return
	}
	for index := range response.Choices {
		if response.Choices[index].Message.ReasoningContent != "" || index >= len(envelope.Choices) {
			continue
		}
		response.Choices[index].Message.ReasoningContent = extractOpenAIReasoningContent(envelope.Choices[index].Message)
	}
}

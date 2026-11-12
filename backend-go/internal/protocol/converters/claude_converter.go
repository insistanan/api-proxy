package converters

import (
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// ============== Claude Messages API 转换器 ==============

// ClaudeConverter 实现 Responses → Claude Messages API 转换
type ClaudeConverter struct {
	// Upstream carries channel options (IncludeHistoryThinking, etc.).
	// Injected by convertResponsesRequestWithStructConverter; may be nil in tests.
	Upstream *config.UpstreamConfig
}

// ToProviderRequest 将 Responses 请求转换为 Claude Messages 格式
func (c *ClaudeConverter) ToProviderRequest(sess *types.Session, req *types.ResponsesRequest) (interface{}, error) {
	includeHistoryThinking := c != nil && c.Upstream != nil && c.Upstream.IncludeHistoryThinking

	// 转换 messages 和 system
	messages, system, err := ResponsesToClaudeMessagesWithOptions(sess, req.Input, req.Instructions, includeHistoryThinking)
	if err != nil {
		return nil, err
	}

	// 启用 Claude Prompt Caching：在最后一个 user message 添加 cache_control
	// 这确保稳定的对话前缀能被缓存，提高后续请求的缓存命中率
	if len(messages) > 0 {
		// 找到最后一个 user 角色的消息
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				// 为该消息的内容块添加 cache_control
				if contentBlocks, ok := messages[i].Content.([]types.ClaudeContent); ok && len(contentBlocks) > 0 {
					// 在最后一个内容块添加 cache_control
					lastIdx := len(contentBlocks) - 1
					contentBlocks[lastIdx].CacheControl = &types.CacheControl{Type: "ephemeral"}
					messages[i].Content = contentBlocks
				}
				break
			}
		}
	}

	// 构建 Claude 请求
	claudeReq := map[string]interface{}{
		"model":    req.Model,
		"messages": messages,
		"stream":   req.Stream,
	}

	effectiveMaxTokens := req.MaxOutputTokens
	if effectiveMaxTokens <= 0 {
		effectiveMaxTokens = req.MaxTokens
	}
	if effectiveMaxTokens <= 0 {
		// Responses 客户端可能省略该字段；Claude 侧这里兜底一个安全默认值。
		effectiveMaxTokens = 65536
	}

	// Claude 使用独立的 system 参数（不在 messages 中）
	// 如果有 system prompt，也为它添加 cache_control 以提高缓存命中率
	if system != "" {
		// system 参数支持两种格式：
		// 1. 简单字符串
		// 2. 数组形式，支持 cache_control
		systemBlocks := []map[string]interface{}{
			{
				"type":          "text",
				"text":          system,
				"cache_control": map[string]string{"type": "ephemeral"},
			},
		}
		claudeReq["system"] = systemBlocks
	}

	// 复制其他参数
	claudeReq["max_tokens"] = effectiveMaxTokens
	if req.Temperature > 0 {
		claudeReq["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		claudeReq["top_p"] = req.TopP
	}
	if req.Stop != nil {
		claudeReq["stop_sequences"] = req.Stop // Claude 使用 stop_sequences
	}

	if tools, err := ResponsesToolsToClaudeTools(req.Tools); err != nil {
		return nil, err
	} else if len(tools) > 0 {
		// 为最后一个 tool 添加 cache_control，因为工具定义通常很长且稳定
		// 这样工具定义部分也能被缓存
		if len(tools) > 0 {
			tools[len(tools)-1].CacheControl = &types.CacheControl{Type: "ephemeral"}
		}
		claudeReq["tools"] = tools
	}
	thinking, err := ResponsesReasoningToClaudeThinking(req.Reasoning, effectiveMaxTokens)
	if err != nil {
		return nil, err
	}
	if thinking != nil {
		if err := validateClaudeThinkingToolChoice(req.ToolChoice); err != nil {
			return nil, err
		}
	}

	if toolChoice, err := ResponsesToolChoiceToClaude(req.ToolChoice); err != nil {
		return nil, err
	} else if toolChoice != nil {
		claudeReq["tool_choice"] = toolChoice
	}
	if thinking != nil {
		claudeReq["thinking"] = thinking
	}
	if req.Metadata != nil {
		claudeReq["metadata"] = req.Metadata
	}

	return claudeReq, nil
}

// FromProviderResponse 将 Claude 响应转换为 Responses 格式
func (c *ClaudeConverter) FromProviderResponse(resp map[string]interface{}, sessionID string) (*types.ResponsesResponse, error) {
	return ClaudeResponseToResponses(resp, sessionID)
}

// GetProviderName 获取上游服务名称
func (c *ClaudeConverter) GetProviderName() string {
	return "Claude Messages API"
}

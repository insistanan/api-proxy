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
	effectiveMaxTokens, err = claudeOutputBudget(req.Model, effectiveMaxTokens)
	if err != nil {
		return nil, err
	}

	// 缓存断点由请求构建入口按渠道配置统一注入。
	if system != "" {
		claudeReq["system"] = system
	}

	// 复制其他参数
	claudeReq["max_tokens"] = effectiveMaxTokens
	if req.Temperature != nil {
		claudeReq["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		claudeReq["top_p"] = *req.TopP
	}
	if req.Stop != nil {
		claudeReq["stop_sequences"] = req.Stop // Claude 使用 stop_sequences
	}

	definitions, err := collectResponsesToolDefinitions(req.Tools, req.Input, sessionResponseItems(sess))
	if err != nil {
		return nil, err
	}
	if tools, err := ResponsesToolsToClaudeTools(definitions); err != nil {
		return nil, err
	} else if len(tools) > 0 {
		claudeReq["tools"] = tools
	}
	thinking, err := ResponsesReasoningToClaudeThinking(req.Reasoning, effectiveMaxTokens)
	if err != nil {
		return nil, err
	}
	if thinkingConfig, ok := thinking.(map[string]interface{}); ok && thinkingConfig["type"] != "disabled" {
		if err := validateClaudeThinkingToolChoice(req.ToolChoice); err != nil {
			return nil, err
		}
	}

	if toolChoice, err := ResponsesToolChoiceToClaude(req.ToolChoice); err != nil {
		return nil, err
	} else if toolChoice != nil {
		claudeReq["tool_choice"] = toolChoice
	}
	if req.ParallelToolCalls != nil && !*req.ParallelToolCalls {
		choice, ok := claudeReq["tool_choice"].(map[string]interface{})
		if !ok {
			choice = map[string]interface{}{"type": "auto"}
		}
		choice["disable_parallel_tool_use"] = true
		claudeReq["tool_choice"] = choice
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

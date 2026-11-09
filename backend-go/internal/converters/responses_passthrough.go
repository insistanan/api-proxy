package converters

import (
	"encoding/json"
	"fmt"

	"github.com/insistanan/api-proxy/internal/types"
)

// ============== Responses 透传转换器 ==============

// ResponsesPassthroughConverter 实现 Responses → Responses 透传
// 用于上游服务本身就是 Responses API 的场景
type ResponsesPassthroughConverter struct{}

// ToProviderRequest 透传 Responses 请求（不做转换）
func (c *ResponsesPassthroughConverter) ToProviderRequest(sess *types.Session, req *types.ResponsesRequest) (interface{}, error) {
	// 直接返回原始请求，保留所有字段
	result := map[string]interface{}{
		"model":                req.Model,
		"instructions":         req.Instructions,
		"input":                req.Input,
		"previous_response_id": req.PreviousResponseID,
		"store":                req.Store,
		"max_tokens":           req.MaxTokens,
		"max_output_tokens":    req.MaxOutputTokens,
		"temperature":          req.Temperature,
		"top_p":                req.TopP,
		"frequency_penalty":    req.FrequencyPenalty,
		"presence_penalty":     req.PresencePenalty,
		"stream":               req.Stream,
		"stop":                 req.Stop,
		"user":                 req.User,
		"stream_options":       req.StreamOptions,
		"tools":                req.Tools,
		"tool_choice":          req.ToolChoice,
		"parallel_tool_calls":  req.ParallelToolCalls,
		"reasoning":            req.Reasoning,
		"metadata":             req.Metadata,
	}

	// 关键：透传 prompt_cache_key 和 prompt_cache_retention
	// 这两个字段是 OpenAI Prompt Caching 的核心路由键
	// 如果丢失，会导致缓存完全失效（即使走透传也无法命中缓存）
	if req.PromptCacheKey != "" {
		result["prompt_cache_key"] = req.PromptCacheKey
	}
	if req.PromptCacheRetention != "" {
		result["prompt_cache_retention"] = req.PromptCacheRetention
	}

	return result, nil
}

// FromProviderResponse 透传 Responses 响应（不做转换）
func (c *ResponsesPassthroughConverter) FromProviderResponse(resp map[string]interface{}, sessionID string) (*types.ResponsesResponse, error) {
	// 直接解析为 ResponsesResponse
	// 注意：这里假设上游返回的就是标准 Responses 格式
	id, _ := resp["id"].(string)
	object, _ := resp["object"].(string)
	model, _ := resp["model"].(string)
	status, _ := resp["status"].(string)
	previousID, _ := resp["previous_id"].(string)
	previousResponseID, _ := resp["previous_response_id"].(string)
	created := int64FromInterface(resp["created"])
	createdAt := int64FromInterface(resp["created_at"])

	// 解析 output。未知/畸形 item 不能静默跳过，否则 session 会在本轮成功后
	// 丢掉一个上下文节点，下一轮协议转换时再以错误的历史继续请求。
	output := []types.ResponsesItem{}
	if rawOutput, exists := resp["output"]; exists && rawOutput != nil {
		outputArr, ok := rawOutput.([]interface{})
		if !ok {
			return nil, fmt.Errorf("Responses 响应 output 不是数组")
		}
		output = make([]types.ResponsesItem, 0, len(outputArr))
		for index, item := range outputArr {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("Responses 响应 output 第 %d 项不是对象", index)
			}
			parsed, err := parseResponsesPassthroughItem(itemMap)
			if err != nil {
				return nil, fmt.Errorf("解析 Responses 响应 output 第 %d 项失败: %w", index, err)
			}
			output = append(output, parsed)
		}
	}

	// 解析 usage（使用统一入口自动检测格式：Claude/Gemini/OpenAI）
	usage := ExtractUsageMetrics(resp["usage"])
	_, usagePresent := resp["usage"]

	return &types.ResponsesResponse{
		ID:                 id,
		Object:             object,
		Model:              model,
		Output:             output,
		Status:             status,
		PreviousID:         previousID,
		PreviousResponseID: previousResponseID,
		Usage:              usage,
		Created:            created,
		CreatedAt:          createdAt,
		Extra:              resp,
		UsagePresent:       usagePresent,
	}, nil
}

func parseResponsesPassthroughItem(itemMap map[string]interface{}) (types.ResponsesItem, error) {
	itemType, _ := itemMap["type"].(string)
	if itemType == "" {
		return types.ResponsesItem{}, fmt.Errorf("item 缺少 type")
	}
	arguments, _ := itemMap["arguments"].(string)
	if arguments == "" {
		if rawArguments, exists := itemMap["arguments"]; exists && rawArguments != nil {
			encoded, err := json.Marshal(rawArguments)
			if err != nil {
				return types.ResponsesItem{}, fmt.Errorf("arguments 序列化失败: %w", err)
			}
			arguments = string(encoded)
		}
	}
	content := itemMap["content"]
	if content == nil && itemType == "custom_tool_call" {
		content = itemMap["input"]
	}
	if content == nil {
		content = itemMap["output"]
	}
	item := types.ResponsesItem{
		ID:               stringValue(itemMap, "id"),
		Type:             itemType,
		Status:           stringValue(itemMap, "status"),
		Role:             stringValue(itemMap, "role"),
		Content:          content,
		Summary:          itemMap["summary"],
		CallID:           stringValue(itemMap, "call_id"),
		Name:             stringValue(itemMap, "name"),
		Arguments:        arguments,
		Tools:            itemMap["tools"],
		Namespace:        stringValue(itemMap, "namespace"),
		Execution:        stringValue(itemMap, "execution"),
		EncryptedContent: itemMap["encrypted_content"],
		Signature:        stringValue(itemMap, "signature"),
	}
	if rawToolUse, ok := itemMap["tool_use"].(map[string]interface{}); ok {
		item.ToolUse = &types.ToolUse{
			ID:    stringValue(rawToolUse, "id"),
			Name:  stringValue(rawToolUse, "name"),
			Input: rawToolUse["input"],
		}
	}
	return item, nil
}

func stringValue(values map[string]interface{}, key string) string {
	value, _ := values[key].(string)
	return value
}

func int64FromInterface(value interface{}) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

// GetProviderName 获取上游服务名称
func (c *ResponsesPassthroughConverter) GetProviderName() string {
	return "Responses API (Passthrough)"
}

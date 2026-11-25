package types

import (
	"encoding/json"
)

// ============== Responses API 类型定义 ==============

// ResponsesRequest Responses API 请求
type ResponsesRequest struct {
	Model                string                 `json:"model"`
	Instructions         string                 `json:"instructions,omitempty"` // 系统指令（映射为 system message）
	Input                interface{}            `json:"input"`                  // string 或 []ResponsesItem
	PreviousResponseID   string                 `json:"previous_response_id,omitempty"`
	Store                *bool                  `json:"store,omitempty"`             // 默认 true
	MaxTokens            int                    `json:"max_tokens,omitempty"`        // 最大 tokens
	MaxOutputTokens      int                    `json:"max_output_tokens,omitempty"` // Responses 原生最大输出 tokens
	Temperature          *float64               `json:"temperature,omitempty"`       // nil 表示未设置，保留显式零值
	TopP                 *float64               `json:"top_p,omitempty"`
	FrequencyPenalty     float64                `json:"frequency_penalty,omitempty"`      // 频率惩罚
	PresencePenalty      float64                `json:"presence_penalty,omitempty"`       // 存在惩罚
	Stream               bool                   `json:"stream,omitempty"`                 // 是否流式输出
	Stop                 interface{}            `json:"stop,omitempty"`                   // 停止序列 (string 或 []string)
	User                 string                 `json:"user,omitempty"`                   // 用户标识
	PromptCacheKey       string                 `json:"prompt_cache_key,omitempty"`       // OpenAI Prompt Caching 路由键
	PromptCacheRetention string                 `json:"prompt_cache_retention,omitempty"` // in_memory 或 24h
	StreamOptions        interface{}            `json:"stream_options,omitempty"`         // 流式选项
	Tools                interface{}            `json:"tools,omitempty"`                  // Responses tools
	ToolChoice           interface{}            `json:"tool_choice,omitempty"`            // auto/none/required 或对象
	ParallelToolCalls    *bool                  `json:"parallel_tool_calls,omitempty"`
	Reasoning            interface{}            `json:"reasoning,omitempty"`
	Metadata             map[string]interface{} `json:"metadata,omitempty"`

	// TransformerMetadata 转换器元数据（仅内存使用，不序列化）
	// 用于在单次请求的转换流程中保留原始格式信息，如 system 数组格式等
	// 注意：此字段不会通过 JSON 序列化保留，仅在同一请求处理链中有效
	TransformerMetadata map[string]interface{} `json:"-"`
}

// ContentBlock 内容块（用于嵌套 content 数组）
type ContentBlock struct {
	Type string `json:"type"` // input_text, output_text
	Text string `json:"text"`
}

// ToolUse 工具使用定义
type ToolUse struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Input interface{} `json:"input"`
}

// ResponsesResponse Responses API 响应
type ResponsesResponse struct {
	ID                 string                 `json:"id"`
	Object             string                 `json:"object,omitempty"`
	Model              string                 `json:"model"`
	Output             []ResponsesItem        `json:"output"`
	Status             string                 `json:"status"` // completed, failed
	PreviousID         string                 `json:"previous_id,omitempty"`
	PreviousResponseID string                 `json:"previous_response_id,omitempty"`
	Usage              ResponsesUsage         `json:"usage"`
	Created            int64                  `json:"created,omitempty"`
	CreatedAt          int64                  `json:"created_at,omitempty"`
	Extra              map[string]interface{} `json:"-"`
	UsagePresent       bool                   `json:"-"`
	IncompleteDetails  map[string]interface{} `json:"incomplete_details,omitempty"`
}

func (r ResponsesResponse) MarshalJSON() ([]byte, error) {
	if len(r.Extra) == 0 {
		return json.Marshal(responsesResponseWire{
			ID:                 r.ID,
			Object:             r.Object,
			Model:              r.Model,
			Output:             r.Output,
			Status:             r.Status,
			PreviousID:         r.PreviousID,
			PreviousResponseID: r.PreviousResponseID,
			Usage:              r.Usage,
			Created:            r.Created,
			CreatedAt:          r.CreatedAt,
			IncompleteDetails:  r.IncompleteDetails,
		})
	}

	out := make(map[string]interface{}, len(r.Extra)+8)
	for k, v := range r.Extra {
		out[k] = v
	}
	if r.IncompleteDetails != nil {
		out["incomplete_details"] = r.IncompleteDetails
	}
	if r.ID != "" {
		out["id"] = r.ID
	}
	if r.Object != "" {
		out["object"] = r.Object
	}
	if r.Model != "" {
		out["model"] = r.Model
	}
	if r.Output != nil {
		// 已解析的输出是唯一事实来源，安全处理或字段修改不能被旧原文覆盖。
		out["output"] = r.Output
	}
	if r.Status != "" {
		out["status"] = r.Status
	}
	if r.PreviousID != "" {
		out["previous_id"] = r.PreviousID
	}
	if r.PreviousResponseID != "" {
		out["previous_response_id"] = r.PreviousResponseID
	}
	if r.UsagePresent || !responsesUsageEmpty(r.Usage) {
		out["usage"] = r.Usage
	}
	if r.Created > 0 {
		out["created"] = r.Created
	}
	if r.CreatedAt > 0 {
		out["created_at"] = r.CreatedAt
	}
	return json.Marshal(out)
}

type responsesResponseWire struct {
	IncompleteDetails  map[string]interface{} `json:"incomplete_details,omitempty"`
	ID                 string                 `json:"id"`
	Object             string                 `json:"object,omitempty"`
	Model              string                 `json:"model"`
	Output             []ResponsesItem        `json:"output"`
	Status             string                 `json:"status"`
	PreviousID         string                 `json:"previous_id,omitempty"`
	PreviousResponseID string                 `json:"previous_response_id,omitempty"`
	Usage              ResponsesUsage         `json:"usage"`
	Created            int64                  `json:"created,omitempty"`
	CreatedAt          int64                  `json:"created_at,omitempty"`
}

// UnmarshalJSON 每次整体替换响应，避免复用对象时留下安全处理前的扩展字段。
func (r *ResponsesResponse) UnmarshalJSON(data []byte) error {
	var wire responsesResponseWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var extra map[string]interface{}
	if err := json.Unmarshal(data, &extra); err != nil {
		return err
	}
	_, usagePresent := extra["usage"]
	for _, key := range []string{"id", "object", "model", "output", "status", "previous_id", "previous_response_id", "usage", "created", "created_at", "incomplete_details"} {
		delete(extra, key)
	}
	*r = ResponsesResponse{ID: wire.ID, Object: wire.Object, Model: wire.Model, Output: wire.Output, Status: wire.Status,
		PreviousID: wire.PreviousID, PreviousResponseID: wire.PreviousResponseID, Usage: wire.Usage,
		Created: wire.Created, CreatedAt: wire.CreatedAt, IncompleteDetails: wire.IncompleteDetails, Extra: extra, UsagePresent: usagePresent}
	return nil
}

func responsesUsageEmpty(usage ResponsesUsage) bool {
	return usage.InputTokens == 0 &&
		usage.OutputTokens == 0 &&
		usage.TotalTokens == 0 &&
		usage.CacheCreationInputTokens == 0 &&
		usage.CacheCreation5mInputTokens == 0 &&
		usage.CacheCreation1hInputTokens == 0 &&
		usage.CacheReadInputTokens == 0 &&
		usage.CacheTTL == "" &&
		usage.InputTokensDetails == nil &&
		usage.OutputTokensDetails == nil
}

// ResponsesUsage Responses API 使用统计
// 完整支持 OpenAI Responses API 和 Claude API 的详细 usage 字段
// 参考 claude-code-hub 实现，支持缓存 TTL 细分 (5m/1h)
type ResponsesUsage struct {
	InputTokens         int                  `json:"input_tokens"`
	InputTokensDetails  *InputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokens        int                  `json:"output_tokens"`
	OutputTokensDetails *OutputTokensDetails `json:"output_tokens_details,omitempty"`
	TotalTokens         int                  `json:"total_tokens"`

	// Claude 扩展字段（缓存创建统计，用于精确计费）
	CacheCreationInputTokens   int    `json:"cache_creation_input_tokens,omitempty"`
	CacheCreation5mInputTokens int    `json:"cache_creation_5m_input_tokens,omitempty"` // 5分钟 TTL
	CacheCreation1hInputTokens int    `json:"cache_creation_1h_input_tokens,omitempty"` // 1小时 TTL
	CacheReadInputTokens       int    `json:"cache_read_input_tokens,omitempty"`
	CacheTTL                   string `json:"cache_ttl,omitempty"` // "5m" | "1h" | "mixed"
}

// InputTokensDetails 输入 Token 详细统计
type InputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// OutputTokensDetails 输出 Token 详细统计
type OutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// ResponsesStreamEvent Responses API 流式事件
type ResponsesStreamEvent struct {
	ID         string          `json:"id,omitempty"`
	Model      string          `json:"model,omitempty"`
	Output     []ResponsesItem `json:"output,omitempty"`
	Status     string          `json:"status,omitempty"`
	PreviousID string          `json:"previous_id,omitempty"`
	Usage      *ResponsesUsage `json:"usage,omitempty"`
	Type       string          `json:"type,omitempty"` // delta, done
	Delta      *ResponsesDelta `json:"delta,omitempty"`
}

// ResponsesDelta 流式增量数据
type ResponsesDelta struct {
	Type    string      `json:"type,omitempty"`
	Content interface{} `json:"content,omitempty"`
}

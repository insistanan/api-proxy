package types

import (
	"encoding/json"
	"fmt"
)

// ============== Responses API 类型定义 ==============

// ResponsesRequest Responses API 请求
type ResponsesRequest struct {
	Model                string                 `json:"model"`
	Instructions         string                 `json:"instructions,omitempty"` // 系统指令（映射为 system message）
	Input                interface{}            `json:"input"`                  // string 或 []ResponsesItem
	PreviousResponseID   string                 `json:"previous_response_id,omitempty"`
	Store                *bool                  `json:"store,omitempty"`                  // 默认 true
	MaxTokens            int                    `json:"max_tokens,omitempty"`             // 最大 tokens
	MaxOutputTokens      int                    `json:"max_output_tokens,omitempty"`      // Responses 原生最大输出 tokens
	Temperature          float64                `json:"temperature,omitempty"`            // 温度参数
	TopP                 float64                `json:"top_p,omitempty"`                  // top_p 参数
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

// ResponsesItem Responses API 消息项
type ResponsesItem struct {
	ID        string      `json:"id,omitempty"`
	Type      string      `json:"type"` // message, text, function_call, tool_call, tool_result
	Status    string      `json:"status,omitempty"`
	Role      string      `json:"role,omitempty"` // user, assistant (用于 type=message)
	Content   interface{} `json:"content,omitempty"`
	Summary   interface{} `json:"summary,omitempty"`
	ToolUse   *ToolUse    `json:"tool_use,omitempty"`
	CallID    string      `json:"call_id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Arguments string      `json:"arguments,omitempty"`
	Tools     interface{} `json:"tools,omitempty"`
	Namespace string      `json:"namespace,omitempty"`
	Execution string      `json:"execution,omitempty"`
	// Responses reasoning/tool item 可能携带不透明的完整性材料，必须在会话持久化
	// 和客户端重放匹配过程中保留。
	EncryptedContent interface{} `json:"encrypted_content,omitempty"`
	Signature        string      `json:"signature,omitempty"`
	// 原生 web_search_call 的搜索、打开页面或页内查找动作须完整保留。
	Action interface{} `json:"action,omitempty"`
}

// UnmarshalJSON 保留 Responses 扩展 item 的真实字段形态。
// 尤其是 custom_tool_call 的 input 与 function_call_output 的 output，不能
// 只依赖 content；否则响应转 Claude/Chat 及 previous_response 指纹都会把
// 工具参数或工具结果变成空值。
func (i *ResponsesItem) UnmarshalJSON(data []byte) error {
	if i == nil {
		return fmt.Errorf("ResponsesItem 不能为空")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*i = ResponsesItem{}
	decodeString := func(key string, target *string) {
		if value, ok := raw[key]; ok {
			_ = json.Unmarshal(value, target)
		}
	}
	decodeValue := func(key string) interface{} {
		value, ok := raw[key]
		if !ok || string(value) == "null" {
			return nil
		}
		var decoded interface{}
		if json.Unmarshal(value, &decoded) != nil {
			return nil
		}
		return decoded
	}
	decodeString("id", &i.ID)
	decodeString("type", &i.Type)
	decodeString("status", &i.Status)
	decodeString("role", &i.Role)
	decodeString("call_id", &i.CallID)
	decodeString("name", &i.Name)
	decodeString("namespace", &i.Namespace)
	decodeString("execution", &i.Execution)
	decodeString("signature", &i.Signature)
	i.Content = decodeValue("content")
	i.Summary = decodeValue("summary")
	i.Tools = decodeValue("tools")
	i.EncryptedContent = decodeValue("encrypted_content")
	i.Action = decodeValue("action")
	if i.Content == nil {
		if i.Type == "custom_tool_call" {
			i.Content = decodeValue("input")
		} else {
			i.Content = decodeValue("output")
		}
	}
	if value, ok := raw["arguments"]; ok && string(value) != "null" {
		if err := json.Unmarshal(value, &i.Arguments); err != nil {
			i.Arguments = string(value)
		}
	}
	if value, ok := raw["tool_use"]; ok && string(value) != "null" {
		var toolUse ToolUse
		if err := json.Unmarshal(value, &toolUse); err != nil {
			return err
		}
		i.ToolUse = &toolUse
	}
	return nil
}

func (i ResponsesItem) MarshalJSON() ([]byte, error) {
	out := map[string]interface{}{
		"type": i.Type,
	}
	if i.ID != "" {
		out["id"] = i.ID
	}
	if i.Status != "" {
		out["status"] = i.Status
	}
	if i.Role != "" {
		out["role"] = i.Role
	}
	if i.Content != nil && i.Type != "custom_tool_call" && !responsesItemUsesOutputField(i.Type) {
		out["content"] = i.Content
	}
	if i.Summary != nil {
		out["summary"] = i.Summary
	}
	if i.ToolUse != nil {
		out["tool_use"] = i.ToolUse
	}
	if i.CallID != "" {
		out["call_id"] = i.CallID
	}
	if i.Name != "" && i.Type != "tool_search_call" {
		out["name"] = i.Name
	}
	if i.Tools != nil {
		out["tools"] = i.Tools
	}
	if i.Namespace != "" {
		out["namespace"] = i.Namespace
	}
	if i.Execution != "" {
		out["execution"] = i.Execution
	}
	if i.EncryptedContent != nil {
		out["encrypted_content"] = i.EncryptedContent
	}
	if i.Signature != "" {
		out["signature"] = i.Signature
	}
	if i.Action != nil {
		out["action"] = i.Action
	}

	switch i.Type {
	case "custom_tool_call":
		if i.Content != nil {
			out["input"] = i.Content
		}
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_result":
		if i.Content != nil {
			out["output"] = i.Content
		}
	case "tool_search_call":
		if i.Arguments != "" {
			var parsed interface{}
			if err := json.Unmarshal([]byte(i.Arguments), &parsed); err == nil {
				out["arguments"] = parsed
			} else {
				out["arguments"] = i.Arguments
			}
		}
	default:
		if i.Arguments != "" {
			out["arguments"] = i.Arguments
		}
	}

	return json.Marshal(out)
}

func responsesItemUsesOutputField(itemType string) bool {
	switch itemType {
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_result":
		return true
	default:
		return false
	}
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
		})
	}

	out := make(map[string]interface{}, len(r.Extra)+8)
	for k, v := range r.Extra {
		out[k] = v
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
		_, exists := out["output"]
		if !exists {
			out["output"] = r.Output
		}
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
	ID                 string          `json:"id"`
	Object             string          `json:"object,omitempty"`
	Model              string          `json:"model"`
	Output             []ResponsesItem `json:"output"`
	Status             string          `json:"status"`
	PreviousID         string          `json:"previous_id,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	Usage              ResponsesUsage  `json:"usage"`
	Created            int64           `json:"created,omitempty"`
	CreatedAt          int64           `json:"created_at,omitempty"`
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

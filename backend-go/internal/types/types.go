package types

import (
	"bytes"
	"encoding/json"
)

// ClaudeRequest Claude 请求结构
type ClaudeRequest struct {
	Model               string                 `json:"model"`
	Messages            []ClaudeMessage        `json:"messages"`
	System              interface{}            `json:"system,omitempty"` // string 或 content 数组
	MaxTokens           int                    `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                    `json:"max_completion_tokens,omitempty"`
	Temperature         float64                `json:"temperature,omitempty"`
	TopP                float64                `json:"top_p,omitempty"`
	StopSequences       []string               `json:"stop_sequences,omitempty"`
	Stream              bool                   `json:"stream,omitempty"`
	Tools               []ClaudeTool           `json:"tools,omitempty"`
	ToolChoice          interface{}            `json:"tool_choice,omitempty"`   // string 或 object
	Thinking            interface{}            `json:"thinking,omitempty"`      // {type: "enabled"/"adaptive", budget_tokens: N}
	OutputConfig        map[string]interface{} `json:"output_config,omitempty"` // Claude Code effort 等扩展配置
	Metadata            map[string]interface{} `json:"metadata,omitempty"`      // Claude Code CLI 等客户端发送的元数据
}

// UnmarshalJSON 兼容 Claude Code 的 tools 字符串数组，同时保留标准 Anthropic 工具对象。
func (r *ClaudeRequest) UnmarshalJSON(data []byte) error {
	type claudeRequestWire struct {
		Model               string                 `json:"model"`
		Messages            []ClaudeMessage        `json:"messages"`
		System              interface{}            `json:"system,omitempty"`
		MaxTokens           int                    `json:"max_tokens,omitempty"`
		MaxCompletionTokens int                    `json:"max_completion_tokens,omitempty"`
		Temperature         float64                `json:"temperature,omitempty"`
		TopP                float64                `json:"top_p,omitempty"`
		StopSequences       []string               `json:"stop_sequences,omitempty"`
		Stream              bool                   `json:"stream,omitempty"`
		Tools               json.RawMessage        `json:"tools,omitempty"`
		ToolChoice          interface{}            `json:"tool_choice,omitempty"`
		Thinking            interface{}            `json:"thinking,omitempty"`
		OutputConfig        map[string]interface{} `json:"output_config,omitempty"`
		Metadata            map[string]interface{} `json:"metadata,omitempty"`
	}

	var wire claudeRequestWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	tools, err := decodeClaudeTools(wire.Tools)
	if err != nil {
		return err
	}

	r.Model = wire.Model
	r.Messages = wire.Messages
	r.System = wire.System
	r.MaxTokens = wire.MaxTokens
	r.MaxCompletionTokens = wire.MaxCompletionTokens
	r.Temperature = wire.Temperature
	r.TopP = wire.TopP
	r.StopSequences = wire.StopSequences
	r.Stream = wire.Stream
	r.Tools = tools
	r.ToolChoice = wire.ToolChoice
	r.Thinking = wire.Thinking
	r.OutputConfig = wire.OutputConfig
	r.Metadata = wire.Metadata
	return nil
}

func decodeClaudeTools(raw json.RawMessage) ([]ClaudeTool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}

	tools := make([]ClaudeTool, 0, len(items))
	for _, item := range items {
		item = bytes.TrimSpace(item)
		if len(item) == 0 || bytes.Equal(item, []byte("null")) {
			continue
		}

		switch item[0] {
		case '"':
			var name string
			if err := json.Unmarshal(item, &name); err != nil {
				return nil, err
			}
			if name == "" {
				continue
			}
			tools = append(tools, ClaudeTool{
				Name: name,
				InputSchema: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			})
		case '{':
			var tool ClaudeTool
			if err := json.Unmarshal(item, &tool); err != nil {
				return nil, err
			}
			if tool.Name == "" {
				continue
			}
			if tool.InputSchema == nil {
				tool.InputSchema = map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				}
			}
			tools = append(tools, tool)
		}
	}

	return tools, nil
}

// ClaudeMessage Claude 消息
type ClaudeMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string 或 content 数组
}

// CacheControl Anthropic 缓存控制
// 用于 Claude API 请求，会序列化到 JSON（仅在发送给 Anthropic 时有效）
type CacheControl struct {
	Type string `json:"type,omitempty"` // "ephemeral"
}

// ClaudeContent Claude 内容块
type ClaudeContent struct {
	Type         string        `json:"type"` // text, tool_use, tool_result
	Text         string        `json:"text,omitempty"`
	Thinking     string        `json:"thinking,omitempty"`
	Signature    string        `json:"signature,omitempty"`
	ID           string        `json:"id,omitempty"`
	Name         string        `json:"name,omitempty"`
	Input        interface{}   `json:"input,omitempty"`
	ToolUseID    string        `json:"tool_use_id,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ClaudeTool Claude 工具定义
type ClaudeTool struct {
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	InputSchema  interface{}   `json:"input_schema"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ClaudeResponse Claude 响应
type ClaudeResponse struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Role       string          `json:"role"`
	Content    []ClaudeContent `json:"content"`
	StopReason string          `json:"stop_reason,omitempty"`
	Usage      *Usage          `json:"usage,omitempty"`
}

// ReasoningHistoryError 表示历史中必需的原始推理无法恢复。
// 接入编排的请求错误契约，返回明确的 400，避免无效重试及上游健康惩罚。
type ReasoningHistoryError struct{ Message string }

func (e *ReasoningHistoryError) Error() string            { return e.Message }
func (e *ReasoningHistoryError) ClientRequestError() bool { return true }

// OpenAIRequest OpenAI 请求结构
type OpenAIRequest struct {
	Model               string          `json:"model"`
	Messages            []OpenAIMessage `json:"messages"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	Temperature         float64         `json:"temperature,omitempty"`
	TopP                float64         `json:"top_p,omitempty"`
	Stop                []string        `json:"stop,omitempty"`
	Stream              bool            `json:"stream,omitempty"`
	StreamOptions       interface{}     `json:"stream_options,omitempty"`
	Tools               []OpenAITool    `json:"tools,omitempty"`
	ToolChoice          interface{}     `json:"tool_choice,omitempty"`      // string 或 object
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"` // none/minimal/low/medium/high/xhigh
	PromptCacheKey      string          `json:"prompt_cache_key,omitempty"` // OpenAI Prompt Caching 路由键
}

// OpenAIMessage OpenAI 消息
type OpenAIMessage struct {
	Role             string           `json:"role"`
	Content          interface{}      `json:"content"` // string 或 null
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
}

// OpenAIToolCall OpenAI 工具调用
type OpenAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function OpenAIToolCallFunction `json:"function"`
}

// OpenAIToolCallFunction OpenAI 工具调用函数
type OpenAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// OpenAITool OpenAI 工具定义
type OpenAITool struct {
	Type     string             `json:"type"`
	Function OpenAIToolFunction `json:"function"`
}

// OpenAIToolFunction OpenAI 工具函数
type OpenAIToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters"`
}

// OpenAIResponse OpenAI 响应
type OpenAIResponse struct {
	ID      string         `json:"id"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

// OpenAIChoice OpenAI 选择
type OpenAIChoice struct {
	Message      OpenAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason,omitempty"`
}

// Usage 使用情况统计
// 完整支持 Claude API 的详细 usage 字段，包括缓存 TTL 细分
type Usage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	// 输出明细。与 input 侧的求和契约相反：output_tokens 已含 thinking_tokens，
	// 这里只是拆分展示，客户端绝不能再加一次。
	OutputTokensDetails *ClaudeOutputTokensDetails `json:"output_tokens_details,omitempty"`
	// 缓存 TTL 细分（参考 claude-code-hub）
	CacheCreation5mInputTokens int    `json:"cache_creation_5m_input_tokens,omitempty"` // 5分钟 TTL
	CacheCreation1hInputTokens int    `json:"cache_creation_1h_input_tokens,omitempty"` // 1小时 TTL
	CacheTTL                   string `json:"cache_ttl,omitempty"`                      // "5m" | "1h" | "mixed"
	// OpenAI 兼容字段
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
}

// ClaudeOutputTokensDetails 是 Anthropic 格式 usage 的输出明细。
// 与 Responses 格式的 OutputTokensDetails（字段名 reasoning_tokens）语义相同、
// 线上字段名不同，所以不能合并成一个类型。
//
// 三家 output 侧语义（2026-08 官方文档确证，勿凭直觉改）：
//   - Anthropic：output_tokens **含** thinking，output_tokens_details.thinking_tokens 是明细
//     （platform.claude.com/docs/en/build-with-claude/adaptive-thinking：
//     output_tokens=348 / thinking_tokens=312）。
//   - OpenAI Responses：output_tokens **含** reasoning，且文档明说 reasoning 按 output 计费
//     （developers.openai.com/api/docs/guides/reasoning：
//     input 75 + output 1186 = total 1261，reasoning 1024 不参与加总）。
//   - Gemini usageMetadata：candidatesTokenCount **不含** thoughtsTokenCount，两者相加才是
//     完整输出（url-context 页：input 27 + output 45 + thoughts 31 + tool_use 10309
//     = total 10412）。
//
// 所以 Gemini → Anthropic/Responses 的转换必须把 thoughts 加进 output_tokens，
// 否则 thinking 模型的输出被系统性低报（reasoning 重的场景可达 86%）。
type ClaudeOutputTokensDetails struct {
	ThinkingTokens int `json:"thinking_tokens,omitempty"`
}

// ProviderRequest 提供商请求（通用）
type ProviderRequest struct {
	URL     string
	Method  string
	Headers map[string]string
	Body    interface{}
}

// ProviderResponse 提供商响应（通用）
type ProviderResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
	Stream     bool
}

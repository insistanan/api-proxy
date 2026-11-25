package types

import "encoding/json"

// ============================================================================
// Gemini API 常量
// ============================================================================

// DummyThoughtSignature 用于跳过 Gemini thought_signature 验证
// 参考: https://ai.google.dev/gemini-api/docs/thought-signatures
const DummyThoughtSignature = "skip_thought_signature_validator"

// StripThoughtSignatureMarker 特殊标记，表示需要完全移除 thought_signature 字段
// 用于 stripThoughtSignature 函数标记需要移除的字段
const StripThoughtSignatureMarker = "__STRIP_THOUGHT_SIGNATURE__"

// ============================================================================
// Gemini API 请求结构
// ============================================================================

// GeminiRequest Gemini API 请求
type GeminiRequest struct {
	// 原生发送保留完整载荷，已知字段仅用于读取和跨协议转换。
	RawJSON           json.RawMessage         `json:"-"`
	CachedContent     string                  `json:"cachedContent,omitempty"`
	Contents          []GeminiContent         `json:"contents"`
	SystemInstruction *GeminiContent          `json:"systemInstruction,omitempty"`
	Tools             []GeminiTool            `json:"tools,omitempty"`
	ToolConfig        *GeminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *GeminiGenerationConfig `json:"generationConfig,omitempty"`
	SafetySettings    []GeminiSafetySetting   `json:"safetySettings,omitempty"`
}

// GeminiContent Gemini 内容
type GeminiContent struct {
	Parts []GeminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"` // "user" 或 "model"
}

// GeminiPart Gemini 内容块
type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *GeminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
	FileData         *GeminiFileData         `json:"fileData,omitempty"`
	Thought          bool                    `json:"thought,omitempty"` // 是否为 thinking 内容
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
}

// UnmarshalJSON 自定义反序列化，兼容部分客户端将 thoughtSignature 放在 part 层级的情况（而非 functionCall 内部）
// 示例（Gemini CLI）：
//
//	{
//	  "functionCall": { ... },
//	  "thoughtSignature": "..."
//	}
func (p *GeminiPart) UnmarshalJSON(data []byte) error {
	type partAlias GeminiPart
	var raw struct {
		partAlias
		ThoughtSignatureCamel string `json:"thoughtSignature,omitempty"`
		ThoughtSignatureSnake string `json:"thought_signature,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*p = GeminiPart(raw.partAlias)
	if raw.ThoughtSignatureSnake != "" {
		p.ThoughtSignature = raw.ThoughtSignatureSnake
	} else {
		p.ThoughtSignature = raw.ThoughtSignatureCamel
	}

	// 保留文本/思考 part 的签名，同时维持旧 functionCall 内部存储契约。
	if p.FunctionCall == nil || p.FunctionCall.ThoughtSignature != "" {
		return nil
	}
	p.FunctionCall.ThoughtSignature = p.ThoughtSignature

	return nil
}

// MarshalJSON 自定义序列化：Gemini thoughtSignature 字段位于 part 层级（与 functionCall 同级）。
func (p GeminiPart) MarshalJSON() ([]byte, error) {
	type partAlias GeminiPart
	out := struct {
		partAlias
		ThoughtSignature string `json:"thoughtSignature,omitempty"`
	}{
		partAlias:        partAlias(p),
		ThoughtSignature: p.ThoughtSignature,
	}

	if p.FunctionCall != nil {
		sig := p.FunctionCall.ThoughtSignature
		if sig != "" {
			out.ThoughtSignature = sig
		}
	}
	if out.ThoughtSignature == StripThoughtSignatureMarker {
		out.ThoughtSignature = ""
	}

	return json.Marshal(out)
}

// GeminiInlineData 内联数据（图片、音频等）
type GeminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64 编码
}

// GeminiFileData 文件引用（File API）
type GeminiFileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

// GeminiFunctionCall 函数调用
// 注意：thought_signature 有两种格式：
// - 下划线格式（thought_signature）：Google 官方 API
// - 驼峰格式（thoughtSignature）：Gemini CLI 等第三方客户端
// 为了保持透传，我们记录原始格式并在输出时使用相同格式
type GeminiFunctionCall struct {
	ID               string                 `json:"id,omitempty"`
	Name             string                 `json:"name"`
	Args             map[string]interface{} `json:"args"`
	ThoughtSignature string                 `json:"-"` // thoughtSignature 位于 part 层级，仅内部使用
}

// GeminiFunctionResponse 函数响应
type GeminiFunctionResponse struct {
	ID       string                 `json:"id,omitempty"`
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// GeminiTool 工具定义
type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// GeminiToolConfig 工具调用配置。
type GeminiToolConfig struct {
	FunctionCallingConfig *GeminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// GeminiFunctionCallingConfig 控制 Gemini 是否以及如何调用函数。
type GeminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"` // AUTO / ANY / NONE / VALIDATED
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// GeminiFunctionDeclaration 函数声明
type GeminiFunctionDeclaration struct {
	Name                 string      `json:"name"`
	Description          string      `json:"description,omitempty"`
	Parameters           interface{} `json:"parameters,omitempty"` // Gemini Schema
	ParametersJSONSchema interface{} `json:"parametersJsonSchema,omitempty"`
}

// GeminiGenerationConfig 生成配置
type GeminiGenerationConfig struct {
	CandidateCount     int                   `json:"candidateCount,omitempty"`
	ResponseSchema     interface{}           `json:"responseSchema,omitempty"`
	ResponseJSONSchema interface{}           `json:"responseJsonSchema,omitempty"`
	Temperature        *float64              `json:"temperature,omitempty"`
	TopP               *float64              `json:"topP,omitempty"`
	TopK               *int                  `json:"topK,omitempty"`
	MaxOutputTokens    int                   `json:"maxOutputTokens,omitempty"`
	StopSequences      []string              `json:"stopSequences,omitempty"`
	ResponseMimeType   string                `json:"responseMimeType,omitempty"`   // "application/json" / "text/plain"
	ResponseModalities []string              `json:"responseModalities,omitempty"` // ["TEXT", "IMAGE", "AUDIO"]
	ThinkingConfig     *GeminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

// GeminiThinkingConfig 推理配置
type GeminiThinkingConfig struct {
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int32 `json:"thinkingBudget,omitempty"` // 推理 token 预算
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`  // 或使用 level 替代 budget
}

// GeminiSafetySetting 安全设置
type GeminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// ============================================================================
// Gemini API 响应结构
// ============================================================================

// GeminiResponse Gemini API 响应
type GeminiResponse struct {
	Candidates     []GeminiCandidate     `json:"candidates"`
	PromptFeedback *GeminiPromptFeedback `json:"promptFeedback,omitempty"`
	UsageMetadata  *GeminiUsageMetadata  `json:"usageMetadata,omitempty"`
	ModelVersion   string                `json:"modelVersion,omitempty"`
}

// GeminiCandidate 候选响应
type GeminiCandidate struct {
	Content       *GeminiContent       `json:"content,omitempty"`
	FinishReason  string               `json:"finishReason,omitempty"` // "STOP", "MAX_TOKENS", "SAFETY", "RECITATION"
	SafetyRatings []GeminiSafetyRating `json:"safetyRatings,omitempty"`
	Index         int                  `json:"index,omitempty"`
}

// GeminiPromptFeedback 提示反馈
type GeminiPromptFeedback struct {
	BlockReason   string               `json:"blockReason,omitempty"`
	SafetyRatings []GeminiSafetyRating `json:"safetyRatings,omitempty"`
}

// GeminiSafetyRating 安全评级
type GeminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
}

// GeminiUsageMetadata 使用统计
type GeminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"` // 推理 tokens
}

// ============================================================================
// Gemini 流式响应结构
// ============================================================================

// GeminiStreamChunk Gemini 流式响应块
type GeminiStreamChunk struct {
	Candidates    []GeminiCandidate    `json:"candidates,omitempty"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
}

// ============================================================================
// Gemini 错误响应结构
// ============================================================================

// GeminiError Gemini 错误响应
type GeminiError struct {
	Error GeminiErrorDetail `json:"error"`
}

// GeminiErrorDetail Gemini 错误详情
type GeminiErrorDetail struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

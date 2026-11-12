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

	// 兼容：当签名出现在 part 层级时，将其归一化到 functionCall 内部（内部存储即可）
	if p.FunctionCall == nil || p.FunctionCall.ThoughtSignature != "" {
		return nil
	}
	if raw.ThoughtSignatureSnake != "" {
		p.FunctionCall.ThoughtSignature = raw.ThoughtSignatureSnake
	} else if raw.ThoughtSignatureCamel != "" {
		p.FunctionCall.ThoughtSignature = raw.ThoughtSignatureCamel
	}

	return nil
}

// MarshalJSON 自定义序列化：Gemini thoughtSignature 字段位于 part 层级（与 functionCall 同级）。
func (p GeminiPart) MarshalJSON() ([]byte, error) {
	type partAlias GeminiPart
	out := struct {
		partAlias
		ThoughtSignature string `json:"thoughtSignature,omitempty"`
	}{
		partAlias: partAlias(p),
	}

	if p.FunctionCall != nil {
		sig := p.FunctionCall.ThoughtSignature
		if sig != "" && sig != StripThoughtSignatureMarker {
			out.ThoughtSignature = sig
		}
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
	Name             string                 `json:"name"`
	Args             map[string]interface{} `json:"args"`
	ThoughtSignature string                 `json:"-"` // thoughtSignature 位于 part 层级，仅内部使用
}

// GeminiFunctionResponse 函数响应
type GeminiFunctionResponse struct {
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
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"` // JSON Schema
}

// UnmarshalJSON 自定义反序列化：
// - 支持 parameters（官方字段）
// - 兼容部分客户端使用 parametersJsonSchema（例如 Gemini CLI）
// 为了让上游模型正确理解参数结构，统一写入 Parameters，并在序列化时输出为 parameters。
func (fd *GeminiFunctionDeclaration) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if nameRaw, ok := raw["name"]; ok {
		if err := json.Unmarshal(nameRaw, &fd.Name); err != nil {
			return err
		}
	}
	if descRaw, ok := raw["description"]; ok {
		if err := json.Unmarshal(descRaw, &fd.Description); err != nil {
			return err
		}
	}

	var paramsRaw json.RawMessage
	if v, ok := raw["parameters"]; ok {
		paramsRaw = v
	} else if v, ok := raw["parametersJsonSchema"]; ok {
		paramsRaw = v
	}
	if paramsRaw != nil {
		var params interface{}
		if err := json.Unmarshal(paramsRaw, &params); err != nil {
			return err
		}
		fd.Parameters = sanitizeGeminiToolSchema(params)
	}

	return nil
}

// sanitizeGeminiToolSchema 清洗工具参数 schema，以兼容 Gemini functionDeclaration.parameters 的严格校验。
func sanitizeGeminiToolSchema(v interface{}) interface{} {
	switch vv := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(vv))
		var constValue interface{}
		hasConst := false

		for k, val := range vv {
			switch k {
			case "$schema", "$id", "$ref", "$defs", "definitions",
				"additionalProperties", "unevaluatedProperties", "propertyNames", "patternProperties",
				"oneOf", "allOf", "not", "if", "then", "else",
				"dependentRequired", "dependentSchemas", "contains", "minContains", "maxContains",
				"prefixItems", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "examples":
				continue
			case "const":
				constValue = val
				hasConst = true
				continue
			case "type":
				if schemaType, nullable, ok := sanitizeGeminiSchemaType(val); ok {
					out[k] = schemaType
					if nullable {
						out["nullable"] = true
					}
				}
			case "properties":
				if properties, ok := val.(map[string]interface{}); ok {
					cleanedProperties := make(map[string]interface{}, len(properties))
					for name, propertySchema := range properties {
						cleanedProperties[name] = sanitizeGeminiToolSchema(propertySchema)
					}
					out[k] = cleanedProperties
				}
			case "items":
				out[k] = sanitizeGeminiToolSchema(val)
			case "anyOf":
				if values, ok := val.([]interface{}); ok {
					cleanedValues := make([]interface{}, 0, len(values))
					for _, value := range values {
						cleanedValues = append(cleanedValues, sanitizeGeminiToolSchema(value))
					}
					out[k] = cleanedValues
				}
			case "format", "title", "description", "nullable", "enum",
				"maxItems", "minItems", "required", "minProperties", "maxProperties",
				"minLength", "maxLength", "pattern", "example", "propertyOrdering",
				"default", "minimum", "maximum":
				out[k] = val
			}
		}

		if hasConst {
			if _, ok := out["enum"]; !ok {
				out["enum"] = []interface{}{sanitizeGeminiToolSchema(constValue)}
			}
		}
		normalizeGeminiSchemaShape(out)

		return out
	case []interface{}:
		out := make([]interface{}, len(vv))
		for i := range vv {
			out[i] = sanitizeGeminiToolSchema(vv[i])
		}
		return out
	default:
		return v
	}
}

func sanitizeGeminiSchemaType(v interface{}) (interface{}, bool, bool) {
	if str, ok := v.(string); ok {
		if str == "" {
			return nil, false, false
		}
		return str, false, true
	}

	values, ok := v.([]interface{})
	if !ok {
		return nil, false, false
	}

	nullable := false
	for _, value := range values {
		str, ok := value.(string)
		if ok && str == "null" {
			nullable = true
		}
		if ok && str != "" && str != "null" {
			return str, nullable, true
		}
	}
	return nil, nullable, false
}

func normalizeGeminiSchemaShape(schema map[string]interface{}) {
	if _, hasType := schema["type"]; !hasType {
		if _, hasProperties := schema["properties"]; hasProperties {
			schema["type"] = "object"
		} else if _, hasItems := schema["items"]; hasItems {
			schema["type"] = "array"
		} else if enumValues, ok := schema["enum"].([]interface{}); ok {
			if inferredType, ok := inferGeminiSchemaTypeFromValues(enumValues); ok {
				schema["type"] = inferredType
			}
		}
	}

	if schema["type"] == "object" {
		if _, hasProperties := schema["properties"]; !hasProperties {
			schema["properties"] = map[string]interface{}{}
		}
	}
}

func inferGeminiSchemaTypeFromValues(values []interface{}) (string, bool) {
	for _, value := range values {
		switch value.(type) {
		case string:
			return "string", true
		case bool:
			return "boolean", true
		case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return "number", true
		case []interface{}:
			return "array", true
		case map[string]interface{}:
			return "object", true
		}
	}
	return "", false
}

// SanitizeGeminiToolSchema 清洗 Gemini functionDeclaration 参数 schema。
func SanitizeGeminiToolSchema(v interface{}) interface{} {
	return sanitizeGeminiToolSchema(v)
}

// GeminiGenerationConfig 生成配置
type GeminiGenerationConfig struct {
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

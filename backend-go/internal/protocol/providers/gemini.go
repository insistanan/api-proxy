package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"insistanan/api-proxy/internal/dialogue/conversation"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/converters"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/protocol/visionmedia"
)

// GeminiProvider Gemini 提供商
type GeminiProvider struct {
	cacheLease                  *conversation.RequestLease
	shadowProviderID            string
	shadowSessionID             string
	shadowSnapshot              GeminiShadowSnapshot
	stripThoughtSignature       bool
	injectDummyThoughtSignature bool
}

// ConvertToProviderRequest 转换为 Gemini 请求。
// 解析入站请求后交给 BuildRequest。
func (p *GeminiProvider) ConvertToProviderRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string) (*http.Request, []byte, error) {
	originalBodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("读取请求体失败: %w", err)
	}
	// 恢复请求体，以便gin context可以被其他地方再次读取（尽管这里我们已经完全处理了）
	c.Request.Body = io.NopCloser(bytes.NewReader(originalBodyBytes))

	outcome, err := p.BuildRequest(RequestInput{
		Ctx:            c.Request.Context(),
		Method:         http.MethodPost,
		Path:           "/v1beta/models",
		Header:         c.Request.Header,
		Body:           originalBodyBytes,
		ConversationID: c.GetString(upstreamreq.ContextKeyConversationUserID),
	}, upstream, apiKey)
	if err != nil {
		return nil, originalBodyBytes, err
	}
	return outcome.Request, outcome.OriginalBody, nil
}

// BuildRequest 以显式输入构造 Gemini 请求（不依赖 Gin）。
func (p *GeminiProvider) buildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	p.cacheLease = conversation.RequestLeaseFrom(input.Ctx)
	originalBodyBytes := input.Body
	if err := converters.ValidateClaudeConversion(input.Body, "gemini"); err != nil {
		return nil, err
	}

	var claudeReq types.ClaudeRequest
	if err := json.Unmarshal(originalBodyBytes, &claudeReq); err != nil {
		return nil, fmt.Errorf("解析Claude请求体失败: %w", err)
	}

	p.shadowProviderID = ""
	if upstream != nil {
		p.shadowProviderID = upstream.RequestIdentity(apiKey).Fingerprint() + "|" + config.ResolveUpstreamModel(claudeReq.Model, upstream)
	}
	p.shadowSessionID = input.ConversationID
	p.shadowSnapshot = defaultGeminiShadowStore.Get(p.shadowProviderID, p.shadowSessionID)
	p.stripThoughtSignature = false
	p.injectDummyThoughtSignature = false
	if upstream != nil {
		p.stripThoughtSignature = upstream.StripThoughtSignature
		p.injectDummyThoughtSignature = upstream.InjectDummyThoughtSignature
	}

	geminiReq, err := p.convertToGeminiRequest(&claudeReq, upstream)
	if err != nil {
		return nil, err
	}

	reqBodyBytes, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("序列化Gemini请求体失败: %w", err)
	}

	model := config.ResolveUpstreamModel(claudeReq.Model, upstream)
	baseURL := ""
	if upstream != nil {
		baseURL = upstream.GetEffectiveBaseURL()
	}
	url := buildGeminiGenerateContentURL(baseURL, model, claudeReq.Stream, upstream != nil && upstream.UseFullURL)

	req, err := http.NewRequestWithContext(input.Ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建Gemini请求失败: %w", err)
	}

	// 使用统一的头部处理逻辑（透明代理）
	// 保留客户端的大部分 headers，只移除/替换必要的认证和代理相关 headers
	req.Header = upstreamreq.PrepareUpstreamHeadersFromHeader(input.Header, req.URL.Host)

	// 删除开发测试注入的客户端请求头，避免上游严格验证报错。
	req.Header.Del("X-Codex-Window-Id")
	req.Header.Del("X-Codex-Installation-Id")
	req.Header.Del("X-Request-Id")
	req.Header.Del("X-Codex-Turn-Metadata")
	req.Header.Del("X-Claude-Code-Session-Id")
	req.Header.Del("X-Stainless-Lang")
	req.Header.Del("X-Stainless-Runtime")
	req.Header.Del("X-Stainless-Runtime-Version")
	req.Header.Del("X-Stainless-Os")
	req.Header.Del("X-Stainless-Arch")
	req.Header.Del("X-Stainless-Package-Version")
	req.Header.Del("X-Stainless-Retry-Count")
	req.Header.Del("X-Stainless-Timeout")
	req.Header.Del("X-App")

	upstreamreq.SetGeminiAuthenticationHeader(req.Header, apiKey)

	return &RequestOutcome{Request: req, OriginalBody: originalBodyBytes}, nil
}

func buildGeminiGenerateContentURL(baseURL string, model string, stream bool, useFullURL ...bool) string {
	modelPath := strings.TrimPrefix(strings.TrimSpace(model), "/")
	if !strings.HasPrefix(modelPath, "models/") && !strings.HasPrefix(modelPath, "tunedModels/") {
		modelPath = "models/" + modelPath
	}

	action := "generateContent"
	if stream {
		action = "streamGenerateContent?alt=sse"
	}

	// "#"后缀与版本前缀约定见 upstreamreq.BuildUpstreamURL；Gemini 默认版本段为 /v1beta
	return upstreamreq.BuildUpstreamURL(baseURL, "/v1beta", fmt.Sprintf("/%s:%s", modelPath, action), useFullURL...)
}

// convertToGeminiRequest 转换为 Gemini 请求体
func (p *GeminiProvider) convertToGeminiRequest(claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig) (map[string]interface{}, error) {
	systemText := buildGeminiSystemInstructionText(claudeReq)
	contents, err := p.convertMessages(claudeReq.Messages, p.shadowSnapshot)
	if err != nil {
		return nil, err
	}
	req := map[string]interface{}{
		"contents": contents,
	}

	// 添加系统指令
	if systemText != "" {
		req["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]string{
				{"text": systemText},
			},
		}
	}

	// 生成配置
	genConfig := map[string]interface{}{}

	if claudeReq.MaxCompletionTokens > 0 {
		genConfig["maxOutputTokens"] = claudeReq.MaxCompletionTokens
	} else if claudeReq.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = claudeReq.MaxTokens
	}

	if claudeReq.Temperature != nil {
		genConfig["temperature"] = *claudeReq.Temperature
	}
	if claudeReq.TopP != nil {
		genConfig["topP"] = *claudeReq.TopP
	}
	if len(claudeReq.StopSequences) > 0 {
		genConfig["stopSequences"] = claudeReq.StopSequences
	}

	// thinking → generationConfig.thinkingConfig。Claude thinking 与 Responses
	// reasoning 同为 effort 语义，复用 converters.ResponsesReasoningToGeminiThinking
	// 的档位映射（Gemini 2.5 预算表 / Gemini 3 thinkingLevel 分支都在那里），
	// 不在 providers 再写一份。
	mappedRequest := *claudeReq
	mappedRequest.Model = config.ResolveUpstreamModel(claudeReq.Model, upstream)
	if cfg, err := geminiThinkingConfigFromClaude(&mappedRequest); err != nil {
		return nil, err
	} else if cfg != nil {
		genConfig["thinkingConfig"] = cfg
	}

	if len(genConfig) > 0 {
		req["generationConfig"] = genConfig
	}

	// 工具
	var functionDeclarations []map[string]interface{}
	if len(claudeReq.Tools) > 0 {
		functionDeclarations = p.convertTools(claudeReq.Tools)
		if len(functionDeclarations) > 0 {
			req["tools"] = []map[string]interface{}{
				{
					"functionDeclarations": functionDeclarations,
				},
			}
		}
	}

	toolConfig, err := mapClaudeToolChoiceToGemini(claudeReq.ToolChoice)
	if err != nil {
		return nil, err
	}
	if toolConfig != nil && len(functionDeclarations) > 0 {
		req["toolConfig"] = toolConfig
	}

	return req, nil
}

// geminiThinkingConfigFromClaude 把 Claude thinking 配置转为 Gemini thinkingConfig。
// effort 解析复用 resolveClaudeReasoningEffort（与 Chat/Responses 上游同判据），
// 档位映射复用 converters.ResponsesReasoningToGeminiThinking。
func geminiThinkingConfigFromClaude(claudeReq *types.ClaudeRequest) (*types.GeminiThinkingConfig, error) {
	effort, err := resolveClaudeReasoningEffort(claudeReq)
	if err != nil {
		return nil, err
	}
	if effort == "" {
		return nil, nil
	}
	reasoning := map[string]interface{}{"effort": effort}
	return converters.ResponsesReasoningToGeminiThinking(claudeReq.Model, reasoning)
}

func buildGeminiSystemInstructionText(claudeReq *types.ClaudeRequest) string {
	parts := make([]string, 0, 1)
	if claudeReq.System != nil {
		if systemText := extractSystemText(claudeReq.System); systemText != "" {
			parts = append(parts, systemText)
		}
	}
	for _, msg := range claudeReq.Messages {
		if strings.ToLower(msg.Role) != "system" {
			continue
		}
		if text := extractClaudeMessageText(msg.Content); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func extractClaudeMessageText(content interface{}) string {
	if str, ok := content.(string); ok {
		return str
	}
	blocks := visionmedia.NormalizeContentBlocks(content)
	if len(blocks) == 0 {
		return ""
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text, ok := visionmedia.ExtractTextFromBlock(block); ok {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

// convertMessages 转换消息
func (p *GeminiProvider) convertMessages(claudeMessages []types.ClaudeMessage, snapshot GeminiShadowSnapshot) ([]map[string]interface{}, error) {
	messages := make([]map[string]interface{}, 0, len(claudeMessages))
	names := buildClaudeToolNameMap(claudeMessages)
	for _, message := range claudeMessages {
		converted, err := p.convertMessage(message, names)
		if err != nil {
			return nil, err
		}
		if converted == nil {
			continue
		}
		if message.Role == "assistant" {
			restoreGeminiShadowMetadata(converted, message.Content, snapshot, p.stripThoughtSignature, p.injectDummyThoughtSignature)
		}
		messages = append(messages, converted)
	}
	return messages, nil
}

func buildClaudeToolNameMap(messages []types.ClaudeMessage) map[string]string {
	toolNameByID := map[string]string{}
	for _, msg := range messages {
		if strings.ToLower(msg.Role) != "assistant" {
			continue
		}
		for _, block := range visionmedia.NormalizeContentBlocks(msg.Content) {
			blockType, _ := block["type"].(string)
			if blockType != "tool_use" {
				continue
			}
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			if id != "" && name != "" {
				toolNameByID[id] = name
			}
		}
	}
	return toolNameByID
}

func extractAssistantToolUseIDs(content interface{}) map[string]bool {
	ids := map[string]bool{}
	for _, block := range visionmedia.NormalizeContentBlocks(content) {
		if block["type"] == "tool_use" {
			if id, _ := block["id"].(string); id != "" {
				ids[id] = true
			}
		}
	}
	return ids
}

func geminiShadowReplayParts(content map[string]interface{}, stripThoughtSignature bool, injectDummyThoughtSignature bool) []interface{} {
	if len(content) == 0 {
		return nil
	}
	var parts []interface{}
	if rawParts, ok := content["parts"].([]interface{}); ok {
		parts = cloneInterface(rawParts).([]interface{})
	} else if rawArray, ok := content["parts"].([]map[string]interface{}); ok {
		parts = make([]interface{}, 0, len(rawArray))
		for _, part := range rawArray {
			parts = append(parts, cloneMapStringInterface(part))
		}
	} else {
		return nil
	}

	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]interface{})
		if !ok {
			continue
		}
		applyGeminiThoughtSignaturePolicy(part, stripThoughtSignature, injectDummyThoughtSignature)
		fc, ok := part["functionCall"].(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := fc["id"].(string)
		if !shouldSendGeminiFunctionID(id) {
			delete(fc, "id")
		}
	}
	return parts
}

// convertMessage 转换单个消息
func (p *GeminiProvider) convertMessage(msg types.ClaudeMessage, toolNameByID map[string]string) (map[string]interface{}, error) {
	role := strings.ToLower(msg.Role)
	if role == "assistant" {
		role = "model"
	} else {
		role = "user"
	}

	parts := []interface{}{}

	// 处理字符串内容
	if str, ok := msg.Content.(string); ok {
		parts = append(parts, map[string]interface{}{
			"text": str,
		})
		return map[string]interface{}{
			"role":  role,
			"parts": parts,
		}, nil
	}

	contents := visionmedia.NormalizeContentBlocks(msg.Content)
	if len(contents) == 0 {
		return nil, nil
	}

	for _, content := range contents {
		contentType, _ := content["type"].(string)

		switch contentType {
		case "thinking":
			// Gemini contents 没有可透传的 Claude thinking 内容块。

		case "text":
			if text, ok := content["text"].(string); ok {
				parts = append(parts, map[string]interface{}{
					"text": text,
				})
			}

		case "image", "image_url", "input_image", "document":
			if part, ok := claudeBlockToGeminiInlineData(content); ok {
				parts = append(parts, part)
			}

		case "tool_use":
			id, _ := content["id"].(string)
			name, _ := content["name"].(string)
			input := content["input"]
			if input == nil {
				input = map[string]interface{}{}
			}
			if id != "" && name != "" {
				toolNameByID[id] = name
			}

			functionCall := map[string]interface{}{
				"name": name,
				"args": input,
			}
			if id != "" && shouldSendGeminiFunctionID(id) {
				functionCall["id"] = id
			}
			part := map[string]interface{}{"functionCall": functionCall}
			applyGeminiThoughtSignaturePolicy(part, p.stripThoughtSignature, p.injectDummyThoughtSignature)

			parts = append(parts, part)

		case "tool_result":
			toolUseID, _ := content["tool_use_id"].(string)
			name := toolNameByID[toolUseID]
			if name == "" {
				return nil, fmt.Errorf("Claude -> Gemini 无法解析 tool_result 对应的 functionResponse.name: tool_use_id=%q", toolUseID)
			}

			functionResponse := map[string]interface{}{
				"name":     name,
				"response": normalizeGeminiFunctionResponse(content["content"]),
			}
			if toolUseID != "" && shouldSendGeminiFunctionID(toolUseID) {
				functionResponse["id"] = toolUseID
			}

			parts = append(parts, map[string]interface{}{"functionResponse": functionResponse})
		}
	}

	if len(parts) == 0 {
		return nil, nil
	}

	return map[string]interface{}{
		"role":  role,
		"parts": parts,
	}, nil
}

func normalizeGeminiFunctionResponse(content interface{}) map[string]interface{} {
	if content == nil {
		return map[string]interface{}{"content": ""}
	}
	if _, isString := content.(string); !isString {
		if blocks, ok := content.([]map[string]interface{}); ok && len(blocks) > 0 {
			texts := make([]string, 0, len(blocks))
			for _, block := range blocks {
				if text, ok := visionmedia.ExtractTextFromBlock(block); ok {
					texts = append(texts, text)
				}
			}
			if len(texts) > 0 {
				return map[string]interface{}{"content": strings.Join(texts, "\n")}
			}
			return map[string]interface{}{"content": content}
		}
	}
	if blocks := visionmedia.NormalizeContentBlocks(content); len(blocks) > 0 {
		texts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if text, ok := visionmedia.ExtractTextFromBlock(block); ok {
				texts = append(texts, text)
			}
		}
		if len(texts) > 0 {
			return map[string]interface{}{"content": strings.Join(texts, "\n")}
		}
	}
	return map[string]interface{}{"content": content}
}

func mapClaudeToolChoiceToGemini(toolChoice interface{}) (map[string]interface{}, error) {
	if toolChoice == nil {
		return nil, nil
	}
	switch v := toolChoice.(type) {
	case string:
		switch v {
		case "", "auto":
			return map[string]interface{}{"functionCallingConfig": map[string]interface{}{"mode": "AUTO"}}, nil
		case "none":
			return map[string]interface{}{"functionCallingConfig": map[string]interface{}{"mode": "NONE"}}, nil
		case "any":
			return map[string]interface{}{"functionCallingConfig": map[string]interface{}{"mode": "ANY"}}, nil
		default:
			return nil, fmt.Errorf("Claude -> Gemini 不支持的 tool_choice 字符串: %s", v)
		}
	case map[string]interface{}:
		choiceType, _ := v["type"].(string)
		switch choiceType {
		case "", "auto":
			return map[string]interface{}{"functionCallingConfig": map[string]interface{}{"mode": "AUTO"}}, nil
		case "none":
			return map[string]interface{}{"functionCallingConfig": map[string]interface{}{"mode": "NONE"}}, nil
		case "any":
			return map[string]interface{}{"functionCallingConfig": map[string]interface{}{"mode": "ANY"}}, nil
		case "tool":
			name, _ := v["name"].(string)
			if name == "" {
				if tool, ok := v["tool"].(map[string]interface{}); ok {
					name, _ = tool["name"].(string)
				}
			}
			if name == "" {
				return nil, fmt.Errorf("Claude -> Gemini tool_choice.type=tool 缺少 name")
			}
			return map[string]interface{}{
				"functionCallingConfig": map[string]interface{}{
					"mode":                 "ANY",
					"allowedFunctionNames": []string{name},
				},
			}, nil
		default:
			return nil, fmt.Errorf("Claude -> Gemini 不支持的 tool_choice.type: %s", choiceType)
		}
	default:
		return nil, fmt.Errorf("Claude -> Gemini 不支持的 tool_choice 类型: %T", toolChoice)
	}
}

func shouldSendGeminiFunctionID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if strings.HasPrefix(id, geminiSynthesizedIDPrefix) || strings.HasPrefix(id, "toolu_") {
		return false
	}
	return true
}

func synthesizeGeminiToolCallID() string {
	return fmt.Sprintf("%s%s", geminiSynthesizedIDPrefix, uuid.NewString())
}

// geminiFunctionCallKey 只用于同一条流内的去重。Gemini 原生流通常只发送一次
// 完整 functionCall，但兼容网关可能在后续 chunk 重复发送相同对象；没有稳定 id
// 时用名称和参数的规范表示识别它。不同参数仍视为不同调用，避免吞掉真实调用。

func rectifyGeminiFunctionCallIDs(parts []interface{}) {
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]interface{})
		if !ok {
			continue
		}
		fc, ok := part["functionCall"].(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := fc["id"].(string)
		if strings.TrimSpace(id) == "" {
			fc["id"] = synthesizeGeminiToolCallID()
		}
	}
}

func visibleGeminiShadowParts(parts []interface{}) []interface{} {
	out := make([]interface{}, 0, len(parts))
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]interface{})
		if !ok {
			continue
		}
		if isGeminiThoughtPart(part) {
			continue
		}
		out = append(out, cloneMapStringInterface(part))
	}
	return out
}

func isGeminiThoughtPart(part map[string]interface{}) bool {
	if thought, ok := part["thought"].(bool); ok {
		return thought
	}
	if thought, ok := part["thought"].(string); ok {
		return thought != "" && thought != "false"
	}
	return false
}

func buildGeminiShadowTurnFromParts(parts []interface{}) GeminiShadowTurn {
	visibleParts := visibleGeminiShadowParts(parts)
	return GeminiShadowTurn{
		AssistantContent: map[string]interface{}{"parts": visibleParts},
		ToolCalls:        extractGeminiShadowToolCalls(visibleParts),
	}
}

func geminiShadowTurnHasParts(turn GeminiShadowTurn) bool {
	parts, ok := turn.AssistantContent["parts"].([]interface{})
	return ok && len(parts) > 0
}

func extractGeminiShadowToolCalls(parts []interface{}) []GeminiShadowToolCall {
	toolCalls := make([]GeminiShadowToolCall, 0)
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]interface{})
		if !ok {
			continue
		}
		if isGeminiThoughtPart(part) {
			continue
		}
		fc, ok := part["functionCall"].(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := fc["id"].(string)
		if id == "" {
			id = synthesizeGeminiToolCallID()
			fc["id"] = id
		}
		name, _ := fc["name"].(string)
		toolCalls = append(toolCalls, GeminiShadowToolCall{
			ID:               id,
			Name:             name,
			Args:             fc["args"],
			ThoughtSignature: extractGeminiThoughtSignature(part),
		})
	}
	return toolCalls
}

func extractGeminiThoughtSignature(part map[string]interface{}) string {
	for _, key := range []string{"thoughtSignature", "thought_signature"} {
		if sig, _ := part[key].(string); sig != "" {
			return sig
		}
	}
	if fc, ok := part["functionCall"].(map[string]interface{}); ok {
		for _, key := range []string{"thoughtSignature", "thought_signature"} {
			if sig, _ := fc[key].(string); sig != "" {
				return sig
			}
		}
	}
	return ""
}

func applyGeminiThoughtSignaturePolicy(part map[string]interface{}, strip bool, injectDummy bool) {
	fc, _ := part["functionCall"].(map[string]interface{})
	if strip {
		delete(part, "thoughtSignature")
		delete(part, "thought_signature")
		delete(fc, "thoughtSignature")
		delete(fc, "thought_signature")
		return
	}
	if fc == nil {
		return
	}
	if _, exists := part["thoughtSignature"]; !exists {
		if sig, _ := fc["thoughtSignature"].(string); sig != "" {
			part["thoughtSignature"] = sig
		} else if sig, _ := fc["thought_signature"].(string); sig != "" {
			part["thoughtSignature"] = sig
		}
	}
	delete(fc, "thoughtSignature")
	delete(fc, "thought_signature")
	if !injectDummy {
		return
	}
	if extractGeminiThoughtSignature(part) != "" {
		return
	}
	part["thoughtSignature"] = types.DummyThoughtSignature
}

func claudeBlockToGeminiInlineData(block map[string]interface{}) (map[string]interface{}, bool) {
	part, ok := visionmedia.ToGeminiImagePart(block, false)
	if !ok {
		return nil, false
	}
	return map[string]interface{}{"inlineData": part.InlineData}, true
}

// convertTools 转换工具
func (p *GeminiProvider) convertTools(claudeTools []types.ClaudeTool) []map[string]interface{} {
	tools := []map[string]interface{}{}

	for _, tool := range claudeTools {
		if tool.Name == "" {
			continue
		}
		tools = append(tools, buildGeminiFunctionDeclaration(tool.Name, tool.Description, tool.InputSchema))
	}

	return tools
}

func buildGeminiFunctionDeclaration(name string, description string, inputSchema interface{}) map[string]interface{} {
	declaration := map[string]interface{}{
		"name":        name,
		"description": description,
	}

	declaration["parametersJsonSchema"] = inputSchema
	return declaration
}

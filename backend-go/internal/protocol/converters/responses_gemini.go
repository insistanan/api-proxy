package converters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/items"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/protocol/visionmedia"
)

func ConvertResponsesToGeminiRequest(model string, sess *types.Session, req *types.ResponsesRequest) ([]byte, error) {
	items, err := parseResponsesInput(req.Input)
	if err != nil {
		return nil, err
	}
	if sess != nil && len(sess.Messages) > 0 {
		items = respitems.MergeResponsesItemsDedup(sess.Messages, items)
	}
	if respitems.ResponsesItemsContainCompaction(items) {
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Responses compaction item 只能由 Responses 原生上游处理，无法转换为 Gemini")}
	}
	items, instructions, err := extractResponsesSystem(items, req.Instructions)
	if err != nil {
		return nil, err
	}
	callNames := buildFunctionCallNameMap(items)

	geminiReq := types.GeminiRequest{
		Contents: make([]types.GeminiContent, 0, len(items)),
	}
	if instructions != "" {
		geminiReq.SystemInstruction = &types.GeminiContent{Parts: []types.GeminiPart{{Text: instructions}}}
	}
	for _, item := range items {
		content, ok, err := responsesItemToGeminiContent(item, callNames)
		if err != nil {
			return nil, err
		}
		if ok {
			geminiReq.Contents = append(geminiReq.Contents, content)
		}
	}
	definitions, err := collectResponsesToolDefinitions(req.Tools, items)
	if err != nil {
		return nil, err
	}
	if tools, err := responsesToolsToGeminiTools(definitions); err != nil {
		return nil, err
	} else if len(tools) > 0 {
		geminiReq.Tools = tools
	}
	if toolConfig, err := responsesToolChoiceToGemini(req.ToolChoice); err != nil {
		return nil, err
	} else if toolConfig != nil {
		geminiReq.ToolConfig = toolConfig
	}
	if len(geminiReq.Tools) == 0 {
		geminiReq.ToolConfig = nil
	}

	genCfg := &types.GeminiGenerationConfig{}
	if req.MaxOutputTokens > 0 {
		genCfg.MaxOutputTokens = req.MaxOutputTokens
	} else if req.MaxTokens > 0 {
		genCfg.MaxOutputTokens = req.MaxTokens
	}
	genCfg.Temperature = req.Temperature
	genCfg.TopP = req.TopP
	if req.Stop != nil {
		if stops, err := stringSlice(req.Stop); err != nil {
			return nil, err
		} else {
			genCfg.StopSequences = stops
		}
	}
	if cfg, err := ResponsesReasoningToGeminiThinking(model, req.Reasoning); err != nil {
		return nil, err
	} else if cfg != nil {
		genCfg.ThinkingConfig = cfg
	}
	if genCfg.MaxOutputTokens > 0 || genCfg.Temperature != nil || genCfg.TopP != nil || len(genCfg.StopSequences) > 0 || genCfg.ThinkingConfig != nil {
		geminiReq.GenerationConfig = genCfg
	}

	return codec.MarshalJSONNoEscape(geminiReq)
}

func ConvertGeminiResponseToResponses(originalRequestJSON []byte, upstreamResponseJSON []byte, toolContexts ...*ResponseToolContext) (*types.ResponsesResponse, error) {
	var geminiResp types.GeminiResponse
	if err := json.Unmarshal(upstreamResponseJSON, &geminiResp); err != nil {
		return nil, fmt.Errorf("解析 Gemini 响应失败: %w", err)
	}
	if len(geminiResp.Candidates) != 1 {
		return nil, fmt.Errorf("Gemini → Responses 需要恰好一个 candidate")
	}
	customTools := responseTools(originalRequestJSON, toolContexts...)

	resp := &types.ResponsesResponse{
		ID:     generateResponseID(),
		Object: "response",
		Status: "completed",
		Output: []types.ResponsesItem{},
	}
	if originalRequestJSON != nil {
		var original struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(originalRequestJSON, &original)
		resp.Model = original.Model
	}

	for _, candidate := range geminiResp.Candidates {
		if candidate.Content == nil {
			continue
		}
		candidateIndex := candidate.Index
		for idx, part := range candidate.Content.Parts {
			if part.Thought && part.Text != "" {
				resp.Output = append(resp.Output, types.ResponsesItem{
					ID:      fmt.Sprintf("rs_%s_%d_%d", resp.ID, candidateIndex, idx),
					Type:    "reasoning",
					Status:  "completed",
					Summary: []interface{}{map[string]interface{}{"type": "summary_text", "text": part.Text}},
				})
				continue
			}
			if part.Text != "" {
				resp.Output = append(resp.Output, types.ResponsesItem{
					ID:      fmt.Sprintf("msg_%s_%d_%d", resp.ID, candidateIndex, idx),
					Type:    "message",
					Status:  "completed",
					Role:    "assistant",
					Content: []interface{}{map[string]interface{}{"type": "output_text", "text": part.Text}},
				})
			}
			if part.FunctionCall != nil {
				args, _ := json.Marshal(part.FunctionCall.Args)
				callID := part.FunctionCall.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%d_%d", candidateIndex, idx)
				}
				item := types.ResponsesItem{
					ID:        fmt.Sprintf("fc_%s", callID),
					Type:      "function_call",
					Status:    "completed",
					CallID:    callID,
					Name:      part.FunctionCall.Name,
					Arguments: string(args),
				}
				item, err := restoreResponseToolItem(item, customTools)
				if err != nil {
					return nil, err
				}
				resp.Output = append(resp.Output, item)
			}
		}

	}

	completion, err := contract.Finish("gemini", geminiResp.Candidates[0].FinishReason)
	if err != nil {
		return nil, err
	}
	applyResponseCompletion(resp, completion)
	if geminiResp.UsageMetadata != nil {
		// thoughtsTokenCount 必须一起传进去：parseGeminiUsage 负责把它计入 output_tokens
		// 并单列 output_tokens_details.reasoning_tokens，此处不再重复赋值，避免两处口径分叉。
		resp.Usage = parseGeminiUsage(map[string]interface{}{
			"promptTokenCount":        geminiResp.UsageMetadata.PromptTokenCount,
			"candidatesTokenCount":    geminiResp.UsageMetadata.CandidatesTokenCount,
			"thoughtsTokenCount":      geminiResp.UsageMetadata.ThoughtsTokenCount,
			"totalTokenCount":         geminiResp.UsageMetadata.TotalTokenCount,
			"cachedContentTokenCount": geminiResp.UsageMetadata.CachedContentTokenCount,
		})
	}

	return resp, nil
}

func responsesToolsToGeminiTools(raw interface{}) ([]types.GeminiTool, error) {
	definitions, err := collectResponsesToolDefinitions(raw)
	if err != nil {
		return nil, err
	}
	if err := validateToolIdentities(definitions, "gemini"); err != nil {
		return nil, err
	}
	chatTools, err := responsesToolDefinitionsToOpenAIChatTools(definitions)
	if err != nil {
		return nil, err
	}
	declarations := make([]types.GeminiFunctionDeclaration, 0, len(chatTools))
	for _, tool := range chatTools {
		function := tool["function"].(map[string]interface{})
		name, _ := function["name"].(string)
		description, _ := function["description"].(string)
		declarations = append(declarations, types.GeminiFunctionDeclaration{Name: name, Description: description, ParametersJSONSchema: function["parameters"]})
	}
	if len(declarations) == 0 {
		return nil, nil
	}
	return []types.GeminiTool{{FunctionDeclarations: declarations}}, nil
}

func responsesToolChoiceToGemini(raw interface{}) (_ *types.GeminiToolConfig, err error) {
	defer func() {
		if err != nil && !types.IsUnsupportedProtocol(err) {
			err = &types.InvalidRequestError{Err: err}
		}
	}()
	if raw == nil {
		return nil, nil
	}

	cfg := &types.GeminiFunctionCallingConfig{}
	switch v := raw.(type) {
	case string:
		switch v {
		case "auto":
			cfg.Mode = "AUTO"
		case "required":
			cfg.Mode = "ANY"
		case "none":
			cfg.Mode = "NONE"
		default:
			return nil, fmt.Errorf("不支持的 Responses tool_choice: %s", v)
		}
	case map[string]interface{}:
		typ, _ := v["type"].(string)
		switch typ {
		case "", "function", "custom", "tool_search":
		case "auto":
			cfg.Mode = "AUTO"
			return &types.GeminiToolConfig{FunctionCallingConfig: cfg}, nil
		case "required":
			cfg.Mode = "ANY"
			return &types.GeminiToolConfig{FunctionCallingConfig: cfg}, nil
		case "none":
			cfg.Mode = "NONE"
			return &types.GeminiToolConfig{FunctionCallingConfig: cfg}, nil
		default:
			return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Gemini 上游暂不支持 tool_choice type %q", typ)}
		}
		name := extractNamedToolChoice(v)
		if typ == "tool_search" {
			name = openAIChatToolSearchName
		}
		if namespace, _ := v["namespace"].(string); namespace != "" {
			name = flattenNamespaceToolName(namespace, name)
		}
		if name == "" {
			return nil, fmt.Errorf("function tool_choice 缺少 name")
		}
		cfg.Mode = "ANY"
		cfg.AllowedFunctionNames = []string{name}
	default:
		return nil, fmt.Errorf("不支持的 tool_choice 类型: %T", raw)
	}

	return &types.GeminiToolConfig{FunctionCallingConfig: cfg}, nil
}

func ResponsesReasoningToGeminiThinking(model string, raw interface{}) (*types.GeminiThinkingConfig, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("reasoning 必须是对象")
	}
	effort, _ := m["effort"].(string)
	effort, err := NormalizeReasoningEffortForConstrainedUpstream(effort)
	if err != nil {
		return nil, fmt.Errorf("Gemini thinking %w", err)
	}
	if effort == "" {
		return nil, nil
	}
	modelLower := strings.ToLower(model)
	if strings.Contains(modelLower, "gemini-3") {
		return responsesReasoningToGemini3Thinking(modelLower, effort)
	}
	return responsesReasoningToGemini25Thinking(modelLower, effort)
}

func responsesReasoningToGemini3Thinking(modelLower string, effort string) (*types.GeminiThinkingConfig, error) {
	if effort == "none" {
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Gemini 3 不支持保证关闭 thinking，不能省略参数后按默认思考继续请求")}
	}

	level := effort
	switch effort {
	case "minimal", "low":
		level = "low"
	case "medium":
		if strings.Contains(modelLower, "pro") {
			level = "high"
		}
	case "high", "xhigh", "max":
		level = "high"
	case "auto":
		return &types.GeminiThinkingConfig{IncludeThoughts: true}, nil
	default:
		return nil, fmt.Errorf("不支持的 reasoning.effort: %s", effort)
	}

	return &types.GeminiThinkingConfig{IncludeThoughts: true, ThinkingLevel: level}, nil
}

func responsesReasoningToGemini25Thinking(modelLower string, effort string) (*types.GeminiThinkingConfig, error) {
	budget := int32(-1)
	switch effort {
	case "none":
		if strings.Contains(modelLower, "pro") {
			return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Gemini 2.5 Pro 不支持关闭 thinking")}
		}
		budget = 0
	case "auto":
		budget = -1
	case "minimal", "low":
		budget = 1024
	case "medium":
		budget = 4096
	case "high":
		budget = 8192
	case "xhigh", "max":
		budget = 16384
	default:
		return nil, fmt.Errorf("不支持的 reasoning.effort: %s", effort)
	}

	return &types.GeminiThinkingConfig{IncludeThoughts: effort != "none", ThinkingBudget: &budget}, nil
}

func responsesItemToGeminiContent(item types.ResponsesItem, callNames map[string]string) (types.GeminiContent, bool, error) {
	role := item.Role
	if role == "assistant" {
		role = "model"
	}
	if role == "" {
		role = "user"
	}
	content := types.GeminiContent{Role: role}
	switch item.Type {
	case "message", "text":
		parts, err := responsesContentToGeminiParts(item.Content)
		if err != nil {
			return types.GeminiContent{}, false, err
		}
		content.Parts = parts
	case "function_call", "custom_tool_call", "tool_search_call":
		var args map[string]interface{}
		if item.Type == "custom_tool_call" {
			input, ok := item.Content.(string)
			if !ok {
				return types.GeminiContent{}, false, contract.Invalid("custom_tool_call input 必须是字符串")
			}
			args = map[string]interface{}{"input": input}
		} else if item.Arguments != "" {
			if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil {
				return types.GeminiContent{}, false, fmt.Errorf("function_call arguments 不是合法 JSON: %w", err)
			}
		}
		if args == nil {
			args = map[string]interface{}{}
		}
		content.Role = "model"
		name := flattenNamespaceToolName(item.Namespace, item.Name)
		if item.Type == "tool_search_call" {
			name = openAIChatToolSearchName
		}
		content.Parts = []types.GeminiPart{{FunctionCall: &types.GeminiFunctionCall{ID: item.CallID, Name: name, Args: args}}}
	default:
		if !isResponsesToolOutputType(item.Type) {
			return types.GeminiContent{}, false, nil
		}
		name := callNames[item.CallID]
		if name == "" {
			name = item.Name
		}
		if name == "" {
			return types.GeminiContent{}, false, fmt.Errorf("function_call_output 缺少可映射的函数名: call_id=%s", item.CallID)
		}
		response := map[string]interface{}{"result": item.Content}
		content.Role = "user"
		content.Parts = []types.GeminiPart{{FunctionResponse: &types.GeminiFunctionResponse{ID: item.CallID, Name: name, Response: response}}}
	case "reasoning":
		return types.GeminiContent{}, false, nil
	}
	if len(content.Parts) == 0 {
		return types.GeminiContent{}, false, nil
	}
	return content, true, nil
}

func responsesContentToGeminiParts(content interface{}) ([]types.GeminiPart, error) {
	if text, ok := content.(string); ok && text != "" {
		return []types.GeminiPart{{Text: text}}, nil
	}
	blocks := visionmedia.NormalizeContentBlocks(content)
	parts := make([]types.GeminiPart, 0, len(blocks))
	for _, block := range blocks {
		if text, ok := visionmedia.ExtractTextFromBlock(block); ok {
			parts = append(parts, types.GeminiPart{Text: text})
			continue
		}
		part, ok := visionmedia.ToGeminiImagePart(block, true)
		if !ok {
			return nil, contract.Invalid("Gemini 图片内容缺少有效数据或 URL")
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func buildFunctionCallNameMap(items []types.ResponsesItem) map[string]string {
	names := map[string]string{}
	for _, item := range items {
		var name string
		switch item.Type {
		case "function_call", "custom_tool_call":
			name = flattenNamespaceToolName(item.Namespace, item.Name)
		case "tool_search_call":
			name = openAIChatToolSearchName
		default:
			continue
		}
		if item.CallID != "" {
			names[item.CallID] = name
		}
		if item.ID != "" {
			names[item.ID] = name
		}
	}
	return names
}

func stringSlice(raw interface{}) ([]string, error) {
	switch v := raw.(type) {
	case string:
		return []string{v}, nil
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("stop 数组必须只包含字符串")
			}
			out = append(out, s)
		}
		return out, nil
	case []string:
		return v, nil
	default:
		return nil, fmt.Errorf("不支持的 stop 类型: %T", raw)
	}
}

package converters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
)

func ConvertResponsesToGeminiRequest(model string, sess *types.Session, req *types.ResponsesRequest) ([]byte, error) {
	items, err := parseResponsesInput(req.Input)
	if err != nil {
		return nil, err
	}
	if sess != nil && len(sess.Messages) > 0 {
		items = utils.MergeResponsesItemsDedup(sess.Messages, items)
	}
	if utils.ResponsesItemsContainCompaction(items) {
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Responses compaction item 只能由 Responses 原生上游处理，无法转换为 Gemini")}
	}
	callNames := buildFunctionCallNameMap(items)

	geminiReq := types.GeminiRequest{
		Contents: make([]types.GeminiContent, 0, len(items)),
	}
	if req.Instructions != "" {
		geminiReq.SystemInstruction = &types.GeminiContent{Parts: []types.GeminiPart{{Text: req.Instructions}}}
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
	if tools, err := responsesToolsToGeminiTools(req.Tools); err != nil {
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
	if req.Temperature > 0 {
		v := req.Temperature
		genCfg.Temperature = &v
	}
	if req.TopP > 0 {
		v := req.TopP
		genCfg.TopP = &v
	}
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

	return utils.MarshalJSONNoEscape(geminiReq)
}

func ConvertGeminiResponseToResponses(originalRequestJSON []byte, upstreamResponseJSON []byte) (*types.ResponsesResponse, error) {
	var geminiResp types.GeminiResponse
	if err := json.Unmarshal(upstreamResponseJSON, &geminiResp); err != nil {
		return nil, fmt.Errorf("解析 Gemini 响应失败: %w", err)
	}
	customTools := extractCustomToolContextFromRawRequest(originalRequestJSON)

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
				callID := fmt.Sprintf("call_%d_%d", candidateIndex, idx)
				item := types.ResponsesItem{
					ID:        fmt.Sprintf("fc_%s", callID),
					Type:      "function_call",
					Status:    "completed",
					CallID:    callID,
					Name:      part.FunctionCall.Name,
					Arguments: string(args),
				}
				if customTools != nil {
					if spec, ok := customTools[part.FunctionCall.Name]; ok {
						item = types.ResponsesItem{
							ID:      fmt.Sprintf("ctc_%s", callID),
							Type:    "custom_tool_call",
							Status:  "completed",
							CallID:  callID,
							Name:    spec.OriginalName,
							Content: extractCustomToolInput(string(args)),
						}
					}
				}
				resp.Output = append(resp.Output, item)
			}
		}
		if candidate.FinishReason == "MAX_TOKENS" {
			resp.Status = "incomplete"
		}
	}

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

func responsesToolsToGeminiTools(raw interface{}) (_ []types.GeminiTool, err error) {
	defer func() {
		if err != nil && !types.IsUnsupportedProtocol(err) {
			err = &types.InvalidRequestError{Err: err}
		}
	}()
	items, err := interfaceSlice(raw)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	decls := make([]types.GeminiFunctionDeclaration, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("Responses tools 项必须是对象")
		}
		spec := normalizeResponsesToolDefinition(m)
		if spec.ToolType != "" && spec.ToolType != "function" && spec.ToolType != "custom" {
			return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Gemini 上游无法等价执行 Responses tool type %q；内置搜索需原生 Responses 渠道", spec.ToolType)}
		}
		if spec.Name == "" {
			return nil, fmt.Errorf("Responses function tool 缺少 name")
		}
		decls = append(decls, types.GeminiFunctionDeclaration{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  types.SanitizeGeminiToolSchema(spec.Parameters),
		})
	}
	return []types.GeminiTool{{FunctionDeclarations: decls}}, nil
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
		case "", "function", "custom":
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
		return nil, nil
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
			return nil, fmt.Errorf("Gemini 2.5 Pro 不支持关闭 thinking")
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
	case "function_call", "custom_tool_call":
		var args map[string]interface{}
		if item.Type == "custom_tool_call" {
			if err := json.Unmarshal([]byte(customToolArgumentsJSON(item)), &args); err != nil {
				return types.GeminiContent{}, false, fmt.Errorf("custom_tool_call input 不是合法 JSON: %w", err)
			}
		} else if item.Arguments != "" {
			if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil {
				return types.GeminiContent{}, false, fmt.Errorf("function_call arguments 不是合法 JSON: %w", err)
			}
		}
		if args == nil {
			args = map[string]interface{}{}
		}
		content.Role = "model"
		content.Parts = []types.GeminiPart{{FunctionCall: &types.GeminiFunctionCall{Name: item.Name, Args: args}}}
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
		content.Parts = []types.GeminiPart{{FunctionResponse: &types.GeminiFunctionResponse{Name: name, Response: response}}}
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
	blocks := utils.NormalizeContentBlocks(content)
	parts := make([]types.GeminiPart, 0, len(blocks))
	for _, block := range blocks {
		if text, ok := utils.ExtractTextFromBlock(block); ok {
			parts = append(parts, types.GeminiPart{Text: text})
			continue
		}
		if inline, ok := geminiInlineDataFromBlock(block); ok {
			parts = append(parts, types.GeminiPart{InlineData: inline})
			continue
		}
		if fileData, ok := geminiFileDataFromBlock(block); ok {
			parts = append(parts, types.GeminiPart{FileData: fileData})
		}
	}
	return parts, nil
}

func geminiInlineDataFromBlock(block map[string]interface{}) (*types.GeminiInlineData, bool) {
	typeVal, _ := block["type"].(string)
	if typeVal != "image" && typeVal != "image_url" && typeVal != "input_image" {
		return nil, false
	}
	if source, ok := block["source"].(map[string]interface{}); ok {
		if data, ok := source["data"].(string); ok && data != "" {
			mime, _ := source["media_type"].(string)
			if mime == "" {
				mime = utils.DefaultImageMediaType
			}
			return &types.GeminiInlineData{MimeType: mime, Data: data}, true
		}
	}
	if mime, data, ok := utils.ParseImageDataURL(imageURIFromBlock(block)); ok {
		return &types.GeminiInlineData{MimeType: mime, Data: data}, true
	}
	return nil, false
}

func geminiFileDataFromBlock(block map[string]interface{}) (*types.GeminiFileData, bool) {
	typeVal, _ := block["type"].(string)
	if typeVal != "image" && typeVal != "image_url" && typeVal != "input_image" {
		return nil, false
	}

	uri := imageURIFromBlock(block)
	mime := utils.DefaultImageMediaType
	if source, ok := block["source"].(map[string]interface{}); ok {
		if mediaType, _ := source["media_type"].(string); mediaType != "" {
			mime = mediaType
		}
	}
	if uri == "" || strings.HasPrefix(uri, "data:") {
		return nil, false
	}
	return &types.GeminiFileData{MimeType: mime, FileURI: uri}, true
}

func imageURIFromBlock(block map[string]interface{}) string {
	if source, ok := block["source"].(map[string]interface{}); ok {
		sourceType, _ := source["type"].(string)
		if sourceType == "url" {
			if uri, _ := source["url"].(string); uri != "" {
				return uri
			}
		}
	}
	if nested, ok := block["image_url"].(map[string]interface{}); ok {
		if uri, _ := nested["url"].(string); uri != "" {
			return uri
		}
	}
	if uri, ok := block["image_url"].(string); ok && uri != "" {
		return uri
	}
	if uri, ok := block["url"].(string); ok && uri != "" {
		return uri
	}
	return ""
}

func buildFunctionCallNameMap(items []types.ResponsesItem) map[string]string {
	names := map[string]string{}
	for _, item := range items {
		if (item.Type != "function_call" && item.Type != "custom_tool_call") || item.Name == "" {
			continue
		}
		if item.CallID != "" {
			names[item.CallID] = item.Name
		}
		if item.ID != "" {
			names[item.ID] = item.Name
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

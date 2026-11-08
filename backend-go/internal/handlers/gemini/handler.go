// Package gemini 提供 Gemini API 的处理器
package gemini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/converters"
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/handlers/proxycore"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

// Handler Gemini API 代理处理器
// 支持多渠道调度：当配置多个渠道时自动启用
// Handler Gemini API 代理处理器。
// 使用通用 RunProxyRequest 骨架，通过 ProtocolSpec 注入协议特有逻辑。
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	contentSafetyPipelines ...*hooks.Pipeline,
) gin.HandlerFunc {
	contentSafetyPipeline := hooks.ResolveContentSafetyPipeline(cfgManager, contentSafetyPipelines...)
	spec := proxycore.ProtocolSpec{
		Kind:         scheduler.ChannelKindGemini,
		LogName:      "Gemini",
		HookPipeline: contentSafetyPipeline,
		ParseRequest: func(c *gin.Context, body []byte) (string, bool, []string, bool) {
			var geminiReq types.GeminiRequest
			if len(body) > 0 {
				if err := json.Unmarshal(body, &geminiReq); err != nil {
					c.JSON(400, types.GeminiError{
						Error: types.GeminiErrorDetail{
							Code:    400,
							Message: fmt.Sprintf("Invalid request body: %v", err),
							Status:  "INVALID_ARGUMENT",
						},
					})
					return "", false, nil, false
				}
			}
			c.Set("__gemini_req", &geminiReq)
			modelAction := c.Param("modelAction")
			modelAction = strings.TrimPrefix(modelAction, "/")
			model := extractModelName(modelAction)
			if model == "" {
				c.JSON(400, types.GeminiError{
					Error: types.GeminiErrorDetail{
						Code:    400,
						Message: "Model name is required in URL path",
						Status:  "INVALID_ARGUMENT",
					},
				})
				return "", false, nil, false
			}
			isStream := strings.Contains(c.Request.URL.Path, "streamGenerateContent")
			prompts := proxycore.ExtractPromptsFromGemini(geminiReq.Contents)
			return model, isStream, prompts, true
		},
		PreRoute: nil,
		BuildUpstreamRequest: func(c *gin.Context, up *config.UpstreamConfig, apiKey string, body []byte) (*http.Request, error) {
			geminiReq := c.MustGet("__gemini_req").(*types.GeminiRequest)
			modelAction := strings.TrimPrefix(c.Param("modelAction"), "/")
			model := extractModelName(modelAction)
			isStream := strings.Contains(c.Request.URL.Path, "streamGenerateContent")
			return buildProviderRequest(c, up, up.GetEffectiveBaseURL(), apiKey, geminiReq, model, isStream)
		},
		HandleSuccess: func(c *gin.Context, resp *http.Response, up *config.UpstreamConfig, apiKey string, body []byte, startTime time.Time) (*types.Usage, error) {
			geminiReq := c.MustGet("__gemini_req").(*types.GeminiRequest)
			modelAction := strings.TrimPrefix(c.Param("modelAction"), "/")
			model := extractModelName(modelAction)
			isStream := strings.Contains(c.Request.URL.Path, "streamGenerateContent")
			return handleSuccess(c, resp, up.ServiceType, envCfg, startTime, geminiReq, model, isStream)
		},
		HandleAllFailed: func(c *gin.Context, failoverErr *proxycore.FailoverError, lastError error) {
			if failoverErr != nil {
				body, err := hooks.RestoreAttachedResponseBody(c, failoverErr.Body)
				if err != nil {
					c.JSON(http.StatusInternalServerError, types.GeminiError{Error: types.GeminiErrorDetail{Code: 500, Message: "Sensitive data restoration failed", Status: "INTERNAL"}})
					return
				}
				c.JSON(failoverErr.Status, types.GeminiError{
					Error: types.GeminiErrorDetail{
						Code:    failoverErr.Status,
						Message: string(body),
						Status:  "UNAVAILABLE",
					},
				})
				return
			}
			c.JSON(503, types.GeminiError{
				Error: types.GeminiErrorDetail{
					Code:    503,
					Message: "All channels failed",
					Status:  "UNAVAILABLE",
				},
			})
		},
		HandleAllKeysFailed: func(c *gin.Context, fuzzyMode bool, failoverErr *proxycore.FailoverError, lastError error) {
			if failoverErr != nil {
				body, err := hooks.RestoreAttachedResponseBody(c, failoverErr.Body)
				if err != nil {
					c.JSON(http.StatusInternalServerError, types.GeminiError{Error: types.GeminiErrorDetail{Code: 500, Message: "Sensitive data restoration failed", Status: "INTERNAL"}})
					return
				}
				c.JSON(failoverErr.Status, types.GeminiError{
					Error: types.GeminiErrorDetail{
						Code:    failoverErr.Status,
						Message: string(body),
						Status:  "UNAVAILABLE",
					},
				})
				return
			}
			c.JSON(503, types.GeminiError{
				Error: types.GeminiErrorDetail{
					Code:    503,
					Message: "All keys failed",
					Status:  "UNAVAILABLE",
				},
			})
		},
	}
	return func(c *gin.Context) {
		proxycore.RunProxyRequest(c, envCfg, cfgManager, channelScheduler, spec)
	}
}

// extractModelName 从 URL 参数提取模型名称
// 输入: "gemini-2.0-flash:generateContent" 或 "gemini-2.0-flash"
// 输出: "gemini-2.0-flash"
func extractModelName(param string) string {
	if param == "" {
		return ""
	}
	// 移除 :generateContent 或 :streamGenerateContent 后缀
	if idx := strings.Index(param, ":"); idx > 0 {
		return param[:idx]
	}
	return param
}

// handleMultiChannel 处理多渠道 Gemini 请求
func ensureThoughtSignatures(geminiReq *types.GeminiRequest) {
	for i := range geminiReq.Contents {
		for j := range geminiReq.Contents[i].Parts {
			part := &geminiReq.Contents[i].Parts[j]
			if part.FunctionCall != nil && part.FunctionCall.ThoughtSignature == "" {
				part.FunctionCall.ThoughtSignature = types.DummyThoughtSignature
			}
		}
	}
}

// stripThoughtSignature 移除所有 functionCall 的 thought_signature 字段
// 用于兼容旧版 Gemini API（不支持该字段）
func stripThoughtSignature(geminiReq *types.GeminiRequest) {
	for i := range geminiReq.Contents {
		for j := range geminiReq.Contents[i].Parts {
			part := &geminiReq.Contents[i].Parts[j]
			if part.FunctionCall != nil {
				// 使用特殊标记表示需要完全移除字段
				part.FunctionCall.ThoughtSignature = types.StripThoughtSignatureMarker
			}
		}
	}
}

// cloneGeminiRequest 深拷贝 GeminiRequest（通过 JSON 序列化/反序列化）
func cloneGeminiRequest(req *types.GeminiRequest) *types.GeminiRequest {
	clone := &types.GeminiRequest{}
	data, _ := json.Marshal(req)
	json.Unmarshal(data, clone)
	return clone
}

// buildProviderRequest 构建上游请求
func buildProviderRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	geminiReq *types.GeminiRequest,
	model string,
	isStream bool,
) (*http.Request, error) {
	// 应用模型映射
	mappedModel := config.ResolveUpstreamModel(model, upstream)

	var requestBody []byte
	var url string
	var err error

	switch upstream.ServiceType {
	case "gemini":
		// Gemini 上游：根据配置处理 thought_signature 字段
		reqToUse := geminiReq

		// 优先处理 StripThoughtSignature（移除字段）
		if upstream.StripThoughtSignature {
			reqCopy := cloneGeminiRequest(geminiReq)
			stripThoughtSignature(reqCopy)
			reqToUse = reqCopy
		} else if upstream.InjectDummyThoughtSignature {
			// 给空签名注入 dummy 值（兼容 x666.me 等要求必须有该字段的 API）
			reqCopy := cloneGeminiRequest(geminiReq)
			ensureThoughtSignatures(reqCopy)
			reqToUse = reqCopy
		}
		// else: 默认直接透传，不做任何修改

		requestBody, err = json.Marshal(reqToUse)
		if err != nil {
			return nil, err
		}

		action := "generateContent"
		if isStream {
			action = "streamGenerateContent"
		}
		url = fmt.Sprintf("%s/v1beta/models/%s:%s", strings.TrimRight(baseURL, "/"), mappedModel, action)
		if isStream {
			url += "?alt=sse"
		}

	case "claude":
		// Claude 上游：需要转换
		claudeReq, err := converters.GeminiToClaudeRequest(geminiReq, mappedModel)
		if err != nil {
			return nil, err
		}
		claudeReq["stream"] = isStream
		requestBody, err = json.Marshal(claudeReq)
		if err != nil {
			return nil, err
		}
		url = fmt.Sprintf("%s/v1/messages", strings.TrimRight(baseURL, "/"))

	case "openai":
		// OpenAI 上游：需要转换
		openaiReq, err := converters.GeminiToOpenAIRequest(geminiReq, mappedModel)
		if err != nil {
			return nil, err
		}
		openaiReq["stream"] = isStream
		requestBody, err = json.Marshal(openaiReq)
		if err != nil {
			return nil, err
		}
		url = fmt.Sprintf("%s/v1/chat/completions", strings.TrimRight(baseURL, "/"))

	default:
		// 默认当作 Gemini 处理，根据配置处理 thought_signature 字段
		reqToUse := geminiReq

		// 优先处理 StripThoughtSignature（移除字段）
		if upstream.StripThoughtSignature {
			reqCopy := cloneGeminiRequest(geminiReq)
			stripThoughtSignature(reqCopy)
			reqToUse = reqCopy
		} else if upstream.InjectDummyThoughtSignature {
			// 给空签名注入 dummy 值（兼容 x666.me 等要求必须有该字段的 API）
			reqCopy := cloneGeminiRequest(geminiReq)
			ensureThoughtSignatures(reqCopy)
			reqToUse = reqCopy
		}
		// else: 默认直接透传，不做任何修改

		requestBody, err = json.Marshal(reqToUse)
		if err != nil {
			return nil, err
		}
		action := "generateContent"
		if isStream {
			action = "streamGenerateContent"
		}
		url = fmt.Sprintf("%s/v1beta/models/%s:%s", strings.TrimRight(baseURL, "/"), mappedModel, action)
		if isStream {
			url += "?alt=sse"
		}
	}

	if upstream.UseFullURL {
		url = baseURL
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	// 使用统一的头部处理逻辑（透明代理）
	// 保留客户端的大部分 headers，只移除/替换必要的认证和代理相关 headers
	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)

	// 设置 Content-Type（覆盖可能来自客户端的值）
	req.Header.Set("Content-Type", "application/json")

	// 设置认证头
	switch upstream.ServiceType {
	case "gemini":
		utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
	case "claude":
		utils.SetAuthenticationHeader(req.Header, apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "openai":
		utils.SetAuthenticationHeader(req.Header, apiKey)
	default:
		utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
	}

	return req, nil
}

// handleSuccess 处理成功的响应
func handleSuccess(
	c *gin.Context,
	resp *http.Response,
	upstreamType string,
	envCfg *config.EnvConfig,
	startTime time.Time,
	geminiReq *types.GeminiRequest,
	model string,
	isStream bool,
) (*types.Usage, error) {
	defer resp.Body.Close()

	if isStream {
		return handleStreamSuccess(c, resp, upstreamType, envCfg, startTime, model)
	}

	// 非流式响应处理
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(500, types.GeminiError{
			Error: types.GeminiErrorDetail{
				Code:    500,
				Message: "Failed to read response",
				Status:  "INTERNAL",
			},
		})
		return nil, err
	}

	decompressedBytes, wasDecoded, decompressErr := utils.DecompressResponseBodyIfNeeded(resp, bodyBytes, envCfg.MaxRequestBodySize)
	if decompressErr != nil && errors.Is(decompressErr, utils.ErrDecompressedBodyTooLarge) {
		log.Printf("[Gemini-Response] 错误: 解压响应体超过大小限制: %v", decompressErr)
		c.JSON(http.StatusBadGateway, types.GeminiError{
			Error: types.GeminiErrorDetail{
				Code:    502,
				Message: "Decompressed response body too large",
				Status:  "UNAVAILABLE",
			},
		})
		return nil, decompressErr
	}
	if wasDecoded {
		bodyBytes = decompressedBytes
		utils.StripEntityHeadersForRebuiltBody(resp.Header)
		resp.Header.Del("Content-Encoding")
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Gemini-Timing] 响应完成: %dms, 状态: %d", responseTime, resp.StatusCode)
	}
	writeResponse := func(body []byte) error {
		checkedBody, err := hooks.RunAttachedPostResponseHooks(c.Request.Context(), c, body, resp)
		if err != nil {
			return err
		}
		proxycore.MarkRequestLogFirstToken(c)
		c.Data(resp.StatusCode, "application/json", checkedBody)
		return nil
	}

	// 根据上游类型转换响应
	var geminiResp *types.GeminiResponse

	switch upstreamType {
	case "gemini":
		// 直接解析 Gemini 响应
		if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
			return nil, writeResponse(bodyBytes)
		}

	case "claude":
		// 转换 Claude 响应为 Gemini 格式
		var claudeResp map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &claudeResp); err != nil {
			return nil, writeResponse(bodyBytes)
		}
		geminiResp, err = converters.ClaudeResponseToGemini(claudeResp)
		if err != nil {
			return nil, writeResponse(bodyBytes)
		}

	case "openai":
		// 转换 OpenAI 响应为 Gemini 格式
		var openaiResp map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &openaiResp); err != nil {
			return nil, writeResponse(bodyBytes)
		}
		geminiResp, err = converters.OpenAIResponseToGemini(openaiResp)
		if err != nil {
			return nil, writeResponse(bodyBytes)
		}

	default:
		// 默认直接返回
		return nil, writeResponse(bodyBytes)
	}

	// 返回 Gemini 格式响应
	respBytes, err := json.Marshal(geminiResp)
	if err != nil {
		return nil, writeResponse(bodyBytes)
	}

	if err := writeResponse(respBytes); err != nil {
		return nil, err
	}

	// 提取 usage 统计
	var usage *types.Usage
	if geminiResp.UsageMetadata != nil {
		// promptTokenCount 含 cachedContentTokenCount，无条件扣除。
		// 两种来源都成立，不需要分支：
		//   原生 Gemini 上游：promptTokenCount 本就含缓存，扣掉即得新增输入；
		//   协议转换（如 claude 上游经 ClaudeResponseToGemini）：转换器先把 cacheRead
		//   加回 PromptTokenCount 并填 CachedContentTokenCount，这里再减回来，净结果一致。
		actualInputTokens := geminiResp.UsageMetadata.PromptTokenCount - geminiResp.UsageMetadata.CachedContentTokenCount
		if actualInputTokens < 0 {
			actualInputTokens = 0
		}
		// candidatesTokenCount 不含 thoughtsTokenCount，两者相加才是完整输出。指标必须按
		// 完整输出记，否则 Gemini 渠道的用量统计比其它渠道系统性偏低、跨渠道不可比
		// （三家 output 侧语义与官方证据见 types.ClaudeOutputTokensDetails）。
		// 注意这里只改指标口径：透传给客户端的 Gemini 原生响应体保持原生语义不动，
		// 原生客户端按 candidatesTokenCount + thoughtsTokenCount 自行求和。
		thoughtsTokens := geminiResp.UsageMetadata.ThoughtsTokenCount
		usage = &types.Usage{
			InputTokens:          actualInputTokens,
			OutputTokens:         geminiResp.UsageMetadata.CandidatesTokenCount + thoughtsTokens,
			CacheReadInputTokens: geminiResp.UsageMetadata.CachedContentTokenCount,
		}
		if thoughtsTokens > 0 {
			usage.OutputTokensDetails = &types.ClaudeOutputTokensDetails{ThinkingTokens: thoughtsTokens}
		}
	}

	return usage, nil
}

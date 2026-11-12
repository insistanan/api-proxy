// Package embeddings 提供 OpenAI Embeddings API 的原样透传代理。
package embeddings

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

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/handlers/proxycore"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// Handler Embeddings API 代理处理器。
// Embeddings 是独立一等公民：只走 Embeddings 渠道池，请求原样透传到 {baseUrl}/v1/embeddings。
// 不接入内容安全管道（不做敏感数据检测与脱敏），仅负责鉴权、模型映射、调度与故障转移。
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
) gin.HandlerFunc {
	spec := proxycore.ProtocolSpec{
		Kind:    scheduler.ChannelKindEmbeddings,
		LogName: "Embeddings",
		ParseRequest: func(c *gin.Context, body []byte) (string, bool, []string, bool) {
			if len(body) == 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Embeddings request body"})
				return "", false, nil, false
			}
			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Embeddings request body", "detail": err.Error()})
				return "", false, nil, false
			}
			model, _ := payload["model"].(string)
			if strings.TrimSpace(model) == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
				return "", false, nil, false
			}
			return strings.TrimSpace(model), false, extractEmbeddingPrompts(payload["input"]), true
		},
		BuildUpstreamRequest: buildUpstreamRequest,
		HandleSuccess: func(c *gin.Context, resp *http.Response, up *config.UpstreamConfig, apiKey string, body []byte, startTime time.Time) (*types.Usage, error) {
			return handleSuccess(c, resp, envCfg, startTime, body)
		},
	}
	return func(c *gin.Context) {
		proxycore.RunProxyRequest(c, envCfg, cfgManager, channelScheduler, spec)
	}
}

func buildUpstreamRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string, originalBody []byte) (*http.Request, error) {
	if err := config.ValidateEmbeddingsUpstream(upstream); err != nil {
		return nil, err
	}
	bodyBytes, err := rewriteEmbeddingsPayload(originalBody, upstream)
	if err != nil {
		return nil, err
	}

	url := utils.BuildUpstreamURL(upstream.GetEffectiveBaseURL(), "/v1", "/embeddings", upstream.UseFullURL)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, &config.ConfigError{Message: fmt.Sprintf("创建 Embeddings 请求失败: %s", err)}
	}

	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
	req.Header.Set("Content-Type", "application/json")
	utils.SetAuthenticationHeader(req.Header, apiKey)
	return req, nil
}

// rewriteEmbeddingsPayload 只改写渠道明确配置的字段：映射模型名；渠道设置了输出维度时覆盖 dimensions。
// 其余字段原样保留。维度为零时不改写 dimensions，由客户端请求决定。
func rewriteEmbeddingsPayload(bodyBytes []byte, upstream *config.UpstreamConfig) ([]byte, error) {
	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, &proxycore.RequestValidationError{Err: fmt.Errorf("解析 Embeddings 请求体失败: %w", err)}
	}
	model, ok := payload["model"].(string)
	if !ok || strings.TrimSpace(model) == "" {
		return nil, &proxycore.RequestValidationError{Err: fmt.Errorf("model is required")}
	}
	if mapped := config.ResolveUpstreamModel(model, upstream); strings.TrimSpace(mapped) != "" {
		payload["model"] = mapped
	}
	if upstream.EmbeddingsDimensions > 0 {
		payload["dimensions"] = upstream.EmbeddingsDimensions
	}
	bodyBytes, err := utils.MarshalJSONNoEscape(payload)
	if err != nil {
		return nil, &proxycore.RequestValidationError{Err: err}
	}
	return bodyBytes, nil
}

func extractEmbeddingPrompts(input interface{}) []string {
	switch typed := input.(type) {
	case string:
		return proxycore.NormalizePromptTexts(typed)
	case []interface{}:
		prompts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				prompts = append(prompts, proxycore.NormalizePromptTexts(text)...)
			}
		}
		return prompts
	default:
		return nil
	}
}

func handleSuccess(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, startTime time.Time, originalBody []byte) (*types.Usage, error) {
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return nil, err
	}
	decompressedBytes, wasDecoded, decompressErr := utils.DecompressResponseBodyIfNeeded(resp, bodyBytes, envCfg.Current().MaxRequestBodySize)
	if decompressErr != nil && errors.Is(decompressErr, utils.ErrDecompressedBodyTooLarge) {
		log.Printf("[Embeddings-Response] 错误: 解压响应体超过大小限制: %v", decompressErr)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Decompressed response body too large"})
		return nil, decompressErr
	}
	if wasDecoded {
		bodyBytes = decompressedBytes
		utils.StripEntityHeadersForRebuiltBody(resp.Header)
		resp.Header.Del("Content-Encoding")
	}

	if envCfg.Current().EnableResponseLogs {
		log.Printf("[Embeddings-Timing] Embeddings 响应完成: %dms, 状态: %d", time.Since(startTime).Milliseconds(), resp.StatusCode)
		proxycore.LogResponseDetails(resp, bodyBytes, envCfg, "Embeddings")
	}

	usage := extractUsage(bodyBytes)
	if usage == nil {
		usage = &types.Usage{InputTokens: utils.EstimateTokens(string(originalBody))}
	}
	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	proxycore.MarkRequestLogFirstToken(c)
	c.Data(resp.StatusCode, "application/json", bodyBytes)
	return usage, nil
}

func extractUsage(bodyBytes []byte) *types.Usage {
	var envelope struct {
		Usage *types.Usage `json:"usage"`
	}
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil || envelope.Usage == nil {
		return nil
	}
	usage := envelope.Usage
	if usage.InputTokens == 0 && usage.PromptTokens > 0 {
		usage.InputTokens = usage.PromptTokens
	}
	if usage.OutputTokens == 0 && usage.CompletionTokens > 0 {
		usage.OutputTokens = usage.CompletionTokens
	}
	return usage
}

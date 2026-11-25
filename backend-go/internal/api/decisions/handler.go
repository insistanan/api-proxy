// Package decisions 提供 Decisions API 的原样透传代理。
package decisions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/insistanan/api-proxy/internal/infra/upstreamreq"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/proxycore"
)

// Handler Decisions API 代理处理器。
// Decisions 是独立一等公民：只走 Decisions 渠道池，请求原样透传到 {baseUrl}/v1/decisions。
// 不接入内容安全管道（不做敏感数据检测与脱敏），仅负责鉴权、模型映射、调度与故障转移。
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
) gin.HandlerFunc {
	spec := proxycore.ProtocolSpec{
		Kind:    scheduler.ChannelKindDecisions,
		LogName: "Decisions",
		ParseRequest: func(c *gin.Context, body []byte) (string, bool, []string, bool) {
			if len(body) == 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Decisions request body"})
				return "", false, nil, false
			}
			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Decisions request body", "detail": err.Error()})
				return "", false, nil, false
			}
			model, _ := payload["model"].(string)
			if strings.TrimSpace(model) == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
				return "", false, nil, false
			}
			return strings.TrimSpace(model), false, extractDecisionPrompts(payload), true
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
	if err := config.ValidateDecisionsUpstream(upstream); err != nil {
		return nil, err
	}
	bodyBytes, err := applyModelMapping(originalBody, upstream)
	if err != nil {
		return nil, err
	}

	url := upstreamreq.BuildUpstreamURL(upstream.GetEffectiveBaseURL(), "/v1", "/decisions", upstream.UseFullURL)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, &config.ConfigError{Message: fmt.Sprintf("创建 Decisions 请求失败: %s", err)}
	}

	req.Header = upstreamreq.PrepareUpstreamHeaders(c, req.URL.Host)
	req.Header.Set("Content-Type", "application/json")
	upstreamreq.SetAuthenticationHeader(req.Header, apiKey)
	return req, nil
}

func applyModelMapping(bodyBytes []byte, upstream *config.UpstreamConfig) ([]byte, error) {
	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, &proxycore.RequestValidationError{Err: fmt.Errorf("解析 Decisions 请求体失败: %w", err)}
	}
	model, ok := payload["model"].(string)
	if !ok || strings.TrimSpace(model) == "" {
		return nil, &proxycore.RequestValidationError{Err: fmt.Errorf("model is required")}
	}
	if mapped := config.ResolveUpstreamModel(model, upstream); strings.TrimSpace(mapped) != "" {
		payload["model"] = mapped
	}
	bodyBytes, err := codec.MarshalJSONNoEscape(payload)
	if err != nil {
		return nil, &proxycore.RequestValidationError{Err: err}
	}
	return bodyBytes, nil
}

// extractDecisionPrompts 把 state 与 questions 中的文本抽取为会话观测用的 prompt。
// 只做文本收集，不改变透传给上游的请求体。
func extractDecisionPrompts(payload map[string]interface{}) []string {
	var prompts []string
	collectStrings(payload["state"], &prompts)
	questions, _ := payload["questions"].(map[string]interface{})
	keys := make([]string, 0, len(questions))
	for key := range questions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		question, _ := questions[key].(map[string]interface{})
		if question == nil {
			continue
		}
		collectStrings(question["instructions"], &prompts)
		collectStrings(question["criteria"], &prompts)
	}
	return proxycore.NormalizePromptTexts(strings.Join(prompts, "\n"))
}

func collectStrings(value interface{}, out *[]string) {
	switch typed := value.(type) {
	case string:
		if text := strings.TrimSpace(typed); text != "" {
			*out = append(*out, text)
		}
	case []interface{}:
		for _, item := range typed {
			collectStrings(item, out)
		}
	case map[string]interface{}:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			collectStrings(typed[key], out)
		}
	}
}

func handleSuccess(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, startTime time.Time, originalBody []byte) (*types.Usage, error) {
	envCfg = proxycore.RequestLogEnv(c, envCfg)
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return nil, err
	}
	decompressedBytes, wasDecoded, decompressErr := codec.DecompressResponseBodyIfNeeded(resp, bodyBytes, envCfg.Current().MaxRequestBodySize)
	if decompressErr != nil && errors.Is(decompressErr, codec.ErrDecompressedBodyTooLarge) {
		log.Printf("[Decisions-Response] 错误: 解压响应体超过大小限制: %v", decompressErr)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Decompressed response body too large"})
		return nil, decompressErr
	}
	if wasDecoded {
		bodyBytes = decompressedBytes
		upstreamreq.StripEntityHeadersForRebuiltBody(resp.Header)
		resp.Header.Del("Content-Encoding")
	}

	if envCfg.Current().EnableResponseLogs {
		log.Printf("[Decisions-Timing] Decisions 响应完成: %dms, 状态: %d", time.Since(startTime).Milliseconds(), resp.StatusCode)
		proxycore.LogResponseDetails(resp, bodyBytes, envCfg, "Decisions")
	}

	usage := tokenusage.ExtractJSONUsage(bodyBytes)
	if usage == nil {
		usage = &types.Usage{InputTokens: tokenusage.EstimateTokens(string(originalBody)), Estimated: true}
	}
	upstreamreq.ForwardResponseHeaders(resp.Header, c.Writer)
	proxycore.MarkRequestLogFirstToken(c)
	c.Data(resp.StatusCode, "application/json", bodyBytes)
	return usage, nil
}

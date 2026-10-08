// Package responses 提供 Responses API 的处理器
package responses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/converters"
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/handlers/proxycore"
	"github.com/BenedictKing/api-proxy/internal/middleware"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/session"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// compactError 封装 compact 请求错误
type compactError struct {
	status          int
	body            []byte
	shouldFailover  bool
	responseWritten bool
}

// CompactHandler Responses API compact 端点处理器
// POST /v1/responses/compact - 压缩对话上下文，用于长期代理工作流
func CompactHandler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager,
	channelScheduler *scheduler.ChannelScheduler,
	contentSafetyPipelines ...*hooks.Pipeline,
) gin.HandlerFunc {
	contentSafetyPipeline := hooks.ResolveContentSafetyPipeline(cfgManager, contentSafetyPipelines...)
	return gin.HandlerFunc(func(c *gin.Context) {
		// 认证
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}
		c.Set(utils.ContextKeyCodexDisguise, cfgManager.GetCodexDisguiseEnabled())

		// 读取请求体
		maxBodySize := envCfg.MaxRequestBodySize
		bodyBytes, err := proxycore.ReadRequestBody(c, maxBodySize)
		if err != nil {
			return
		}
		requestID := proxycore.BindRequestLogID(c)

		// compact 是控制面操作：只复用主请求已经建立的内部会话，不创建新的会话记录。
		// 这样不会改变客户端协议，同时避免外部 conversation ID 与内部记录 ID 分裂。
		identity := proxycore.ResolveConversationIdentity(c, bodyBytes)
		conversationID := proxycore.ResolveExistingConversationID(
			channelScheduler, scheduler.ChannelKindResponses, identity)
		model := compactRequestModel(bodyBytes)
		hooks.AttachHookPipeline(c, contentSafetyPipeline, hooks.HookContext{
			APIType:        string(scheduler.ChannelKindResponses),
			Model:          model,
			Stream:         false,
			RequestID:      requestID,
			ConversationID: conversationID,
		})
		defer hooks.ClearAttachedSensitiveData(c)
		if err := hooks.AssignAttachedConversation(c.Request.Context(), c, conversationID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "关联内容安全记录会话失败", "code": "CONTENT_SAFETY_LOG_ERROR"})
			return
		}

		// 检查是否为多渠道模式
		isMultiChannel := channelScheduler.IsMultiChannelModeForModel(scheduler.ChannelKindResponses, model)

		if isMultiChannel {
			handleMultiChannelCompact(c, envCfg, cfgManager, sessionManager, channelScheduler, bodyBytes, conversationID, model)
		} else {
			handleSingleChannelCompact(c, envCfg, cfgManager, sessionManager, channelScheduler, bodyBytes, conversationID, model)
		}
	})
}

// handleSingleChannelCompact 单渠道 compact 请求（带 key 轮转）
func handleSingleChannelCompact(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	conversationID string,
	model string,
) {
	upstream, _, err := cfgManager.GetCurrentResponsesUpstreamWithIndexForModel(model)
	if err != nil {
		c.JSON(503, gin.H{"error": "未配置任何 Responses 渠道"})
		return
	}

	if len(upstream.APIKeys) == 0 {
		c.JSON(503, gin.H{"error": "当前渠道未配置 API 密钥"})
		return
	}

	// Key 轮转：尝试所有可用 key
	failedKeys := make(map[string]bool)
	var lastErr *compactError

	for attempt := 0; attempt < len(upstream.APIKeys); attempt++ {
		apiKey, err := cfgManager.GetNextResponsesAPIKey(upstream, failedKeys)
		if err != nil {
			break
		}

		success, compactErr := tryCompactWithKey(c, upstream, apiKey, bodyBytes, envCfg, cfgManager, sessionManager, channelScheduler, conversationID)
		if success {
			return
		}

		if compactErr != nil {
			if compactErr.responseWritten {
				return
			}
			lastErr = compactErr
			if compactErr.shouldFailover {
				failedKeys[apiKey] = true
				cfgManager.MarkKeyAsFailed(apiKey, "Responses")
				continue
			}
			// 非故障转移错误，直接返回
			writeCompactResponseBody(c, compactErr.status, compactErr.body)
			return
		}
	}

	// 所有 key 都失败
	if cfgManager.GetFuzzyModeEnabled() {
		c.JSON(503, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "service_unavailable",
				"message": "All upstream channels are currently unavailable",
			},
		})
		return
	}

	if lastErr != nil {
		writeCompactResponseBody(c, lastErr.status, lastErr.body)
	} else {
		c.JSON(503, gin.H{"error": "所有 API 密钥都不可用"})
	}
}

// handleMultiChannelCompact 多渠道 compact 请求（带故障转移和亲和性）
func handleMultiChannelCompact(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	conversationID string,
	model string,
) {
	failedChannels := make(map[int]bool)
	maxAttempts := channelScheduler.GetActiveChannelCountForModel(scheduler.ChannelKindResponses, model)
	var lastErr *compactError

	for attempt := 0; attempt < maxAttempts; attempt++ {
		selection, err := channelScheduler.SelectChannel(c.Request.Context(), conversationID, failedChannels, scheduler.ChannelKindResponses, model)
		if err != nil {
			break
		}

		upstream := selection.Upstream
		channelIndex := selection.ChannelIndex
		releaseReservation := func() {
			if selection != nil && selection.Permit != nil {
				selection.Permit.ReleaseReservation()
			}
		}

		// 每个渠道尝试所有 key
		success, successKey, compactErr := tryCompactChannelWithAllKeys(c, upstream, channelIndex, cfgManager, sessionManager, channelScheduler, bodyBytes, conversationID, envCfg)

		// 渠道结束：经许可句柄记账（成功/失败/取消中性，恰好一次）并释放
		// 剩余资源（R2-b）。compact 失败原因无法区分审核/容量等中性类，
		// 按 compactError 的 shouldFailover 分类：可故障转移→失败，其余→中性。
		finalizeCompactPermit := func(healthSuccess bool) {
			if selection.Permit == nil {
				return
			}
			if healthSuccess {
				selection.Permit.RecordHealthSuccess()
			} else if compactErr != nil && compactErr.shouldFailover {
				selection.Permit.RecordHealthFailure()
			} else {
				selection.Permit.RecordHealthNeutral()
			}
			selection.Permit.Release()
			selection.Permit = nil
		}

		if success {
			finalizeCompactPermit(true)
			releaseReservation()
			// compact 不产生 usage，但仍需记录成功以更新熔断器/权重
			if successKey != "" {
				channelScheduler.RecordSuccessWithUsage(upstream.BaseURL, successKey, channelIndex, nil, scheduler.ChannelKindResponses)
				// compact 属同一对话，成功也要建立/续期对话级粘滞（与主链路一致）
				channelScheduler.MarkConversationSuccess(conversationID, scheduler.ChannelKindResponses, channelIndex, upstream.Name)
				// 只有真正成功的请求才设置会话级 Trace 亲和
				channelScheduler.SetTraceAffinityForKind(scheduler.ChannelKindResponses, conversationID, channelIndex)
				channelScheduler.ConsumePromotionCount(channelIndex, scheduler.ChannelKindResponses)
			}
			return
		}

		finalizeCompactPermit(false)
		releaseReservation()
		failedChannels[channelIndex] = true
		if compactErr != nil {
			if compactErr.responseWritten {
				return
			}
			lastErr = compactErr
		}
	}

	// 所有渠道都失败
	if cfgManager.GetFuzzyModeEnabled() {
		c.JSON(503, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "service_unavailable",
				"message": "All upstream channels are currently unavailable",
			},
		})
		return
	}

	if lastErr != nil {
		writeCompactResponseBody(c, lastErr.status, lastErr.body)
	} else {
		c.JSON(503, gin.H{"error": "所有 Responses 渠道都不可用"})
	}
}

func compactRequestModel(body []byte) string {
	var payload struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.Model)
}

// tryCompactChannelWithAllKeys 尝试渠道的所有 key
func tryCompactChannelWithAllKeys(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	channelIndex int,
	cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	conversationID string,
	envCfg *config.EnvConfig,
) (bool, string, *compactError) {
	if len(upstream.APIKeys) == 0 {
		return false, "", nil
	}

	metricsManager := channelScheduler.MetricsManager(scheduler.ChannelKindResponses)

	failedKeys := make(map[string]bool)
	var lastErr *compactError

	// 强制探测模式
	forceProbeMode := proxycore.AreAllKeysSuspended(metricsManager, upstream.BaseURL, upstream.APIKeys, channelIndex)
	if forceProbeMode {
		log.Printf("[Compact-Probe] 渠道 %s 所有 Key 都被熔断，启用强制探测模式", upstream.Name)
	}

	for attempt := 0; attempt < len(upstream.APIKeys); attempt++ {
		apiKey, err := cfgManager.GetNextResponsesAPIKey(upstream, failedKeys)
		if err != nil {
			break
		}

		// 检查熔断状态
		if !forceProbeMode && metricsManager.ShouldSuspendKey(upstream.BaseURL, apiKey, channelIndex) {
			failedKeys[apiKey] = true
			log.Printf("[Compact-Key] 跳过熔断中的 Key: %s", utils.MaskAPIKey(apiKey))
			continue
		}

		success, compactErr := tryCompactWithKey(c, upstream, apiKey, bodyBytes, envCfg, cfgManager, sessionManager, channelScheduler, conversationID)
		if success {
			return true, apiKey, nil
		}

		if compactErr != nil {
			if compactErr.responseWritten {
				return true, "", nil
			}
			lastErr = compactErr
			if compactErr.shouldFailover {
				failedKeys[apiKey] = true
				cfgManager.MarkKeyAsFailed(apiKey, "Responses")
				channelScheduler.RecordFailure(upstream.BaseURL, apiKey, channelIndex, scheduler.ChannelKindResponses)
				continue
			}
			// 非故障转移错误，返回但标记渠道成功（请求已处理）
			writeCompactResponseBody(c, compactErr.status, compactErr.body)
			return true, "", nil
		}
	}

	return false, "", lastErr
}

func writeCompactResponseBody(c *gin.Context, status int, body []byte) {
	restored, err := hooks.RestoreAttachedResponseBody(c, body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "敏感信息还原失败", "code": "CONTENT_SAFETY_HOOK_ERROR"})
		return
	}
	c.Data(status, "application/json", restored)
}

// tryCompactWithKey 使用单个 key 尝试 compact 请求
func tryCompactWithKey(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	apiKey string,
	bodyBytes []byte,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager,
	channelScheduler *scheduler.ChannelScheduler,
	conversationID string,
) (bool, *compactError) {
	if upstream == nil || upstream.ServiceType != converters.ResponsesUpstreamResponses {
		return false, &compactError{
			status:         http.StatusBadRequest,
			body:           []byte(`{"error":"当前渠道不是原生 Responses 上游，不支持 /responses/compact"}`),
			shouldFailover: false,
		}
	}
	if upstream != nil && upstream.DisablePromptCacheKey {
		var payload map[string]interface{}
		if json.Unmarshal(bodyBytes, &payload) == nil {
			delete(payload, "prompt_cache_key")
			delete(payload, "prompt_cache_retention")
			if sanitized, err := utils.MarshalJSONNoEscape(payload); err == nil {
				bodyBytes = sanitized
			}
		}
	}
	targetURL := buildCompactURL(upstream)
	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return false, &compactError{status: 500, body: []byte(`{"error":"创建请求失败"}`), shouldFailover: true}
	}

	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
	req.Header.Del("authorization")
	req.Header.Del("x-api-key")
	utils.SetAuthenticationHeader(req.Header, apiKey)
	req.Header.Set("Content-Type", "application/json")
	if err := hooks.RunAttachedPreRequestHooks(c.Request.Context(), c, req, upstream.Name, "responses"); err != nil {
		_ = req.Body.Close()
		writeCompactContentSafetyError(c, err)
		return false, &compactError{responseWritten: true}
	}

	proxyURL, err := cfgManager.ResolveUpstreamProxyURL(upstream)
	if err != nil {
		_ = req.Body.Close()
		return false, &compactError{status: 500, body: []byte(`{"error":"上游代理配置无效"}`), shouldFailover: false}
	}
	resp, err := proxycore.SendRequest(req, upstream, envCfg, false, "Responses", proxyURL, nil)
	if err != nil {
		return false, &compactError{status: 502, body: []byte(`{"error":"上游请求失败"}`), shouldFailover: true}
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		// 读取中断：响应不完整，按上游网络故障转移处理，不回写截断 body。
		log.Printf("[Compact-Key] 警告: 读取上游响应体失败 (状态: %d): %v", resp.StatusCode, readErr)
		return false, &compactError{status: 502, body: []byte(`{"error":"读取上游响应失败"}`), shouldFailover: true}
	}
	respBody = utils.DecompressGzipIfNeeded(resp, respBody)

	// 判断是否需要故障转移
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		shouldFailover, _ := proxycore.ShouldRetryWithNextKey(resp.StatusCode, respBody, cfgManager.GetFuzzyModeEnabled(), "Responses")
		return false, &compactError{status: resp.StatusCode, body: respBody, shouldFailover: shouldFailover}
	}

	// 成功
	respBody, err = hooks.RunAttachedPostResponseHooks(c.Request.Context(), c, respBody, resp)
	if err != nil {
		writeCompactContentSafetyError(c, err)
		return false, &compactError{responseWritten: true}
	}
	compactResponseID := extractCompactResponseID(respBody)
	if err := commitCompactSession(sessionManager, bodyBytes, respBody, conversationID); err != nil {
		// 上游 compact 已成功，但代理本地的历史边界没有落盘时，不能
		// 继续返回成功并清理旧链；否则下一轮可能拿着新 response ID
		// 进入一个空 session，静默丢失压缩后的上下文。
		log.Printf("[Compact-Session] 压缩后的 Responses 会话替换失败: %v", err)
		return false, &compactError{
			status:         http.StatusInternalServerError,
			body:           []byte(`{"error":"本地会话压缩状态保存失败"}`),
			shouldFailover: false,
		}
	}
	if compactResponseID != "" {
		proxycore.AssociateConversationExternalID(channelScheduler, conversationID, scheduler.ChannelKindResponses, compactResponseID)
	}
	// compact 已经建立新的响应边界，不能把上游返回的旧 previous ID
	// 原样交给客户端；Cursor 下一轮若重放它，会重新命中压缩前的链。
	respBody, err = clearCompactPreviousResponseIDs(respBody)
	if err != nil {
		return false, &compactError{status: http.StatusInternalServerError,
			body: []byte(`{"error":"清理 compact 旧会话 ID 失败"}`), shouldFailover: false}
	}
	// compact 成功后，旧的 Messages→Responses 链不能继续引用压缩前
	// 的 response_id；否则下一轮仍会把服务端旧上下文带回来。
	session.DefaultResponseChainManager().Clear(conversationID)
	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	c.Data(resp.StatusCode, "application/json", respBody)
	return true, nil
}

func clearCompactPreviousResponseIDs(body []byte) ([]byte, error) {
	result := body
	for _, path := range []string{
		"previous_id", "previous_response_id",
		"response.previous_id", "response.previous_response_id",
	} {
		if !gjson.GetBytes(result, path).Exists() {
			continue
		}
		var err error
		result, err = sjson.DeleteBytes(result, path)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func extractCompactResponseID(body []byte) string {
	for _, path := range []string{"id", "response.id"} {
		if id := strings.TrimSpace(gjson.GetBytes(body, path).String()); id != "" {
			return id
		}
	}
	return ""
}

// commitCompactSession 用 compact 结果替换代理本地旧历史。compact 请求的
// input 通常是客户端重放的完整 transcript，绝不能再把它当作普通一轮追加，
// 否则压缩成功后本地 session 仍会保留完整旧上下文。
func commitCompactSession(sessionManager *session.SessionManager, requestBody, responseBody []byte, conversationID string) error {
	if sessionManager == nil {
		return nil
	}
	previousResponseID := strings.TrimSpace(gjson.GetBytes(requestBody, "previous_response_id").String())
	responseID := extractCompactResponseID(responseBody)
	if responseID == "" {
		return fmt.Errorf("compact 响应缺少新 response id，无法建立压缩后的会话边界")
	}

	output := gjson.GetBytes(responseBody, "output")
	if !output.Exists() {
		output = gjson.GetBytes(responseBody, "response.output")
	}
	if !output.Exists() || !output.IsArray() || len(output.Array()) == 0 {
		return fmt.Errorf("compact 响应缺少非空 output，保留压缩前会话")
	}
	var rawItems []interface{}
	if err := json.Unmarshal([]byte(output.Raw), &rawItems); err != nil {
		return fmt.Errorf("解析 compact output 失败: %w", err)
	}
	items, err := parseInputToItems(rawItems)
	if err != nil {
		return fmt.Errorf("解析 compact session item 失败: %w", err)
	}
	if previousResponseID == "" {
		return sessionManager.ReplaceConversationSessionAfterCompact(conversationID, responseID, items)
	}
	return sessionManager.ReplaceSessionAfterCompact(previousResponseID, responseID, items)
}

func writeCompactContentSafetyError(c *gin.Context, err error) {
	if writeErr := hooks.WriteAttachedContentSafetyError(c, err); writeErr == nil {
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{
		"error": "内容安全检查失败",
		"code":  "CONTENT_SAFETY_HOOK_ERROR",
	})
}

// buildCompactURL 构建 compact 端点 URL
func buildCompactURL(upstream *config.UpstreamConfig) string {
	baseURL := strings.TrimSuffix(upstream.BaseURL, "/")
	versionPattern := regexp.MustCompile(`/v\d+[a-z]*$`)
	if versionPattern.MatchString(baseURL) {
		return baseURL + "/responses/compact"
	}
	return baseURL + "/v1/responses/compact"
}

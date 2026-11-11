// Package responses 提供 Responses API 的处理器
package responses

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/converters"
	"github.com/insistanan/api-proxy/internal/handlers/hooks"
	"github.com/insistanan/api-proxy/internal/handlers/proxycore"
	"github.com/insistanan/api-proxy/internal/middleware"
	"github.com/insistanan/api-proxy/internal/providers"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/session"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/utils"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// compactError 封装 compact 请求错误
type compactError struct {
	status          int
	body            []byte
	shouldFailover  bool
	responseWritten bool
	localAdmission  bool // 本地排队、预算或配置删除，不标记 Key/渠道失败
	budgetExhausted bool // 全请求预算耗尽，不能继续轮换 Key 或渠道
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
		c.Request = c.Request.WithContext(upstreamcontrol.WithScope(c.Request.Context()))

		// 读取请求体
		maxBodySize := envCfg.Current().MaxRequestBodySize
		bodyBytes, err := proxycore.ReadRequestBody(c, maxBodySize)
		if err != nil {
			return
		}
		requestID := proxycore.BindRequestLogID(c)

		// compact 是控制面操作：只复用主请求已经建立的内部会话，不创建新的会话记录。
		// 这样不会改变客户端协议，同时避免外部 conversation ID 与内部记录 ID 分裂。
		identity := proxycore.ResolveConversationIdentity(c, bodyBytes)
		conversationID, err := proxycore.ResolveExistingConversationID(
			channelScheduler, scheduler.ChannelKindResponses, identity)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "CONVERSATION_STORE_ERROR"})
			return
		}
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
		isMultiChannel := cfgManager.GetFuzzyModeEnabledForProtocol("responses") && channelScheduler.IsMultiChannelModeForModel(scheduler.ChannelKindResponses, model)

		if isMultiChannel {
			handleMultiChannelCompact(c, envCfg, cfgManager, sessionManager, channelScheduler, bodyBytes, conversationID, model)
		} else {
			handleSingleChannelCompact(c, envCfg, cfgManager, sessionManager, channelScheduler, bodyBytes, conversationID, model)
		}
	})
}

// handleSingleChannelCompact 沿用单渠道入口，仅允许显式模型映射指定的单跳目标。
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
	upstream, channelIndex, err := cfgManager.GetCurrentResponsesUpstreamWithIndexForModel(model)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "未配置任何 Responses 渠道"})
		return
	}
	result := compactAttempt(c, upstream, channelIndex, nil, cfgManager, sessionManager,
		channelScheduler, bodyBytes, conversationID, envCfg).TryWithModelMappingFailover()
	if !result.Handled {
		proxycore.HandleAllKeysFailed(c, cfgManager.GetFuzzyModeEnabledForProtocol("responses"), result.FailoverError, result.LastError, "Responses")
	}
}

// handleMultiChannelCompact 复用普通请求的选渠许可和健康记账外壳。
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
	proxycore.HandleMultiChannelFailover(c, envCfg, channelScheduler, scheduler.ChannelKindResponses,
		"Responses", conversationID, model, cfgManager.GetFuzzyModeEnabledForProtocol("responses"),
		func(selection *scheduler.SelectionResult) proxycore.MultiChannelAttemptResult {
			result := compactAttempt(c, selection.Upstream, selection.ChannelIndex, selection.Permit,
				cfgManager, sessionManager, channelScheduler, bodyBytes, conversationID, envCfg).
				TryWithModelMappingFailover()
			return proxycore.MultiChannelAttemptResult{
				Handled:           result.Handled,
				Attempted:         true,
				SuccessKey:        result.SuccessKey,
				SuccessBaseURLIdx: result.SuccessBaseURLIdx,
				FailoverError:     result.FailoverError,
				LastError:         result.LastError,
				Health:            result.Health,
				AbortFailover:     result.AbortFailover,
			}
		},
		func(selection *scheduler.SelectionResult, result proxycore.MultiChannelAttemptResult) {
			if result.SuccessKey != "" {
				channelScheduler.MarkConversationSuccess(conversationID, scheduler.ChannelKindResponses,
					selection.ChannelIndex, selection.Upstream.Name)
			}
		},
		func(c *gin.Context, failoverErr *proxycore.FailoverError, lastError error) {
			proxycore.HandleAllChannelsFailed(c, cfgManager.GetFuzzyModeEnabledForProtocol("responses"), failoverErr, lastError, "Responses")
		})
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

// compactAttempt 仅注入 compact 专属载荷和成功事务；模型、目标渠道、URL、Key、
// 预算和准入保持普通 Responses 的唯一编排，目标渠道不会再次解析其映射。
func compactAttempt(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	channelIndex int,
	permit *scheduler.ChannelPermit,
	cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	conversationID string,
	envCfg *config.EnvConfig,
) proxycore.UpstreamAttempt {
	return proxycore.UpstreamAttempt{
		Context:            c,
		EnvConfig:          envCfg,
		ConfigManager:      cfgManager,
		ChannelScheduler:   channelScheduler,
		Kind:               scheduler.ChannelKindResponses,
		APIType:            "Responses",
		MetricsManager:     channelScheduler.MetricsManager(scheduler.ChannelKindResponses),
		Upstream:           upstream,
		RequestedModel:     compactRequestModel(bodyBytes),
		AllowModelFailover: cfgManager.GetFuzzyModeEnabledForProtocol("responses"),
		Permit:             permit,
		SkipVisionLayer:    true,
		URLResults:         channelScheduler.GetSortedURLsForChannel(scheduler.ChannelKindResponses, channelIndex, upstream.GetAllBaseURLs()),
		RequestBody:        bodyBytes,
		NextAPIKey:         cfgManager.GetNextResponsesAPIKey,
		LogContext: proxycore.AttemptLogContext{
			ChannelIndex:   channelIndex,
			Model:          compactRequestModel(bodyBytes),
			ConversationID: conversationID,
		},
		BuildRequest: func(c *gin.Context, candidate *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return buildCompactRequest(c, candidate, apiKey, bodyBytes, cfgManager, sessionManager, conversationID)
		},
		MarkURLFailure: func(baseURL string) {
			channelScheduler.MarkURLFailure(scheduler.ChannelKindResponses, channelIndex, baseURL)
		},
		MarkURLSuccess: func(baseURL string) {
			channelScheduler.MarkURLSuccess(scheduler.ChannelKindResponses, channelIndex, baseURL)
		},
		HandleSuccess: func(c *gin.Context, resp *http.Response, candidate *config.UpstreamConfig, apiKey string) (*types.Usage, error) {
			return nil, finishCompactResponse(c, resp, bodyBytes, sessionManager, channelScheduler, conversationID)
		},
	}
}

func writeCompactResponseBody(c *gin.Context, status int, body []byte) {
	restored, err := hooks.RestoreAttachedResponseBody(c, body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "敏感信息还原失败", "code": "CONTENT_SAFETY_HOOK_ERROR"})
		return
	}
	c.Data(status, "application/json", restored)
}

// buildCompactRequest 只修改本次目标模型和必要缓存字段，保留原生扩展字段。
// candidate 的 BaseURL 已由编排固定，用同一地址产生 URL 与历史归属指纹。
func buildCompactRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string,
	bodyBytes []byte, cfgManager *config.ConfigManager, sessionManager *session.SessionManager,
	conversationID string) (*http.Request, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return nil, &proxycore.RequestValidationError{Err: fmt.Errorf("解析 compact 请求失败: %w", err)}
	}
	if payload == nil {
		return nil, &proxycore.RequestValidationError{Err: fmt.Errorf("compact 请求必须是 JSON 对象")}
	}
	var requestedModel string
	if raw, ok := payload["model"]; ok {
		if string(raw) == "null" {
			return nil, &proxycore.RequestValidationError{Err: fmt.Errorf("compact model 必须是字符串")}
		}
		if err := json.Unmarshal(raw, &requestedModel); err != nil {
			return nil, &proxycore.RequestValidationError{Err: fmt.Errorf("compact model 必须是字符串: %w", err)}
		}
	}
	if upstream == nil || upstream.ServiceType != converters.ResponsesUpstreamResponses {
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("当前渠道不是原生 Responses 上游，不支持 /responses/compact")}
	}
	model := config.ResolveUpstreamModel(requestedModel, upstream)
	if strings.TrimSpace(model) == "" {
		return nil, &proxycore.RequestValidationError{Err: fmt.Errorf("compact 请求缺少可解析的 model")}
	}
	bodyBytes, err := sjson.SetBytes(bodyBytes, "model", model)
	if err != nil {
		return nil, fmt.Errorf("设置 compact 目标模型失败: %w", err)
	}
	bodyBytes, err = providers.PrepareNativeResponseHistory(sessionManager, conversationID,
		bodyBytes, upstream.RequestIdentity(apiKey).Fingerprint())
	if err != nil {
		return nil, fmt.Errorf("准备 compact 历史失败: %w", err)
	}
	if upstream.DisablePromptCacheKey {
		for _, field := range []string{"prompt_cache_key", "prompt_cache_retention"} {
			bodyBytes, err = sjson.DeleteBytes(bodyBytes, field)
			if err != nil {
				return nil, fmt.Errorf("清理 compact 缓存字段失败: %w", err)
			}
		}
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, buildCompactURL(upstream), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, &config.ConfigError{Message: fmt.Sprintf("创建 compact 请求失败: %s", err)}
	}
	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
	compatibility := cfgManager != nil && cfgManager.ClientCompatibilityEnabled(upstream)
	utils.PrepareTargetIdentity(req.Header, "responses", compatibility)
	if compatibility {
		utils.SeedDisguiseSession(req.Header, conversationID, "responses")
		utils.ApplyCodexDisguise(req.Header, false)
	}
	req.Header.Del("authorization")
	req.Header.Del("x-api-key")
	utils.SetAuthenticationHeader(req.Header, apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// tryCompactWithKey 保留单候选接缝供针对性验证，生产入口通过 compactAttempt 编排。
func tryCompactWithKey(c *gin.Context, upstream *config.UpstreamConfig, apiKey string,
	bodyBytes []byte, envCfg *config.EnvConfig, cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager, channelScheduler *scheduler.ChannelScheduler,
	conversationID string) (bool, *compactError) {
	req, err := buildCompactRequest(c, upstream, apiKey, bodyBytes, cfgManager, sessionManager, conversationID)
	if err != nil {
		status := http.StatusInternalServerError
		var requestErr *proxycore.RequestValidationError
		var clientErr interface{ ClientRequestError() bool }
		if errors.As(err, &requestErr) || types.IsUnsupportedProtocol(err) ||
			(errors.As(err, &clientErr) && clientErr.ClientRequestError()) {
			status = http.StatusBadRequest
		}
		payload, _ := json.Marshal(gin.H{"error": err.Error()})
		return false, &compactError{status: status, body: payload}
	}
	if err := hooks.RunAttachedPreRequestHooks(c.Request.Context(), c, req, upstream.Name, "responses"); err != nil {
		_ = req.Body.Close()
		writeCompactContentSafetyError(c, err)
		return false, &compactError{responseWritten: true}
	}
	proxyURL, err := cfgManager.ResolveUpstreamProxyURL(upstream)
	if err != nil {
		_ = req.Body.Close()
		return false, &compactError{status: http.StatusInternalServerError, body: []byte(`{"error":"上游代理配置无效"}`)}
	}
	req = req.WithContext(cfgManager.UpstreamRequestContext(req.Context(), upstream, apiKey))
	resp, err := proxycore.SendRequest(req, upstream, envCfg, false, "Responses", proxyURL, nil, 0)
	if err != nil {
		if c.Request.Context().Err() != nil {
			return false, &compactError{responseWritten: true}
		}
		payload, _ := json.Marshal(gin.H{"error": err.Error()})
		return false, &compactError{status: http.StatusBadGateway, body: payload,
			shouldFailover: !upstreamcontrol.IsLocalError(err), localAdmission: upstreamcontrol.IsLocalError(err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, &compactError{status: http.StatusBadGateway, body: []byte(`{"error":"读取上游响应失败"}`), shouldFailover: true}
		}
		respBody = utils.DecompressGzipIfNeeded(resp, respBody)
		shouldFailover, _ := proxycore.ShouldRetryWithNextKey(resp.StatusCode, respBody, cfgManager.GetFuzzyModeEnabledForProtocol("responses"), "Responses")
		return false, &compactError{status: resp.StatusCode, body: respBody, shouldFailover: shouldFailover}
	}
	if err := finishCompactResponse(c, resp, bodyBytes, sessionManager, channelScheduler, conversationID); err != nil {
		return false, &compactError{responseWritten: c.Writer.Written(), status: http.StatusInternalServerError, body: []byte(`{"error":"compact 响应处理失败"}`)}
	}
	return true, nil
}

// finishCompactResponse 在成功回程后原子替换完整 compact output 与映射。
// 本地提交失败必须中性终止，不能重发已经成功的压缩操作。
func finishCompactResponse(c *gin.Context, resp *http.Response, requestBody []byte,
	sessionManager *session.SessionManager, channelScheduler *scheduler.ChannelScheduler,
	conversationID string) error {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取 compact 响应失败: %w", err)
	}
	if err := c.Request.Context().Err(); err != nil {
		return err
	}
	respBody = utils.DecompressGzipIfNeeded(resp, respBody)
	respBody, err = hooks.RunAttachedPostResponseHooks(c.Request.Context(), c, respBody, resp)
	if err != nil {
		return err
	}
	if err := commitCompactSession(sessionManager, requestBody, respBody, conversationID, responseUpstreamOwner(resp)); err != nil {
		return &compactCommitError{Err: fmt.Errorf("本地会话压缩状态保存失败: %w", err)}
	}
	compactResponseID := extractCompactResponseID(respBody)
	if compactResponseID != "" {
		proxycore.AssociateConversationExternalID(channelScheduler, conversationID, scheduler.ChannelKindResponses, compactResponseID)
	}
	respBody, err = clearCompactPreviousResponseIDs(respBody)
	if err != nil {
		return &compactCommitError{Err: fmt.Errorf("清理 compact 旧会话 ID 失败: %w", err)}
	}
	session.DefaultResponseChainManager().Clear(conversationID)
	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	c.Data(resp.StatusCode, "application/json", respBody)
	return nil
}

type compactCommitError struct{ Err error }

func (e *compactCommitError) Error() string {
	return e.Err.Error()
}

func (e *compactCommitError) Unwrap() error {
	return e.Err
}

func (e *compactCommitError) LocalResponseError() bool {
	return true
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
func commitCompactSession(sessionManager *session.SessionManager, requestBody, responseBody []byte, conversationID string, owner ...string) error {
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
		return sessionManager.ReplaceConversationSessionAfterCompact(conversationID, responseID, items, owner...)
	}
	return sessionManager.ReplaceSessionAfterCompact(previousResponseID, responseID, items, owner...)
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

// buildCompactURL 与普通 Responses 共用完整地址、# 和版本前缀规则。
func buildCompactURL(upstream *config.UpstreamConfig) string {
	return utils.BuildUpstreamURL(upstream.GetEffectiveBaseURL(), "/v1", "/responses/compact", upstream.UseFullURL)
}

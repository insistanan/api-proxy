package proxycore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/BenedictKing/api-proxy/internal/bodyfilter"
	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/middleware"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/urlhealth"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

// ProtocolSpec 描述一种代理协议的差异点。
// 五个协议共有的流程（认证/读体/会话观测/渠道选择/failover/日志）由 RunProxyRequest 承担，
// 协议只需填这张表。
type ProtocolSpec struct {
	Kind    scheduler.ChannelKind
	LogName string // 日志前缀，如 "Messages" / "Chat"

	// HookPipeline 可选：请求前/请求时/请求后公共管道。请求前 Hook 会在
	// 最终上游载荷生成后、Vision 处理前后执行，既避免原文先进入图片理解层，
	// 也会检查图片理解层新生成的文本。
	HookPipeline *hooks.Pipeline

	// AllowContentPolicyChannelFailover 仅多渠道分派生效：内容审核错误时
	// 跨渠道转移（不计入渠道故障）。单渠道保持按 Key 分类的既有语义。
	AllowContentPolicyChannelFailover bool

	// PreRoute 可选：协议特有的前置路由（如 chat 的 modelcatalog 路由）。
	// 返回 true 表示请求已被处理，主流程应直接返回。
	PreRoute func(c *gin.Context, body []byte, model string, conversationID string, startTime time.Time) bool

	// ParseRequest 解析请求体，返回本次请求的模型名、是否流式、提示词列表。
	// 解析失败时应自行写出 4xx 响应并返回 ok=false。
	ParseRequest func(c *gin.Context, body []byte) (model string, stream bool, prompts []string, ok bool)

	// BuildUpstreamRequest 构造发往上游的 http.Request（含协议特有的请求体清洗/模型映射）。
	BuildUpstreamRequest func(c *gin.Context, up *config.UpstreamConfig, apiKey string, body []byte) (*http.Request, error)

	// HandleSuccess 处理上游 2xx 响应（含流式与非流式分派）。
	HandleSuccess func(c *gin.Context, resp *http.Response, up *config.UpstreamConfig, apiKey string, body []byte, startTime time.Time) (*types.Usage, error)

	// RequestTimeoutOverride 可按原始请求覆盖普通上游请求超时。
	// 生图任务通常比对话请求耗时更长，返回 0 表示沿用 EnvConfig.RequestTimeout。
	RequestTimeoutOverride func(body []byte) time.Duration

	// HandleAllFailed 可选：所有渠道都失败时的错误响应格式。
	// 若为 nil，使用默认 HandleAllChannelsFailed（gin.H 格式）。
	// Gemini 协议需要覆盖此选项以返回 types.GeminiError 格式。
	HandleAllFailed func(c *gin.Context, failoverErr *FailoverError, lastError error)

	// HandleAllKeysFailed 可选：单渠道模式下所有 Key 都失败时的错误响应格式。
	// 若为 nil，使用默认 HandleAllKeysFailed（gin.H 格式）。
	HandleAllKeysFailed func(c *gin.Context, fuzzyMode bool, failoverErr *FailoverError, lastError error)
}

// RunProxyRequest 是一个可复用的代理请求处理主流程。
// 它实现了五个协议共有的骨架：
//
//	认证 → 读 body → 解析 → 脱敏 → 会话观测 → 日志 → 指定渠道分支 → PreRoute → 多渠道/单渠道分派
//
// 协议特有的逻辑（解析、构建请求、处理响应、前置路由）通过 ProtocolSpec 注入。
// 调用方通常这样使用：
//
//	func Handler(envCfg, cfgManager, channelScheduler) gin.HandlerFunc {
//	    spec := ProtocolSpec{...}
//	    return func(c *gin.Context) {
//	        RunProxyRequest(c, envCfg, cfgManager, channelScheduler, spec)
//	    }
//	}
func RunProxyRequest(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	spec ProtocolSpec,
) {
	// 1. 认证
	middleware.ProxyAuthMiddleware(envCfg)(c)
	if c.IsAborted() {
		return
	}

	startTime := time.Now()

	// 2. 读取请求体
	bodyBytes, err := ReadRequestBody(c, envCfg.MaxRequestBodySize)
	if err != nil {
		return
	}
	requestID := BindRequestLogID(c)

	// 过滤客户端私有参数（如 _debug, _internal_id 等），保护 JSON Schema 属性定义
	if filteredBytes, modified, removed := bodyfilter.FilterPrivateParams(bodyBytes); modified {
		if envCfg.ShouldLog("debug") {
			log.Printf("[%s-BodyFilter] 过滤私有参数: %v", spec.LogName, removed)
		}
		bodyBytes = filteredBytes
	}
	RestoreRequestBody(c, bodyBytes)

	// 3. 解析请求
	model, stream, prompts, ok := spec.ParseRequest(c, bodyBytes)
	if !ok {
		return
	}
	identity := ResolveConversationIdentity(c, bodyBytes)
	transcript := BuildConversationTranscript(string(spec.Kind), bodyBytes)
	imageFingerprints := utils.ExtractImageFingerprints(bodyBytes)
	hooks.AttachHookPipeline(c, spec.HookPipeline, hooks.HookContext{
		APIType:   string(spec.Kind),
		Model:     model,
		Stream:    stream,
		RequestID: requestID,
	})
	defer hooks.ClearAttachedSensitiveData(c)
	if err := hooks.RunAttachedPreRequestHooks(c.Request.Context(), c, c.Request, "", string(spec.Kind)); err != nil {
		conversationID := ObserveConversationRequest(
			channelScheduler, spec.Kind, identity, transcript, model, nil, imageFingerprints, stream,
		)
		if assignErr := hooks.AssignAttachedConversation(c.Request.Context(), c, conversationID); assignErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "关联内容安全记录会话失败", "code": "CONTENT_SAFETY_LOG_ERROR"})
			MarkConversationFailure(channelScheduler, conversationID, spec.Kind, assignErr)
			MarkConversationComplete(channelScheduler, conversationID, spec.Kind)
			return
		}
		MarkConversationFailure(channelScheduler, conversationID, spec.Kind, err)
		MarkConversationComplete(channelScheduler, conversationID, spec.Kind)
		handleContentSafetyPreparationError(c, spec.LogName, err)
		return
	}
	bodyBytes, err = io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取脱敏请求体失败", "code": "CONTENT_SAFETY_HOOK_ERROR"})
		return
	}
	RestoreRequestBody(c, bodyBytes)
	for index := range prompts {
		prompts[index], err = hooks.MaskAttachedText(c, prompts[index])
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "脱敏会话观测文本失败", "code": "CONTENT_SAFETY_HOOK_ERROR"})
			return
		}
		prompts[index] = utils.RedactSensitivePlaceholdersForLog(prompts[index])
	}

	// 4. 会话观测
	conversationID := ObserveConversationRequest(
		channelScheduler,
		spec.Kind,
		identity,
		transcript,
		model,
		prompts,
		imageFingerprints,
		stream,
	)
	defer MarkConversationComplete(channelScheduler, conversationID, spec.Kind)
	// 会话记录 ID 写入请求上下文，供协议回调（如 HandleSuccess）取用。
	c.Set(utils.ContextKeyConversationUserID, conversationID)
	if err := hooks.AssignAttachedConversation(c.Request.Context(), c, conversationID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "关联内容安全记录会话失败", "code": "CONTENT_SAFETY_LOG_ERROR"})
		MarkConversationFailure(channelScheduler, conversationID, spec.Kind, err)
		return
	}

	// 5. 记录原始请求
	LogOriginalRequest(c, bodyBytes, envCfg, spec.LogName)

	// 6. 指定渠道分支
	requestedChannelIndex, hasRequestedChannel, err := ExtractRequestedChannelIndex(bodyBytes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  "INVALID_CHANNEL_INDEX",
		})
		return
	}
	if hasRequestedChannel {
		upstream, channelIndex, err := ResolveRequestedUpstream(cfgManager, spec.Kind, requestedChannelIndex)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": err.Error(),
				"code":  "INVALID_CHANNEL_INDEX",
			})
			return
		}
		handleSingleChannelProxy(c, envCfg, cfgManager, channelScheduler, spec, bodyBytes, model, stream, conversationID, upstream, channelIndex, startTime)
		return
	}

	// 7. PreRoute（协议特有前置路由，如 chat 的 modelcatalog 路由）
	if spec.PreRoute != nil {
		if spec.PreRoute(c, bodyBytes, model, conversationID, startTime) {
			return
		}
	}

	// 8. 多渠道/单渠道分派
	if channelScheduler.IsMultiChannelModeForModel(spec.Kind, model) {
		handleMultiChannelProxy(c, envCfg, cfgManager, channelScheduler, spec, bodyBytes, model, stream, conversationID, startTime)
	} else {
		handleSingleChannelProxy(c, envCfg, cfgManager, channelScheduler, spec, bodyBytes, model, stream, conversationID, nil, 0, startTime)
	}
}

// buildProtocolAttempt 构造五协议共享的 UpstreamAttempt 骨架：公共字段与
// 五个协议级闭包（NextAPIKey/BuildRequest/DeprioritizeKey/HandleSuccess/
// LogContext）只写一份，调用方只补差异点。差异经参数表达：
//   - urlResults：单渠道为配置序（BuildDefaultURLResults），多渠道为延迟排序序
//   - deprioritizeKind：密钥降级目标池，多渠道修复为按实际 kind 写池
//     （历史 bug：曾硬编码 "messages"，跨池降级写错渠道池）
//   - markURL：多渠道需回写 URL 健康状态（MarkURLFailure/Success），
//     单渠道不参与 URL 健康统计，传 nil 即省略
//   - allowContentPolicyChannelFailover：内容审核错误跨渠道转移开关；
//     仅多渠道分派传入 spec.AllowContentPolicyChannelFailover，单渠道恒为 false
func buildProtocolAttempt(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	spec ProtocolSpec,
	bodyBytes []byte,
	model string,
	stream bool,
	conversationID string,
	upstream *config.UpstreamConfig,
	channelIndex int,
	startTime time.Time,
	urlResults []urlhealth.URLLatencyResult,
	deprioritizeKind scheduler.ChannelKind,
	markURLFailure func(url string),
	markURLSuccess func(url string),
	allowContentPolicyChannelFailover bool,
) UpstreamAttempt {
	attempt := NewAttemptBuilder(
		c, envCfg, cfgManager, channelScheduler,
		spec.Kind, spec.LogName, upstream, model, bodyBytes, stream,
		channelIndex, conversationID,
	).
		WithURLResults(urlResults).
		WithNextAPIKey(func(up *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(up, failedKeys, spec.LogName)
		}).
		WithBuildRequest(func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return spec.BuildUpstreamRequest(c, upstreamCopy, apiKey, bodyBytes)
		}).
		WithRequestTimeout(func() time.Duration {
			if spec.RequestTimeoutOverride == nil {
				return 0
			}
			return spec.RequestTimeoutOverride(bodyBytes)
		}()).
		WithDeprioritizeKey(func(apiKey string) {
			if err := cfgManager.MoveAPIKeyToBottomForKind(string(deprioritizeKind), channelIndex, apiKey); err != nil {
				log.Printf("[%s-Key] 警告: 密钥降级失败: %v", spec.LogName, err)
			}
		}).
		WithMarkURL(markURLFailure, markURLSuccess).
		WithAllowContentPolicyChannelFailover(allowContentPolicyChannelFailover).
		WithHandleSuccess(func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string) (*types.Usage, error) {
			return spec.HandleSuccess(c, resp, upstreamCopy, apiKey, bodyBytes, startTime)
		}).
		Build()
	return attempt
}

// handleSingleChannelProxy 处理单渠道代理请求。
// 当 upstream 为 nil 时，从配置中获取当前可用渠道。
func handleSingleChannelProxy(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	spec ProtocolSpec,
	bodyBytes []byte,
	model string,
	stream bool,
	conversationID string,
	upstream *config.UpstreamConfig,
	channelIndex int,
	startTime time.Time,
) {
	// 未指定渠道时，从配置获取
	if upstream == nil {
		var err error
		upstream, channelIndex, err = cfgManager.GetCurrentUpstreamWithIndexForModelForKind(string(spec.Kind), model)
		if err != nil {
			c.JSON(503, gin.H{
				"error": err.Error(),
				"code":  "NO_UPSTREAM",
			})
			return
		}
	}

	// 快捷测试跳过模型重定向，直接使用用户指定的请求模型
	isQuickTest := IsQuickTestRequest(c, bodyBytes)
	if isQuickTest {
		upstream = upstream.Clone()
		upstream.ModelMapping = nil
		upstream.DefaultModel = ""
	}

	// 渠道级熔断检查（internal/circuit）：非快捷测试且已熔断（Open）时直接拦截并返回 503 CIRCUIT_OPEN。
	// 快捷测试跳过熔断拦截，允许管理端手动探测或快速测试已熔断的渠道——
	// 也不占用半开探测名额（R2-b：不挤占其他请求的探测许可）。
	// 非快捷测试经 CircuitAllow 正式占用：可能取得探测名额（封装为许可句柄，
	// 绑定取得时状态代次），渠道结束时统一记账释放。
	var singlePermit *scheduler.ChannelPermit
	if !isQuickTest {
		circuitResult := channelScheduler.CircuitAllow(spec.Kind, channelIndex, upstream.Name)
		if !circuitResult.Allowed {
			snapMap := channelScheduler.GetCircuitSnapshotForKind(spec.Kind)
			cooldown := 0
			if snap, ok := snapMap[channelIndex]; ok {
				cooldown = snap.CooldownRemainingSec
			}
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": fmt.Sprintf("当前渠道 [%d] %s 处于熔断保护状态 (冷却剩余 %d 秒)，请稍后重试或切换渠道", channelIndex, upstream.Name, cooldown),
				"code":  "CIRCUIT_OPEN",
			})
			return
		}
		if circuitResult.Probe {
			singlePermit = channelScheduler.NewCircuitProbePermit(spec.Kind, channelIndex, circuitResult.Generation)
			// 渠道结束必经许可收尾（含提前返回路径）。
			defer func() {
				singlePermit.Release()
			}()
		}
	}

	// 无 API Key 检查
	if len(upstream.APIKeys) == 0 {
		// 本地配置问题：中性归还探测名额（不按渠道失败记账）。
		if singlePermit != nil {
			singlePermit.RecordHealthNeutral()
		}
		c.JSON(503, gin.H{
			"error": "当前渠道未配置API密钥",
			"code":  "NO_API_KEYS",
		})
		return
	}

	// 对话路由冲突检查
	if err := channelScheduler.ValidateFixedChannel(conversationID, spec.Kind, channelIndex); err != nil {
		// 路由冲突是本地拒绝：中性归还探测名额。
		if singlePermit != nil {
			singlePermit.RecordHealthNeutral()
		}
		MarkConversationFailure(channelScheduler, conversationID, spec.Kind, err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "CONVERSATION_ROUTE_OVERRIDE"})
		return
	}

	baseURLs := upstream.GetAllBaseURLs()
	urlResults := BuildDefaultURLResults(baseURLs)

	// 单渠道：URL 用配置序，不参与 URL 健康统计（markURL 传 nil），
	// 密钥降级按本协议渠道池写；内容审核跨渠道转移不生效（保持按 Key 分类语义）。
	attempt := buildProtocolAttempt(c, envCfg, cfgManager, channelScheduler, spec, bodyBytes, model, stream, conversationID, upstream, channelIndex, startTime, urlResults, spec.Kind, nil, nil, false)
	attempt.Permit = singlePermit
	result := attempt.TryWithModelMappingFailover()
	if result.Handled {
		if result.SuccessKey != "" {
			MarkConversationSuccess(channelScheduler, conversationID, spec.Kind, channelIndex, upstream.Name)
			channelScheduler.ConsumePromotionCount(channelIndex, spec.Kind)
		} else if result.LastError != nil && !errors.Is(result.LastError, context.Canceled) {
			MarkConversationFailure(channelScheduler, conversationID, spec.Kind, result.LastError)
		}
		// 渠道级熔断记账（R2-a/R2-b）：经许可句柄带代次记账。
		recordChannelHealth(channelScheduler, spec.Kind, channelIndex, result.Health, true, spec.LogName, singlePermit)
		return
	}

	// 渠道整体失败（可重试错误耗尽候选）：按映射路径给出的健康结论记账。
	// 单渠道的映射目标可能被转交给其他渠道，此时源渠道结论为中性（见 mappingSourceHealth）；
	// Unset 仅在无尝试结论时出现，保持原先按失败记账的语义。
	singleHealth := result.Health
	if singleHealth == channelHealthUnset {
		singleHealth = channelHealthFailure
	}
	recordChannelHealth(channelScheduler, spec.Kind, channelIndex, singleHealth, true, spec.LogName, singlePermit)
	log.Printf("[%s-Error] 所有API密钥都失败了", spec.LogName)
	MarkConversationFailure(channelScheduler, conversationID, spec.Kind, result.LastError)
	if spec.HandleAllKeysFailed != nil {
		spec.HandleAllKeysFailed(c, cfgManager.GetFuzzyModeEnabled(), result.FailoverError, result.LastError)
	} else {
		HandleAllKeysFailed(c, cfgManager.GetFuzzyModeEnabled(), result.FailoverError, result.LastError, spec.LogName)
	}
}

// handleMultiChannelProxy 处理多渠道代理请求。
func handleMultiChannelProxy(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	spec ProtocolSpec,
	bodyBytes []byte,
	model string,
	stream bool,
	conversationID string,
	startTime time.Time,
) {
	HandleMultiChannelFailover(
		c,
		envCfg,
		channelScheduler,
		spec.Kind,
		spec.LogName,
		conversationID,
		model,
		cfgManager.GetFuzzyModeEnabled(),
		func(selection *scheduler.SelectionResult) MultiChannelAttemptResult {
			upstream := selection.Upstream
			channelIndex := selection.ChannelIndex

			if upstream == nil {
				return MultiChannelAttemptResult{}
			}

			baseURLs := upstream.GetAllBaseURLs()
			sortedURLResults := channelScheduler.GetSortedURLsForChannel(spec.Kind, channelIndex, baseURLs)

			// 多渠道：URL 用延迟排序序，回写 URL 健康状态；密钥降级按实际
			// kind 写池（修复历史 bug：曾硬编码 messages 池，跨池降级写错渠道）。
			// 内容审核跨渠道转移按 spec 开关生效；选渠许可传入 attempt，
			// 供发送层"未发出即失败"路径中性归还探测名额。
			attempt := buildProtocolAttempt(c, envCfg, cfgManager, channelScheduler, spec, bodyBytes, model, stream, conversationID, upstream, channelIndex, startTime, sortedURLResults, spec.Kind,
				func(url string) { channelScheduler.MarkURLFailure(spec.Kind, channelIndex, url) },
				func(url string) { channelScheduler.MarkURLSuccess(spec.Kind, channelIndex, url) },
				spec.AllowContentPolicyChannelFailover,
			)
			attempt.Permit = selection.Permit
			result := attempt.TryWithModelMappingFailover()

			return MultiChannelAttemptResult{
				Handled:           result.Handled,
				Attempted:         true,
				SuccessKey:        result.SuccessKey,
				SuccessBaseURLIdx: result.SuccessBaseURLIdx,
				FailoverError:     result.FailoverError,
				Usage:             result.Usage,
				LastError:         result.LastError,
				Health:            result.Health,
				AbortFailover:     result.AbortFailover,
			}
		},
		func(selection *scheduler.SelectionResult, result MultiChannelAttemptResult) {
			if selection == nil || selection.Upstream == nil {
				return
			}
			if result.SuccessKey != "" {
				MarkConversationSuccess(channelScheduler, conversationID, spec.Kind, selection.ChannelIndex, selection.Upstream.Name)
				return
			}
			if result.LastError != nil && !errors.Is(result.LastError, context.Canceled) {
				MarkConversationFailure(channelScheduler, conversationID, spec.Kind, result.LastError)
			}
		},
		func(ctx *gin.Context, failoverErr *FailoverError, lastError error) {
			MarkConversationFailure(channelScheduler, conversationID, spec.Kind, lastError)
			if spec.HandleAllFailed != nil {
				spec.HandleAllFailed(ctx, failoverErr, lastError)
			} else {
				HandleAllChannelsFailed(ctx, cfgManager.GetFuzzyModeEnabled(), failoverErr, lastError, spec.LogName)
			}
		},
	)
}

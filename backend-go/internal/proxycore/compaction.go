package proxycore

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/dialogue/routing/intent"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/safety/pipeline"
	"insistanan/api-proxy/internal/storage/logger"
)

const compactionRequestKey = "__compaction_request"
const compactionSourceTargetKey = "__compaction_source_target"

type compactionSourceTarget struct {
	channelID string
	model     string
}

// BindCompactionRequest 同时供普通入口与独立 compact 端点使用。
func BindCompactionRequest(c *gin.Context, kind string, body []byte) (contract.CompactionRequest, error) {
	r, err := contract.DetectCompaction(kind, c.Request.URL.Path, c.GetHeader("X-Api-Proxy-Task"), body)
	if err != nil {
		return r, err
	}
	c.Set(compactionRequestKey, r)
	if r.Kind != "" {
		c.Header("X-Api-Proxy-Compaction", r.Kind)
		log.Printf("[Compaction] requestId=%s kind=%s evidence=%s", RequestLogID(c), r.Kind, r.Evidence)
		if state := requestLifecycle(c); state != nil {
			state.CompactionKind, state.CompactionEvidence = r.Kind, r.Evidence
		}
	}
	return r, nil
}

func compactionRequest(c *gin.Context) contract.CompactionRequest {
	if c == nil {
		return contract.CompactionRequest{}
	}
	value, _ := c.Get(compactionRequestKey)
	r, _ := value.(contract.CompactionRequest)
	return r
}

// BeginCompactionLog 为独立 compact 端点补齐与普通入口一致的请求生命周期。
func BeginCompactionLog(c *gin.Context, env *config.EnvConfig, body []byte) func() {
	state := newRequestLifecycle(c, body, time.Now())
	recordRequestLifecycle(c, env, "Responses", logger.PhaseRequestStart, state)
	return func() { finishRequestLifecycle(c, env, "Responses", state) }
}

// ResolveCompactionSource 只读取已有会话路由；摘要不能重新分类、建立会话或改写亲和。
func ResolveCompactionSource(c *gin.Context, cfg *config.ConfigManager, sch *scheduler.ChannelScheduler, kind scheduler.ChannelKind, body []byte, model, conversationID string) (*config.UpstreamConfig, int, error) {
	index, explicit, err := ExtractRequestedChannelIndex(body)
	if err != nil {
		return nil, 0, err
	}
	if conversationID != "" && sch.GetConversationRegistry() != nil {
		record, err := sch.GetConversationRegistry().Lookup(c.Request.Context(), conversationID)
		if err != nil {
			return nil, 0, err
		}
		if record != nil {
			pinned := -1
			name, id := "", ""
			if record.LastResolved != nil && record.LastResolved.Kind == string(kind) {
				pinned, name = record.LastResolved.ChannelIndex, record.LastResolved.ChannelName
			}
			if record.RouteOverride != nil {
				if record.RouteOverride.Kind != string(kind) {
					return nil, 0, fmt.Errorf("压缩请求与会话固定协议不一致")
				}
				pinned, id = record.RouteOverride.ChannelIndex, record.RouteOverride.ChannelID
				if id != "" {
					channels, _, err := cfg.GetChannelConfig(string(kind))
					if err != nil {
						return nil, 0, err
					}
					pinned = -1
					for i := range channels {
						if channels[i].ID == id {
							pinned = i
							break
						}
					}
					if pinned < 0 {
						return nil, 0, fmt.Errorf("主会话固定渠道已不存在，拒绝重新选择压缩来源")
					}
				}
			}
			if pinned >= 0 {
				if explicit && pinned != index {
					return nil, 0, fmt.Errorf("压缩请求不能覆盖主会话渠道")
				}
				up, resolved, err := ResolveRequestedUpstream(cfg, kind, pinned)
				if err != nil {
					return nil, 0, err
				}
				if id != "" && up.ID != id || id == "" && name != "" && up.Name != name {
					return nil, 0, fmt.Errorf("主会话渠道配置已变化，请先恢复主会话路由")
				}
				if compactionRequest(c).Kind == "native" && record.LastResolvedModel != "" {
					last := record.LastResolved
					if last == nil || last.Kind != string(kind) || last.ChannelIndex != resolved || last.ChannelName != "" && last.ChannelName != up.Name {
						return nil, 0, fmt.Errorf("主会话固定渠道与实际模型来源不一致，拒绝重新选择压缩来源")
					}
					// 选渠许可会重新读取配置，实际模型随请求保存，不能被映射首项覆盖。
					c.Set(compactionSourceTargetKey, compactionSourceTarget{channelID: up.ID, model: record.LastResolvedModel})
				}
				return up, resolved, nil
			}
		}
	}
	if explicit {
		return ResolveRequestedUpstream(cfg, kind, index)
	}
	if scheduler.PoolConstraintFrom(c.Request.Context()).PoolID != "" {
		return sch.IntentSingleChannel(c.Request.Context(), kind)
	}
	return cfg.GetCurrentUpstreamWithIndexForModelForKind(string(kind), model)
}

// runIndependentCompactionRequest 复用正常协议交付与计费，不增加主会话轮次或启动缓存保活。
// 只有原生控制允许通过协议回调提交压缩后的历史，文本摘要完全隔离。
func runIndependentCompactionRequest(c *gin.Context, env *config.EnvConfig, cfg *config.ConfigManager, sch *scheduler.ChannelScheduler, spec ProtocolSpec, body []byte, model string, stream bool, start time.Time) {
	native := compactionRequest(c).Kind == "native"
	c.Set(upstreamreq.ContextKeyAuxiliaryTask, !native)
	identity := ResolveConversationIdentity(c, body)
	conversationID, err := ResolveExistingConversationID(c.Request.Context(), sch, spec.Kind, identity)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "CONVERSATION_STORE_ERROR"})
		return
	}
	pipeline.AttachHookPipeline(c, spec.HookPipeline, pipeline.HookContext{
		RedactionScope: RedactionScope(identity, env.Current().ProxyAccessKey), APIType: string(spec.Kind),
		Model: model, Stream: stream, RequestID: RequestLogID(c), ConversationID: conversationID,
	})
	defer pipeline.ClearAttachedSensitiveData(c)
	if err := pipeline.AssignAttachedConversation(c.Request.Context(), c, conversationID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "CONTENT_SAFETY_LOG_ERROR"})
		return
	}
	if err := pipeline.RunAttachedPreRequestHooks(c.Request.Context(), c, c.Request, "", string(spec.Kind)); err != nil {
		handleContentSafetyPreparationError(c, spec.LogName, err)
		return
	}
	if prepared, ok := pipeline.PreparedRequestBody(c.Request); ok {
		body = prepared
	}
	RestoreRequestBody(c, body)
	if state := requestLifecycle(c); state != nil {
		state.Model, state.Stream, state.ConversationID = model, stream, conversationID
		recordRequestLifecycle(c, env, spec.LogName, logger.PhaseRequestStart, state)
	}
	LogOriginalRequest(c, body, env, spec.LogName)
	owner, _, err := ResolveCompactionSource(c, cfg, sch, spec.Kind, body, model, conversationID)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "COMPACTION_SOURCE_UNAVAILABLE"})
		return
	}
	if native {
		c.Set(upstreamreq.ContextKeyConversationUserID, conversationID)
		if service := intent.FromContext(c.Request.Context()); service != nil && cfg.GetIntentRouting(string(spec.Kind)).Enabled {
			ctx, release, err := service.BeginProtocolControl(c.Request.Context(), string(spec.Kind), conversationID, RequestLogID(c), body)
			if err != nil {
				intentRouteError(c, err)
				return
			}
			defer release()
			c.Request = c.Request.WithContext(ctx)
		}
	}
	usePool := !native && owner.Compaction.SummaryPool &&
		(compactionRequest(c).Evidence == "X-Api-Proxy-Task" || owner.Compaction.DetectClientPrompts)
	upstreams := []config.UpstreamConfig{*owner}
	if usePool {
		all, _, err := cfg.GetChannelConfig(string(spec.Kind))
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		var indices []int
		upstreams = nil
		for index, up := range all {
			if up.ExcludeFromConversation && up.Compaction.SummaryCapable && config.GetChannelStatus(&up) == config.ChannelStatusActive {
				indices = append(indices, index)
			}
		}
		sort.SliceStable(indices, func(i, j int) bool { return all[indices[i]].Priority < all[indices[j]].Priority })
		for _, index := range indices {
			upstreams = append(upstreams, all[index])
		}
	}
	var lastErr error
	for offset, candidate := range upstreams {
		if c.Request.Context().Err() != nil {
			return
		}
		selection, err := sch.SelectMappedChannel(spec.Kind, candidate.ID)
		if err != nil {
			lastErr = err
			log.Printf("[Compaction] 压缩渠道不可用: %v", err)
			continue
		}
		defer selection.Permit.Release()
		up := selection.Upstream
		if usePool {
			if !up.ExcludeFromConversation || !up.Compaction.SummaryCapable || up.Compaction.SummaryModel == "" {
				selection.Permit.RecordHealthNeutral()
				selection.Permit.Release()
				lastErr = fmt.Errorf("摘要池配置已变化，请重试")
				continue
			}
			up.DefaultModel, up.ModelMapping, up.ModelMappingChannels = up.Compaction.SummaryModel, nil, nil
		}
		if !native {
			up.EnablePreviousResponseID = false
		}
		c.Header("X-Api-Proxy-Compaction-Route", "source")
		if usePool {
			c.Header("X-Api-Proxy-Compaction-Route", "summary_pool")
		}
		log.Printf("[Compaction] requestId=%s source=%s target=%s model=%s attempt=%d", RequestLogID(c), owner.ID, up.ID, config.ResolveUpstreamModel(model, up), offset+1)
		urls := BuildDefaultURLResults(up.GetAllBaseURLs())
		if native {
			if preferred, ok := sch.GetPreferredBaseURL(conversationID); ok {
				urls = scheduler.PreferBaseURLInResults(urls, preferred)
			}
		}
		attempt := buildProtocolAttempt(c, env, cfg, sch, spec, body, model, stream, "", up, selection.ChannelIndex, start,
			urls, spec.Kind, nil, nil, false)
		attempt.Permit, attempt.SkipVisionLayer = selection.Permit, true
		result := attempt.TryWithModelMappingFailover()
		recordChannelHealth(sch, spec.Kind, selection.ChannelIndex, result.Health, true, spec.LogName, selection.Permit)
		selection.Permit.Release()
		if result.Handled || c.Writer.Written() {
			return
		}
		lastErr = result.LastError
		if !usePool {
			HandleAllKeysFailed(c, false, result.FailoverError, result.LastError, spec.LogName)
			return
		}
		if lastErr == nil && result.FailoverError != nil {
			lastErr = fmt.Errorf("摘要渠道 %q 返回 HTTP %d", up.Name, result.FailoverError.Status)
		}
		if result.AbortFailover {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("当前入口协议的摘要池没有可用渠道，请加入渠道并指定摘要模型")
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": lastErr.Error(), "code": "COMPACTION_UNAVAILABLE"})
}

// prepareCompactionAttempt 必须在普通模型映射之前执行，禁止原生控制跨厂商转换或映射故障转移。
func (a UpstreamAttempt) prepareCompactionAttempt() (UpstreamAttempt, error) {
	r := compactionRequest(a.Context)
	if !r.Native() {
		return a, nil
	}
	native := map[scheduler.ChannelKind]string{scheduler.ChannelKindMessages: "claude", scheduler.ChannelKindResponses: "responses"}
	if a.Upstream.ServiceType != native[a.Kind] || a.Upstream.StripThoughtSignature || a.Upstream.InjectDummyThoughtSignature {
		return a, fmt.Errorf("原生压缩及其历史必须保持原生协议和签名，不能转入文本摘要池")
	}
	models, channels := config.ResolveUpstreamModelTargets(a.RequestedModel, a.Upstream)
	if len(channels) > 0 && channels[0] != "" {
		return a, fmt.Errorf("原生压缩不能使用跨渠道模型映射")
	}
	model := config.ResolveUpstreamModel(a.RequestedModel, a.Upstream)
	if len(models) > 0 {
		model = models[0]
	}
	if value, exists := a.Context.Get(compactionSourceTargetKey); exists {
		source := value.(compactionSourceTarget)
		if source.channelID != a.Upstream.ID {
			return a, fmt.Errorf("压缩渠道与主会话实际模型来源不一致")
		}
		model = source.model
	}
	if turn := intent.TurnFrom(a.Context.Request.Context()); turn != nil {
		// 意图会话保存的是已交付目标；优先于仅记录最近尝试的会话观测。
		if previous := turn.PinnedModel(a.Upstream.ID); previous != "" {
			model = previous
		}
	}
	cfg := a.Upstream.Compaction
	if r.Kind == "native" && cfg.NativeModel != "" {
		if !cfg.NativeCompatibilityConfirmed || len(a.Upstream.APIKeys) != 1 || len(a.Upstream.GetAllBaseURLs()) != 1 || model != cfg.NativeSourceModel {
			return a, fmt.Errorf("原生压缩模型替换不符合已确认的来源与模型对，拒绝改写")
		}
		model = cfg.NativeModel
		a.Context.Header("X-Api-Proxy-Compaction-Route", "native_model")
		log.Printf("[Compaction] requestId=%s channel=%s nativeModel=%s", RequestLogID(a.Context), a.Upstream.ID, model)
	}
	a.Upstream = a.Upstream.Clone()
	a.Upstream.DefaultModel, a.Upstream.ModelMapping, a.Upstream.ModelMappingChannels = model, nil, nil
	a.AllowModelFailover, a.SkipVisionLayer = false, true
	return a, nil
}

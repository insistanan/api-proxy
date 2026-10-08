// 本文件是单渠道内"模型映射层"故障转移的入口：把请求模型解析成候选上游模型序列
// （多候选按 modelMapping 的显式数组顺序，AllowModelFailover 为假时只保留首个），逐个交给
// Key/BaseURL 层循环（upstream_attempt_keys.go）尝试。
// 附带 BaseURL 候选列表的构造（BuildDefaultURLResults）与对话粘性偏好
// （preferConversationBaseURL）。
package proxycore

import (
	"context"
	"log"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/urlhealth"
)

// TryWithModelMappingFailover 按模型映射执行上游故障转移。
func (a UpstreamAttempt) TryWithModelMappingFailover() UpstreamAttemptResult {
	return a.tryWithModelMappingFailover()
}

// tryWithModelMappingFailover 在模型映射级别进行 failover。
// 非 Fuzzy 模式下仅尝试首个映射模型，避免跨模型故障转移。
func (a UpstreamAttempt) tryWithModelMappingFailover() UpstreamAttemptResult {
	// 获取该模型的映射列表
	targetModels := config.ResolveUpstreamModelList(a.RequestedModel, a.Upstream)

	if len(targetModels) == 0 {
		// 没有映射，直接使用原始模型
		targetModels = []string{a.RequestedModel}
	}
	if !a.AllowModelFailover && len(targetModels) > 1 {
		targetModels = targetModels[:1]
	}

	// 如果只有一个目标模型，直接调用原有逻辑
	if len(targetModels) == 1 {
		return a.tryWithAllKeys()
	}

	// 多个目标模型：依次尝试
	log.Printf("[%s-ModelMapping] 模型 %s 映射到 %d 个备选: %v", a.APIType, a.RequestedModel, len(targetModels), targetModels)

	var lastFailoverError *FailoverError
	var lastErr error

	for modelIdx, targetModel := range targetModels {
		// 检查客户端是否已取消
		select {
		case <-a.Context.Request.Context().Done():
			log.Printf("[%s-Cancel] 客户端已取消，停止模型 failover", a.APIType)
			// 客户端明确取消：中性结束，立即停止。
			return newUpstreamAttemptResult(true, "", 0, nil, nil, context.Canceled).withHealth(channelHealthNeutral)
		default:
		}

		log.Printf("[%s-ModelMapping] 尝试备选模型 %d/%d: %s -> %s",
			a.APIType, modelIdx+1, len(targetModels), a.RequestedModel, targetModel)

		// 创建上游副本，临时覆盖模型映射为当前尝试的单一模型
		upstreamCopy := a.Upstream.Clone()
		upstreamCopy.ModelMapping = map[string][]string{
			a.RequestedModel: {targetModel},
		}

		modelAttempt := a
		modelAttempt.Upstream = upstreamCopy
		result := modelAttempt.tryWithAllKeys()

		if result.Handled {
			if result.SuccessKey != "" {
				// 成功
				log.Printf("[%s-ModelMapping] 模型 %s (备选 %d/%d) 请求成功",
					a.APIType, targetModel, modelIdx+1, len(targetModels))
				return result
			}
			// handled=true 但 successKey 为空：非 failover 错误（如客户端取消、参数错误等）
			return result
		}

		// 未处理（failover 错误），保存错误信息并尝试下一个模型
		if result.FailoverError != nil {
			lastFailoverError = result.FailoverError
		}
		if result.LastError != nil {
			lastErr = result.LastError
		}

		log.Printf("[%s-ModelMapping] 模型 %s (备选 %d/%d) 失败，尝试下一个备选模型",
			a.APIType, targetModel, modelIdx+1, len(targetModels))
	}

	// 所有模型都失败
	log.Printf("[%s-ModelMapping] 所有 %d 个备选模型都失败", a.APIType, len(targetModels))
	// 可重试故障耗尽全部候选模型：渠道按失败记账。
	return newUpstreamAttemptResult(false, "", 0, lastFailoverError, nil, lastErr).withHealth(channelHealthFailure)
}

func preferConversationBaseURL(
	channelScheduler *scheduler.ChannelScheduler,
	envCfg *config.EnvConfig,
	apiType string,
	logCtx AttemptLogContext,
	urlResults []urlhealth.URLLatencyResult,
) []urlhealth.URLLatencyResult {
	if channelScheduler == nil || logCtx.ConversationID == "" {
		return urlResults
	}
	preferredBaseURL, ok := channelScheduler.GetPreferredBaseURL(logCtx.ConversationID)
	if !ok {
		return urlResults
	}
	if envCfg != nil && envCfg.ShouldLog("info") {
		log.Printf("[%s-BaseURL-Affinity] conversation sticky prefer %s", apiType, preferredBaseURL)
	}
	return scheduler.PreferBaseURLInResults(urlResults, preferredBaseURL)
}

// BuildDefaultURLResults 将 URLs 转为按原始顺序的结果列表（无动态排序）
func BuildDefaultURLResults(urls []string) []urlhealth.URLLatencyResult {
	results := make([]urlhealth.URLLatencyResult, len(urls))
	for i, url := range urls {
		results[i] = urlhealth.URLLatencyResult{
			URL:         url,
			OriginalIdx: i,
			Success:     true,
		}
	}
	return results
}

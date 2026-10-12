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
// 每个候选是（目标模型，可选的显式目标渠道）：未指定渠道的候选留在本渠道内执行；
// 指定了渠道的候选交给目标渠道单跳执行（见 upstream_attempt_mapped.go）。
// 非 Fuzzy 模式下仅尝试首个映射候选，避免跨模型故障转移。
func (a UpstreamAttempt) tryWithModelMappingFailover() UpstreamAttemptResult {
	// 目标模型与目标渠道来自同一条映射规则，保证二者始终对应
	targetModels, targetChannelIDs := config.ResolveUpstreamModelTargets(a.RequestedModel, a.Upstream)

	if len(targetModels) == 0 {
		// 没有映射，直接使用原始模型
		targetModels = []string{a.RequestedModel}
		targetChannelIDs = []string{""}
	}
	if !a.AllowModelFailover && len(targetModels) > 1 {
		targetModels = targetModels[:1]
		targetChannelIDs = targetChannelIDs[:1]
	}

	// 只有一个本地目标：直接调用原有逻辑
	if len(targetModels) == 1 && targetChannelIDs[0] == "" {
		return a.tryWithAllKeys()
	}

	// 多个候选，或单个显式目标渠道：依次尝试
	log.Printf("[%s-ModelMapping] 模型 %s 映射到 %d 个备选: %v", a.APIType, a.RequestedModel, len(targetModels), targetModels)

	var lastFailoverError *FailoverError
	var lastErr error
	// localAttempted：本渠道自身是否发起过本地候选尝试；mappedRouted：是否有候选转交目标渠道。
	// 二者决定源渠道的健康结论（见 mappingSourceHealth）。
	localAttempted := false
	mappedRouted := false

	for modelIdx, targetModel := range targetModels {
		channelID := targetChannelIDs[modelIdx]

		// 检查客户端是否已取消
		select {
		case <-a.Context.Request.Context().Done():
			log.Printf("[%s-Cancel] 客户端已取消，停止模型 failover", a.APIType)
			// 客户端明确取消：中性结束，立即停止。
			return newUpstreamAttemptResult(true, "", 0, nil, nil, context.Canceled).withHealth(channelHealthNeutral)
		default:
		}

		var result UpstreamAttemptResult
		if channelID == "" {
			log.Printf("[%s-ModelMapping] 尝试备选模型 %d/%d: %s -> %s",
				a.APIType, modelIdx+1, len(targetModels), a.RequestedModel, targetModel)
			localAttempted = true

			// 创建上游副本，临时覆盖模型映射为当前尝试的单一模型。
			// 渠道登记同样清空：本地候选不再解析映射登记（单跳）。
			upstreamCopy := a.Upstream.Clone()
			upstreamCopy.ModelMapping = map[string][]string{
				a.RequestedModel: {targetModel},
			}
			upstreamCopy.ModelMappingChannels = nil

			modelAttempt := a
			modelAttempt.Upstream = upstreamCopy
			result = modelAttempt.tryWithAllKeys()
			if result.Handled {
				if result.SuccessKey != "" {
					log.Printf("[%s-ModelMapping] 模型 %s (备选 %d/%d) 请求成功",
						a.APIType, targetModel, modelIdx+1, len(targetModels))
				}
				// handled=true：成功，或非 failover 错误（如客户端取消、参数错误等）
				return result
			}
		} else {
			mappedRouted = true
			mappedResult, err := a.tryMappedChannelTarget(targetModel, channelID)
			if err != nil {
				lastErr = err
				log.Printf("[%s-ModelMapping] 映射目标渠道不可用，模型 %s -> %s: %v",
					a.APIType, a.RequestedModel, targetModel, err)
				if !a.ConfigManager.GetFuzzyModeEnabled() {
					// 非 Fuzzy：目标渠道不可用直接报错，不转入正常调度的下一渠道
					return newUpstreamAttemptResult(false, "", 0, nil, nil, err).
						withHealth(mappingSourceHealth(localAttempted, mappedRouted)).
						withAbortFailover()
				}
				// Fuzzy：继续下一个候选，全部耗尽后由外层转入正常调度的下一渠道
				continue
			}
			result = mappedResult
			if result.Handled {
				// 目标渠道已写回响应：源渠道健康结论由映射路径决定，目标渠道已自行记账
				return result.withHealth(mappingSourceHealth(localAttempted, mappedRouted))
			}
		}

		// 未处理（failover 错误），保存错误信息并尝试下一个候选
		if result.FailoverError != nil {
			lastFailoverError = result.FailoverError
		}
		if result.LastError != nil {
			lastErr = result.LastError
		}

		log.Printf("[%s-ModelMapping] 模型 %s (备选 %d/%d) 失败，尝试下一个备选模型",
			a.APIType, targetModel, modelIdx+1, len(targetModels))
	}

	// 所有候选都失败
	log.Printf("[%s-ModelMapping] 所有 %d 个备选模型都失败", a.APIType, len(targetModels))
	// 可重试故障耗尽全部候选：源渠道的健康结论按参与方式决定。
	return newUpstreamAttemptResult(false, "", 0, lastFailoverError, nil, lastErr).
		withHealth(mappingSourceHealth(localAttempted, mappedRouted))
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

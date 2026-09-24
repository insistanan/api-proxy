// 本文件是单渠道内尝试的入口与 BaseURL 候选构造。
// 渠道内的尝试层级（Key → 模型候选 → 重试次数）实现在 upstream_attempt_keys.go，
// 本文件只保留对外入口与模型候选覆盖辅助（applyRequestedModelOverride）。
// 附带 BaseURL 候选列表的构造（BuildDefaultURLResults）与对话粘性偏好
// （preferConversationBaseURL）。
package proxycore

import (
	"log"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/urlhealth"
)

// TryWithModelMappingFailover 执行一次渠道内的上游尝试。
func (a UpstreamAttempt) TryWithModelMappingFailover() UpstreamAttemptResult {
	return a.tryWithAllKeys()
}

// applyRequestedModelOverride 把当前尝试的模型候选固化到上游配置上：
// 覆盖模型映射，避免 Provider 在协议转换阶段再次重定向到别的模型。
// 模型候选的遍历顺序由 tryWithAllKeys 的 Key → 模型 → 重试分层负责。
func applyRequestedModelOverride(upstream *config.UpstreamConfig, requestedModel string, targetModel string) {
	if upstream == nil || targetModel == "" {
		return
	}
	if requestedModel == "" {
		requestedModel = targetModel
	}
	upstream.ModelMapping = map[string][]string{
		requestedModel: {targetModel},
	}
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

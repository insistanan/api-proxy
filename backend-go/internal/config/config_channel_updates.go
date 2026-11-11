// 渠道更新：在候选快照上应用字段变更，并保留原子提交前的校验顺序。
package config

import (
	"fmt"
	"strings"
	"time"
)

// applyCommonUpdates 应用通用的 UpstreamUpdate 字段到上游配置
// 返回 shouldResetMetrics 标识是否需要重置指标
func applyCommonUpdates(upstream *UpstreamConfig, index int, updates UpstreamUpdate, label string) (bool, error) {
	if updates.MaxConcurrency != nil {
		if *updates.MaxConcurrency < 1 {
			return false, fmt.Errorf("最大并发必须是正整数")
		}
		upstream.MaxConcurrency = *updates.MaxConcurrency
	}
	if updates.ClientIdentityMode != nil {
		mode := strings.TrimSpace(*updates.ClientIdentityMode)
		if mode != "" && mode != "inherit" && mode != "api" && mode != "compatibility" {
			return false, fmt.Errorf("客户端身份模式无效")
		}
		upstream.ClientIdentityMode = mode
	}
	if updates.VisionCapable != nil && *updates.VisionCapable &&
		updates.VisionLayerEnabled != nil && *updates.VisionLayerEnabled {
		return false, fmt.Errorf("渠道不能同时标记为支持图片理解并启用图片理解层")
	}
	shouldResetMetrics := false

	if updates.Name != nil {
		upstream.Name = *updates.Name
	}
	if updates.PoolID != nil {
		upstream.PoolID = strings.TrimSpace(*updates.PoolID)
	}
	if updates.BaseURL != nil {
		upstream.BaseURL = *updates.BaseURL
		if updates.BaseURLs == nil {
			upstream.BaseURLs = nil
		}
	}
	if updates.UseFullURL != nil {
		upstream.UseFullURL = *updates.UseFullURL
	}
	if updates.BaseURLs != nil {
		upstream.BaseURLs = deduplicateBaseURLs(updates.BaseURLs, upstream.UseFullURL)
	}
	if updates.ServiceType != nil {
		upstream.ServiceType = *updates.ServiceType
	}
	if updates.ResponsesModel != nil {
		upstream.ResponsesModel = strings.TrimSpace(*updates.ResponsesModel)
	}
	if updates.ImagesExtraParams != nil {
		normalized, err := normalizeImagesExtraParams(updates.ImagesExtraParams)
		if err != nil {
			return false, err
		}
		upstream.ImagesExtraParams = normalized
	}
	if updates.ImagesForceNonStream != nil {
		upstream.ImagesForceNonStream = *updates.ImagesForceNonStream
	}
	if updates.ImagesOutputFormat != nil {
		upstream.ImagesOutputFormat = strings.TrimSpace(*updates.ImagesOutputFormat)
	}
	if updates.ImagesFrameCount != nil {
		upstream.ImagesFrameCount = *updates.ImagesFrameCount
	}
	if updates.ImagesFrameDurationMS != nil {
		upstream.ImagesFrameDurationMS = *updates.ImagesFrameDurationMS
	}
	if updates.Description != nil {
		upstream.Description = *updates.Description
	}
	if updates.Website != nil {
		upstream.Website = *updates.Website
	}
	if updates.APIKeys != nil {
		shouldResetMetrics = applyAPIKeysUpdate(upstream, index, updates.APIKeys, label)
	}
	if updates.ModelMapping != nil {
		upstream.ModelMapping = normalizeModelMapping(updates.ModelMapping)
		// 映射整体替换时，目标渠道登记以请求为准（未传则清空），再按新映射修剪。
		upstream.ModelMappingChannels = normalizeModelMappingChannels(updates.ModelMappingChannels, upstream.ModelMapping)
	} else if updates.ModelMappingChannels != nil {
		upstream.ModelMappingChannels = normalizeModelMappingChannels(updates.ModelMappingChannels, upstream.ModelMapping)
	}
	if updates.DefaultModel != nil {
		upstream.DefaultModel = strings.TrimSpace(*updates.DefaultModel)
	}
	if updates.InsecureSkipVerify != nil {
		upstream.InsecureSkipVerify = *updates.InsecureSkipVerify
	}
	if updates.ProxyMode != nil {
		upstream.ProxyMode = *updates.ProxyMode
	}
	if updates.ProxyURL != nil {
		upstream.ProxyURL = *updates.ProxyURL
	}
	normalizeUpstreamProxyConfig(upstream)
	if err := validateUpstreamProxyConfig(upstream); err != nil {
		return false, err
	}
	if updates.Priority != nil {
		upstream.Priority = *updates.Priority
	}
	if updates.Status != nil {
		if err := setUpstreamStatus(upstream, *updates.Status, time.Now()); err != nil {
			return false, err
		}
		// 编辑表单显式设为活跃时，也需要解除独立于配置状态的运行时熔断。
		if upstream.Status == ChannelStatusActive {
			shouldResetMetrics = true
		}
	}
	if updates.PromotionUntil != nil {
		upstream.PromotionUntil = updates.PromotionUntil
	}
	if updates.PromotionCount != nil {
		upstream.PromotionCount = *updates.PromotionCount
	}
	if updates.LowQuality != nil {
		upstream.LowQuality = *updates.LowQuality
	}
	if updates.VisionCapable != nil {
		upstream.VisionCapable = *updates.VisionCapable
		if *updates.VisionCapable {
			upstream.VisionLayerEnabled = false
			upstream.VisionLayerChannelID = ""
			upstream.VisionLayerModel = ""
		}
	}
	if updates.ExcludeFromConversation != nil {
		upstream.ExcludeFromConversation = *updates.ExcludeFromConversation
	}
	if updates.VisionLayerEnabled != nil {
		upstream.VisionLayerEnabled = *updates.VisionLayerEnabled
		if *updates.VisionLayerEnabled {
			upstream.VisionCapable = false
		} else {
			upstream.VisionLayerChannelID = ""
			upstream.VisionLayerModel = ""
		}
	}
	if updates.VisionLayerChannelID != nil {
		upstream.VisionLayerChannelID = strings.TrimSpace(*updates.VisionLayerChannelID)
	}
	if updates.VisionLayerModel != nil {
		upstream.VisionLayerModel = strings.TrimSpace(*updates.VisionLayerModel)
	}
	if updates.Temporary != nil {
		upstream.Temporary = *updates.Temporary
	}
	if updates.TemporaryUntil != nil {
		upstream.TemporaryUntil = updates.TemporaryUntil
	}
	if updates.DeprecatedAt != nil {
		upstream.DeprecatedAt = updates.DeprecatedAt
	}
	prepareUpstreamLifecycle(upstream, time.Now())
	if updates.InjectDummyThoughtSignature != nil {
		upstream.InjectDummyThoughtSignature = *updates.InjectDummyThoughtSignature
	}
	if updates.StripThoughtSignature != nil {
		upstream.StripThoughtSignature = *updates.StripThoughtSignature
	}
	if updates.IncludeHistoryThinking != nil {
		upstream.IncludeHistoryThinking = *updates.IncludeHistoryThinking
	}
	if updates.DisablePromptCacheInjection != nil {
		upstream.DisablePromptCacheInjection = *updates.DisablePromptCacheInjection
	}
	if updates.DisablePromptCacheKey != nil {
		upstream.DisablePromptCacheKey = *updates.DisablePromptCacheKey
	}
	if updates.RequireReasoningContent != nil {
		upstream.RequireReasoningContent = *updates.RequireReasoningContent
	}
	if updates.ReasoningOutputMode != nil {
		upstream.ReasoningOutputMode = *updates.ReasoningOutputMode
	}
	if updates.ParseEmbeddedReasoning != nil {
		upstream.ParseEmbeddedReasoning = *updates.ParseEmbeddedReasoning
	}
	if err := ValidateReasoningOptions(upstream); err != nil {
		return false, err
	}
	if updates.EnablePreviousResponseID != nil {
		upstream.EnablePreviousResponseID = *updates.EnablePreviousResponseID
	}
	return shouldResetMetrics, nil
}

// applyCommonUpdatesToList 应用渠道更新，并在状态池发生变化时同步调整优先级。
func applyCommonUpdatesToList(upstreams []UpstreamConfig, pools []ChannelPool, index int, updates UpstreamUpdate, label string) (bool, error) {
	original := upstreams[index].Clone()
	previousPool := channelStatusPool(&upstreams[index])
	shouldResetMetrics, err := applyCommonUpdates(&upstreams[index], index, updates, label)
	if err != nil {
		upstreams[index] = *original
		return false, err
	}
	if err := ValidateChannelPoolAssignment(pools, upstreams[index].PoolID); err != nil {
		upstreams[index] = *original
		return false, err
	}
	if err := validateAllVisionLayerConfigs(upstreams); err != nil {
		upstreams[index] = *original
		return false, err
	}
	if err := validateAllModelMappingChannels(upstreams); err != nil {
		upstreams[index] = *original
		return false, err
	}
	if previousPool != channelStatusPool(&upstreams[index]) {
		moveChannelToStatusPoolTail(upstreams, index)
	}
	return shouldResetMetrics, nil
}

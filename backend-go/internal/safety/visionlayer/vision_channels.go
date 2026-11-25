package visionlayer

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
)

func describeImages(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	targetPoolID string,
	visionChannelID string,
	visionModelInput string,
	conversationID string,
	profile visionAnalysisProfile,
	images []visionImage,
) (map[string]string, error) {
	var lastErr error
	requestContext := c.Request.Context()
	failoverEnabled := cfgManager.GetFuzzyModeEnabledForProtocol(string(kind))
	if err := requestContext.Err(); err != nil {
		return nil, err
	}

	// 显式配置的图片理解渠道始终优先；默认模式直接从公共池开始。
	if visionChannelID != "" {
		selection, err := channelScheduler.SelectVisionChannel(requestContext, kind, visionChannelID, targetPoolID)
		if err == nil {
			result := describeImagesOnChannel(c, envCfg, cfgManager, channelScheduler, selection, visionModelInput, conversationID, profile, images, 2)
			if result.err == nil {
				return result.descriptions, nil
			}
			if !result.retryable || !failoverEnabled {
				return nil, result.err
			}
			lastErr = result.err
		} else {
			lastErr = err
			if !failoverEnabled {
				return nil, err
			}
		}
	}

	// 公共池优先；全部可重试失败后，再尝试文本渠道所在分组。
	stages := []func() []*scheduler.SelectionResult{
		func() []*scheduler.SelectionResult {
			return channelScheduler.ListPublicVisionChannels(requestContext, visionChannelID)
		},
		func() []*scheduler.SelectionResult {
			return channelScheduler.ListPoolVisionChannels(requestContext, kind, visionChannelID, targetPoolID)
		},
	}
	for _, loadCandidates := range stages {
		if err := requestContext.Err(); err != nil {
			return nil, err
		}
		candidates := loadCandidates()
		for _, candidate := range candidates {
			// 预检不能代替正式准入；尚未尝试的后备渠道不占预留或探测名额。
			selection, err := channelScheduler.AdmitChannelCandidate(requestContext, candidate)
			if scheduler.IsCandidateUnavailable(err) {
				lastErr = err
				continue
			}
			if err != nil {
				return nil, err
			}
			result := describeImagesOnChannel(c, envCfg, cfgManager, channelScheduler, selection, visionModelInput, conversationID, profile, images, 2)
			if result.err == nil {
				return result.descriptions, nil
			}
			lastErr = result.err
			if !result.retryable || !failoverEnabled {
				return nil, result.err
			}
		}
	}

	if lastErr == nil {
		return nil, fmt.Errorf("公共图片理解池和当前分组中没有可用的图片理解渠道")
	}
	return nil, fmt.Errorf("所有可用图片理解渠道均失败: %w", lastErr)
}

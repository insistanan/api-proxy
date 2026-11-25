package visionlayer

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
)

// describeImagesOnChannel 接管许可，以同一渠道快照完成模型解析、缓存和发送。
// 仅真实上游调用参与健康记账，缓存命中和等待其他请求的结果按中性释放。
func describeImagesOnChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	selection *scheduler.SelectionResult,
	visionModelInput string,
	conversationID string,
	profile visionAnalysisProfile,
	images []visionImage,
	maxFailures int,
) (result visionAttemptResult) {
	ctx := c.Request.Context()
	descriptions := make(map[string]string, len(images))
	pendingImages := make([]visionImage, 0, len(images))
	waitingImages := make([]visionImage, 0, len(images))
	remainingImages := images
	var cacheStore visionCacheStore
	if registry := channelScheduler.GetConversationRegistry(); registry != nil {
		cacheStore = registry
	}
	defer selection.Permit.Release()
	defer func() {
		if result.err != nil {
			// 包括收集后续图片时的缓存错误；已完成的占位不会被重复改写。
			failPendingAnalyses(pendingImages, visionAnalysisResult{
				err: result.err, retryable: result.retryable, ownerCanceled: ctx.Err() != nil,
			})
		}
		if ctx.Err() != nil {
			selection.Permit.RecordHealthNeutral()
			return
		}
		switch result.health {
		case visionHealthSuccess:
			selection.Permit.RecordHealthSuccess()
		case visionHealthFailure:
			selection.Permit.RecordHealthFailure()
		default:
			selection.Permit.RecordHealthNeutral()
		}
	}()
	if err := ctx.Err(); err != nil {
		return visionAttemptResult{err: err}
	}
	visionModel := config.ResolveUpstreamModel(visionModelInput, selection.Upstream)
	if visionModel == "" {
		return visionAttemptResult{err: fmt.Errorf("图片理解渠道 %q 未能解析可用模型", selection.Upstream.Name)}
	}
	for len(remainingImages) > 0 {
		pendingImages = pendingImages[:0]
		waitingImages = waitingImages[:0]
		for _, image := range remainingImages {
			if err := ctx.Err(); err != nil {
				result.err = err
				return result
			}
			image.cacheKey = buildImageCacheKey(image.fingerprint, selection.Upstream, visionModel, profile, images)
			image.memoryKey = memoryCacheKey(conversationID, image.cacheKey)
			cached, cachedOK, call, owner := claimAnalysis(image.memoryKey)
			if cachedOK {
				descriptions[image.id] = cached
				continue
			}
			image.call = call
			if !owner {
				waitingImages = append(waitingImages, image)
				continue
			}
			// 先登记占位再读持久层，读失败也能由 defer 通知全部等待者。
			pendingImages = append(pendingImages, image)
			cached, cachedOK, err := loadCache(cacheStore, conversationID, image)
			if err != nil {
				result.err = wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_CACHE_ERROR", fmt.Errorf("读取图片理解缓存失败: %w", err))
				return result
			}
			if cachedOK {
				finishAnalysis(image.memoryKey, call, visionAnalysisResult{result: cached})
				descriptions[image.id] = cached
				pendingImages = pendingImages[:len(pendingImages)-1]
			}
		}

		// 先完成自己持有的图片，再等待其他图片，避免不同批次互相等待。
		if len(pendingImages) > 0 {
			// 即使部分命中缓存或由其他请求持有，也发送缓存键对应的完整批次。
			// 只发送缺失图片会丢失比较上下文，使同一个键代表不同的分析输入。
			result = requestImageDescriptions(c, envCfg, cfgManager, channelScheduler, selection, visionModel, profile, images, maxFailures)
			if result.err != nil {
				return result
			}
			for _, image := range pendingImages {
				description, ok := result.descriptions[image.id]
				if !ok {
					result.err = wrapRequestError(http.StatusBadGateway, "VISION_LAYER_INVALID_RESPONSE", fmt.Errorf("图片理解结果缺少编号 %s", image.id))
					result.health = visionHealthFailure
					return result
				}
				if err := storeCache(cacheStore, conversationID, image, description); err != nil {
					result.err = wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_CACHE_ERROR", fmt.Errorf("保存图片理解缓存失败: %w", err))
					return result
				}
				finishAnalysis(image.memoryKey, image.call, visionAnalysisResult{result: description})
				descriptions[image.id] = description
			}
		}
		remainingImages = nil
		for _, waiter := range waitingImages {
			select {
			case <-waiter.call.done:
				if err := ctx.Err(); err != nil {
					result.err = err
					return result
				}
				if waiter.call.ownerCanceled {
					// 只接手被发起者取消的分析；真实上游失败仍沿用原重试决策。
					// 新一轮重新争取占位，多个等待者仍只会有一个发起上游调用。
					remainingImages = append(remainingImages, waiter)
					continue
				}
				if waiter.call.err != nil {
					result.err = waiter.call.err
					result.retryable = waiter.call.retryable
					return result
				}
				descriptions[waiter.id] = waiter.call.result
			case <-ctx.Done():
				result.err = ctx.Err()
				return result
			}
		}
	}
	result.descriptions = descriptions
	return result
}

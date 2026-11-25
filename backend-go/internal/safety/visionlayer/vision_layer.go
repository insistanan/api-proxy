// Package visionlayer 为不支持图片的渠道提供受控的图片理解层。
package visionlayer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/visionmedia"
)

// PrepareRequest 在 Provider 完成协议转换后处理最终上游请求。
// 原始客户端请求保持不变；纯文本上游只接收带位置标识的图片理解文本。
func PrepareRequest(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	targetUpstream *config.UpstreamConfig,
	requestedModel string,
	conversationID string,
	request *http.Request,
) error {
	if kind == scheduler.ChannelKindImages {
		// Images 入口里的图片是生图/图生图素材，不是对话里需要理解的内容。
		return nil
	}
	if request == nil || request.Body == nil || targetUpstream == nil || targetUpstream.VisionCapable {
		return nil
	}
	if contentType := request.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil {
			return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", fmt.Errorf("解析最终上游请求媒体类型失败: %w", err))
		}
		if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
			// 非 JSON 请求在读取正文前跳过，不能用解析失败来判断协议。
			return nil
		}
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", fmt.Errorf("读取最终上游请求失败: %w", err))
	}
	request.Body = io.NopCloser(bytes.NewReader(body))

	var payload interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", fmt.Errorf("解析最终上游请求 JSON 失败: %w", err))
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", fmt.Errorf("最终上游请求包含多个顶层 JSON 值"))
		}
		return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", fmt.Errorf("解析最终上游请求 JSON 尾部失败: %w", err))
	}
	if _, ok := payload.(map[string]interface{}); !ok {
		return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", fmt.Errorf("最终上游请求 JSON 必须是对象"))
	}
	images, err := collectImagesForUpstream(payload, targetUpstream.ServiceType)
	if err != nil {
		if !visionmedia.ValueHasVisionContent(payload) {
			return nil
		}
		return err
	}
	if len(images) == 0 {
		return nil
	}
	visionChannelID, err := preferredVisionChannelID(targetUpstream)
	if err != nil {
		return err
	}
	visionModelInput := strings.TrimSpace(targetUpstream.VisionLayerModel)
	if visionModelInput == "" {
		visionModelInput = strings.TrimSpace(requestedModel)
	}
	if visionModelInput == "" {
		return fmt.Errorf("渠道 %q 未提供可供图片理解渠道解析的模型名", targetUpstream.Name)
	}
	profile := resolveVisionAnalysisProfile(extractUserText(payload))

	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", fmt.Errorf("图片理解层缺少对话标识"))
	}

	descriptions := make(map[string]string, len(images))
	pendingImages := make([]visionImage, 0, len(images))
	pendingFingerprints := make(map[string]struct{}, len(images))
	// 先校验并去重全部图片，缓存与分析占位在选定渠道、解析实际模型后处理。
	for _, image := range images {
		fingerprint := visionmedia.ImageFingerprintForBlock(image)
		if fingerprint == "" {
			return fmt.Errorf("无法为图片理解层中的图片生成指纹")
		}
		if _, exists := pendingFingerprints[fingerprint]; exists {
			continue
		}
		pendingFingerprints[fingerprint] = struct{}{}
		pendingImages = append(pendingImages, visionImage{
			id:          fmt.Sprintf("image_%d", len(pendingImages)+1),
			fingerprint: fingerprint,
			block:       image,
		})
	}

	for start := 0; start < len(pendingImages); start += maxVisionBatchImages {
		end := start + maxVisionBatchImages
		if end > len(pendingImages) {
			end = len(pendingImages)
		}
		batch := pendingImages[start:end]
		batchResult, err := describeImages(c, envCfg, cfgManager, channelScheduler, kind, targetUpstream.PoolID, visionChannelID, visionModelInput, conversationID, profile, batch)
		if err != nil {
			if c.Request.Context().Err() != nil || upstreamcontrol.IsLocalError(err) {
				return err
			}
			return wrapRequestError(http.StatusBadGateway, "VISION_LAYER_UPSTREAM_ERROR", err)
		}
		for _, image := range batch {
			result, ok := batchResult[image.id]
			if !ok {
				err = wrapRequestError(http.StatusBadGateway, "VISION_LAYER_INVALID_RESPONSE", fmt.Errorf("图片理解结果缺少编号 %s", image.id))
				return err
			}
			descriptions[image.fingerprint] = result
		}
	}

	transformed, err := transformImagesForUpstream(payload, targetUpstream.ServiceType, descriptions)
	if err != nil {
		return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", err)
	}
	transformedBody, err := codec.MarshalJSONNoEscape(transformed)
	if err != nil {
		return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", fmt.Errorf("序列化图片理解层请求失败: %w", err))
	}
	request.Body = io.NopCloser(bytes.NewReader(transformedBody))
	// 重放与保活必须读取实际发送的最终正文，不能沿用图片替换前的 GetBody。
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(transformedBody)), nil
	}
	request.ContentLength = int64(len(transformedBody))
	request.Header.Del("Content-Length")
	request.Header.Set("Content-Type", "application/json")
	return nil
}

func preferredVisionChannelID(targetUpstream *config.UpstreamConfig) (string, error) {
	if targetUpstream == nil || !targetUpstream.VisionLayerEnabled {
		return "", nil
	}
	channelID := strings.TrimSpace(targetUpstream.VisionLayerChannelID)
	if channelID == "" {
		return "", fmt.Errorf("渠道 %q 已启用图片理解层，但未选择图片理解渠道", targetUpstream.Name)
	}
	return channelID, nil
}

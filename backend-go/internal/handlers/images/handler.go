package images

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/handlers/proxycore"
	"github.com/BenedictKing/api-proxy/internal/handlers/streams"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

// Handler Images API 代理处理器
// 使用通用 RunProxyRequest 骨架，通过 ProtocolSpec 注入协议特有逻辑。
// contentSafetyPipelines 为可选注入点：主进程必须传入带 BlockedLogRecorder 的共享管道，
// 不传时按 ResolveContentSafetyPipeline 的约定自建一份（仅供测试与独立调用）。
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	endpoint string,
	contentSafetyPipelines ...*hooks.Pipeline,
) gin.HandlerFunc {
	contentSafetyPipeline := hooks.ResolveContentSafetyPipeline(cfgManager, contentSafetyPipelines...)
	spec := proxycore.ProtocolSpec{
		Kind:         scheduler.ChannelKindImages,
		LogName:      "Images",
		HookPipeline: contentSafetyPipeline,
		PreRoute:     nil,
		ParseRequest: func(c *gin.Context, body []byte) (string, bool, []string, bool) {
			contentType := c.GetHeader("Content-Type")
			requestMeta := extractImagesRequestMetadata(contentType, body)
			return requestMeta.Model, requestMeta.Stream, extractImagesPrompts(contentType, body), true
		},
		BuildUpstreamRequest: func(c *gin.Context, up *config.UpstreamConfig, apiKey string, body []byte) (*http.Request, error) {
			return buildImagesUpstreamRequest(c, up, apiKey, endpoint, body)
		},
		RequestTimeoutOverride: func([]byte) time.Duration {
			return 10 * time.Minute
		},
		HandleSuccess: func(c *gin.Context, resp *http.Response, up *config.UpstreamConfig, apiKey string, body []byte, startTime time.Time) (*types.Usage, error) {
			// Images 的 stream 位于 JSON 或 multipart 字段中；解析阶段已经识别，
			// 成功出口也必须把它传给转发器，否则上游返回非 SSE Content-Type
			// 时会按普通 JSON 缓冲，客户端收不到实时事件。
			requestMeta := extractImagesRequestMetadata(c.GetHeader("Content-Type"), body)
			if up.ServiceType == "responses" {
				return handleImagesResponsesSuccess(c, resp, envCfg, endpoint, requestMeta.Stream)
			}
			return handleImagesSuccess(c, resp, envCfg, startTime, requestMeta.Stream)
		},
	}
	return func(c *gin.Context) {
		proxycore.RunProxyRequest(c, envCfg, cfgManager, channelScheduler, spec)
	}
}

func buildImagesUpstreamRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string, endpoint string, originalBody []byte) (*http.Request, error) {
	if err := config.ValidateImagesUpstream(upstream); err != nil {
		return nil, err
	}
	if upstream.ServiceType == "responses" {
		return buildImagesResponsesRequest(c, upstream, apiKey, endpoint, originalBody)
	}
	bodyBytes, contentType, err := applyImagesModelMapping(c.GetHeader("Content-Type"), originalBody, upstream)
	if err != nil {
		return nil, err
	}

	url := buildOpenAIEndpointURL(upstream.GetEffectiveBaseURL(), endpoint)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建 Images 请求失败: %w", err)
	}

	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	utils.SetAuthenticationHeader(req.Header, apiKey)
	return req, nil
}

func applyImagesModelMapping(contentType string, bodyBytes []byte, upstream *config.UpstreamConfig) ([]byte, string, error) {
	if boundary, ok := utils.MultipartBoundary(contentType); ok {
		metadata := extractImagesRequestMetadata(contentType, bodyBytes)
		if strings.TrimSpace(metadata.Model) == "" || config.ResolveUpstreamModel(metadata.Model, upstream) == metadata.Model {
			return bodyBytes, contentType, nil
		}
		return applyImagesMultipartModelMapping(boundary, bodyBytes, upstream)
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return bodyBytes, contentType, nil
	}
	if !strings.Contains(mediaType, "json") {
		return bodyBytes, contentType, nil
	}

	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, "", fmt.Errorf("解析 Images 请求体失败: %w", err)
	}

	if model, ok := payload["model"].(string); ok && strings.TrimSpace(model) != "" {
		mappedModel := config.ResolveUpstreamModel(model, upstream)
		if mappedModel == model {
			return bodyBytes, contentType, nil
		}
		payload["model"] = mappedModel
	} else {
		return bodyBytes, contentType, nil
	}

	mappedBody, err := utils.MarshalJSONNoEscape(payload)
	if err != nil {
		return nil, "", err
	}
	return mappedBody, contentType, nil
}

// applyImagesMultipartModelMapping 把 multipart 表单里的 model 字段改写为上游模型名，
// 并整表重新编码。返回的 Content-Type 带**新的** boundary，调用方必须同步到请求头。
func applyImagesMultipartModelMapping(boundary string, bodyBytes []byte, upstream *config.UpstreamConfig) ([]byte, string, error) {
	parts, err := utils.ParseMultipartParts(bodyBytes, boundary)
	if err != nil {
		return nil, "", fmt.Errorf("解析 Images multipart 请求体失败: %w", err)
	}

	for index := range parts {
		// 带 filename 的部件是上传的图片/蒙版，即使表单字段名恰好叫 model 也不是模型名。
		if parts[index].FormName != "model" || parts[index].FileName != "" {
			continue
		}
		model := strings.TrimSpace(string(parts[index].Content))
		if model == "" {
			continue
		}
		parts[index].Content = []byte(config.ResolveUpstreamModel(model, upstream))
	}

	mappedBody, mappedContentType, err := utils.EncodeMultipartParts(parts)
	if err != nil {
		return nil, "", fmt.Errorf("重建 Images multipart 请求体失败: %w", err)
	}
	return mappedBody, mappedContentType, nil
}

func buildOpenAIEndpointURL(baseURL string, endpoint string) string {
	return utils.BuildUpstreamURL(baseURL, "/v1", endpoint)
}

func handleImagesSuccess(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, startTime time.Time, requestedStream bool) (*types.Usage, error) {
	defer resp.Body.Close()

	isStream := requestedStream || streams.IsEventStreamResponse(resp)
	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		if isStream {
			log.Printf("[Images-Stream] Images 流式响应开始: %dms, 状态: %d", responseTime, resp.StatusCode)
		} else {
			log.Printf("[Images-Timing] Images 响应开始转发: %dms, 状态: %d", responseTime, resp.StatusCode)
		}
	}

	err := streams.ForwardUpstreamResponseBody(c, resp, "application/json", isStream)
	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		if isStream {
			log.Printf("[Images-Stream] Images 流式响应完成: %dms", responseTime)
		} else {
			log.Printf("[Images-Timing] Images 响应转发完成: %dms", responseTime)
		}
	}
	return nil, err
}

type imagesRequestMetadata struct {
	Model  string
	Stream bool
}

func extractImagesRequestMetadata(contentType string, bodyBytes []byte) imagesRequestMetadata {
	var metadata imagesRequestMetadata

	if boundary, ok := utils.MultipartBoundary(contentType); ok {
		values, err := utils.ReadMultipartTextFields(bodyBytes, boundary, []string{"model", "stream"})
		if err != nil {
			// 表单畸形时不静默当成"无 model"：记录后交由上游按原样报错，避免把请求
			// 悄悄当作未指定模型转发出去。
			log.Printf("[Images-Request] 警告: 解析 multipart 请求元数据失败: %v", err)
			return metadata
		}
		metadata.Model = strings.TrimSpace(values["model"])
		metadata.Stream = parseImagesStreamValue(strings.TrimSpace(values["stream"]))
		return metadata
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return metadata
	}
	if !strings.Contains(mediaType, "json") {
		return metadata
	}

	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err == nil {
		metadata.Model, _ = payload["model"].(string)
		metadata.Stream = parseImagesStreamValue(payload["stream"])
	}
	return metadata
}

// extractImagesPrompts 取出用于会话观测的用户 prompt 文本。
// JSON 与 multipart 两种形态都覆盖：Images 的 edits/variations 常用 multipart 表单，
// 只解析 JSON 会让这两个端点的观测 prompt 永远为空。
func extractImagesPrompts(contentType string, bodyBytes []byte) []string {
	if boundary, ok := utils.MultipartBoundary(contentType); ok {
		values, err := utils.ReadMultipartTextFields(bodyBytes, boundary, []string{"prompt"})
		if err != nil {
			log.Printf("[Images-Request] 警告: 解析 multipart prompt 失败: %v", err)
			return nil
		}
		return proxycore.NormalizePromptTexts(values["prompt"])
	}
	return proxycore.ExtractPromptJSONFieldPrompts(bodyBytes, "prompt")
}

func parseImagesStreamValue(value interface{}) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		}
	case json.Number:
		return typed.String() == "1"
	case float64:
		return typed == 1
	}
	return false
}

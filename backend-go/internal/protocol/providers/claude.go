package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/codec"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/dialogue/prompt/cacheinject"
	"insistanan/api-proxy/internal/dialogue/prompt/copilotopt"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/types"
)

// ClaudeProvider Claude 提供商（直接透传）
type ClaudeProvider struct{}

// redirectModelInBody 仅修改请求体中的 model 字段，保持其他内容不变
// 使用 map[string]interface{} 避免结构体字段丢失问题。
// Claude Code auto mode 的未知字段（含 safeguards）会留下来，这里也不改写 tool_use.id。
func redirectModelInBody(bodyBytes []byte, upstream *config.UpstreamConfig) []byte {
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber() // 保留数字精度

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return bodyBytes // 解析失败，返回原始数据
	}

	model, ok := data["model"].(string)
	if !ok {
		return bodyBytes // 没有 model 字段或类型不对
	}

	newModel := config.ResolveUpstreamModel(model, upstream)
	if newModel == model {
		return bodyBytes // 模型未变，无需重编码
	}

	data["model"] = newModel

	// 使用 Encoder 并禁用 HTML 转义，保持原始格式
	newBytes, err := codec.MarshalJSONNoEscape(data)
	if err != nil {
		return bodyBytes // 编码失败，返回原始数据
	}
	return newBytes
}

// ConvertToProviderRequest 转换为 Claude 请求（实现真正的透传）。
// 解析入站请求后交给 BuildRequest。
func (p *ClaudeProvider) ConvertToProviderRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string) (*http.Request, []byte, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, nil, err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes)) // 恢复body

	outcome, err := p.BuildRequest(RequestInput{
		Ctx:                c.Request.Context(),
		Method:             c.Request.Method,
		Path:               c.Request.URL.Path,
		RawQuery:           c.Request.URL.RawQuery,
		Header:             c.Request.Header,
		Body:               bodyBytes,
		ConversationID:     c.GetString(upstreamreq.ContextKeyConversationUserID),
		ClaudeCodeDisguise: c.GetBool(upstreamreq.ContextKeyClaudeCodeDisguise),
	}, upstream, apiKey)
	if err != nil {
		return nil, bodyBytes, err
	}
	return outcome.Request, outcome.OriginalBody, nil
}

// BuildRequest 以显式输入构造 Claude 透传请求（不依赖 Gin）。
func (p *ClaudeProvider) buildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	bodyBytes := input.Body

	// 模型重定向、缓存断点注入和伪装都走 map，不按结构体重编码。
	// 因此 safeguards 等未知字段和已有 tool_use.id 会原样送到 Claude 上游。
	// 模型重定向本身只改 model。
	if upstream != nil && (len(upstream.ModelMapping) > 0 || upstream.DefaultModel != "") {
		bodyBytes = redirectModelInBody(bodyBytes, upstream)
	}

	// 自动 Prompt Caching 断点注入（参照 cc-switch cache_injector）：
	// 若现有断点 < 4，在 tools 末尾、system 末尾、最新消息与历史 anchor 自动补充 cache_control
	if upstream == nil || !upstream.DisablePromptCacheInjection {
		injectedBytes, _, _ := cacheinject.InjectPromptCacheBreakpoints(bodyBytes)
		bodyBytes = injectedBytes
	}

	// 构建目标URL（"#"后缀与版本前缀约定见 upstreamreq.BuildUpstreamURL）
	endpoint := strings.TrimPrefix(input.Path, "/v1")
	targetURL := upstreamreq.BuildUpstreamURL(upstream.GetEffectiveBaseURL(), "/v1", endpoint, upstream.UseFullURL)

	if input.RawQuery != "" {
		targetURL += "?" + input.RawQuery
	}

	// 先构造 URL 和请求头；请求体在所有改写完成后统一设置。
	req, err := http.NewRequestWithContext(input.Ctx, input.Method, targetURL, nil)
	if err != nil {
		return nil, err
	}

	// 使用统一的头部处理逻辑
	req.Header = upstreamreq.PrepareUpstreamHeadersFromHeader(input.Header, req.URL.Host)

	compatibility := upstream.UseClientCompatibility(input.ClaudeCodeDisguise)
	upstreamreq.PrepareTargetIdentity(req.Header, "claude", compatibility)
	if compatibility {
		upstreamreq.SeedDisguiseSession(req.Header, input.ConversationID, "claude")
		var streamRequest struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(bodyBytes, &streamRequest)
		upstreamreq.ApplyClaudeCodeDisguise(req.Header, streamRequest.Stream)

		bodyBytes = upstreamreq.ApplyClaudeCodeBodyDisguise(
			bodyBytes,
			req.Header.Get("X-Claude-Code-Session-Id"),
		)
	}

	// 设置认证头（会覆盖客户端的认证头）
	upstreamreq.SetAuthenticationHeader(req.Header, apiKey)

	// Copilot 请求优化：如果上游为 Copilot / Github 兼容渠道，自动计算交互分类并附带 x-initiator / x-interaction-type 请求头
	hasBeta := req.Header.Get("anthropic-beta") != ""
	classification := copilotopt.ClassifyRequest(bodyBytes, hasBeta, true, true)
	copilotopt.ApplyCopilotHeaders(req.Header, classification)
	if mergedBody, ok := copilotopt.MergeToolResults(bodyBytes); ok {
		bodyBytes = mergedBody
	}

	// 首次发送与重试必须读取同一份最终载荷，不能捕获仍在改写的 bodyBytes。
	requestBody := bodyBytes
	if len(requestBody) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(requestBody))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(requestBody)), nil
		}
	}
	req.ContentLength = int64(len(requestBody))
	req.Header.Del("Content-Length")

	return &RequestOutcome{Request: req, OriginalBody: bodyBytes}, nil
}

// ConvertToClaudeResponse 转换为 Claude 响应（直接透传）
func (p *ClaudeProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	var claudeResp types.ClaudeResponse
	if err := json.Unmarshal(providerResp.Body, &claudeResp); err != nil {
		return nil, err
	}
	return &claudeResp, nil
}

// HandleStreamResponseCtx 处理流式响应（直接透传，支持客户端断连中止）
// 发送事件和读取正文都绑定 ctx，消费方退出后不会阻塞在 channel 或上游读取中。
func (p *ClaudeProvider) HandleStreamResponseCtx(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	pump := newStreamPump(ctx)
	eventChan, errChan := pump.eventChan, pump.errChan

	go func() {
		defer close(eventChan)
		defer close(errChan)
		defer codec.BindStreamBody(ctx, body)()

		scanner := pump.newScanner(body)
		for scanner.Scan() {
			event := scanner.Event()
			line, err := scanner.DataLine()
			if err != nil {
				pump.fail(err)
				return
			}
			payload, _ := codec.SSEDataJSON(line)
			var data struct {
				Type  string          `json:"type"`
				Error json.RawMessage `json:"error"`
			}
			if payload != "" {
				if err := json.Unmarshal([]byte(payload), &data); err != nil {
					pump.fail(err)
					return
				}
			}
			if event.Name == "error" || data.Type == "error" || (len(data.Error) > 0 && string(data.Error) != "null") {
				pump.fail(NewUpstreamStreamFailedError(proxycoreIsModelCapacity(payload), payload))
				return
			}
			// 下游安全与用量钩子共享单载荷事件；未知 JSON 字段保持原样。
			output, err := event.Normalized()
			if err != nil {
				pump.fail(err)
				return
			}
			if !pump.send(output) || data.Type == "message_stop" {
				return
			}
		}
		pump.finishRead(false, scanner.Err())
	}()

	return eventChan, errChan, nil
}

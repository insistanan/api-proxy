package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/cacheinject"
	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/copilotopt"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
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
	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return bodyBytes // 编码失败，返回原始数据
	}
	return newBytes
}

// ConvertToProviderRequest 转换为 Claude 请求（实现真正的透传）。
// R4-a：解析 gin 后委托显式实现 BuildRequest（薄壳）。
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
		ClaudeCodeDisguise: c.GetBool(utils.ContextKeyClaudeCodeDisguise),
	}, upstream, apiKey)
	if err != nil {
		return nil, bodyBytes, err
	}
	return outcome.Request, outcome.OriginalBody, nil
}

// BuildRequest 以显式输入构造 Claude 透传请求（R4-a 主实现，不依赖 gin）。
func (p *ClaudeProvider) BuildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	bodyBytes := input.Body

	// 模型重定向、缓存断点注入和伪装都走 map，不按结构体重编码。
	// 因此 safeguards 等未知字段和已有 tool_use.id 会原样送到 Claude 上游。
	// 模型重定向本身只改 model。
	if upstream.ModelMapping != nil && len(upstream.ModelMapping) > 0 {
		bodyBytes = redirectModelInBody(bodyBytes, upstream)
	}

	// 自动 Prompt Caching 断点注入（参照 cc-switch cache_injector）：
	// 若现有断点 < 4，在 tools 末尾、system 末尾、最新消息与历史 anchor 自动补充 cache_control
	if upstream == nil || !upstream.DisablePromptCacheInjection {
		injectedBytes, _, _ := cacheinject.InjectPromptCacheBreakpoints(bodyBytes)
		bodyBytes = injectedBytes
	}

	// 构建目标URL（"#"后缀与版本前缀约定见 utils.BuildUpstreamURL）
	endpoint := strings.TrimPrefix(input.Path, "/v1")
	targetURL := utils.BuildUpstreamURL(upstream.GetEffectiveBaseURL(), "/v1", endpoint)

	if input.RawQuery != "" {
		targetURL += "?" + input.RawQuery
	}

	// 先构造 URL 和请求头；请求体在所有改写完成后统一设置。
	req, err := http.NewRequestWithContext(input.Ctx, input.Method, targetURL, nil)
	if err != nil {
		return nil, err
	}

	// 使用统一的头部处理逻辑
	req.Header = utils.PrepareUpstreamHeadersFromHeader(input.Header, req.URL.Host)

	if input.ClaudeCodeDisguise {
		var streamRequest struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(bodyBytes, &streamRequest)
		utils.ApplyClaudeCodeDisguise(req.Header, streamRequest.Stream)

		bodyBytes = utils.ApplyClaudeCodeBodyDisguise(
			bodyBytes,
			req.Header.Get("X-Claude-Code-Session-Id"),
		)
	}

	// 设置认证头（会覆盖客户端的认证头）
	utils.SetAuthenticationHeader(req.Header, apiKey)

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

// HandleStreamResponse 处理流式响应（直接透传）
// 兼容旧签名：内部委托带 ctx 的版本，使用 context.Background()。
func (p *ClaudeProvider) HandleStreamResponse(body io.ReadCloser) (<-chan string, <-chan error, error) {
	return p.HandleStreamResponseCtx(context.Background(), body)
}

// HandleStreamResponseCtx 处理流式响应（直接透传，支持客户端断连中止）
// 修复：生产 goroutine 向 eventChan 发送事件时 select ctx.Done()，
// 客户端断连（消费者不再消费）时立即停止读取上游并退出，杜绝
// "上游持续产出 > 缓冲容量(100) 后 goroutine 永久阻塞在 channel send"的泄漏。
func (p *ClaudeProvider) HandleStreamResponseCtx(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	pump := newStreamPump(ctx)
	eventChan, errChan := pump.eventChan, pump.errChan

	go func() {
		defer close(eventChan)
		defer close(errChan)
		defer body.Close()

		trySend := pump.send

		scanner := pump.newScanner(body)

		// 注意：为了让下游的 token 注入/修补逻辑保持正确，这里必须按「完整 SSE 事件」转发。
		// 上游以空行分隔事件：event/data/id/retry/... + "\n"，空行 => 事件结束。
		var eventBuf strings.Builder

		flushEvent := func() bool {
			if eventBuf.Len() == 0 {
				return true
			}
			event := eventBuf.String()
			eventBuf.Reset()
			terminal := false
			if strings.Contains(event, "message_stop") {
				for _, line := range strings.Split(event, "\n") {
					payload, isData := utils.SSEDataJSON(line)
					if !isData {
						continue
					}
					var data struct {
						Type string `json:"type"`
					}
					if json.Unmarshal([]byte(payload), &data) == nil && data.Type == "message_stop" {
						terminal = true
						break
					}
				}
			}
			// 收到完整终止事件即结束读取；随后上游关闭连接不属于流处理失败。
			return trySend(event) && !terminal
		}

		for scanner.Scan() {
			// 客户端断连：即使上游仍在产出，也立即中止，避免泄漏
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := scanner.Text()

			// 透传所有 SSE 字段（包括注释、id、retry 等）
			eventBuf.WriteString(line)
			eventBuf.WriteString("\n")

			// 空行表示一个 SSE event 结束
			if line == "" {
				if !flushEvent() {
					return
				}
			}
		}

		// 若上游未以空行结尾，仍尝试把最后的残留事件发出去
		if !flushEvent() {
			return
		}

		if err := scanner.Err(); err != nil {
			// 未收到完整 message_stop 的连接中断仍需上报，不能把截断当成完成。
			pump.fail(err)
		}
	}()

	return eventChan, errChan, nil
}

package llmgate

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/httpclient"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/providers"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

// noopResponseWriter 满足 gin.CreateTestContextOnly 的参数约束。
// 内部调用不读取客户端响应流（响应体在 http.Client 层读取），这里不会写入任何数据。
type noopResponseWriter struct{}

func (noopResponseWriter) Header() http.Header       { return http.Header{} }
func (noopResponseWriter) Write([]byte) (int, error) { return 0, nil }
func (noopResponseWriter) WriteHeader(int)           {}

const (
	// DefaultTimeoutSecs 单次内部调用的默认整体超时（含重试）。
	DefaultTimeoutSecs = 30
	// maxFailuresPerChannel 单渠道（URL×Key 组合维度）最大失败次数，达到即换渠道/放弃。
	// 与 visionlayer 的 describeImagesOnChannel 同值。
	maxFailuresPerChannel = 2
	// DefaultMaxTokens 内部调用回复上限。闸门与助手都只需要短回复。
	DefaultMaxTokens = 2048
)

var callCounter atomic.Uint64

// Caller 承载一次内部调用所需的依赖。main.go 组装一次、全程复用；
// 各方法并发安全（依赖的 ConfigManager / scheduler 均自带同步）。
type Caller struct {
	EnvConfig        *config.EnvConfig
	CfgManager       *config.ConfigManager
	ChannelScheduler *scheduler.ChannelScheduler
	// TimeoutSecs 单次 Call 的整体超时；0 取 DefaultTimeoutSecs。
	TimeoutSecs int
}

// CallResult 是一次内部调用的结果。
type CallResult struct {
	Text  string
	Usage *types.Usage
}

// Call 在选中渠道上发起一次非流式单轮调用。prompt 是 Claude Messages 格式的
// 请求参数（system / messages），由 provider 按渠道 ServiceType 转换为对应上游协议。
// 渠道由 PickChannel 的规则决定：channelID 为空时自动按四协议优先级复用主链路
// 编排（prompt.Model 作为分组匹配词参与选路，空模型走兜底分组）。
func (c *Caller) Call(ctx context.Context, channelID string, prompt Prompt) (*CallResult, error) {
	if c == nil {
		return nil, fmt.Errorf("内部调用器未初始化")
	}
	if c.CfgManager == nil {
		return nil, fmt.Errorf("内部调用器未绑定配置管理器")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selection, err := PickChannel(ctx, c.CfgManager, c.ChannelScheduler, channelID, prompt.Model)
	if err != nil {
		return nil, err
	}
	timeout := c.TimeoutSecs
	if timeout <= 0 {
		timeout = DefaultTimeoutSecs
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	return c.callOnChannel(callCtx, selection, prompt)
}

// PreviewChannel 返回当前配置下内部调用会命中的渠道（不实际发送请求），
// 用于可用性探测与状态展示。编排选渠产生的 in-flight 预留在此立即释放。
func (c *Caller) PreviewChannel(ctx context.Context, channelID string, model string) (*SelectedChannel, error) {
	if c == nil || c.CfgManager == nil {
		return nil, fmt.Errorf("内部调用器未初始化")
	}
	selection, err := PickChannel(ctx, c.CfgManager, c.ChannelScheduler, channelID, model)
	if err != nil {
		return nil, err
	}
	if selection.Reserved && c.ChannelScheduler != nil {
		c.ChannelScheduler.ReleaseChannelReservation(selection.Kind, selection.ChannelIndex)
		selection.Reserved = false
	}
	return selection, nil
}

// Prompt 描述一次内部调用的输入。Model 为空时使用渠道 DefaultModel。
type Prompt struct {
	Model    string
	System   string
	Messages []PromptMessage
	// MaxTokens 为 0 时取 DefaultMaxTokens。
	MaxTokens int
}

type PromptMessage struct {
	Role    string // "user" / "assistant"
	Content string
}

func (c *Caller) callOnChannel(ctx context.Context, selection *SelectedChannel, prompt Prompt) (*CallResult, error) {
	if selection.Reserved && c.ChannelScheduler != nil {
		// 编排选渠占用的 in-flight 预留：保持到本次内部调用（含全部重试）结束。
		defer c.ChannelScheduler.ReleaseChannelReservation(selection.Kind, selection.ChannelIndex)
	}
	upstream := selection.Upstream
	provider := providers.GetProvider(upstream.ServiceType)
	if provider == nil {
		return nil, fmt.Errorf("内部调用渠道 %q 的服务类型 %q 不受支持", upstream.Name, upstream.ServiceType)
	}
	model := config.ResolveUpstreamModel(prompt.Model, upstream)
	if model == "" {
		model = strings.TrimSpace(upstream.DefaultModel)
	}
	if model == "" {
		return nil, fmt.Errorf("内部调用渠道 %q 未能解析可用模型", upstream.Name)
	}
	// 模型已解析：清空映射，防止 Provider 再次重定向（与 visionlayer 同约定）。
	gateUpstream := upstream.Clone()
	gateUpstream.DefaultModel = model
	gateUpstream.ModelMapping = nil

	body, err := buildClaudeRequestBody(model, prompt)
	if err != nil {
		return nil, err
	}

	requestID := fmt.Sprintf("llmgate-%d", callCounter.Add(1))
	failedKeys := make(map[string]bool)
	urlResults := c.ChannelScheduler.GetSortedURLsForChannel(selection.Kind, selection.ChannelIndex, upstream.GetAllBaseURLs())
	var lastErr error
	failCount := 0

	for _, urlResult := range urlResults {
		baseURL := urlResult.URL
		for attempt := 0; attempt < len(upstream.APIKeys); attempt++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			apiKey, keyErr := c.CfgManager.GetNextAPIKey(gateUpstream, failedKeys, "LlmGate")
			if keyErr != nil {
				lastErr = keyErr
				break
			}
			if c.ChannelScheduler.ShouldSuspendKey(baseURL, apiKey, selection.ChannelIndex, selection.Kind) {
				failedKeys[apiKey] = true
				continue
			}
			attemptStart := time.Now()
			upstreamCopy := gateUpstream.Clone()
			upstreamCopy.BaseURL = baseURL
			request, requestErr := buildProviderRequest(ctx, provider, upstreamCopy, apiKey, body)
			if requestErr != nil {
				lastErr = requestErr
				failedKeys[apiKey] = true
				failCount++
				c.recordAttempt(selection, upstream, requestID, model, baseURL, apiKey, "failed", 0, false, attemptStart, "build_request", requestErr.Error(), attempt > 0, nil)
				if failCount >= maxFailuresPerChannel {
					return nil, lastErr
				}
				continue
			}
			result, attemptErr := c.sendRequest(provider, selection, upstream, requestID, model, baseURL, apiKey, request, attemptStart, attempt > 0)
			if attemptErr == nil {
				return result, nil
			}
			lastErr = attemptErr
			if !isRetryable(attemptErr) {
				return nil, attemptErr
			}
			failCount++
			if failCount >= maxFailuresPerChannel {
				return nil, lastErr
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("内部调用渠道 %q 没有可用的 BaseURL", upstream.Name)
	}
	return nil, lastErr
}

// gateAttemptError 标记可重试的上游失败（与 visionlayer 的两层判定同构：
// 响应体谓词一票否决，再叠加状态码分类）。
type gateAttemptError struct {
	StatusCode int
	Body       []byte
	Err        error
}

func (e *gateAttemptError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("内部调用上游返回 HTTP %d", e.StatusCode)
}

func (e *gateAttemptError) Unwrap() error { return e.Err }

func isRetryable(err error) bool {
	var gateErr *gateAttemptError
	if !asGateAttemptError(err, &gateErr) || gateErr == nil {
		// 网络错误等非上游响应失败：值得换 Key/URL 重试。
		return true
	}
	if utils.IsNonRetryableUpstreamErrorBody(gateErr.Body) {
		return false
	}
	retryable, _ := utils.ClassifyUpstreamStatus(gateErr.StatusCode)
	return retryable
}

func asGateAttemptError(err error, target **gateAttemptError) bool {
	for err != nil {
		if typed, ok := err.(*gateAttemptError); ok {
			*target = typed
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// sendRequest 发送一次上游请求并解析响应。超时与取消由 request 自带的
// context（调用方注入）控制。
func (c *Caller) sendRequest(
	provider providers.Provider,
	selection *SelectedChannel,
	upstream *config.UpstreamConfig,
	requestID, model, baseURL, apiKey string,
	request *http.Request,
	attemptStart time.Time,
	retried bool,
) (*CallResult, error) {
	timeout := c.timeoutDuration()
	client, clientErr := httpclient.GetManager().GetStandardClientForUpstream(timeout, c.CfgManager, upstream)
	if clientErr != nil {
		_ = request.Body.Close()
		return nil, clientErr
	}
	c.ChannelScheduler.RecordRequestStart(baseURL, apiKey, selection.ChannelIndex, selection.Kind)
	metricsRequestID := c.ChannelScheduler.RecordRequestConnected(baseURL, apiKey, model, selection.ChannelIndex, selection.Kind)
	response, requestErr := client.Do(request)
	if requestErr != nil {
		c.recordFinalizeFailure(baseURL, apiKey, selection, metricsRequestID)
		c.ChannelScheduler.MarkURLFailure(selection.Kind, selection.ChannelIndex, baseURL)
		c.CfgManager.MarkKeyAsFailed(apiKey, "LlmGate")
		err := &gateAttemptError{Err: requestErr}
		c.recordAttempt(selection, upstream, requestID, model, baseURL, apiKey, "failed", 0, false, attemptStart, "network", requestErr.Error(), retried, nil)
		return nil, err
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		c.recordFinalizeFailure(baseURL, apiKey, selection, metricsRequestID)
		c.ChannelScheduler.MarkURLFailure(selection.Kind, selection.ChannelIndex, baseURL)
		c.CfgManager.MarkKeyAsFailed(apiKey, "LlmGate")
		err := &gateAttemptError{Err: readErr}
		c.recordAttempt(selection, upstream, requestID, model, baseURL, apiKey, "failed", response.StatusCode, false, attemptStart, "read_response", readErr.Error(), retried, nil)
		return nil, err
	}
	body = utils.DecompressGzipIfNeeded(response, body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		c.recordFinalizeFailure(baseURL, apiKey, selection, metricsRequestID)
		c.ChannelScheduler.MarkURLFailure(selection.Kind, selection.ChannelIndex, baseURL)
		err := &gateAttemptError{
			StatusCode: response.StatusCode,
			Body:       body,
			Err: fmt.Errorf("内部调用渠道 %q 返回 HTTP %d（协议=%s，模型=%s，URL=%s）: %s",
				upstream.Name, response.StatusCode, upstream.ServiceType, model, baseURL, truncateBody(body)),
		}
		c.recordAttempt(selection, upstream, requestID, model, baseURL, apiKey, "failed", response.StatusCode, false, attemptStart, "upstream", err.Error(), retried, nil)
		return nil, err
	}
	claudeResponse, convertErr := provider.ConvertToClaudeResponse(&types.ProviderResponse{
		StatusCode: response.StatusCode,
		Headers:    response.Header,
		Body:       body,
	})
	if convertErr != nil {
		c.recordFinalizeFailure(baseURL, apiKey, selection, metricsRequestID)
		c.ChannelScheduler.MarkURLFailure(selection.Kind, selection.ChannelIndex, baseURL)
		err := fmt.Errorf("解析内部调用响应失败: %w", convertErr)
		c.recordAttempt(selection, upstream, requestID, model, baseURL, apiKey, "failed", response.StatusCode, false, attemptStart, "response_processing", err.Error(), retried, nil)
		return nil, err
	}
	result := extractText(claudeResponse)
	if result == "" {
		c.recordFinalizeFailure(baseURL, apiKey, selection, metricsRequestID)
		err := fmt.Errorf("内部调用渠道 %q 未返回文字结果", upstream.Name)
		c.recordAttempt(selection, upstream, requestID, model, baseURL, apiKey, "failed", response.StatusCode, false, attemptStart, "empty_response", err.Error(), retried, nil)
		return nil, err
	}
	c.ChannelScheduler.RecordRequestFinalizeSuccess(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, claudeResponse.Usage, selection.Kind)
	c.ChannelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, selection.Kind)
	c.ChannelScheduler.MarkURLSuccess(selection.Kind, selection.ChannelIndex, baseURL)
	c.recordAttempt(selection, upstream, requestID, model, baseURL, apiKey, "completed", response.StatusCode, true, attemptStart, "", "", retried, claudeResponse.Usage)
	return &CallResult{Text: result, Usage: claudeResponse.Usage}, nil
}

func (c *Caller) recordFinalizeFailure(baseURL, apiKey string, selection *SelectedChannel, metricsRequestID uint64) {
	c.ChannelScheduler.RecordRequestFinalizeFailure(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, selection.Kind)
	c.ChannelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, selection.Kind)
}

func (c *Caller) timeoutDuration() time.Duration {
	timeout := c.TimeoutSecs
	if timeout <= 0 {
		timeout = DefaultTimeoutSecs
	}
	return time.Duration(timeout) * time.Second
}

// buildProviderRequest 借用 provider 的转换链构造上游请求。providers 统一以
// Claude Messages 请求体为输入（visionlayer 同此约定），这里用一个剥离了
// 客户端痕迹的合成 gin context 承载（空 header + Context 注入，绝不携带
// 管理端 cookie / x-proxy-key，与 eval.Sender 的安全约束一致）。
func buildProviderRequest(ctx context.Context, provider providers.Provider, upstream *config.UpstreamConfig, apiKey string, body []byte) (*http.Request, error) {
	synthetic := gin.CreateTestContextOnly(noopResponseWriter{}, gin.New())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/messages", io.NopCloser(bytes.NewReader(body)))
	req.Header = http.Header{}
	req.Header.Set("Content-Type", "application/json")
	synthetic.Request = req
	upstreamRequest, _, err := provider.ConvertToProviderRequest(synthetic, upstream, apiKey)
	if err != nil {
		return nil, err
	}
	if upstreamRequest == nil || upstreamRequest.Body == nil {
		return nil, fmt.Errorf("内部调用渠道 %q 转换后缺少请求体", upstream.Name)
	}
	if err := ctx.Err(); err != nil {
		_ = upstreamRequest.Body.Close()
		return nil, err
	}
	return upstreamRequest, nil
}

func buildClaudeRequestBody(model string, prompt Prompt) ([]byte, error) {
	maxTokens := prompt.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	request := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"stream":     false,
	}
	if system := strings.TrimSpace(prompt.System); system != "" {
		request["system"] = system
	}
	messages := make([]any, 0, len(prompt.Messages))
	for _, message := range prompt.Messages {
		messages = append(messages, map[string]any{
			"role":    message.Role,
			"content": message.Content,
		})
	}
	request["messages"] = messages
	return utils.MarshalJSONNoEscape(request)
}

// extractText 从 Claude 响应中拼接全部 text 块（与 visionlayer.extractResponseText 同口径，
// 独立实现避免跨包导出）。
func extractText(response *types.ClaudeResponse) string {
	if response == nil {
		return ""
	}
	parts := make([]string, 0, len(response.Content))
	for _, block := range response.Content {
		if strings.EqualFold(block.Type, "text") && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func (c *Caller) recordAttempt(
	selection *SelectedChannel,
	upstream *config.UpstreamConfig,
	requestID, model, baseURL, apiKey, status string,
	statusCode int,
	success bool,
	startedAt time.Time,
	errorType, errorMessage string,
	retried bool,
	usage *types.Usage,
) {
	if c.ChannelScheduler == nil || selection == nil || upstream == nil {
		return
	}
	store := c.ChannelScheduler.GetChannelLogStore(selection.Kind)
	if store == nil {
		return
	}
	store.Record(&metrics.ChannelLog{
		RequestID:    requestID,
		AttemptID:    fmt.Sprintf("%s-%d", requestID, time.Now().UnixNano()),
		Timestamp:    time.Now().Format(time.RFC3339Nano),
		Status:       status,
		StatusCode:   statusCode,
		Success:      success,
		DurationMs:   time.Since(startedAt).Milliseconds(),
		APIType:      "LlmGate",
		Model:        model,
		InputTokens:  usageInputTokens(usage),
		OutputTokens: usageOutputTokens(usage),
		ChannelIndex: selection.ChannelIndex,
		ChannelName:  upstream.Name,
		BaseURL:      baseURL,
		KeyMask:      utils.MaskAPIKey(apiKey),
		ErrorType:    errorType,
		ErrorMessage: truncateBody([]byte(errorMessage)),
		Retried:      retried,
		Stream:       false,
	})
}

func usageInputTokens(usage *types.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.InputTokens > 0 {
		return usage.InputTokens
	}
	return usage.PromptTokens
}

func usageOutputTokens(usage *types.Usage) int {
	if usage == nil {
		return 0
	}
	return usage.OutputTokens
}

func truncateBody(body []byte) string {
	const limit = 512
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}

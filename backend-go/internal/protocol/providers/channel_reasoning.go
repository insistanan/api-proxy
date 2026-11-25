package providers

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/converters"
)

type requestBuildFunc func(RequestInput, *config.UpstreamConfig, string) (*RequestOutcome, error)

func buildWithChannelReasoning(input RequestInput, upstream *config.UpstreamConfig, apiKey string, build requestBuildFunc) (*RequestOutcome, error) {
	if !converters.HasChannelReasoningOverride(upstream) {
		return build(input, upstream, apiKey)
	}
	if err := config.ValidateReasoningOptions(upstream); err != nil {
		return nil, err
	}
	originalInput := input.Body
	var err error
	input.Body, err = converters.RemoveRequestReasoningParameters(input.Body)
	if err != nil {
		return nil, err
	}
	buildUpstream := upstream
	if upstream.ReasoningEffort == "none" && upstream.RequireReasoningContent {
		// 此能力是在思考模式下探测到的；强制关闭后不应要求客户端补旧推理原文。
		// 仍保留已有原文与其他历史归属保护，不伪造缺失内容。
		upstreamCopy := *upstream
		upstreamCopy.RequireReasoningContent = false
		buildUpstream = &upstreamCopy
	}
	outcome, err := build(input, buildUpstream, apiKey)
	if err != nil {
		return outcome, err
	}
	if outcome == nil || outcome.Request == nil || outcome.Request.GetBody == nil {
		return outcome, fmt.Errorf("渠道思考覆盖需要可重放的上游请求体")
	}
	replay, err := outcome.Request.GetBody()
	if err != nil {
		return outcome, fmt.Errorf("读取上游请求副本失败: %w", err)
	}
	body, readErr := io.ReadAll(replay)
	if err := errors.Join(readErr, replay.Close()); err != nil {
		return outcome, fmt.Errorf("读取上游请求副本失败: %w", err)
	}
	model := config.ResolveUpstreamModel(gjson.GetBytes(input.Body, "model").String(), upstream)
	controlled, err := converters.ApplyChannelReasoningOverride(body, upstream, model)
	if err != nil {
		return outcome, err
	}
	if err := outcome.Request.Body.Close(); err != nil {
		return outcome, fmt.Errorf("关闭原上游请求体失败: %w", err)
	}
	// Claude 原生构建器把最终正文作为重试/日志正文返回，保持该既有契约。
	if bytes.Equal(outcome.OriginalBody, body) {
		outcome.OriginalBody = controlled
	} else if bytes.Equal(outcome.OriginalBody, input.Body) {
		outcome.OriginalBody = originalInput
	}
	outcome.Request.Body = io.NopCloser(bytes.NewReader(controlled))
	outcome.Request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(controlled)), nil }
	outcome.Request.ContentLength = int64(len(controlled))
	outcome.Request.TransferEncoding = nil
	outcome.Request.Header.Del("Content-Length")
	outcome.Request.Header.Set("Content-Type", "application/json")
	return outcome, nil
}

// 保持各 Provider 的状态与成功处理实例一致，只包装请求级参数。
func (p *ClaudeProvider) BuildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	return buildWithChannelReasoning(input, upstream, apiKey, p.buildRequest)
}

func (p *OpenAIProvider) BuildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	return buildWithChannelReasoning(input, upstream, apiKey, p.buildRequest)
}

func (p *GeminiProvider) BuildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	return buildWithChannelReasoning(input, upstream, apiKey, p.buildRequest)
}

func (p *MessagesResponsesProvider) BuildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	return buildWithChannelReasoning(input, upstream, apiKey, p.buildRequest)
}

func (p *ResponsesProvider) BuildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	return buildWithChannelReasoning(input, upstream, apiKey, p.buildRequest)
}

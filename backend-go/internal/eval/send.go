package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/httpclient"
	"github.com/BenedictKing/api-proxy/internal/utils"
)

// RawResponse 一次原生上游调用的原始结果。评测不走转换器。
type RawResponse struct {
	StatusCode int
	Body       []byte
	JSON       map[string]interface{}
	LatencyMS  int64
	Model      string
	URL        string
}

type Sender struct {
	cfg    *config.ConfigManager
	envCfg *config.EnvConfig
}

func NewSender(cfg *config.ConfigManager, envCfg *config.EnvConfig) *Sender {
	return &Sender{cfg: cfg, envCfg: envCfg}
}

type SendOptions struct {
	ModelOverride    string
	ThinkingOverride string
}

func (s *Sender) Send(ctx context.Context, located config.LocatedChannel, stimulus Stimulus, options SendOptions) (*RawResponse, error) {
	upstream := located.Upstream
	serviceType := strings.TrimSpace(upstream.ServiceType)
	if serviceType == "" {
		return nil, fmt.Errorf("渠道 %s 没有 serviceType", upstream.Name)
	}

	model, err := resolveEvalModel(upstream, options.ModelOverride)
	if err != nil {
		return nil, err
	}

	baseURLs := upstream.GetAllBaseURLs()
	if len(baseURLs) == 0 {
		return nil, fmt.Errorf("渠道 %s 没有 BaseURL", upstream.Name)
	}
	baseURL := baseURLs[0]

	if len(upstream.APIKeys) == 0 {
		return nil, fmt.Errorf("渠道 %s 没有 API Key", upstream.Name)
	}
	apiKey := upstream.APIKeys[0]

	thinking := firstNonEmpty(options.ThinkingOverride, stimulus.Thinking, ThinkingInherit)
	body, endpoint, err := buildNativeBody(serviceType, model, stimulus, thinking)
	if err != nil {
		return nil, err
	}

	fullURL := buildEvalURL(serviceType, baseURL, model, endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	switch serviceType {
	case ServiceGemini:
		utils.SetGeminiAuthenticationHeader(headers, apiKey)
	default:
		utils.SetAuthenticationHeader(headers, apiKey)
	}
	applyEvalClientHeaders(headers, serviceType, s.cfg)
	req.Header = headers

	proxyURL, err := s.cfg.ResolveUpstreamProxyURL(&upstream)
	if err != nil {
		return nil, err
	}

	started := time.Now()
	// R4-a：评测原生发送直接走 internal/httpclient（唯一网络发送实现），
	// 不再依赖 handlers/proxycore——评测不做协议转换、不进入生产调度，
	// 也不应继承代理主链路的请求日志副作用。
	resp, err := httpclient.Send(req, httpclient.SendOptions{
		Timeout:            time.Duration(s.envCfg.RequestTimeout) * time.Millisecond,
		ProxyURL:           proxyURL,
		InsecureSkipVerify: upstream.InsecureSkipVerify,
		StreamLabel:        "Eval",
	})
	latency := time.Since(started).Milliseconds()
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("读取上游响应失败: %w", err)
	}

	result := &RawResponse{
		StatusCode: resp.StatusCode,
		Body:       rawBody,
		LatencyMS:  latency,
		Model:      model,
		URL:        fullURL,
		JSON:       map[string]interface{}{},
	}
	if len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, &result.JSON); err != nil {
			result.JSON = map[string]interface{}{"_raw": string(rawBody)}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("上游返回 HTTP %d: %s", resp.StatusCode, truncateForError(string(rawBody)))
	}
	return result, nil
}

func resolveEvalModel(upstream config.UpstreamConfig, override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		// 统一覆盖走映射表，但清掉 DefaultModel，免得渠道默认模型把覆盖值吞掉。
		cloned := upstream
		cloned.DefaultModel = ""
		mapped := config.ResolveUpstreamModel(strings.TrimSpace(override), &cloned)
		if strings.TrimSpace(mapped) == "" {
			return "", fmt.Errorf("渠道 %s 的统一模型覆盖无法解析", upstream.Name)
		}
		return mapped, nil
	}
	mapped := config.ResolveUpstreamModel("", &upstream)
	if strings.TrimSpace(mapped) != "" {
		return mapped, nil
	}
	return "", fmt.Errorf("渠道 %s 没有 defaultModel，请填写统一模型或给渠道配置默认模型", upstream.Name)
}

func buildEvalURL(serviceType, baseURL, model, endpoint string) string {
	switch serviceType {
	case ServiceGemini:
		escaped := url.PathEscape(model)
		return utils.BuildUpstreamURL(baseURL, "/v1beta", "/models/"+escaped+":generateContent")
	case ServiceResponses:
		return utils.BuildUpstreamURL(baseURL, "/v1", "/responses")
	case ServiceOpenAI:
		return utils.BuildUpstreamURL(baseURL, "/v1", "/chat/completions")
	default:
		if endpoint == "" {
			endpoint = "/messages"
		}
		return utils.BuildUpstreamURL(baseURL, "/v1", endpoint)
	}
}

func buildNativeBody(serviceType, model string, stimulus Stimulus, thinking string) ([]byte, string, error) {
	maxTokens := stimulus.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 256
	}
	// 等级解析：inherit 跟随题目，题目没开就关闭；off 显式关闭；
	// enabled 兼容旧值等价 medium；low/medium/high/max 直接映射。
	effort := thinkingEffort(thinking, stimulus.Thinking)
	budget := stimulus.ThinkingBudget
	if budget <= 0 {
		budget = 1024
	}

	switch serviceType {
	case ServiceClaude:
		payload := map[string]interface{}{
			"model":      model,
			"max_tokens": maxTokens,
			"messages": []map[string]interface{}{
				{"role": "user", "content": stimulus.Prompt},
			},
		}
		if stimulus.System != "" {
			payload["system"] = stimulus.System
		}
		if effort != "" {
			payload["thinking"] = map[string]interface{}{"type": "enabled", "budget_tokens": claudeThinkingBudget(effort, budget)}
			payload["temperature"] = 1
		} else {
			payload["temperature"] = stimulus.Temperature
		}
		body, err := json.Marshal(payload)
		return body, "/messages", err
	case ServiceOpenAI:
		messages := []map[string]interface{}{}
		if stimulus.System != "" {
			messages = append(messages, map[string]interface{}{"role": "system", "content": stimulus.System})
		}
		messages = append(messages, map[string]interface{}{"role": "user", "content": stimulus.Prompt})
		payload := map[string]interface{}{
			"model":       model,
			"messages":    messages,
			"max_tokens":  maxTokens,
			"temperature": stimulus.Temperature,
		}
		if effort != "" {
			payload["reasoning_effort"] = effort
		}
		body, err := json.Marshal(payload)
		return body, "/chat/completions", err
	case ServiceResponses:
		payload := map[string]interface{}{
			"model":             model,
			"input":             stimulus.Prompt,
			"max_output_tokens": maxTokens,
			"temperature":       stimulus.Temperature,
		}
		if stimulus.System != "" {
			payload["instructions"] = stimulus.System
		}
		if effort != "" {
			payload["reasoning"] = map[string]interface{}{"effort": effort}
		}
		storeFalse := false
		payload["store"] = storeFalse
		body, err := json.Marshal(payload)
		return body, "/responses", err
	case ServiceGemini:
		payload := map[string]interface{}{
			"contents": []map[string]interface{}{
				{"role": "user", "parts": []map[string]interface{}{{"text": stimulus.Prompt}}},
			},
			"generationConfig": map[string]interface{}{
				"maxOutputTokens": maxTokens,
				"temperature":     stimulus.Temperature,
			},
		}
		if stimulus.System != "" {
			payload["systemInstruction"] = map[string]interface{}{
				"parts": []map[string]interface{}{{"text": stimulus.System}},
			}
		}
		if effort != "" {
			generation := payload["generationConfig"].(map[string]interface{})
			budget32 := int32(geminiThinkingBudget(effort, budget))
			generation["thinkingConfig"] = map[string]interface{}{
				"includeThoughts": true,
				"thinkingBudget":  budget32,
			}
		}
		if stimulus.ForceJSON {
			generation := payload["generationConfig"].(map[string]interface{})
			generation["responseMimeType"] = "application/json"
		}
		body, err := json.Marshal(payload)
		return body, "", err
	default:
		return nil, "", fmt.Errorf("不支持的 serviceType %s", serviceType)
	}
}

// thinkingEffort 把思考选择归一化成 effort（""=不思考）。inherit 跟随题目。
func thinkingEffort(override, stimulus string) string {
	value := override
	if value == "" || value == ThinkingInherit {
		value = stimulus
	}
	switch value {
	case "", ThinkingOff, "none", "false":
		return ""
	case ThinkingEnabled:
		return ThinkingMedium
	case ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingMax:
		return value
	default:
		// 未知等级按 medium 兜底，别把上游请求整坏。
		return ThinkingMedium
	}
}

// claudeThinkingBudget 把 effort 映射成 Claude thinking budget_tokens。
// xhigh/max 意味着更深的思考预算，按 effort 逐级放大。
func claudeThinkingBudget(effort string, fallback int) int {
	switch effort {
	case ThinkingLow:
		return 1024
	case ThinkingHigh:
		return 8192
	case ThinkingMax:
		return 16000
	default:
		return fallback
	}
}

// geminiThinkingBudget 把 effort 映射成 Gemini thinkingConfig.thinkingBudget。
func geminiThinkingBudget(effort string, fallback int) int {
	switch effort {
	case ThinkingLow:
		return 512
	case ThinkingHigh:
		return 8192
	case ThinkingMax:
		return 16384
	default:
		return fallback
	}
}

func applyEvalClientHeaders(headers http.Header, serviceType string, cfg *config.ConfigManager) {
	switch serviceType {
	case ServiceClaude:
		headers.Set("anthropic-version", "2023-06-01")
		if cfg != nil && cfg.GetClaudeCodeDisguiseEnabled() {
			utils.ApplyClaudeCodeDisguise(headers, false)
			return
		}
		headers.Set("User-Agent", "claude-cli/2.0.34 (external, cli)")
	case ServiceResponses:
		if cfg != nil && cfg.GetCodexDisguiseEnabled() {
			utils.ApplyCodexDisguise(headers, false)
			return
		}
		headers.Set("User-Agent", "codex-cli")
	default:
		if headers.Get("User-Agent") == "" {
			headers.Set("User-Agent", "api-proxy-eval/1.0")
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncateForError(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= 300 {
		return text
	}
	return text[:300] + "..."
}

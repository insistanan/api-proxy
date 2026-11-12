package llmgate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// DecisionQuestion 描述单个具名选择题。决策结果作为 CallResult.Text 返回所选标签，
// 不要求决策模型生成聊天文本或自由形式的 JSON。
type DecisionQuestion struct {
	Name         string
	Instructions string
	Criteria     map[string]string
}

func buildDecisionRequestBody(model string, prompt Prompt) ([]byte, error) {
	question := prompt.Decision
	if question == nil || strings.TrimSpace(question.Name) == "" || strings.TrimSpace(question.Instructions) == "" || len(question.Criteria) < 2 {
		return nil, fmt.Errorf("决策调用需要具名问题、审核指令和至少两个选项")
	}
	if len(prompt.Messages) != 1 || prompt.Messages[0].Role != "user" {
		return nil, fmt.Errorf("决策调用只接受一个待审核的用户数据对象")
	}
	var state any
	decoder := json.NewDecoder(strings.NewReader(prompt.Messages[0].Content))
	decoder.UseNumber()
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("解析决策输入失败: %w", err)
	}
	request := map[string]any{"model": model}
	switch prompt.Protocol {
	case "decisions":
		labels := make([]string, 0, len(question.Criteria))
		for label := range question.Criteria {
			labels = append(labels, label)
		}
		sort.Strings(labels)
		choices := make([]map[string]string, 0, len(labels))
		for _, label := range labels {
			choices = append(choices, map[string]string{"value": label, "description": question.Criteria[label]})
		}
		// OpenAI 的 input 为字符串/消息数组，结构化审核数据保留为 JSON 字符串。
		request["input"] = prompt.Messages[0].Content
		request["questions"] = []any{map[string]any{
			"type": "choice", "name": question.Name, "instructions": question.Instructions, "choices": choices,
		}}
	case "systemone":
		request["state"] = state
		request["questions"] = map[string]any{question.Name: map[string]any{
			"type": "choice", "instructions": question.Instructions, "criteria": question.Criteria,
		}}
	default:
		return nil, fmt.Errorf("决策请求协议无效: %q", prompt.Protocol)
	}
	return utils.MarshalJSONNoEscape(request)
}

func buildDecisionRequest(ctx context.Context, upstream *config.UpstreamConfig, apiKey string, body []byte, protocol string) (*http.Request, error) {
	if err := config.ValidateDecisionsUpstream(upstream); err != nil {
		return nil, err
	}
	endpoint := "/decisions"
	if protocol == "systemone" {
		endpoint = "/systemone"
	}
	url := utils.BuildUpstreamURL(upstream.GetEffectiveBaseURL(), "/v1", endpoint, upstream.UseFullURL)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建决策审批请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	utils.SetAuthenticationHeader(request.Header, apiKey)
	return request, nil
}

func decodeDecisionResponse(body []byte, prompt Prompt) (*types.ClaudeResponse, error) {
	if prompt.Decision == nil {
		return nil, fmt.Errorf("决策响应缺少对应问题")
	}
	var envelope struct {
		Answers json.RawMessage `json:"answers"`
		Usage   *types.Usage    `json:"usage"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("解析决策响应失败: %w", err)
	}
	type answer struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		Choice string `json:"choice"`
	}
	var selected answer
	switch prompt.Protocol {
	case "decisions":
		var answers []answer
		if err := json.Unmarshal(envelope.Answers, &answers); err != nil {
			return nil, fmt.Errorf("Decisions answers 必须为数组: %w", err)
		}
		found := false
		for _, item := range answers {
			if item.Name != prompt.Decision.Name {
				continue
			}
			if found {
				return nil, fmt.Errorf("决策响应包含重复的审核答案")
			}
			selected, found = item, true
		}
		if !found {
			return nil, fmt.Errorf("决策响应缺少问题 %q 的答案", prompt.Decision.Name)
		}
	case "systemone":
		var answers map[string]answer
		if err := json.Unmarshal(envelope.Answers, &answers); err != nil {
			return nil, fmt.Errorf("System One answers 必须为对象: %w", err)
		}
		var found bool
		selected, found = answers[prompt.Decision.Name]
		if !found {
			return nil, fmt.Errorf("决策响应缺少问题 %q 的答案", prompt.Decision.Name)
		}
	default:
		return nil, fmt.Errorf("决策响应协议无效: %q", prompt.Protocol)
	}
	if _, ok := prompt.Decision.Criteria[selected.Choice]; selected.Type != "choice" || !ok {
		return nil, fmt.Errorf("决策响应的答案类型或选择值无效")
	}
	if envelope.Usage != nil {
		if envelope.Usage.InputTokens == 0 {
			envelope.Usage.InputTokens = envelope.Usage.PromptTokens
		}
		if envelope.Usage.OutputTokens == 0 {
			envelope.Usage.OutputTokens = envelope.Usage.CompletionTokens
		}
	}
	return &types.ClaudeResponse{
		Content: []types.ClaudeContent{{Type: "text", Text: selected.Choice}},
		Usage:   envelope.Usage,
	}, nil
}

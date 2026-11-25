package cachekeepalive

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type bodyChange struct {
	path  string
	value any
}

func changeBody(body []byte, remove []string, changes ...bodyChange) ([]byte, error) {
	var err error
	for _, path := range remove {
		body, err = sjson.DeleteBytes(body, path)
		if err != nil {
			return nil, fmt.Errorf("移除保活请求参数 %s: %w", path, err)
		}
	}
	for _, change := range changes {
		body, err = sjson.SetBytes(body, change.path, change.value)
		if err != nil {
			return nil, fmt.Errorf("设置保活请求参数 %s: %w", change.path, err)
		}
	}
	return body, nil
}

func implicitTTL(root gjson.Result) (time.Duration, error) {
	switch root.Get("prompt_cache_retention").String() {
	case "", "in_memory":
		// 使用最短常见空闲窗口；兼容服务的真实淘汰策略仍以命中反馈为准。
		return 5 * time.Minute, nil
	case "24h":
		return 24 * time.Hour, nil
	default:
		return 0, errors.New("无法识别上游的 prompt_cache_retention")
	}
}

func checkInterval(s *snapshot, ttl time.Duration) error {
	if ttl > 0 && time.Duration(s.policy.IntervalSeconds)*time.Second >= ttl-expiryMargin {
		return errors.New("保活间隔未给实际缓存窗口留出至少 15 秒余量")
	}
	return nil
}

func prepareChatBody(s *snapshot, body []byte, root gjson.Result) ([]byte, string, time.Duration, error) {
	model := root.Get("model").String()
	if model == "" || !root.Get("messages").IsArray() || root.Get("messages.#").Int() == 0 {
		return nil, "", 0, errors.New("Chat 保活候选缺少模型或消息")
	}
	if root.Get("audio").Exists() {
		return nil, "", 0, errors.New("音频输出不适用文本缓存保活")
	}
	for _, tool := range root.Get("tools").Array() {
		if kind := tool.Get("type").String(); kind != "function" && kind != "custom" {
			return nil, "", 0, errors.New("Chat 保活不执行服务端工具")
		}
	}
	ttl, err := implicitTTL(root)
	if err != nil {
		return nil, "", 0, err
	}
	if err := checkInterval(s, ttl); err != nil {
		return nil, "", 0, err
	}
	s.mode = "implicit"
	limit := "max_completion_tokens"
	remove := []string{"max_tokens"}
	// 请求只带 max_tokens 时沿用该字段；两个互斥字段不同时发送。
	if root.Get("max_tokens").Exists() && !root.Get("max_completion_tokens").Exists() {
		limit, remove = "max_tokens", []string{"max_completion_tokens"}
	}
	if !s.stream {
		remove = append(remove, "stream_options")
	}
	changes := []bodyChange{{limit, s.policy.MaxOutputTokens}, {"n", 1},
		{"messages.-1", map[string]any{"role": "user", "content": s.policy.Prompt}}}
	if root.Get("store").Exists() {
		changes = append(changes, bodyChange{"store", false})
	}
	if s.stream {
		changes = append(changes, bodyChange{"stream_options.include_usage", true})
	}
	body, err = changeBody(body, remove, changes...)
	return body, model, ttl, err
}

func prepareResponsesBody(s *snapshot, body []byte, root gjson.Result) ([]byte, string, time.Duration, error) {
	model := root.Get("model").String()
	input := root.Get("input")
	if model == "" || (!input.IsArray() && input.Type != gjson.String) {
		return nil, "", 0, errors.New("Responses 保活候选缺少模型或可重放的 input")
	}
	// previous_response_id 指向固定历史，原样保留；conversation 是可变服务端状态。
	if value := root.Get("conversation"); value.Exists() && value.Type != gjson.Null {
		return nil, "", 0, errors.New("Responses conversation 会自动写入共享历史，无法隔离心跳；请使用完整 input 或 previous_response_id")
	}
	for _, tool := range root.Get("tools").Array() {
		if kind := tool.Get("type").String(); kind != "function" && kind != "custom" {
			return nil, "", 0, errors.New("Responses 保活不执行搜索、MCP、计算机或其他服务端工具")
		}
	}
	if root.Get("context_management").Exists() {
		return nil, "", 0, errors.New("Responses 自动上下文压缩会改变缓存前缀，跳过本轮保活")
	}
	ttl, err := implicitTTL(root)
	if err != nil {
		return nil, "", 0, err
	}
	if err := checkInterval(s, ttl); err != nil {
		return nil, "", 0, err
	}
	s.mode = "implicit"
	if input.Type == gjson.String {
		body, err = sjson.SetBytes(body, "input", []map[string]any{{"role": "user", "content": input.String()}})
		if err != nil {
			return nil, "", 0, err
		}
	}
	body, err = changeBody(body, nil, bodyChange{"max_output_tokens", s.policy.MaxOutputTokens},
		bodyChange{"store", false}, bodyChange{"background", false},
		bodyChange{"input.-1", map[string]any{"role": "user", "content": []map[string]string{{"type": "input_text", "text": s.policy.Prompt}}}})
	return body, model, ttl, err
}

var cacheResourceName = regexp.MustCompile(`^cachedContents/[A-Za-z0-9_-]+$`)

func prepareGeminiBody(s *snapshot, body []byte, root gjson.Result) ([]byte, string, time.Duration, error) {
	u, err := url.Parse(s.source.URL)
	if err != nil {
		return nil, "", 0, errors.New("Gemini 上游地址无效")
	}
	index := strings.LastIndex(u.Path, "/models/")
	if index < 0 {
		return nil, "", 0, errors.New("Gemini 上游地址缺少可识别的模型端点")
	}
	modelAction := u.Path[index+len("/models/"):]
	separator := strings.LastIndex(modelAction, ":")
	if separator <= 0 {
		return nil, "", 0, errors.New("Gemini 上游不是内容生成端点")
	}
	model, action := modelAction[:separator], modelAction[separator+1:]
	if action != "generateContent" && action != "streamGenerateContent" {
		return nil, "", 0, errors.New("Gemini 上游不是内容生成端点")
	}
	s.stream = action == "streamGenerateContent"
	if cached := root.Get("cachedContent").String(); cached != "" {
		if !cacheResourceName.MatchString(cached) {
			return nil, "", 0, errors.New("Gemini 缓存资源名不属于当前 API 的 cachedContents 命名空间")
		}
		u.Path, u.RawPath = u.Path[:index]+"/"+cached, ""
		query := u.Query()
		query.Del("alt")
		u.RawQuery = query.Encode()
		s.cacheName, s.cacheURL, s.mode = cached, u.String(), "explicit"
		// 显式缓存只更新到期时间，不调用模型、不执行工具、不改对话。
		return []byte(`{}`), model, 0, nil
	}
	if !root.Get("contents").IsArray() || root.Get("contents.#").Int() == 0 {
		return nil, "", 0, errors.New("Gemini 保活候选缺少 contents")
	}
	for _, tool := range root.Get("tools").Array() {
		invalid := false
		tool.ForEach(func(key, _ gjson.Result) bool {
			invalid = key.String() != "functionDeclarations" && key.String() != "function_declarations"
			return !invalid
		})
		if invalid {
			return nil, "", 0, errors.New("Gemini 保活不执行搜索、代码执行或其他服务端工具")
		}
	}
	if root.Get("generationConfig.thinkingConfig.thinkingBudget").Int() >= int64(s.policy.MaxOutputTokens) {
		return nil, "", 0, errors.New("Gemini 思考预算不小于保活输出上限")
	}
	for _, modality := range root.Get("generationConfig.responseModalities").Array() {
		if modality.String() != "TEXT" {
			return nil, "", 0, errors.New("Gemini 图片或音频生成不适用文本缓存保活")
		}
	}
	s.mode = "implicit"
	body, err = changeBody(body, nil, bodyChange{"generationConfig.maxOutputTokens", s.policy.MaxOutputTokens},
		bodyChange{"generationConfig.candidateCount", 1},
		bodyChange{"contents.-1", map[string]any{"role": "user", "parts": []map[string]string{{"text": s.policy.Prompt}}}})
	// Gemini 隐式缓存没有公开的固定空闲 TTL，不能把五分钟当作保证。
	return body, model, 0, err
}

package cachekeepalive

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var errNoCacheControl = errors.New("请求没有显式缓存控制")

func prepareBody(s *snapshot) ([]byte, string, time.Duration, error) {
	reader, err := s.source.Open()
	if err != nil {
		return nil, "", 0, fmt.Errorf("读取保活候选正文失败: %w", err)
	}
	if reader == nil {
		return nil, "", 0, errors.New("保活候选未提供正文 reader")
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, MaxBodyBytes+1))
	err = errors.Join(readErr, reader.Close())
	if err != nil {
		return nil, "", 0, fmt.Errorf("读取保活候选正文失败: %w", err)
	}
	if len(body) > MaxBodyBytes || int64(len(body)) != s.source.Size || !gjson.ValidBytes(body) {
		return nil, "", 0, errors.New("保活候选正文不完整或超过大小上限")
	}
	root := gjson.ParseBytes(body)
	s.stream = root.Get("stream").Bool()
	s.mode = "prefix"
	switch s.source.Upstream.ServiceType {
	case "openai":
		return prepareChatBody(s, body, root)
	case "responses":
		return prepareResponsesBody(s, body, root)
	case "gemini":
		return prepareGeminiBody(s, body, root)
	case "claude":
	default:
		return nil, "", 0, errors.New("实际请求协议不支持缓存保活")
	}
	model := root.Get("model").String()
	messages := root.Get("messages")
	if model == "" || !messages.IsArray() || len(messages.Array()) == 0 {
		return nil, "", 0, errors.New("保活候选缺少完整模型和消息上下文")
	}
	if servers := root.Get("mcp_servers"); servers.Exists() && servers.Type != gjson.Null && (!servers.IsArray() || len(servers.Array()) > 0) {
		return nil, "", 0, errors.New("原请求配置了远程 MCP 服务，心跳可能额外执行工具，停止保活")
	}
	if container := root.Get("container"); container.Exists() && container.Type != gjson.Null {
		return nil, "", 0, errors.New("原请求配置了服务端执行容器，心跳可能改变容器状态，停止保活")
	}
	if err := checkClaudeKeepaliveContext(root.Get("context_management")); err != nil {
		return nil, "", 0, err
	}
	for _, tool := range root.Get("tools").Array() {
		kind := tool.Get("type").String()
		if kind != "" && kind != "custom" {
			return nil, "", 0, errors.New("包含服务端或特殊工具的请求暂不支持自动保活")
		}
	}
	if root.Get("thinking.type").String() == "enabled" &&
		root.Get("thinking.budget_tokens").Int() >= int64(s.policy.MaxOutputTokens) {
		return nil, "", 0, errors.New("思考预算不小于保活输出上限；保留原思考配置并跳过本轮")
	}
	// 缓存断点沿用原请求，避免改变 tools、system、thinking 或 tool_choice。
	ttl, err := cacheTTL(root)
	if errors.Is(err, errNoCacheControl) {
		s.mode, ttl = "implicit", 0
	} else if err != nil {
		return nil, "", 0, err
	}
	if ttl > 0 && time.Duration(s.policy.IntervalSeconds)*time.Second >= ttl-expiryMargin {
		return nil, "", 0, errors.New("保活间隔未给实际缓存 TTL 留出至少 15 秒余量")
	}
	for _, change := range []struct {
		path  string
		value any
	}{
		{"stream", s.stream},
		{"max_tokens", s.policy.MaxOutputTokens},
		{"messages.-1", map[string]any{"role": "user", "content": []map[string]string{{"type": "text", "text": s.policy.Prompt}}}},
	} {
		body, err = sjson.SetBytes(body, change.path, change.value)
		if err != nil {
			return nil, "", 0, fmt.Errorf("构造缓存保活正文失败: %w", err)
		}
	}
	return body, model, ttl, nil
}

// Claude Code 的 keep: all 只要求保留思考历史，不会清理缓存前缀。
// 原样保留这些配置；真正清理或压缩历史的策略仍不能用于固定前缀的心跳。
func checkClaudeKeepaliveContext(value gjson.Result) error {
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if !value.IsObject() {
		return errors.New("context_management 格式无效，无法确认心跳是否保留历史")
	}
	for field := range value.Map() {
		if field != "edits" {
			return errors.New("context_management 包含未知选项，无法确认心跳是否保留历史")
		}
	}
	edits := value.Get("edits")
	if !edits.Exists() {
		return nil
	}
	if !edits.IsArray() {
		return errors.New("context_management.edits 不是数组，无法确认心跳是否保留历史")
	}
	for _, edit := range edits.Array() {
		switch edit.Get("type").String() {
		case "clear_thinking_20251015":
			keep := edit.Get("keep")
			all := keep.Type == gjson.String && keep.String() == "all" ||
				keep.IsObject() && len(keep.Map()) == 1 && keep.Get("type").String() == "all"
			if !all {
				return errors.New("原请求要求清理历史思考，可能改变缓存前缀；仅保留全部思考（keep: all）时支持保活")
			}
			if len(edit.Map()) != 2 {
				return errors.New("历史思考保留策略包含未知选项，无法确认心跳是否保留历史")
			}
		case "clear_tool_uses_20250919":
			return errors.New("原请求要求清理历史工具记录，可能改变缓存前缀，停止保活")
		case "compact_20260112":
			return errors.New("原请求启用了服务端历史压缩，可能改变缓存前缀，停止保活")
		default:
			return errors.New("原请求包含无法识别的历史处理策略，无法确认心跳是否保留历史")
		}
	}
	return nil
}

func cacheTTL(root gjson.Result) (time.Duration, error) {
	var controls []gjson.Result
	if control := root.Get("cache_control"); control.Exists() {
		controls = append(controls, control)
	}
	for _, field := range []string{"tools", "system"} {
		for _, block := range root.Get(field).Array() {
			if control := block.Get("cache_control"); control.Exists() {
				controls = append(controls, control)
			}
		}
	}
	for _, message := range root.Get("messages").Array() {
		for _, block := range message.Get("content").Array() {
			if control := block.Get("cache_control"); control.Exists() {
				controls = append(controls, control)
			}
		}
	}
	if len(controls) == 0 {
		return 0, errNoCacheControl
	}
	ttl := time.Hour
	for _, control := range controls {
		if control.Get("type").String() != "ephemeral" {
			return 0, errors.New("请求使用了不支持的缓存类型")
		}
		switch control.Get("ttl").String() {
		case "", "5m":
			ttl = 5 * time.Minute
		case "1h":
		default:
			return 0, errors.New("请求使用了无法识别的缓存 TTL")
		}
	}
	return ttl, nil
}

func usageNumbers(usage *types.Usage) (input, read, creation, output int64, err error) {
	if usage == nil || usage.Estimated || usage.UnattributedCacheInput != nil {
		return 0, 0, 0, 0, errors.New("上游未返回可信的缓存用量，停止保活")
	}
	for _, value := range []int{usage.InputTokens, usage.CacheReadInputTokens, usage.CacheCreationInputTokens,
		usage.CacheCreation5mInputTokens, usage.CacheCreation1hInputTokens, usage.OutputTokens} {
		if value < 0 || value > 1000000000 {
			return 0, 0, 0, 0, errors.New("上游返回的缓存用量超出有效范围")
		}
	}
	input, read, creation, output = int64(usage.InputTokens), int64(usage.CacheReadInputTokens),
		int64(usage.CacheCreationInputTokens), int64(usage.OutputTokens)
	if creation == 0 {
		creation = int64(usage.CacheCreation5mInputTokens) + int64(usage.CacheCreation1hInputTokens)
	}
	if input+read+creation == 0 {
		err = errors.New("上游未返回有效输入用量，停止保活")
	}
	return
}

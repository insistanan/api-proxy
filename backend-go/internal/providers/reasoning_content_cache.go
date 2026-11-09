package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"

	"github.com/insistanan/api-proxy/internal/types"
)

// reasoningContentCache 保存 "assistant 消息指纹 → reasoning_content" 的进程内缓存。
//
// 背景：Chat 推理模型（如 DeepSeek）要求将 thinking 模式产生的 reasoning_content
// 原样随 assistant 历史消息回传，否则下一轮返回 400
// （"The reasoning_content in the thinking mode must be passed back to the API."，
// Claude 兼容网关则报 "The content[].thinking in the thinking mode must be passed back..."）。
//
// 代理不把上游 reasoning_content 暴露给 Messages 客户端；客户端历史中没有明文
// thinking 时，messages→chat 转换仍需要从缓存回填 reasoning_content。
// 该缓存把每条 assistant 响应的（消息指纹 → reasoning_content）暂存，
// 在下一轮转换 assistant 历史消息缺失明文 thinking 时自动补回。缓存对所有
// 上游协议共用，使故障转移到严格 Chat 渠道时仍可回放前一渠道的 thinking。
type reasoningContentCache struct {
	mu        sync.RWMutex
	entries   map[string]string
	order     []string // 插入顺序，用于容量淘汰（FIFO）
	max       int
	maxBytes  int
	usedBytes int
}

// reasoningCache 全局缓存实例。GetProvider 每次返回新的 OpenAIProvider 实例，
// 因此缓存必须挂在包级，不能放在 provider 实例上。工具调用会同时写入完整
// 消息键和 ID 回退键，容量相应提高以覆盖长时间 Claude Code 会话。
var reasoningCache = newReasoningContentCache(2048)

const maxReasoningCacheBytes = 32 * 1024 * 1024

// missingReasoningContentFallback 用于历史工具调用的最后一道兼容兜底。
//
// Claude Code 会把部分历史 thinking 变为 redacted_thinking；若代理刚重启、缓存已
// 淘汰，或上一渠道根本未返回可读推理，则原始 reasoning_content 已无法恢复。部分
// DeepSeek 兼容 Chat 渠道会仅因带 tool_calls 的 assistant 历史缺少该字段而拒绝整次
// 请求。此值不伪造原始推理，只为维持 Chat 消息协议的字段连续性；正常情况下始终
// 优先使用上游返回并缓存的原始 reasoning_content。
const missingReasoningContentFallback = "Historical tool-call reasoning was redacted during protocol conversion."

func newReasoningContentCache(max int) *reasoningContentCache {
	if max <= 0 {
		max = 2048
	}
	return &reasoningContentCache{
		entries:  make(map[string]string),
		max:      max,
		maxBytes: maxReasoningCacheBytes,
	}
}

func (c *reasoningContentCache) put(key, value string) {
	if c == nil || key == "" || value == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(key)+len(value) > c.maxBytes {
		return
	}
	if old, exists := c.entries[key]; exists {
		c.usedBytes -= len(key) + len(old)
	} else {
		c.order = append(c.order, key)
	}
	c.entries[key] = value
	c.usedBytes += len(key) + len(value)
	for len(c.order) > c.max || c.usedBytes > c.maxBytes {
		oldest := c.order[0]
		c.order = c.order[1:]
		if old, exists := c.entries[oldest]; exists {
			c.usedBytes -= len(oldest) + len(old)
			delete(c.entries, oldest)
		}
	}
}

func (c *reasoningContentCache) get(key string) string {
	if c == nil || key == "" {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.entries[key]
}

// reset 仅供测试清理全局状态。
func (c *reasoningContentCache) reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]string)
	c.order = nil
	c.usedBytes = 0
}

// reasoningFingerprint 生成缓存键的指纹。
// 使用 sha256 前 24 位十六进制，避免用完整内容做键占用内存。
func reasoningFingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:24]
}

// storeReasoning 保存一条 assistant 文本与其 reasoning_content 的映射。
// reasoning 为空或文本为空时不保存（无法作为回传键）。
func storeReasoning(text, reasoning string) {
	if strings.TrimSpace(text) == "" || reasoning == "" {
		return
	}
	reasoningCache.put(reasoningFingerprint(text), reasoning)
}

// lookupReasoning 查询指定 assistant 文本对应的 reasoning_content。
// 仅用于客户端回传历史时缺失明文 thinking 的兜底补回。
func lookupReasoning(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return reasoningCache.get(reasoningFingerprint(text))
}

// reasoningToolCallIdentity 是用于关联同一条 assistant 工具调用消息的稳定表示。
// type 是固定的 function，不参与指纹；部分兼容上游会在响应中省略它。
type reasoningToolCallIdentity struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// reasoningMessageFingerprint 为 assistant 消息生成缓存键。仅以文本匹配无法覆盖
// Claude Code 常见的“thinking + tool_use”回合，因此将工具调用的 ID、名称和参数
// 一并纳入。参数会先规范化 JSON，避免上游响应与 Claude 回传时仅格式不同而失配。
func reasoningMessageFingerprint(message types.OpenAIMessage) string {
	text := extractOpenAIMessageText(message)
	if strings.TrimSpace(text) == "" && len(message.ToolCalls) == 0 {
		return ""
	}

	toolCalls := make([]reasoningToolCallIdentity, 0, len(message.ToolCalls))
	for _, toolCall := range message.ToolCalls {
		toolCalls = append(toolCalls, reasoningToolCallIdentity{
			ID:        toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: normalizeReasoningToolArguments(toolCall.Function.Arguments),
		})
	}

	payload, err := json.Marshal(struct {
		Text      string                      `json:"text"`
		ToolCalls []reasoningToolCallIdentity `json:"tool_calls,omitempty"`
	}{
		Text:      text,
		ToolCalls: toolCalls,
	})
	if err != nil {
		return ""
	}
	return reasoningFingerprint("assistant-message:" + string(payload))
}

func normalizeReasoningToolArguments(arguments string) string {
	var value interface{}
	if err := json.Unmarshal([]byte(arguments), &value); err != nil {
		return strings.TrimSpace(arguments)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return strings.TrimSpace(arguments)
	}
	return string(canonical)
}

func reasoningToolCallFingerprint(id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	return reasoningFingerprint("assistant-tool-call:" + id)
}

// storeReasoningForAssistantMessage 保存完整助手消息与 reasoning_content 的关联。
// 工具调用消息没有文本也必须缓存，否则 Claude Code 回传 redacted_thinking 后无法
// 满足上游对 reasoning_content 原样回传的要求。
func storeReasoningForAssistantMessage(message types.OpenAIMessage, reasoning string) {
	if reasoning == "" {
		return
	}
	if key := reasoningMessageFingerprint(message); key != "" {
		reasoningCache.put(key, reasoning)
	}
	// Claude Code 在上下文压缩后可能裁剪 tool_use.input，但通常保留工具调用 ID。
	// 因此以 ID 额外保存受控回退键；工具调用 ID 为单轮响应生成，碰撞风险极低。
	for _, toolCall := range message.ToolCalls {
		if key := reasoningToolCallFingerprint(toolCall.ID); key != "" {
			reasoningCache.put(key, reasoning)
		}
	}
}

// lookupReasoningForAssistantMessage 查询完整助手消息对应的 reasoning_content。
// 无工具调用时兼容旧版纯文本缓存；有工具调用时绝不退化为文本匹配，以防将不同的
// 工具调用错误地关联到同一份 reasoning_content。
func lookupReasoningForAssistantMessage(message types.OpenAIMessage) string {
	if key := reasoningMessageFingerprint(message); key != "" {
		if reasoning := reasoningCache.get(key); reasoning != "" {
			return reasoning
		}
	}
	for _, toolCall := range message.ToolCalls {
		if key := reasoningToolCallFingerprint(toolCall.ID); key != "" {
			if reasoning := reasoningCache.get(key); reasoning != "" {
				return reasoning
			}
		}
	}
	if len(message.ToolCalls) == 0 {
		return lookupReasoning(extractOpenAIMessageText(message))
	}
	return ""
}

// reasoningContentForAssistantMessage 返回应随 assistant 历史消息回传的推理内容。
// 有原始 thinking 或缓存命中时直接使用原值；带工具调用、已见 redacted_thinking，
// 或渠道已确认严格校验时，原值永久不可恢复则返回兼容占位，避免 400。
func reasoningContentForAssistantMessage(message types.OpenAIMessage, force bool) string {
	if reasoning := lookupReasoningForAssistantMessage(message); reasoning != "" {
		return reasoning
	}
	if force || len(message.ToolCalls) > 0 {
		return missingReasoningContentFallback
	}
	return ""
}

// CacheClaudeResponseReasoning 将任意上游转换后的 Claude 响应登记到 Chat
// reasoning 缓存。这样当故障转移从 Claude/Gemini/Responses 到要求
// reasoning_content 的 Chat 渠道时，仍可保留上一轮的思考上下文。
func CacheClaudeResponseReasoning(response *types.ClaudeResponse) {
	if response == nil || response.Role != "assistant" {
		return
	}

	var text strings.Builder
	var reasoning strings.Builder
	toolCalls := make([]types.OpenAIToolCall, 0)
	for _, content := range response.Content {
		switch content.Type {
		case "thinking":
			reasoning.WriteString(content.Thinking)
		case "text":
			text.WriteString(content.Text)
		case "tool_use":
			arguments, err := json.Marshal(content.Input)
			if err != nil || string(arguments) == "null" {
				arguments = []byte("{}")
			}
			toolCalls = append(toolCalls, types.OpenAIToolCall{
				ID:   content.ID,
				Type: "function",
				Function: types.OpenAIToolCallFunction{
					Name:      content.Name,
					Arguments: string(arguments),
				},
			})
		}
	}

	storeReasoningForAssistantMessage(types.OpenAIMessage{
		Role:      "assistant",
		Content:   text.String(),
		ToolCalls: toolCalls,
	}, reasoning.String())
}

// clearReasoningContentCacheForTest 清空全局缓存，仅供测试使用。
func clearReasoningContentCacheForTest() {
	reasoningCache.reset()
}

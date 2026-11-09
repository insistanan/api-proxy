package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"strings"
	"sync"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/types"
)

// reasoningContentCache 保存 "assistant 消息指纹 → reasoning_content" 的进程内缓存。
//
// 背景：Chat 推理模型（如 DeepSeek）要求将 thinking 模式产生的 reasoning_content
// 原样随 assistant 历史消息回传，否则下一轮返回 400
// （"The reasoning_content in the thinking mode must be passed back to the API."，
// Claude 兼容网关则报 "The content[].thinking in the thinking mode must be passed back..."）。
//
// 默认由客户端保存并回传原文。缓存仅辅助恢复丢失的历史，并严格限定会话、
// 渠道和模型来源；没有会话标识时禁止使用全局文本匹配。
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

// ReasoningCacheScope 生成不含凭据的来源范围；缺少会话时不启用缓存。
func ReasoningCacheScope(conversationID string, upstream *config.UpstreamConfig, model string) string {
	if strings.TrimSpace(conversationID) == "" || upstream == nil {
		return ""
	}
	payload, _ := json.Marshal([]string{
		conversationID, upstream.ID, upstream.ServiceType,
		config.ResolveUpstreamModel(model, upstream), upstream.GetEffectiveBaseURL(),
	})
	return reasoningFingerprint(string(payload))
}

func scopedReasoningKey(scope, key string) string {
	if scope == "" || key == "" {
		return ""
	}
	return scope + ":" + key
}

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
	if old, exists := c.entries[key]; exists {
		c.usedBytes -= len(key) + len(old)
		if old != value {
			// 同一范围重复正文也可能对应不同推理，标记歧义后不再猜测。
			value = ""
			if old != "" {
				log.Printf("[Reasoning-Cache] 消息关联存在歧义，已禁用该缓存键")
			}
		}
	} else {
		if len(key)+len(value) > c.maxBytes {
			log.Printf("[Reasoning-Cache] 单条推理超过缓存容量，未登记")
			return
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = value
	c.usedBytes += len(key) + len(value)
	evicted := 0
	for len(c.order) > c.max || c.usedBytes > c.maxBytes {
		oldest := c.order[0]
		c.order = c.order[1:]
		if old, exists := c.entries[oldest]; exists {
			c.usedBytes -= len(oldest) + len(old)
			delete(c.entries, oldest)
			evicted++
		}
	}
	if evicted > 0 {
		log.Printf("[Reasoning-Cache] 容量淘汰 %d 项", evicted)
	}
}

func (c *reasoningContentCache) get(key string) string {
	value, _ := c.lookup(key)
	return value
}

// lookup 区分未命中和已标记歧义；歧义键不能再通过工具 ID 猜测恢复。
func (c *reasoningContentCache) lookup(key string) (string, bool) {
	if c == nil || key == "" {
		return "", false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	value, exists := c.entries[key]
	return value, exists
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
func storeReasoning(text, reasoning string, scopes ...string) {
	if strings.TrimSpace(text) == "" || reasoning == "" {
		return
	}
	storeReasoningForAssistantMessage(types.OpenAIMessage{Role: "assistant", Content: text}, reasoning, scopes...)
}

// lookupReasoning 查询指定 assistant 文本对应的 reasoning_content。
// 仅用于客户端回传历史时缺失明文 thinking 的兜底补回。
func lookupReasoning(text string, scopes ...string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return lookupReasoningForAssistantMessage(types.OpenAIMessage{Role: "assistant", Content: text}, scopes...)
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
func storeReasoningForAssistantMessage(message types.OpenAIMessage, reasoning string, scopes ...string) {
	if reasoning == "" || len(scopes) == 0 || scopes[0] == "" {
		return
	}
	if key := reasoningMessageFingerprint(message); key != "" {
		reasoningCache.put(scopedReasoningKey(scopes[0], key), reasoning)
	}
	// Claude Code 在上下文压缩后可能裁剪 tool_use.input，但通常保留工具调用 ID。
	// 因此以 ID 额外保存受控回退键；工具调用 ID 为单轮响应生成，碰撞风险极低。
	for _, toolCall := range message.ToolCalls {
		if key := reasoningToolCallFingerprint(toolCall.ID); key != "" {
			reasoningCache.put(scopedReasoningKey(scopes[0], key), reasoning)
		}
	}
}

// lookupReasoningForAssistantMessage 查询完整助手消息对应的 reasoning_content。
// 只匹配同一来源范围，禁止退回未隔离的全局文本缓存。
func lookupReasoningForAssistantMessage(message types.OpenAIMessage, scopes ...string) string {
	if len(scopes) == 0 || scopes[0] == "" {
		return ""
	}
	if key := reasoningMessageFingerprint(message); key != "" {
		if reasoning, exists := reasoningCache.lookup(scopedReasoningKey(scopes[0], key)); exists {
			return reasoning
		}
	}
	var recovered string
	for _, toolCall := range message.ToolCalls {
		if key := reasoningToolCallFingerprint(toolCall.ID); key != "" {
			if reasoning, exists := reasoningCache.lookup(scopedReasoningKey(scopes[0], key)); exists {
				if reasoning == "" {
					return ""
				}
				if recovered != "" && recovered != reasoning {
					return ""
				}
				recovered = reasoning
			}
		}
	}
	return recovered
}

// reasoningContentForAssistantMessage 返回应随 assistant 历史消息回传的推理内容。
// 仅返回缓存中的原文，必需历史缺失由请求构造层显式报错。
func reasoningContentForAssistantMessage(message types.OpenAIMessage, scope string) string {
	return lookupReasoningForAssistantMessage(message, scope)
}

// CacheClaudeResponseReasoning 在入口已知会话和来源时登记缓存。
// 旧调用没有来源范围时不缓存；跨渠道续接依赖客户端回传原文。
func CacheClaudeResponseReasoning(response *types.ClaudeResponse, scopes ...string) {
	if response == nil || response.Role != "assistant" || len(scopes) == 0 || scopes[0] == "" {
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
	}, reasoning.String(), scopes[0])
}

// clearReasoningContentCacheForTest 清空全局缓存，仅供测试使用。
func clearReasoningContentCacheForTest() {
	reasoningCache.reset()
}

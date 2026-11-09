package session

import (
	"sync"
	"time"

	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
)

// ResponseChainState 记录 Messages→Responses 链式上下文状态。
// 注意：OpenAI 官方文档明确 previous_response_id 链上的历史 input 仍会计费；
// 本能力主要用于减少重复传输与提升上游缓存亲和，不是“免费上下文”。
type ResponseChainState struct {
	UpstreamIdentity upstreamcontrol.Identity
	ResponseID       string
	MessageCount     int
	// OutputFingerprint 是上一条 Responses response 的可见 assistant output
	// 指纹。Messages 客户端下一轮通常会重放该 assistant 消息；携带
	// previous_response_id 时必须把这段已存在于服务端链中的消息从后缀中移除。
	OutputFingerprint string
	// MessageFingerprint 是建立链时客户端已发送消息序列的规范指纹。
	// 仅比较数量不足以识别 Cursor 压缩后“同数量但内容已替换”的历史。
	MessageFingerprint string
	SystemFingerprint  string
	ToolsFingerprint   string
	BaseURL            string
	Model              string
	UpdatedAt          time.Time
}

// ResponseChainManager 按 conversationID 保存最近一次成功的 Responses 链状态。
type ResponseChainManager struct {
	mu    sync.RWMutex
	items map[string]*ResponseChainState
	ttl   time.Duration
}

var defaultResponseChainManager = NewResponseChainManager(30 * time.Minute)

// DefaultResponseChainManager 返回进程级默认管理器。
func DefaultResponseChainManager() *ResponseChainManager {
	return defaultResponseChainManager
}

// NewResponseChainManager 创建链式上下文管理器。
func NewResponseChainManager(ttl time.Duration) *ResponseChainManager {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &ResponseChainManager{
		items: make(map[string]*ResponseChainState),
		ttl:   ttl,
	}
}

// Get 获取会话链状态。
func (manager *ResponseChainManager) Get(conversationID string) (*ResponseChainState, bool) {
	if manager == nil || conversationID == "" {
		return nil, false
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	state, exists := manager.items[conversationID]
	if !exists || state == nil {
		return nil, false
	}
	if time.Since(state.UpdatedAt) > manager.ttl {
		return nil, false
	}
	copied := *state
	return &copied, true
}

// Set 保存会话链状态。
func (manager *ResponseChainManager) Set(conversationID string, state ResponseChainState) {
	if manager == nil || conversationID == "" || state.ResponseID == "" {
		return
	}
	state.UpdatedAt = time.Now()
	manager.mu.Lock()
	manager.items[conversationID] = &state
	manager.mu.Unlock()
}

// Clear 清除会话链状态（例如 previous_response_id 失效时回退全量上下文）。
func (manager *ResponseChainManager) Clear(conversationID string) {
	if manager == nil || conversationID == "" {
		return
	}
	manager.mu.Lock()
	delete(manager.items, conversationID)
	manager.mu.Unlock()
}

// ClearAll 清除全部进程内 Responses 链状态。
func (manager *ResponseChainManager) ClearAll() {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	manager.items = make(map[string]*ResponseChainState)
	manager.mu.Unlock()
}

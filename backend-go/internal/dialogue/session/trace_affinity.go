package session

import (
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// affinityDebug 控制亲和性日志是否输出
// 通过环境变量 AFFINITY_DEBUG=true 启用
var affinityDebug = os.Getenv("AFFINITY_DEBUG") == "true"

const defaultTraceAffinityKind = "messages"

// TraceAffinity 记录 trace 与渠道的亲和关系
type TraceAffinity struct {
	ChannelIndex int
	LastUsedAt   time.Time
}

// TraceAffinityManager 管理 trace 与渠道的亲和性
type TraceAffinityManager struct {
	mu       sync.RWMutex
	affinity map[string]*TraceAffinity // key: channel_kind + conversation_id
	ttl      time.Duration
	stopCh   chan struct{} // 用于停止清理 goroutine
	doneCh   chan struct{}
	stopOnce sync.Once
}

// NewTraceAffinityManager 创建 Trace 亲和性管理器（默认 30 分钟无活动过期）
func NewTraceAffinityManager() *TraceAffinityManager {
	return newTraceAffinityManagerWithTTL(0)
}

// newTraceAffinityManagerWithTTL 创建带自定义 TTL 的管理器；ttl <= 0 时取默认值。
func newTraceAffinityManagerWithTTL(ttl time.Duration) *TraceAffinityManager {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}

	mgr := &TraceAffinityManager{
		affinity: make(map[string]*TraceAffinity),
		ttl:      ttl,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}

	// 启动定期清理
	go mgr.cleanupLoop()

	return mgr
}

// GetPreferredChannelForKind 获取指定渠道池内的 conversation record 偏好渠道。
func (m *TraceAffinityManager) GetPreferredChannelForKind(kind string, conversationID string) (int, bool) {
	if conversationID == "" {
		return -1, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	affinity, exists := m.affinity[traceAffinityKey(kind, conversationID)]
	if !exists {
		return -1, false
	}

	// 检查是否过期
	if time.Since(affinity.LastUsedAt) > m.ttl {
		return -1, false
	}

	return affinity.ChannelIndex, true
}

// SetPreferredChannelForKind 设置指定渠道池内的 conversation record 偏好渠道。
func (m *TraceAffinityManager) SetPreferredChannelForKind(kind string, conversationID string, channelIndex int) {
	if conversationID == "" {
		return
	}

	var logType int // 0=无, 1=新建, 2=变更
	var oldChannel int

	m.mu.Lock()
	key := traceAffinityKey(kind, conversationID)
	oldAffinity, existed := m.affinity[key]
	if existed && oldAffinity.ChannelIndex != channelIndex {
		logType, oldChannel = 2, oldAffinity.ChannelIndex
	} else if !existed {
		logType = 1
	}
	m.affinity[key] = &TraceAffinity{
		ChannelIndex: channelIndex,
		LastUsedAt:   time.Now(),
	}
	m.mu.Unlock()

	if affinityDebug {
		if logType == 2 {
			log.Printf("[Affinity-Set] %s 会话亲和变更: %s -> 渠道[%d] (原渠道[%d])", normalizedAffinityKind(kind), maskSessionKey(conversationID), channelIndex, oldChannel)
		} else if logType == 1 {
			log.Printf("[Affinity-Set] 新建 %s 会话亲和: %s -> 渠道[%d]", normalizedAffinityKind(kind), maskSessionKey(conversationID), channelIndex)
		}
	}
}

// RemoveForKind 移除指定渠道池内的亲和记录。
func (m *TraceAffinityManager) RemoveForKind(kind string, conversationID string) {
	var oldChannel int
	var existed bool

	m.mu.Lock()
	key := traceAffinityKey(kind, conversationID)
	if affinity, exists := m.affinity[key]; exists {
		oldChannel, existed = affinity.ChannelIndex, true
		delete(m.affinity, key)
	}
	m.mu.Unlock()

	if affinityDebug && existed {
		log.Printf("[Affinity-Remove] 移除 %s 会话亲和: %s (原渠道[%d])", normalizedAffinityKind(kind), maskSessionKey(conversationID), oldChannel)
	}
}

func normalizedAffinityKind(kind string) string {
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind == "" {
		return defaultTraceAffinityKind
	}
	return kind
}

func traceAffinityKey(kind string, conversationID string) string {
	return normalizedAffinityKind(kind) + "\x00" + conversationID
}

// RemoveByChannelForKind 移除指定渠道池中某个渠道的所有亲和记录。
func (m *TraceAffinityManager) RemoveByChannelForKind(kind string, channelIndex int) {
	m.mu.Lock()
	removed := 0
	prefix := normalizedAffinityKind(kind) + "\x00"
	for key, affinity := range m.affinity {
		if strings.HasPrefix(key, prefix) && affinity.ChannelIndex == channelIndex {
			delete(m.affinity, key)
			removed++
		}
	}
	m.mu.Unlock()

	if affinityDebug && removed > 0 {
		log.Printf("[Affinity-RemoveByChannel] %s 渠道[%d]被移除，清理了 %d 条亲和记录", normalizedAffinityKind(kind), channelIndex, removed)
	}
}

// Cleanup 清理过期的亲和记录
func (m *TraceAffinityManager) Cleanup() int {
	m.mu.Lock()
	now := time.Now()
	cleaned := 0
	for conversationID, affinity := range m.affinity {
		if now.Sub(affinity.LastUsedAt) > m.ttl {
			delete(m.affinity, conversationID)
			cleaned++
		}
	}
	ttl := m.ttl
	m.mu.Unlock()

	if affinityDebug && cleaned > 0 {
		log.Printf("[Affinity-Cleanup] 清理了 %d 条过期会话亲和记录 (TTL: %v)", cleaned, ttl)
	}

	return cleaned
}

// cleanupLoop 定期清理过期记录
func (m *TraceAffinityManager) cleanupLoop() {
	defer close(m.doneCh)
	ticker := time.NewTicker(5 * time.Minute) // 每 5 分钟清理一次
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.Cleanup()
		case <-m.stopCh:
			return
		}
	}
}

// Stop 停止并等待清理退出；并发或重复调用安全。
func (m *TraceAffinityManager) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
	<-m.doneCh
}

// SizeForKind 返回指定渠道池的亲和记录数量。
func (m *TraceAffinityManager) SizeForKind(kind string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := normalizedAffinityKind(kind) + "\x00"
	count := 0
	for key := range m.affinity {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	return count
}

// GetTTL 获取 TTL 设置
func (m *TraceAffinityManager) GetTTL() time.Duration {
	return m.ttl
}

// maskSessionKey 掩码亲和键（保护隐私）。
// 使用 rune 切片确保 UTF-8 安全
func maskSessionKey(sessionKey string) string {
	if sessionKey == "" {
		return "***"
	}
	runes := []rune(sessionKey)
	n := len(runes)
	switch {
	case n <= 4:
		return string(runes[:1]) + "***"
	case n <= 8:
		return string(runes[:2]) + "***" + string(runes[n-1:])
	case n <= 16:
		return string(runes[:3]) + "***" + string(runes[n-2:])
	default:
		return string(runes[:8]) + "***" + string(runes[n-4:])
	}
}

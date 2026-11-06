package session

import (
	"log"
	"os"
	"sync"
	"time"
)

// baseURLAffinityDebug 控制 BaseURL 亲和日志；AFFINITY_DEBUG=true 时启用。
var baseURLAffinityDebug = os.Getenv("AFFINITY_DEBUG") == "true"

// BaseURLAffinity 记录会话与上游 BaseURL 的粘滞关系，用于提升 prompt cache 命中率。
type BaseURLAffinity struct {
	BaseURL    string
	LastUsedAt time.Time
}

// BaseURLAffinityManager 按 conversationID（必要时兼容旧 session key）记住最近成功的 BaseURL。
type BaseURLAffinityManager struct {
	mu       sync.RWMutex
	affinity map[string]*BaseURLAffinity
	ttl      time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// NewBaseURLAffinityManager 创建默认 30 分钟 TTL 的管理器。
func NewBaseURLAffinityManager() *BaseURLAffinityManager {
	return newBaseURLAffinityManagerWithTTL(30 * time.Minute)
}

// newBaseURLAffinityManagerWithTTL 创建自定义 TTL 的管理器。
func newBaseURLAffinityManagerWithTTL(ttl time.Duration) *BaseURLAffinityManager {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	manager := &BaseURLAffinityManager{
		affinity: make(map[string]*BaseURLAffinity),
		ttl:      ttl,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	go manager.cleanupLoop()
	return manager
}

// GetPreferredBaseURL 返回会话偏好的 BaseURL。
func (manager *BaseURLAffinityManager) GetPreferredBaseURL(sessionKey string) (string, bool) {
	if sessionKey == "" || manager == nil {
		return "", false
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	entry, exists := manager.affinity[sessionKey]
	if !exists {
		return "", false
	}
	if time.Since(entry.LastUsedAt) > manager.ttl {
		return "", false
	}
	if entry.BaseURL == "" {
		return "", false
	}
	return entry.BaseURL, true
}

// SetPreferredBaseURL 记录会话最近成功的 BaseURL。
func (manager *BaseURLAffinityManager) SetPreferredBaseURL(sessionKey string, baseURL string) {
	if manager == nil || sessionKey == "" || baseURL == "" {
		return
	}

	var logKind int // 0 none, 1 create, 2 change
	var previous string

	manager.mu.Lock()
	oldEntry, existed := manager.affinity[sessionKey]
	if existed && oldEntry.BaseURL != baseURL {
		logKind = 2
		previous = oldEntry.BaseURL
	} else if !existed {
		logKind = 1
	}
	manager.affinity[sessionKey] = &BaseURLAffinity{
		BaseURL:    baseURL,
		LastUsedAt: time.Now(),
	}
	manager.mu.Unlock()

	if baseURLAffinityDebug {
		if logKind == 2 {
			log.Printf("[BaseURL-Affinity] session %s: %s -> %s", maskSessionKey(sessionKey), previous, baseURL)
		} else if logKind == 1 {
			log.Printf("[BaseURL-Affinity] session %s sticky %s", maskSessionKey(sessionKey), baseURL)
		}
	}
}

// Cleanup 清理过期记录。
func (manager *BaseURLAffinityManager) Cleanup() int {
	if manager == nil {
		return 0
	}
	manager.mu.Lock()
	now := time.Now()
	cleaned := 0
	for key, entry := range manager.affinity {
		if now.Sub(entry.LastUsedAt) > manager.ttl {
			delete(manager.affinity, key)
			cleaned++
		}
	}
	manager.mu.Unlock()
	if baseURLAffinityDebug && cleaned > 0 {
		log.Printf("[BaseURL-Affinity] cleaned %d expired entries", cleaned)
	}
	return cleaned
}

func (manager *BaseURLAffinityManager) cleanupLoop() {
	defer close(manager.doneCh)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			manager.Cleanup()
		case <-manager.stopCh:
			return
		}
	}
}

// Stop 停止并等待后台清理退出；并发或重复调用安全。
func (manager *BaseURLAffinityManager) Stop() {
	if manager == nil {
		return
	}
	manager.stopOnce.Do(func() { close(manager.stopCh) })
	<-manager.doneCh
}

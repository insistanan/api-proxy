package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/insistanan/api-proxy/internal/dialogue/internal/syncstate"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/insistanan/api-proxy/internal/dialogue/conversation"
	respitems "github.com/insistanan/api-proxy/internal/protocol/items"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/protocol/visionmedia"
)

// Session 会话数据结构。
// 定义已下沉到 types（纯数据体，converters 依赖它做参数类型），
// 此处保留别名以兼容 session 包内外的既有引用。
type Session = types.Session

// SessionManager 会话管理器
type SessionManager struct {
	sessions        map[string]*Session // sessionID → Session
	responseMapping map[string]string   // responseID → sessionID
	responseOwners  map[string]string   // responseID → 上游组合摘要，与轮次原子提交
	mu              syncstate.Mutex
	active          map[string]int
	registry        *conversation.Registry

	// 清理配置
	maxAge      time.Duration // 最大保留时间
	maxMessages int           // 单会话消息上限，<= 0 表示不限制
	maxTokens   int           // 单会话 Token 上限，<= 0 表示不限制

	// 资源限制
	maxSessions       int // 全局最大 session 数，0 表示不限制
	maxLoadedSessions int // 内存缓存上限；持久化历史通过 response ID 按需恢复
	store             *sqliteStore
	stopCh            chan struct{}
	doneCh            chan struct{}
	stopOnce          sync.Once
}

func NewPersistentSessionManager(path string, maxAge time.Duration, maxMessages int, maxTokens int) (*SessionManager, error) {
	store, err := newSQLiteStore(path)
	if err != nil {
		return nil, err
	}
	sm := newSessionManager(maxAge, maxMessages, maxTokens, store)
	go sm.cleanupLoop()
	return sm, nil
}

// NewPersistentSessionManagerWithDB 借用共享连接；Stop 只停止会话后台任务。
func NewPersistentSessionManagerWithDB(db *sql.DB, maxAge time.Duration, maxMessages int, maxTokens int) (*SessionManager, error) {
	store, err := newSQLiteStoreWithDB(db)
	if err != nil {
		return nil, err
	}
	sm := newSessionManager(maxAge, maxMessages, maxTokens, store)
	go sm.cleanupLoop()
	return sm, nil
}

func newSessionManager(maxAge time.Duration, maxMessages int, maxTokens int, store *sqliteStore) *SessionManager {
	return &SessionManager{
		sessions:          make(map[string]*Session),
		active:            make(map[string]int),
		responseMapping:   make(map[string]string),
		responseOwners:    make(map[string]string),
		maxAge:            maxAge,
		maxMessages:       maxMessages,
		maxTokens:         maxTokens,
		maxSessions:       0, // 默认不限制，由调用方按需设置
		maxLoadedSessions: 128,
		store:             store,
		stopCh:            make(chan struct{}),
		doneCh:            make(chan struct{}),
	}
}

func (sm *SessionManager) Stop() {
	if sm == nil {
		return
	}
	sm.stopOnce.Do(func() {
		close(sm.stopCh)
		<-sm.doneCh
		if sm.store != nil {
			if err := sm.store.close(); err != nil {
				log.Printf("[Session] 关闭持久化存储失败: %v", err)
			}
		}
	})
}

// SetMaxSessions 设置全局最大 session 数量上限
// 设置为 0 表示不限制（默认）
// 达到上限时，新会话会触发淘汰最久未访问的 session
func (sm *SessionManager) SetMaxSessions(limit int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxSessions = limit
}

// GetOrCreateSessionForConversation 返回隔离快照；生产请求通过 BeginTurn 同时固定版本和保留租约。
func (sm *SessionManager) GetOrCreateSessionForConversation(ctx context.Context, previous, conversationID string) (*Session, error) {
	return sm.snapshot(ctx, previous, conversationID, false)
}

func (sm *SessionManager) snapshot(ctx context.Context, previous, conversationID string, pin bool) (*Session, error) {
	var result *Session
	err := sm.withConversation(ctx, conversationID, func() error {
		if err := sm.mu.LockContext(ctx); err != nil {
			return err
		}
		defer sm.mu.Unlock()
		var err error
		result, err = sm.getOrCreateLocked(ctx, strings.TrimSpace(previous), strings.TrimSpace(conversationID))
		if err == nil && pin {
			sm.active[result.ID]++
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	cloned, err := cloneSession(result)
	if err != nil && pin {
		sm.release(result.ID)
	}
	return cloned, err
}

func (sm *SessionManager) getOrCreateLocked(ctx context.Context, previous, conversationID string) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var current *Session
	if previous != "" {
		current = sm.sessions[sm.responseMapping[previous]]
	} else if conversationID != "" {
		for _, candidate := range sm.sessions {
			if candidate.ConversationID == conversationID && (current == nil || candidate.LastAccessAt.After(current.LastAccessAt)) {
				current = candidate
			}
		}
	}
	if current == nil && sm.store != nil {
		var err error
		if previous != "" {
			current, err = sm.store.load(ctx, `WHERE rs.id=(SELECT session_id FROM response_session_mappings WHERE response_id=?)`, previous)
		} else if conversationID != "" {
			current, err = sm.store.load(ctx, `WHERE rs.conversation_id=? ORDER BY COALESCE(rsa.last_access_at,rs.last_access_at) DESC,rs.id DESC LIMIT 1`, conversationID)
		}
		if err != nil {
			return nil, err
		}
	}
	if current != nil && sm.expired(current) && sm.active[current.ID] == 0 {
		sm.removeCachedSessionLocked(current.ID)
		current = nil
	}
	if current == nil {
		if previous != "" {
			return nil, &HistoryError{Message: "previous_response_id 未找到可恢复的本地会话，请开启新会话并提供完整历史"}
		}
		id, err := generateID("sess")
		if err != nil {
			return nil, err
		}
		now := time.Now()
		current = &Session{ID: id, ConversationID: conversationID, Messages: []types.ResponsesItem{}, CreatedAt: now, LastAccessAt: now}
		if sm.store != nil {
			removed, err := sm.store.create(ctx, current, sm.maxSessions, sm.activeIDsLocked())
			if err != nil {
				return nil, err
			}
			for _, id := range removed {
				sm.removeCachedSessionLocked(id)
			}
		}
	} else {
		candidate := *current
		if conversationID != "" && candidate.ConversationID != conversationID {
			return nil, &HistoryError{Message: "Responses 历史属于其他会话"}
		}
		candidate.LastAccessAt = time.Now()
		if sm.store != nil {
			if err := sm.store.touch(ctx, &candidate); err != nil {
				return nil, err
			}
		}
		current = &candidate
	}
	sm.cacheLoadedSessionLocked(current, previous)
	return current, nil
}

func (sm *SessionManager) expired(current *Session) bool {
	return sm.maxAge > 0 && time.Since(current.LastAccessAt) > sm.maxAge
}

func (sm *SessionManager) activeIDsLocked() []string {
	ids := make([]string, 0, len(sm.active))
	for id, count := range sm.active {
		if count > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func (sm *SessionManager) cacheLoadedSessionLocked(current *Session, responseID string) {
	if sm.store != nil && sm.maxLoadedSessions > 0 && len(sm.sessions) >= sm.maxLoadedSessions && sm.sessions[current.ID] == nil {
		var oldest *Session
		for id, candidate := range sm.sessions {
			if sm.active[id] == 0 && (oldest == nil || candidate.LastAccessAt.Before(oldest.LastAccessAt)) {
				oldest = candidate
			}
		}
		if oldest != nil {
			sm.removeCachedSessionLocked(oldest.ID)
		}
	}
	sm.sessions[current.ID] = current
	if responseID != "" {
		sm.responseMapping[responseID] = current.ID
	}
}
func (sm *SessionManager) removeCachedSessionLocked(id string) {
	delete(sm.sessions, id)
	for responseID, sessionID := range sm.responseMapping {
		if sessionID == id {
			sm.forgetResponseMappingLocked(responseID)
		}
	}
}
func cloneSession(current *Session) (*Session, error) {
	if current == nil {
		return nil, nil
	}
	copy := *current
	var err error
	copy.Messages, err = cloneResponsesItems(current.Messages)
	return &copy, err
}
func cloneResponsesItems(items []types.ResponsesItem) ([]types.ResponsesItem, error) {
	data, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("复制会话历史失败: %w", err)
	}
	var copied []types.ResponsesItem
	if err = json.Unmarshal(data, &copied); err != nil {
		return nil, fmt.Errorf("复制会话历史失败: %w", err)
	}
	return copied, nil
}

func (sm *SessionManager) commitSnapshot(ctx context.Context, current *Session, items []types.ResponsesItem, tokens int, vision bool, responseID, owner string, replace bool) error {
	if current == nil {
		return fmt.Errorf("会话快照不能为空")
	}
	copied, err := cloneResponsesItems(items)
	if err != nil {
		return err
	}
	next := *current
	replace = replace || respitems.ResponsesItemsContainCompaction(copied)
	if replace {
		next.Messages = copied
		next.TotalTokens = 0
		next.HasVisionContent = false
	} else {
		next.Messages = respitems.MergeResponsesItemsDedup(current.Messages, copied)
		next.TotalTokens += tokens
		next.HasVisionContent = next.HasVisionContent || vision
	}
	for _, item := range next.Messages {
		if visionmedia.ResponsesItemHasVisionContent(item) {
			next.HasVisionContent = true
		}
	}
	if responseID != "" || replace {
		next.LastResponseID = responseID
	}
	next.LastAccessAt = time.Now()
	next.Version++
	// 所有历史编码在全局锁外完成，失败不会留下任何半更新状态。
	messages, err := json.Marshal(next.Messages)
	if err != nil {
		return err
	}
	return sm.withConversation(ctx, next.ConversationID, func() error {
		if err := sm.mu.LockContext(ctx); err != nil {
			return err
		}
		defer sm.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if cached := sm.sessions[current.ID]; cached != nil && cached.Version != current.Version {
			return fmt.Errorf("会话历史已更新，拒绝合并过时响应")
		}
		alias := ""
		if sm.registry != nil && next.ConversationID != "" && responseID != "" {
			alias = conversation.ResponseAliasKey(responseID)
		}
		if sm.store != nil {
			if err := sm.store.commit(ctx, &next, messages, current.Version, replace, responseID, owner, alias); err != nil {
				return err
			}
		} else {
			if sm.sessions[current.ID] == nil {
				return fmt.Errorf("会话已删除")
			}
			if responseID != "" && sm.responseMapping[responseID] != "" {
				return fmt.Errorf("response ID 已存在，拒绝覆盖历史归属")
			}
		}
		if replace {
			for id, sessionID := range sm.responseMapping {
				if sessionID == next.ID {
					sm.forgetResponseMappingLocked(id)
				}
			}
		}
		sm.cacheLoadedSessionLocked(&next, responseID)
		if responseID != "" {
			sm.responseOwners[responseID] = owner
		}
		return nil
	})
}

func (sm *SessionManager) DeleteConversation(id string) error {
	return sm.deleteWith(context.Background(), id, nil, nil)
}
func (sm *SessionManager) DeleteAll() error { return sm.deleteWith(context.Background(), "", nil, nil) }

func (sm *SessionManager) cleanupLoop() {
	defer close(sm.doneCh)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			sm.cleanup()
		case <-sm.stopCh:
			return
		}
	}
}
func (sm *SessionManager) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	var removed []string
	if sm.store != nil {
		var cutoff time.Time
		if sm.maxAge > 0 {
			cutoff = time.Now().Add(-sm.maxAge)
		}
		var err error
		removed, err = sm.store.prune(ctx, cutoff, sm.maxMessages, sm.maxTokens, sm.activeIDsLocked())
		if err != nil {
			log.Printf("[Session-Cleanup] 清理失败: %v", err)
			return
		}
	} else {
		for id, current := range sm.sessions {
			if sm.active[id] == 0 && (sm.expired(current) || sm.maxMessages > 0 && len(current.Messages) > sm.maxMessages || sm.maxTokens > 0 && current.TotalTokens > sm.maxTokens) {
				removed = append(removed, id)
			}
		}
	}
	for _, id := range removed {
		sm.removeCachedSessionLocked(id)
	}
}
func generateID(prefix string) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("生成会话 ID 失败: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(bytes), nil
}

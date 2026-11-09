package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
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
	mu              sync.RWMutex

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

func newSessionManager(maxAge time.Duration, maxMessages int, maxTokens int, store *sqliteStore) *SessionManager {
	return &SessionManager{
		sessions:          make(map[string]*Session),
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

// GetOrCreateSession 获取或创建会话
func (sm *SessionManager) GetOrCreateSession(previousResponseID string) (*Session, error) {
	return sm.GetOrCreateSessionForConversation(previousResponseID, "")
}

// GetOrCreateSessionForConversation 获取或创建会话，并将 Responses 会话链关联到持久化对话记录。
func (sm *SessionManager) GetOrCreateSessionForConversation(previousResponseID string, conversationID string) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	previousResponseID = strings.TrimSpace(previousResponseID)
	conversationID = strings.TrimSpace(conversationID)

	// 如果提供了 previousResponseID，尝试查找对应的会话
	if previousResponseID != "" {
		if sessionID, ok := sm.responseMapping[previousResponseID]; ok {
			if session, exists := sm.sessions[sessionID]; exists {
				previousConversationID := session.ConversationID
				if err := bindConversation(session, conversationID); err != nil {
					return nil, err
				}
				session.LastAccessAt = time.Now()
				if sm.store != nil {
					if err := sm.store.touchSession(session, previousConversationID != session.ConversationID); err != nil {
						session.ConversationID = previousConversationID
						return nil, err
					}
				}
				return cloneSession(session), nil
			}
		}
		if sm.store != nil {
			session, found, err := sm.store.loadByResponseID(previousResponseID, sm.maxAge)
			if err != nil {
				return nil, err
			}
			if found {
				sm.cacheLoadedSessionLocked(session, previousResponseID)
				previousConversationID := session.ConversationID
				if err := bindConversation(session, conversationID); err != nil {
					sm.removeCachedSessionLocked(session.ID)
					return nil, err
				}
				session.LastAccessAt = time.Now()
				if err := sm.store.touchSession(session, previousConversationID != session.ConversationID); err != nil {
					return nil, err
				}
				return cloneSession(session), nil
			}
		}
		if conversationID != "" {
			// 主请求已经绑定到明确的代理 conversation。此时无法确认外部
			// response ID 是否属于该会话，创建空 session 会让原生上游把
			// 完整 input 与旧服务端链叠加，也会让非原生转换静默丢历史。
			// 必须显式失败，让客户端重新建立可恢复的会话边界。
			return nil, &HistoryError{Message: fmt.Sprintf("previous_response_id %s 未找到可恢复的本地会话；请发送完整对话重新建立会话", previousResponseID)}
		}
		// 仅保留无 conversation 绑定的旧内部调用兼容路径；这类调用无法
		// 做跨会话归属校验，生产请求不应走到这里。
		log.Printf("[Session-Recovery] previous_response_id %s 未找到持久化会话，将从当前输入恢复为空会话", previousResponseID)
		session, err := sm.createSessionLocked(previousResponseID, conversationID)
		return cloneSession(session), err
	}

	if conversationID != "" {
		var latest *Session
		for _, candidate := range sm.sessions {
			if candidate != nil && candidate.ConversationID == conversationID &&
				(latest == nil || candidate.LastAccessAt.After(latest.LastAccessAt)) {
				latest = candidate
			}
		}
		if latest == nil && sm.store != nil {
			loaded, found, err := sm.store.loadLatestByConversation(conversationID, sm.maxAge)
			if err != nil {
				return nil, err
			}
			if found {
				sm.cacheLoadedSessionLocked(loaded, loaded.LastResponseID)
				latest = loaded
			}
		}
		if latest != nil {
			latest.LastAccessAt = time.Now()
			if sm.store != nil {
				if err := sm.store.touchSession(latest, false); err != nil {
					return nil, err
				}
			}
			return cloneSession(latest), nil
		}
	}

	session, err := sm.createSessionLocked("", conversationID)
	return cloneSession(session), err
}

func (sm *SessionManager) createSessionLocked(previousResponseID string, conversationID string) (*Session, error) {
	if err := sm.evictIfNeededLocked(); err != nil {
		return nil, err
	}
	now := time.Now()
	session := &Session{
		ID:             generateID("sess"),
		ConversationID: conversationID,
		Messages:       []types.ResponsesItem{},
		LastResponseID: previousResponseID,
		CreatedAt:      now,
		LastAccessAt:   now,
	}
	sm.sessions[session.ID] = session
	if previousResponseID != "" {
		sm.responseMapping[previousResponseID] = session.ID
	}
	if err := sm.persistSessionLocked(session); err != nil {
		delete(sm.sessions, session.ID)
		sm.forgetResponseMappingLocked(previousResponseID)
		return nil, err
	}
	if previousResponseID != "" && sm.store != nil {
		if err := sm.store.upsertMapping(previousResponseID, session.ID); err != nil {
			delete(sm.sessions, session.ID)
			sm.forgetResponseMappingLocked(previousResponseID)
			if cleanupErr := sm.store.deleteSession(session.ID); cleanupErr != nil {
				return nil, fmt.Errorf("保存 response ID 映射失败: %v；清理未完成会话失败: %w", err, cleanupErr)
			}
			return nil, err
		}
	}
	log.Printf("[Session-Create] 创建新会话: %s (总数: %d)", session.ID, len(sm.sessions))
	return session, nil
}

func bindConversation(session *Session, conversationID string) error {
	if session == nil || conversationID == "" {
		return nil
	}
	if session.ConversationID == "" {
		session.ConversationID = conversationID
		return nil
	}
	if session.ConversationID != conversationID {
		return fmt.Errorf("Responses 会话 %s 已关联到其他对话 %s", session.ID, session.ConversationID)
	}
	return nil
}

// evictIfNeededLocked 当 session 数达到上限时，淘汰最久未访问的 session
// 调用方必须持有写锁
func (sm *SessionManager) evictIfNeededLocked() error {
	if sm.maxSessions > 0 && sm.store != nil {
		ids, err := sm.store.pruneToLimit(sm.maxSessions - 1)
		if err != nil {
			return fmt.Errorf("清理 Responses 会话容量失败: %w", err)
		}
		for _, id := range ids {
			sm.removeCachedSessionLocked(id)
		}
	}
	sm.evictLoadedSessionLocked()
	return nil
}

func (sm *SessionManager) evictLoadedSessionLocked() {
	if sm.maxLoadedSessions <= 0 || len(sm.sessions) < sm.maxLoadedSessions {
		return
	}

	// 找到最久未访问的 session（不包含 responseID 映射仍在使用的）
	var oldestID string
	var oldestTime time.Time
	for id, s := range sm.sessions {
		if s.LastAccessAt.Before(oldestTime) || oldestID == "" {
			oldestID = id
			oldestTime = s.LastAccessAt
		}
	}

	if oldestID != "" {
		log.Printf("[Session-Cache] 内存会话缓存已达上限 (%d)，释放最久未访问会话: %s (最后访问: %v 前)",
			sm.maxLoadedSessions, oldestID, time.Since(oldestTime))
		sm.removeCachedSessionLocked(oldestID)
	}
}

func (sm *SessionManager) cacheLoadedSessionLocked(session *Session, responseID string) {
	if session == nil {
		return
	}
	sm.evictLoadedSessionLocked()
	sm.sessions[session.ID] = session
	if responseID != "" {
		sm.responseMapping[responseID] = session.ID
	}
}

func (sm *SessionManager) removeCachedSessionLocked(sessionID string) {
	delete(sm.sessions, sessionID)
	for responseID, mappedID := range sm.responseMapping {
		if mappedID == sessionID {
			sm.forgetResponseMappingLocked(responseID)
		}
	}
}

// RecordResponseMapping 记录 responseID 到 sessionID 的映射
func (sm *SessionManager) RecordResponseMapping(responseID, sessionID string) error {
	responseID = strings.TrimSpace(responseID)
	sessionID = strings.TrimSpace(sessionID)
	if responseID == "" || sessionID == "" {
		return fmt.Errorf("response ID 和 session ID 不能为空")
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.responseMapping[responseID] = sessionID
	if sm.store != nil {
		if err := sm.store.upsertMapping(responseID, sessionID); err != nil {
			sm.forgetResponseMappingLocked(responseID)
			return err
		}
	}
	log.Printf("[Session-Mapping] 记录映射: %s -> %s", responseID, sessionID)
	return nil
}

// GetSessionByResponseID 根据 responseID 获取会话
func (sm *SessionManager) GetSessionByResponseID(responseID string) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	responseID = strings.TrimSpace(responseID)

	if responseID == "" {
		return nil, fmt.Errorf("response_id 不能为空")
	}

	sessionID, ok := sm.responseMapping[responseID]
	if !ok {
		if sm.store != nil {
			session, found, err := sm.store.loadByResponseID(responseID, sm.maxAge)
			if err != nil {
				return nil, err
			}
			if found {
				sm.cacheLoadedSessionLocked(session, responseID)
				session.LastAccessAt = time.Now()
				if err := sm.store.touchSession(session, false); err != nil {
					return nil, err
				}
				return cloneSession(session), nil
			}
		}
		return nil, fmt.Errorf("无效的 previous_response_id: %s", responseID)
	}

	session, exists := sm.sessions[sessionID]
	if !exists {
		return nil, fmt.Errorf("无效的 previous_response_id: %s", responseID)
	}

	session.LastAccessAt = time.Now()
	if sm.store != nil {
		if err := sm.store.touchSession(session, false); err != nil {
			return nil, err
		}
	}
	return cloneSession(session), nil
}

// cloneSession 返回只读快照。会话转换通常在 manager 锁外读取 Messages；
// 如果直接暴露内存中的指针，另一个请求 CommitTurn 替换 slice 时就会形成
// 竞态，并且转换器可能看到半轮历史。
func cloneSession(session *Session) *Session {
	if session == nil {
		return nil
	}
	cloned := *session
	cloned.Messages = cloneResponsesItems(session.Messages)
	return &cloned
}

func cloneResponsesItems(items []types.ResponsesItem) []types.ResponsesItem {
	if items == nil {
		return nil
	}
	cloned := make([]types.ResponsesItem, len(items))
	for index, item := range items {
		cloned[index] = item
		cloned[index].Content = cloneJSONValue(item.Content)
		cloned[index].Summary = cloneJSONValue(item.Summary)
		cloned[index].Tools = cloneJSONValue(item.Tools)
		cloned[index].EncryptedContent = cloneJSONValue(item.EncryptedContent)
		if item.ToolUse != nil {
			toolUse := *item.ToolUse
			toolUse.Input = cloneJSONValue(item.ToolUse.Input)
			cloned[index].ToolUse = &toolUse
		}
	}
	return cloned
}

func cloneJSONValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned interface{}
	if err := json.Unmarshal(data, &cloned); err != nil {
		return value
	}
	return cloned
}

// AppendMessage 追加消息到会话
func (sm *SessionManager) AppendMessage(sessionID string, item types.ResponsesItem, tokensUsed int) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, exists := sm.sessions[sessionID]
	if !exists {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}

	session.Messages = append(session.Messages, item)
	session.TotalTokens += tokensUsed
	session.LastAccessAt = time.Now()
	if utils.ResponsesItemHasVisionContent(item) {
		session.HasVisionContent = true
	}
	if sm.store == nil {
		return nil
	}
	return sm.persistSessionLocked(session)
}

// CommitTurn 一次性持久化一轮请求，避免输入、输出、response ID 分别把完整
// messages_json 重写多次。它还确保会话正文和 response 映射在同一事务中提交。
func (sm *SessionManager) CommitTurn(sessionID string, items []types.ResponsesItem, tokensUsed int, hasVision bool, responseID string, owner ...string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	session, exists := sm.sessions[sessionID]
	if !exists {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}
	previousMessages := session.Messages
	previousTokens := session.TotalTokens
	previousResponseID := session.LastResponseID
	previousLastAccess := session.LastAccessAt
	previousVision := session.HasVisionContent
	compactionRoot := utils.ResponsesItemsContainCompaction(items)
	if compactionRoot {
		items = utils.ResponsesItemsFromCompactionRoot(items)
	}
	if len(items) > 0 {
		if compactionRoot {
			// compaction item 是新的历史根。不能再把它追加到旧 session，
			// 否则压缩后的下一轮转换仍会携带完整旧 transcript。
			session.Messages = append([]types.ResponsesItem(nil), items...)
			// tokensUsed 通常是包含压缩前完整 input 的本轮 usage，不能
			// 作为新根的累计历史 token，否则清理阈值会把已压缩会话立即
			// 当成旧长会话；后续轮次再重新累计。
			session.TotalTokens = 0
			session.HasVisionContent = false
			for _, item := range session.Messages {
				if utils.ResponsesItemHasVisionContent(item) {
					session.HasVisionContent = true
					break
				}
			}
		} else {
			session.Messages = utils.MergeResponsesItemsDedup(session.Messages, items)
			for _, item := range items {
				if utils.ResponsesItemHasVisionContent(item) {
					session.HasVisionContent = true
				}
			}
		}
	}
	if !compactionRoot {
		session.TotalTokens += tokensUsed
	}
	if compactionRoot {
		session.LastResponseID = responseID
	} else if responseID != "" {
		session.LastResponseID = responseID
	}
	session.LastAccessAt = time.Now()
	if hasVision && !compactionRoot {
		session.HasVisionContent = true
	}
	if sm.store == nil {
		if compactionRoot {
			for mappedResponseID, mappedSessionID := range sm.responseMapping {
				if mappedSessionID == session.ID && mappedResponseID != responseID {
					sm.forgetResponseMappingLocked(mappedResponseID)
				}
			}
		}
		if responseID != "" {
			sm.responseMapping[responseID] = sessionID
		}
		sm.rememberResponseOwnerLocked(responseID, owner)
		return nil
	}
	var persistErr error
	if compactionRoot {
		persistErr = sm.store.replaceSessionAndMapping(session, previousResponseID, responseID, owner...)
	} else {
		persistErr = sm.store.upsertSessionAndMapping(session, responseID, owner...)
	}
	if persistErr != nil {
		session.Messages = previousMessages
		session.TotalTokens = previousTokens
		session.LastResponseID = previousResponseID
		session.LastAccessAt = previousLastAccess
		session.HasVisionContent = previousVision
		return persistErr
	}
	if compactionRoot {
		for mappedResponseID, mappedSessionID := range sm.responseMapping {
			if mappedSessionID == session.ID && mappedResponseID != responseID {
				sm.forgetResponseMappingLocked(mappedResponseID)
			}
		}
	}
	if responseID != "" {
		sm.responseMapping[responseID] = sessionID
	}
	sm.rememberResponseOwnerLocked(responseID, owner)
	return nil
}

// ReplaceSessionAfterBoundary 在无法确认客户端 input 是旧会话后缀时，
// 把当前客户端历史视为新的本地边界。它与 compaction 使用同一套原子映射
// 清理，避免“上游已去掉 previous_response_id，但本地仍把完整历史追加到
// 旧 session”造成下一轮代理再次发送重复上下文。
func (sm *SessionManager) ReplaceSessionAfterBoundary(sessionID string, items []types.ResponsesItem, hasVision bool, responseID string, owner ...string) error {
	if sm == nil {
		return nil
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	session, exists := sm.sessions[sessionID]
	if !exists {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}
	previousMessages := session.Messages
	previousTokens := session.TotalTokens
	previousResponseID := session.LastResponseID
	previousLastAccess := session.LastAccessAt
	previousVision := session.HasVisionContent
	session.Messages = append([]types.ResponsesItem(nil), items...)
	session.TotalTokens = 0
	session.LastResponseID = responseID
	session.LastAccessAt = time.Now()
	session.HasVisionContent = hasVision
	if !hasVision {
		for _, item := range session.Messages {
			if utils.ResponsesItemHasVisionContent(item) {
				session.HasVisionContent = true
				break
			}
		}
	}
	if sm.store != nil {
		if err := sm.store.replaceSessionAndMapping(session, previousResponseID, responseID, owner...); err != nil {
			session.Messages = previousMessages
			session.TotalTokens = previousTokens
			session.LastResponseID = previousResponseID
			session.LastAccessAt = previousLastAccess
			session.HasVisionContent = previousVision
			return err
		}
	}
	for mappedResponseID, mappedSessionID := range sm.responseMapping {
		if mappedSessionID == session.ID && mappedResponseID != responseID {
			sm.forgetResponseMappingLocked(mappedResponseID)
		}
	}
	if responseID != "" {
		sm.responseMapping[responseID] = session.ID
	}
	sm.rememberResponseOwnerLocked(responseID, owner)
	return nil
}

// ReplaceSessionAfterCompact 用 compact 产生的摘要/控制 item 替换旧历史。
// compact 请求携带的 input 往往是完整 transcript，不能通过 CommitTurn 追加；
// 否则上游虽然压缩成功，代理在下一次协议转换时仍会发送压缩前的全部消息。
// 找不到旧 response_id 时返回错误：compact 成功后必须建立新的本地历史边界，
// 否则下一轮可能以新 response_id 创建空 session，静默丢失压缩后的上下文。
func (sm *SessionManager) ReplaceSessionAfterCompact(previousResponseID, responseID string, items []types.ResponsesItem, owner ...string) error {
	if sm == nil {
		return nil
	}
	previousResponseID = strings.TrimSpace(previousResponseID)
	responseID = strings.TrimSpace(responseID)
	if previousResponseID == "" || responseID == "" {
		return nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	var current *Session
	if sessionID, ok := sm.responseMapping[previousResponseID]; ok {
		current = sm.sessions[sessionID]
	}
	if current == nil && sm.store != nil {
		loaded, found, err := sm.store.loadByResponseID(previousResponseID, sm.maxAge)
		if err != nil {
			return err
		}
		if found {
			sm.cacheLoadedSessionLocked(loaded, previousResponseID)
			current = loaded
		}
	}
	if current == nil {
		return fmt.Errorf("未找到 compact 前的本地会话: %s", previousResponseID)
	}

	previousMessages := current.Messages
	previousTokens := current.TotalTokens
	previousResponse := current.LastResponseID
	previousLastAccess := current.LastAccessAt
	previousVision := current.HasVisionContent
	previousMappings := make(map[string]string)
	for mappedResponseID, mappedSessionID := range sm.responseMapping {
		if mappedSessionID == current.ID {
			previousMappings[mappedResponseID] = mappedSessionID
		}
	}

	current.Messages = append([]types.ResponsesItem(nil), items...)
	current.TotalTokens = 0
	current.LastResponseID = responseID
	current.LastAccessAt = time.Now()
	current.HasVisionContent = false
	for _, item := range current.Messages {
		if utils.ResponsesItemHasVisionContent(item) {
			current.HasVisionContent = true
			break
		}
	}

	if sm.store != nil {
		if err := sm.store.replaceSessionAndMapping(current, previousResponseID, responseID, owner...); err != nil {
			current.Messages = previousMessages
			current.TotalTokens = previousTokens
			current.LastResponseID = previousResponse
			current.LastAccessAt = previousLastAccess
			current.HasVisionContent = previousVision
			for mappedResponseID, mappedSessionID := range sm.responseMapping {
				if mappedSessionID == current.ID {
					sm.forgetResponseMappingLocked(mappedResponseID)
				}
			}
			for mappedResponseID, mappedSessionID := range previousMappings {
				sm.responseMapping[mappedResponseID] = mappedSessionID
			}
			return err
		}
	}
	for mappedResponseID, mappedSessionID := range sm.responseMapping {
		if mappedSessionID == current.ID && mappedResponseID != responseID {
			sm.forgetResponseMappingLocked(mappedResponseID)
		}
	}
	sm.responseMapping[responseID] = current.ID
	sm.rememberResponseOwnerLocked(responseID, owner)
	log.Printf("[Session-Compact] 会话 %s 已由 %s 压缩为 %s（保留 %d 个 item）",
		current.ID, previousResponseID, responseID, len(current.Messages))
	return nil
}

// ReplaceConversationSessionAfterCompact 在 compact 请求没有携带
// previous_response_id 时，按已经绑定的代理 conversation 找到当前 session
// 并建立新的压缩边界。不能因为缺少外部 response ID 就跳过本地状态替换。
func (sm *SessionManager) ReplaceConversationSessionAfterCompact(conversationID, responseID string, items []types.ResponsesItem, owner ...string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return fmt.Errorf("compact 缺少可用于恢复会话的 conversation ID")
	}
	current, err := sm.GetOrCreateSessionForConversation("", conversationID)
	if err != nil {
		return err
	}
	return sm.ReplaceSessionAfterBoundary(current.ID, items, false, responseID, owner...)
}

// MarkSessionHasVisionContent 显式标记会话历史含图
func (sm *SessionManager) MarkSessionHasVisionContent(sessionID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, exists := sm.sessions[sessionID]
	if !exists {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}

	session.HasVisionContent = true
	session.LastAccessAt = time.Now()
	if sm.store == nil {
		return nil
	}
	return sm.store.markVisionContent(session)
}

// UpdateLastResponseID 更新会话的最后一个 responseID
func (sm *SessionManager) UpdateLastResponseID(sessionID, responseID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, exists := sm.sessions[sessionID]
	if !exists {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}

	session.LastResponseID = responseID
	session.LastAccessAt = time.Now()
	if sm.store == nil {
		return nil
	}
	return sm.store.updateLastResponseID(session)
}

// GetSession 获取会话（只读）
func (sm *SessionManager) GetSession(sessionID string) (*Session, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, exists := sm.sessions[sessionID]
	if !exists {
		return nil, fmt.Errorf("会话不存在: %s", sessionID)
	}

	return cloneSession(session), nil
}

// DeleteConversation 删除一个持久化对话下的全部 Responses 会话、消息和 response ID 映射。
func (sm *SessionManager) DeleteConversation(conversationID string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return fmt.Errorf("conversation_id 不能为空")
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sessionIDs := make(map[string]struct{})
	for sessionID, current := range sm.sessions {
		if current != nil && current.ConversationID == conversationID {
			sessionIDs[sessionID] = struct{}{}
		}
	}
	if sm.store != nil {
		if err := sm.store.deleteConversationSessions(conversationID); err != nil {
			return fmt.Errorf("删除 Responses 持久化会话失败: %w", err)
		}
	}
	for sessionID := range sessionIDs {
		delete(sm.sessions, sessionID)
	}
	for responseID, sessionID := range sm.responseMapping {
		if _, ok := sessionIDs[sessionID]; ok {
			sm.forgetResponseMappingLocked(responseID)
		}
	}
	if len(sessionIDs) > 0 {
		log.Printf("[Session-Delete] 已删除对话 %s 的 %d 条 Responses 会话链", conversationID, len(sessionIDs))
	}
	return nil
}

// DeleteAll 删除全部 Responses 会话及 response ID 映射。
func (sm *SessionManager) DeleteAll() error {
	if sm == nil {
		return nil
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.store != nil {
		if err := sm.store.deleteAllSessions(); err != nil {
			return fmt.Errorf("删除全部 Responses 持久化会话失败: %w", err)
		}
	}
	sm.sessions = make(map[string]*Session)
	sm.responseMapping = make(map[string]string)
	sm.responseOwners = make(map[string]string)
	return nil
}

// cleanupLoop 定期清理过期会话
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

// cleanup 执行清理逻辑
func (sm *SessionManager) cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	removedSessions := 0
	removedMappings := 0

	// 清理过期会话
	for sessionID, session := range sm.sessions {
		shouldRemove := false

		// 时间过期
		if now.Sub(session.LastAccessAt) > sm.maxAge {
			shouldRemove = true
			log.Printf("[Session-Cleanup] 清理过期会话 (时间): %s (最后访问: %v 前)", sessionID, now.Sub(session.LastAccessAt))
		}

		// 可选的资源上限；主程序传 0，表示只按七天保留期清理。
		if sm.maxMessages > 0 && len(session.Messages) > sm.maxMessages {
			shouldRemove = true
			log.Printf("[Session-Cleanup] 清理过期会话 (消息数): %s (%d 条)", sessionID, len(session.Messages))
		}

		// Token 超限
		if sm.maxTokens > 0 && session.TotalTokens > sm.maxTokens {
			shouldRemove = true
			log.Printf("[Session-Cleanup] 清理过期会话 (Token): %s (%d tokens)", sessionID, session.TotalTokens)
		}

		if shouldRemove {
			delete(sm.sessions, sessionID)
			if sm.store != nil {
				if err := sm.store.deleteSession(sessionID); err != nil {
					log.Printf("[Session-Cleanup] 删除持久化会话失败: %v", err)
				}
			}
			removedSessions++
		}
	}

	// 清理孤立的 responseID 映射
	for responseID, sessionID := range sm.responseMapping {
		if _, exists := sm.sessions[sessionID]; !exists {
			sm.forgetResponseMappingLocked(responseID)
			removedMappings++
		}
	}

	if removedSessions > 0 || removedMappings > 0 {
		log.Printf("[Session-Cleanup] 清理完成: 删除 %d 个会话, %d 个映射", removedSessions, removedMappings)
		log.Printf("[Session-Stats] 当前活跃会话: %d 个, 映射: %d 个", len(sm.sessions), len(sm.responseMapping))
	}
}

func (sm *SessionManager) persistSessionLocked(session *Session) error {
	if sm.store == nil || session == nil {
		return nil
	}
	return sm.store.upsertSession(session)
}

// GetStats 获取统计信息
func (sm *SessionManager) GetStats() map[string]interface{} {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return map[string]interface{}{
		"total_sessions": len(sm.sessions),
		"total_mappings": len(sm.responseMapping),
	}
}

// generateID 生成唯一ID
func generateID(prefix string) string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// 降级方案：使用时间戳
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(bytes))
}

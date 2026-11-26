package session

// 预算只约束可从 SQLite 恢复的缓存。活动请求持有的快照和租约另计，不能为满足预算删历史。
func (sm *SessionManager) pruneCacheLocked(keep string) {
	if sm.store == nil {
		return
	}
	for (sm.maxLoadedSessions > 0 && len(sm.sessions) > sm.maxLoadedSessions) || (sm.maxLoadedBytes > 0 && sm.loadedBytes > sm.maxLoadedBytes) {
		var oldest *Session
		for id, candidate := range sm.sessions {
			if id != keep && sm.active[id] == 0 && (oldest == nil || candidate.LastAccessAt.Before(oldest.LastAccessAt)) {
				oldest = candidate
			}
		}
		if oldest == nil {
			return
		}
		sm.removeCachedSessionLocked(oldest.ID)
	}
}

// 按正文、扩展字段和容器开销记账，不声称它等于进程实际堆占用。
func sessionCacheBytes(current *Session) int {
	size := 192 + len(current.ID) + len(current.ConversationID) + len(current.LastResponseID)
	for _, item := range current.Messages {
		size += 384 + len(item.ID) + len(item.Type) + len(item.Status) + len(item.Role) + len(item.CallID) + len(item.Name) + len(item.Arguments) + len(item.Namespace) + len(item.Execution) + len(item.Signature)
		for _, value := range []any{item.Content, item.Summary, item.Tools, item.EncryptedContent, item.Action} {
			size += historyValueBytes(value)
		}
		for key, value := range item.Extra {
			size += len(key) + len(value) + 64
		}
		if item.ToolUse != nil {
			size += 64 + len(item.ToolUse.ID) + len(item.ToolUse.Name) + historyValueBytes(item.ToolUse.Input)
		}
	}
	return size
}

func historyValueBytes(value any) int {
	switch value := value.(type) {
	case string:
		return len(value)
	case []any:
		size := 24 + 16*len(value)
		for _, item := range value {
			size += historyValueBytes(item)
		}
		return size
	case map[string]any:
		size := 64
		for key, item := range value {
			size += 64 + len(key) + historyValueBytes(item)
		}
		return size
	default:
		return 16
	}
}

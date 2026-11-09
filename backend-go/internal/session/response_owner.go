package session

import (
	"database/sql"
	"fmt"
)

// ResponseOwner 按 response ID 查询生成它的凭据组合；旧记录无归属时不得复用链。
func (sm *SessionManager) ResponseOwner(responseID string) (string, bool, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.store == nil {
		if _, exists := sm.responseMapping[responseID]; !exists {
			return "", false, nil
		}
		owner := sm.responseOwners[responseID]
		return owner, owner != "", nil
	}
	var owner string
	err := sm.store.db.QueryRow(`SELECT upstream_identity FROM response_session_mappings WHERE response_id = ?`, responseID).Scan(&owner)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("读取 Responses 凭据归属失败: %w", err)
	}
	return owner, owner != "", nil
}

func (sm *SessionManager) rememberResponseOwnerLocked(responseID string, owner []string) {
	if sm.store == nil && responseID != "" && len(owner) > 0 {
		sm.responseOwners[responseID] = owner[0]
	}
}

func (sm *SessionManager) forgetResponseMappingLocked(responseID string) {
	delete(sm.responseMapping, responseID)
	delete(sm.responseOwners, responseID)
}

func responseOwnerValue(owner []string) string {
	if len(owner) > 0 {
		return owner[0]
	}
	return ""
}

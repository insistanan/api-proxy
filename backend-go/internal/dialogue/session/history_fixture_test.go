package session

import (
	"context"
	"fmt"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func commitFixtureTurn(manager *SessionManager, snapshot *Session, items []types.ResponsesItem, tokens int, vision bool, responseID string, owners ...string) error {
	turn, err := manager.BeginTurn(context.Background(), "", snapshot.ConversationID)
	if err != nil {
		return err
	}
	defer turn.Close()
	if turn.Snapshot.ID != snapshot.ID {
		return fmt.Errorf("测试历史 ID 不一致")
	}
	owner := ""
	if len(owners) > 0 {
		owner = owners[0]
	}
	return turn.Commit(context.Background(), items, tokens, vision, responseID, owner, false)
}

func (sm *SessionManager) GetSession(id string) (*Session, error) {
	sm.mu.RLock()
	current := sm.sessions[id]
	sm.mu.RUnlock()
	if current == nil && sm.store != nil {
		var err error
		current, err = sm.store.load(context.Background(), `WHERE rs.id=?`, id)
		if err != nil {
			return nil, err
		}
	}
	if current == nil {
		return nil, fmt.Errorf("会话不存在: %s", id)
	}
	return cloneSession(current)
}

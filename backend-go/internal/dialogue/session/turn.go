package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"insistanan/api-proxy/internal/dialogue/conversation"
	"insistanan/api-proxy/internal/protocol/types"
)

// Turn 在上游调用前固定历史版本。租约保护在途历史，Close 由请求入口负责。
type Turn struct {
	Snapshot *Session
	manager  *SessionManager
	once     sync.Once
}

func (sm *SessionManager) BindRegistry(registry *conversation.Registry) error {
	if registry != nil {
		if err := registry.BindHistory(sm); err != nil {
			return err
		}
	}
	sm.mu.Lock()
	sm.registry = registry
	sm.mu.Unlock()
	return nil
}

func (sm *SessionManager) withConversation(ctx context.Context, id string, operation func() error) error {
	sm.mu.RLock()
	registry := sm.registry
	sm.mu.RUnlock()
	if registry == nil || id == "" {
		return operation()
	}
	var db *sql.DB
	if sm.store != nil {
		db = sm.store.db
	}
	return registry.WithConversation(ctx, id, db, operation)
}

func (sm *SessionManager) BeginTurn(ctx context.Context, previous, conversationID string) (*Turn, error) {
	current, err := sm.snapshot(ctx, previous, conversationID, true)
	if err != nil {
		return nil, err
	}
	return &Turn{Snapshot: current, manager: sm}, nil
}

func (t *Turn) Close() {
	if t != nil {
		t.once.Do(func() { t.manager.release(t.Snapshot.ID) })
	}
}
func (sm *SessionManager) release(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.active[id]--
	if sm.active[id] <= 0 {
		delete(sm.active, id)
	}
}

func (t *Turn) Commit(ctx context.Context, items []types.ResponsesItem, tokens int, vision bool, responseID, owner string, replace bool) error {
	if t == nil {
		return fmt.Errorf("请求缺少历史快照")
	}
	return t.manager.commitSnapshot(ctx, t.Snapshot, items, tokens, vision, responseID, owner, replace)
}

func (sm *SessionManager) DeleteWithRecords(ctx context.Context, id string, db *sql.DB, remove func(*sql.Tx) error) error {
	return sm.deleteWith(ctx, id, db, remove)
}

func (sm *SessionManager) deleteWith(ctx context.Context, id string, db *sql.DB, remove func(*sql.Tx) error) error {
	if err := sm.mu.LockContext(ctx); err != nil {
		return err
	}
	defer sm.mu.Unlock()
	var ids []string
	if sm.store != nil {
		if db != nil && db != sm.store.db {
			return fmt.Errorf("对话和历史必须在同一数据库事务内删除")
		}
		tx, err := sm.store.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		query := `SELECT id FROM response_sessions`
		args := []any{}
		if id != "" {
			query += ` WHERE conversation_id=?`
			args = append(args, id)
		}
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sessionID string
			if err = rows.Scan(&sessionID); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, sessionID)
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if err = deleteSessionIDs(ctx, tx, ids); err != nil {
			return err
		}
		if remove != nil {
			if err = remove(tx); err != nil {
				return err
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	} else {
		if db != nil {
			return fmt.Errorf("会话历史与注册表的存储模式不一致")
		}
		if remove != nil {
			if err := remove(nil); err != nil {
				return err
			}
		}
	}
	for sessionID, current := range sm.sessions {
		if id == "" || current.ConversationID == id {
			ids = append(ids, sessionID)
		}
	}
	for _, sessionID := range ids {
		sm.removeCachedSessionLocked(sessionID)
	}
	return nil
}

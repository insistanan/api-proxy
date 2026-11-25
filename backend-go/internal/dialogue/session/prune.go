package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PruneWithRecords 由 Registry 在持有对话锁时调用，始终保持对话锁→历史锁顺序。
func (sm *SessionManager) PruneWithRecords(ctx context.Context, db *sql.DB, remove func(*sql.Tx) ([]string, error)) ([]string, error) {
	if (sm.store == nil) != (db == nil) || sm.store != nil && sm.store.db != db {
		return nil, fmt.Errorf("会话历史和注册表必须共享同一持久化数据库或同时使用内存")
	}
	if err := sm.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer sm.mu.Unlock()
	if db == nil {
		records, err := remove(nil)
		if err != nil {
			return nil, err
		}
		expired := make(map[string]bool, len(records))
		for _, id := range records {
			expired[id] = true
		}
		for id, current := range sm.sessions {
			if expired[current.ConversationID] {
				sm.removeCachedSessionLocked(id)
			}
		}
		return records, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	records, err := remove(tx)
	if err != nil {
		return nil, err
	}
	// 同时清理启动阶段 Registry 已过期、但尚未绑定生命周期协调者的冷历史。
	rows, err := tx.QueryContext(ctx, `SELECT id FROM response_sessions WHERE conversation_id<>'' AND NOT EXISTS (SELECT 1 FROM conversations WHERE conversations.id=response_sessions.conversation_id)`)
	if err != nil {
		return nil, err
	}
	sessions := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		sessions = append(sessions, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if err = deleteSessionIDs(ctx, tx, sessions); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	for _, id := range sessions {
		sm.removeCachedSessionLocked(id)
	}
	return records, nil
}

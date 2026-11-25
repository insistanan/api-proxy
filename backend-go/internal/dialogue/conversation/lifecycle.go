package conversation

import (
	"context"
	"database/sql"
	"fmt"
)

// ResponseAliasKey 与显式身份解析共用编码，不能在其他模块自行拼接。
func ResponseAliasKey(responseID string) string { return explicitIdentityKey("responses", responseID) }

// WithConversation 把存在性检查与历史写入纳入同一删除边界。
// 请求开始后记录被删除时，晚到回程不能重新创建历史或协议缓存。
func (r *Registry) WithConversation(ctx context.Context, id string, db *sql.DB, operation func() error) error {
	if err := r.mu.LockContext(ctx); err != nil {
		return err
	}
	defer r.mu.Unlock()
	if r.stopped {
		return fmt.Errorf("对话注册表已停止")
	}
	if r.store != nil {
		if r.store.db != db {
			return fmt.Errorf("会话历史与对话注册表必须使用同一数据库连接")
		}
		var exists bool
		if err := r.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conversations WHERE id=?)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("对话已删除或过期")
		}
	} else if db != nil {
		return fmt.Errorf("会话历史与对话注册表的存储模式不一致")
	} else if r.records[id] == nil {
		return fmt.Errorf("对话已删除或过期")
	}
	if err := operation(); err != nil {
		return err
	}
	// alias 已在历史事务内更新，下一次身份查找重新从持久层读取。
	if r.store != nil {
		for key, recordID := range r.identityIndex {
			if recordID == id && (r.records[id] == nil || key != r.records[id].identityKey) {
				delete(r.identityIndex, key)
			}
		}
	}
	return nil
}

// RegisterCacheInvalidator 由装配层登记，可重建缓存统一跟随删除、TTL 和停机释放。
func (r *Registry) RegisterCacheInvalidator(name string, invalidate func(string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.invalidators[name] = invalidate
}

func (r *Registry) invalidateCachesLocked(id string) {
	for _, invalidate := range r.invalidators {
		invalidate(id)
	}
}

// HistoryDeletion 由会话历史模块实现，在同一事务内删除关联记录，成功后清除缓存。
type HistoryDeletion interface {
	DeleteWithRecords(context.Context, string, *sql.DB, func(*sql.Tx) error) error
}

func (r *Registry) DeleteWithHistory(ctx context.Context, id string, history HistoryDeletion) error {
	if err := r.mu.LockContext(ctx); err != nil {
		return err
	}
	defer r.mu.Unlock()
	if r.stopped {
		return fmt.Errorf("对话注册表已停止")
	}
	if history == nil {
		return fmt.Errorf("会话历史管理器未初始化")
	}
	var db *sql.DB
	if r.store != nil {
		db = r.store.db
	}
	err := history.DeleteWithRecords(ctx, id, db, func(tx *sql.Tx) error {
		if tx == nil {
			return nil
		}
		for _, table := range []string{"conversation_lineages", "conversation_image_understandings", "conversation_aliases"} {
			query := "DELETE FROM " + table
			args := []any{}
			if id != "" {
				query += " WHERE conversation_id=?"
				args = append(args, id)
			}
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				return err
			}
		}
		if id != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE conversation_lineages SET parent_conversation_id='' WHERE parent_conversation_id=?`, id); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM conversations WHERE id=?`, id)
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM conversations`)
		return err
	})
	if err != nil {
		return err
	}
	if id == "" {
		r.records = make(map[string]*Record)
		r.identityIndex = make(map[string]string)
		r.frontierIndex = make(map[string]map[string]struct{})
	} else {
		r.unindexLineageLocked(r.records[id])
		delete(r.records, id)
		r.removeIdentityKeysLocked(id)
		r.clearParentReferencesLocked(id)
	}
	r.invalidateCachesLocked(id)
	return nil
}

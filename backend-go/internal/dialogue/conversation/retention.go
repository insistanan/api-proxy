package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// HistoryPruner 在同一事务清理对话记录和失去归属的历史，提交后才释放历史缓存。
type HistoryPruner interface {
	PruneWithRecords(context.Context, *sql.DB, func(*sql.Tx) ([]string, error)) ([]string, error)
}

func (r *Registry) BindHistory(history HistoryPruner) error {
	r.mu.Lock()
	r.history = history
	r.mu.Unlock()
	return r.cleanup()
}

func (s *SQLiteStore) purgeExpired(ctx context.Context, cutoff time.Time, activeIDs []string, history HistoryPruner) ([]string, error) {
	operation := func(tx *sql.Tx) ([]string, error) { return s.deleteExpiredRecords(ctx, tx, cutoff, activeIDs) }
	if history != nil {
		return history.PruneWithRecords(ctx, s.db, operation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ids, err := operation(tx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *SQLiteStore) deleteExpiredRecords(ctx context.Context, tx *sql.Tx, cutoff time.Time, activeIDs []string) ([]string, error) {
	if activeIDs == nil {
		activeIDs = []string{}
	}
	active, err := json.Marshal(activeIDs)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM conversations WHERE last_seen_at < ? AND id NOT IN (SELECT value FROM json_each(?))`, cutoff.Unix(), string(active))
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	for _, table := range []string{"conversation_aliases", "conversation_image_understandings", "conversation_lineages"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE conversation_id IN (SELECT value FROM json_each(?))`, string(encoded)); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversation_lineages SET parent_conversation_id='' WHERE parent_conversation_id IN (SELECT value FROM json_each(?))`, string(encoded)); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM conversations WHERE id IN (SELECT value FROM json_each(?))`, string(encoded)); err != nil {
		return nil, err
	}
	return ids, nil
}

// cleanup 的删除清单来自同一个事务；失败不发布缓存失效或删除统计。
func (r *Registry) cleanup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.mu.LockContext(ctx); err != nil {
		return err
	}
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-r.maxIdle)
	active := []string{}
	expired := map[string]bool{}
	for id, rec := range r.records {
		if rec.ActiveRequests > 0 || rec.dirty {
			active = append(active, id)
		} else if rec.LastSeenAt.Before(cutoff) {
			expired[id] = true
		}
	}
	if r.store != nil {
		ids, err := r.store.purgeExpired(ctx, cutoff, active, r.history)
		if err != nil {
			return err
		}
		expired = make(map[string]bool, len(ids))
		for _, id := range ids {
			expired[id] = true
		}
	}
	for id := range expired {
		r.invalidateCachesLocked(id)
		r.unindexLineageLocked(r.records[id])
		delete(r.records, id)
		r.removeIdentityKeysLocked(id)
		r.clearParentReferencesLocked(id)
	}
	r.trimCacheLocked()
	return nil
}

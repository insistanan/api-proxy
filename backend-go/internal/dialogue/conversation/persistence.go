package conversation

import (
	"context"
	"log"
	"time"
)

func (r *Registry) cleanupLoop() {
	defer close(r.doneCh)
	ticker := time.NewTicker(defaultCleanupInterval)
	defer ticker.Stop()
	retry := time.NewTicker(5 * time.Second)
	defer retry.Stop()

	for {
		select {
		case <-ticker.C:
			if err := r.cleanup(); err != nil {
				log.Printf("[Conversation-Cleanup] 清理失败: %v", err)
			}
		case <-retry.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			r.mu.Lock()
			r.retryDirtyLocked(ctx)
			r.trimCacheLocked()
			r.mu.Unlock()
			cancel()
		case <-r.stopCh:
			return
		}
	}
}

func (r *Registry) persistLocked(ctx context.Context, rec *Record) error {
	if r.store == nil || rec == nil {
		return nil
	}
	if err := r.store.Upsert(ctx, rec); err != nil {
		rec.dirty = true
		rec.PersistenceError = err.Error()
		log.Printf("[Conversation] 保存对话失败: %v", err)
		return err
	}
	rec.dirty = false
	rec.PersistenceError = ""
	return nil
}

// 只重试观测数据的本地写入，不重新发起模型调用。控制操作失败会回滚候选副本。
func (r *Registry) retryDirtyLocked(ctx context.Context) error {
	for _, record := range r.records {
		if record.dirty {
			if err := r.persistLocked(ctx, record); err != nil {
				return err
			}
		}
	}
	return nil
}

package conversation

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// ActiveIDs 只查询索引所需的元数据；持久化错误必须与空结果区分。
func (r *Registry) ActiveIDs(ctx context.Context, since time.Time) ([]string, error) {
	if err := r.mu.RLockContext(ctx); err != nil {
		return nil, err
	}
	defer r.mu.RUnlock()
	ids := make(map[string]struct{})
	if r.store != nil {
		rows, err := r.store.db.QueryContext(ctx, `SELECT id FROM conversations WHERE last_request_at >= ?`, since.Unix())
		if err != nil {
			return nil, fmt.Errorf("查询活跃会话失败: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	for id, rec := range r.records {
		if rec.ActiveRequests > 0 || !rec.LastRequestAt.Before(since) {
			ids[id] = struct{}{}
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

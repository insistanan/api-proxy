package logger

import (
	"context"
	"database/sql"
	"fmt"
)

type ContextTrendPoint struct {
	FirstAt    int64            `json:"firstAt"`
	LastAt     int64            `json:"lastAt"`
	RequestID  string           `json:"requestId,omitempty"`
	Calls      int64            `json:"calls"`
	KnownCalls int64            `json:"knownCalls"`
	Peak       *ContextSnapshot `json:"peak,omitempty"`
}

type ContextOverview struct {
	Calls      int64               `json:"calls"`
	KnownCalls int64               `json:"knownCalls"`
	BucketSize int64               `json:"bucketSize"`
	Points     []ContextTrendPoint `json:"points"`
}

// GetContextOverview 对完整会话的摘要流做有界聚合，不读取历史正文。
// 长会话每组保留上下文估算峰值，缺失和尾部摘要不参与峰值比较。
func (s *Store) GetContextOverview(ctx context.Context, key string) (ContextOverview, error) {
	result := ContextOverview{Points: []ContextTrendPoint{}}
	if s == nil || !s.requestIndex {
		return result, fmt.Errorf("日志会话索引不可用")
	}
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, fmt.Errorf("开始上下文总览查询失败: %w", err)
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM log_request_index WHERE group_key = ?`, key).Scan(&result.Calls); err != nil {
		return result, err
	}
	if result.Calls == 0 {
		return result, ErrLogGroupNotFound
	}
	result.BucketSize = max(1, (result.Calls+199)/200)
	rows, err := tx.QueryContext(ctx, `SELECT summary_json, boot_id FROM log_request_index WHERE group_key = ? ORDER BY started_at, request_id`, key)
	if err != nil {
		return result, fmt.Errorf("查询上下文趋势失败: %w", err)
	}
	for rows.Next() {
		var body, bootID string
		if err := rows.Scan(&body, &bootID); err != nil {
			rows.Close()
			return result, err
		}
		summary, err := s.decodeIndexedSummary(body, bootID)
		if err != nil {
			rows.Close()
			return result, err
		}
		if len(result.Points) == 0 || result.Points[len(result.Points)-1].Calls >= result.BucketSize {
			result.Points = append(result.Points, ContextTrendPoint{FirstAt: summary.StartedAt})
		}
		point := &result.Points[len(result.Points)-1]
		point.LastAt = summary.StartedAt
		point.Calls++
		if snapshot := summary.Context; snapshot != nil && snapshot.Completeness == "captured" && snapshot.PreviousResponseID == "" {
			point.KnownCalls++
			result.KnownCalls++
			if point.Peak == nil || snapshot.EstimatedTokens >= point.Peak.EstimatedTokens {
				point.Peak = snapshot
				point.RequestID = summary.RequestID
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

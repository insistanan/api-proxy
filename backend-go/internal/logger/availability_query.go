package logger

import (
	"context"
	"fmt"
	"time"

	"github.com/insistanan/api-proxy/internal/availability"
)

// QueryAvailability 在数据库聚合最近 24 个小时桶，不读取请求正文或截断最近 N 次请求。
func (s *Store) QueryAvailability(ctx context.Context, kind string, now time.Time) ([]availability.Aggregate, error) {
	if s == nil || s.readDB == nil {
		return nil, fmt.Errorf("可用性日志存储未初始化")
	}
	from := now.UnixMilli()/availability.BucketMillis*availability.BucketMillis - 23*availability.BucketMillis
	rows, err := s.readDB.QueryContext(ctx, `WITH samples AS (
		SELECT *, (unix_milli / ?) * ? AS bucket,
			ROW_NUMBER() OVER (PARTITION BY channel_id, model, candidate_id, outcome, reason, unix_milli / ?
				ORDER BY unix_milli DESC, request_log_id DESC) AS position
		FROM availability_samples WHERE kind = ? AND unix_milli >= ? AND unix_milli <= ?
	)
	SELECT channel_id, model, candidate_id, outcome, reason, bucket,
		COUNT(*), SUM(CASE WHEN unix_milli >= ? THEN 1 ELSE 0 END), MAX(unix_milli),
		MAX(CASE WHEN position = 1 THEN request_log_id ELSE 0 END),
		MAX(CASE WHEN position = 1 THEN cooldown_until ELSE 0 END)
	FROM samples GROUP BY channel_id, model, candidate_id, outcome, reason, bucket`,
		availability.BucketMillis, availability.BucketMillis, availability.BucketMillis,
		kind, from, now.UnixMilli(), now.Add(-time.Hour).UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("查询可用性统计失败: %w", err)
	}
	defer rows.Close()
	result := make([]availability.Aggregate, 0)
	for rows.Next() {
		var a availability.Aggregate
		if err := rows.Scan(&a.ChannelID, &a.Model, &a.CandidateID, &a.Outcome, &a.Reason,
			&a.Bucket, &a.Count, &a.Recent, &a.LastSeen, &a.LastSequence, &a.CooldownUntil); err != nil {
			return nil, fmt.Errorf("读取可用性统计失败: %w", err)
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历可用性统计失败: %w", err)
	}
	return result, nil
}

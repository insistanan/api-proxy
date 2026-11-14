package metrics

import (
	"fmt"
	"time"
)

// 历史 API Key 注册表：换 Key 后仍需用旧 Key 聚合统计。
// 这是运行时数据，跟随指标库保存，不再写入配置文件。
// 渠道身份使用稳定 ID，避免渠道重排或索引变化后错绑。

// SaveHistoricalKeys 覆盖保存某渠道的历史 Key 集合。
func (s *SQLiteStore) SaveHistoricalKeys(apiType string, channelID string, keys []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开启历史 Key 事务失败: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`DELETE FROM channel_historical_keys WHERE api_type = ? AND channel_id = ?`,
		apiType, channelID,
	); err != nil {
		return fmt.Errorf("清理历史 Key 失败: %w", err)
	}

	now := time.Now().UnixMilli()
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO channel_historical_keys (api_type, channel_id, api_key, added_at) VALUES (?, ?, ?, ?)`,
			apiType, channelID, key, now,
		); err != nil {
			return fmt.Errorf("写入历史 Key 失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交历史 Key 失败: %w", err)
	}
	return nil
}

// LoadHistoricalKeys 按渠道 ID 读取某协议下的历史 Key。
func (s *SQLiteStore) LoadHistoricalKeys(apiType string) (map[string][]string, error) {
	result := make(map[string][]string)
	if s == nil || s.db == nil {
		return result, nil
	}
	rows, err := s.db.Query(
		`SELECT channel_id, api_key FROM channel_historical_keys WHERE api_type = ? ORDER BY added_at, rowid`,
		apiType,
	)
	if err != nil {
		return nil, fmt.Errorf("读取历史 Key 失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var channelID, apiKey string
		if err := rows.Scan(&channelID, &apiKey); err != nil {
			return nil, fmt.Errorf("解析历史 Key 失败: %w", err)
		}
		result[channelID] = append(result[channelID], apiKey)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历历史 Key 失败: %w", err)
	}
	return result, nil
}

// DeleteHistoricalKeys 删除某渠道的历史 Key。
func (s *SQLiteStore) DeleteHistoricalKeys(apiType string, channelID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := s.db.Exec(
		`DELETE FROM channel_historical_keys WHERE api_type = ? AND channel_id = ?`,
		apiType, channelID,
	); err != nil {
		return fmt.Errorf("删除历史 Key 失败: %w", err)
	}
	return nil
}

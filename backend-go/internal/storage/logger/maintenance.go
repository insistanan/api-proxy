package logger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

type BudgetEvent struct {
	Timestamp   string `json:"timestamp"`
	Paused      bool   `json:"paused"`
	UsedBytes   int64  `json:"usedBytes"`
	BudgetBytes int64  `json:"budgetBytes"`
}

type MaintenanceStatus struct {
	CleanupDeferred bool   `json:"cleanupDeferred"`
	LastCleanupAt   string `json:"lastCleanupAt,omitempty"`
	DeletedRows     int64  `json:"deletedRows"`
	ExpiredBodies   int64  `json:"expiredBodies"`
	CleanupError    string `json:"cleanupError,omitempty"`
	BudgetError     string `json:"budgetError,omitempty"`
}

type StorageStatus struct {
	Queue             QueueStatus       `json:"queue"`
	Policy            StoragePolicy     `json:"policy"`
	UsedBytes         int64             `json:"usedBytes"`
	FreeBytes         int64             `json:"freeBytes"`
	DatabaseBytes     int64             `json:"databaseBytes"`
	WALBytes          int64             `json:"walBytes"`
	BodyCapturePaused bool              `json:"bodyCapturePaused"`
	WriteError        string            `json:"writeError,omitempty"`
	Maintenance       MaintenanceStatus `json:"maintenance"`
	BudgetEvents      []BudgetEvent     `json:"budgetEvents"`
}

// QueueStatus 为本次启动累计状态，Pending 包含正在处理的写入批次。
type QueueStatus struct {
	PendingEntries     int    `json:"pendingEntries"`
	PendingBytes       int    `json:"pendingBytes"`
	CapacityEntries    int    `json:"capacityEntries"`
	CapacityBytes      int    `json:"capacityBytes"`
	AcceptedWrites     uint64 `json:"acceptedWrites"`
	RejectedWrites     uint64 `json:"rejectedWrites"`
	LastRejectedAt     string `json:"lastRejectedAt,omitempty"`
	LastRejectedError  string `json:"lastRejectedError,omitempty"`
	WriteFailures      uint64 `json:"writeFailures"`
	LastWriteFailureAt string `json:"lastWriteFailureAt,omitempty"`
	WriteRecoveries    uint64 `json:"writeRecoveries"`
	LastRecoveryAt     string `json:"lastRecoveryAt,omitempty"`
}

func databaseUsage(ctx context.Context, db *sql.DB) (used, free int64, err error) {
	var pages, unused, size int64
	err = db.QueryRowContext(ctx, `SELECT page_count, freelist_count, page_size FROM pragma_page_count(), pragma_freelist_count(), pragma_page_size()`).Scan(&pages, &unused, &size)
	if err != nil {
		return 0, 0, fmt.Errorf("读取日志数据库占用失败: %w", err)
	}
	return (pages - unused) * size, unused * size, nil
}

func logFileSize(path string, optional bool) (int64, error) {
	info, err := os.Stat(path)
	if optional && os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取日志文件大小失败: %w", err)
	}
	return info.Size(), nil
}

func (s *Store) GetStorageStatus(ctx context.Context) (StorageStatus, error) {
	var result StorageStatus
	if s == nil || s.readDB == nil {
		return result, fmt.Errorf("日志存储尚未初始化")
	}
	result.Policy = s.storagePolicy()
	var err error
	result.UsedBytes, result.FreeBytes, err = databaseUsage(ctx, s.readDB)
	if err != nil {
		return result, err
	}
	if s.dbPath != ":memory:" {
		result.DatabaseBytes, err = logFileSize(s.dbPath, false)
		if err != nil {
			return result, err
		}
		result.WALBytes, err = logFileSize(s.dbPath+"-wal", true)
		if err != nil {
			return result, err
		}
	}
	s.statusMu.Lock()
	result.BodyCapturePaused = s.budgetPaused
	result.Maintenance = s.maintenance
	result.BudgetEvents = append([]BudgetEvent{}, s.budgetEvents...)
	s.statusMu.Unlock()
	s.mu.Lock()
	var cleanupErrors []error
	for index, failure := range s.cleanupErrors {
		if failure != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("%w；下次尝试 %s", failure, s.cleanupSkipUntil[index].Format(time.RFC3339)))
		}
	}
	if s.bodyCleanupError != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("%w；下次尝试 %s", s.bodyCleanupError, s.bodyCleanupSkipUntil.Format(time.RFC3339)))
	}
	s.mu.Unlock()
	if failure := errors.Join(cleanupErrors...); failure != nil {
		if result.Maintenance.CleanupError != "" {
			result.Maintenance.CleanupError += "；"
		}
		result.Maintenance.CleanupError += failure.Error()
	}
	s.queueMu.Lock()
	result.Queue = s.queueStatus
	result.Queue.PendingBytes = s.queueBytes
	result.Queue.CapacityEntries = cap(s.queue)
	result.Queue.CapacityBytes = maxQueuedLogBytes
	if s.writeErr != nil {
		result.WriteError = s.writeErr.Error()
	}
	s.queueMu.Unlock()
	return result, nil
}

// refreshBudget 由清理循环每分钟检查。预算是软上限；已开始请求沿用入口快照。
// 降至预算的 90% 后恢复，避免在阈值附近反复切换。事件持久化到运行日志。
func (s *Store) refreshBudget(policy StoragePolicy) error {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	used, _, err := databaseUsage(ctx, s.readDB)
	s.statusMu.Lock()
	if err != nil {
		s.maintenance.BudgetError = err.Error()
		s.statusMu.Unlock()
		return err
	}
	s.maintenance.BudgetError = ""
	budget := int64(policy.BudgetMB) << 20
	paused := s.budgetPaused
	next := budgetShouldPause(used, budget, paused)
	s.budgetPaused = next
	if next == paused {
		s.statusMu.Unlock()
		return nil
	}
	event := BudgetEvent{Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Paused: next, UsedBytes: used, BudgetBytes: budget}
	s.budgetEvents = append(s.budgetEvents, event)
	if len(s.budgetEvents) > 20 {
		s.budgetEvents = s.budgetEvents[len(s.budgetEvents)-20:]
	}
	s.statusMu.Unlock()
	message := fmt.Sprintf("日志预算限制解除：占用 %d 字节，预算 %d 字节；新请求按采集配置记录正文", used, budget)
	if next {
		message = fmt.Sprintf("日志预算触发，正文采集降为元数据：占用 %d 字节，预算 %d 字节；在途请求沿用原快照", used, budget)
	}
	return s.RecordAppLog(AppLog{Tag: "Logger-Budget", Message: message})
}

func budgetShouldPause(used, budget int64, paused bool) bool {
	return budget > 0 && (used >= budget || (paused && used > budget*9/10))
}

// expireTrafficBodies 只移除正文与头部，保留生命周期、调用摘要及明确工具 ID 索引。
// 去重正文仍被新记录引用时继续保留；删除引用与设置 expired 必须同事务完成。
func (s *Store) expireTrafficBodies(cutoff int64) (bool, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().Before(s.bodyCleanupSkipUntil) {
		return false, 0, nil
	}
	batch := s.bodyCleanupBatch
	if batch == 0 {
		batch = cleanupShrinkBatch
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return s.bodyCleanupFailure(ctx, batch, err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT t.id, COALESCE(r.body_hash, '') FROM traffic_body_state bs
		JOIN traffic_logs t ON t.id = bs.traffic_id
		LEFT JOIN traffic_body_refs r ON r.traffic_id = t.id
		WHERE bs.state IN ('stored', 'truncated', 'summary') AND bs.unix_milli < ? ORDER BY bs.unix_milli, bs.traffic_id LIMIT ?`, cutoff, batch)
	if err != nil {
		return s.bodyCleanupFailure(ctx, batch, err)
	}
	type bodyRow struct {
		id   int64
		hash string
	}
	var bodies []bodyRow
	for rows.Next() {
		var body bodyRow
		if err = rows.Scan(&body.id, &body.hash); err != nil {
			break
		}
		bodies = append(bodies, body)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return s.bodyCleanupFailure(ctx, batch, err)
	}
	for _, body := range bodies {
		if _, err = tx.ExecContext(ctx, `UPDATE traffic_logs SET body = '', headers_json = '' WHERE id = ?`, body.id); err != nil {
			return s.bodyCleanupFailure(ctx, batch, err)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE traffic_body_state SET state = 'expired' WHERE traffic_id = ?`, body.id); err != nil {
			return s.bodyCleanupFailure(ctx, batch, err)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM traffic_body_refs WHERE traffic_id = ?`, body.id); err != nil {
			return s.bodyCleanupFailure(ctx, batch, err)
		}
		if body.hash != "" {
			if _, err = tx.ExecContext(ctx, `DELETE FROM traffic_bodies WHERE body_hash = ? AND NOT EXISTS (SELECT 1 FROM traffic_body_refs WHERE body_hash = ?)`, body.hash, body.hash); err != nil {
				return s.bodyCleanupFailure(ctx, batch, err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return s.bodyCleanupFailure(ctx, batch, err)
	}
	s.bodyCleanupSkipUntil = time.Time{}
	s.bodyCleanupError = nil
	return len(bodies) == batch, int64(len(bodies)), nil
}

func (s *Store) bodyCleanupFailure(ctx context.Context, batch int, err error) (bool, int64, error) {
	if isCleanupTimeout(ctx, err) && batch > 1 {
		s.bodyCleanupBatch = max(1, batch/2)
		return true, 0, nil
	}
	s.bodyCleanupSkipUntil = time.Now().Add(cleanupRetryInterval)
	s.bodyCleanupError = fmt.Errorf("清理过期正文失败（批量 %d，%s 内不再重试）: %w", batch, cleanupRetryInterval, err)
	return false, 0, s.bodyCleanupError
}

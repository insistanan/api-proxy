// 日志清理：手动清理和自动分批清理，共用 Store 的写锁与连接。
package logger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PurgeOptions 描述一次手动日志清理：全部清空，或按本地日历日清理某一天
// （Date 为 YYYY-MM-DD）。二者必须且只能设置其一。
type PurgeOptions struct {
	All  bool
	Date string
}

// PurgeResult 是手动清理的删除行数统计。
type PurgeResult struct {
	DeletedAppLogs     int64 `json:"deletedAppLogs"`
	DeletedTrafficLogs int64 `json:"deletedTrafficLogs"`
	DeletedRequestLogs int64 `json:"deletedRequestLogs"`
}

func (s *Store) cleanupLoop() {
	defer s.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			more, err := s.cleanupExpired()
			delay := time.Hour
			if err != nil {
				s.reportError(err)
				delay = time.Minute
			} else if more {
				delay = 100 * time.Millisecond
			}
			timer.Reset(delay)
		case <-s.done:
			return
		}
	}
}

func (s *Store) cleanupExpired() (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("日志存储不可清理")
	}
	cutoff := RetentionCutoff(time.Now()).UnixMilli()
	// 顺序必须与 cleanupBatch 的三个槽位一致。
	targets := []struct {
		statement string
		initial   int
	}{
		{`DELETE FROM app_logs WHERE id IN (SELECT id FROM app_logs WHERE unix_milli < ? ORDER BY unix_milli, id LIMIT ?)`, cleanupBatchSize},
		{`DELETE FROM traffic_logs WHERE id IN (SELECT id FROM traffic_logs WHERE unix_milli < ? ORDER BY unix_milli, id LIMIT ?)`, cleanupShrinkBatch},
		{`DELETE FROM request_logs WHERE id IN (SELECT id FROM request_logs WHERE unix_milli < ? ORDER BY unix_milli, id LIMIT ?)`, cleanupShrinkBatch},
		{`DELETE FROM traffic_bodies WHERE body_hash IN (SELECT b.body_hash FROM traffic_bodies b WHERE b.created_at < ? AND NOT EXISTS (SELECT 1 FROM traffic_body_refs r WHERE r.body_hash = b.body_hash) LIMIT ?)`, cleanupShrinkBatch},
	}
	more := false
	for i, target := range targets {
		select {
		case <-s.done:
			return false, nil
		default:
		}
		again, err := s.cleanupTable(target.statement, cutoff, i, target.initial)
		if err != nil {
			return false, err
		}
		more = more || again
	}
	return more, nil
}

// cleanupTable 删除一张表的一批过期日志，写完或超时后立刻放开写锁。
// 超时只缩小下次批量；中断会回滚当前批，不能把它记成清理失败，否则会每分钟重试同一批并永远没有进度。
func (s *Store) cleanupTable(statement string, cutoff int64, table int, initial int) (bool, error) {
	if table < 0 || table >= len(s.cleanupBatch) {
		return false, fmt.Errorf("日志清理表序号越界: %d", table)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := s.cleanupBatch[table]
	if batch < 1 {
		batch = initial
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	started := time.Now()
	var result sql.Result
	var err error
	if table == 1 || table == 2 {
		result, err = s.deleteExpiredIndexedLogs(ctx, dayTables[table], cutoff, batch)
	} else {
		result, err = s.db.ExecContext(ctx, statement, cutoff, batch)
	}
	timedOut := isCleanupTimeout(err)
	if err != nil && !timedOut {
		return false, fmt.Errorf("清理过期日志失败: %w", err)
	}
	var affected int64
	if !timedOut {
		affected, err = result.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("读取日志清理结果失败: %w", err)
		}
	}
	next, ceiling, again, giveUp := nextCleanupBatch(batch, s.cleanupCeiling[table], affected, time.Since(started), timedOut)
	s.cleanupBatch[table] = next
	s.cleanupCeiling[table] = ceiling
	if giveUp {
		return false, fmt.Errorf("清理过期日志失败: %w", err)
	}
	return again, nil
}

// nextCleanupBatch 决定下一批删多少。
// 超时且还能缩小：记为仍有剩余，不视为失败。单行仍超时才放弃。
// 删满且远快于预算时放大，但不超过上次超时的批量。未满一批说明这张表已经追上。
func nextCleanupBatch(batch, ceiling int, affected int64, elapsed time.Duration, timedOut bool) (next, nextCeiling int, more, giveUp bool) {
	if batch < 1 {
		batch = cleanupBatchSize
	}
	if ceiling < 0 {
		ceiling = 0
	}
	if timedOut {
		if batch <= 1 {
			return 1, ceiling, false, true
		}
		next = batch / 2
		if batch > cleanupShrinkBatch {
			next = cleanupShrinkBatch
		}
		if next < 1 {
			next = 1
		}
		return next, batch, true, false
	}
	if affected < int64(batch) {
		return batch, ceiling, false, false
	}
	next = batch
	nextCeiling = ceiling
	if elapsed < cleanupTimeout/4 && batch < cleanupBatchSize {
		grown := batch * 2
		if grown > cleanupBatchSize {
			grown = cleanupBatchSize
		}
		if ceiling == 0 || grown < ceiling {
			next = grown
		}
	}
	return next, nextCeiling, true, false
}

func isCleanupTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// 驱动偶发只返回 SQLITE_INTERRUPT，不带回 context 错误。
	message := err.Error()
	return strings.Contains(message, "SQLITE_INTERRUPT") || strings.Contains(message, "sqlite3_interrupt")
}

// RetentionCutoff 返回“昨天 00:00:00”本地时间。早于该时刻的日志会被删除，
// 因此库里只保留今天与昨天。
func RetentionCutoff(now time.Time) time.Time {
	localNow := now.In(time.Local)
	year, month, day := localNow.Date()
	today := time.Date(year, month, day, 0, 0, 0, 0, time.Local)
	return today.AddDate(0, 0, -1)
}

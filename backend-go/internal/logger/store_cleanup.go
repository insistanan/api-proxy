// 日志清理：手动清理和自动分批清理，共用 Store 的写锁与连接。
package logger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
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

// cleanupTarget 描述自动清理的一张表：顺序必须与 cleanupBatch、cleanupCeiling、
// cleanupSkipUntil 的下标一致。正文表只能按内容寻址，没有统一的 unix_milli 列。
type cleanupTarget struct {
	name string
	// statement 只给不走摘要索引的表使用；索引表的语句由 deleteExpiredIndexedLogs 生成。
	statement string
	initial   int
	// indexed 为 true 时删除前要同步重建摘要索引。
	indexed bool
}

func cleanupTargets() []cleanupTarget {
	return []cleanupTarget{
		{
			name:      "app_logs",
			statement: `DELETE FROM app_logs WHERE id IN (SELECT id FROM app_logs WHERE unix_milli < ? ORDER BY unix_milli, id LIMIT ?)`,
			initial:   cleanupBatchSize,
		},
		{
			name:    "traffic_logs",
			initial: cleanupShrinkBatch,
			indexed: true,
		},
		{
			name:    "request_logs",
			initial: cleanupShrinkBatch,
			indexed: true,
		},
		{
			// 正文按首次写入时间过期；ORDER BY created_at 让覆盖索引直接给出候选行，
			// 没有可删正文时只走索引范围扫描，不会再把整张正文表读一遍。
			name:      "traffic_bodies",
			statement: `DELETE FROM traffic_bodies WHERE body_hash IN (SELECT b.body_hash FROM traffic_bodies b WHERE b.created_at < ? AND NOT EXISTS (SELECT 1 FROM traffic_body_refs r WHERE r.body_hash = b.body_hash) ORDER BY b.created_at LIMIT ?)`,
			initial:   cleanupShrinkBatch,
		},
	}
}

func (s *Store) cleanupLoop() {
	defer s.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	// 一轮清理可能跨多批，删到追平后再汇总输出一次删除行数，避免每批刷一条日志。
	var roundDeleted int64
	for {
		select {
		case <-timer.C:
			more, deleted, err := s.cleanupExpired()
			roundDeleted += deleted
			delay := time.Hour
			if err != nil {
				s.reportCleanupError(err)
				delay = time.Minute
			} else if more {
				delay = 100 * time.Millisecond
			} else {
				if roundDeleted > 0 {
					s.checkpointAfterCleanup()
					log.Printf("[Logger-Cleanup] 已清理 %d 行过期日志", roundDeleted)
				}
				roundDeleted = 0
			}
			timer.Reset(delay)
		case <-s.done:
			return
		}
	}
}

func (s *Store) cleanupExpired() (bool, int64, error) {
	if s == nil || s.db == nil {
		return false, 0, fmt.Errorf("日志存储不可清理")
	}
	cutoff := RetentionCutoff(time.Now()).UnixMilli()
	targets := cleanupTargets()
	var (
		more    bool
		deleted int64
		errs    []error
	)
	for i, target := range targets {
		select {
		case <-s.done:
			return more, deleted, nil
		default:
		}
		again, rows, err := s.cleanupTable(target, cutoff, i)
		deleted += rows
		more = more || again
		if err != nil {
			// 一张表卡住不能阻止其他表继续清理，否则一张超时表会拖住整轮。
			errs = append(errs, err)
		}
	}
	return more, deleted, errors.Join(errs...)
}

// cleanupTable 删除一张表的一批过期日志，写完或超时后立刻放开写锁。
// 超时只缩小下次批量；中断会回滚当前批，不能把它记成清理失败，否则会每分钟重试同一批并永远没有进度。
func (s *Store) cleanupTable(target cleanupTarget, cutoff int64, table int) (bool, int64, error) {
	if table < 0 || table >= len(s.cleanupBatch) {
		return false, 0, fmt.Errorf("日志清理表序号越界: %d", table)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if until := s.cleanupSkipUntil[table]; !until.IsZero() && time.Now().Before(until) {
		return false, 0, nil
	}
	batch := s.cleanupBatch[table]
	if batch < 1 {
		batch = target.initial
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	started := time.Now()
	var result sql.Result
	var err error
	if target.indexed {
		result, err = s.deleteExpiredIndexedLogs(ctx, target.name, cutoff, batch)
	} else {
		result, err = s.db.ExecContext(ctx, target.statement, cutoff, batch)
	}
	elapsed := time.Since(started)
	timedOut := isCleanupTimeout(ctx, err)
	if err != nil && !timedOut {
		s.cleanupSkipUntil[table] = started.Add(cleanupRetryInterval)
		return false, 0, fmt.Errorf("清理 %s 过期日志失败（批量 %d，耗时 %s）: %w",
			target.name, batch, elapsed.Round(time.Millisecond), err)
	}
	var affected int64
	if !timedOut {
		affected, err = result.RowsAffected()
		if err != nil {
			return false, 0, fmt.Errorf("读取 %s 清理结果失败: %w", target.name, err)
		}
	}
	next, ceiling, again, giveUp := nextCleanupBatch(batch, s.cleanupCeiling[table], affected, elapsed, timedOut)
	s.cleanupBatch[table] = next
	s.cleanupCeiling[table] = ceiling
	if giveUp {
		// 单行仍超时说明这批语句的代价与行数无关（例如缺索引导致全表扫描），
		// 继续每分钟重试只会重复撞同一根墙，先暂停这张表，让其他表照常清理。
		s.cleanupSkipUntil[table] = started.Add(cleanupRetryInterval)
		return false, 0, fmt.Errorf("清理 %s 过期日志失败（单批 %d 行仍超时，%s 内不再重试）: %w",
			target.name, batch, cleanupRetryInterval, err)
	}
	s.cleanupSkipUntil[table] = time.Time{}
	return again, affected, nil
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

// isCleanupTimeout 判断这次失败是否只是本批预算到点。
// 以 ctx 状态为主：到点后驱动可能返回自己的中断错误，只匹配错误文本会漏判成硬失败。
func isCleanupTimeout(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	// 驱动偶发只返回 SQLITE_INTERRUPT，不带回 context 错误。
	message := err.Error()
	return strings.Contains(message, "SQLITE_INTERRUPT") || strings.Contains(message, "sqlite3_interrupt")
}

// checkpointAfterCleanup 删除后把已提交的 WAL 页写回主库，让 WAL 可以复用。
// 用 PASSIVE 不等待读者，避免为了收缩 WAL 反而阻塞日志写入；失败只输出诊断，不影响清理结果。
func (s *Store) checkpointAfterCleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		fmt.Fprintf(os.Stderr, "[Logger-Cleanup] WAL checkpoint 失败（不影响清理结果）: %v\n", err)
	}
}

// 清理失败属于存储自诊断，直接写 stderr，避免再进一次日志队列。
func (s *Store) reportCleanupError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "[Logger-Cleanup] %v\n", err)
}

// RetentionCutoff 返回“昨天 00:00:00”本地时间。早于该时刻的日志会被删除，
// 因此库里只保留今天与昨天。
func RetentionCutoff(now time.Time) time.Time {
	localNow := now.In(time.Local)
	year, month, day := localNow.Date()
	today := time.Date(year, month, day, 0, 0, 0, 0, time.Local)
	return today.AddDate(0, 0, -1)
}

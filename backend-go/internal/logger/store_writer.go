// 日志写入队列：背压、批处理与错误通知，队列和关闭责任仍由 Store 持有。
package logger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// enqueue 对条数和字节数同时施加背压；已接收的日志不因队列拥堵被丢弃。
// 数据库写入有超时，写入失败或关闭会唤醒所有等待中的生产者。
func (s *Store) enqueue(item queuedWrite) error {
	if s == nil || s.readOnly {
		return fmt.Errorf("日志存储不可写")
	}
	item.bytes = queuedWriteBytes(item)
	if item.bytes > maxQueuedLogBytes {
		return fmt.Errorf("单条日志超过 %d MiB 缓冲上限", maxQueuedLogBytes>>20)
	}
	for {
		s.queueMu.Lock()
		if s.closed {
			s.queueMu.Unlock()
			return fmt.Errorf("日志存储已关闭")
		}
		if err := s.writeErr; err != nil {
			s.queueMu.Unlock()
			return err
		}
		if len(s.queue) < cap(s.queue) && s.queueBytes+item.bytes <= maxQueuedLogBytes {
			s.queueBytes += item.bytes
			if item.flushDone != nil {
				s.flushWaiters[item.flushDone] = struct{}{}
			}
			s.queue <- item
			s.queueMu.Unlock()
			return nil
		}
		ready := s.spaceReady
		s.queueMu.Unlock()
		<-ready
	}
}

func (s *Store) writeLoop() {
	defer s.wg.Done()
	batch := make([]queuedWrite, 0, maxWriteBatchEntries)
	for {
		var first queuedWrite
		select {
		case first = <-s.queue:
		case <-s.done:
			select {
			case first = <-s.queue:
			default:
				return
			}
		}
		batch = append(batch[:0], first)
		batchBytes := first.bytes
	collect:
		for len(batch) < maxWriteBatchEntries && batchBytes < maxWriteBatchBytes && batch[len(batch)-1].kind != writeFlush {
			select {
			case item := <-s.queue:
				batch = append(batch, item)
				batchBytes += item.bytes
			default:
				break collect
			}
		}
		err := s.writeBatchWithRetry(batch)
		s.completeBatch(batch, err)
		if err != nil {
			// 只有关闭期间仍无法落库才结束重试；Close 返回错误并释放内存。
			for {
				select {
				case item := <-s.queue:
					s.completeBatch([]queuedWrite{item}, err)
				default:
					return
				}
			}
		}
	}
}

func (s *Store) writeBatchWithRetry(batch []queuedWrite) error {
	for {
		err := s.applyBatch(batch)
		s.queueMu.Lock()
		if err == nil {
			recovered := s.writeErr != nil
			s.writeErr = nil
			s.notifySpaceLocked()
			s.queueMu.Unlock()
			if recovered {
				fmt.Fprintln(os.Stderr, "[Logger-Recovery] 日志数据库已恢复，继续写入已接收的日志")
			}
			return nil
		}
		s.writeErr = fmt.Errorf("日志批次暂未落库，将保留缓冲并重试: %w", err)
		err = s.writeErr
		for done := range s.flushWaiters {
			done <- err
			delete(s.flushWaiters, done)
		}
		s.notifySpaceLocked()
		s.queueMu.Unlock()
		s.reportError(err)
		timer := time.NewTimer(time.Second)
		select {
		case <-s.done:
			timer.Stop()
			err = fmt.Errorf("关闭日志存储时仍有记录未落库: %w", err)
			s.queueMu.Lock()
			s.writeErr = err
			s.queueMu.Unlock()
			return err
		case <-timer.C:
		}
	}
}

func (s *Store) notifySpaceLocked() {
	close(s.spaceReady)
	s.spaceReady = make(chan struct{})
}

// applyBatch / VACUUM 必须整段占用写连接；cleanupLoop 的自动清理同样串行化，避免交错。
func (s *Store) applyBatch(batch []queuedWrite) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyBatchLocked(batch)
}

func (s *Store) applyBatchLocked(batch []queuedWrite) error {
	if len(batch) == 1 && batch[0].kind == writeFlush {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), logWriteTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始日志写入事务失败: %w", err)
	}
	defer tx.Rollback()
	statements := make(map[writeKind]*sql.Stmt, 3)
	defer func() {
		for _, statement := range statements {
			_ = statement.Close()
		}
	}()
	requestIDs := make(map[string]struct{})
	for _, item := range batch {
		if err := applyLogWrite(ctx, tx, statements, item); err != nil {
			return err
		}
		if item.kind == writeTraffic && item.traffic.RequestID != "" {
			requestIDs[item.traffic.RequestID] = struct{}{}
		}
		if item.kind == writeRequest && item.request.RequestID != "" {
			requestIDs[item.request.RequestID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(requestIDs))
	for id := range requestIDs {
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		if err := refreshRequestIndex(ctx, tx, ids); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE log_index_state SET
			traffic_id = COALESCE((SELECT MAX(id) FROM traffic_logs), 0),
			request_id = COALESCE((SELECT MAX(id) FROM request_logs), 0) WHERE singleton = 1`); err != nil {
			return fmt.Errorf("保存日志索引进度失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交日志写入事务失败: %w", err)
	}
	return nil
}

func applyLogWrite(ctx context.Context, tx *sql.Tx, statements map[writeKind]*sql.Stmt, item queuedWrite) error {
	var query string
	var args []any
	var bodyHash string
	switch item.kind {
	case writeFlush:
		return nil
	case writeApp:
		query = `INSERT INTO app_logs (unix_milli, timestamp, tag, message) VALUES (?, ?, ?, ?)`
		args = []any{item.app.UnixMilli, item.app.Timestamp, item.app.Tag, item.app.Message}
	case writeTraffic:
		storedBody := item.traffic.Body
		// 小型元数据保持内联；大型正文按内容寻址，轮次间不建立依赖链。
		if len(storedBody) >= 4096 && item.traffic.Phase != PhaseRequestStart && item.traffic.Phase != PhaseRequestEnd {
			digest := sha256.Sum256([]byte(storedBody))
			bodyHash = hex.EncodeToString(digest[:])
			storedBody = ""
		}
		streamValue := 0
		if item.traffic.Stream {
			streamValue = 1
		}
		truncatedValue := 0
		if item.traffic.Truncated {
			truncatedValue = 1
		}
		query = `INSERT INTO traffic_logs (
			unix_milli, timestamp, request_id, attempt_id, phase, api_type, method, url,
			status_code, stream, headers_json, body, truncated, original_bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		args = []any{
			item.traffic.UnixMilli, item.traffic.Timestamp, item.traffic.RequestID, item.traffic.AttemptID,
			item.traffic.Phase, item.traffic.APIType, item.traffic.Method, item.traffic.URL,
			item.traffic.StatusCode, streamValue, item.traffic.HeadersJSON, storedBody,
			truncatedValue, item.traffic.OriginalBytes,
		}
	case writeRequest:
		query = `INSERT INTO request_logs (unix_milli, timestamp, request_id, api_type, payload_json) VALUES (?, ?, ?, ?, ?)`
		args = []any{item.request.UnixMilli, item.request.Timestamp, item.request.RequestID, item.request.APIType, string(item.request.Payload)}
	default:
		return fmt.Errorf("未知日志类型 %d", item.kind)
	}
	statement := statements[item.kind]
	if statement == nil {
		var err error
		statement, err = tx.PrepareContext(ctx, query)
		if err != nil {
			return fmt.Errorf("准备日志写入语句失败: %w", err)
		}
		statements[item.kind] = statement
	}
	result, err := statement.ExecContext(ctx, args...)
	if err != nil {
		return fmt.Errorf("写入日志类型 %d 失败: %w", item.kind, err)
	}
	if bodyHash != "" {
		id, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("读取流量日志 ID 失败: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO traffic_bodies (body_hash, body, created_at) VALUES (?, ?, ?) ON CONFLICT(body_hash) DO NOTHING`, bodyHash, item.traffic.Body, item.traffic.UnixMilli); err != nil {
			return fmt.Errorf("写入去重正文失败: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO traffic_body_refs (traffic_id, body_hash) VALUES (?, ?)`, id, bodyHash); err != nil {
			return fmt.Errorf("写入正文引用失败: %w", err)
		}
	}
	return nil
}

// 诊断直接写 stderr，避免重新进入日志队列；故障持续时最多每 10 秒输出一次。
func (s *Store) reportError(err error) {
	if s == nil {
		fmt.Fprintf(os.Stderr, "[Logger-Write] %v\n", err)
		return
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	if time.Since(s.lastDiagnostic) < 10*time.Second {
		return
	}
	s.lastDiagnostic = time.Now()
	fmt.Fprintf(os.Stderr, "[Logger-Write] %v\n", err)
}

package logger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type writeKind int

const (
	writeApp writeKind = iota
	writeTraffic
	writeRequest
	writeFlush
)

const (
	maxQueuedLogBytes    = 32 << 20
	maxWriteBatchBytes   = 2 << 20
	maxWriteBatchEntries = 256
	logWriteTimeout      = 5 * time.Second
	cleanupBatchSize     = 1000
)

type queuedWrite struct {
	kind      writeKind
	app       AppLog
	traffic   TrafficLog
	request   RequestLogRecord
	flushDone chan error
	bytes     int
}

// Store 是 logs.db 的唯一写入口。服务进程持有一份；CLI 只读打开。
type Store struct {
	db        *sql.DB
	readDB    *sql.DB
	dbPath    string
	queue     chan queuedWrite
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
	readOnly  bool
	closeErr  error

	queueMu        sync.Mutex
	queueBytes     int
	spaceReady     chan struct{}
	flushWaiters   map[chan error]struct{}
	closed         bool
	writeErr       error
	diagnosticMu   sync.Mutex
	lastDiagnostic time.Time
}

func logDatabaseURI(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("日志数据库路径不能为空")
	}
	if path == ":memory:" {
		return "file:app_logs_" + NewRequestID(), nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析日志数据库路径失败: %w", err)
	}
	absPath = filepath.ToSlash(absPath)
	if !strings.HasPrefix(absPath, "/") {
		absPath = "/" + absPath
	}
	return (&url.URL{Scheme: "file", Path: absPath}).String(), nil
}

func openSQLite(dsn string, connections int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开日志数据库失败: %w", err)
	}
	db.SetMaxOpenConns(connections)
	db.SetMaxIdleConns(connections)
	db.SetConnMaxLifetime(0)
	ctx, cancel := context.WithTimeout(context.Background(), logWriteTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接日志数据库失败: %w", err)
	}
	return db, nil
}

func initLogSchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS app_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			unix_milli INTEGER NOT NULL,
			timestamp TEXT NOT NULL,
			tag TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_app_logs_unix ON app_logs(unix_milli, id)`,
		`CREATE TABLE IF NOT EXISTS traffic_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			unix_milli INTEGER NOT NULL,
			timestamp TEXT NOT NULL,
			request_id TEXT NOT NULL,
			attempt_id TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL,
			api_type TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			status_code INTEGER NOT NULL DEFAULT 0,
			stream INTEGER NOT NULL DEFAULT 0,
			headers_json TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			truncated INTEGER NOT NULL DEFAULT 0,
			original_bytes INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_traffic_logs_unix ON traffic_logs(unix_milli, id)`,
		`CREATE INDEX IF NOT EXISTS idx_traffic_logs_request ON traffic_logs(request_id, unix_milli, id)`,
		`CREATE TABLE IF NOT EXISTS request_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			unix_milli INTEGER NOT NULL,
			timestamp TEXT NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			api_type TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_unix ON request_logs(unix_milli, id)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_api ON request_logs(api_type, unix_milli, id)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_request ON request_logs(request_id, unix_milli, id)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("初始化日志表失败: %w", err)
		}
	}
	return nil
}

// Open 打开可写日志库并启动后台写入 / 清理。
func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	uri, err := logDatabaseURI(path)
	if err != nil {
		return nil, err
	}
	writeMode, readMode := "rwc", "ro"
	if path == ":memory:" {
		writeMode, readMode = "memory&cache=shared", "memory&cache=shared"
	} else if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("创建日志数据库目录失败: %w", err)
	}
	db, err := openSQLite(uri+"?mode="+writeMode+"&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(3000)", 1)
	if err != nil {
		return nil, err
	}
	if err := initLogSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	readDB, err := openSQLite(uri+"?mode="+readMode+"&_pragma=query_only(1)&_pragma=busy_timeout(3000)", 4)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &Store{
		db:           db,
		readDB:       readDB,
		dbPath:       path,
		queue:        make(chan queuedWrite, writeQueueSize),
		done:         make(chan struct{}),
		spaceReady:   make(chan struct{}),
		flushWaiters: make(map[chan error]struct{}),
	}
	store.wg.Add(2)
	go store.writeLoop()
	go store.cleanupLoop()
	return store, nil
}

// OpenReadOnly 供 CLI 查询。不启动写入循环。
func OpenReadOnly(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultDBPath
	}
	if path == ":memory:" {
		return nil, fmt.Errorf("内存日志数据库不能通过路径重新只读打开")
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("日志数据库不存在: %s（服务启动后才会创建）", path)
		}
		return nil, fmt.Errorf("访问日志数据库失败: %w", err)
	}
	uri, err := logDatabaseURI(path)
	if err != nil {
		return nil, err
	}
	db, err := openSQLite(uri+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(3000)", 4)
	if err != nil {
		return nil, err
	}
	store := &Store{
		readDB:   db,
		dbPath:   path,
		readOnly: true,
	}
	return store, nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.dbPath
}

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

func queuedWriteBytes(item queuedWrite) int {
	return 512 + len(item.app.Timestamp) + len(item.app.Tag) + len(item.app.Message) +
		len(item.traffic.Timestamp) + len(item.traffic.RequestID) + len(item.traffic.AttemptID) +
		len(item.traffic.Phase) + len(item.traffic.APIType) + len(item.traffic.Method) +
		len(item.traffic.URL) + len(item.traffic.HeadersJSON) + len(item.traffic.Body) +
		len(item.request.Timestamp) + len(item.request.RequestID) + len(item.request.APIType) + len(item.request.Payload)
}

func (s *Store) record(item queuedWrite) error {
	err := s.enqueue(item)
	if err != nil {
		s.reportError(err)
	}
	return err
}

func (s *Store) RecordAppLog(entry AppLog) error {
	entry = normalizeAppLog(entry)
	entry.Tag = strings.Clone(entry.Tag)
	entry.Message = strings.Clone(entry.Message)
	return s.record(queuedWrite{kind: writeApp, app: entry})
}

func (s *Store) RecordTraffic(entry TrafficLog) error {
	return s.record(queuedWrite{kind: writeTraffic, traffic: normalizeTrafficLog(entry)})
}

func (s *Store) RecordRequest(entry RequestLogRecord) error {
	entry = normalizeRequestRecord(entry)
	entry.Payload = append([]byte(nil), entry.Payload...)
	return s.record(queuedWrite{kind: writeRequest, request: entry})
}

// Flush 等待此前接收的日志提交，并返回异步写入的实际错误。
func (s *Store) Flush() error {
	if s != nil && s.readOnly {
		return nil
	}
	done := make(chan error, 1)
	if err := s.enqueue(queuedWrite{kind: writeFlush, flushDone: done}); err != nil {
		return err
	}
	return <-done
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.queueMu.Lock()
		s.closed = true
		if !s.readOnly {
			close(s.done)
			s.notifySpaceLocked()
		}
		s.queueMu.Unlock()
		if !s.readOnly {
			s.wg.Wait()
		}
		s.queueMu.Lock()
		s.closeErr = s.writeErr
		s.queueMu.Unlock()
		if s.readDB != nil {
			s.closeErr = errors.Join(s.closeErr, s.readDB.Close())
		}
		if s.db != nil {
			s.closeErr = errors.Join(s.closeErr, s.db.Close())
		}
	})
	return s.closeErr
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

func (s *Store) completeBatch(batch []queuedWrite, err error) {
	s.queueMu.Lock()
	for i := range batch {
		s.queueBytes -= batch[i].bytes
		if batch[i].flushDone != nil {
			if _, waiting := s.flushWaiters[batch[i].flushDone]; waiting {
				batch[i].flushDone <- err
				delete(s.flushWaiters, batch[i].flushDone)
			}
		}
		batch[i] = queuedWrite{}
	}
	s.notifySpaceLocked()
	s.queueMu.Unlock()
}

func (s *Store) applyBatch(batch []queuedWrite) error {
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
	for _, item := range batch {
		if err := applyLogWrite(ctx, tx, statements, item); err != nil {
			return err
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
	switch item.kind {
	case writeFlush:
		return nil
	case writeApp:
		query = `INSERT INTO app_logs (unix_milli, timestamp, tag, message) VALUES (?, ?, ?, ?)`
		args = []any{item.app.UnixMilli, item.app.Timestamp, item.app.Tag, item.app.Message}
	case writeTraffic:
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
			item.traffic.StatusCode, streamValue, item.traffic.HeadersJSON, item.traffic.Body,
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
	if _, err := statement.ExecContext(ctx, args...); err != nil {
		return fmt.Errorf("写入日志类型 %d 失败: %w", item.kind, err)
	}
	return nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cutoff := RetentionCutoff(time.Now()).UnixMilli()
	statements := []string{
		`DELETE FROM app_logs WHERE id IN (SELECT id FROM app_logs WHERE unix_milli < ? ORDER BY unix_milli, id LIMIT ?)`,
		`DELETE FROM traffic_logs WHERE id IN (SELECT id FROM traffic_logs WHERE unix_milli < ? ORDER BY unix_milli, id LIMIT ?)`,
		`DELETE FROM request_logs WHERE id IN (SELECT id FROM request_logs WHERE unix_milli < ? ORDER BY unix_milli, id LIMIT ?)`,
	}
	more := false
	for _, statement := range statements {
		select {
		case <-s.done:
			return false, nil
		default:
		}
		result, err := s.db.ExecContext(ctx, statement, cutoff, cleanupBatchSize)
		if err != nil {
			return false, fmt.Errorf("清理过期日志失败: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("读取日志清理结果失败: %w", err)
		}
		more = more || affected == cleanupBatchSize
	}
	return more, nil
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

// RetentionCutoff 返回“昨天 00:00:00”本地时间。早于该时刻的日志会被删除，
// 因此库里只保留今天与昨天。
func RetentionCutoff(now time.Time) time.Time {
	localNow := now.In(time.Local)
	year, month, day := localNow.Date()
	today := time.Date(year, month, day, 0, 0, 0, 0, time.Local)
	return today.AddDate(0, 0, -1)
}

func (s *Store) QueryAppLogs(ctx context.Context, opts QueryOptions) ([]AppLog, error) {
	if s == nil {
		return nil, nil
	}
	query := `SELECT id, unix_milli, timestamp, tag, message FROM app_logs WHERE unix_milli >= ? AND unix_milli <= ?`
	args := []any{opts.From, opts.To}
	query += ` ORDER BY unix_milli ASC, id ASC`
	if opts.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, opts.Limit)
	}
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询 app_logs 失败: %w", err)
	}
	defer rows.Close()
	entries := make([]AppLog, 0)
	for rows.Next() {
		var entry AppLog
		if err := rows.Scan(&entry.ID, &entry.UnixMilli, &entry.Timestamp, &entry.Tag, &entry.Message); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) QueryTrafficLogs(ctx context.Context, opts QueryOptions) ([]TrafficLog, error) {
	if s == nil {
		return nil, nil
	}
	query := `SELECT id, unix_milli, timestamp, request_id, attempt_id, phase, api_type, method, url,
		status_code, stream, headers_json, body, truncated, original_bytes
		FROM traffic_logs WHERE unix_milli >= ? AND unix_milli <= ?`
	args := []any{opts.From, opts.To}
	if opts.RequestID != "" {
		query += ` AND request_id = ?`
		args = append(args, opts.RequestID)
	}
	query += ` ORDER BY unix_milli ASC, id ASC`
	if opts.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, opts.Limit)
	}
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询 traffic_logs 失败: %w", err)
	}
	defer rows.Close()
	entries := make([]TrafficLog, 0)
	for rows.Next() {
		var entry TrafficLog
		var streamValue, truncatedValue int
		if err := rows.Scan(
			&entry.ID, &entry.UnixMilli, &entry.Timestamp, &entry.RequestID, &entry.AttemptID,
			&entry.Phase, &entry.APIType, &entry.Method, &entry.URL, &entry.StatusCode,
			&streamValue, &entry.HeadersJSON, &entry.Body, &truncatedValue, &entry.OriginalBytes,
		); err != nil {
			return nil, err
		}
		entry.Stream = streamValue != 0
		entry.Truncated = truncatedValue != 0
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) QueryRequestLogs(ctx context.Context, opts QueryOptions) ([]json.RawMessage, error) {
	if s == nil {
		return nil, nil
	}
	query := `SELECT payload_json FROM request_logs WHERE 1=1`
	args := make([]any, 0, 6)
	if opts.From > 0 {
		query += ` AND unix_milli >= ?`
		args = append(args, opts.From)
	}
	if opts.To > 0 {
		query += ` AND unix_milli <= ?`
		args = append(args, opts.To)
	}
	if opts.RequestID != "" {
		query += ` AND request_id = ?`
		args = append(args, opts.RequestID)
	}
	if opts.APIType != "" {
		query += ` AND api_type = ?`
		args = append(args, opts.APIType)
	}
	order := ` ORDER BY unix_milli DESC, id DESC`
	if opts.From > 0 || opts.To > 0 {
		order = ` ORDER BY unix_milli ASC, id ASC`
	}
	query += order
	if opts.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, opts.Limit)
	}
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询 request_logs 失败: %w", err)
	}
	defer rows.Close()
	entries := make([]json.RawMessage, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		entries = append(entries, json.RawMessage(payload))
	}
	return entries, rows.Err()
}

func normalizeAppLog(entry AppLog) AppLog {
	now := time.Now()
	if entry.UnixMilli == 0 {
		if parsed, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
			entry.UnixMilli = parsed.UnixMilli()
			now = parsed
		} else {
			entry.UnixMilli = now.UnixMilli()
		}
	} else {
		now = time.UnixMilli(entry.UnixMilli)
	}
	if entry.Timestamp == "" {
		entry.Timestamp = now.UTC().Format(time.RFC3339Nano)
	}
	return entry
}

func normalizeTrafficLog(entry TrafficLog) TrafficLog {
	now := time.Now()
	if entry.UnixMilli == 0 {
		entry.UnixMilli = now.UnixMilli()
	} else {
		now = time.UnixMilli(entry.UnixMilli)
	}
	if entry.Timestamp == "" {
		entry.Timestamp = now.UTC().Format(time.RFC3339Nano)
	}
	if entry.Body != "" {
		prepared, originalBytes, truncated := PrepareBody([]byte(entry.Body))
		entry.Body = prepared
		if entry.OriginalBytes == 0 {
			entry.OriginalBytes = originalBytes
		}
		entry.Truncated = entry.Truncated || truncated
	}
	return entry
}

func normalizeRequestRecord(entry RequestLogRecord) RequestLogRecord {
	now := time.Now()
	if entry.UnixMilli == 0 {
		if parsed, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
			entry.UnixMilli = parsed.UnixMilli()
			now = parsed
		} else {
			entry.UnixMilli = now.UnixMilli()
		}
	}
	if entry.Timestamp == "" {
		entry.Timestamp = now.UTC().Format(time.RFC3339Nano)
	}
	if entry.APIType == "" {
		entry.APIType = "unknown"
	}
	if len(entry.Payload) == 0 {
		entry.Payload = []byte("{}")
	}
	return entry
}

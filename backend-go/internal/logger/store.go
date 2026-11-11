package logger

import (
	"context"
	"database/sql"
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
	// 自动清理和日志写入共用一条连接。单批预算到点就中断，避免大正文把写入堵住。
	cleanupTimeout = 1 * time.Second
	// 流量和请求日志带正文。一次删一千行很容易超预算，超时还会把 WAL 里这笔删除整批回滚。
	cleanupShrinkBatch = 32
	// 某张表报错或单行仍超时后暂停清理这么久，避免每分钟重复扫描同一张大表。
	cleanupRetryInterval = time.Hour
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
	db           *sql.DB
	readDB       *sql.DB
	dbPath       string
	bootID       string
	mu           sync.Mutex
	queue        chan queuedWrite
	done         chan struct{}
	closeOnce    sync.Once
	wg           sync.WaitGroup
	readOnly     bool
	bodyRefs     bool
	requestIndex bool
	closeErr     error

	queueMu        sync.Mutex
	queueBytes     int
	spaceReady     chan struct{}
	flushWaiters   map[chan error]struct{}
	closed         bool
	writeErr       error
	diagnosticMu   sync.Mutex
	lastDiagnostic time.Time

	// cleanupBatch / cleanupCeiling 按 app、traffic、request、body 记住删得动的批量。
	// ceiling 是上次超时的批量，本进程内不再涨回去；cleanupSkipUntil 表示该表暂停清理到什么时候。
	// 只在持有 s.mu 时访问。
	cleanupBatch     [4]int
	cleanupCeiling   [4]int
	cleanupSkipUntil [4]time.Time
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
		`CREATE TABLE IF NOT EXISTS traffic_bodies (body_hash TEXT PRIMARY KEY, body TEXT NOT NULL, created_at INTEGER NOT NULL)`,
		// 清理按 created_at 找过期正文；覆盖索引让判断不必读取大正文所在的整行。
		`CREATE INDEX IF NOT EXISTS idx_traffic_bodies_created ON traffic_bodies(created_at, body_hash)`,
		`CREATE TABLE IF NOT EXISTS traffic_body_refs (traffic_id INTEGER PRIMARY KEY, body_hash TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_traffic_body_refs_hash ON traffic_body_refs(body_hash)`,
		`CREATE TRIGGER IF NOT EXISTS delete_traffic_body_ref AFTER DELETE ON traffic_logs
			BEGIN DELETE FROM traffic_body_refs WHERE traffic_id = OLD.id; END`,
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
	return initRequestIndex(db)
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
		bootID:       NewRequestID(),
		bodyRefs:     true,
		requestIndex: true,
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
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'traffic_body_refs'`).Scan(&count); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("读取日志正文存储结构失败: %w", err)
	}
	store.bodyRefs = count > 0
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'log_request_index'`).Scan(&count); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("读取日志摘要索引结构失败: %w", err)
	}
	store.requestIndex = count > 0
	return store, nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.dbPath
}

// BootID 用于识别上次进程遗留且没有收尾的请求。
func (s *Store) BootID() string {
	if s == nil {
		return ""
	}
	return s.bootID
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
	entry = normalizeTrafficLog(entry)
	// 索引依赖生命周期 JSON；非法输入在入队前拒绝，避免永久重试阻塞后续日志。
	if _, err := parseTrafficLifecycle(entry.Phase, entry.Body); err != nil {
		s.reportError(err)
		return err
	}
	return s.record(queuedWrite{kind: writeTraffic, traffic: entry})
}

func (s *Store) RecordRequest(entry RequestLogRecord) error {
	entry = normalizeRequestRecord(entry)
	entry.Payload = append([]byte(nil), entry.Payload...)
	if _, err := parseRequestLogMeta(entry.Payload); err != nil {
		s.reportError(err)
		return err
	}
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

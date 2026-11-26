package metrics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore SQLite 持久化存储
type SQLiteStore struct {
	db     *sql.DB
	dbPath string

	// 写入缓冲区
	writeBuffer    []PersistentRecord
	bufferMu       sync.Mutex
	pendingRecords int
	pendingBytes   int
	droppedRecords uint64
	lastWriteError string

	// 配置
	batchSize             int           // 批量写入阈值（记录数）
	flushInterval         time.Duration // 定时刷新间隔
	retentionDays         int           // 数据保留天数
	retentionDaysProvider func() int    // 可选热更新来源，构造后不修改函数本身

	// 控制
	stopCh    chan struct{}
	wg        sync.WaitGroup
	closed    bool          // 是否已关闭
	flushMu   sync.Mutex    // 串行化 flush 与 delete 操作，避免并发竞态
	flushWake chan struct{} // 合并唤醒，由唯一写入循环刷盘
	flushErr  error
	closeOnce sync.Once
	closeErr  error
}

// SQLiteStoreConfig SQLite 存储配置
type SQLiteStoreConfig struct {
	DBPath                string // 数据库文件路径
	RetentionDays         int    // 数据保留天数（3-30）
	RetentionDaysProvider func() int
}

// 硬编码的内部配置
const (
	defaultBatchSize     = 100              // 批量写入阈值
	defaultFlushInterval = 30 * time.Second // 定时刷新间隔
	maxPendingRecords    = 4096
	maxPendingBytes      = 8 << 20
	metricsWriteTimeout  = 5 * time.Second
)

// NewSQLiteStore 创建 SQLite 存储
func NewSQLiteStore(cfg *SQLiteStoreConfig) (*SQLiteStore, error) {
	if cfg == nil {
		cfg = &SQLiteStoreConfig{
			DBPath:        ".config/metrics.db",
			RetentionDays: 7,
		}
	}

	// 验证保留天数范围
	if cfg.RetentionDays < 3 {
		cfg.RetentionDays = 3
	} else if cfg.RetentionDays > 30 {
		cfg.RetentionDays = 30
	}

	// 确保目录存在
	dir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败: %w", err)
	}

	// 打开数据库连接（WAL 模式 + NORMAL 同步）
	// modernc.org/sqlite 使用 _pragma= 语法设置 PRAGMA
	dsn := cfg.DBPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 设置连接池参数
	db.SetMaxOpenConns(1) // SQLite 单写入连接
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // 不限制连接生命周期

	// 初始化表结构
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化数据库 schema 失败: %w", err)
	}

	store := &SQLiteStore{
		db:                    db,
		dbPath:                cfg.DBPath,
		writeBuffer:           make([]PersistentRecord, 0, defaultBatchSize),
		batchSize:             defaultBatchSize,
		flushInterval:         defaultFlushInterval,
		retentionDays:         cfg.RetentionDays,
		retentionDaysProvider: cfg.RetentionDaysProvider,
		stopCh:                make(chan struct{}),
		flushWake:             make(chan struct{}, 1),
	}

	// 启动后台任务
	store.wg.Add(2)
	go store.flushLoop()
	go store.cleanupLoop()

	log.Printf("[SQLite-Init] 指标存储已初始化: %s (保留 %d 天)", cfg.DBPath, cfg.RetentionDays)
	return store, nil
}

// initSchema 初始化数据库表结构
func initSchema(db *sql.DB) error {
	schema := `
		-- 请求记录表
		CREATE TABLE IF NOT EXISTS request_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			metrics_key TEXT NOT NULL,
			base_url TEXT NOT NULL,
			key_mask TEXT NOT NULL,
			timestamp INTEGER NOT NULL,
			success INTEGER NOT NULL,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			cache_creation_tokens INTEGER DEFAULT 0,
			cache_read_tokens INTEGER DEFAULT 0,
			api_type TEXT NOT NULL DEFAULT 'messages',
			model TEXT NOT NULL DEFAULT ''
		);

		-- 索引：按 api_type 和时间查询
		CREATE INDEX IF NOT EXISTS idx_records_api_type_timestamp
			ON request_records(api_type, timestamp);

		-- 索引：按 metrics_key 查询
		CREATE INDEX IF NOT EXISTS idx_records_metrics_key
			ON request_records(metrics_key);

		-- 历史 API Key 注册表：换 Key 后的旧 Key 用于统计聚合，属于运行时数据
		CREATE TABLE IF NOT EXISTS channel_historical_keys (
			api_type TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			api_key TEXT NOT NULL,
			added_at INTEGER NOT NULL,
			PRIMARY KEY (api_type, channel_id, api_key)
		);
	`

	_, err := db.Exec(schema)
	if err != nil {
		return err
	}

	// 只迁移确实缺失的列；锁冲突和损坏不能被当作“已经迁移”。
	var modelColumns int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('request_records') WHERE name = 'model'").Scan(&modelColumns); err != nil {
		return fmt.Errorf("读取指标表结构失败: %w", err)
	}
	if modelColumns == 0 {
		if _, err := db.Exec("ALTER TABLE request_records ADD COLUMN model TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("迁移指标 model 列失败: %w", err)
		}
	}
	return nil
}

// AddRecord 添加记录到写入缓冲区（非阻塞）
func (s *SQLiteStore) AddRecord(record PersistentRecord) {
	size := persistentRecordBytes(record)
	s.bufferMu.Lock()
	if s.closed {
		s.bufferMu.Unlock()
		return // 已关闭，忽略新记录
	}
	// 在途批次仍占用预算，失败回填不会突破总容量。
	if s.pendingRecords >= maxPendingRecords || size > maxPendingBytes-s.pendingBytes {
		s.droppedRecords++
		dropped := s.droppedRecords
		s.bufferMu.Unlock()
		if dropped == 1 || dropped&(dropped-1) == 0 {
			log.Printf("[SQLite-Queue] 指标队列容量不足，累计拒收 %d 条，持久统计存在缺口", dropped)
		}
		return
	}
	record.MetricsKey = strings.Clone(record.MetricsKey)
	record.BaseURL = strings.Clone(record.BaseURL)
	record.KeyMask = strings.Clone(record.KeyMask)
	record.Model = strings.Clone(record.Model)
	record.APIType = strings.Clone(record.APIType)
	s.writeBuffer = append(s.writeBuffer, record)
	s.pendingRecords++
	s.pendingBytes += size
	shouldFlush := len(s.writeBuffer) >= s.batchSize
	s.bufferMu.Unlock()

	if shouldFlush {
		select {
		case s.flushWake <- struct{}{}:
		default:
		}
	}
}

// flush 刷新缓冲区到数据库
func (s *SQLiteStore) flush() error {
	s.bufferMu.Lock()
	if len(s.writeBuffer) == 0 {
		s.bufferMu.Unlock()
		return nil
	}

	// 取出缓冲区数据
	records := s.writeBuffer
	s.writeBuffer = make([]PersistentRecord, 0, s.batchSize)
	s.bufferMu.Unlock()

	// 批量写入
	err := s.batchInsertRecords(records)
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	if err != nil {
		log.Printf("[SQLite-Flush] 警告: 批量写入指标记录失败: %v", err)
		s.lastWriteError = err.Error()
		s.writeBuffer = append(records, s.writeBuffer...)
		return fmt.Errorf("%d 条指标未能落盘: %w", len(records), err)
	}
	s.lastWriteError = ""
	s.pendingRecords -= len(records)
	for _, record := range records {
		s.pendingBytes -= persistentRecordBytes(record)
	}
	return nil
}

// batchInsertRecords 批量插入记录
func (s *SQLiteStore) batchInsertRecords(records []PersistentRecord) error {
	if len(records) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), metricsWriteTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO request_records
		(metrics_key, base_url, key_mask, timestamp, success,
		 input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, api_type, model)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range records {
		success := 0
		if r.Success {
			success = 1
		}
		_, err := stmt.ExecContext(ctx,
			r.MetricsKey, r.BaseURL, r.KeyMask, r.Timestamp.Unix(), success,
			r.InputTokens, r.OutputTokens, r.CacheCreationTokens, r.CacheReadTokens, r.APIType, r.Model,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// LoadRecords 加载指定时间范围内的记录
func (s *SQLiteStore) LoadRecords(since time.Time, apiType string) ([]PersistentRecord, error) {
	rows, err := s.db.Query(`
		SELECT metrics_key, base_url, key_mask, timestamp, success,
		       input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, model
		FROM request_records
		WHERE timestamp >= ? AND api_type = ?
		ORDER BY timestamp ASC
	`, since.Unix(), apiType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []PersistentRecord
	for rows.Next() {
		var r PersistentRecord
		var ts int64
		var success int

		err := rows.Scan(
			&r.MetricsKey, &r.BaseURL, &r.KeyMask, &ts, &success,
			&r.InputTokens, &r.OutputTokens, &r.CacheCreationTokens, &r.CacheReadTokens, &r.Model,
		)
		if err != nil {
			return nil, err
		}

		r.Timestamp = time.Unix(ts, 0)
		r.Success = success == 1
		r.APIType = apiType
		records = append(records, r)
	}

	return records, rows.Err()
}

// CleanupOldRecords 清理过期数据
func (s *SQLiteStore) CleanupOldRecords(before time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), metricsWriteTimeout)
	defer cancel()
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM request_records WHERE timestamp < ?",
		before.Unix(),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// DeleteRecordsByMetricsKeys 按 metrics_key 和 api_type 批量删除记录
// apiType: 接口类型（messages/responses/gemini/chat），避免误删其他接口的数据
func (s *SQLiteStore) DeleteRecordsByMetricsKeys(metricsKeys []string, apiType string) (int64, error) {
	if len(metricsKeys) == 0 {
		return 0, nil
	}

	// 获取 flush 锁，确保删除期间不会有后台 flush 写入新记录
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	// 先刷新缓冲区，确保待删除的记录已写入数据库
	if err := s.flush(); err != nil {
		return 0, fmt.Errorf("删除前保存指标失败，未执行删除: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), metricsWriteTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// 分批删除，限制单条 SQL 的参数数量。
	const batchSize = 500
	var totalDeleted int64

	for i := 0; i < len(metricsKeys); i += batchSize {
		end := i + batchSize
		if end > len(metricsKeys) {
			end = len(metricsKeys)
		}
		batch := metricsKeys[i:end]

		// 构建 IN 子句的占位符
		placeholders := make([]string, len(batch))
		args := make([]interface{}, 0, len(batch)+1)
		args = append(args, apiType) // 第一个参数是 api_type
		for j, key := range batch {
			placeholders[j] = "?"
			args = append(args, key)
		}

		query := fmt.Sprintf(
			"DELETE FROM request_records WHERE api_type = ? AND metrics_key IN (%s)",
			strings.Join(placeholders, ","),
		)

		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, fmt.Errorf("删除指标批次 %d-%d 失败: %w", i, end, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		totalDeleted += affected
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return totalDeleted, nil
}

// flushLoop 定时刷新循环
func (s *SQLiteStore) flushLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	for {
		stopping := false
		select {
		case <-ticker.C:
		case <-s.flushWake:
		case <-s.stopCh:
			stopping = true
		}
		s.flushMu.Lock()
		err := s.flush()
		s.flushMu.Unlock()
		if stopping {
			s.flushErr = err
			return
		}
		if err != nil {
			// 写入失败后限速；持续请求只能合并一个唤醒，不能形成磁盘重试风暴。
			retry := time.NewTimer(time.Second)
			select {
			case <-retry.C:
			case <-s.stopCh:
				retry.Stop()
				s.flushMu.Lock()
				s.flushErr = s.flush()
				s.flushMu.Unlock()
				return
			}
		}
	}
}

// cleanupLoop 定期清理循环
func (s *SQLiteStore) cleanupLoop() {
	defer s.wg.Done()

	// 启动时先清理一次
	s.doCleanup()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.doCleanup()
		case <-s.stopCh:
			return
		}
	}
}

// doCleanup 执行清理
func (s *SQLiteStore) doCleanup() {
	retentionDays := s.retentionDays
	if s.retentionDaysProvider != nil {
		retentionDays = s.retentionDaysProvider()
	}
	if retentionDays < 3 || retentionDays > 30 {
		log.Printf("[SQLite-Cleanup] 指标保留期无效: %d，取消本次清理", retentionDays)
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	deleted, err := s.CleanupOldRecords(cutoff)
	if err != nil {
		log.Printf("[SQLite-Cleanup] 警告: 清理过期指标记录失败: %v", err)
	} else if deleted > 0 {
		log.Printf("[SQLite-Cleanup] 已清理 %d 条过期指标记录（超过 %d 天）", deleted, retentionDays)
	}
}

// Close 关闭存储
func (s *SQLiteStore) Close() error {
	s.closeOnce.Do(func() {
		// 标记为已关闭，阻止新记录
		s.bufferMu.Lock()
		s.closed = true
		s.bufferMu.Unlock()

		// 停止后台循环（flushLoop 会在退出前执行最后一次 flush）
		close(s.stopCh)
		s.wg.Wait()

		s.closeErr = errors.Join(s.flushErr, s.db.Close())
	})
	return s.closeErr
}

// GetRecordCount 获取记录总数（用于调试）
func (s *SQLiteStore) GetRecordCount() (int64, error) {
	var count int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM request_records").Scan(&count)
	return count, err
}

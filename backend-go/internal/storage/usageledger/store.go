package usageledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	_ "time/tzdata"

	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/storage/atomicfile"
	"insistanan/api-proxy/internal/storage/sqlitedb"
)

const schema = `
CREATE TABLE IF NOT EXISTS ledger_state (
  id INTEGER PRIMARY KEY CHECK (id = 1), enabled INTEGER NOT NULL DEFAULT 0,
  zone TEXT NOT NULL DEFAULT '', checkpoint INTEGER NOT NULL DEFAULT 0
);
INSERT INTO ledger_state(id) VALUES(1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS usage_facts (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, attempt_id TEXT NOT NULL UNIQUE,
  at_ms INTEGER NOT NULL, protocol TEXT NOT NULL, model TEXT NOT NULL, source TEXT NOT NULL,
  input INTEGER NOT NULL, output INTEGER NOT NULL, creation INTEGER NOT NULL, cache_read INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS usage_buckets (
  day TEXT NOT NULL, hour_ms INTEGER NOT NULL, model TEXT NOT NULL, source TEXT NOT NULL,
  input INTEGER NOT NULL, output INTEGER NOT NULL, creation INTEGER NOT NULL, cache_read INTEGER NOT NULL,
  attempts INTEGER NOT NULL, updated INTEGER NOT NULL,
  PRIMARY KEY(day, hour_ms, model, source)
);
CREATE INDEX IF NOT EXISTS usage_activity ON usage_facts(at_ms)
 WHERE source='upstream' AND input + output + creation + cache_read > 0;
CREATE TABLE IF NOT EXISTS usage_gaps (
  start_day TEXT NOT NULL, end_day TEXT NOT NULL, PRIMARY KEY(start_day, end_day)
);`

const dayFormat = "2006-01-02"
const batchSize = 256
const unavailableSuffix = ".unavailable"

type recording struct {
	start int64
	zone  *time.Location
}

type Store struct {
	db            *sql.DB
	active        atomic.Pointer[recording]
	enableMu      sync.Mutex
	queue         chan tokenusage.Fact
	wake          chan struct{}
	stop          chan struct{}
	done          chan struct{}
	closeOnce     sync.Once
	closeErr      error
	mu            sync.Mutex
	closed        bool
	losses        map[string]struct{}
	pendingLosses bool
	writeFailed   bool
}

// MarkUnavailable 保存网关在账本无法打开时继续运行的起点。
func MarkUnavailable(path string, now time.Time) error {
	marker := path + unavailableSuffix
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return atomicfile.Write(marker, strconv.AppendInt(nil, now.UnixMilli(), 10), atomicfile.Options{})
}

func Open(path string) (_ *Store, err error) {
	db, err := sqlitedb.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, db.Close())
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("初始化用量账本: %w", err)
	}
	s := &Store{db: db, queue: make(chan tokenusage.Fact, 2048), wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}), losses: make(map[string]struct{})}
	marker := path + unavailableSuffix
	var unavailableSince int64
	if raw, readErr := os.ReadFile(marker); readErr == nil {
		unavailableSince, err = strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("解析用量账本停写标记: %w", err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, fmt.Errorf("读取用量账本停写标记: %w", readErr)
	}
	if err := s.resume(ctx, time.Now(), unavailableSince); err != nil {
		return nil, fmt.Errorf("恢复用量账本: %w", err)
	}
	if unavailableSince != 0 {
		if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("[UsageLedger-Open] 清除已处理的停写标记失败: %v", err)
		}
	}
	go s.writeLoop()
	return s, nil
}

func (s *Store) resume(ctx context.Context, now time.Time, unavailableSince int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var start, checkpoint int64
	var zoneName string
	if err := tx.QueryRowContext(ctx, `SELECT enabled, zone, checkpoint FROM ledger_state WHERE id = 1`).Scan(&start, &zoneName, &checkpoint); err != nil {
		return err
	}
	if start == 0 {
		return tx.Commit()
	}
	zone, err := time.LoadLocation(zoneName)
	if err != nil {
		return err
	}
	if unavailableSince != 0 && (checkpoint == 0 || unavailableSince < checkpoint) {
		checkpoint = unavailableSince
	}
	if checkpoint != 0 {
		from := time.UnixMilli(min(max(start, checkpoint), now.UnixMilli())).In(zone).Format(dayFormat)
		to := now.In(zone).Format(dayFormat)
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_gaps VALUES(?, ?) ON CONFLICT DO NOTHING`, from, to); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ledger_state SET checkpoint = ? WHERE id = 1`, now.UnixMilli()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.active.Store(&recording{start: start, zone: zone})
	return nil
}

// Enable 固定实例的统计时区与起点。重复请求返回原设置。
func (s *Store) Enable(ctx context.Context, zone *time.Location, at time.Time) error {
	s.enableMu.Lock()
	defer s.enableMu.Unlock()
	if s.active.Load() != nil {
		return nil
	}
	start := at.UnixMilli()
	if _, err := s.db.ExecContext(ctx, `UPDATE ledger_state SET enabled = ?, zone = ?, checkpoint = ? WHERE id = 1`, start, zone.String(), start); err != nil {
		return fmt.Errorf("启用用量账本: %w", err)
	}
	s.active.Store(&recording{start: start, zone: zone})
	return nil
}

func (s *Store) Enabled() bool { return s.active.Load() != nil }

// Record 只占用有限的缓冲空间。拥堵日期会作为覆盖缺口落盘。
func (s *Store) Record(fact tokenusage.Fact) {
	active := s.active.Load()
	if active == nil || fact.At.UnixMilli() < active.start {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.queue <- fact:
		if len(s.queue) == batchSize {
			select {
			case s.wake <- struct{}{}:
			default:
			}
		}
	default:
		s.losses[fact.At.In(active.zone).Format(dayFormat)] = struct{}{}
	}
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.stop)
		s.mu.Unlock()
		<-s.done
		s.closeErr = errors.Join(s.closeErr, s.db.Close())
	})
	return s.closeErr
}

func (s *Store) writeLoop() {
	defer close(s.done)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	lastCheckpoint := time.Now()
	var batch []tokenusage.Fact
	var gaps map[string]struct{}
	stopping := false
	draining := false
	retrying := false
	for {
		if !stopping {
			if draining {
				select {
				case <-s.stop:
					stopping = true
				default:
				}
			} else {
				wake := (<-chan struct{})(s.wake)
				if retrying {
					wake = nil
				}
				select {
				case <-ticker.C:
				case <-wake:
				case <-s.stop:
					stopping = true
				}
			}
		}
		active := s.active.Load()
		if active == nil {
			if stopping {
				return
			}
			continue
		}
		if len(batch) == 0 && len(gaps) == 0 {
			s.mu.Lock()
			for len(s.queue) > 0 && len(batch) < batchSize {
				batch = append(batch, <-s.queue)
			}
			gaps, s.losses = s.losses, make(map[string]struct{})
			s.pendingLosses = len(gaps) > 0
			s.mu.Unlock()
		}
		s.mu.Lock()
		now := time.Now()
		drained := len(s.queue) == 0 && len(s.losses) == 0
		s.mu.Unlock()
		checkpoint := drained && (stopping || now.Sub(lastCheckpoint) >= time.Minute)
		if len(batch) == 0 && len(gaps) == 0 && !checkpoint {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := s.flush(ctx, active.zone, batch, gaps, now, checkpoint, stopping && drained)
		cancel()
		s.mu.Lock()
		wasFailed := s.writeFailed
		s.writeFailed = err != nil
		if err == nil {
			s.pendingLosses = false
		}
		s.mu.Unlock()
		if err != nil {
			draining, retrying = false, true
			if !wasFailed {
				log.Printf("[UsageLedger-Write] 用量账本写入失败，待重试: %v", err)
			}
			if stopping {
				s.closeErr = fmt.Errorf("关闭前排空用量账本: %w", err)
				return
			}
			continue
		}
		draining, retrying = !drained, false
		batch, gaps = batch[:0], nil
		if checkpoint {
			lastCheckpoint = now
		}
		if stopping && drained {
			return
		}
	}
}

func (s *Store) flush(ctx context.Context, zone *time.Location, batch []tokenusage.Fact, gaps map[string]struct{}, now time.Time, checkpoint, clean bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, fact := range batch {
		result, err := tx.ExecContext(ctx, `INSERT INTO usage_facts(attempt_id, at_ms, protocol, model, source, input, output, creation, cache_read)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(attempt_id) DO NOTHING`, fact.AttemptID, fact.At.UnixMilli(), fact.Protocol, fact.Model, fact.Source,
			fact.Input, fact.Output, fact.CacheCreation, fact.CacheRead)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			continue
		}
		local := fact.At.In(zone)
		hour := local.Add(-time.Duration(local.Minute())*time.Minute - time.Duration(local.Second())*time.Second - time.Duration(local.Nanosecond()))
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_buckets VALUES(?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
ON CONFLICT DO UPDATE SET input = input + excluded.input, output = output + excluded.output,
creation = creation + excluded.creation, cache_read = cache_read + excluded.cache_read,
attempts = attempts + 1, updated = excluded.updated`, local.Format(dayFormat), hour.UnixMilli(), fact.Model, fact.Source,
			fact.Input, fact.Output, fact.CacheCreation, fact.CacheRead, now.UnixMilli()); err != nil {
			return err
		}
	}
	for day := range gaps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_gaps VALUES(?, ?) ON CONFLICT DO NOTHING`, day, day); err != nil {
			return err
		}
	}
	if checkpoint {
		value := now.UnixMilli()
		if clean {
			value = 0
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ledger_state SET checkpoint = ? WHERE id = 1`, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

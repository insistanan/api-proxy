package logger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCleanupDeletesOlderThanYesterday(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "logs.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	now := time.Now()
	keepToday := now
	keepYesterday := RetentionCutoff(now).Add(time.Hour)
	dropOld := RetentionCutoff(now).Add(-time.Hour)

	store.RecordAppLog(AppLog{UnixMilli: dropOld.UnixMilli(), Message: "old"})
	store.RecordAppLog(AppLog{UnixMilli: keepYesterday.UnixMilli(), Message: "yesterday"})
	store.RecordAppLog(AppLog{UnixMilli: keepToday.UnixMilli(), Message: "today"})
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.cleanupExpired(); err != nil {
		t.Fatal(err)
	}

	logs, err := store.QueryAppLogs(context.Background(), QueryOptions{
		From: dropOld.Add(-time.Hour).UnixMilli(),
		To:   keepToday.Add(time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(logs))
	for _, entry := range logs {
		got = append(got, entry.Message)
	}
	if len(got) != 2 || got[0] != "yesterday" || got[1] != "today" {
		t.Fatalf("got %v, want [yesterday today]", got)
	}
}

// waitInitialCleanup 等待 Open 启动的后台清理跑完首轮。
// 首轮会写入清理跳过窗口，测试若与它并发，手工清理会因跳过窗口而拿不到真实结果。
func waitInitialCleanup(t *testing.T, store *Store) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		store.statusMu.Lock()
		done := store.maintenance.LastCleanupAt != ""
		store.statusMu.Unlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("等待后台清理首轮完成超时")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCleanupFailureDoesNotBlockOtherTables(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	waitInitialCleanup(t, store)

	old := RetentionCutoff(time.Now()).Add(-time.Hour)
	if err := store.RecordAppLog(AppLog{UnixMilli: old.UnixMilli(), Message: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTraffic(TrafficLog{
		UnixMilli: old.UnixMilli(),
		RequestID: "old",
		Phase:     PhaseClientRequest,
		Body:      strings.Repeat("x", 5000),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DROP TABLE app_logs`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.cleanupExpired(); err == nil {
		t.Fatal("app_logs 不可清理时必须返回错误")
	}
	var bodies int
	if err := store.readDB.QueryRow(`SELECT COUNT(*) FROM traffic_bodies`).Scan(&bodies); err != nil {
		t.Fatal(err)
	}
	if bodies != 0 {
		t.Fatal("一张表失败后，其他表的过期正文仍应被清理")
	}
	if _, _, err := store.cleanupExpired(); err != nil {
		t.Fatalf("失败表在暂停期内不应再次报错: %v", err)
	}
}

func TestNextCleanupBatchAvoidsRetryingASizeThatTimedOut(t *testing.T) {
	next, ceiling, more, giveUp := nextCleanupBatch(cleanupBatchSize, 0, 0, cleanupTimeout, true)
	if giveUp || !more || next != cleanupShrinkBatch || ceiling != cleanupBatchSize {
		t.Fatalf("大批量超时后应降到 %d 并记住上限 %d，得到 next=%d ceiling=%d more=%v giveUp=%v",
			cleanupShrinkBatch, cleanupBatchSize, next, ceiling, more, giveUp)
	}

	next, ceiling, more, giveUp = nextCleanupBatch(next, ceiling, int64(next), cleanupTimeout/5, false)
	if giveUp || !more || next != cleanupShrinkBatch*2 || ceiling != cleanupBatchSize {
		t.Fatalf("远快于预算时应放大，得到 next=%d ceiling=%d more=%v giveUp=%v", next, ceiling, more, giveUp)
	}

	timedOutAt := next
	next, ceiling, more, giveUp = nextCleanupBatch(timedOutAt, ceiling, 0, cleanupTimeout, true)
	if giveUp || !more || next != cleanupShrinkBatch || ceiling != timedOutAt {
		t.Fatalf("放大后再次超时应退回并压低上限，得到 next=%d ceiling=%d more=%v giveUp=%v", next, ceiling, more, giveUp)
	}

	next, ceiling, more, giveUp = nextCleanupBatch(next, ceiling, int64(cleanupShrinkBatch), time.Millisecond, false)
	if giveUp || !more || next != cleanupShrinkBatch || ceiling != timedOutAt {
		t.Fatalf("不应涨回刚超时的批量，得到 next=%d ceiling=%d more=%v giveUp=%v", next, ceiling, more, giveUp)
	}

	keptCeiling := ceiling
	next, ceiling, more, giveUp = nextCleanupBatch(1, keptCeiling, 0, cleanupTimeout, true)
	if !giveUp || more || next != 1 || ceiling != keptCeiling {
		t.Fatalf("单行超时必须失败且保留上限，得到 next=%d ceiling=%d more=%v giveUp=%v", next, ceiling, more, giveUp)
	}

	next, ceiling, more, giveUp = nextCleanupBatch(cleanupShrinkBatch, 0, 3, time.Millisecond, false)
	if more || giveUp || next != cleanupShrinkBatch {
		t.Fatalf("未删满一批表示这张表已追上，得到 next=%d more=%v giveUp=%v", next, more, giveUp)
	}

	next, _, more, giveUp = nextCleanupBatch(cleanupShrinkBatch, 0, int64(cleanupShrinkBatch), cleanupTimeout/2, false)
	if giveUp || !more || next != cleanupShrinkBatch {
		t.Fatalf("接近预算的满批不应放大，得到 next=%d more=%v giveUp=%v", next, more, giveUp)
	}
}

func TestListSystemLogSummariesGroupsByRequestIDWithoutBody(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "logs.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	now := time.Now()
	older := now.Add(-time.Minute)
	store.RecordTraffic(TrafficLog{
		UnixMilli: older.UnixMilli(),
		RequestID: "req-old",
		Phase:     PhaseClientRequest,
		APIType:   "Chat",
		Body:      `{"secret":"should-not-appear-in-list"}`,
	})
	store.RecordTraffic(TrafficLog{
		UnixMilli: now.UnixMilli(),
		RequestID: "req-new",
		Phase:     PhaseClientRequest,
		APIType:   "Messages",
		Body:      `{"messages":[{"role":"user","content":"hi"}]}`,
	})
	store.RecordTraffic(TrafficLog{
		UnixMilli: now.Add(time.Millisecond).UnixMilli(),
		RequestID: "req-new",
		Phase:     PhaseUpstreamRequest,
		APIType:   "Messages",
		Body:      `{"model":"claude"}`,
	})
	store.RecordTraffic(TrafficLog{
		UnixMilli:  now.Add(2 * time.Millisecond).UnixMilli(),
		RequestID:  "req-new",
		Phase:      PhaseUpstreamResponse,
		APIType:    "Messages",
		StatusCode: 200,
		Stream:     true,
		Body:       `{"ok":true}`,
	})
	store.RecordRequest(RequestLogRecord{
		UnixMilli: now.UnixMilli(),
		RequestID: "req-new",
		APIType:   "messages",
		Payload:   []byte(`{"channelName":"prod","resolvedModel":"claude-sonnet","status":"completed"}`),
	})
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	summaries, err := store.ListSystemLogSummaries(context.Background(), SystemLogListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("got %d summaries, want 2", len(summaries))
	}
	if summaries[0].RequestID != "req-new" {
		t.Fatalf("first requestId=%s, want req-new", summaries[0].RequestID)
	}
	if summaries[0].StatusCode != 200 || !summaries[0].Stream {
		t.Fatalf("summary=%+v, want status 200 stream", summaries[0])
	}
	if summaries[0].ChannelName != "prod" || summaries[0].Model != "claude-sonnet" {
		t.Fatalf("joined meta=%+v", summaries[0])
	}
	if len(summaries[0].MissingPhases) != 0 {
		t.Fatalf("missingPhases=%v, want empty", summaries[0].MissingPhases)
	}
	if len(summaries[1].MissingPhases) != 2 {
		t.Fatalf("old missingPhases=%v, want upstream request+response", summaries[1].MissingPhases)
	}

	filtered, err := store.ListSystemLogSummaries(context.Background(), SystemLogListOptions{
		APIType: "messages",
		Limit:   10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].RequestID != "req-new" {
		t.Fatalf("filtered=%+v, want only req-new", filtered)
	}

	detail, err := store.GetSystemLogDetail(context.Background(), "req-new")
	if err != nil {
		t.Fatal(err)
	}
	if detail == nil || len(detail.TrafficLogs) != 3 {
		t.Fatalf("detail=%+v, want 3 traffic rows", detail)
	}
	if detail.TrafficLogs[0].Body == "" {
		t.Fatal("detail should include body")
	}
}

func TestLogBurstPersistsEveryAcceptedEntry(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const workers = 4
	const perWorker = 4096
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := store.RecordAppLog(AppLog{Message: fmt.Sprintf("worker=%d entry=%d", worker, i)}); err != nil {
					errors <- err
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	var count, distinct int
	if err := store.readDB.QueryRow("SELECT COUNT(*), COUNT(DISTINCT message) FROM app_logs").Scan(&count, &distinct); err != nil {
		t.Fatal(err)
	}
	if count != workers*perWorker || distinct != count {
		t.Fatalf("持久化 %d 条、去重 %d 条，预期 %d 条", count, distinct, workers*perWorker)
	}
}

func TestLogReadersDoNotBlockWriterConnection(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := 0; i < 4; i++ {
		conn, err := store.readDB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
	}
	if err := store.RecordAppLog(AppLog{Message: "读取连接全部占用时仍能落库"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- store.Flush() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("日志写入被读取连接阻塞")
	}
}

func TestLogWriteFailureReachesFlushAndClose(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.db.Exec("DROP TABLE app_logs"); err != nil {
		t.Fatal(err)
	}
	_ = store.RecordAppLog(AppLog{Message: "不能假报落库成功"})
	if err := store.Flush(); err == nil {
		t.Fatal("写入失败后 Flush 必须返回错误")
	}
	if err := store.Close(); err == nil {
		t.Fatal("关闭时必须报告未落库日志")
	}
	if err := store.Close(); err == nil {
		t.Fatal("重复关闭必须保留首次关闭错误")
	}
}

func TestLogCloseDrainsAndRejectsLateWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := store.RecordAppLog(AppLog{Message: fmt.Sprintf("entry=%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAppLog(AppLog{Message: "关闭后的日志"}); err == nil {
		t.Fatal("关闭后必须拒绝写入")
	}
	if err := store.Flush(); err == nil {
		t.Fatal("关闭后的 Flush 必须立即返回错误")
	}
	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var count int
	if err := reader.readDB.QueryRow("SELECT COUNT(*) FROM app_logs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("关闭只保存 %d 条日志，预期 100 条", count)
	}
}

func TestLineWriterBoundsPartialLineWithoutLosingText(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	writer := newLineWriter(store)
	message := strings.Repeat("x ", maxAppLineBytes+17)
	if n, err := writer.Write([]byte(message)); err != nil || n != len(message) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if len(writer.buf) >= maxAppLineBytes {
		t.Fatal("无换行输入未限制暂存长度")
	}
	if _, err := writer.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	logs, err := store.QueryAppLogs(context.Background(), QueryOptions{To: time.Now().Add(time.Hour).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	var restored strings.Builder
	for _, entry := range logs {
		restored.WriteString(entry.Message)
	}
	if restored.String() != message {
		t.Fatal("超长日志分段后文本缺失或顺序改变")
	}
	if writer.buf != nil {
		t.Fatal("完整行写入后仍持有暂存缓冲")
	}
}

func TestLogWriteRecoversAndRetriesAcceptedEntry(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.db.Exec("ALTER TABLE app_logs RENAME TO unavailable_app_logs"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAppLog(AppLog{Message: "存储恢复后应保留"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err == nil {
		t.Fatal("数据库不可写时必须报告实际失败")
	}
	if _, err := store.db.Exec("ALTER TABLE unavailable_app_logs RENAME TO app_logs"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := store.Flush(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("数据库恢复后未继续落库")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var count int
	if err := store.readDB.QueryRow("SELECT COUNT(*) FROM app_logs WHERE message = ?", "存储恢复后应保留").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("原先已接收的记录应恢复落库一次，实际 %d 次", count)
	}
	status, err := store.GetStorageStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Queue.WriteFailures == 0 || status.Queue.WriteRecoveries != 1 || status.Queue.PendingEntries != 0 || status.Queue.LastRecoveryAt == "" {
		t.Fatalf("故障恢复状态不完整: %+v", status.Queue)
	}
}

// 写入队列的准入以条数和字节为配额，且处理中的批次同样占用配额。
func TestWriteQueueAdmissionAndFlushCancellation(t *testing.T) {
	t.Run("拥堵明确超时且保留已接收记录", func(t *testing.T) {
		store := &Store{queue: make(chan queuedWrite, 1), spaceReady: make(chan struct{}), flushWaiters: make(map[chan error]struct{})}
		first := queuedWrite{kind: writeApp, app: AppLog{Message: "已接收"}}
		if err := store.enqueue(first); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		if err := store.enqueueContext(ctx, queuedWrite{kind: writeApp, app: AppLog{Message: "未接收"}}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("拥堵必须明确返回超时，实际 %v", err)
		}
		if store.queueStatus.AcceptedWrites != 1 || store.queueStatus.RejectedWrites != 1 || store.queueStatus.PendingEntries != 1 || store.queueStatus.LastRejectedError == "" {
			t.Fatalf("队列状态错误: %+v", store.queueStatus)
		}
		accepted := <-store.queue
		if accepted.app.Message != "已接收" {
			t.Fatal("拥堵不能替换已接收记录")
		}
		store.completeBatch([]queuedWrite{accepted}, nil)
		if store.queueBytes != 0 || store.queueStatus.PendingEntries != 0 {
			t.Fatal("完成写入后必须释放队列配额")
		}
	})

	t.Run("处理中记录占用条数配额", func(t *testing.T) {
		store := &Store{queue: make(chan queuedWrite, 1), spaceReady: make(chan struct{}), flushWaiters: make(map[chan error]struct{})}
		if err := store.enqueue(queuedWrite{kind: writeApp, app: AppLog{Message: "处理中"}}); err != nil {
			t.Fatal(err)
		}
		processing := <-store.queue
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		if err := store.enqueueContext(ctx, queuedWrite{kind: writeApp}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("处理中记录也应占用条数配额: %v", err)
		}
		store.completeBatch([]queuedWrite{processing}, nil)
		if err := store.enqueue(queuedWrite{kind: writeApp}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("取消 Flush 不泄漏等待者也不计为拒收", func(t *testing.T) {
		store := &Store{queue: make(chan queuedWrite, 1), spaceReady: make(chan struct{}), flushWaiters: make(map[chan error]struct{})}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		if err := store.FlushContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Flush 应支持取消等待: %v", err)
		}
		if len(store.flushWaiters) != 0 {
			t.Fatal("取消后的 Flush 不应泄漏等待者")
		}
		barrier := <-store.queue
		store.completeBatch([]queuedWrite{barrier}, nil)
		if store.queueBytes != 0 || store.queueStatus.RejectedWrites != 0 {
			t.Fatal("取消 Flush 只中止等待，不应计为拒收日志")
		}
	})
}

type countedLogReadCloser struct {
	io.ReadCloser
	closes atomic.Int32
}

func (c *countedLogReadCloser) Close() error {
	c.closes.Add(1)
	return c.ReadCloser.Close()
}

func TestResponseCaptureConcurrentCloseUnblocksReadOnce(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	inner := &countedLogReadCloser{ReadCloser: reader}
	capture := CaptureResponseBody(inner, TrafficLog{})
	readResult := make(chan error, 1)
	go func() {
		_, err := capture.Read(make([]byte, 16))
		readResult <- err
	}()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = capture.Close()
		}()
	}
	select {
	case err := <-readResult:
		if err == nil {
			t.Fatal("上游关闭后读取应返回错误")
		}
	case <-time.After(time.Second):
		t.Fatal("Close 未打断阻塞的上游读取")
	}
	wg.Wait()
	if closes := inner.closes.Load(); closes != 1 {
		t.Fatalf("底层响应体关闭 %d 次，预期 1 次", closes)
	}
}

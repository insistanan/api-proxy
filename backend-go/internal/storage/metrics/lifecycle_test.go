package metrics

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 并发 Stop 返回时循环必须已经退出，重复调用也不能重复关闭 channel。
func TestMetricsBackgroundStopConcurrent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		create func() (func(), <-chan struct{})
	}{
		{"默认指标", func() (func(), <-chan struct{}) { m := NewMetricsManager(); return m.Stop, m.doneCh }},
		{"配置指标", func() (func(), <-chan struct{}) { m := NewMetricsManagerWithConfig(5, .5); return m.Stop, m.doneCh }},
		{"持久化指标", func() (func(), <-chan struct{}) {
			m := NewMetricsManagerWithPersistence(5, .5, nil, "chat")
			return m.Stop, m.doneCh
		}},
		{"性能画像", func() (func(), <-chan struct{}) { m := NewProfileManager(); return m.Stop, m.doneCh }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stop, done := tc.create()
			t.Cleanup(stop)
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); stop() }()
			}
			completed := make(chan struct{})
			go func() { wg.Wait(); close(completed) }()
			select {
			case <-completed:
			case <-time.After(5 * time.Second):
				t.Fatal("并发停止未结束")
			}
			select {
			case <-done:
			default:
				t.Fatal("Stop 返回后后台循环尚未退出")
			}
		})
	}
}

func TestMetricsStoreConcurrentCloseFlushesAcceptedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 7})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	store.batchSize = 1
	record := func(key string) PersistentRecord {
		return PersistentRecord{MetricsKey: key, BaseURL: "https://example.com", KeyMask: "fixture", Timestamp: time.Now(), APIType: "messages", Success: true}
	}
	for i := 0; i < 32; i++ {
		store.AddRecord(record(fmt.Sprintf("before-%d", i)))
	}

	start := make(chan struct{})
	closeErrors := make(chan error, 8)
	var writers sync.WaitGroup
	for i := 0; i < 8; i++ {
		writers.Add(1)
		go func(worker int) {
			defer writers.Done()
			<-start
			for j := 0; j < 32; j++ {
				store.AddRecord(record(fmt.Sprintf("racing-%d-%d", worker, j)))
			}
		}(i)
		go func() { <-start; closeErrors <- store.Close() }()
	}
	close(start)
	writers.Wait()
	for i := 0; i < 8; i++ {
		require.NoError(t, <-closeErrors)
	}

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM request_records WHERE metrics_key LIKE 'before-%'").Scan(&count))
	require.Equal(t, 32, count, "关闭开始前接受的记录必须全部落盘")
}

package config

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigCloseWaitsForRegisteredWorker(t *testing.T) {
	cm := &ConfigManager{stopChan: make(chan struct{})}
	cm.workers.Add(1)
	stopping := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseWorker)
	// 模拟已经开始、收到停止信号后仍需完成的维护操作。
	go func() { defer cm.workers.Done(); <-cm.stopChan; close(stopping); <-release }()
	returned := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { returned <- cm.Close() }()
	}
	select {
	case <-stopping:
	case <-time.After(5 * time.Second):
		t.Fatal("维护任务未收到停止信号")
	}
	select {
	case <-returned:
		t.Fatal("Close 未等待维护任务完成")
	case <-time.After(20 * time.Millisecond):
	}
	releaseWorker()
	for i := 0; i < 8; i++ {
		select {
		case err := <-returned:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Close 未结束")
		}
	}
}

func TestConfigCloseStopsRealWatcher(t *testing.T) {
	cm, err := NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- cm.Close() }()
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		require.NoError(t, <-errors)
	}
	// watcher 底层事件 channel 已关闭，且不会再有定时回调重新发布配置。
	_, open := <-cm.watcher.Events
	require.False(t, open)
	cm.workers.Wait()
}

package syncstate

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

func TestCanceledWriterReleasesWaitingReaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var lock Mutex
		lock.RLock()
		ctx, cancel := context.WithCancel(context.Background())
		writer := make(chan error, 1)
		go func() { writer <- lock.LockContext(ctx) }()
		synctest.Wait()
		reader := make(chan struct{})
		go func() { lock.RLock(); lock.RUnlock(); close(reader) }()
		synctest.Wait()
		select {
		case <-reader:
			t.Fatal("新读者不能越过等待中的写者")
		default:
		}
		cancel()
		synctest.Wait()
		require.ErrorIs(t, <-writer, context.Canceled)
		<-reader
		lock.RUnlock()
		require.NoError(t, lock.LockContext(context.Background()))
		lock.Unlock()
	})
}

func TestConcurrentReadersAndWritersProtectState(t *testing.T) {
	var lock Mutex
	var workers sync.WaitGroup
	value := 0
	for worker := 0; worker < 8; worker++ {
		workers.Go(func() {
			for step := 0; step < 100; step++ {
				lock.Lock()
				value++
				lock.Unlock()
				lock.RLock()
				observed := value
				lock.RUnlock()
				if observed <= 0 {
					t.Error("读到未发布状态")
				}
			}
		})
	}
	workers.Wait()
	require.Equal(t, 800, value)
}

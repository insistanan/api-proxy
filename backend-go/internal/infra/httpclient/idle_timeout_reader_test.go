package httpclient

import (
	"io"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIdleTimeoutReader_TriggerOnHang(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader, writer := io.Pipe()
		defer writer.Close()
		wrapped := NewIdleTimeoutReader(reader, time.Second, "Test")
		defer wrapped.Close()
		_, err := wrapped.Read(make([]byte, 1024))
		require.ErrorIs(t, err, ErrStreamIdleTimeout)
		synctest.Wait() // Close 解阻塞底层 Read，所有读取 goroutine 都能结束。
	})
}

// lateWriteReader 模拟 Close 无法立即终止读取，但可由测试显式放行并等待结束。
type lateWriteReader struct {
	release chan struct{}
	done    chan struct{}
	once    sync.Once
}

func (r *lateWriteReader) Read(p []byte) (int, error) {
	<-r.release
	p[0] = 0x41
	close(r.done)
	return 1, nil
}

func (r *lateWriteReader) Close() error { return nil }

func (r *lateWriteReader) unblock() { r.once.Do(func() { close(r.release) }) }

func TestIdleTimeoutReader_NoWriteToCallerBufferAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := &lateWriteReader{release: make(chan struct{}), done: make(chan struct{})}
		defer inner.unblock()
		reader := NewIdleTimeoutReader(inner, time.Second, "Test")
		p := make([]byte, 16)
		_, err := reader.Read(p)
		require.ErrorIs(t, err, ErrStreamIdleTimeout)
		p[0], p[1] = 0, 1
		inner.unblock()
		<-inner.done
		synctest.Wait()
		require.Equal(t, byte(0), p[0], "迟到写入不得污染调用方缓冲")
		require.Equal(t, byte(1), p[1])
		_, err = reader.Read(p)
		require.ErrorIs(t, err, ErrStreamIdleTimeout)
	})
}

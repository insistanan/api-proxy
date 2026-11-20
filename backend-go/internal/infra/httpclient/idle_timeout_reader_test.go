package httpclient

import (
	"errors"
	"io"
	"testing"
	"time"
)

type blockingReader struct{}

func (b *blockingReader) Read(p []byte) (int, error) {
	select {} // 永久阻塞，模拟上游挂起
}
func (b *blockingReader) Close() error { return nil }

type oneShotReader struct {
	read  bool
	inner io.Reader
}

func (o *oneShotReader) Read(p []byte) (int, error) {
	if o.read {
		return 0, io.EOF
	}
	o.read = true
	return o.inner.Read(p)
}
func (o *oneShotReader) Close() error { return nil }

// TestIdleTimeoutReader_TriggerOnHang 验证：上游挂起（Read 永久阻塞）时触发空闲超时。
func TestIdleTimeoutReader_TriggerOnHang(t *testing.T) {
	r := NewIdleTimeoutReader(&blockingReader{}, 100*time.Millisecond, "Test")
	buf := make([]byte, 1024)
	_, err := r.Read(buf)
	if !errors.Is(err, ErrStreamIdleTimeout) {
		t.Fatalf("期望 ErrStreamIdleTimeout，得到: %v", err)
	}
}

// lateWriteReader 模拟"Close 未能立即解阻塞"的病态底层：
// Read 永久阻塞到放行后才写入。Close 对 Read 无解阻塞作用。
type lateWriteReader struct {
	release chan struct{}
}

func (l *lateWriteReader) Read(p []byte) (int, error) {
	<-l.release
	p[0] = 0x41
	return 1, nil
}
func (l *lateWriteReader) Close() error { return nil }

// TestIdleTimeoutReader_NoWriteToCallerBufferAfterTimeout 验证：空闲超时返回后，
// 调用方复用自己的缓冲 p，迟到的底层写入不得落在 p 上（旧实现为数据竞争，
// -race 下必报）；且超时为终态，后续 Read 直接返回同一错误，不再起读底层。
func TestIdleTimeoutReader_NoWriteToCallerBufferAfterTimeout(t *testing.T) {
	release := make(chan struct{})
	r := NewIdleTimeoutReader(&lateWriteReader{release: release}, 50*time.Millisecond, "Test")
	p := make([]byte, 16)
	_, err := r.Read(p)
	if !errors.Is(err, ErrStreamIdleTimeout) {
		t.Fatalf("期望 ErrStreamIdleTimeout，得到: %v", err)
	}

	// 模拟 io.Copy / bufio.Scanner 在 Read 返回后复用缓冲的常态行为。
	p[0] = 0x00
	p[1] = 0x01
	close(release)
	time.Sleep(100 * time.Millisecond) // 给迟到的底层读留出写入窗口

	if _, err := r.Read(p); !errors.Is(err, ErrStreamIdleTimeout) {
		t.Fatalf("超时后后续 Read 应返回 ErrStreamIdleTimeout，得到: %v", err)
	}
}

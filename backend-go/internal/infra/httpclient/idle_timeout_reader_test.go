package httpclient

import (
	"errors"
	"io"
	"strings"
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

// TestIdleTimeoutReader_PassThrough 验证：正常数据立即返回，不受影响。
func TestIdleTimeoutReader_PassThrough(t *testing.T) {
	r := NewIdleTimeoutReader(&oneShotReader{inner: strings.NewReader("hello")}, 5*time.Second, "Test")
	buf := make([]byte, 1024)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("期望无错误，得到: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("期望 'hello'，得到: %q", string(buf[:n]))
	}
}

// TestIdleTimeoutReader_Disabled 验证：idleTimeout <= 0 时直接透传（不包装）。
func TestIdleTimeoutReader_Disabled(t *testing.T) {
	inner := &oneShotReader{inner: strings.NewReader("x")}
	r := NewIdleTimeoutReader(inner, 0, "Test")
	if r != io.ReadCloser(inner) {
		t.Fatal("idleTimeout<=0 时应直接返回原 reader")
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

// TestIdleTimeoutReader_SequencePreserved 验证：多轮读取下数据完整、顺序不变
// （私有缓冲 copy 路径不丢字节），EOF 语义与底层一致。
func TestIdleTimeoutReader_SequencePreserved(t *testing.T) {
	r := NewIdleTimeoutReader(io.NopCloser(strings.NewReader("hello world, 流式数据完整性")), 5*time.Second, "Test")
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("期望无错误，得到: %v", err)
	}
	if string(got) != "hello world, 流式数据完整性" {
		t.Fatalf("数据不完整或顺序错乱: %q", string(got))
	}
}

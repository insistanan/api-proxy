package proxycore

import (
	"context"
	"testing"
	"time"
)

type detachedContextKeyType struct{}

var detachedContextKey detachedContextKeyType

func TestDetachClientCancellationKeepsValuesButIgnoresCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), detachedContextKey, "request-1"))
	detached := DetachClientCancellation(parent)
	cancel()
	select {
	case <-detached.Done():
		t.Fatal("客户端取消不应中止已分离的上游读取")
	case <-time.After(20 * time.Millisecond):
	}
	if detached.Err() != nil {
		t.Fatalf("分离后的 context 错误 = %v", detached.Err())
	}
	if detached.Value(detachedContextKey) != "request-1" {
		t.Fatal("分离后的 context 丢失了请求值")
	}
}

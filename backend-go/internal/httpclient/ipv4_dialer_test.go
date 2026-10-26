package httpclient

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestDialerConnectsToIPv4Loopback(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法监听 tcp4: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	d := &ipv4Dialer{d: &net.Dialer{Timeout: 2 * time.Second}}
	conn, err := d.DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("DialContext 失败: %v", err)
	}
	_ = conn.Close()
}

func TestDialerLiteralIPv6Unchanged(t *testing.T) {
	// IPv6 字面量交由标准拨号器处理（此处只验证不 panic）。
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	d := &ipv4Dialer{d: &net.Dialer{Timeout: 500 * time.Millisecond}}
	// 连接不存在的地址，期望返回错误而非 panic/类型错误。
	_, err := d.DialContext(ctx, "tcp", "[::1]:1")
	if err == nil {
		t.Skip("本机 IPv6 回环可用，跳过（仅验证逻辑路径不崩溃）")
	}
}

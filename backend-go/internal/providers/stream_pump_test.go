package providers

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// TestStreamPumpClosesBothChannels 收尾约定锚点：pump goroutine 退出时
// 双通道必须关闭（消费方依赖 eventChan 关闭判定流结束）。
func TestStreamPumpClosesBothChannels(t *testing.T) {
	pump := newStreamPump(context.Background())
	go func() {
		defer close(pump.eventChan)
		defer close(pump.errChan)
		pump.send("event")
	}()

	for event := range pump.eventChan {
		if event != "event" {
			t.Fatalf("event = %q, want %q", event, "event")
		}
	}
	select {
	case _, ok := <-pump.errChan:
		if ok {
			t.Fatalf("errChan 应已关闭")
		}
	case <-time.After(time.Second):
		t.Fatalf("errChan 未关闭")
	}
}

// TestStreamPumpFailBeforeClose 锚点：close(errChan) 不清除已缓冲错误，
// 消费方在通道关闭后仍能先取到 fail 入队的错误——这是恢复
// openai/gemini/responses_messages 的 defer close(errChan) 的前提。
func TestStreamPumpFailBeforeClose(t *testing.T) {
	pump := newStreamPump(context.Background())
	wantErr := errors.New("upstream boom")
	go func() {
		defer close(pump.eventChan)
		defer close(pump.errChan)
		pump.fail(wantErr)
	}()

	<-pump.eventChan // 等流结束
	select {
	case err, ok := <-pump.errChan:
		if !ok || !errors.Is(err, wantErr) {
			t.Fatalf("errChan 取值 = (%v, %v), want (%v, true)", err, ok, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatalf("errChan 关闭前应能取到缓冲错误")
	}
}

// TestStreamPumpSendStopsOnContextCancel 断连中止锚点：ctx 取消后
// send 立即返回 false，pump goroutine 不阻塞在满缓冲的 channel send 上。
func TestStreamPumpSendStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pump := newStreamPump(ctx)
	cancel()

	done := make(chan bool, 1)
	go func() {
		// 缓冲 100，连发 200 个：若 send 不感知取消将永久阻塞。
		for i := 0; i < 200; i++ {
			if !pump.send("event") {
				break
			}
		}
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("ctx 取消后 send 仍阻塞")
	}
}

// TestStreamPumpNewScannerBuffer 验证 scanner 缓冲约定（64KB 初始 / 1MB 上限）。
func TestStreamPumpNewScannerBuffer(t *testing.T) {
	pump := newStreamPump(context.Background())
	scanner := pump.newScanner(strings.NewReader("data: x\n"))

	var line string
	for scanner.Scan() {
		line = scanner.Text()
	}
	if line != "data: x" {
		t.Fatalf("line = %q, want %q", line, "data: x")
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatalf("scanner err = %v", err)
	}
}

// 未完成的上游断连必须上报，不能仅凭错误字符串把它当成客户端取消。
func TestStreamPumpFinishRead(t *testing.T) {
	resetErr := errors.New("read tcp: connection reset by peer")
	pump := newStreamPump(context.Background())
	if pump.finishRead(false, resetErr) {
		t.Fatal("未完成的重置不能当成成功")
	}
	if err := <-pump.errChan; !errors.Is(err, resetErr) {
		t.Fatalf("未保留原始错误链: %v", err)
	}
	if !pump.finishRead(true, resetErr) {
		t.Fatal("完成后的重置应当成功")
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceled := newStreamPump(ctx)
	cancel()
	if canceled.finishRead(false, resetErr) || len(canceled.errChan) != 0 {
		t.Fatal("客户端取消不能记录上游故障")
	}
}

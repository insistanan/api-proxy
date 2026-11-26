package providers

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

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

func TestStreamPumpBackpressureAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pump := newStreamPump(ctx)
		done := make(chan bool, 1)
		go func() { done <- pump.send("event") }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("消费者尚未接收事件，发送不应完成")
		default:
		}
		cancel()
		if <-done {
			t.Fatal("取消后的发送不能成功")
		}
	})
}

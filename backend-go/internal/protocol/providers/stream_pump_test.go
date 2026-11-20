package providers

import (
	"context"
	"errors"
	"testing"
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

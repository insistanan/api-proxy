package utils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// BindStreamBody 将上游响应体绑定到请求取消，返回正常收尾时的释放函数。
// 关闭响应体会解除阻塞中的 Read；同一绑定只执行一次 Close。
func BindStreamBody(ctx context.Context, body io.ReadCloser) func() {
	var closeOnce sync.Once
	closeBody := func() { closeOnce.Do(func() { body.Close() }) }
	stop := context.AfterFunc(ctx, closeBody)
	return func() {
		stop()
		closeBody()
	}
}

// CheckStreamCompletion 区分请求取消、真实完成和上游中断。
// completed 必须来自协议完成信号；EOF 本身不能证明回复完整。
// 完成后的连接关闭不影响成功结果，未完成时保留读取错误链。
func CheckStreamCompletion(ctx context.Context, completed bool, readErr error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if completed {
		return nil
	}
	if readErr == nil || errors.Is(readErr, io.EOF) {
		return fmt.Errorf("上游流在完成事件前结束: %w", io.ErrUnexpectedEOF)
	}
	return fmt.Errorf("上游流在完成事件前中断: %w", readErr)
}

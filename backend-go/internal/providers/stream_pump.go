package providers

import (
	"bufio"
	"context"
	"io"

	"github.com/insistanan/api-proxy/internal/utils"
)

// streamPump 聚合各 provider 流式实现共有的脚手架：事件/错误双通道、
// 带断连中止的 send/fail、上游 SSE scanner 构造。四个协议的
// HandleStreamResponseCtx 共用此骨架，协议差异（事件合成、收尾动作）
// 留在各自的循环体内。
//
// 约定：pump goroutine 必须 defer close 双通道。消费方
// （handlers/streams.ProcessStreamEvents）对 errChan 关闭有专门分支；
// close 不清除已缓冲的错误，fail 先入队的错误仍会被消费。
type streamPump struct {
	ctx       context.Context
	eventChan chan string
	errChan   chan error
}

func newStreamPump(ctx context.Context) *streamPump {
	return &streamPump{
		ctx:       ctx,
		eventChan: make(chan string, 100),
		errChan:   make(chan error, 1),
	}
}

// send 向 eventChan 发送一个事件；客户端断连（ctx 取消）时返回 false，调用方应立即退出。
func (p *streamPump) send(event string) bool {
	if p.ctx.Err() != nil {
		return false
	}
	select {
	case p.eventChan <- event:
		return true
	case <-p.ctx.Done():
		return false
	}
}

// fail 向 errChan 发送错误；客户端断连时不阻塞。
func (p *streamPump) fail(err error) {
	select {
	case p.errChan <- err:
	case <-p.ctx.Done():
	}
}

// newScanner 构造上游 SSE scanner：初始 64KB、上限 1MB 缓冲，
// 处理大 JSON chunk，避免默认 64KB 限制截断。
func (p *streamPump) newScanner(body io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(body)
	const maxScannerBufferSize = 1024 * 1024 // 1MB
	scanner.Buffer(utils.GetChunkBuffer64KB()[:0], maxScannerBufferSize)
	return scanner
}

// finishRead 验证协议完成状态；取消不伪造成功，也不记录上游故障。
func (p *streamPump) finishRead(completed bool, readErr error) bool {
	if err := utils.CheckStreamCompletion(p.ctx, completed, readErr); err != nil {
		if p.ctx.Err() == nil {
			p.fail(err)
		}
		return false
	}
	return true
}

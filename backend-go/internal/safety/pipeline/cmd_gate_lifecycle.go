package pipeline

import (
	"context"
	"fmt"
)

// beginTask 将派生调用登记到服务生命周期；关闭准入和 WaitGroup.Add 共用锁。
func (g *CmdGate) beginTask(parent context.Context) (context.Context, func(), error) {
	g.lifecycleMu.Lock()
	defer g.lifecycleMu.Unlock()
	if g.closed {
		return nil, nil, fmt.Errorf("命令审核服务正在关闭")
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(g.lifecycle, cancel)
	g.workers.Add(1)
	return ctx, func() {
		stop()
		cancel()
		g.workers.Done()
	}, nil
}

// Close 等待模型审查、否决监听及其数据库收尾，调用方随后才可关闭存储。
func (g *CmdGate) Close() error {
	g.lifecycleMu.Lock()
	g.closed = true
	g.stop()
	g.lifecycleMu.Unlock()
	g.workers.Wait()
	return nil
}

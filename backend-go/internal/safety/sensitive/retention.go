package sensitive

import (
	"context"
	"log"
	"sync"
	"time"
)

// StartHistoryRetention 定期清理安全历史，返回取消并等待后台任务退出的函数。
// 调用方必须先停止该任务，再关闭它依赖的两个存储。
func StartHistoryRetention(blocked *BlockedStore, gate *GateStore) func() error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				purgeHistory(ctx, "ContentSafety", blocked.Purge)
				purgeHistory(ctx, "Gate", gate.Purge)
			}
		}
	}()
	return func() error {
		once.Do(cancel)
		<-done
		return nil
	}
}

func purgeHistory(parent context.Context, component string, purge func(context.Context) error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := purge(ctx); err != nil && parent.Err() == nil {
		log.Printf("[%s-Purge] 清理安全历史失败: %v", component, err)
	}
}

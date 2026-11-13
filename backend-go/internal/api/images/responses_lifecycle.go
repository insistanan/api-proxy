package images

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const imagesRequestTimeout = 10 * time.Minute
const responsesImageUsageDrainTimeout = 15 * time.Second
const responsesImageLifecycleKey = "images.responses.lifecycle"

var errResponsesImageUsageDrainTimeout = errors.New("图片已交付，但等待 Responses 完成事件及用量超过 15 秒")

// responsesImageLifecycle 只在图片完整写出后忽略客户端取消，并短暂补收用量。
// 请求开始时仍传播取消；父 deadline 与生图总超时始终有效。
type responsesImageLifecycle struct {
	ctx         context.Context
	cancel      context.CancelCauseFunc
	cancelTimer context.CancelFunc
	stopParent  func() bool
	mu          sync.Mutex
	delivered   bool
	closed      bool
	drainTimer  *time.Timer
}

func newResponsesImageLifecycle(parent context.Context) *responsesImageLifecycle {
	deadline := time.Now().Add(imagesRequestTimeout)
	if parentDeadline, ok := parent.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	bounded, cancelTimer := context.WithDeadline(context.WithoutCancel(parent), deadline)
	ctx, cancel := context.WithCancelCause(bounded)
	lifecycle := &responsesImageLifecycle{ctx: ctx, cancel: cancel, cancelTimer: cancelTimer}
	lifecycle.stopParent = context.AfterFunc(parent, func() {
		lifecycle.mu.Lock()
		defer lifecycle.mu.Unlock()
		if !lifecycle.delivered {
			lifecycle.cancel(context.Cause(parent))
		}
	})
	if err := parent.Err(); err != nil {
		cancel(context.Cause(parent))
	}
	return lifecycle
}

func (lifecycle *responsesImageLifecycle) markDelivered() {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.delivered || lifecycle.closed {
		return
	}
	lifecycle.delivered = true
	lifecycle.drainTimer = time.AfterFunc(responsesImageUsageDrainTimeout, func() {
		lifecycle.cancel(errResponsesImageUsageDrainTimeout)
	})
}

func (lifecycle *responsesImageLifecycle) close() {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.closed {
		return
	}
	lifecycle.closed = true
	lifecycle.stopParent()
	if lifecycle.drainTimer != nil {
		lifecycle.drainTimer.Stop()
	}
	lifecycle.cancel(context.Canceled)
	lifecycle.cancelTimer()
}

func responsesLifecycle(c *gin.Context) *responsesImageLifecycle {
	if value, exists := c.Get(responsesImageLifecycleKey); exists {
		return value.(*responsesImageLifecycle)
	}
	lifecycle := newResponsesImageLifecycle(c.Request.Context())
	c.Set(responsesImageLifecycleKey, lifecycle)
	return lifecycle
}

func resetResponsesLifecycle(c *gin.Context) *responsesImageLifecycle {
	finishResponsesLifecycle(c)
	lifecycle := newResponsesImageLifecycle(c.Request.Context())
	c.Set(responsesImageLifecycleKey, lifecycle)
	return lifecycle
}

func finishResponsesLifecycle(c *gin.Context) {
	if value, exists := c.Get(responsesImageLifecycleKey); exists {
		value.(*responsesImageLifecycle).close()
	}
}

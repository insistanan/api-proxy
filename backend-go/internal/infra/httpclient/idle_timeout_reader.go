package httpclient

import (
	"errors"
	"io"
	"log"
	"time"
)

// ErrStreamIdleTimeout 流式响应空闲超时错误。
// 当上游"TCP 通但流中长时间无数据"时由 IdleTimeoutReader 返回，用于检测流中挂起。
var ErrStreamIdleTimeout = errors.New("stream idle timeout: upstream sent no data for the idle timeout period")

// IdleTimeoutReader 包装 io.ReadCloser，在读操作时检测空闲超时。
//
// 背景：流式响应使用无整体超时的客户端（避免误杀思考模型的长静默期），
// 导致"上游挂起（TCP 通但永不发数据）"时 handler 无限等待。
// 本 reader 在每次 Read 时 select 空闲超时：底层 Read 阻塞期间若超过
// idleTimeout 无数据，则关闭底层连接解阻塞，返回 ErrStreamIdleTimeout。
//
// 设计要点：
//   - 每次成功读取后重置计时（每次 Read 独立计时），长流中持续有数据不受影响；
//   - 默认值应保守（如 5 分钟），避免思考模型 1-2 分钟无输出被误杀；
//   - 仅对流式响应启用，普通请求不走此包装；
//   - 底层数据读入按 len(p) 分配的私有缓冲再 copy 给调用方：超时返回后
//     stale goroutine 只写自己的缓冲，不会写入调用方已复用的 p（数据竞争）；
//   - 超时后 reader 进入废弃态，后续 Read 直接返回 ErrStreamIdleTimeout，
//     不再对已 Close 的底层并发起读（io.Reader 终态错误语义）。
//     代价是每次 Read 一次 len(p) 分配 + 一次 copy，相对流式路径本身的
//     逐行字符串分配可忽略；若 profile 证明热点再考虑池化。
func NewIdleTimeoutReader(inner io.ReadCloser, idleTimeout time.Duration, apiType string) io.ReadCloser {
	if idleTimeout <= 0 {
		return inner // 未配置空闲超时，直接透传
	}
	return &idleTimeoutReader{
		inner:       inner,
		idleTimeout: idleTimeout,
		apiType:     apiType,
	}
}

type idleTimeoutReader struct {
	inner       io.ReadCloser
	idleTimeout time.Duration
	apiType     string
	// timedOut 标记空闲超时已发生（终态）。只在 Read 的调用方 goroutine
	// 内读写（io.Reader 不约定并发调用），无需加锁。
	timedOut bool
}

type readResult struct {
	buf []byte
	err error
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	if r.timedOut {
		return 0, ErrStreamIdleTimeout
	}

	// 私有缓冲：与调用方的 p 解耦，超时放弃读取后 stale goroutine
	// 的迟到写入只会落在私有缓冲里，随 GC 回收。
	buf := make([]byte, len(p))
	done := make(chan readResult, 1)
	go func() {
		n, err := r.inner.Read(buf)
		done <- readResult{buf: buf[:n], err: err}
	}()

	timer := time.NewTimer(r.idleTimeout)
	defer timer.Stop()

	select {
	case res := <-done:
		n := copy(p, res.buf)
		return n, res.err
	case <-timer.C:
		// 空闲超时：关闭底层连接以解阻塞 pending Read，返回超时错误。
		log.Printf("[%s-Stream-IdleTimeout] 警告: 流式响应空闲超时（%.0f 秒无数据），判定上游挂起，已中止流", r.apiType, r.idleTimeout.Seconds())
		r.timedOut = true
		_ = r.inner.Close()
		return 0, ErrStreamIdleTimeout
	}
}

func (r *idleTimeoutReader) Close() error {
	return r.inner.Close()
}

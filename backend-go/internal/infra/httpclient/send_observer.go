package httpclient

import (
	"context"
	"time"
)

type sendObserverKey struct{}
type responseHeadersObserverKey struct{}

// WithSendObserver 在取得准入名额、即将执行 HTTP 请求时通知观测方。
// 配置失败和本地排队失败不会通知；回调必须同步完成且不能修改请求。
func WithSendObserver(ctx context.Context, observer func()) context.Context {
	return context.WithValue(ctx, sendObserverKey{}, observer)
}

func notifySendObserver(ctx context.Context) {
	if observer, ok := ctx.Value(sendObserverKey{}).(func()); ok && observer != nil {
		observer()
	}
}

// WithResponseHeadersObserver 记录单次 HTTP 发送至收到响应头的耗时，不包含本地准入排队。
// 此值包含连接、TLS 和请求发送；失败时不产生响应头耗时。
func WithResponseHeadersObserver(ctx context.Context, observer func(time.Duration)) context.Context {
	return context.WithValue(ctx, responseHeadersObserverKey{}, observer)
}

func notifyResponseHeadersObserver(ctx context.Context, elapsed time.Duration) {
	if observer, ok := ctx.Value(responseHeadersObserverKey{}).(func(time.Duration)); ok && observer != nil {
		observer(elapsed)
	}
}

package httpclient

import "context"

type sendObserverKey struct{}

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

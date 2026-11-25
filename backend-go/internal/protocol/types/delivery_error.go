package types

import "errors"

// DownstreamWriteError 标记客户端交付失败，不能据此惩罚上游渠道或重放请求。
// 原始错误链保留给调用方判断具体的 I/O 原因。
type DownstreamWriteError struct {
	Err error
}

func (e *DownstreamWriteError) Error() string { return "写入客户端响应失败: " + e.Err.Error() }
func (e *DownstreamWriteError) Unwrap() error { return e.Err }

func IsDownstreamWriteError(err error) bool {
	var target *DownstreamWriteError
	return errors.As(err, &target)
}

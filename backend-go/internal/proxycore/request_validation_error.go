package proxycore

// RequestValidationError 表示请求在发送前无法满足目标协议契约。
// 这类错误应返回客户端，不能通过换 Key 重试，也不能污染渠道健康。
type RequestValidationError struct {
	Err error
}

func (err *RequestValidationError) Error() string { return err.Err.Error() }

func (err *RequestValidationError) Unwrap() error { return err.Err }

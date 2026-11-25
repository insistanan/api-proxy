// Package streams 提供跨协议的响应体转发和 SSE 交付生命周期。
// ForwardEventStream 由调用者提供完成判据，负责读取、取消、写回及错误通知；
// ForwardUpstreamResponseBody 用于无 SSE 协议状态的普通响应体。
// Messages 专用的事件、usage、安全回灌和日志处理位于 messages 子包。
// 本包依赖 common、codec 和 proxycore，不依赖具体协议入口或 messages 子包。
package streams

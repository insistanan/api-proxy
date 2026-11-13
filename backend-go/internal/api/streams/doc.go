// Package streams 提供 Claude 协议流式响应处理链路。
//
//   - 主循环：stream.go（Context、HandleStreamResponse /
//     ProcessStreamEvents / ProcessStreamEvent）
//   - 事件判定与构造：stream_events.go
//   - usage 检测/修补：stream_usage_detect.go / stream_usage_patch.go
//   - thinking 与工具调用累积：stream_reasoning.go
//   - 结束收尾与日志：stream_log.go
//   - 非流式响应体转发：response_stream.go
//
// 依赖方向：本包依赖 hooks（流式内容安全回灌）与 proxycore
// （首 token 计时标记）；由各协议 handler 直接调用，proxycore 不依赖本包。
package streams

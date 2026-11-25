// Package messages 处理 Messages SSE 的预读、事件、用量和交付状态。
// stream.go 负责入口与主循环，stream_delivery.go 统一写入和失败收尾，
// stream_events.go 处理协议事件；用量、推理累积和日志各自独立维护。
// 本包依赖父包 streams 的公共响应判定，父包不反向依赖本包。
package messages

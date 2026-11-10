// Package hooks 在请求发送前、流式响应期间和响应结束后执行内容安全钩子。
//
//   - 管线定义：hook_pipeline.go（Pipeline / HookStage / HookResult）
//   - 请求前 Hook 与内容安全错误类型：hooks_pre_request.go
//   - 流式期间的延迟匹配与协议化错误写出：hooks_stream.go
//   - 响应后 Hook：hooks_post_response.go
//   - 载荷处理：content_safety_segments 定义分段约束；同前缀的 _extract 提取内容，
//     _apply 执行脱敏或拦截，_record 保存记录，_multipart 与 _protocol 处理协议差异
//
// proxycore 和 streams 依赖本包，分别挂载请求钩子、处理安全错误和执行流式钩子。
// 本包不反向依赖这两个包，避免内容安全处理与请求编排形成循环依赖。
package hooks

// Package proxycore 编排 Messages、Responses、Gemini、Chat 和 Images 的代理请求。
//
// 各协议通过 ProtocolSpec 提供差异逻辑，共用请求处理、上游尝试和故障转移流程。
// 文件按职责划分，阅读时可以从以下入口开始：
//
//   - 入口/分派：protocol.go（RunProxyRequest + ProtocolSpec）
//   - 请求处理：request_body / request_send / request_channel /
//     request_prompts / request_sanitize
//   - 单次上游尝试：upstream_attempt.go 定义调用契约；同前缀的 _keys 处理重试循环，
//     _prepare/_read/_compatibility/_response 负责准备候选、读取、兼容重试与响应交付，
//     _state/_error/_log 管理状态、错误与日志，_builder 构造请求
//   - 故障转移：failover_classify（重试分类）、failover_error（全部失败时的错误响应）、
//     failover_compat（上游兼容性探测）、content_policy_retry（审核兼容重试）、
//     upstream_failover（候选模型映射）、multi_channel_failover（多渠道循环）
//   - 会话观测：conversation.go（注册表读写）、conversation_identity.go
//     （身份/指纹解析）
//
// 本包依赖 hooks 提供的内容安全错误类型和钩子管线。
// 流式处理位于 internal/handlers/streams，由各协议 handler 直接调用。
package proxycore

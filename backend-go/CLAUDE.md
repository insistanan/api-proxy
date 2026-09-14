# backend-go 模块文档

[← 根目录](../CLAUDE.md)

Go 后端核心服务：五协议代理入口（`/v1/messages`、`/v1/responses`、`/v1/chat`、`/v1/images`、`/v1beta/models/*`）、多渠道调度、协议转换、会话管理、内容安全。整体链路见 `../docs/flows.md`，能力复用表见 `../docs/capabilities.md`。接口签名、路由清单以代码为准，本文件不复述。

## 命令

```bash
make dev          # 热重载开发
make test         # 运行测试
make check        # gofmt 校验 + go vet + go test（提交前必跑）
make build        # 构建生产版本
make lint         # golangci-lint（首次运行自动安装）
make fmt          # 格式化代码
```

## 扩展指南

- **新增上游服务**：在 `internal/providers/` 实现 `Provider` 接口（签名以 `provider.go` 为准），并在 `GetProvider()` 按 ServiceType 注册。messages 与 visionlayer 共用该注册表。
- **新增协议 / 渠道能力**：五协议渠道路由经 `handlers.RegisterChannelRoutes` + `ProtocolSpec` 声明式注册；渠道 CRUD/key/Ping 一律复用 `core/channelcrud`，禁止另写。
- **调度策略**：配置顺序（对话路由覆盖 > 命中分组内配置顺序 > 兜底分组，过滤熔断），渠道级熔断经许可句柄（`SelectionResult.Permit`，R2-b）取得与释放；实现在 `internal/scheduler/selection.go` 的 `SelectChannel`。改调度前先读 `../docs/flows.md`。
- **内容安全**：钩子管线在 `internal/handlers/hooks/`，检测实现在 `internal/sensitive`；main.go 组建管线注入各协议。
- **视觉分流**：`internal/visionlayer`，在请求发送上游前自动执行（见 `../docs/flows.md` F2），勿绕过。

## 日志规范（标签唯一出处）

所有日志输出使用 `[Component-Action]` 标签格式，禁止使用 emoji 符号（确保跨平台兼容性）。

**格式规范**:
```go
// 标准格式
log.Printf("[Component-Action] 消息内容: %v", value)

// 警告信息
log.Printf("[Component] 警告: 消息内容")
```

**请求块分隔符**: `FilteredLogger`（`internal/middleware/logger.go`）在静默模式下为每个请求打印 `┌` 进入行与 `└` 结束行（含状态码与耗时），属于访问日志分隔符，不使用 `[Component-Action]` 标签。渠道画像/指标加载的 `[Profile-*]`、`[Metrics-Load]` 信息日志已移除（评分与加载结果以 Web 界面为准），加载失败警告仍保留。

**标签命名示例**:

| 组件 | 标签 | 用途 |
|------|------|------|
| 调度器 | `[Scheduler-Channel]` | 渠道选择 |
| 调度器 | `[Scheduler-Promotion]` | 促销渠道 |
| 调度器 | `[Scheduler-Affinity]` | Trace 亲和性 |
| 调度器 | `[Scheduler-Fallback]` | 降级选择 |
| 认证 | `[Auth-Failed]` | 认证失败 |
| 认证 | `[Auth-Success]` | 认证成功 |
| 指标 | `[Metrics-Store]` | 指标存储 |
| 会话 | `[Session-Manager]` | 会话管理 |
| 配置 | `[Config-Watcher]` | 配置热重载 |
| 压缩 | `[Gzip]` | Gzip 解压缩 |
| Messages | `[Messages-Stream]` | Messages 流式处理 |
| Messages | `[Messages-Stream-Token]` | Messages Token 统计 |
| Responses | `[Responses-Stream]` | Responses 流式处理 |
| Responses | `[Responses-Stream-Token]` | Responses Token 统计 |
| Models | `[Models]` | 跨接口的模型列表合并操作 |
| 评测 | `[Eval-Init]` / `[Eval-Seed]` / `[Eval-Run]` / `[Eval-Watch]` / `[Eval-Fingerprint]` / `[Eval-Shutdown]` | 评测工作台 |
| 熔断 | `[Circuit-Open]` / `[Circuit-Close]` / `[Circuit-Probe]` / `[Circuit-Init]` | 渠道级三态熔断器（internal/circuit） |
| 调度熔断 | `[X-Circuit]`（X 为协议前缀，如 Messages-Circuit） | 选渠时跳过熔断渠道 |
| AI 闸门 | `[Gate-Init]` / `[Gate-Review]` / `[Gate-Decision]` / `[Gate-Veto]` / `[Gate-Shutdown]` | 危险命令 AI 闸门（cmd_gate.go / gate_store.go） |
| 内部 LLM 调用 | `[LlmGate-Key]`（Key 失败标记，经 ConfigManager） | llmgate 渠道调用（internal/llmgate） |
| AI 助手 | `[Assistant-Init]` / `[Assistant-Shutdown]` | AI 配置助手（internal/assistant） |

## 工具使用注意事项

**Edit 工具与 Tab 缩进**:
- Go 文件使用 tab 缩进，`Edit` 工具匹配时可能因空白字符差异失败
- 失败时可用 `sed -i '' 's/old/new/g' file.go` 替代

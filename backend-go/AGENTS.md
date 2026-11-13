# backend-go AGENTS.md

本目录为 **api-proxy** 的 Go 后端核心实现，负责多协议大模型代理、跨协议转换、渠道调度准入、内容安全风控与管理 API。系统全景架构与流程说明见 [架构说明](../docs/架构说明.md)，代码规范与核心约束见 [规范与不变量](../docs/规范与不变量.md)。

---

## 1. 技术栈与运行环境

- **语言与版本**：Go 1.26（工具链锁定 1.26.10，见 `go.mod`），模块名 `github.com/insistanan/api-proxy`，全静态编译（`CGO_ENABLED=0`）。
- **Web 框架**：Gin。流式 SSE 事件解析与转发采用内部手写状态机（`internal/infra/utils/sse.go`、`internal/handlers/streams` 与 `internal/protocol/providers`），无 WebSocket 依赖。
- **存储引擎**：纯 Go 实现的 SQLite（`modernc.org/sqlite`），采用原生 `database/sql` 与原生 SQL 语句维护表结构及升级；日志库采用读写连接分离（1 写 + 最多 4 读），业务库采用单连接池并开启 WAL 模式。
- **核心工具依赖**：`tidwall/gjson` 与 `sjson`（高性能 JSON 切片读写）、`fsnotify`（配置监听防抖热重载）、`godotenv`（环境变量）、`klauspost/compress` 与 `brotli`（多算法解压）、`stretchr/testify`（测试断言）。

---

## 2. 目录结构与分层架构

### 2.1 入口与装配（cmd/server/）

| 文件 | 核心职责 |
|---|---|
| `cmd/server/main.go` | 应用上下文定义与依赖注入组装（`newApp()`），按线性依赖初始化组件并登记逆序释放闭包；监听系统退出信号 |
| `cmd/server/app_routes.go` | 路由装配：注册 `/api` 管理路由组、公开健康检查 `/health` 及各协议代理端点 |
| `cmd/server/app_lifecycle.go` | 启动控制台横幅打印、脱敏模式热更新跟随、优雅停机（Graceful Shutdown）流程 |
| `cmd/server/version.go` | 编译期由 ldflags 注入的 `Version`、`BuildTime` 与 `GitCommit` 元数据 |

### 2.2 核心业务包（internal/ 9 大领域模块）

- **`handlers/`（协议端点处理层）**：
  - `proxycore/`：跨协议统一请求流水线（`RunProxyRequest`），负责代理通用鉴权、读体、脱敏、会话观测与重试驱动；
  - `messages/`、`responses/`、`chat/`、`gemini/`、`images/`：五大编排协议端点实现；
  - `embeddings/`、`decisions/`：原样透传端点；
  - `streams/`、`hooks/`、`admin/`、`channels/`、`clients/`、`modelapi/`、`system/`：流式转发、生命周期钩子与管理控制端点。
- **`orchestration/`（调度与稳定性控制）**：
  - `scheduler/`：多渠道调度核心（`ChannelScheduler`），支持配置顺序轮询、促销优先机制与选渠许可句柄（`ChannelPermit`）；
  - `circuit/`：渠道级三态熔断器（Closed / Open / HalfOpen），防止向上游故障节点持续发压；
  - `upstreamcontrol/`：按 `[目标URL + 实际协议 + API Key]` 物理凭据维度聚合全局并发（默认并发 5）、FIFO 排队队列（最多 3 个、最长 10 秒）与 429 冷却退避；
  - `urlhealth/`：多 BaseURL 健康探测与动态权重管理；
  - `availability/`、`proxy/`：渠道可用性检查与重试状态分类。
- **`protocol/`（协议抽象与转换）**：
  - `converters/`：跨协议请求结构、响应结构与 SSE 流式事件的互转桥梁；
  - `providers/`：统一抽象 Provider 接口，实现面向各上游物理协议的请求构造与返回解析；
  - `types/`：统一协议模型定义。
- **`safety/`（内容安全与风控）**：
  - `sensitive/`：敏感信息检测与 AES-256-GCM 可逆脱敏体系；
  - `llmgate/`：高风险命令的大模型审核闸门与配置助手推理后端；
  - `bodyfilter/`、`mediasanitizer/`、`visionlayer/`：参数过滤、媒体模态净化与图片理解缓存。
- **`dialogue/`（会话与上下文治理）**：
  - `session/`：Responses 会话链追踪与基于 Trace 的 BaseURL 亲和性管理（提升 Prompt Cache 命中率）；
  - `conversation/`：对话上下文注册表，结合内存 LRU 缓存与 SQLite 分页查询；
  - `cacheinject/`、`copilotopt/`、`rectifier/`、`intent/`、`semantic/`：提示词缓存断点注入与参数整流。
- **`storage/`（持久化存储与观测）**：
  - `logger/`：基于 SQLite 的日志落盘，内置条数与字节双重背压缓冲队列、正文哈希去重、调用聚合（Call Query）与上下文快照（Context Snapshot）；
  - `metrics/`：按 7 大渠道分组统计调用量、延迟与成功率，支持持久化至 SQLite；
  - `sqlitedb/`：底层连接池与 WAL 模式管理；
  - `atomicfile/`：原子文件写原语。
- **`admin/`（后台管理支撑）**：
  - `assistant/`：内置 AI 配置助手服务；
  - `channelcrud/`：协议无关的通用渠道增删改查；
  - `modelcatalog/`、`skills/`、`mcpsync/`、`piagent/`：模型目录、Agent 技能与客户端协同。
- **`infra/`（基础设施）**：
  - `config/`：配置管理与热重载；
  - `httpclient/`：外部大模型 HTTP 推理请求唯一统一发送出口；
  - `middleware/`：鉴权、跨域与通用中间件；
  - `utils/`：通用工具集合；
  - `cli/`：命令行日志查询（`api-proxy logs`）。
- **`eval/`（评估与探针）**：渠道质量评估与探针工具。

---

## 3. 核心设计与代码规范

1. **统一错误包装**：出现错误时，使用 `fmt.Errorf` 配合 `%w` 包装错误链，并使用通顺中文说明失败原因，严禁吞掉异常或返回无意义的兜底数据。
2. **日志打印规范**：采用标准 `log.Printf`，格式遵循 `[组件-动作] 中文说明`，例如 `[Circuit-Init] 初始化熔断器`。
3. **选渠许可生命周期**：获取 `ChannelPermit` 后必须在 `defer` 中执行 `Release()`；请求完成时，必须确保调用一次 `RecordHealth` 更新健康指标。
4. **共享准入凭据聚合**：并发与排队必须以底层物理凭据 `[URL + 上游类型 + Key]` 为聚合维度，不可按逻辑渠道名分组。
5. **流式 Context 绑定**：流式转发必须透传 Context，并在结束或客户端断开时第一时间关闭上游响应的 Body，杜绝连接和推理泄漏。
6. **逆序释放原则**：优雅停机时严格按逆序执行：排空 HTTP 请求 → 停止数据生产者与后台任务 → 关闭数据库连接池。

---

## 4. 常用维护命令

| 命令 | 说明 |
|---|---|
| `make dev` | 启动 Air 后端热重载服务（端口 3000） |
| `make build` | 复制已有前端产物并静态编译出单一二进制至 `../dist/api-proxy-go` |
| `make check` | 运行 `gofmt -l .`（有输出即失败）+ `go vet` + `go test` 本地门禁 |
| `make test` / `make test-cover` | 运行测试套件 / 输出代码覆盖率报告 |
| `make fmt` | 使用 `gofmt` 格式化当前目录下的所有 Go 源码 |
| `./build.sh` | 跨平台交叉编译脚本（打包 linux/darwin/windows 的 amd64/arm64 产物） |

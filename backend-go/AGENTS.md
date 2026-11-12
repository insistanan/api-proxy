# backend-go AGENTS.md

本目录实现 api-proxy 的 Go 后端，负责代理请求、渠道调度、内容安全和管理 API。跨模块的请求流程与存储说明见 [架构说明](../docs/架构说明.md)，修改代码时还需遵守 [规范与不变量](../docs/规范与不变量.md)。

## 技术栈

- Go 1.26（工具链 1.26.10，`go.mod`），module `github.com/insistanan/api-proxy`，`CGO_ENABLED=0` 纯静态构建
- Web 框架：Gin（版本见 `go.mod`）；业务 SSE 解析与转发在 `internal/infra/utils/sse.go`、`handlers/streams` 和 `internal/protocol/providers`，无 WebSocket 功能
- 数据库：modernc.org/sqlite（纯 Go SQLite），`database/sql` 原生 SQL；各存储自行建表与升级列，无 ORM 或外部 migration 框架
- 其他直接依赖：tidwall/gjson + sjson（JSON 操作）、fsnotify（配置热重载）、godotenv、go-toml、klauspost/compress + andybalholm/brotli（响应解压）、stretchr/testify（测试）

## 目录结构

`backend-go/` 根目录只放模块元信息、构建脚本与嵌入声明，不放业务代码：

| 文件 | 职责 |
|---|---|
| `webassets.go` | `package apiproxy`，通过 `go:embed all:frontend/dist` 导出 `FrontendFS`。`go:embed` 只能引用声明文件所在目录及子目录内的文件，因此嵌入声明必须留在根目录 |
| `Makefile` / `build.sh` / `.air.toml` | 构建、交叉编译与热重载入口，均指向 `./cmd/server` |

`cmd/` 下的可执行程序：

| 路径 | 职责 |
|---|---|
| `cmd/server` | 主服务入口（`package main`）。`main.go` 定义 `app`，通过 `newApp()` 按依赖顺序装配组件并处理监听与退出信号；`app_routes.go` 路由装配（`/api` 管理组 + 代理端点：五大编排协议 + Embeddings/Decisions 透传）；`app_lifecycle.go` 启动横幅、脱敏模式热跟随、逆序 shutdown；`version.go` 定义 `Version`/`BuildTime`/`GitCommit`（由 ldflags 注入）。同目录的 `*_test.go` 为路由与生命周期测试 |
| `cmd/stream_verify` | 对比验证流式响应 Token 统计与记录格式的调试工具 |

`internal/` 按职能分组。分组目录本身不含业务代码，包名与目录名一致：

| 分组目录 | 包 |
|---|---|
| `handlers/` | HTTP 适配层，按业务域分子包（见下表） |
| `orchestration/` | 调度与稳定性：`scheduler`（ChannelScheduler + ChannelPermit）、`circuit`（三态熔断）、`upstreamcontrol`（共享并发、FIFO 队列、429 冷却与请求预算）、`urlhealth`（多 BaseURL 健康管理）、`availability`（按真实请求结果采样渠道成败，不参与选渠和主动探测）、`proxy`（框架无关的候选重试状态与错误分类，有静态依赖断言） |
| `protocol/` | 协议层数据面：`converters`（协议转换）、`providers`（上游请求构造与响应转换）、`types`（跨协议共享结构体：Usage、会话、Gemini/Responses 类型） |
| `dialogue/` | 会话与智能层：`conversation`（对话注册表）、`session`（Responses 会话 + Trace/BaseURL 亲和）、`semantic`（可审核数据集、持久化向量快照与离线评测）、`intent`（意图分类、分组决策与会话切换保护，不修改主请求提示词；Windows 本地 embedding 在 `local_windows.go`）、`cacheinject`（Anthropic/Bedrock prompt caching 断点注入）、`copilotopt`（Copilot 请求识别与优化）、`rectifier`（thinking budget / signature 契约整流） |
| `safety/` | 内容安全与请求体兼容：`sensitive`（检测/掩码/可逆脱敏）、`llmgate`（危险命令闸门与助手共用推理）、`visionlayer`（图片理解）、`bodyfilter`（过滤请求体中下划线开头的私有参数）、`mediasanitizer`（模型不支持图片时的模态降级占位） |
| `storage/` | 存储与观测：`sqlitedb`（统一业务库连接预算与打开封装）、`atomicfile`（原子写原语）、`metrics`（按 ChannelKind 分组 + SQLite 持久化）、`logger`（SQLite 日志存储） |
| `infra/` | 基础设施：`config`（EnvConfig + ConfigManager 热重载）、`httpclient`（推理请求发送出口）、`middleware`（auth/cors/logger）、`utils`（共用工具）、`cli`（`api-proxy logs` 子命令） |
| `admin/` | 管理界面支撑：`assistant`（AI 配置助手）、`skills`（Agent Skill 目录管理）、`piagent`（pi-agent 的模型供应商配置管理）、`modelcatalog`（模型目录 + Chat 路由别名）、`mcpsync`（MCP 统一列表分发到各智能体配置文件）、`channelcrud`（协议无关渠道 CRUD） |

`internal/handlers/` 子包：

| 子包 | 职责 |
|---|---|
| `handlers/messages`、`handlers/responses`、`handlers/chat`、`handlers/gemini`、`handlers/images` | 五大编排协议 handler |
| `handlers/proxycore` | 共享编排与候选重试（`RunProxyRequest`、`ProtocolSpec`、上游尝试） |
| `handlers/streams` | Claude 流式链路 |
| `handlers/embeddings`、`handlers/decisions` | 原样透传端点，经 `RunProxyRequest` 选渠，不接内容安全管道 |
| `handlers/modelapi` | 统一模型目录转换为 OpenAI/Anthropic/Gemini 查询响应 |
| `handlers/hooks` | 请求前与响应后内容安全处理、流式命令闸门；内置处理器在构造时直接装配 |
| `handlers/channels` | 渠道管理 API：渠道动作、日志、指标、性能、池、映射目标、熔断、免费渠道、模型可用性、全局统计、上游模型发现、促销路由 |
| `handlers/clients` | 客户端伪装与一键配置：Claude Code、Codex、OpenCode、pi-agent、DSH、MCP、skills、工具版本、agentprompt |
| `handlers/admin` | 管理后台：设置、ccs、意图配置、会话、数据集、AI 助手、安全闸门、请求/系统/拦截日志、客户端伪装自动同步 |
| `handlers/system` | 健康检查、前端静态资源、版本信息、代理模型列表、配置保存与开发信息 |
| `handlers/common` | 子包共享契约：`ConfigManager`（促销期接口）、`ReadLimitedBody`（限长读取响应体）。只收录被两个及以上子包使用的符号 |

## 架构核心（详见 docs/架构说明.md）

- **装配**：`cmd/server/main.go` 的 `newApp()` 线性装配组件，初始化失败按 `registerCleanup` 逆序清理。
- **五大编排协议统一入口**：`handlers/proxycore/protocol.go` 的 `RunProxyRequest` 负责认证、读体、解析、脱敏、会话观测、日志与渠道分派，协议差异通过 `ProtocolSpec` 注入；`handlers/embeddings` 与 `handlers/decisions` 是原样透传端点，同样经 `RunProxyRequest` 编排选渠，但不接内容安全管道。Responses compact 另有控制请求入口，复用上游尝试和选渠机制。
- **调度**：`ChannelScheduler` 按 `ChannelKind`（messages/responses/gemini/chat/images/embeddings/decisions）区分入口和指标，`ServiceType` 表示上游服务类型。共享准入按实际 URL、上游协议和 API Key 分配名额，入口协议不能代替实际上游协议。
- **鉴权三层**：WebAuthMiddleware 只管页面开关；管理路由必须在路由组上显式绑定 AdminAuthMiddleware（漏绑会打 `[Auth-Warn]`）；代理鉴权由 RunProxyRequest 或独立控制、模型目录入口调用。

## 命令

| 命令 | 说明 |
|---|---|
| `make dev` | Air 热重载开发 |
| `make build` | copy-frontend → `CGO_ENABLED=0 go build`（ldflags 注入版本），产物 `../dist/api-proxy-go` |
| `make check` | gofmt -l（有输出即失败）+ go vet + go test，提交前门禁；目录在 AGENTS.md 的登记由根 `scripts/check.mjs` 校验 |
| `make test` / `make test-cover` | 测试 / 覆盖率 |
| `make fmt` | gofmt 格式化 |
| `./build.sh` | 跨平台交叉编译（linux/darwin/windows × amd64/arm64） |

在本目录执行 `make build` 前，需要先构建 `frontend/dist`。本目录的构建命令通过 `../scripts/copy-frontend.mjs` 复制已有产物，不会重新构建前端；需要一次完成前后端构建时，使用根目录的 `make build`。

## 配置

启动配置通过环境变量或 `.env` 读取，包括 `PORT`（默认 3000）、`ENV`（默认 production）、`PROXY_ACCESS_KEY`、`ENABLE_WEB_UI`（默认 true）、`LOG_LEVEL`（默认 info）和 `LOG_TO_CONSOLE`（默认 false）。程序先加载可执行文件所在目录的 `.env`，再加载工作目录中的文件，不覆盖已有环境变量。这些启动配置修改后需要重启。

请求与响应日志、超时、请求体限制、指标保留期、CORS、日志路径和指标持久化开关保存在 `settings.gateway` 中。升级时只为缺失字段迁移旧环境变量值，之后以持久化设置为准。修改日志路径或指标持久化开关需要重启，其余策略支持热更新。客户端伪装版本由 `settings.disguise` 管理，配置发布后立即应用本地版本，npm 查询在后台独立执行。

`config.json` 保存七组渠道配置（五大编排协议 + Embeddings/Decisions 透传池）和 settings。`internal/infra/config/config_loader.go` 使用 fsnotify 监听变化，以 100ms 防抖和 SHA-256 摘要比较减少重复加载。写入通过 `internal/storage/atomicfile` 原子完成，热重载只发布完整候选快照。

## 数据存储

主配置是 JSON，业务存储使用纯 Go SQLite。默认文件及使用方见 [架构说明](../docs/架构说明.md)。SQLite 使用 WAL；日志库单写连接与最多 4 个只读连接分离，其他业务库通常为单连接。建表与升级由各存储执行 SQL，配置迁移在 config 包中处理，无外部 migration 框架。日志库路径可在 `settings.gateway` 中修改，重启后生效。

## 代码规范（详见 docs/规范与不变量.md）

- 注释使用自然、完整的中文，说明职责、调用约束和设计原因。保留仍相关的决策编号（如「R4-b」「Veil ADR 0004」）和必要背景，删除失效计划与重复说明，不为缩短篇幅而省略关键条件。
- 使用 `fmt.Errorf` 和 `%w` 包装错误，错误消息用中文说明失败原因，避免静默忽略异常。
- 日志：标准 `log.Printf`，格式 `[组件-动作] 中文消息`（如 `[Circuit-Init]`、`[Auth-Failed]`）
- 测试：testify assert/require；有特性测试（characterization_test.go）和静态依赖断言测试，新增结构性改动要考虑补这两类
- 无 `.golangci.yml`，lint 门禁是 gofmt + vet + test

## 关键不变量（完整说明见 docs/规范与不变量.md）

1. shutdown 逆序：先排空 HTTP 请求，再停数据生产者（scheduler）并等待后台任务退出，最后关闭存储；清理登记表是启动失败与正常关闭的唯一释放入口
2. 生产选渠默认按配置顺序；健康促销渠道在所属分组内临时优先，仍受熔断准入限制。促销任一限制达到后恢复配置顺序；`storage/metrics/performance_profile.go` 保留画像采集和评分，供 `/api/performance/report` 展示，不参与选渠排序
3. Provider 流式转换使用 `HandleStreamResponseCtx`，原生转发也须传播 context 并关闭上游 body，客户端断连停止读取
4. `ChannelPermit`：defer 释放，正常结束先提交健康结果；预留释放与健康记账分别至多执行一次，未记账即释放按中性结束，结束后不接受迟到结果
5. 调用 metrics 时必须传入 `apiType`，空串会按 messages 统计。可选存储先通过接口变量传递，避免 typed-nil
6. Messages 转换为 Responses 时，构造请求与处理响应复用 Gin context 中的 Provider 实例，保留 previous_response_id 链
7. 重启后遗留的 gate waiting 记录一律按拦截处理
8. 配置重载只发布完整候选，读者只能拿到完整旧快照或完整新快照

---

最后核对：v3.3.16（升级版本时用 `scripts/check.mjs` 的目录登记校验辅助核对本文件与实际目录的差集）

# backend-go AGENTS.md

Go 后端：api-proxy 代理网关核心。跨模块的架构图、请求流转、数据存储说明见根目录 [docs/架构说明.md](../docs/架构说明.md)，代码规范与不变量全文见 [docs/规范与不变量.md](../docs/规范与不变量.md)。

## 技术栈

- Go 1.25（`go.mod`），module `github.com/BenedictKing/api-proxy`，`CGO_ENABLED=0` 纯静态构建
- Web 框架：gin v1.10.0；**SSE 流式全部手写**（`utils/sse.go`、`handlers/streams`），无 websocket 库
- 数据库：modernc.org/sqlite（纯 Go SQLite），`database/sql` 原生 SQL，**无 ORM、无 migration 框架**（建表靠 `CREATE TABLE IF NOT EXISTS`）
- 其他直接依赖：tidwall/gjson + sjson（JSON 操作）、fsnotify（配置热重载）、godotenv、go-toml、klauspost/compress + andybalholm/brotli（响应解压）、stretchr/testify（测试）

## 目录结构

根目录 `package main` 按职责分文件：

| 文件 | 职责 |
|---|---|
| `main.go` | `app` struct 聚合 + `newApp()` 线性装配 + 优雅关闭；`app` 字段顺序即依赖顺序 |
| `app_routes.go` | 路由装配（`/api` 管理组 + 五协议代理端点） |
| `app_lifecycle.go` | 启动横幅、脱敏模式热跟随、逆序 shutdown |
| `version.go` | `Version`/`BuildTime`/`GitCommit`（由 ldflags 注入） |

`internal/` 按职能分组（各包多有 doc.go 声明职责与依赖方向）：

| 分组 | 包 |
|---|---|
| 协议层 | `handlers/messages`、`handlers/responses`、`handlers/chat`、`handlers/gemini`、`handlers/images`（五协议 handler）、`handlers/proxycore`（五协议共享编排核心）、`handlers/streams`（Claude 流式链路）、`converters`（协议转换，约 10k 行）、`providers`（Provider 接口 + openai/gemini/claude/responses 实现） |
| 调度与稳定性 | `scheduler`（ChannelScheduler + ChannelPermit）、`circuit`（三态熔断）、`ratelimit`（滑动窗口限流）、`urlhealth`（多 BaseURL 健康管理） |
| 观测 | `metrics`（按 ChannelKind 分组 + SQLite 持久化）、`session`（Responses 会话 + Trace/BaseURL 亲和）、`conversation`（对话注册表）、`logger`（SQLite 日志存储）、`cli`（`api-proxy logs` 子命令） |
| 内容安全 | `sensitive`（检测/掩码/可逆脱敏）、`llmgate`（危险命令 AI 闸门 + AI 助手共用能力）、`handlers/hooks`（三阶段钩子管线）、`bodyfilter`、`cacheinject`、`copilotopt`、`mediasanitizer`、`rectifier` |
| 管理界面支撑 | `assistant`（AI 配置助手）、`skills`（Agent Skill 目录管理）、`piagent`、`eval`（评测工作台）、`modelcatalog`（模型目录 + Chat 路由别名）、`core/channelcrud`（协议无关渠道 CRUD） |
| 基础设施 | `config`（EnvConfig + ConfigManager 热重载）、`httpclient`（网络发送唯一出口）、`proxy`（框架无关编排核心，有静态依赖断言）、`atomicfile`（原子写原语）、`middleware`（auth/cors/logger）、`utils`（通用工具，约 7k 行） |

## 架构核心（详见 docs/架构说明.md）

- **装配**：`newApp()`（main.go:69-380）线性装配全部组件，初始化失败按 `registerCleanup` 逆序清理。
- **五协议统一入口**：`proxycore.RunProxyRequest`（protocol.go:77+）承担"认证 → 读体 → 解析 → 脱敏 → 会话观测 → 日志 → 渠道分派"，协议差异通过 `ProtocolSpec`（protocol.go:26-61）注入。新增协议 = 实现 ProtocolSpec 钩子，不要另起炉灶。
- **调度**：`ChannelScheduler` 按 5 种 `ChannelKind`（messages/responses/gemini/chat/images）调度。**ChannelKind ≠ upstream ServiceType**（channel_scheduler.go:37-39 有明确注释），这是最容易混淆的概念。
- **鉴权三层**：WebAuthMiddleware 只管页面开关；管理路由必须在路由组上显式绑定 AdminAuthMiddleware（漏绑会打 `[Auth-Warn]`）；代理端点的 ProxyAuthMiddleware 在 RunProxyRequest 内部调用而非路由层。

## 命令

| 命令 | 说明 |
|---|---|
| `make dev` | Air 热重载开发 |
| `make build` | copy-frontend → `CGO_ENABLED=0 go build`（ldflags 注入版本），产物 `../dist/api-proxy-go` |
| `make check` | gofmt -l（有输出即失败）+ go vet + go test，提交前门禁 |
| `make test` / `make test-cover` | 测试 / 覆盖率 |
| `make fmt` | gofmt 格式化 |
| `./build.sh` | 跨平台交叉编译（linux/darwin/windows × amd64/arm64） |

构建前提：`backend-go/frontend/dist` 必须先有前端产物（`make build` 自动经 `../scripts/copy-frontend.mjs` 复制，build.sh 会检查）。

## 配置

两层，均支持热更新语义：

- **环境变量 / .env**（`internal/config/env.go:54-100` 完整清单）：`PORT`（3000）、`ENABLE_WEB_UI`、`PROXY_ACCESS_KEY`（访问密钥）、`LOG_LEVEL`、`ENABLE_REQUEST_LOGS`/`ENABLE_RESPONSE_LOGS`、`CIRCUIT_*`（6 项熔断参数，均带 clamp）、`METRICS_*`、`REQUEST_TIMEOUT` 等。.env 加载顺序：可执行文件同目录 → cwd，不覆盖已有环境变量。
- **config.json**（`internal/config/config_loader.go`）：五组渠道（upstream/responsesUpstream/geminiUpstream/chatUpstream/imagesUpstream）+ settings；fsnotify + 100ms debounce + sha256 摘要比较，原子写（经 `internal/atomicfile`），热重载只发布完整候选快照。

## 数据存储

全部 SQLite（WAL 模式，`SetMaxOpenConns(1)`），文件在运行目录 `.config/` 下：`config.json`、`logs.db`、`blocked-logs.db`、`conversations.db`、`metrics.db`（可选）、`redaction-mappings.db` + `master-key`（AES-256-GCM）、`assistant.db`、`eval.db`。结构演进无 migration 框架，靠 `CREATE TABLE IF NOT EXISTS` + config 包内 JSON 迁移逻辑。

## 代码规范（详见 docs/规范与不变量.md）

- 注释全中文、高密度，含决策编号（如 "R4-b"、"Veil ADR 0004"）——改动时保留并延续这些编号
- 错误处理：`fmt.Errorf` 带 `%w` 包装 + 中文错误消息；显式抛错，不静默兜底
- 日志：标准 `log.Printf`，格式 `[组件-动作] 中文消息`（如 `[Circuit-Init]`、`[Auth-Failed]`）
- 测试：testify assert/require；有特性测试（characterization_test.go）和静态依赖断言测试，新增结构性改动要考虑补这两类
- 无 `.golangci.yml`，lint 门禁是 gofmt + vet + test

## 关键不变量（违反必出 bug，全文见 docs/规范与不变量.md）

1. shutdown 逆序：先停数据生产者（scheduler）再关存储；`app` 字段顺序即构造顺序，不可调换
2. 生产选渠严格按配置顺序，性能画像仅观测不参与选渠
3. 流式响应必须用 `HandleStreamResponseCtx`（带 context），客户端断连立即停读上游
4. `ChannelPermit`：defer 释放，结束时 RecordHealth 恰好一次再 Release，均可幂等
5. `apiType` 必须无条件传入 metrics（空串会被当 messages 口径）；可选 store 收进接口变量避免 typed-nil
6. Messages→Responses 转换时 provider 实例存 gin context 复用，否则丢 previous_response_id 链
7. 重启后遗留的 gate waiting 记录一律按拦截处理
8. 配置重载只发布完整候选，读者只能拿到完整旧快照或完整新快照

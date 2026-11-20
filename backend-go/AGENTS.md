# 后端约定

单 Go module：`github.com/insistanan/api-proxy`，Go 1.26，工具链 1.26.10。编码规则见 [规范与不变量](../docs/规范与不变量.md)，请求流程见 [架构说明](../docs/架构说明.md)。

## 入口

| 路径 | 职责 |
|---|---|
| `cmd/server/` | 服务入口；main.go 管理进程启动，app.go 装配依赖，app_routes.go 注册路由，app_lifecycle.go 管理停机 |
| `cmd/stream_verify/` | 独立 SSE 与用量验证工具 |
| `webassets.go` | 嵌入前端资源，必须留在 module 根目录 |

## internal 模块

| 目录 | 职责与主要子包 |
|---|---|
| `api/` | 代理与管理 HTTP 接入、SSE 写回；`api/admin/` 按业务域分包，`api/admin/shared` 放跨管理子包协作，`api/common` 放跨 API 子包共用能力；协议差异留在端点中 |
| `proxycore/` | 统一请求编排、上游尝试、故障转移与日志策略；入口 protocol.go |
| `clients/` | 客户端配置接口与文件操作；协同服务在下方子包中 |
| `management/` | 管理支撑：assistant、channelcrud、modelcatalog |
| `orchestration/` | scheduler、circuit、upstreamcontrol、availability、proxy、ratelimit、urlhealth |
| `protocol/` | converters、providers、codec、items、tokenusage、visionmedia、types |
| `safety/` | sensitive、llmgate、pipeline、bodyfilter、mediasanitizer、visionlayer |
| `dialogue/` | session、conversation、prompt、routing（intent / semantic） |
| `storage/` | logger、logbody、metrics、sqlitedb、atomicfile |
| `infra/` | config、httpclient、upstreamreq、middleware、cli、utils |

`api/` 子包登记：

| 子包 | 职责 |
|---|---|
| `api/messages`、`api/responses`、`api/chat`、`api/gemini`、`api/images` | 五类编排协议入口 |
| `api/embeddings`、`api/decisions` | 原样透传 |
| `api/admin/` | 管理 API 的业务域子包 |
| `api/admin/{assistant,conversations,extensions,intent,logs,security,settings}` | 对应业务域的管理接口 |
| `api/channels`、`api/modelapi` | 渠道与模型接口 |
| `api/system` | 健康检查、前端资源与代理模型列表 |
| `api/streams` | 流式写回与生命周期处理 |
| `api/common` | HTTP 接入层共用契约与工具 |

`clients/` 子包：`clients/piagent` 处理 pi-agent 配置，`clients/mcpsync` 同步 MCP，`clients/skills` 管理 Skill 文件。

## 依赖边界

- Go 子目录就是独立包；只有职责独立且依赖方向清晰时才拆包。同一职责内文件较多时按职责命名文件，不为缩短文件列表制造包边界。
- 通用编排留在 proxycore，协议转换留在 protocol；安全管线不反向依赖 api 或 proxycore。
- JSON、SSE 等编解码工具放 protocol/codec；用量、Responses 条目和上游请求分别放对应包，避免重新堆入 utils。
- clients 顶层包含 HTTP 配置接口；其 piagent / mcpsync / skills 子包不依赖 HTTP，供接口层调用。

## 维护命令

在本目录执行 `make dev`、`make test`、`make test-cover`、`make fmt`。`make build` 复制已有前端产物后编译；完整前后端构建和提交门禁在仓库根执行。`./build.sh` 输出平台产物到 `backend-go/dist/`，具体目标以脚本为准。

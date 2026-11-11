# api-proxy

api-proxy 是一个自托管 LLM API 代理网关，可以管理多个上游渠道，处理协议转换和故障转移，并提供内容安全检查。Web 管理界面随 Go 程序一起打包，部署时只需要一个可执行文件。

[English](README-en.md) | 当前版本见 [VERSION](VERSION)

## 功能特性

- **协议接入与转换**：提供 Claude Messages、OpenAI Responses、OpenAI Chat、Gemini 和 Images 五类入口。前四类支持对话协议转换；Images 可以连接原生 Images API 或 Responses 生图上游，支持的操作取决于上游协议。
- **多渠道调度**：按分组和配置顺序尝试渠道，支持故障转移、渠道熔断，以及健康渠道在组内临时优先的「促销」功能。同一 URL、上游协议和 API Key 共用并发名额，超出的请求按 FIFO 排队；重试会遵守 429 冷却时间和请求预算。多 BaseURL 健康管理与会话亲和也由调度链路处理。
- **客户端配置**：为 Claude Code、Codex、OpenCode、pi-agent、DSH 和 GPT 提供配置页，可将客户端连接地址指向本代理，并设置客户端伪装版本。
- **内容安全**：检测敏感数据，使用 AES-256-GCM 保存可逆脱敏映射，递归过滤私有参数，并通过 AI 闸门审核危险命令。
- **日志与指标**：按渠道和模型查看运行指标；请求日志和系统日志保存在 SQLite 中，也可以通过 CLI 查询。拦截记录按会话分组，便于回看。
- **部署方式**：Vue 3 管理界面通过 `go:embed` 嵌入 Go 程序，默认在 3000 端口同时提供代理 API 和管理界面。

## 快速开始

### Docker（推荐）

```bash
# 先将 docker-compose.yml 中的 PROXY_ACCESS_KEY 改为自己的密钥，再启动服务。
docker compose up -d
```

关键环境变量：

| 变量 | 说明 |
|---|---|
| `PROXY_ACCESS_KEY` | 访问密钥，**必须修改** |
| `ENABLE_WEB_UI` | `true`（默认）一体化模式 / `false` 纯 API 模式 |
| `PORT` / `ENV` | 监听端口（默认 3000）与运行环境（默认 production） |
| `LOG_LEVEL` / `LOG_TO_CONSOLE` | 日志级别（默认 info）与控制台日志开关（默认 false） |

配置与数据库默认保存在工作目录的 `./.config` 下，运行日志和请求正文写入其中的 `logs.db`。请求与响应日志开关、超时、CORS 等策略在设置页的「请求与日志」中维护。

升级旧版本时，`ENABLE_REQUEST_LOGS` 等旧环境变量只用于补齐尚未保存的设置字段，之后以持久化设置为准。修改日志数据库路径或指标持久化开关需要重启，其余请求策略支持热更新。需要从源码构建镜像时，步骤见[开发说明](docs/开发说明.md)。

### 从 Release 下载

到 [Releases](../../releases) 下载对应平台的可执行文件，支持 Linux、macOS、Windows 的 amd64 和 arm64 架构。设置访问密钥并启动程序后，在浏览器打开 `http://localhost:3000` 即可进入管理界面。

### 从源码构建

本地构建使用 Go 1.26.10、Node 24.16.0，以及 make。先在 `frontend/` 目录执行 `npm ci` 或 Bun 1.4.4 的 `bun install --frozen-lockfile` 安装依赖，再回到根目录执行以下命令。根 Makefile 使用 npm 和 Node；Windows 下未安装 make 时，可以用根目录的 `npm run check` 运行全量门禁。

```bash
make build   # 前端构建 → 嵌入 → Go 编译，产物 dist/api-proxy-go
make dev     # 启动 Vite 前端与 Air 后端热重载
make check   # 提交前全量门禁
```

## 端点一览

| 端点 | 用途 |
|---|---|
| `POST /v1/messages`、`/v1/messages/count_tokens` | Claude Messages 协议 |
| `POST /v1/responses`（含 `/compact`） | OpenAI Responses 协议 |
| `POST /v1/chat/completions` | OpenAI Chat 协议 |
| `POST /v1/images/{generations,edits,variations}` | Images 协议 |
| `POST /v1beta/models/*` | Gemini 协议 |
| `GET /v1/models` | 模型目录 |
| `/api/*` | 管理 API（需访问密钥，`x-api-key` 头） |
| `/health` | 健康检查（公开，Docker HEALTHCHECK 使用） |

Images 渠道的上游协议可选 **Images API** 或 **Responses API**，客户端都可以通过 `/v1/images/generations` 和 `/v1/images/edits` 调用。使用 Responses 生图时，需要配置 `responsesModel` 作为主模型；客户端传入的 `model` 和渠道模型映射用于选择图片生成工具的模型。代理会将 JSON 或 SSE 结果转换为 Images 格式，图片以 `b64_json` 返回。

托管图片 URL 和 variations 请求需要使用原生 Images API 渠道，Responses 生图上游不支持这两类操作。

## 文档

| 文档 | 内容 |
|---|---|
| [docs/开发说明.md](docs/开发说明.md) | 环境准备、命令、门禁、发布流程、Docker 部署 |
| [docs/架构说明.md](docs/架构说明.md) | 架构图、请求流转、调度体系、数据存储、鉴权模型 |
| [docs/规范与不变量.md](docs/规范与不变量.md) | 代码规范与关键不变量 |
| [docs/技术栈.md](docs/技术栈.md) | 依赖与工具链总览 |
| [docs/历史说明.md](docs/历史说明.md) | 版本演进史与关键里程碑 |

版本历史见 [CHANGELOG.md](CHANGELOG.md)。

## 许可证

[MIT](LICENSE) 

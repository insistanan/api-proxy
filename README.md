# api-proxy

**自托管 LLM API 代理网关** —— 多渠道池化调度、五协议统一入口互转、内容安全防护、Web 管理界面，单二进制一体化部署。

[English](README-en.md) | 当前版本：v3.3.0（见 [VERSION](VERSION)）

## 功能特性

- **五协议统一入口**：Claude Messages、OpenAI Responses、OpenAI Chat、Gemini、Images 五种协议互转，客户端用任意协议接入，上游渠道任意搭配
- **多渠道池化调度**：故障转移、渠道级三态熔断、滑动窗口限流与 429 平滑排队、多 BaseURL 健康管理与动态排序、Trace/BaseURL 会话亲和（提升 prompt cache 命中）
- **客户端伪装与一键配置**：Claude Code / Codex / OpenCode / pi-agent / DSH / GPT 配置页，一键把各 CLI 工具指向本代理，伪装版本号可配置
- **内容安全**：敏感数据检测与可逆脱敏（AES-256-GCM）、私有参数递归过滤、危险命令 AI 闸门
- **渠道评测工作台**：探针 / 套件 / 批次回看 / SSE 实时进度
- **观测能力**：按渠道与模型维度的指标统计、请求日志与系统日志（SQLite 存储 + CLI 查询）、拦截记录会话分组
- **一体化部署**：Vue 3 管理界面经 `go:embed` 嵌入 Go 二进制，单端口（默认 3000）同时提供代理 API 与 Web 管理界面

## 快速开始

### Docker（推荐）

```bash
# 修改 docker-compose.yml 中的 PROXY_ACCESS_KEY（必改）后
docker compose up -d
```

关键环境变量：

| 变量 | 说明 |
|---|---|
| `PROXY_ACCESS_KEY` | 访问密钥，**必须修改** |
| `ENABLE_WEB_UI` | `true`（默认）一体化模式 / `false` 纯 API 模式 |
| `LOG_LEVEL` / `ENABLE_REQUEST_LOGS` / `ENABLE_RESPONSE_LOGS` | 日志控制 |

配置持久化在 `./.config`，日志在 `./logs`。

### 从 Release 下载

到 [Releases](../../releases) 下载对应平台产物（linux / macOS / Windows × amd64 / arm64），单文件直接运行，浏览器打开 `http://localhost:3000` 进入管理界面。

### 从源码构建

要求：Go 1.25、Node 20+（或 bun）、make（Windows 下可用 `npm run check` 代替门禁）。

```bash
make build   # 前端构建 → 嵌入 → Go 编译，产物 dist/api-proxy-go
make dev     # 后端热重载开发（前端另行在 frontend/ 下 npm run dev）
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

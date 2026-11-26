# api-proxy

api-proxy 是一个自托管、轻量级、开箱即用的大模型（LLM）API 代理网关。它支持多协议统一接入、跨协议自动转换、多渠道故障转移、共享准入排队控制与可逆脱敏安全风控。管理界面与 Go 后端完全打包在单一可执行文件中，无需配置外部数据库或 Node 服务，部署极其简便。

[English](README-en.md) | 当前版本号见 [VERSION](VERSION)

---

## 核心特性

- **协议接入与转换**：提供 Messages、Responses、Chat、Gemini、Images 五类编排入口，以及 Embeddings、Decisions 原样透传。转换范围取决于入口和上游能力，不能表达的字段或内容会明确报错。
- **渠道与调度**：按分组和配置顺序选渠，支持故障转移、熔断、健康促销、意图路由与会话绑定。相同实际地址、上游协议和 Key 共享并发及队列，发送遵守冷却与请求预算。
- **内容安全**：总开关默认关闭，启用后分别配置规则与阻断或可逆脱敏。危险命令可以直接阻断，也可单独启用 AI 与人工审核；未启用的审核不会默认接管请求。
- **调用观测**：SQLite 保存请求、尝试、上下文快照、Token 与延迟指标，管理台和 `api-proxy logs` 可查询。模型指纹提供参考库内的统计排名，不能作为模型身份认证。
- **个人用量与森林**：独立账本保留启用后的使用足迹，提供年度热力图和每日票据。真实用量、主动专注与确认休息推动森林成长，清理调用日志不会删除手账。
- **客户端与扩展**：管理 Claude Code、Codex、OpenCode、pi-agent、DSH 配置，保存前核对文件修订；Skills 和 MCP 通过托管记录同步，翻译支持后台执行和取消，仓库推送先预览可分享文件。
- **单文件部署**：Vue 管理台嵌入 Go 二进制，默认同用 3000 端口，无需额外数据库服务。构建需要 Node.js，运行二进制无需 Node 服务。

---

## 快速开始

### 方式一：Docker 部署（推荐）

```bash
# 1. 确保当前目录下有 docker-compose.yml
# 2. 将其中的 PROXY_ACCESS_KEY 修改为你自己的强密码
# 3. 启动服务
docker compose up -d
```

关键环境变量说明：

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `PROXY_ACCESS_KEY` | 公开示例值 | **部署前须显式设置自己的密钥**，代理与管理后台共用；当前程序不会因沿用示例值而拒绝启动 |
| `ENABLE_WEB_UI` | `true` | 是否开启 Web 管理后台。设为 `false` 则转为无 UI 的纯 API 代理模式 |
| `PORT` | `3000` | 服务监听端口 |
| `ENV` | `production` | 运行环境模式（production / development） |
| `LOG_LEVEL` | `info` | 控制台基础日志输出级别 |
| `LOG_TO_CONSOLE` | `false` | 是否同时在控制台输出请求正文详细日志 |

> **数据持久化说明**：网关的配置文件（`config.json`）与 SQLite 数据库默认保存在容器工作目录下的 `./.config` 文件夹中。请求超时、请求体限制和 CORS 等策略可在「设置」中调整；日志数据库路径与指标持久化开关需要重启后生效。

---

### 方式二：从 GitHub Releases 下载预编译文件

前往 [Releases](../../releases) 下载对应操作系统与架构（Linux / macOS / Windows × amd64 / arm64）的可执行文件。设置环境变量并启动：

```bash
# Linux amd64 示例（其他平台使用对应文件名）
export PROXY_ACCESS_KEY="your-secure-key"
./api-proxy-linux-amd64

# Windows PowerShell 示例
$env:PROXY_ACCESS_KEY="your-secure-key"
.\api-proxy-windows-amd64.exe
```

启动成功后，在浏览器访问 `http://localhost:3000` 即可进入 Web 管理控制台。

---

### 方式三：从源码编译构建

本地构建需要 Go、Node.js、npm 和 `make`。版本来源见[技术栈](docs/技术栈.md)，Windows PowerShell 构建方式见[开发说明](docs/开发说明.md#常用命令)。

```bash
# 1. 准备前端依赖（在 frontend 目录执行）
cd frontend && npm ci && cd ..

# 2. 一键编译全栈可执行文件（产物生成于 dist/api-proxy-go）
make build

# 3. 运行本地开发热重载（同时启动 Vite 前端与 Air 后端）
make dev

# 4. 提交前执行本地全量门禁检查
make check   # 或 npm run check
```

---

## 核心端点一览

| 接口端点 | 协议类型与主要功能 |
|---|---|
| `POST /v1/messages` | Claude Messages 协议端点，支持长文本推理与流式输出 |
| `POST /v1/messages/count_tokens` | Claude Token 计算端点 |
| `POST /v1/responses` | OpenAI Responses 协议端点，支持 `/compact` 结构压缩 |
| `POST /v1/chat/completions` | 标准 OpenAI Chat Completions 协议端点 |
| `POST /v1/images/generations` | 图像生成端点（支持原生 Images 渠道与 Responses 生图上游） |
| `POST /v1/images/{edits,variations}` | 图像编辑与图像变体生成端点 |
| `POST /v1beta/models/*` | Google Gemini 原生协议端点 |
| `POST /v1/embeddings` | OpenAI Embeddings 向量嵌入透传端点 |
| `POST /v1/decisions` | Decisions 决策判定透传端点 |
| `GET /v1/models` | 可用大模型目录查询端点 |
| `/api/*` | 后台管理控制台 API（请求头需携带 `x-api-key: PROXY_ACCESS_KEY`） |
| `/health` | 服务健康检查端点（公开无须鉴权，供容器健康检查与负载均衡使用） |

---

## 开发与文档导航

| 文档 | 核心内容 |
|---|---|
| [docs/开发说明.md](docs/开发说明.md) | 本地环境搭建、常用命令、门禁机制、Docker 部署与常见坑点 |
| [docs/架构说明.md](docs/架构说明.md) | 系统全景设计、请求生命周期流水线、稳定性控制体系与持久化架构 |
| [docs/规范与不变量.md](docs/规范与不变量.md) | 后端运行状态不变量、前端交互规范、错误处理要求 |
| [docs/技术栈.md](docs/技术栈.md) | 核心技术选型、依赖版本与运行时关键组件速查 |
| [个人森林与用量手账](docs/个人森林与用量手账方案.md) | 当前玩法、数据口径、计时与后续范围 |
| [前端设计系统与主题](docs/前端设计系统与主题.md) | 主题切换、前端目录职责、组件与样式维护 |

完整版本变更记录见 [CHANGELOG.md](CHANGELOG.md)。

---

## 开源协议

本项目基于 [MIT 协议](LICENSE) 开源。

# api-proxy

api-proxy 是一个自托管、轻量级、开箱即用的大模型（LLM）API 代理网关。它支持多协议统一接入、跨协议自动转换、多渠道故障转移、共享准入排队控制与可逆脱敏安全风控。管理界面与 Go 后端完全打包在单一可执行文件中，无需配置外部数据库或 Node 服务，部署极其简便。

[English](README-en.md) | 当前版本号见 [VERSION](VERSION)

---

## 核心特性

- **多协议统一接入与转换**：
  - 对外提供五大主流编排端点（Claude Messages、OpenAI Responses、OpenAI Chat Completions、Google Gemini 以及 Images 图像生成）与原样透传端点（OpenAI Embeddings / Decisions）；
  - 支持跨协议无缝转换，让支持 OpenAI 格式的客户端直接调用 Claude 或 Gemini 上游；
  - 完整支持流式推理、Thinking/Reasoning 思考过程输出与 Prompt Cache 亲和性路由。
- **稳健的多渠道调度体系**：
  - 内部支持 7 大渠道分组（Messages、Responses、Gemini、Chat、Images、Embeddings、Decisions）；
  - 支持基于配置顺序轮询、故障自动切换与三态熔断保护；
  - 支持渠道「健康促销」机制，在指定时间或调用次数内临时优先调度特定渠道；
  - **凭据级共享准入控制**：对指向相同物理服务地址与 API Key 的渠道请求进行全局并发合并与 FIFO 排队，遵守 429 冷却时间与重试预算，彻底防范上游配额击穿。
- **内容安全与隐私脱敏**：
  - 内置敏感凭据检测与 AES-256-GCM **可逆脱敏机制**（请求前自动打掩码，SSE 流式返回时实时无缝还原）；
  - 内置大模型 AI 命令审核闸门，拦截恶意与高风险命令。
- **全方位调用观测与上下文快照**：
  - 采用轻量嵌入式 SQLite 记录请求轨迹、Token 消耗、首字延迟（TTFT）与传输速率（TPS）；
  - 支持多轮请求的**调用聚合（Call Query）**与**上下文快照（Context Snapshot）**分析，直观定位冗余上下文；
  - 针对长会话提供正文内容哈希去重与有界摘要，避免日志库急剧膨胀；
  - 提供独立的命令行日志查询工具（`api-proxy logs`）。
- **客户端一键配置**：
  - 为 Claude Code、Codex CLI、OpenCode、pi-agent、DSH 等主流工具提供可视化配置指引，并支持客户端版本伪装。
- **极简部署形态**：
  - Vue 3 前端静态产物在编译时直接内嵌至 Go 二进制中，单二进制在单端口（默认 3000）上同时提供代理服务与 Web 管理后台。

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
| `PROXY_ACCESS_KEY` | — | **核心访问凭证**，必须修改。代理端点与管理后台均使用该密钥鉴权 |
| `ENABLE_WEB_UI` | `true` | 是否开启 Web 管理后台。设为 `false` 则转为无 UI 的纯 API 代理模式 |
| `PORT` | `3000` | 服务监听端口 |
| `ENV` | `production` | 运行环境模式（production / development） |
| `LOG_LEVEL` | `info` | 控制台基础日志输出级别 |
| `LOG_TO_CONSOLE` | `false` | 是否同时在控制台输出请求正文详细日志 |

> **数据持久化说明**：网关的配置文件（`config.json`）与 SQLite 数据库默认保存在容器工作目录下的 `./.config` 文件夹中。详细的请求超时、请求体限制、CORS 来源及日志持久化开关可在登录控制台后于「设置」页面在线热更新。

---

### 方式二：从 GitHub Releases 下载预编译文件

前往 [Releases](../../releases) 下载对应操作系统与架构（Linux / macOS / Windows × amd64 / arm64）的二进制压缩包。解压后设置环境变量并启动：

```bash
# Linux / macOS 示例
export PROXY_ACCESS_KEY="your-secure-key"
./api-proxy-go

# Windows PowerShell 示例
$env:PROXY_ACCESS_KEY="your-secure-key"
.\api-proxy-windows-amd64.exe
```

启动成功后，在浏览器访问 `http://localhost:3000` 即可进入 Web 管理控制台。

---

### 方式三：从源码编译构建

本地构建环境要求：Go 1.26.10、Node.js 24.16.0（或 Bun 1.4.4）以及 `make`（Windows 用户建议在 Git Bash 中运行）。

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
| [docs/历史说明.md](docs/历史说明.md) | 架构演进阶段与重大设计决策背后的思考 |

完整版本变更记录见 [CHANGELOG.md](CHANGELOG.md)。

---

## 开源协议

本项目基于 [MIT 协议](LICENSE) 开源。

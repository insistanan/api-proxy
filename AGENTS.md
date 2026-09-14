# AGENTS.md

## 项目定位

**api-proxy**（v3.3.0 起由 claude-proxy 更名，MIT 协议）：自托管 LLM API 代理网关。

- 多渠道池化调度与故障转移，五协议统一入口互转（Claude Messages / OpenAI Responses / Gemini / OpenAI Chat / Images）
- 客户端伪装与一键配置页（Claude Code / Codex / OpenCode / pi-agent / DSH / GPT）
- 内容安全：敏感数据检测、可逆脱敏、危险命令 AI 闸门
- 渠道评测工作台（探针 / 评分 / 批次回看）
- Go 后端 + Vue 3 前端，前端产物 `go:embed` 嵌入，单二进制单端口（默认 3000）一体化部署

单 Go module：`github.com/BenedictKing/api-proxy`（Go 1.25），无 go workspace。

## 仓库结构

| 路径 | 职责 |
|---|---|
| `backend-go/` | Go 后端，入口见 [backend-go/AGENTS.md](backend-go/AGENTS.md) |
| `frontend/` | Vue 3 + TypeScript + Vuetify 管理前端，入口见 [frontend/AGENTS.md](frontend/AGENTS.md) |
| `docs/` | 全部详细文档（见下方导航），新增文档一律放这里 |
| `scripts/` | 门禁与构建脚本：`check.mjs`（全量门禁）、`copy-frontend.mjs`（前端产物复制唯一权威入口）、`eol.mjs`（行尾守卫） |
| `.githooks/` | pre-commit 钩子（暂存区行尾规范化） |
| `dist/` | 本地构建产物（不入仓） |
| `CHANGELOG.md` | 中文维护的唯一功能史料，写版本相关内容时以它为准 |
| `VERSION` | 当前版本号（v3.3.0） |

## 文档导航（docs/）

详细内容不内联到本文件，按工作内容先读对应文档再动手：

| 文档 | 内容 | 什么时候读 |
|---|---|---|
| [docs/开发说明.md](docs/开发说明.md) | 环境准备、常用命令、门禁细节、前端产物链路、发布流程、Docker 部署、常见坑 | 日常开发必读 |
| [docs/架构说明.md](docs/架构说明.md) | 整体架构与架构图（Mermaid）、代理请求流转、调度器、前后端交互、数据存储 | 改动跨模块逻辑前必读 |
| [docs/规范与不变量.md](docs/规范与不变量.md) | 代码规范与关键不变量（后端 11 条不变量、前端约定、注释/日志/错误处理风格） | 写任何代码前必读 |
| [docs/技术栈.md](docs/技术栈.md) | 技术栈与依赖总览（根工程 / 后端 / 前端 / 工具链） | 选型、升级依赖、排查版本问题时 |
| [docs/历史说明.md](docs/历史说明.md) | 版本演进史与关键里程碑（v1.x TypeScript → v2.0.0 Go 重写 → v3.3.0） | 理解历史决策、写 CHANGELOG 时 |

## 常用命令

| 命令 | 说明 |
|---|---|
| `make dev` | 后端热重载开发（Air），日常开发主入口 |
| `make build` | 前端构建 → copy-frontend → Go 单二进制，产物 `dist/api-proxy-go` |
| `make check` | 全量门禁；等价写法 `npm run check`（无需安装 make/bun） |
| `make run` | 直接运行当前源码 |

前端命令在 `frontend/` 目录下执行（`npm run dev` / `npm run check` 等），详见 [frontend/AGENTS.md](frontend/AGENTS.md)。

## 硬性约定

1. **本地门禁**：提交前 `npm run check`（或 `make check`）必须全绿——gofmt / go vet / go test / 前端 type-check / 图标扫描 / ESLint `--max-warnings 0`。仓库没有 push/PR 触发的 CI，门禁完全依赖本地执行。
2. **行尾策略**：Windows 工作区 CRLF，Go/shell/`.githooks`/`scripts/*.mjs` 例外强制 LF，由 `.gitattributes` + pre-commit 钩子 + `scripts/eol.mjs` 三层保障，不要手动绕过。
3. **前端产物链路**：`frontend/dist` →（`scripts/copy-frontend.mjs`，唯一权威入口）→ `backend-go/frontend/dist` → `go:embed`。绝不要手工复制 dist（历史上嵌套 dist 曾导致构建事故，`.gitignore` 有兜底规则）。
4. **发布流程**：`version-bump` 项目技能升版本并提交 → push tag `v*` → 三平台（linux/macos/windows × amd64/arm64）release workflow 产出 draft release → `github-release` 项目技能从 CHANGELOG 生成发布公告。
5. **敏感信息**：`.config/`（运行时数据）、`.ccb_config/` 等目录含密钥，绝不能提交或写入文档。

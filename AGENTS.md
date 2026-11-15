# AGENTS.md

**api-proxy**：自托管 LLM API 代理网关，v3.3.0 起由 claude-proxy 更名，MIT 协议。Go 后端与 Vue 管理台合成单个二进制，默认端口 3000。

支持 Messages / Responses / Gemini / Chat / Images 五类编排入口，以及 Embeddings / Decisions 原样透传；转换能力受上游限制。另提供渠道池与故障转移、客户端配置、可逆脱敏和命令安全审核。

## 仓库导航

| 路径 | 职责 |
|---|---|
| `backend-go/` | Go 后端，先读 [后端约定](backend-go/AGENTS.md) |
| `frontend/` | Vue 管理台，先读 [前端约定](frontend/AGENTS.md) |
| `docs/` | 开发、架构和规范文档，新增文档放这里 |
| `deploy/` | 发布用固定资源清单与 schema，如本地向量模型预设 |
| `scripts/` | 全量门禁、前端产物复制、行尾维护 |
| `.githooks/` | 提交前规范化暂存文件的行尾 |
| `.github/` | tag 发布构建与 draft release |
| `.claude/` | 项目技能：version-bump、github-release 等 |
| `dist/` | 本地构建产物，不入仓 |
| `CHANGELOG.md` | 中文功能史料，版本变更以此为准 |
| `VERSION` | 当前版本号的唯一文件入口 |

## 按需阅读

- 日常开发：[开发说明](docs/开发说明.md)。
- 写代码前：[规范与不变量](docs/规范与不变量.md)。
- 跨模块改动前：[架构说明](docs/架构说明.md)。
- 依赖与版本问题：[技术栈](docs/技术栈.md)。
- 历史决策：[历史说明](docs/历史说明.md)。

## 常用命令

| 命令 | 作用 |
|---|---|
| `make dev` | 启动前端 Vite 与后端 Air |
| `make build` | 构建前端、复制产物、编译后端 |
| `npm run check` / `make check` | 全量门禁，含暂存区隔离构建 |
| `make run` | 构建并嵌入前端后运行 Go 源码 |

Windows 无 make 时的构建命令见开发说明。

## 必须遵守

1. 提交前暂存完整改动，并通过全量门禁；仓库没有分支 push / PR 检查 CI。
2. Windows 工作区使用 CRLF；Go、shell、钩子和 `scripts/*.mjs` 使用 LF，遵循 `.gitattributes` 和行尾脚本。
3. 前端产物只走 `frontend/dist` → `scripts/copy-frontend.mjs` → `backend-go/frontend/dist` → `go:embed`，不要手工复制。
4. 发布：version-bump 升版本并提交 → 推送 `v*` tag → workflow 生成 draft release → github-release 从 CHANGELOG 生成公告。普通重构提交不自动升版本。
5. `.config/`、`.ccb_config/` 含运行数据或密钥，绝不能提交或写入文档。

最后核对：v3.3.16。升级版本时核对目录与协议口径；门禁会检查目录登记。

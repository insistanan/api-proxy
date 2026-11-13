# 前端约定

Vue 3 + TypeScript strict + Vuetify 3 管理台。构建产物由 Go 嵌入；完整工具链见 [技术栈](../docs/技术栈.md)，编码规则见 [规范与不变量](../docs/规范与不变量.md)。

## src 目录

| 路径 | 职责 |
|---|---|
| `App.vue`、`main.ts`、`router/`、`plugins/` | 应用入口、路由与插件装配 |
| `app/` | 应用级流程，如认证后刷新渠道；允许组合业务模块 |
| `api/` | 聚合业务接口，页面和 Store 可从 `@/api` 引用 |
| `features/channels/` | 渠道、渠道池、模型映射、意图路由、渠道弹窗状态 |
| `features/observability/` | 对话、日志、上下文快照、命令审核与通知 |
| `features/settings/` | 网关、伪装、提示词、助手与语义安全设置 |
| `features/clients/` | Claude Code / Codex（GPT）/ OpenCode / pi-agent / DSH 配置 |
| `features/assistant/` | 内置配置助手 |
| `features/extensions/` | mcp、skills、datasets，各自维护视图与服务 |
| `shared/` | 通用组件、composables、HTTP / health / version、认证与偏好 Store、工具 |
| `types/` | 前后端契约类型，不依赖业务服务 |
| `styles/`、`assets/` | 应用样式、字体与静态资源 |

业务文件跟随所属 feature，只创建实际需要的 views / components / services 等子目录。shared 不依赖 features、app 或业务 API 门面；跨模块编排放应用入口或 app。业务服务调用 shared HTTP 层，契约类型从 `@/types` 获取。

跨业务静态导入走模块公开出口（index.ts；数据集组件走 datasets/ui.ts），由 ESLint 检查。模块内部、测试和应用装配可直引实现；新增业务模块同步 eslint.config.js 的模块列表。

## 日常约定

- 全局数据用 Pinia；弹窗和设置页维护草稿，保存成功再同步 Store。
- 新图标先在 `plugins/vuetify.ts` 注册；路由视图保留单个真实 DOM 根节点。
- 管理请求走 `shared/services/http.ts`；鉴权流走 `shared/utils/sse.ts`。
- 提示、确认框、剪贴板、日期和图表工具复用 shared；新代码使用 `@/` 别名。
- 七种渠道协议的 `ApiTab` 在 `types/channel.ts`，`API_TABS` 在渠道 service 中。
- `committed: true` 表示配置已落盘，不能自动重试，提示刷新核对。

## 命令（在 frontend 执行）

| 命令 | 作用 |
|---|---|
| `npm run dev` | Vite（5173），代理到后端 3000 |
| `npm run build` | 构建到 dist |
| `npm run check` | 类型检查与图标扫描 |
| `npm test` | Vitest |
| `npm run lint` / `lint:fix` | ESLint；门禁要求零警告 |
| `npm run format` | Prettier |

依赖变更须同步 package-lock.json 与 bun.lock。提交前在根目录执行全量门禁，前端 check 不能替代它。

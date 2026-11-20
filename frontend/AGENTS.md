# 前端约定

Vue 3 + TypeScript strict + Tailwind CSS 4 管理台。Go 嵌入前端构建产物。主题和组件维护方式见 [前端设计系统与主题](../docs/前端设计系统与主题.md)。

## src 目录

| 路径 | 职责 |
|---|---|
| `App.vue`、`main.ts`、`router/` | 应用入口、路由与插件装配 |
| `app/`、`app/layout/` | 应用级流程、顶栏、导航、版本入口；允许组合业务模块 |
| `api/` | 聚合业务接口，页面和 Store 可从 `@/api` 引用 |
| `features/channels/` | 渠道、渠道池、模型映射、意图路由、渠道弹窗状态 |
| `features/observability/` | 对话、日志、上下文快照、命令审核与通知 |
| `features/settings/` | 网关、伪装、提示词、助手与语义安全设置 |
| `features/clients/` | Claude Code / Codex（GPT）/ OpenCode / pi-agent / DSH 配置 |
| `features/assistant/` | 内置配置助手 |
| `features/extensions/` | mcp、skills、datasets，各自维护视图与服务 |
| `shared/` | 通用组件、composables、HTTP / health / version、认证与偏好 Store、工具 |
| `shared/ui/` | 项目维护的基础控件，复杂交互使用 Reka UI |
| `shared/components/` | 跨业务的组合组件，按 layout、overlay、feedback、data、domain 归档 |
| `shared/theme/` | 外观偏好、预设、根节点同步、主题面板、图表颜色适配 |
| `types/` | 前后端契约类型，不依赖业务服务 |
| `styles/`、`styles/theme/` | Tailwind CSS 入口、基础样式、字体声明及主题令牌 |
| `public/` | 需要固定 URL 的 favicon 和字体；有需经 Vite 处理的资源时放 `src/assets/` |

业务文件跟随所属 feature，只创建实际需要的 views / components / services 等子目录。shared 不依赖 features、app 或业务 API 门面；跨模块编排放应用入口或 app。业务服务调用 shared HTTP 层，契约类型从 `@/types` 获取。

`shared` 只放跨 feature 复用的通用组件与能力；只在单个 feature 内多处复用的组件仍归该 feature。不要仅因组件被复用就抽象成通用 API，抽象应能明确消除重复且不隐藏业务语义。组件分类已有多层结构时沿用职责目录，不为单文件新增子目录。

跨业务静态导入走模块公开出口（index.ts；数据集组件走 datasets/ui.ts，纯默认配置走 datasets/config.ts），由 ESLint 检查。模块内部、测试和应用装配可直引实现；新增业务模块同步 eslint.config.js 的模块列表。

## 日常约定

- 全局数据用 Pinia；弹窗和设置页维护草稿，保存成功再同步 Store。
- 新图标先在 `shared/ui/icon/icons.ts` 注册；路由视图保留单个真实 DOM 根节点。
- 使用 Tailwind 语义类与基础控件处理布局和外观。业务特有的选择器和容器查询留在相应 feature 内，引用 `styles/index.css` 中的 Tailwind 配置。
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

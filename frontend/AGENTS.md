# frontend AGENTS.md

本目录为 **api-proxy** 的管理后台前端实现，采用 Vue 3 + Vuetify 3 构建，编译产物在发布时内嵌至 Go 后端。全景系统架构见 [架构说明](../docs/架构说明.md)，交互设计与编码规范见 [规范与不变量](../docs/规范与不变量.md)。

---

## 1. 技术栈与环境版本

- **核心框架**：Vue 3 (^3.5) + TypeScript 5.9（开启 strict 严格模式）+ Vite 7。
- **UI 与样式**：Vuetify 3，采用全量组件注册与按需引入的 `@mdi/js` SVG 图标；支持明暗双主题切换（基于“砚与铜”雅致配色）。
- **状态与路由**：Pinia 3（集成 `pinia-plugin-persistedstate` 本地持久化）；vue-router 4（采用 History 模式，全页面路由懒加载）。
- **网络通信**：原生 `fetch` 统一封装于 `src/services/http.ts` 的 `apiRequest<T>` 中；流式数据通过 `src/utils/sse.ts` 的 `readSSEStream` 鉴权读取。
- **调用观测与上下文快照**：支持请求聚合（LogCalls）、上下文快照（ContextSnapshotPanel）与趋势分析（ContextTrend）。
- **工具链要求**：Node.js `24.16.0`（见根目录 `.node-version`）与 Bun `1.4.4`（见根目录 `.bun-version`）。仓库内同时维护 `bun.lock` 与 `package-lock.json`，修改依赖时需保持两份锁文件同步。

---

## 2. 源码目录结构（src/）

| 目录/文件 | 核心职责 |
|---|---|
| `views/` | 页面视图（如渠道概览 `ChannelsView`、全局设置 `SettingsView`、请求日志 `RequestLogsView`、对话详情 `ConversationsView` 及各客户端配置页） |
| `components/` | 可复用 UI 组件库；`channel/` 存放渠道编辑表单与快速添加弹窗，`settings/` 存放网关、日志、安全等分类设置面板，包含调用聚合与上下文快照组件 |
| `composables/` | 编排页面异步逻辑，持有局部的表单编辑草稿、查询过滤参数及临时校验提示；已保存的全局共享数据统一由 Store 管理 |
| `stores/` | Pinia 状态层：`auth`（鉴权秘钥）、`channel`（渠道分组管理）、`preferences`（用户偏好）、`dialog`、`system`、`assistant`、`logs` 与 `logCalls`（聚合调用） |
| `services/` | 领域接口封装：`http.ts` 负责注入 `x-api-key` 与统一错误解析，`channel-api.ts` 提供 7 大协议通用的 CRUD 操作，`observability.ts` 承接上下文与调用观测 |
| `types/` | 严格 TypeScript 类型声明（按 channel、settings、logs、conversation 等领域细分） |
| `utils/` | 工具库：`sse.ts`（流式事件解析）、`contextPresentation.ts`（上下文呈现辅助）、`quickInputParser.ts` 等 |
| `plugins/vuetify.ts` | Vuetify 插件配置：包含全量组件注册、自定义 SVG 图标映射表（iconMap）与主题声明 |
| `router/` | 路由映射定义：根路径 `/` 默认重定向至 `/channels/messages` |

---

## 3. 核心设计与交互约定

1. **状态唯一真理源与草稿隔离**：
   - 全局共享的领域数据（如渠道列表、全局设置）由 Pinia Store 独占管理；
   - 渠道编辑弹窗或设置页面的修改操作仅变更本地 Composable 草稿；用户保存成功后才向 Store 同步最新数据，点击取消则直接丢弃草稿，避免污染全局状态。
2. **图标显式按需注册**：
   - 为减少生产包体积，`plugins/vuetify.ts` 设置了 `autoImport: false`；
   - 在模板中使用任何新的 `mdi-*` 图标前，**必须先在 `iconMap` 中显式登记导入**，本地门禁 `npm run check:icons` 会严格扫描拦截未注册的图标。
3. **视图根节点单 DOM 元素约束**：
   - 根组件包含路由切换过渡动画 `<transition mode="out-in">`；
   - **所有路由页面（`views/*.vue`）的最外层必须是单个真实的 HTML DOM 容器元素**（如 `<div>`），严禁使用多根节点或 Fragment，否则会导致路由切换时页面彻底变白。
4. **全栈贯穿的 7 大渠道类型（ApiTab）**：
   - `ApiTab` 常量定义于 `services/channel-api.ts`，涵盖 `messages`、`responses`、`gemini`、`chat`、`images`、`embeddings` 和 `decisions`；
   - Store、API 工厂与路由参数校验均与该枚举对齐。
5. **已提交错误（`committed: true`）的处理策略**：
   - 当后端返回的错误标记了 `committed: true` 时，说明配置已经原子落盘但在收尾热重载广播时发生异常；
   - 前端**绝不发起自动重试**，而是给出明确提示，建议用户刷新页面核对最新状态。

---

## 4. 常用开发命令

| 命令 | 说明 |
|---|---|
| `npm run dev` | 启动 Vite 开发服务器（默认端口 5173，自动代理 API 请求至 3000 端口） |
| `npm run build` | 编译前端生产静态资源至 `dist/` |
| `npm run type-check` | 执行 `vue-tsc` 类型安全检查 |
| `npm run check:icons` | 检查模板中所用的 MDI 图标是否全部已在 `vuetify.ts` 中注册 |
| `npm run check` | 本目录门禁：执行 `type-check` + `check:icons` |
| `npm test` | 执行 Vitest 前端自动化单元测试 |
| `npm run lint` / `lint:fix` | 执行 ESLint 校验与自动修复（门禁严格要求 `--max-warnings 0`） |
| `npm run format` | 调用 Prettier 格式化 `src/` 源码 |

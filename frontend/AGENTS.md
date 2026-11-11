# frontend AGENTS.md

本目录实现 api-proxy 的 Vue 3 管理界面，构建产物通过 `go:embed` 嵌入 Go 程序。前后端如何协作见 [架构说明](../docs/架构说明.md)，代码规范见 [规范与不变量](../docs/规范与不变量.md)。

## 技术栈

- Vue 3（^3.5）+ TypeScript 5.9（strict）+ Vite 7 + vue-tsc
- UI：Vuetify 3（全量组件注册，`@mdi/js` 按需 SVG 图标），SCSS
- 状态：Pinia 3 + pinia-plugin-persistedstate（localStorage 持久化）；路由 vue-router 4（History 模式，全部懒加载）
- HTTP 使用原生 fetch，统一封装在 `src/services/http.ts` 的 `apiRequest<T>` 中；`api.ts` 保留原有导入入口。
- 图表 apexcharts + vue3-apexcharts；拖拽 vuedraggable；测试 Vitest
- 依赖可通过 Bun 或 npm 安装。发布 workflow 使用 `bun.lock`，Docker 构建使用 `package-lock.json`，修改依赖后需要核对两份锁文件。
- Node 24.16.0 与 Bun 1.4.4 分别由根 `.node-version`、`.bun-version` 固定。

## 目录结构（src/）

| 目录 | 职责 |
|---|---|
| `views/` | 路由页面，如 ChannelsView、SettingsView、RequestLogsView、ConversationsView 及客户端配置页；完整列表见 `router/index.ts` |
| `components/` | 可复用组件；`channel/` 放渠道表单与弹窗，`settings/` 放设置分区 |
| `composables/` | 编排异步操作，并持有局部编辑草稿、查询状态和校验结果；已保存的共享领域数据由 store 管理（R5/方案 8.1） |
| `stores/` | Pinia store：auth / channel / preferences / dialog / system / assistant / logs；日志详情缓存由 logs store 管理 |
| `services/` | `http.ts` 统一鉴权与解析，领域模块声明业务端点，`api.ts` 组合并保留原导入入口；`version.ts` 检查 Release 版本 |
| `types/` | 按领域拆分类型（channel / client-config / conversation / logs / metrics / model / security / settings / skill / assistant），`types/index.ts` 统一再导出 |
| `utils/` | sse.ts（SSE 流读取）/ quickInputParser/ ccsDeepLink / platform / uuid 等 |
| `plugins/vuetify.ts` | 全量组件注册 + 自定义 SVG iconSet + 双主题（"砚与铜"配色）+ 全局组件默认值 |
| `router/` | History 模式，`/` 重定向 `/channels/messages`，动态路由 `/channels/:type` |

## 架构要点

- 认证由 App.vue 的全局密钥对话框处理，流程在 `composables/useAuthFlow.ts` 中。路由 meta 虽带 `requiresAuth: true`，但没有 `beforeEach` 守卫。认证失败 5 次会锁定 5 分钟，只有 401 响应会使密钥失效，网络错误会保留密钥。
- `ApiTab` 定义 messages、responses、gemini、chat 和 images 五种协议。channel store 使用 `Record<ApiTab, ...>` 保存分组数据，`channelApi(type)` 按协议返回共用操作方法。新增协议时，需要同步修改 store、API 工厂、路由参数校验和后端端点。
- 领域服务共用 `apiRequest<T>`，由 HTTP 层注入 `x-api-key` 并统一处理响应。收到 401 时清除认证状态并抛出 `ApiError`，错误携带 status、details、code 和 committed 等信息。`committed` 表示配置已经提交，但后续处理失败；此时不自动重试，提示用户刷新并查看实际状态，处理逻辑见 `useChannelActions.ts`。
- 需要鉴权的 SSE 通过 fetch 和 `utils/sse.ts` 中的 `readSSEStream` 读取，以便携带访问密钥。
- Dashboard 与图表历史数据使用 `shallowRef` 保存只读快照，更新必须替换根对象。渠道配置继续保持深层响应式，并复用未变对象；分组监听仅覆盖成员与排序字段。波形由 `ChannelActivityChart` 接收稳定派生数组，图表只通过 series props 更新，不重复调用 `updateSeries`。
- **dev 代理**（vite.config.ts）：`/api`、`/v1`、`/v1beta`、`/health` 转发到 `VITE_PROXY_TARGET`（默认 http://localhost:3000）；生产环境 baseURL 固定 `/api`（同域，Go 嵌入部署）。
- 构建分包：manualChunks 拆 charts-vendor / vuetify-vendor / mdi-icons / vue-vendor；路径别名 `@` → src。

## 命令

| 命令 | 说明 |
|---|---|
| `npm run dev` | Vite 开发服务器（端口 `VITE_FRONTEND_PORT`，默认 5173） |
| `npm run build` / `preview` | 构建 / 预览 |
| `npm run type-check` | vue-tsc --noEmit |
| `npm run lint` / `lint:fix` | ESLint（门禁要求 `--max-warnings 0`） |
| `npm run check:icons` | 校验所有 `mdi-*` 图标已在 vuetify.ts 的 iconMap 注册（未注册 exit 1） |
| `npm run check` | 本目录的 type-check + check:icons；全量门禁在根目录运行 `npm run check` |
| `npm test` | vitest run |
| `npm run format` | prettier --write src/** |

## 约定与坑

1. **图标必须先注册**：vuetify.ts 中设置了 `autoImport: false`，使用新的 `mdi-*` 图标前，需要在 iconMap 中手动注册，否则 `check:icons` 会失败。
2. **路由 transition 单根元素**：App.vue 用 `<transition mode="out-in">` 包 router-view，**视图根节点必须是单个真实 DOM 元素**，否则路由切换整块空白（ChannelsView.vue 头部有详细注释）。
3. 注释使用自然、完整的中文，解释状态由谁持有、错误如何处理，以及需要保持的交互约束。保留有效的设计背景和 store 分节风格，去掉失效计划与重复说明，避免过度压缩成术语短句。
4. ESLint 9 flat config：`no-explicit-any: warn`、生产环境 `no-console: warn` / `no-debugger: error`、未用变量忽略 `^_` 前缀。
5. UI 文案全中文（`index.html` lang="zh-CN"，标题"API Proxy · 管理面板"）。
6. 构建产物不要手工处理：`frontend/dist` 由根目录 `scripts/copy-frontend.mjs` 统一复制到 `backend-go/frontend/dist`。

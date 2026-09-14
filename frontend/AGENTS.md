# frontend AGENTS.md

Vue 3 管理前端：api-proxy 的 Web 管理界面，构建产物经 `go:embed` 嵌入 Go 后端单二进制。整体架构图见根目录 [docs/架构说明.md](../docs/架构说明.md)，代码规范全文见 [docs/规范与不变量.md](../docs/规范与不变量.md)。

## 技术栈

- Vue 3（^3.5）+ TypeScript 5.9（strict）+ Vite 7 + vue-tsc
- UI：Vuetify 3（全量组件注册，`@mdi/js` 按需 SVG 图标），SCSS
- 状态：Pinia 3 + pinia-plugin-persistedstate（localStorage 持久化）；路由 vue-router 4（History 模式，全部懒加载）
- HTTP：**无 axios**，全部原生 fetch 封装（`src/services/api.ts` 的 `ApiService.request`）
- 图表 apexcharts + vue3-apexcharts；拖拽 vuedraggable；测试 Vitest
- 依赖安装：bun.lock 与 package-lock.json 两份锁文件并存（本机用 bun，CI 用 bun install，改动依赖后注意两份锁文件的取舍）

## 目录结构（src/，约 87 个文件）

| 目录 | 职责 |
|---|---|
| `views/` | 13 个路由级页面（ChannelsView / SettingsView / EvalView / LogsView 等） |
| `components/` | 26 个可复用组件（最大 ChannelOrchestration.vue、AddChannelModal.vue） |
| `composables/` | 12 个组合式函数——**状态本体在 store（唯一来源），composable 只做编排**（"R5/方案 8.1" 收敛原则） |
| `stores/` | 6 个 Pinia store：auth / channel / preferences / dialog / system / assistant |
| `services/` | `api.ts`（唯一 API 封装层）+ `version.ts`（Release 版本检查，30 分钟缓存） |
| `types/` | 按领域拆分类型（channel / client-config / conversation / eval / logs / metrics / model / security / settings / skill / assistant），`types/index.ts` 统一再导出 |
| `utils/` | sse.ts（SSE 流读取）/ quickInputParser（唯一有单测的模块）/ ccsDeepLink / platform / uuid 等 |
| `plugins/vuetify.ts` | 全量组件注册 + 自定义 SVG iconSet + 双主题（"砚与铜"配色）+ 全局组件默认值 |
| `router/` | History 模式，`/` 重定向 `/channels/messages`，动态路由 `/channels/:type` |

## 架构要点

- **认证是全局对话框模式，不是路由守卫**：路由 meta 虽带 `requiresAuth: true` 但没有 `beforeEach` 守卫；未认证时 App.vue 弹密钥输入 dialog 挡住整个应用，流程在 `composables/useAuthFlow.ts`（5 次失败锁 5 分钟；仅 401 视为密钥无效，网络错误保留密钥）。
- **五种协议类型贯穿全栈**（`ApiTab`：messages/responses/gemini/chat/images）：channel store 收敛为 `Record<ApiTab, ...>`、`channelApi(type)` 工厂按类型查表返回统一方法集（一份实现覆盖五种渠道）。**新增协议类型要同时改多处**：store 的 Record、channelApi 工厂、路由参数校验、后端对应端点。
- **API 层约定**：单例 `ApiService`，自动注入 `x-api-key` 头；401 统一 clearAuth 并抛 `ApiError`（携带 status/details/code/committed——`committed` 表示"配置已原子提交但收尾失败"，此时不自动重试，提示用户刷新，见 useChannelActions.ts）。
- **SSE 需要鉴权**，所以用 fetch + `utils/sse.ts` 的 readSSEStream 而非 EventSource。
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
| `npm run check` | type-check + check:icons，提交前门禁 |
| `npm test` | vitest run |
| `npm run format` | prettier --write src/** |

## 约定与坑

1. **图标必须先注册**：vuetify.ts 中 `autoImport: false`，新用 `mdi-*` 图标要先在 iconMap 手动注册，否则 check:icons 挂掉。
2. **路由 transition 单根元素**：App.vue 用 `<transition mode="out-in">` 包 router-view，**视图根节点必须是单个真实 DOM 元素**，否则路由切换整块空白（ChannelsView.vue 头部有详细注释）。
3. 注释全中文，风格统一：每个文件头部有职责块注释；store 内用 `// ===== 状态 =====` / `// ===== 计算属性 =====` 分节；代码中保留"设计取舍/收敛背景"说明（改动时不要删）。
4. ESLint 9 flat config：`no-explicit-any: warn`、生产环境 `no-console: warn` / `no-debugger: error`、未用变量忽略 `^_` 前缀。
5. UI 文案全中文（`index.html` lang="zh-CN"，标题"API Proxy · 管理面板"）。
6. 构建产物不要手工处理：`frontend/dist` 由根目录 `scripts/copy-frontend.mjs` 统一复制到 `backend-go/frontend/dist`。

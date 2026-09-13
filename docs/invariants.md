# 不变量与铁律

任何代码不得违反。机器能验证的已接门禁（`make check`），其余靠本文件兜底——AI 重构时看到"看似冗余"的校验/上限/顺序，先查这里，绝不擅自合理化删除。

## 仓库层

- 技术文档总是 放在 `docs/`；根目录绝不 新增 README / CHANGELOG / CLAUDE.md / AGENTS.md / LICENSE 之外的文档。
- 配置文件绝不 提交真实密钥；只提交 `*.example`。
- 发布产物总是 走版本注入构建（根 `make build` / CI release，注入 Version/BuildTime/GitCommit）；绝不 裸 `go build` 出 dist exe。
- 未经用户明确要求：绝不 建文档、跑测试、编译打包、执行 git commit/push/branch。

## 后端（backend-go）

- 认证：代理端点总是 经 `ProxyAuthMiddleware` 校验（在 `RunProxyRequest` 第一步）；除 `/health` 外绝不 存在匿名业务端点。生产环境必须设强 `PROXY_ACCESS_KEY`。
- 渠道与管理：五协议的渠道/key CRUD + Ping 总是 复用 `core/channelcrud`；绝不 在协议 handler 里再写一套渠道增删改。
- 调度：渠道选择总是 走 `ChannelScheduler`（顺序：对话路由覆盖 → 按配置顺序选择渠道 → 过滤熔断/挂起渠道）；handler 绝不 自行挑选渠道。
- 故障转移顺序：生产请求在命中渠道池内总是先尝试配置顺序第一的渠道，失败后才依次推进到下一个渠道，再进入兜底分组；促销、亲和、自适应评分、in-flight 负载和稳定散列绝不 改写该顺序。模型映射目标数组同理，首个目标失败后才尝试后续目标。
- 内容安全：钩子管线总是 注入 messages/responses/chat/gemini/images 五协议；新增协议入口必须在 `ProtocolSpec.HookPipeline` 接线并在 `extractSafetySegments` 登记提取器（未登记会显式报错，绝不静默放行），绝不 绕过管线直通。白名单放行只作用于 `tool_result` / `tool_argument` 来源且工具名命中 `ContentSafetyConfig.Whitelist.ToolNames` 的片段；放行**不是静默**，总写一条 `BlockTypeWhitelist` 审计事件到 blocked_store，让拦截记录页可见"已放行"条目。
- 可逆脱敏：`sensitiveData.mode=mask` 时，个人信息与凭据总是 在协议解析后、会话观测和请求日志前先替换为 `[[MASKED:TYPE:STABLE_TOKEN]]` 进程内稳定语义占位符，不注入提示词，并在最终上游请求形成后复检；同一原文跨请求复用稳定标识以保持上游提示词缓存，明文映射只挂当前请求并在统一退出路径显式清理，绝不 写日志、配置或全局缓存，日志也绝不 保留完整占位符。非流式、SSE 与最终透传的上游错误总是 在客户端出口精确还原；仅去掉外围方括号的当前请求已知内部标识也可按完整标识符边界还原，未知值、部分匹配或其他改写绝不 模糊匹配。自定义检测规则（正则）在凭据/个人信息之后执行、复检之前，仅 mask 模式参与（block 模式下启用属配置错误，校验拒绝）；放行字面量（字符串级白名单）与命中片段精确相等时跳过对应检测，整段相等跳过整个 segment，**复检同样过放行字面量过滤**。
- 内容安全失败路径（每条都有明确决策，新增失败路径必须先在此登记再实现）：

| 情形 | 行为 | 位置 |
|---|---|---|
| 检测器初始化失败（配置非法） | 请求报错（ContentSafetyHookError 包裹），不静默放行 | `currentSnapshot` |
| 规则返回无效/重叠区间 | 掩码失败，请求报错 | `Vault.Mask` |
| Vault 掩码写失败（含持久映射库写失败） | 请求报错，不转发（fail-closed） | `applyContentSafetySegments` |
| 持久映射库读失败 | 降级只用进程内映射表，不中断请求 | `MappingStore` 调用点 |
| 还原遇到未知占位符 | 原样下发（精确还原契约），持久化启用时记 `restore_miss` 审计事件 | `Vault.Restore` / StreamRestorer |
| 部分匹配/未知改写 | 原样下发，绝不模糊匹配 | StreamRestorer |
| 凭据/个人信息脱敏后复检仍命中 | 请求报错（掩码不干净不外发）；放行字面量先过滤 | `applyContentSafetySegments` 复检段 |
| 危险命令 AI 闸门判断失败 | 一律拦截（保守默认） | `CmdGate.Review` |
| 客户端断连 | ctx 取消链路，闸门 pending 记 cancelled | `awaitUserDecision` |
| multipart 重编码失败 | 请求报错 | `runMultipartFormSafety` |
| 自定义规则正则命中占位符外的区间 | 接线顺序保证（custom 在内置规则后、无后续掩码步骤），不产生嵌套占位符 | `applyContentSafetySegments` |
- 内容安全流式拦截：`WriteAttachedStreamError` / `WriteAttachedStreamHookError` 写完 error SSE 事件后**必须**补发该协议的流终止序列（messages 发 `message_stop`，responses 发 `response.completed` + ``，chat 发 `data: [DONE]`）。不补终止序列客户端 agent 会一直等 `message_stop`，表现为"卡死、无法中断对话"。
- multipart 请求体：读写一律走 `utils` 的 `MultipartBoundary` / `ParseMultipartParts` / `EncodeMultipartParts` / `ReadMultipartTextFields`；重新编码后**必须**把 `EncodeMultipartParts` 返回的 Content-Type 同步到请求头——沿用旧 boundary 的上游会读到空表单。
- 指标：请求成败/用量总是 经 scheduler 的 `Record*` 入口记录；绝不 在 handler 里新开 `MetricsManager` 手算指标。
- usage 口径：Anthropic 格式出口的 `input_tokens` 总是 uncached 余量，正常缓存字段完整下发，客户端按契约自行求和 `input_tokens + cache_read + cache_creation`；`cache_creation_input_tokens` 是创建总量，5m/1h 字段只是明细，绝不重复相加。Gemini 格式出口的 `promptTokenCount` 总是 **含**缓存并另发 `cachedContentTokenCount`（客户端不求和）。要不要减，**只看上游缓存量的字段名**：`cached_tokens` / `cachedContentTokenCount` 是 subset 式（input 含缓存，必须减），`cache_read_input_tokens` 是 Anthropic 式（上游已拆好，再减就是双减）。所有减法总是 带 `<0` 钳制，且总是 绑定"本次上游报出的原始 input"而非累积字段（流式会对每个 chunk 反复调用，绑定累积值会二次相减）。绝不 在某一条协议路径上单独换口径——四处实现（`normalizeOpenAIUsage`、`captureResponsesUsage`、`chat_to_responses.go`、Gemini 两条流式路径）必须同判据，理由与反证见 `docs/capabilities.md` 治理记录。
- usage output 侧口径：Anthropic / Responses 两家出口的 `output_tokens` 总是 **含**推理 token（`output_tokens_details` 只作明细拆分，客户端绝不 再求和——与 input 侧的求和契约方向相反）；而 Gemini 上游的 `candidatesTokenCount` **不含** `thoughtsTokenCount`。所以从 Gemini usage 转出去时总是 `candidates + thoughts`，绝不 只取 `candidatesTokenCount`（少报推理 token，reasoning 重的场景缺口可达 86%）。五处实现（`converters/parseGeminiUsage`、`converters/responses_stream.go`、`providers/gemini.go` 非流式与流式 `captureUsage`、`handlers/gemini` 的 handler.go + stream.go 指标）必须同口径。`/v1beta` 原生出口只改指标口径，透传客户端的响应体与 SSE 总是 保持 `candidatesTokenCount` 原值（原生客户端按 Gemini 契约自行求和）。三家语义的官方证据见 `types.ClaudeOutputTokensDetails` 注释与 `docs/capabilities.md` 治理记录。
- Responses 续接：`previous_response_id` 与「完整历史作为 input」总是 互斥（官方只承认这两种续接方式）；两者同时发到上游，同一段历史会被按两次语义纳入，上下文与计费翻倍。无法确认 input 只是链上后缀时（本地 session 因重启 / TTL / LRU 驱逐为空、扩展 item 下标对不齐、前缀比对失败），总是 经 `dropPreviousResponseIDIfSelfContained` 按 input 形态定夺：确认自带会话根就删链、按客户端历史建新边界，否则原样保留链。绝不 同时删链又裁剪 input（两头都丢上下文），也绝不 在无法判定时放行两者并存。判据总是 只看 item 形态、绝不 依赖本地 session——session 为空正是最需要它的时刻。
- 流式：上游流总是 经 `HandleStreamResponseCtx`（断连中止）+ `IdleTimeoutReader`（空闲超时）转发；绝不 裸 `io.Copy`。
- Messages 出口 thinking：Chat 上游的 `reasoning_content` / `<think>` `<thinking>` 标签包裹思考、Responses 上游的 reasoning summary，总是 转成 Claude `thinking` content block 下发（thinking 在 text 之前）。绝不 吞掉思考。content 与 reasoning 重复时剥离正文前缀。缓存仍保留供下一轮 Chat 回传补齐。裸 content 无标签的思考无法可靠拆分，不猜测。
- 视觉：图片请求总是 经 `visionlayer.PrepareRequest` 就地处理；绝不 绕过 `prepareRequestForUpstream` 把原始 base64 直接透传上游。
- 日志：总是 `[Component-Action]` 标签、绝不 使用 emoji；上游 key 绝不 完整输出，一律 `utils.MaskAPIKey` 脱敏（标签表见 `backend-go/CLAUDE.md`）。运行日志与请求/响应正文总是 写入 `.config/logs.db`，绝不 再写 `logs/` 文件。请求/响应体、合成流内容总是 跟 `ENABLE_REQUEST_LOGS` / `ENABLE_RESPONSE_LOGS` / `RAW_LOG_OUTPUT` / `SSE_DEBUG_LEVEL` 走；绝不 再绑 `IsDevelopment()`。流量正文存完整 JSON，单条硬顶 1MB，图片 data URL / 内嵌 base64 换成占位符。保留今天与昨天（本地日历）。`ENV`/`NODE_ENV` 未设置时默认 production，只控制 Gin 模式、`/admin/dev/info`、CORS localhost。
- 评测：管理端诊断例外，**只观察不改调度**。发送走 `eval.Sender` + `proxycore.SendRequest`，绝不 走 `RunProxyRequest` / `ConvertToProviderRequest` / `PrepareUpstreamHeaders` / `scheduler.Record*`。未知 extract/judge kind 拒绝入库。真伪套件禁止 rubric。值班只能挂便宜套件。Images / 备用池 / 弃用 / 熔断记 `inapplicable`，不是 fail。`/v1/*` 铁律不变。

## 前端（frontend）

- 图标：新 mdi 图标总是 先在 `src/plugins/vuetify.ts` 的 `iconMap` 注册再使用；绝不 直接写未注册名（`npm run check:icons` 机器拦截）。
- 渠道 API：总是 走 `services/api.ts` 的 `channelApiByType` 工厂；绝不 在组件里拼接裸 fetch 调后端。
- 评测 API：走 `services/api.ts` 的 `api.*Eval*` 方法；`/eval` 页用 `channelApiByType` 拉四协议渠道，绝不 用当前 tab 的 `currentChannelsData`。
- 上游内容渲染：上游返回的 SVG / HTML 总是 当文本插值，或经 `evalSvgPreviewUrl` 走 `<img src="data:...">`；绝不 `v-html`。
- 渠道行信息位：模型映射、评测结论这类必须一眼看到的信息，总是 放在 `.channel-row-meta` 副行并保持 `flex: 0 0 auto`；绝不 塞回主行的名称列——那是 grid 的 `minmax(140px, 1fr)` 单元格，`v-chip` 默认可收缩，宽度不够时文字被压成 0 宽只剩一圈边框，**既不报错也看不出内容**（曾经三项功能都在、用户却以为没做）。
- 指标条语义色：`alarm-breathe`（error 色急促呼吸）+ 警示斜纹总是 只给"故障"档用，即成功率低；缓存命中率低只是省得少，总是 走 `fill-glow-breathe`（currentColor 慢呼吸）。缓存条与成功率条共用 `high/medium/low` 档位类名，覆盖规则总是 写在成功率档位之后并带 `.cache-mmb-fill` 三类特异性；绝不 只靠特异性不看顺序（伪元素上的 `display: none` / `animation-duration` 会被同特异性的后来者盖回去）。

## 完成定义（每个任务，全部满足才算完）

1. `make check` 全绿（后端 gofmt + vet + test；前端 type-check + 图标扫描）。
2. 本次变更影响的 docs 已同步：新概念 → `docs/glossary.md`；新能力 → `docs/capabilities.md`；新铁律 → 本文件；链路变化 → `docs/flows.md`。
3. 没有新增重复代码：`docs/capabilities.md` 已登记的能力一律复用。

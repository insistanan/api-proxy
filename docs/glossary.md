# 术语表（Glossary）

本项目核心领域术语的正宗定义。写码前先查这里，用既有术语，禁止发明平行概念/新表/新名词。

## 后端核心概念

| 术语 | 定义 | 代码位置 |
|------|------|------|
| **上游（Upstream）** | 被代理的实际 AI 服务（Claude / OpenAI / Gemini 等）。一个上游包含 baseURL、API Keys、模型映射。 | `internal/config` |
| **渠道（Channel）** | 上游的编排单元，封装一个上游实例及其健康状态。五种类型：messages / responses / gemini / chat / images。 | `internal/scheduler` |
| **ChannelKind** | 渠道类型枚举，跨层一等公民：驱动调度、指标、路由按类型分发。 | `internal/scheduler` |
| **渠道 CRUD 收敛** | 五协议的渠道增删改查 / key 管理 / Ping 由单份实现提供，经 `RegisterChannelRoutes` 声明式注册（替代历史五组重复实现）。 | `internal/core/channelcrud`、`internal/handlers/channel_routes.go` |
| **API Key** | 上游认证密钥。渠道可挂多个 key，按优先级轮询，失败降级（`MoveAPIKeyToBottomForKind`）。 | `internal/config` |
| **熔断（Circuit Breaker）** | 滑动窗口失败率超阈值后挂起渠道/key，暂停参与调度，可自动恢复也可手动重置。默认：窗口 10 次、阈值 50%、恢复 15 分钟、最小请求数保护 `max(3, windowSize/2)`。 | `internal/metrics/channel_metrics.go` |
| **渠道级熔断器（Channel Circuit）** | 渠道粒度的三态熔断器（Closed/Open/HalfOpen），与 Key 级滑动窗口熔断正交。触发：连续失败 ≥`CIRCUIT_FAILURE_THRESHOLD`(4) 或 样本 ≥`CIRCUIT_MIN_REQUESTS`(10) 且错误率 ≥`CIRCUIT_ERROR_RATE`(0.6)。Open 冷却 `CIRCUIT_COOLDOWN_SECONDS`(60s) 后**惰性**转 HalfOpen（无后台定时器，被查询时转换）；HalfOpen 同时只放 1 个探测请求，探测失败立即回 Open 重新计时，连续成功 `CIRCUIT_SUCCESS_THRESHOLD`(2) 次恢复 Closed。客户端取消与内容审核拦截记 Neutral（只释放探测名额不计健康度）。状态仅内存，重启清零；`CIRCUIT_ENABLED=false` 一键关闭。手动重置 `POST /api/circuit-breakers/reset`。 | `internal/circuit`、`internal/scheduler/selection.go`（拦截）、`internal/handlers/proxycore/multi_channel_failover.go`（记账） |
| **故障转移（Failover）** | key / URL / 渠道失败后按显式顺序切换到下一候选。分三层：模型映射目标、单渠道内 Key/BaseURL、跨渠道；渠道与目标模型均先尝试列表第一项，失败后才推进。 | `internal/handlers/proxycore/upstream_failover.go`（模型映射层）、`upstream_attempt_keys.go`（Key/BaseURL 层）、`multi_channel_failover.go`（跨渠道） |
| **Fuzzy 模式** | 对所有非 2xx 错误都触发 failover 的宽松错误处理模式。 | `config.GetFuzzyModeEnabled` |
| **Trace 亲和性** | 同一用户/会话绑定同一渠道，保证多轮对话上下文连贯。实现按 `userID + kind` 绑定 `channelIndex`。在选渠顺序中作为**兜底**（新对话尚无对话级亲和时沿用）；对话多轮续接优先走对话级亲和。 | `internal/session/trace_affinity.go` |
| **对话级亲和（Conversation Affinity）** | 同一**对话**复用最近一次成功命中的渠道（`Record.LastResolved`），保证多轮会话不来回乱切。粘滞带负载感知：渠道健康且 in-flight ≤ `affinityLoadThreshold`(=3) 才沿用，过载/不健康/失败则放行给负载均衡。优先级高于用户级 Trace 亲和。 | `internal/scheduler/selection.go`（`selectConversationAffinity`） |
| **对话稳定散列（Conversation Hash Spreading）** | 同优先级/评分接近的候选渠道间，按 conversationID 的 FNV-1a 散列做确定性偏移选渠，让不同对话**固定**摊到不同供应商，而非都选当前负载最低——兼顾"分布"与"不来回切"。评分显著更高的渠道仍优先，不被散列掩盖性能。 | `internal/scheduler/adaptive_scheduler.go`（`stableHashOffset` + `selectFromGroup`） |
| **促销渠道（Promotion）** | 促销期内优先调度的渠道，带调用次数配额，用完自动失效。判定顺序先于 Trace 亲和（完整调度顺序见 `docs/flows.md` F1）。 | `internal/scheduler` |
| **渠道池（Channel Pool）** | 渠道的逻辑分组，支持按池调度与拖拽排序布局。 | `internal/config`、`main.go` |
| **性能画像 / 自适应调度** | 按 baseURL+模型统计性能表现，负载均衡时取向性能最优渠道。画像为进程内存，重启丢失。 | `internal/metrics/performance_profile.go`、`internal/scheduler/adaptive_scheduler.go` |
| **对话路由覆盖（Route Override）** | 对指定对话强制绑定渠道的覆盖规则，调度时优先级最高，冲突时返回 409 拒绝。 | `internal/conversation`、`scheduler.ValidateFixedChannel` |
| **内容安全管道** | pre-request / post-response / stream 钩子管线，内置敏感词、凭据、危险命令检测，拦截写 blocked_store。注入 messages / responses / chat / gemini / images 五协议 handler；images 只接 pre-request（含 multipart 表单分支），响应侧是图片不扫描。pre-request 在 vision 前后各跑一次。 | `internal/handlers/hooks/hook_pipeline.go`、`internal/sensitive` |
| **请求级脱敏映射仓（Vault）** | `sensitiveData.mode=mask` 时保存单次请求内的“稳定语义占位符 → 原文”映射，覆盖同请求的重试、渠道故障转移、非流式响应与 SSE；占位符标识在进程内对同一原文稳定，以保持上游提示词缓存，明文映射请求统一退出时显式清理，不持久化、不跨请求还原。 | `internal/sensitive/vault.go`、`internal/handlers/hooks` |
| **白名单放行** | `ContentSafetyConfig.Whitelist` 启用后，`tool_result` / `tool_argument` 来源且工具名命中 `ToolNames` 的片段跳过全部检测维度。放行非静默，写 `BlockTypeWhitelist` 审计事件到 blocked_store。工具名在各协议提取器里提取（messages 用 `tool_use_id` 关联 `tool_use.name`，responses 用 `call_id` 关联 `function_call.name`）。流式阶段无完整工具名上下文，白名单不生效。 | `internal/handlers/hooks/content_safety_segments.go` |
| **视觉分流（Vision Layer）** | 图片请求发送上游前转为分析描述文本：检测 → 描述生成 → 两级缓存（内存+持久）→ 并发去重 → 就地改写请求。视觉渠道选择走独立 `SelectVisionChannel`。 | `internal/visionlayer` |
| **URL 健康排序** | 多 BaseURL 渠道按延迟与失败冷却动态排序选路。默认：失败冷却 30s、连续 3 次失败移末尾（main.go 注入）。 | `internal/urlhealth` |
| **Pi Agent 配置管理** | 管理 Pi Coding Agent 的 providers / credentials / model-settings / backups：原子写（临时文件+rename）、写前备份、跨进程锁、revision 冲突检测。凭据文件 0600。配置目录 `~/.pi/agent`（可 `PI_AGENT_CONFIG_DIR` 覆盖）。路由在 main.go 直接注册 `/api/settings/pi-agent/*`。 | `internal/piagent`、`internal/handlers/pi_agent.go` |
| **模型目录（modelcatalog）** | `/v1/models` 聚合层：静态别名 + 渠道池匹配 + 上游 `/models` 发现，内存目录 + 后台刷新。无独立路由，聚合入口挂在 messages 包 handler 下。 | `internal/modelcatalog`、`internal/handlers/messages/models.go` |
| **评测探针（Probe）** | 一条可入库的检测题：刺激（prompt）+ 封闭抽取 + 封闭判定。存在 `.config/eval.db`，不写死在 Go。 | `internal/eval` |
| **日志库（logs.db）** | 运行日志、流量正文与请求元数据的 sqlite。路径 `.config/logs.db`，与 eval/conversations 并列。`app_logs` 接管原 `app.log`；`traffic_logs` 存客户端请求 / 上游请求 / 上游响应 / 流式合成（完整 JSON，1MB 硬顶，图片占位）；`request_logs` 供 Web 请求日志页。保留今天与昨天。查询入口 `api-proxy logs query/show`。 | `internal/logger`、`internal/cli` |
| **内置题 / 自建题** | 内置题（`builtin=true`）由 `seed.go` 定义，每次启动同步覆盖，UI 与 API 都改不了删不了——判定器和题目必须同版本。要定制就复制成自建题。 | `internal/eval/seed.go` |
| **评测套件（Suite）** | 一组探针。便宜套件才能挂值班；真伪套件禁止 rubric。 | `internal/eval` |
| **评测值班（Watch）** | 全局唯一的便宜套件定时任务。进程重启不立即触发；已有评测在跑则 skip。 | `internal/eval/watch.go` |
| **评测结论聚合（latest-map）** | 按渠道取最近一次评测的聚合芯片：fail > suspect > pass；仅 error/insufficient/inapplicable 为中性；超过 7 天标过期。 | `internal/eval`、渠道行芯片 |
| **inapplicable** | 评测结论：渠道/协议不适用（Images、备用池、弃用、熔断、serviceType 不匹配），不是 fail。 | `internal/eval` |
| **指纹相近（fingerprintTwins）** | 同一批次里两个渠道的随机数直方图余弦相似度 ≥ 0.95，回填进结果 detail 供人看。只是提示，不改 verdict——同源官方中转也会相近。 | `internal/eval/runner.go` |
| **AI 闸门（AIGate）** | 危险命令命中后的二次审查：会话级白/黑名单 → llmgate 大模型判断 → pending 等待窗口（AI 判危险，超时拦截）或放行 + 事后否决窗口。判断失败一律拦截。 | `internal/handlers/hooks/cmd_gate.go`、`internal/sensitive/gate_store.go` |
| **内部 LLM 调用（llmgate）** | 代理自身发起的大模型单轮调用：渠道优先级 messages → responses → chat → gemini，显式指定不回退。闸门与 AI 助手共用。 | `internal/llmgate` |
| **行动协议（Action Protocol）** | AI 配置助手的配置变更协议：模型在回复中嵌入 ` ```assistant-action ` JSON 块，后端白名单执行（create_channel / create_pool / set_model_mapping / update_channel），绝不携带 API Key。 | `internal/assistant/actions.go` |
| **放行字面量（Allowlist）** | 字符串级白名单：条目与命中片段完全相等时跳过全部检测维度（整段相等跳过 segment，子串相等丢弃命中，复检同样过滤）。与按工具名放行的 Whitelist 语义不同。 | `internal/config/config.go AllowlistConfig`、`handlers/hooks/allowlist.go` |
| **自定义检测规则（CustomRules）** | 用户自定义正则，命中走可逆脱敏（用户指定占位符类型前缀）；仅 mask 模式参与，执行顺序在内置规则之后。 | `internal/sensitive/custom_detector.go` |
| **持久映射库（MappingStore）** | 可逆脱敏的加密落盘映射（AES-256-GCM + HKDF 三钥 + HMAC 索引）；mask 模式启用，占位符密钥从主密钥派生使同原文跨重启同占位符。 | `internal/sensitive/mapping_store.go` |

## 协议与流式

| 术语 | 定义 | 代码位置 |
|------|------|------|
| **ProtocolSpec** | 描述协议差异的插槽结构（ParseRequest / BuildUpstreamRequest / HandleSuccess / PreRoute / HookPipeline / AllowContentPolicyChannelFailover）。五协议均经 `RunProxyRequest` 通用骨架执行。 | `internal/handlers/proxycore/protocol.go` |
| **上游适配器（Provider）** | 按 ServiceType 实现的上游接入点：构建上游请求、解析响应、流式处理。`GetProvider(serviceType)` 全集 = `{openai, gemini, claude, responses}`（无 codex）。visionlayer 的视觉描述走独立 `imageAdapterForService`，不共用 Provider 注册表。 | `internal/providers/provider.go` |
| **转换器（Converter）** | 协议格式双向转换，分散在 converters 包多个文件，**非单一工厂**：`factory.go` 仅 Claude 上游走工厂（Resp/Gemini 直接分发）；Responses 主链路在 `responses_protocol.go`；Gemini↔Claude/OpenAI 在 `gemini_converter.go`；Chat↔Responses 在 `chat_to_responses.go` / `responses_to_chat.go`。 | `internal/converters` |
| **Messages 出口 thinking** | Messages 入口把上游推理转成 Claude `thinking` content block 下发，让 Claude Code 在思考区渲染。来源：Chat `reasoning_content` / `<think>` 标签、Responses reasoning summary、Gemini thought。 | `internal/providers/openai.go`、`responses_messages.go`、`gemini.go` |
| **推理内容缓存** | 按 assistant 消息指纹缓存 reasoning，供下一轮 Chat 上游要求回传 `reasoning_content` 时补齐。下发 thinking 不替代缓存。 | `internal/providers/reasoning_content_cache.go` |
| **SSE（Server-Sent Events）** | 流式响应的 `data:` 行传输格式，转发时按协议解析/重建。 | `internal/utils/sse.go`（data 行解析/重建）、`internal/handlers/streams/stream_events.go`（Claude 事件判定与构造） |
| **首字节超时** | `RESPONSE_HEADER_TIMEOUT`（默认 120s）：从发请求到收到响应头的最大等待。 | `internal/httpclient` |
| **空闲超时（Idle Timeout）** | `STREAM_IDLE_TIMEOUT`（默认 300s）：流中两次数据事件间最大间隔，挡"流中挂起"。 | `internal/httpclient/idle_timeout_reader.go` |
| **断连中止** | 流式转发 goroutine `select ctx.Done()`，客户端断连立即中止上游请求，防泄漏。`HandleStreamResponseCtx` 是 Provider 接口方法，五个 provider 均实现。 | `internal/providers`、`internal/handlers/streams/stream.go` |
| **流式合成（StreamSynthesizer）** | 把各上游的流式事件拉平为文本/工具调用序列，**仅用于日志与 Token 观测**；协议 SSE 转换是独立状态机（`converters/responses_stream.go`）。两者经核实非重复（encoder/decoder 互逆对偶，输入域互斥），见 capabilities.md。 | `internal/utils/stream_synthesizer.go` |
| **缓存键规范化（canonical JSON）** | 结构化 JSON 归一化后哈希作缓存键（键排序、稳定序列化）。已收敛为 `utils.CanonicalJSON` 单一实现（原 `providers/responses_messages.go` 与 `converters/responses_protocol.go` 各一份，v3.0.0 合并）。 | `internal/utils` |

## 前端

| 术语 | 定义 | 代码位置 |
|------|------|------|
| **ApiTab** | 前端渠道页签枚举（messages / responses / gemini / chat / images），与后端 ChannelKind 对应。 | `stores/channel.ts` |
| **渠道 API 工厂** | `channelApiByType`：五协议共享同一 API 调用封装。 | `services/api.ts` |
| **Composable** | Vue 3 组合式函数，抽取跨组件复用逻辑（自动刷新、主题、计数组件）。 | `composables/` |
| **图标注册表（iconMap）** | mdi 图标需先在 `iconMap` 注册才能使用；`npm run check:icons` 机器校验。 | `plugins/vuetify.ts` |
| **系统日志** | 日志页内 Tab：按 `requestId` 聚合 `traffic_logs`，展示一次打进代理的大模型请求的原请求 / 发给上游 / 上游返回。不是 `app_logs`。深链 `?tab=system&id=`。 | `views/RequestLogsView.vue`、`components/SystemLogsPanel.vue` |

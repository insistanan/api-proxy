# 关键数据流

顺序感文档：改代理链路前先读这里。每个流程标注了关键代码位置，细节以代码为准——本文件只描述"顺序与分工"，代码描述"具体实现"。

## F1 统一代理主链路（messages / chat / images / gemini 四协议）

五个协议收敛到同一个骨架 `proxycore.RunProxyRequest`（协议差异由 `ProtocolSpec` 描述：ParseRequest / BuildUpstreamRequest / HandleSuccess / PreRoute / HookPipeline / AllowContentPolicyChannelFailover）。

```
1. 认证        ProxyAuthMiddleware（RunProxyRequest 函数内第一步；Web UI 路由另有 WebAuthMiddleware）
2. 读体        ReadRequestBody
3. 解析        spec.ParseRequest（得 model/stream/prompts）
4. 钩子挂载     AttachHookPipeline（内容安全管线）
5. 会话观测     ObserveConversationRequest（对话注册、attempt 记录、绑定 userID）
6. 原生日志     LogOriginalRequest（脱敏）
7. 指定渠道     body 带 channel_index → 直接单渠道分支（快捷测试带 purpose: quick_test 或 X-Proxy-Purpose 头时跳过 ModelMapping 与 DefaultModel 重定向，直接使用请求模型）
8. PreRoute    spec.PreRoute（协议特有前置路由；目前仅 chat 使用，为 modelcatalog 路由）
9. 分派         IsMultiChannelModeForModel → 单渠道 或 多渠道 HandleMultiChannelFailover
```

单渠道尝试（`UpstreamAttempt.TryWithModelMappingFailover`，多渠道模式下每个候选渠道内重复此循环）：

```
选 key（GetNextAPIKey）
→ 构建上游请求（spec.BuildUpstreamRequest，含模型映射）
→ prepareRequestForUpstream：内容安全 pre-request 钩子 → visionlayer.PrepareRequest → 内容安全钩子二次
→ SendRequest
→ 失败：兼容性与整流重试（prompt_cache_key 移除、reasoning_content 补齐、Unicode 转义、Thinking Budget 约束整流、Thinking Signature 块自愈、模态拒绝图片纯文本降级）→ 按状态码/错误分类（ShouldRetryWithNextKey；Fuzzy 模式下所有非 2xx 都转移）
→ 成功：spec.HandleSuccess（非流式 2xx 错误信封嗅探拦截；Messages 流式经 prime-then-commit 预读首包健康校验后再提交响应头）
       → post-response 钩子 → scheduler.RecordSuccessWithUsage → 会话/对话成功标记
```

渠道选择顺序（`SelectChannel`，见 `internal/scheduler/selection.go`）：

```
1. 对话路由覆盖（Route Override）— 最高优先级，冲突 409
2. 当前命中渠道池内按配置优先级选择第一个可用渠道
3. 当前渠道失败后，按同一池的配置顺序依次选择下一个渠道
4. 命中渠道池全部不可用后进入兜底分组，并按兜底分组配置顺序尝试
仍过滤熔断/挂起渠道，渠道内多 BaseURL 按 urlhealth 延迟排序。
```

渠道级三态熔断（`internal/circuit`）叠加在第 2/3/4 步的健康过滤之上：Open 渠道直接跳过；Open 冷却到期后惰性转 HalfOpen 并限量放行 1 个探测请求（探测失败立即回 Open，连续成功 2 次恢复 Closed）。渠道级记账在跨渠道 failover（`HandleMultiChannelFailover`）与单渠道出口（`handleSingleChannelProxy`）按渠道整体结果记录——成功记 Success、可重试失败记 Failure、客户端取消与内容审核转移记 Neutral（只释放探测名额不计健康度）。`CIRCUIT_ENABLED=false` 一键关闭；运行时状态经 `GET /api/circuit-breakers` 查询、`POST /api/circuit-breakers/reset` 手动重置。

模型映射目标列表同样遵循显式顺序：管理端 `ModelMappingEditor` 支持拖拽
`modelMapping[source]`，请求先尝试第一个目标模型，失败后才尝试后续目标。

## F2 视觉旁路（最不透明的链路段）

位置：**单渠道尝试循环内部**、上游请求构建之后（`prepareRequestForUpstream`，见 `handlers/proxycore/upstream_attempt_keys.go`）。

- `visionlayer.PrepareRequest` 就地改写上游请求：图片 → 分析描述文本（按目标协议替换为文本块）。
- 缓存两级：进程内存 + 持久（经 scheduler 按会话/kind/模型落存储）；同图并发去重（claim 机制）。
- 需要分析时用**独立的视觉渠道选择**（`SelectVisionChannel`），不受主请求渠道绑定约束。
- 改消息/图片相关处理时：**绝不绕过 `prepareRequestForUpstream` 直接改 `spec.BuildUpstreamRequest` 的产物**，否则图片请求会漏掉分流与缓存。

## F3 /v1/models 聚合

静态模型别名（opus/sonnet/haiku/gpt/codex/gemini 等）+ 渠道上游 `/models` 发现（modelcatalog 的 parse*）+ 池匹配（family/后缀）→ 合并去重返回。入口在 messages 包 handler（modelcatalog 无独立路由）。

模型名按客户端发来的**原样**参与上游映射匹配，代理不对后缀（历史上的 `[1m]`）做任何剥离或改写：`config.redirectModelList` 依次尝试 `*` 通配 → 精确匹配 → 双向 `Contains` 模糊匹配，全不命中则原样发往上游。需要区分同一别名的不同变体时，把完整名字配成 `ModelMapping` 的键。

## F4 内容安全（HookPipeline）

阶段：pre-request / request-time / post-response / stream。管线在 main.go 组建（`NewContentSafetyPipelineWithRecorder`），注入 **messages / responses / chat / gemini / images 五协议** handler。检测实现在 `sensitive` 包（敏感词/个人信息/凭据/危险命令），拦截写 blocked_store（前端 Blocked Logs 视图）。协议解析后先在会话观测与请求日志之前执行一次；最终上游载荷在 vision 前后再查（vision 改写后复检）。个人信息与凭据由全局 `sensitiveData.mode` 统一选择 `block` 或 `mask`——请求方向（applyContentSafetySegments）、流式响应工具参数（scanAttachedStreamCredentials）与非流式响应工具参数（post-response hook）三处同口径：block 拦截，mask/audit 放行（响应方向段不可改写，mask 在响应方向等价"放行 + 留痕"）；请求方向可变段 mask 真正替换为占位符。`credential.userInputMode / toolResultMode / toolArgumentMode` 是退役的兼容字段，运行时不读取。

### 吸收自 Veil 的三个增强（F4c）

- **放行字面量**（字符串级白名单，`ContentSafetyConfig.Allowlist`）：条目与命中片段**完全相等**时跳过检测——整段相等跳过整个 segment（写 `allowlist` 审计），子串相等丢弃该命中（info/credential/custom 三类命中均过滤，**复检同样过滤**，否则放行值会被复检误报"脱敏不干净"）。出厂含 `127.0.0.1` / `localhost` / `0.0.0.0` / `::1`。与工具名白名单（`Whitelist.ToolNames`）语义不同，勿混用。
- **自定义检测规则**（`ContentSafetyConfig.CustomRules`，正则）：命中走可逆脱敏（占位符类型前缀用户指定，如 `CODENAME`），**仅 mask 模式参与**（block 模式下启用被校验拒绝）。执行顺序在凭据/个人信息**之后**、复检之前——内置规则先占区间，且后续无其他掩码步骤，宽正则不会把占位符再包一层。
- **持久映射库**（`sensitive.MappingStore`，mask 模式启用时 main.go 注入）：映射 AES-256-GCM 加密落盘 `.config/redaction-mappings.db`，主密钥 `.config/master-key`（0600，自动生成），HKDF-SHA256 派生 enc/mac/placeholder 三把子钥；等值索引用 HMAC（非裸哈希，防只偷 DB 的彩虹表攻击）。占位符密钥改由主密钥派生 → **同原文跨重启同占位符**，历史会话旧占位符经 `Vault.PreloadPersistent`（非流式在 Restore、流式在每事件 data 载荷）批量预载还原；还原 miss（如密钥轮换）原样下发 + `restore_miss` 审计。TTL 90 天 + 10 万行 LRU。mode 热切换由 15s 轮询跟随（mask→block 撤销注入，停写不停读）。全部失败路径见 `docs/invariants.md` 决策表。`mask` 使用 `[[MASKED:TYPE:STABLE_TOKEN]]` 进程内稳定语义占位符且不注入提示词，避免上游提示词缓存因随机标识失效；上游响应在最终客户端出口按当前请求映射精确还原。

images 的两点差异：① 请求体有 JSON 与 multipart/form-data 两种形态，后者走 `contentSafetyPreRequestHook.runMultipartFormSafety`（检查全部非文件文本部件；掩码改写后重编码表单，**新 boundary 同步回 Content-Type**），文件部件不扫描——图片内容由 vision 层负责；② 不接 post-response / stream 钩子：响应是 base64 图片或 URL，扫描多 MB base64 无检出收益。

白名单（`ContentSafetyConfig.Whitelist`）：启用后 `tool_result` / `tool_argument` 来源的片段若携带的工具名命中 `ToolNames`，则跳过全部检测维度（敏感词/凭据/敏感信息），但仍写一条 `BlockTypeWhitelist` 审计事件到 blocked_store，让拦截记录页可见"已放行"条目。工具名在各协议提取器里提取：messages 用 `tool_use_id` 关联前一条 assistant 的 `tool_use.name`；chat 的 `tool`/`function` role 消息有 `name`，assistant 的 `tool_calls.function.name`；responses 用 `call_id` 关联 `function_call.name`；gemini 的 `functionCall.name` / `functionResponse.name`。流式阶段（post-response stream）逐 chunk 拼接，无完整工具名上下文，白名单不生效。

流式拦截终止：`WriteAttachedStreamError` / `WriteAttachedStreamHookError` 在写完 error SSE 事件后补发该协议的流终止序列（messages 发 `message_stop`，responses 发 `response.completed` + ``，chat 发 `data: [DONE]`，gemini/images 无统一终止标记靠连接关闭）。不补终止序列客户端 agent 会一直等 `message_stop`，表现为"卡死、无法中断对话"。

流式还原：协议转换后的 SSE JSON 按字段路径和协议逻辑索引维护独立 `StreamRestorer`，正文、推理与并行工具参数不会串流；只暂存可能组成当前请求已知占位符或其去方括号内部标识的尾部前缀。终止事件前先释放未闭合前缀；完整占位符和模型仅去掉外围方括号后的已知内部标识替换为原文，未知值、部分匹配或其他改写原样下发。

### 危险命令 AI 闸门（AIGate）

危险命令命中后不再一律拦截（`hooks/cmd_gate.go` 的 `CmdGate`，main.go 组装注入内容安全管道；存储 `sensitive.GateStore` 与拦截记录同库）。启用条件：危险命令检测 + `dangerousCmd.aiGate.enabled` 同时开启。决策顺序：

```
命中（流式 Feed / Flush / 非流式 post-response，统一入口 gateReviewDangerousCommand）
→ 1. 会话级白/黑名单（gate_decisions 表，conversationID 维度最新决定，后决定覆盖先决定）→ 直接放行/拦截
→ 2. 会话级 AI 审查去重（CmdGate.aiReviews，进程内 conversationID → 命令骨架 normalizeGateCommandKey → 最近一次 AI 结论；骨架忽略参数/路径/文件差异，保留命令名、已知子命令与管道结构）：
     该骨架之前判 allow → 直接放行（不再调大模型）
     该骨架之前判 block → 跳过模型调用，直接进等待窗口
     骨架不同的命令仍各自送审
→ 3. AI 二次判断（llmgate 调用，见下；结论写入 aiReviews 去重缓存）：
     失败（无渠道/超时/解析失败）→ 一律拦截（保守默认）
     verdict=block → 注册 pending 挂起请求，等待窗口（waitSeconds，默认 10s）内用户可放行，超时/客户端断连即拦截
     verdict=allow → 立即放行 + 注册同长度的事后否决窗口（用户加黑名单 → 终止当前流 + 写黑名单；超时只影响后续）
```

- **挂起语义**：非流式响应在写回前整段就绪，Review 同步阻塞即挂起；流式响应命中点在 `FeedAttachedStreamText` / `FeedAttachedStreamToolArgumentsForKey` 内（SSE 转发循环中），阻塞即暂停转发（客户端 idle 超时默认 300s，远大于等待窗口）。同一命令在同一请求内放行一次后不再送审（attached.allowedCommands，防止流式扫描器重复命中）。
- **事后否决**：放行后 `StreamVetoGuard`（attached 上，每请求一个）经 `AttachedStreamVetoDone` 暴露给四协议流式转发循环（messages 走 select 分支，responses/chat/gemini 同步循环顶部非阻塞检查），触发即走 `HandleAttachedStreamVeto` 写错误 SSE + 终止序列。非流式响应放行后不可撤回，黑名单只对后续生效。
- **管理 API**：`GET /api/security/gate/pending`（前端全局轮询）、`POST /api/security/gate/pending/:id/decision`、`GET/POST/DELETE /api/security/gate/decisions`（POST 为拦截记录页「移入白/黑名单」入口；DELETE 解除名单同时失效该会话的 AI 审查去重缓存）。前端通知组件 `GatePendingNotification`（声音 Web Audio 合成 + 气泡 + 可选浏览器系统通知，设置页开关，localStorage 偏好）。
- **审计**：`dangerous_cmd_ai`（AI/名单/否决路径的拦截）、`dangerous_cmd_allow`（放行留痕），与既有 `dangerous_cmd`（闸门关闭时的纯规则拦截）区分。危险命令类审计事件带结构化 `command` 字段；拦截记录页支持「危险命令相关」类别筛选（category=dangerous_cmd 命中三种危险命令事件）与「仅活跃会话」筛选（activeOnly=true，会话正在发送或近 30 分钟有请求）。
- **流式性能**：工具参数检测按增量触发（累计新增 ≥1KB 或片段含 `}`/`]` 才全量扫描，流结束 Flush 补扫兜底），凭据检测器与处置模式在扫描器初始化时固化，消除逐 chunk 的配置深拷贝与 O(n²) 全窗口重扫；pre_request 钩子按请求体指纹缓存结果，同一请求的多次执行（vision 层前后 / 故障转移重试）只扫一次。

## F4b 内部 LLM 调用与 AI 配置助手

`internal/llmgate` 提供"选一个大模型渠道并发一段提示词"的通用能力：`PickChannel`（显式 channelId 只认该渠道、不回退，解析不出模型的渠道显式报错；model 非空时按 messages → responses → chat → gemini 顺序复用主链路编排选渠——`scheduler.SelectChannel` 的分组匹配 → 优先级 → 熔断/健康过滤 → 兜底分组与组内降级全部生效，prompt.Model 作为分组匹配词参与路由，失败渠道可经 `failedChannels` 参数排除以支持跨渠道重试；**model 为空（自动模式）时不走分组路由**——主链路空模型语义只进兜底分组，挂在命名分组的渠道会被整体跳过，与内部调用"按四协议优先级找一个能用渠道"的意图不符，故改为按 messages → responses → chat → gemini × 配置顺序全池扫描，取第一个"可用 + 能解析出模型"的渠道（解析不出模型的渠道——无 DefaultModel 且映射不命中——跳过））+ `Caller.Call`（非流式单轮，provider 转换链复用 visionlayer 同款约定——providers 统一以 Claude Messages 请求体为输入；URL×Key 循环、两层重试判定、metrics 记账与 vision 同形。自动模式下渠道级失败——模型解析失败、渠道整体尝试失败——会带着 failedChannels 换渠道重选，上限 `maxChannelAttempts=4`；显式 channelId 不换渠道）+ `Caller.PreviewChannel`（仅供探测）。资源释放铁律：选渠取得的许可句柄（`SelectedChannel.Permit`，持有 in-flight 预留与可能的半开探测名额，R2-b）必须在调用结束/探测结束时收尾——`Call` 经许可按结果记账 Success/Failure（取消记 Neutral）后释放，`PreviewChannel` 恒立即释放（中性名额）；漏收尾会把渠道卡死在 HalfOpen（表现：状态栏显示该渠道、实际请求却选不中它）。许可绑定取得时的熔断器状态代次，手动重置后的迟到结果不释放新名额。消费方：危险命令 AI 闸门（F4，闸门 prompt 不带模型 → 全池扫描）、AI 配置助手。

AI 配置助手（`internal/assistant` + `handlers/assistant.go`，管理端 `/api/assistant/*`）：右下角浮窗（`AssistantWidget.vue` + `stores/assistant.ts`），会话持久化 `.config/assistant.db`。系统提示词 = 配置手册（包内常量，覆盖协议/分组/兜底/映射概念）+ 当前配置动态摘要（**API Key 一律掩码，绝不进提示词**）+ 行动协议。模型在回复中嵌入 ` ```assistant-action ` 代码块，后端白名单执行（create_channel / create_pool / set_model_mapping / update_channel），全部走 ConfigManager 既有校验；行动参数不含任何 Key 字段，更新渠道永不触碰 APIKeys。首版非流式同步回复。模型路由：`settings.assistant.model` 取渠道分组的匹配词作为模型 ID 走主链路编排（空 = 自动，走兜底分组），不单独维护默认模型；`settings.assistant.channelId` 显式指定渠道（空 = 自动，按四协议优先级；指定后不回退不换渠道，解析不出模型的渠道在探测期即报错）；`GET /api/assistant/status` 返回当前配置的分组、生效渠道与渠道选择器候选列表（设置页与浮窗展示，浮窗与设置页均可切换渠道，model 与 channelId 必须整组提交）。

## F5 responses 链路

`/v1/responses` 已并入 `RunProxyRequest` 主骨架（F1），协议差异全部经 `ProtocolSpec` 闭包表达：BuildUpstreamRequest 内走 `converters` 转换（主分发在 `responses_protocol.go`，仅 Claude 上游走 factory.go 工厂）、`session.SessionManager` 会话管理（previous_response_id 链）与流式事件转换（`converters.ConvertUpstreamStreamLineToResponses`）都在 HandleSuccess/Provider 回调内完成。会话记录 ID 经 `utils.ContextKeyConversationUserID` 从骨架传入回调。多渠道模式下内容审核错误跨渠道转移（`AllowContentPolicyChannelFailover`），单渠道不生效。`/v1/responses/compact` 是唯一独立于主骨架的端点。

### 历史边界（压缩 / 换根）

Responses 只承认两种续接：完整历史作为 input，或只发新增输入并用 `previous_response_id` 链接。两者同时到达上游时同一段历史被算两次，上下文与计费都翻倍，客户端读到的 usage 压缩后不降反升，于是压缩后立刻又要压缩。

上下文压缩有两种来源，检测方式不同：
- **服务端压缩**：`/v1/responses/compact` 产出显式 `type:"compaction"` item，`utils.ResponsesRawItemsContainCompaction` / `ResponsesItemsContainCompactionFromInput` 一眼可辨。
- **客户端压缩**（Cursor）：客户端静默重写自己的 transcript——把中间若干轮换成一条摘要、再原样重发最近几轮，没有任何结构标记。此时 input 比本地历史更短且可能不以 user/developer 开头，按条数或按形态都判不出来，只有与本地 session 的**重叠**能认出来（合法增量只发服务端链上还不存在的 item）。判据与保守规则见 `docs/capabilities.md` 的「Responses 续接边界判定」。

`providers/responses.go` 在转换前判定边界：命中则清空 `PreviousResponseID`、从请求体删掉 `previous_response_id`、把 `sess` 置 nil（旧 session 只用于响应完成后替换本地状态，不能参与本次转换），并置 `ContextKeyResponsesHistoryBoundary` / `ContextKeyResponsesPreviousIDDropped`。这两个 context key 下游驱动三件事：`prepareResponsesUsageRequestBody` 的 usage 校正门控、session 侧走 `ReplaceSessionAfterBoundary` 而非 `CommitTurn`、以及 `clearResponsesPreviousIDs`。同一请求可能在多渠道/多 Key 间 failover，两个 key 在每个候选开始时都会重置，避免前一个候选的状态污染后续候选。

usage 侧另有一条独立的循环成因：OpenAI 兼容上游（grok-4.6 实测）的 `input_tokens_details.cached_tokens` 是跨请求单调递增的累积器，原样下发会让客户端一直看到满缓存。剥离判据见 `docs/capabilities.md` 的「累积式缓存统计剥离」。

## F6 评测原生发送（观察，不进生产调度）

管理端 `/api/eval/*`（`WebAuthMiddleware`）。与 F1 并列，**不**走 `RunProxyRequest`。

```
FindChannelByID（稳定 UUID，跨五类切片）
→ 空 http.Header + SetAuthenticationHeader / SetGeminiAuthenticationHeader
  + 可选 ApplyClaudeCodeDisguise / ApplyCodexDisguise
→ 按 serviceType 组原生 body（claude messages / openai chat / gemini generateContent / responses）
→ 第一 BaseURL + ResolveUpstreamProxyURL + proxycore.SendRequest
→ Extract（封闭 kind）→ Judge（封闭 kind；rubric 再发一封给同一渠道）
→ 写 eval.db，批次结束后做跨渠道随机数指纹比对（只回填 detail，不改 verdict）
→ 更新 latest-map
```

默认非流式。模型：请求覆盖优先（走 `ResolveUpstreamModel` 映射，但清掉 DefaultModel，免得覆盖值被吞）；否则渠道 `defaultModel`。值班 ticker 到期触发普通 `eval_runs`（`trigger=watch`）；Runner 忙则 skip。
前端进度走 `GET /api/eval/runs/:id/events`（SSE），后端只在 status / 已完成格子数变化时发帧，终态自动断开。

## 模块间通信约定

- 同 JVM：包间直接调用；跨层依赖在 main.go 组装注入，不在业务包内 new 全局单例。
- 持久化：metrics / session / blocked 各自带 sqlite_store，路径由 main.go 注入。
- 日志库：`.config/logs.db`，由 `logger.Setup` 在 main.go 注入；运行日志 / 流量正文 / 请求元数据三表，保留今天与昨天。Web 日志页「系统日志」Tab 按 requestId 看三段正文（`GET /api/system-logs`）。
- 评测库：`.config/eval.db`，由 `eval.NewService` 在 main.go 注入；关闭顺序在调度器 Stop 之后。
- 路由总表：main.go 是路由唯一真相；文档不复制路由清单。

# 版本历史

> **注意**: v2.0.0 开始为 Go 语言重写版本，v1.x 为 TypeScript 版本

---

## [v3.3.6] - 2026-10-26

### 新功能

- **Images Responses API 渠道** — Images 入口支持转发到 Responses API 的 `image_generation` 工具，兼容 JSON、multipart 和 SSE 响应，并保留图片用量统计
- **Images 渠道模型配置** — Images 渠道可选择 Images API 或 Responses API；模型重定向支持图片模型候选与手动输入，Responses API 新增 `gpt-image-2.5` 候选

### 修复

- **网络连接稳定性** — 延长 TLS 握手超时并增加握手重试，识别 Windows 客户端主动断开，避免已成功的上游请求被误记为渠道失败
- **响应头处理** — 过滤上游逐跳响应头和 CORS 头，避免与网关 CORS 配置冲突导致浏览器断开连接

### 界面优化

- **日志页面布局** — 左侧日志列表和右侧详情改为独立滚动，避免长列表把详情内容推到屏幕下方；窄屏调整为上下分区

## [v3.3.5] - 2026-10-25

### 修复

- **Responses 上下文 token 口径** — 对上游提供的 `context_details.input_tokens` 优先使用当前上下文规模，避免 Grok 累计计费用量触发 Codex 客户端频繁压缩；原始 usage 仍保留用于指标与计费

## [v3.3.4] - 2026-10-25

### 修复

- **Images 流式透传** — 请求显式设置 `stream=true` 时，即使上游没有返回 `text/event-stream`，代理也会逐块刷新转发，避免 partial image 事件被缓冲
- **Responses 原生生图字段保留** — 原生 Responses 生图请求继续按原始 JSON 局部改写模型，保留 `n`、`size`、`quality`、`background`、`partial_images`、`input_image_mask` 等官方工具参数
- **生图请求超时** — Images 请求和 Responses `image_generation` 请求使用 10 分钟请求/流空闲窗口，减少长耗时生图被代理默认 5 分钟限制中止

---

## [v3.3.3] - 2026-10-25

### 修复

- **上传附件不再把上下文估成 300%** — Messages / Responses 本地 token 估算不再把 PDF、图片等 base64 当成长文本。图片按 1600、文件按 4096 定额；请求含不透明媒体时，不再用本地估算覆盖上游 usage。纯文本长对话的双向错报校正保持不变

---

## [v3.3.2] - 2026-10-12

### 变更

- **回滚渠道选取** — 撤销 v3.3.1 把渠道锁在分组第一位、并拿掉失败率过滤的改动。选渠恢复为按配置顺序跳过不健康渠道：动态分数只展示、不重排；Fuzzy 不再决定失败后是否换渠道。熔断恢复连续失败和错误率两条路径（默认连续 4 次，或错误率 0.6 且至少 10 次，冷却 60 秒），`CIRCUIT_ERROR_RATE` / `CIRCUIT_MIN_REQUESTS` 重新生效

- **Claude Code auto mode 透传** — Claude→Claude 继续转发请求头和未知请求字段（含 `safeguards`）。非流式响应不再用结构体重编码丢掉 `safeguard_results` 等未知字段；usage 只在估算、虚假值补全或错报校正时改对应字段。SSE 未知事件同样留下。不改写 tool-use ID。openai / gemini / responses 上游仍是协议转换，做不了服务端分类器

### 修复

- **2xx 信封检测读失败仍关闭响应体** — 回滚选取逻辑时保留这一处：读取失败或超过上限后 body 不再交给成功路径，必须显式 Close，否则连接不归还连接池
- **DSH providerKeys 不再序列化为 null** — settings 缺失、providers 为空或请求里带了脏 key 时，读取和保存共用同一顺序：文件里实际存在的 key 优先，过滤不存在的 key，其余按字母序补齐；空结果返回 `[]`。避免前端 `[...providerKeys]` 抛出 “undefined is not iterable”

---

## [v3.3.1] - 2026-09-24

### 变更（渠道编排规则收敛）

- **渠道内尝试层级固定** — Key（外层）→ 模型候选（中层）→ 重试次数（内层）：同一个 Key + 同一个模型候选连续失败 5 次才切换下一个模型候选，该 Key 的模型候选全部失败才换下一个 Key，Key 全部失败才轮到下一个渠道；模型候选遍历不再跟随 Fuzzy 开关（换模型属于渠道内层级），BaseURL 仍作为最外层故障转移单位
- **失败率退出调度链路** — 移除渠道动态评分与失败率健康过滤（选渠、llmgate、图片理解渠道筛选三处），失败率/成功率只保留为前端展示指标
- **Fuzzy 模式语义明确** — 关闭时只尝试命中分组位次第一的渠道，渠道内候选（Key/模型/重试）耗尽直接报错阻断，不换渠道、不受抢优先级影响；打开时按配置位次顺序转移，命中分组渠道耗尽后进入兜底分组。分组兜底（规则 1）两种模式都保留：命中分组没有可尝试的渠道时进入兜底分组
- **抢优先级（促销）收敛** — 只在 Fuzzy 模式打开时生效，支持同时设置多个渠道，处于促销期的渠道按各自位次整体排到普通渠道之前
- **熔断口径调整** — 默认连续失败 5 次熔断、冷却 15 分钟；移除错误率触发路径及配套的 `CIRCUIT_ERROR_RATE` / `CIRCUIT_MIN_REQUESTS` 环境变量（错误率只用于展示，不再参与熔断判定）；冷却上限由 600 秒放宽到 3600 秒；Key 级熔断保留
- **图片理解层顺序补全** — 显式配置渠道 → 公用图片理解池 → 同分组图片理解渠道 → 兜底分组图片理解渠道；全部失败直接返回"模型不可用"阻断，不再切换文本渠道
- **前端展示优化** — 渠道编排成功率条补充失败占比与失败绝对量；全局统计新增"可用率"视图（成功率/失败率双线 + 90% 阈值线 + 异常时段计数）；Fuzzy 与抢优先级说明文案同步新语义

### 修复（渠道编排）

- **传输层失败不再烧满同候选重试预算** — 连接失败、响应头超时、响应体读不完整同属"没拿到上游响应"，与模型候选无关：现在标记该 Key 失败后立即换下一个 Key，不再在同一个 Key 的同一个模型上原地重试 5 次（沿用旧口径：这类失败换 Key，不是换模型）。此前单次请求会在一个坏渠道上空等 5 × 首字节超时（默认 120s，约 10 分钟），客户端往往等不到就取消，导致渠道级"连续失败 5 次熔断"始终记不上，坏渠道一直占着位次第一
- **2xx 信封检测读失败分支补上响应体关闭** — 该分支在读取响应体失败或超过 10MB 上限后直接换 Key，body 不再交给成功路径处理，遗漏 Close 会导致连接不归还连接池

---

## [v3.3.0] - 2026-09-14

### 新功能

- **危险命令 AI 闸门** — 命中检测规则 → 会话白/黑名单 → AI 二次判断 → 挂起等待放行（或放行 + 事后否决）；pending 通知组件；allowlist 字面量放行与自定义正则检测规则；危险命令检测总开关关闭时联动关闭 AI 闸门
- **AI 配置助手** — 浮窗会话 + 行动协议（create_channel / create_pool / set_model_mapping / update_channel）；模型路由按 `settings.assistant.model` 分组匹配词复用渠道编排，status 返回当前命中渠道；设置页新增 AI 助手分区（分组下拉 + 生效渠道展示）；助手支持显式 channelId（不回退不换渠道）
- **AI 闸门会话级审查去重** — 同对话内同一命令骨架（忽略参数/路径差异）只送审一次，结论进程内缓存，解除名单即失效；拦截记录带结构化 command 字段，拦截页支持移入/移出黑白名单与「仅活跃会话」「危险命令相关」筛选
- **llmgate 分组模型路由与自动模式全池扫描** — auto 选渠改走 scheduler.SelectChannel 主链路（分组匹配 → 优先级 → 熔断/健康 → 兜底分组），修复无 defaultModel 渠道"未能解析可用模型"导致内部调用必失败的问题；空模型自动模式按四协议 × 配置顺序全池扫描，渠道级失败带 failedChannels 换渠道重选（上限 4）；新增 PreviewChannel 探测接口；调用接入日志关联（CallMetadata：requestID/父请求/会话），限流响应体读取（2MB）
- **工具版本检测与提醒** — 新增 `/api/tools/:tool/version` 接口（本地 CLI 版本 + npm registry 最新版本，per-tool 缓存 1 小时，force 刷新，两端独立探测互不阻塞）；ToolInstallBanner 组件接入 GPT / Claude Code / OpenCode / PiAgent 四个配置页（安装命令按后端 OS 区分、版本状态徽章、一键复制、重新检测）；发现新版本时右上角徽章下弹提醒气泡（5 秒收起，未升级每小时提醒）
- **拦截记录展示真实命中片段** — 命中片段从纯元数据改为元数据 + 命中上下文窗口：敏感词/个人信息显示命中原文及前后 60 字符（PII 明文落库，管理员已确认），凭据只显示 [凭据已隐藏:规则] 绝不落明文，危险命令附命令原文，白名单放行附工具输出段首；表格只展示片段行，完整片段在详情弹窗查看
- **Codex 模型目录动态拉取** — 用 `codex debug models` 获取内置模型替代硬编码模板；自定义条目从内置清单复制 model_messages（满足 Codex 0.144+ 指令硬性校验）；model_catalog_json 按整体替换语义补齐内置条目，目录删空时回退配置键；内置模型标记不可删，新增表单改为对话框

### 修复

- **管理鉴权扩展名绕过（安全）** — WebAuthMiddleware 不再按扩展名放行：静态资源放行改为已注册资源路径（/assets/ 与固定入口文件），带 `.js` 等后缀的 provider ID 等动态路由参数不再被当作静态文件绕过管理鉴权；管理密钥校验收敛到 AdminAuthMiddleware，由各 setupAdmin* 路由组显式绑定
- **流式检测性能** — 工具参数检测按增量触发；凭据检测器与处置模式初始化时固化；pre_request 钩子按请求体指纹缓存（重试 / vision 前后只扫一次）
- **npm dist-tags scoped 包名 404** — 对 scoped 包名用 url.PathEscape 转义 `/` 为 %2F，修复 Claude Code / Codex / Pi 配置页"最新版本"查询 404

### 重构（架构优化 R1–R6 首轮，方案见 docs/architecture-optimization-plan.md）

- **网络发送唯一出口** — 新增 `internal/httpclient`（客户端选择 / identity 编码 / 流式空闲超时），eval 与 proxycore 均委托此处
- **无框架请求编排** — 新增 `internal/proxy`（候选 BaseURL×Key 重试状态与故障分类谓词），带"不导入 gin / handlers / SQLite 驱动"静态断言；providers 新增 RequestInput / BuildRequest / BuildUpstreamRequest 显式请求构造，llmgate / visionlayer 不再伪造 HTTP 框架上下文
- **渠道许可句柄收口** — scheduler.SelectionResult.Permit（ChannelPermit）统一选渠资源的取得 / 健康记账（恰好一次）/ 释放（幂等），单渠道、多渠道、compact、llmgate、vision 全部经此收尾；熔断半开探测名额在调用结束按结果记账，杜绝探测名额泄漏；新增 permit_test 与 channel_health_test 锁定契约
- **配置原子写** — 新增 `internal/atomicfile`（同目录临时文件 → 完整写入 → 同步 → 平台替换 → 目录同步，替换为唯一提交点，目录同步失败以 CommittedError 显式暴露）；config 写入统一接入，失败显式传播，杜绝半截配置文件；piagent fileio 平台差异收拢并删除 fileio_unix.go / fileio_windows.go
- **应用组装拆分** — main.go 拆出 app_lifecycle.go（初始化失败逆序清理已创建资源）与 app_routes.go（路由注册），main 只留入口
- **定价模块下线** — 整体移除 `internal/pricing`、metrics/cost 与 pricing_handler；全局统计去掉按模型花费端点与 CostUSD 字段（渠道自报价不可信，维护成本高于价值）；前端对应图表与 API 移除
- **前端状态收敛** — 新增 composables（useAuthFlow / useChannelActions / useChannelActivity / useChannelEditor / channelMetricsFormat），App.vue / AddChannelModal.vue / ChannelOrchestration.vue 的业务操作与界面状态拆分归属；RequestLogsView 重构，新增 PromptBodyDialog 查看完整请求体；助手浮窗重构为 v-overlay 浮层栈，渠道选择器可切换
- **项目更名** — claude-proxy 更名为 api-proxy（module 路径 `github.com/BenedictKing/api-proxy`）

### 其他

- 构建与门禁对齐：三个 release workflow 与两个 Dockerfile 的 Go 版本声明对齐 go.mod；scripts/check.mjs 接入 ESLint 校验；删除 diagrams/ 全部过时产物（约 7.6 万行），现行架构事实以 docs/ 为准
- 文档：新增 docs/architecture-optimization-plan.md（R1–R6 设计稿）与 docs/baseline-2026-09-14.md；capabilities / flows / glossary / invariants 与 backend-go/CLAUDE.md 同步本轮事实

---

## [v3.2.0] - 2026-09-11

### 新功能

- **Codex 模型目录管理** — GPT 配置页新增"模型目录"面板：内置 DeepSeek-Flash / DeepSeek-V4-Pro / 通用三套预设模板（元数据字段与 DeepSeek 官方 Codex 接入文档一致），按 slug 新增/编辑/删除 `~/.codex/models.json` 模型条目（更新仅覆盖可调字段，手工写入的未知字段与条目保留）；"默认模型"一键写入 config.toml 根级 `model` / `model_catalog_json` / `model_reasoning_effort`
- **客户端伪装版本号可配置** — 新增环境变量 `CLAUDE_CODE_DISGUISE_VERSION` / `CODEX_DISGUISE_VERSION`（.env 支持，重启生效），未配置时沿用内置默认值（2.1.238 / 0.150.1）；影响 User-Agent、version 头、请求体计费块 cc_version 与会话指纹
- **敏感数据可逆脱敏** — 新增 `sensitive/vault` 占位符存储与递归 JSON 还原；SSE 流式事件按 JSON 路径精确还原（`hooks/redaction_stream`）；`info_detector` 增加银行卡号检测；处置模式统一为"阻断 / 可逆掩码"，前端 Settings 敏感信息/凭据合并为统一开关；占位符语义化改进
- **拦截日志会话分组懒加载** — `ListGrouped` 不再全量返回组内明细（大会话数千条导致前端展开卡死），改为返回组摘要（count/类型集合/最新代表记录），新增 groupKey 按组分页查明细；前端展开懒加载每页 50 条 + 加载更多

### 修复

- **敏感数据误报降低** — `credential_detector` / `info_detector` 检测精度改进（版本号语境等放行），掩码还原链路修复
- **伪装 SDK 版本过旧** — Claude Code 伪装的 `X-Stainless-Package-Version` 从 0.94.0 更新至 0.124.0（npm 最新）

### 其他

- 文档：`ENVIRONMENT.md` 登记伪装版本环境变量，`.env.example` 新增客户端伪装配置段

---

## [v3.1.2] - 2026-08-27

### 修复

- **无 ID 客户端重试创建重复对话记录** — `resolveTranscriptLocked` 新增同深度重试匹配：当严格扩展未命中时，在 `frontierIndex` 中按同 depth + 同 frontierHash 查找失败记录并复用。筛选条件：上次失败（`LastError != ""`）、已结束（`ActiveRequests == 0`）、scope 一致、lane 一致、5 分钟窗口内；depth=1 要求 scopeHash 非空。带显式 ID 的客户端不受影响。

### 测试

- 新增 `retry_match_test.go`，覆盖 11 个场景：depth≥2 失败重试合并、depth=1 有/无 scope、成功后不合并、并发不合并、超时窗口不合并、不同 scope/lane 不合并、严格扩展优先、多次重试合并、空 transcript 不合并

---

## [v3.1.1] - 2026-08-27

### 修复

- **output_item.added 缺少 function name** — Chat→Responses 流式转换中，`response.output_item.added` 事件现在携带 `item.name`（从同 chunk 提取 `function.name`，或回退到先前 chunk 已存储的 name）
- **modalities 对象格式未归一化** — `sanitizeOpenAIChatModalities` 将客户端发送的 `{"text":true,"audio":true}` 对象格式归一化为 `["text","audio"]` 数组，避免上游 400；全 false 时删除字段
- **prompt_cache_key 未转发** — `OpenAIChatConverter.ToProviderRequest` 现在转发 `prompt_cache_key` 和 `prompt_cache_retention` 字段到上游请求

### 测试

- 新增 `TestConvertOpenAIChatToResponses_ParallelToolCalls`（并行 tool call name + output_index 验证）
- 新增 `TestConvertOpenAIChatToResponses_ReasoningTextToolCallMixedOutputIndex`（reasoning→text→tool_call 混合 output_index 连续性）
- 增强 `TestConvertOpenAIChatToResponses_ToolCall`（验证 output_item.added 携带 name）
- 新增 3 个 modalities 归一化测试（对象→数组、数组保持、空值删除）
- 新增 `TestOpenAIChatConverter_ForwardsPromptCacheKey`（prompt_cache_key 转发）

---

## [v3.1.0] - 2026-08-26

### 新功能

- **客户端配置页"连接本代理"** — Claude Code / OpenCode / pi / DSH 四页重构快速选择为"连接本代理"：选定协议后自动填充 Base URL（`http://localhost:{port}`，端口取自 `/health`）与本代理访问密钥
- **一键导入本代理模型** — 新增 `GET /api/proxy-models` 端点（走 Web 鉴权，内容等同 `/v1/models`），客户端配置页按 `owned_by` 的 `pool:协议` 分组过滤后导入模型，导入即替换
- **Claude Code 每模型族独立 1M 开关** — `modelDefaults` 新增 `supports1M` 字段，按 family 独立勾选，保存时在模型标识追加 `[1m]` 后缀，读取时反向还原

### 修复

- **保存按钮冻结** — 客户端配置页头部 sticky 吸顶（避开 app-bar 高度），滚动时保存按钮不再消失
- **pi 识图与输入能力冲突** — 合并"支持图片"开关与"输入能力"文本框为统一复选框组（文本/图片），单一来源不再冲突

### 重构

- **pi 配置简化** — 砍掉凭据/备份标签页与对应后端端点；移除"启用的模型模式"配置项；默认模型设置置顶
- **DSH 配置简化** — 默认模型卡片置顶，移除渠道选择中间层
- **Claude Code 移除会话模型覆盖** — 前后端删除 `ANTHROPIC_MODEL` / `ANTHROPIC_REASONING_MODEL` 读写
- **客户端菜单调整** — Claude Code 移至第一位，pi-agent 菜单更名"pi"
- **导入默认值统一** — 上下文窗口默认 220K、最大输出 32K、思考等级含 max，三客户端一致

---

## [v3.0.4] - 2026-08-21

### 修复

- **优雅关闭顺序与 Manager Stop 幂等性** — 修正服务关闭时各管理器的停止顺序，并保证重复调用 `Stop` 安全无副作用
- **跨池故障转移降级 bug** — 修复上游故障转移跨密钥池降级时的错误判定，兼容性重试结果处理收敛为单一实现
- **客户端断连检测 nil panic** — 修复 `IsClientDisconnectError` 在入参为 nil 时的空指针崩溃
- **上游响应体读取与配置回滚错误显式化** — 上游响应体读取失败、配置回滚失败不再被静默吞掉
- **token 用量口径收敛** — 统一 input/output 两侧 token 统计口径

### 重构

- **五协议统一入口骨架迁移** — `RunProxyRequest` 主骨架迁入 responses 链路，五协议入口进一步统一
- **流式脚手架收敛** — 提取 `streamPump` 收敛四协议流式处理脚手架；SSE/multipart 通用能力下沉共享
- **上游故障转移结构化** — 故障转移骨架收敛为 `UpstreamAttempt` 结构体，`SelectChannel` 拆分为职责单一的选路方法
- **handlers 包拆分** — 原 common 千行文件拆分为 proxycore / hooks / streams，装配与全局状态收敛
- **上游 URL 拼接收敛** — 统一为 `BuildUpstreamURL` 并下沉至 Session
- **移除模型名后缀剥离机制** — 配置层不再隐式剥离模型名后缀
- **images 接入内容安全、清理调度器与跨层死代码、消除导出类型命名 stutter**

### 构建与文档

- **全量门禁 npm 化** — 根目录 `npm run check` 一键跑完后端 gofmt/vet/test 与前端 type-check/图标扫描，摆脱 make/bun 依赖
- 精简文档体系，规范 AGENTS/CLAUDE 结构

---

## [v3.0.3] - 2026-08-19

### 修复

- **模型映射展示优化** — 渠道列表中的模型映射改为紧凑预览，并在悬浮提示中分行展示完整映射与兜底模型，提升多条映射和长模型名称的可读性。

---

## [v3.0.2] - 2026-08-19

### 修复

- **安装 Skill 默认目标限制** — 将前端 UI 及后端列表中安装 Skill 的默认范围收敛为项目(project)与通用目录(agents),不再无脑按存在性(exists)勾选,避免用户不小心将特定项目的 Skill 安装到所有运行目录引发混乱。
- **UI 细节优化** — 修正前端 `testChannelWithModel` 的误导提示词、消除多余代码注释、合并更新导航状态。
- **模型推理级别(reasoning_effort)适配** — 修复 Gemini 和 Chat 接口转发时的 effort 档位丢失,适配高阶模型(`max`/`ultra`)至系统网关(`xhigh`),并将标准化逻辑作为共享方法输出。

### 重构

- **移除旧模型审计(Model Audit)模块** — 大幅缩减代码库,删除未再维护的审计代码及对应的图表前端页面。
- **顶栏导航与渠道列表视觉更新** — 导航重构为分组结构、消除手写链接;渠道列表收敛活跃指示器动画、标签样式变为行内标签(section-inline-label)形态,精简视觉噪声。

---

## [v3.0.1] - 2026-08-16

### 修复

- **图片选渠死参数清理** — `SelectChannel` 的 `hasImage bool` 形参被函数体 `_ = hasImage` 直接丢弃,属无副作用死参数。移除该形参及其在 `HandleMultiChannelFailover` / `handleMultiChannelProxy` / `RunProxyRequest` 入口的整条传递链(含 `utils.DetectImageContent` 在 messages/chat/images/gemini 四协议入口处的冗余计算)。图片是否被处理仍由被选中渠道的 `visionCapable` / `visionLayerEnabled` 配置在 `prepareRequestForUpstream → visionlayer.PrepareRequest` 决定,行为不变
- **Responses previous_response_id 自指透传移除** — Responses handler 入口处"当前请求无图但 `previous_response_id` 会话历史含图 → `hasImage=true`"的透传块被移除。该 `hasImage` 唯一作用是将 `session.HasVisionContent` 再写一次 true(本已为 true),属自指循环,无外部可观察行为变化
- **`handleSuccess` / `handleStreamSuccess` 的 `CommitTurn` 图片检测修正** — 两处原先误用响应体变量 `bodyBytes` 调用 `utils.DetectImageContent`,改用请求体 `originalRequestJSON`,正确检测当前轮输入是否含图

### 重构

- `SelectChannel` 签名收敛为 `(ctx, userID, failedChannels, kind, requestedModel)`,减少一处无效耦合
- `HasVisionContent` 字段 / `CommitTurn` 的 `hasVision` 形参 / SQLite `has_vision_content` 列保留(`CommitTurn` 仍通过 `utils.ResponsesItemHasVisionContent` 遍历 items 自动维护该标记),为未来"历史图描述重放"等场景留钩子

---

## [v3.0.0] - 2026-08-15

### 重构

- **canonical JSON 收敛** — `providers/responses_messages.go` 与 `converters/responses_protocol.go` 各一份的缓存键规范化实现合并为统一入口 `utils.CanonicalJSON`，三处调用（Messages→Responses 缓存键、Responses→Chat 缓存键、OpenAI 缓存键）改走共享实现

### 文档

- **文档体系重建** — 重写 `README.md`（移除"已归档/CCX"过时声明，按五协议 + 各子系统现状全文重写）、`docs/ENVIRONMENT.md`（修正 `RESPONSE_HEADER_TIMEOUT=120`、补 `STREAM_IDLE_TIMEOUT`/`FORCE_HTTP1`）、`docs/RELEASE.md`（改为根 `VERSION` 单一版本真相源 + tag 触发 workflow）、`docs/glossary.md`、`docs/capabilities.md`、`docs/flows.md`
- **版本号统一** — 版本单一事实源 = 根 `VERSION` = `v3.0.0`；`frontend/package.json` 对齐为 `3.0.0`；`CHANGELOG.md` 归档 v2.19.2
- **上下架清理** — 移除五份过时/违规文档（`backend-go/DEV_GUIDE.md`、`backend-go/docs/MALFORMED_TOOLCALL_MEMO.md`、`frontend/ESLINT.md`、`docs/MODEL_AUDIT_UI_OPTIMIZATION.md`、`docs/123/`）；`docs/glossary.md` 登记 Pi Agent / 模型目录语义，`docs/capabilities.md` 新增「已知重复与待治理」附注与治理记录

---

## [v2.19.2] - 2026-08-07

### 新增与优化

- **渠道分组与兜底路由** — 前端统一使用“分组/兜底分组”术语；模型未匹配任何分组，或命中分组内渠道全部不可用时，自动转入兜底分组
- **历史配置兼容迁移** — 自动迁移旧版默认子池及自定义 `*` 规则，保留原有渠道路由关系
- **图片理解渠道快捷菜单** — 公用纯图片理解池补齐编辑、复制、测试、日志、状态管理等快捷操作
- **选渠性能优化** — 命中分组和兜底分组从同一配置快照收集渠道，避免高并发选渠重复复制配置

### 修复

- **Responses 容量错误重试** — 上游返回空响应或 `Selected model is at capacity` 时，对同一候选自动额外重试一次，且不污染 Key 熔断状态
- **启动性能与内存占用** — Responses 历史会话改为按需加载，并限制热会话缓存，避免大型历史数据库拖慢启动
- **会话写入优化** — 一轮 Responses 会话的多次完整 JSON 写入合并为单事务提交
- **Messages 思考内容隔离** — Messages 转 Chat 时不再把 `reasoning_content` 输出到正文，同时保留内部回填能力，避免 Cursor 上下文快速膨胀
- **流式响应健壮性** — Responses 延迟写入 SSE 响应头，并限制流式元数据缓冲区大小

### 文档

- **社区链接** — README 新增 [LINUX DO](https://linux.do/) 友情链接

---

## [v2.18.0] - 2026-08-01

### 重构：架构收敛 + 流式健壮性修复

- **渠道管理五平行世界收敛** — 新增 `internal/core/channelcrud`，将 messages/responses/gemini/chat/images 五个协议包各自复制约 2900 行的渠道 CRUD/key 管理/ping 收敛为一份实现 + 各协议包薄封装工厂 `Crud()`，净删约 2700 行
- **路由注册收敛** — 新增 `handlers.RegisterChannelRoutes`，main.go 中五段几乎逐字复制的路由注册（约 250 行）收敛为 5 次声明式调用
- **scheduler 指标收敛** — `ChannelScheduler` 的 5 个独立 `MetricsManager`/`ChannelLogStore` 字段收敛为 `map[ChannelKind]` 查表
- **指标 handler 收敛** — Gemini 3 个重复的 metrics handler 改为委托通用 `ByKind` 版本
- **前端 api.ts 收敛** — 新增 `channelApiByType(type)` 工厂，89 个旧命名方法（`getResponsesChannels`/`getGeminiChannels` 等 5 组重复）清理为统一接口，api.ts 从 2028 行降至 1663 行；`stores/channel.ts` 的 5 路 if/else 瀑布收敛为 `channelsDataMap` + `tabApi()` 查表
- **图表定时器收敛** — 新增 `useAutoRefresh` composable，GlobalStatsChart/KeyTrendChart 的重复定时器逻辑统一
- **流式泄漏修复** — `Provider` 接口新增 `HandleStreamResponseCtx(ctx, body)`，5 个 provider 的生产 goroutine 在向 eventChan 发送时 select ctx.Done()，客户端断连立即中止，杜绝"缓冲写满后永久阻塞"的 goroutine/上游连接泄漏
- **流式超时增强** — 流式 client 增加首字节超时（`RESPONSE_HEADER_TIMEOUT`，默认 120s）；新增 `STREAM_IDLE_TIMEOUT`（默认 300s）空闲超时，通过 `IdleTimeoutReader` 检测"TCP 通但流中挂起"的上游
- **Bug 修复** — Messages/Responses 负载均衡下拉调用不存在的 `/loadbalance` 路由（404）已修复

### 文档

- **新增 ADR** — `docs/adr/ADR-0001~0004`：核心层收敛、协议注册机制、流式框架、渐进重构节奏（已移除）
- **新增术语表** — `docs/glossary.md`

---

## [v2.12.2] - 2026-07-10

---

## [v2.9.0] - 2026-06-11

### 新增

- **渠道调度：失败率动态打分系统** — 实现基于失败率的渠道动态打分机制，分数范围 0-100，分数越高优先级越高。失败率高的渠道（如 5 次请求失败 4-5 次）自动降低优先级，避免不断重试导致响应缓慢
- **模型支持：新增 Fable 模型** — 静态模型列表添加 `fable`，支持 Claude Fable 5
- **模型支持：上下文窗口后缀处理** — 支持 Claude Code 的 `[1m]` 后缀（如 `opus[1m]`、`claude-opus-4-8[1m]`），自动剥离后发送到上游，确保与 Claude Code 和 Cursor 客户端兼容

### 优化

- **调度器：按动态分数排序** — `getActiveChannels` 方法为每个渠道计算动态分数并按分数排序（分数相同时按配置优先级排序），确保高失败率渠道自动排在队尾
- **指标：新增请求计数方法** — 添加 `GetChannelRequestCount` 方法，用于计算渠道的总请求数（所有 Key 聚合），支持动态打分系统的样本数判断
- **调度器：新渠道保护** — 请求数少于 5 次的新渠道给予满分（100 分），避免因样本不足被误判为低优先级

### 文档

- **新增文档** — `docs/MODEL_SUFFIX_HANDLING.md` 详细说明后缀处理机制和使用场景（已移除）
- **更新文档** — `CLAUDE.md` 添加模型支持章节，说明静态模型列表和上下文窗口后缀

### 说明

动态打分系统对以下场景**不生效**（保持原有优先级逻辑）：
- 促销期渠道（最高优先级，绕过健康检查和动态打分）
- Trace 亲和性绑定（会话固定渠道）
- 对话路由覆盖（手动固定渠道）

动态打分系统对以下场景**生效**：
- 常规渠道自动切换（按失败率动态调整顺序）
- 图片理解模型列表（Vision 渠道也按失败率排序）

---

## [v2.8.2] - 2026-06-11

### 优化

- **性能优化：默认关闭控制台日志输出** — `LOG_TO_CONSOLE` 默认值改为 `false`，生产环境仅写文件不写 stdout，消除 Windows 下同步控制台写入的性能瓶颈
- **内存保护：MetricsManager 单 Key 记录上限** — `requestHistory` 增加每 Key 10 万条截断上限，防止高频调用导致内存无限增长
- **内存保护：SessionManager 会话数上限** — 新增 `maxSessions` 全局会话数限制（默认 5000），达到上限时按 LRU 淘汰最久未访问的会话
- **内存保护：流处理日志缓冲区上限** — StreamContext LogBuffer 增加 2MB 上限，防止超长流式响应导致单请求内存暴涨
- **测试修复：流 token 修补逻辑测试** — 修复 `TestPatchMessageStartInputTokensIfNeeded` 的 3 个测试用例，确保虚假 input_tokens 修补逻辑的正确性

---

## [v2.8.1] - 2026-06-10

### 优化

- **调度器：多促销渠道并发支持** — `findPromotedChannel` 改为 `findPromotedChannels`，返回所有促销渠道并按优先级循环尝试，首个促销渠道在本次请求失败后会自动尝试下一个
- **调度器：亲和性不健康时主动清除绑定** — 当 Trace 亲和绑定的渠道不健康或状态异常时，立即清除亲和记录，避免 30 分钟 TTL 内每次请求空转健康检查
- **调度器：单渠道模式下消费 PromotionCount** — 4 种 API handler（Messages/Responses/Gemini/Chat）的单渠道成功路径均补上 `ConsumePromotionCount` 调用，与多渠道路径保持一致
- **指标：渠道健康判断改为逐 Key 独立计算** — `IsChannelHealthyWithKeys` 不再聚合所有 Key 样本算总失败率，改为逐 Key 独立判断，避免坏 Key 被健康 Key 的大量样本"稀释"
- **调度器：锁粒度缩小** — `SelectChannel` 仅在读取 `conversationRegistry` 时短暂持 `RLock`，提取引用后立即释放，减少高并发下的锁阻塞

---

## [v2.8.0] - 2026-06-07

### 新增

- **请求日志归档与可视化可观测性**
  - 新增 `RequestLogStore` 组件，用于记录每次请求尝试（Attempt）和会话明细，支持本地 JSONL 文件自动归档与过期清理（默认保留 7 天）
  - 后端提供 `/api/request-logs` 查询接口，支持按 API 类型进行过滤与分页限制（最大 50 条）
  - 前端新增 `RequestLogsView` 请求日志管理页面，以表格形式直观展示请求时间、类型、渠道、状态、模型转换、首 Token 耗时、Token 吞吐（I/O）、缓存读写（C/R）、吞吐量比（TPM）以及错误排查日志
  - 侧边栏及导航栏新增请求日志入口，图标采用 `mdi-text-box-search-outline`（已导入 `@mdi/js`）
  - 支持会话上下文级别联动：可通过点击日志或对话列表中的 UUID 快速打开该会话/请求的“可观测性详情 Dialog”

- **会话可观测性深度解析**
  - 扩展 `Observation` 与 `Record` 数据结构，新增 `Prompts` 数组字段，支持追踪和存储对话中最近 3 条请求 Prompt 的摘要信息
  - 支持无 `conversationID` 时的 Fallback 路由机制，以 `model+prompt` 的 SHA1 摘要匹配历史会话亲和
  - 前端 `ConversationsView` 重构升级：
    - 支持点击 ID 弹窗展示“对话可观测详情”，包含：会话完整拓扑元数据、请求/路由模型、生成时间与最新活跃时间、完整 Prompt 历史轨迹追踪、重试次数、API 密钥掩码及失败日志
    - 对话列表中支持折叠展示最近 3 次 Prompt 历程，提供一键复制 ID 及直接跳转检索等便捷功能

- **渠道编排可观测性仪表盘升级**
  - 前端渠道编排 `ChannelOrchestration` 卡片集成各渠道实时状态监控与指标简图
  - 支持卡片内展示各 Key 的活跃度、失败统计、以及可观测性日志面板

- **多接口适配层与 Failover 机制补全**
  - 在 `upstream_failover` 和 `request` 中集成 `RequestLogStore` 的自动打点打标签，对每次 retry / failover / error 自动分级归档并提供脱敏记录
  - 完善 OpenAI, Gemini, Responses, Messages 等底层提供商对 logs 的统一打点转换逻辑

---

## [v2.7.0] - 2026-06-07

### 新增

- **OpenAI 协议转换增强**
  - 流式请求自动注入 `stream_options.include_usage: true`，确保上游返回 usage 数据
  - 统一 usage 解析：提取 `normalizeOpenAIUsage()` 处理多层嵌套缓存 token（支持 `input_tokens_details` 和 `prompt_tokens_details`）
  - 流式响应：累积 text delta 后合并发送，避免碎片化 `content_block_delta` 事件
  - 正确发送 `message_delta` 事件携带 `stop_reason` 和 `usage` 信息
  - 非流式响应补全 `cache_creation`/`cache_ttl` 等字段映射
  - Chat 转换器同步支持 `openAIChatStreamOptions()` 逻辑

- **指标系统增强**
  - `RequestRecord`/`PersistentRecord` 增加 `Model` 字段，指标追踪按模型记录
  - SQLite 存储 schema 新增 `model` 列，含平滑迁移逻辑（`ALTER TABLE` 兼容旧数据库）
  - `KeyHistoryDataPoint` API 返回 `model`（去重逗号连接）和 `cacheHitRate`（缓存命中率百分比）字段
  - `RecordRequestConnected` 签名扩展接收 `model` 参数

- **渠道统计图表重构 (ChannelMetricsChart)**
  - 新增 Token 使用量和缓存统计图表行
  - Tooltip 显示模型名称和缓存命中率
  - 多 key 指标历史数据切换展示
  - KeyTrendChart tooltip 优化：流量视图直接显示模型名称

- **对话路由改进**
  - `FallbackKey` 从 `conversationID` 改为 `model+prompt` 的 SHA1 摘要
  - 允许无 `conversationID` 时仍基于模型和首条 prompt 路由亲和
  - `ExtractUserID` 支持 `prompt_cache_key` 及多种 metadata 字段（`user_id`/`conversation_id`/`session_id`/`thread_id`）

- **日志管理增强**
  - 日志目录路径解析：支持相对路径自动转换为二进制文件同级目录
  - 自动清理过期日志：启动时和每 24 小时定时清理超过 `MaxAge` 天数的日志文件
  - 日志保留天数优化：默认从 30 天调整为 7 天，减少磁盘占用
  - Gin 日志集成：将 Gin 的日志输出重定向到统一的日志系统
  - 智能文件识别：根据 `.log`/`.log.gz` 后缀识别日志文件
  - 保护活跃日志：清理时跳过当前正在使用的日志文件

- **Usage 字段提取增强**
  - 新增 `nestedCachedTokens()` 统一提取 `cached_tokens`
  - `checkUsageFieldsWithPatch` 和 `extractUsageFromMap` 补全 `input_tokens_details`/`prompt_tokens_details` 的缓存 token 回退

- **模型目录管理**
  - 新增 `modelcatalog` 模块用于模型目录管理

### 修复

- **Failover 判定逻辑修复**
  - `shouldRetryWithNextKeyFuzzy` 重排检查优先级：不可重试错误 → 配额状态码 → 配额关键词 → 500+ → 默认 failover
  - 修复 500 + `sensitive_words_detected` 误判为可 failover 的问题
  - 修复 500 + 配额关键词未标记 `isQuotaRelated=true` 的问题

### 重构

- **文档结构重组**
  - 创建 `docs/` 统一存放技术文档
  - 移动 `ENVIRONMENT.md` → `docs/ENVIRONMENT.md`
  - 移动 `PERFORMANCE_ANALYSIS.md` → `docs/PERFORMANCE_ANALYSIS.md`（已移除）
  - 根目录只保留标准文件（`README.md`/`CHANGELOG.md`/`CLAUDE.md`/`AGENTS.md`/`LICENSE`）
  - 更新 `CLAUDE.md` 和 `AGENTS.md` 新增文档编写规范约束

### 测试

- `openai_request_test`: 验证 `stream_options.include_usage` 注入
- `openai_stream_test`: 验证缓存 usage 映射和 text delta 合并

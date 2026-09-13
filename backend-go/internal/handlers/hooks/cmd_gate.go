package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/llmgate"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
)

// CmdGate 危险命令 AI 闸门：命中危险命令后不直接拦截，而是
//  1. 查会话级白/黑名单（用户此前对同一对话的决定），命中即按名单处理；
//  2. 由代理调用一个大模型渠道二次判断（llmgate）；
//  3. AI 判危险 → 挂起请求等待用户放行（窗口超时即拦截）；
//     AI 判安全 → 立即放行并保留同长度的事后否决窗口；
//     AI 判断失败（无渠道/超时/无法解析）→ 一律拦截（保守默认）。
//
// 名单作用域是 conversationID：用户加白/黑名单后，同一对话的后续危险命令
// 直接按最新决定处理（后决定覆盖先决定）。
type CmdGate struct {
	cfgManager *config.ConfigManager
	caller     *llmgate.Caller
	store      *sensitive.GateStore
	recorder   BlockedLogRecorder

	mu      sync.Mutex
	waiters map[int64]chan string
	vetoes  map[int64]*gateVeto
}

// gateVeto 是放行后的事后否决监听。
type gateVeto struct {
	doneCh chan struct{}
	cancel context.CancelFunc
	timer  *time.Timer
}

// GateVerdict 是闸门对一次命中的最终裁决。
type GateVerdict struct {
	Allow     bool
	BlockType string // 审计类型（dangerous_cmd_ai / dangerous_cmd_allow）
	Reason    string
}

// NewCmdGate 创建闸门。caller / store 为 nil 时闸门不可用（Review 一律拦截）。
func NewCmdGate(cfgManager *config.ConfigManager, caller *llmgate.Caller, store *sensitive.GateStore, recorder BlockedLogRecorder) *CmdGate {
	return &CmdGate{
		cfgManager: cfgManager,
		caller:     caller,
		store:      store,
		recorder:   recorder,
		waiters:    make(map[int64]chan string),
		vetoes:     make(map[int64]*gateVeto),
	}
}

// gateSettings 返回当前生效的闸门配置（每次实时读取，热重载友好）。
func (g *CmdGate) gateSettings() config.DangerousCmdAIGateConfig {
	if g == nil || g.cfgManager == nil {
		return config.DangerousCmdAIGateConfig{}
	}
	return g.cfgManager.GetSettings().ContentSafety.DangerousCmd.AIGate
}

// Enabled 报告闸门是否启用（危险命令检测本体 + AI 闸门都开着）。
func (g *CmdGate) Enabled() bool {
	if g == nil || g.cfgManager == nil {
		return false
	}
	settings := g.cfgManager.GetSettings().ContentSafety.DangerousCmd
	return settings.Enabled && settings.AIGate.Enabled
}

// waitSeconds 返回用户决定窗口时长。
func (g *CmdGate) waitSeconds() int {
	seconds := g.gateSettings().WaitSeconds
	if seconds <= 0 {
		return config.DefaultAIGateWaitSeconds
	}
	return seconds
}

// Review 对一次危险命令命中做完整裁决。返回的 error 恒为 nil：
// 闸门自身的全部失败路径（无渠道、超时、解析失败）都归入"拦截"裁决，
// 调用方不需要也不应该再为闸门做兜底分支。
// onVeto 在 AI 判安全放行后传入：若用户在否决窗口内加黑名单，onVeto 被
// 调用一次（流式链路用它终止当前 SSE 流；非流式传 nil——响应已不可收回）。
func (g *CmdGate) Review(ctx context.Context, metadata HookContext, match sensitive.CommandMatch, onVeto func()) GateVerdict {
	verdict := g.review(ctx, metadata, match, onVeto)
	g.audit(metadata, match, verdict)
	return verdict
}

func (g *CmdGate) review(ctx context.Context, metadata HookContext, match sensitive.CommandMatch, onVeto func()) GateVerdict {
	if g == nil || !g.Enabled() || g.caller == nil || g.store == nil {
		// 闸门未启用或基础设施缺失：调用方不会走到这里（接线处先判 Enabled），
		// 兜底返回拦截，绝不静默放行。
		return GateVerdict{Allow: false, BlockType: sensitive.BlockTypeDangerousCmd, Reason: "闸门不可用"}
	}
	// 1. 会话级名单。
	if decision, ok := g.latestDecision(ctx, metadata.ConversationID); ok {
		if decision.Decision == sensitive.GateDecisionAllow {
			return GateVerdict{Allow: true, BlockType: sensitive.BlockTypeDangerousCmdAllow, Reason: "会话白名单放行"}
		}
		return GateVerdict{Allow: false, BlockType: sensitive.BlockTypeDangerousCmdAI, Reason: "会话黑名单拦截"}
	}
	// 2. AI 二次判断。
	aiVerdict, aiReason, aiErr := g.askModel(ctx, metadata, match)
	if aiErr != nil {
		log.Printf("[Gate-Review] AI 判断失败，按拦截处理: %v", aiErr)
		return GateVerdict{Allow: false, BlockType: sensitive.BlockTypeDangerousCmdAI, Reason: "AI 判断不可用，已默认拦截: " + aiErr.Error()}
	}
	if aiVerdict == sensitive.GateDecisionAllow {
		// 3a. AI 判安全：立即放行 + 事后否决窗口。
		g.armVetoWindow(metadata, match, aiReason, onVeto)
		return GateVerdict{Allow: true, BlockType: sensitive.BlockTypeDangerousCmdAllow, Reason: "AI 判定安全: " + aiReason}
	}
	// 3b. AI 判危险：挂起等待用户决定，超时拦截。
	return g.awaitUserDecision(ctx, metadata, match, aiReason)
}

// latestDecision 读取会话最新名单决定。
func (g *CmdGate) latestDecision(ctx context.Context, conversationID string) (sensitive.GateDecision, bool) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return sensitive.GateDecision{}, false
	}
	decision, ok, err := g.store.LatestDecisionForConversation(ctx, conversationID)
	if err != nil {
		// 名单读失败按"无名单"继续走 AI 判断，不因审计存储故障拖垮主链路。
		log.Printf("[Gate-Decision] 读取会话名单失败: %v", err)
		return sensitive.GateDecision{}, false
	}
	return decision, ok
}

// askModel 调用大模型渠道做二次判断，返回 (verdict, reason, error)。
func (g *CmdGate) askModel(ctx context.Context, metadata HookContext, match sensitive.CommandMatch) (string, string, error) {
	prompt := buildGatePrompt(metadata, match)
	result, err := g.caller.Call(ctx, g.gateSettings().ChannelID, prompt)
	if err != nil {
		return "", "", err
	}
	verdict, reason, parseErr := parseGateVerdict(result.Text)
	if parseErr != nil {
		return "", "", parseErr
	}
	return verdict, reason, nil
}

// awaitUserDecision 注册 pending 并阻塞等待用户放行 / 窗口超时 / 客户端断连。
func (g *CmdGate) awaitUserDecision(ctx context.Context, metadata HookContext, match sensitive.CommandMatch, aiReason string) GateVerdict {
	wait := time.Duration(g.waitSeconds()) * time.Second
	pending, err := g.store.RecordPending(ctx, sensitive.GatePending{
		ConversationID: metadata.ConversationID,
		RequestID:      metadata.RequestID,
		APIType:        metadata.APIType,
		Model:          metadata.Model,
		Rule:           match.Rule,
		Command:        match.Command,
		Source:         match.Context,
		AIVerdict:      sensitive.GateDecisionBlock,
		AIReason:       aiReason,
		ExpiresAt:      time.Now().UTC().Add(wait),
	})
	if err != nil {
		log.Printf("[Gate-Review] 写入待确认记录失败，按拦截处理: %v", err)
		return GateVerdict{Allow: false, BlockType: sensitive.BlockTypeDangerousCmdAI, Reason: "AI 判定危险（待确认记录写入失败，已默认拦截）"}
	}
	decisionCh := g.registerWaiter(pending.ID)
	defer g.unregisterWaiter(pending.ID)

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case decision := <-decisionCh:
		if decision == sensitive.GateDecisionAllow {
			return GateVerdict{Allow: true, BlockType: sensitive.BlockTypeDangerousCmdAllow, Reason: "AI 判定危险，用户已放行: " + aiReason}
		}
		return GateVerdict{Allow: false, BlockType: sensitive.BlockTypeDangerousCmdAI, Reason: "AI 判定危险，用户已拦截: " + aiReason}
	case <-timer.C:
		g.resolvePending(pending.ID, "expired")
		return GateVerdict{Allow: false, BlockType: sensitive.BlockTypeDangerousCmdAI, Reason: "AI 判定危险，等待超时已拦截: " + aiReason}
	case <-ctx.Done():
		g.resolvePending(pending.ID, "cancelled")
		return GateVerdict{Allow: false, BlockType: sensitive.BlockTypeDangerousCmdAI, Reason: "客户端断开，已拦截: " + aiReason}
	}
}

// armVetoWindow 放行后注册否决窗口：写一条 waiting 记录（前端展示"已放行可否决"），
// 窗口内用户加黑名单则写名单、结束记录并触发 onVeto；窗口自然超时则记录过期。
func (g *CmdGate) armVetoWindow(metadata HookContext, match sensitive.CommandMatch, aiReason string, onVeto func()) {
	wait := time.Duration(g.waitSeconds()) * time.Second
	pending, err := g.store.RecordPending(context.Background(), sensitive.GatePending{
		ConversationID: metadata.ConversationID,
		RequestID:      metadata.RequestID,
		APIType:        metadata.APIType,
		Model:          metadata.Model,
		Rule:           match.Rule,
		Command:        match.Command,
		Source:         match.Context,
		AIVerdict:      sensitive.GateDecisionAllow,
		AIReason:       aiReason,
		ExpiresAt:      time.Now().UTC().Add(wait),
	})
	if err != nil {
		// 记录失败不影响放行决定（主请求继续），只是这次没有否决入口。
		log.Printf("[Gate-Veto] 写入否决窗口记录失败（放行不受影响）: %v", err)
		return
	}
	vetoCtx, cancel := context.WithCancel(context.Background())
	veto := &gateVeto{
		doneCh: make(chan struct{}),
		cancel: cancel,
		timer: time.AfterFunc(wait, func() {
			g.resolvePending(pending.ID, "expired")
			g.closeVeto(pending.ID)
		}),
	}
	g.mu.Lock()
	g.vetoes[pending.ID] = veto
	g.mu.Unlock()
	decisionCh := g.registerWaiter(pending.ID)
	go func() {
		select {
		case <-decisionCh:
			// 唯一可能的用户决定是 block（Decide 已限定 AI-allow 窗口只接受拦截）；
			// 写黑名单并终止当前流。
			g.mu.Lock()
			stillArmed := g.vetoes[pending.ID] != nil
			g.mu.Unlock()
			if stillArmed {
				g.recordDecision(metadata, match, sensitive.GateDecisionBlock)
				g.closeVeto(pending.ID)
				if onVeto != nil {
					onVeto()
				}
			}
		case <-vetoCtx.Done():
			// 窗口已结束（超时或流程清理），goroutine 退出。
		}
	}()
}

func (g *CmdGate) closeVeto(pendingID int64) {
	g.mu.Lock()
	veto := g.vetoes[pendingID]
	delete(g.vetoes, pendingID)
	g.mu.Unlock()
	if veto != nil {
		veto.timer.Stop()
		veto.cancel()
		select {
		case <-veto.doneCh:
		default:
			close(veto.doneCh)
		}
	}
}

// Decide 处理用户对一条 pending 的决定（管理 API 入口）。
// 返回 false 表示记录不存在或已结束（幂等拒绝）。
func (g *CmdGate) Decide(pendingID int64, decision string) (sensitive.GatePending, bool, error) {
	if g == nil || g.store == nil {
		return sensitive.GatePending{}, false, fmt.Errorf("闸门存储不可用")
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != sensitive.GateDecisionAllow && decision != sensitive.GateDecisionBlock {
		return sensitive.GatePending{}, false, fmt.Errorf("无效的闸门决定 %q", decision)
	}
	// 先读当前记录做语义校验，再原子迁移状态（waiting → allowed/blocked）。
	// 竞争由 ResolvePending 的 WHERE status='waiting' 条件裁决。
	pending, err := g.store.GetPending(context.Background(), pendingID)
	if err != nil {
		if err == sensitive.ErrGatePendingNotFound {
			return sensitive.GatePending{}, false, nil
		}
		return sensitive.GatePending{}, false, err
	}
	if pending.Status != "waiting" {
		return sensitive.GatePending{}, false, nil
	}
	// AI 判安全的否决窗口只接受拦截；AI 判危险的等待窗口两者皆可。
	if pending.AIVerdict == sensitive.GateDecisionAllow && decision != sensitive.GateDecisionBlock {
		return sensitive.GatePending{}, false, fmt.Errorf("该命令已按 AI 判定放行，只能加入黑名单")
	}
	status := "allowed"
	if decision == sensitive.GateDecisionBlock {
		status = "blocked"
	}
	pending, err = g.store.ResolvePending(context.Background(), pendingID, status)
	if err != nil {
		if err == sensitive.ErrGatePendingNotFound {
			return sensitive.GatePending{}, false, nil
		}
		return sensitive.GatePending{}, false, err
	}
	g.recordDecisionFromPending(pending, decision)
	g.notifyWaiter(pendingID, decision)
	return pending, true, nil
}

// ListPending 返回等待中的待确认记录（管理 API 入口）。
func (g *CmdGate) ListPending() ([]sensitive.GatePending, error) {
	if g == nil || g.store == nil {
		return nil, fmt.Errorf("闸门存储不可用")
	}
	return g.store.ListWaiting(context.Background())
}

// ListDecisions 返回全部会话级名单决定（管理 API 入口）。
func (g *CmdGate) ListDecisions() ([]sensitive.GateDecision, error) {
	if g == nil || g.store == nil {
		return nil, fmt.Errorf("闸门存储不可用")
	}
	return g.store.ListDecisions(context.Background())
}

// RemoveDecisions 解除会话名单（管理 API 入口）。
func (g *CmdGate) RemoveDecisions(conversationID string) (int64, error) {
	if g == nil || g.store == nil {
		return 0, fmt.Errorf("闸门存储不可用")
	}
	return g.store.RemoveDecisionsForConversation(context.Background(), conversationID)
}

func (g *CmdGate) recordDecision(metadata HookContext, match sensitive.CommandMatch, decision string) {
	entry := sensitive.GateDecision{
		ConversationID: strings.TrimSpace(metadata.ConversationID),
		Decision:       decision,
		Rule:           match.Rule,
		CommandSnippet: truncateGateSnippet(match.Command),
	}
	if entry.ConversationID == "" {
		// 无会话标识时无法建立会话级名单；记录决定没有意义，跳过。
		return
	}
	if _, err := g.store.AddDecision(context.Background(), entry); err != nil {
		log.Printf("[Gate-Decision] 写入会话名单失败: %v", err)
	}
}

func (g *CmdGate) recordDecisionFromPending(pending sensitive.GatePending, decision string) {
	entry := sensitive.GateDecision{
		ConversationID: strings.TrimSpace(pending.ConversationID),
		Decision:       decision,
		Rule:           pending.Rule,
		CommandSnippet: truncateGateSnippet(pending.Command),
	}
	if entry.ConversationID == "" {
		return
	}
	if _, err := g.store.AddDecision(context.Background(), entry); err != nil {
		log.Printf("[Gate-Decision] 写入会话名单失败: %v", err)
	}
}

func (g *CmdGate) registerWaiter(pendingID int64) <-chan string {
	ch := make(chan string, 1)
	g.mu.Lock()
	g.waiters[pendingID] = ch
	g.mu.Unlock()
	return ch
}

func (g *CmdGate) unregisterWaiter(pendingID int64) {
	g.mu.Lock()
	delete(g.waiters, pendingID)
	g.mu.Unlock()
}

func (g *CmdGate) notifyWaiter(pendingID int64, decision string) {
	g.mu.Lock()
	ch := g.waiters[pendingID]
	g.mu.Unlock()
	if ch != nil {
		select {
		case ch <- decision:
		default:
		}
	}
}

// resolvePending 尽力迁移 pending 状态；竞争失败（用户先一步决定）是正常路径。
func (g *CmdGate) resolvePending(pendingID int64, status string) {
	if _, err := g.store.ResolvePending(context.Background(), pendingID, status); err != nil && err != sensitive.ErrGatePendingNotFound {
		log.Printf("[Gate-Review] 更新待确认状态失败: %v", err)
	}
}

// audit 写闸门审计事件。失败只记日志——审计写入失败不应改变已做出的裁决。
func (g *CmdGate) audit(metadata HookContext, match sensitive.CommandMatch, verdict GateVerdict) {
	if g == nil || g.recorder == nil {
		return
	}
	blockType := verdict.BlockType
	if blockType == "" {
		if verdict.Allow {
			blockType = sensitive.BlockTypeDangerousCmdAllow
		} else {
			blockType = sensitive.BlockTypeDangerousCmdAI
		}
	}
	snippet := fmt.Sprintf("mode=ai_gate source=%s context=%s\n%s\n裁决: %s",
		"assistant", match.Context, match.Command, verdict.Reason)
	_, err := g.recorder.Record(context.Background(), sensitive.BlockedLog{
		APIType:        metadata.APIType,
		BlockType:      blockType,
		RuleName:       match.Rule,
		PromptSnippet:  snippet,
		ChannelName:    metadata.ChannelName,
		Model:          metadata.Model,
		RequestID:      metadata.RequestID,
		ConversationID: metadata.ConversationID,
	})
	if err != nil {
		log.Printf("[Gate-Review] 写入闸门审计事件失败: %v", err)
	}
}

func truncateGateSnippet(command string) string {
	const limit = 120
	runes := []rune(command)
	if len(runes) <= limit {
		return command
	}
	return string(runes[:limit-3]) + "..."
}

// buildGatePrompt 组装 AI 二次判断的提示词。
func buildGatePrompt(metadata HookContext, match sensitive.CommandMatch) llmgate.Prompt {
	system := "你是命令安全审查员。给定一条即将由 AI 编程助手执行的 shell 命令，判断它是否对系统或数据构成真实风险。" +
		"考虑：不可逆的删除或覆盖（rm -rf、mkfs、dd 写盘、> 覆盖关键文件）、远程下载后直接执行（curl|sh、wget 并 bash）、反弹 shell 与隐蔽外联、提权（sudo/su/chmod 777 敏感路径）、破坏性环境变量改写（PATH 劫持、LD_PRELOAD）。" +
		"同时考虑语境豁免：沙箱/容器/临时目录内、路径明确受限（rm -rf ./build）、只读查询、常见构建测试命令（npm test、go build）不构成风险。" +
		"证据不足时倾向判定 block（保守）。" +
		"必须只输出一个 JSON 对象：{\"verdict\":\"allow\" 或 \"block\",\"reason\":\"简体中文一句话理由\"}，不要输出其他内容。"
	var builder strings.Builder
	builder.WriteString("命中规则: ")
	builder.WriteString(match.Rule)
	builder.WriteString("\n命令来源: ")
	if match.Context == "tool" {
		builder.WriteString("AI 助手的工具调用参数（即将在本机执行）")
	} else {
		builder.WriteString("AI 助手输出中的代码块")
	}
	builder.WriteString("\n入口协议: ")
	builder.WriteString(metadata.APIType)
	if metadata.Model != "" {
		builder.WriteString("\n请求模型: ")
		builder.WriteString(metadata.Model)
	}
	builder.WriteString("\n待审查命令:\n")
	builder.WriteString(match.Command)
	return llmgate.Prompt{
		System: system,
		Messages: []llmgate.PromptMessage{
			{Role: "user", Content: builder.String()},
		},
		MaxTokens: 512,
	}
}

// parseGateVerdict 解析模型返回的裁决 JSON，带关键字回退。
func parseGateVerdict(raw string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", fmt.Errorf("AI 判断返回为空")
	}
	// 主路径：截取首个 { 到最后一个 } 的 JSON 对象。
	if start := strings.Index(trimmed, "{"); start >= 0 {
		if end := strings.LastIndex(trimmed, "}"); end > start {
			var parsed struct {
				Verdict string `json:"verdict"`
				Reason  string `json:"reason"`
			}
			if err := json.Unmarshal([]byte(trimmed[start:end+1]), &parsed); err == nil {
				verdict := strings.ToLower(strings.TrimSpace(parsed.Verdict))
				if verdict == sensitive.GateDecisionAllow || verdict == sensitive.GateDecisionBlock {
					return verdict, strings.TrimSpace(parsed.Reason), nil
				}
			}
		}
	}
	// 回退：JSON 解析失败时按关键字裁决。同时出现时 block 优先（保守）。
	lowered := strings.ToLower(trimmed)
	hasBlock := strings.Contains(lowered, "block")
	hasAllow := strings.Contains(lowered, "allow")
	switch {
	case hasBlock:
		return sensitive.GateDecisionBlock, "（关键字回退）" + firstLine(trimmed), nil
	case hasAllow:
		return sensitive.GateDecisionAllow, "（关键字回退）" + firstLine(trimmed), nil
	default:
		return "", "", fmt.Errorf("AI 判断返回无法解析: %.200s", trimmed)
	}
}

func firstLine(value string) string {
	if index := strings.IndexAny(value, "\r\n"); index >= 0 {
		return value[:index]
	}
	return value
}

package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/llmgate"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/gin-gonic/gin"
)

// gateTestHarness 组装一个可运行的闸门：临时配置 + 闸门存储 + 可编程的 AI 上游。
type gateTestHarness struct {
	gate          *CmdGate
	cfgManager    *config.ConfigManager
	store         *sensitive.GateStore
	recorder      *gateTestRecorder
	aiVerdict     atomic.Value // string：AI 上游下一次返回的 verdict
	aiUnavailable atomic.Bool  // true 时 AI 上游返回 500
	aiCalls       atomic.Int32 // AI 上游累计被调用的次数（去重断言用）
	pendingID     atomic.Int64
}

type gateTestRecorder struct {
	entries []sensitive.BlockedLog
}

func (r *gateTestRecorder) Record(_ context.Context, entry sensitive.BlockedLog) (sensitive.BlockedLog, error) {
	r.entries = append(r.entries, entry)
	return entry, nil
}

func newGateTestHarness(t *testing.T, waitSeconds int) *gateTestHarness {
	t.Helper()
	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })
	settings := cfgManager.GetSettings()
	settings.ContentSafety.DangerousCmd.Enabled = true
	settings.ContentSafety.DangerousCmd.EnabledRules = allDangerousCmdRulesForTest()
	settings.ContentSafety.DangerousCmd.AIGate.Enabled = true
	settings.ContentSafety.DangerousCmd.AIGate.WaitSeconds = waitSeconds
	if err := cfgManager.UpdateSettings(settings); err != nil {
		t.Fatalf("启用闸门配置失败: %v", err)
	}

	store, err := sensitive.NewGateStore(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatalf("创建闸门存储失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	harness := &gateTestHarness{
		cfgManager: cfgManager,
		store:      store,
		recorder:   &gateTestRecorder{},
	}
	harness.aiVerdict.Store("block")

	aiUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		harness.aiCalls.Add(1)
		if harness.aiUnavailable.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream down"}}`))
			return
		}
		verdict, _ := harness.aiVerdict.Load().(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": `{"verdict":"` + verdict + `","reason":"测试理由"}`,
			}},
		})
	}))
	t.Cleanup(aiUpstream.Close)

	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		ID: "gate-ai", Name: "gate-ai", BaseURL: aiUpstream.URL,
		APIKeys: []string{"sk-gate"}, ServiceType: "claude", DefaultModel: "gate-model",
	}); err != nil {
		t.Fatalf("添加 AI 渠道失败: %v", err)
	}

	// Caller 的 metrics 记账依赖真实 scheduler（方法内部直接解引用），
	// 与 content_safety_integration_test 的构造方式一致。
	metricsManagers := []*metrics.MetricsManager{
		metrics.NewMetricsManager(), metrics.NewMetricsManager(), metrics.NewMetricsManager(),
		metrics.NewMetricsManager(), metrics.NewMetricsManager(),
	}
	t.Cleanup(func() {
		for _, manager := range metricsManagers {
			manager.Stop()
		}
	})
	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		metricsManagers[0], metricsManagers[1], metricsManagers[2], metricsManagers[3], metricsManagers[4],
		nil, nil,
	)
	caller := &llmgate.Caller{EnvConfig: config.NewEnvConfig(), CfgManager: cfgManager, ChannelScheduler: channelScheduler}
	harness.gate = NewCmdGate(cfgManager, caller, store, harness.recorder)
	return harness
}

func (h *gateTestHarness) match() sensitive.CommandMatch {
	return sensitive.CommandMatch{
		Rule: "destructive", Command: "rm -rf /", Context: "tool", ToolName: "Bash",
		ExecutionContext: `{"command":"rm -rf /","cwd":"/workspace"}`,
	}
}

func (h *gateTestHarness) metadata() HookContext {
	return HookContext{APIType: "messages", Model: "test-model", RequestID: "req-1", ConversationID: "conv-1"}
}

// TestGateAIBlockThenUserAllows AI 判危险 → 用户放行 → 仅同会话同命令和上下文复用决定。
func TestGateAIBlockThenUserAllows(t *testing.T) {
	h := newGateTestHarness(t, 10)
	h.aiVerdict.Store("block")

	resultCh := make(chan GateVerdict, 1)
	go func() {
		resultCh <- h.gate.Review(context.Background(), h.metadata(), h.match(), nil)
	}()

	// 等 pending 出现（说明请求已挂起在等待用户）。
	var pendingID int64
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := h.gate.ListPending()
		if err != nil {
			t.Fatalf("查询待确认列表失败: %v", err)
		}
		if len(pending) == 1 {
			pendingID = pending[0].ID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pendingID == 0 {
		t.Fatal("3 秒内未出现待确认记录")
	}

	decided, ok, err := h.gate.Decide(pendingID, "allow")
	if err != nil || !ok {
		t.Fatalf("用户放行失败: ok=%v err=%v", ok, err)
	}
	if decided.Status != "allowed" {
		t.Fatalf("pending 状态应为 allowed，实际 %s", decided.Status)
	}

	verdict := <-resultCh
	if !verdict.Allow {
		t.Fatalf("用户已放行，整体应放行: %+v", verdict)
	}

	// 同会话中完全相同的命令和上下文直接复用人工决定。
	h.aiVerdict.Store("block") // 即使 AI 再判危险也轮不到它
	again := h.gate.Review(context.Background(), h.metadata(), h.match(), nil)
	if !again.Allow || again.Reason != "会话内同命令及执行上下文已获用户放行" {
		t.Fatalf("同会话同命令应按人工决定放行: %+v", again)
	}
	h.aiVerdict.Store("allow")
	changed := h.match()
	changed.ExecutionContext = `{"command":"rm -rf /","cwd":"/another-workspace"}`
	if next := h.gate.Review(context.Background(), h.metadata(), changed, nil); !next.Allow || next.AIReview == nil || next.AIReview.Reused {
		t.Fatalf("上下文变化必须重新审核: %+v", next)
	}
	if h.aiCalls.Load() != 2 {
		t.Fatalf("人工决定不能覆盖不同上下文，AI 调用次数=%d", h.aiCalls.Load())
	}
}

// TestGateAIBlockTimeoutExpired AI 判危险 → 无人处理 → 超时拦截。
// 等待窗口取配置下限 3 秒（校验范围 3-120）。
func TestGateAIBlockTimeoutExpired(t *testing.T) {
	h := newGateTestHarness(t, 3)
	h.aiVerdict.Store("block")

	verdict := h.gate.Review(context.Background(), h.metadata(), h.match(), nil)
	if verdict.Allow {
		t.Fatal("超时未放行应拦截")
	}
	if !strings.Contains(verdict.Reason, "超时") {
		t.Fatalf("拒绝理由应说明超时: %+v", verdict)
	}
	// 拦截审计已写入。
	found := false
	for _, entry := range h.recorder.entries {
		if entry.BlockType == sensitive.BlockTypeDangerousCmdAI {
			found = true
		}
	}
	if !found {
		t.Fatal("应写入 dangerous_cmd_ai 审计事件")
	}
}

// TestGateAIAllowAndVeto AI 判安全 → 立即放行 → 否决窗口内加黑名单 → 终止回调触发 + 后续拦截。
func TestGateAIAllowAndVeto(t *testing.T) {
	h := newGateTestHarness(t, 10)
	h.aiVerdict.Store("allow")

	vetoCh := make(chan struct{}, 1)
	verdict := h.gate.Review(context.Background(), h.metadata(), h.match(), func() {
		vetoCh <- struct{}{}
	})
	if !verdict.Allow {
		t.Fatalf("AI 判安全应立即放行: %+v", verdict)
	}

	// pending（否决窗口）应已注册。
	pending, err := h.gate.ListPending()
	if err != nil {
		t.Fatalf("查询待确认列表失败: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("放行后应存在 1 条否决窗口记录，实际 %d", len(pending))
	}

	// AI-allow 窗口拒绝 allow 决定。
	if _, _, err := h.gate.Decide(pending[0].ID, "allow"); err == nil {
		t.Fatal("AI 判安全的窗口不应接受放行决定")
	}
	// 加黑名单：终止回调触发。
	if _, ok, err := h.gate.Decide(pending[0].ID, "block"); err != nil || !ok {
		t.Fatalf("否决失败: ok=%v err=%v", ok, err)
	}
	select {
	case <-vetoCh:
	case <-time.After(2 * time.Second):
		t.Fatal("否决后终止回调未触发")
	}
	// 同会话后续命中按黑名单拦截。
	again := h.gate.Review(context.Background(), h.metadata(), h.match(), nil)
	if again.Allow {
		t.Fatalf("否决后同会话后续命令应拦截: %+v", again)
	}
}

// TestGateAIFailureBlocks AI 渠道失败 → 直接拦截（保守默认）。
func TestGateAIFailureBlocks(t *testing.T) {
	h := newGateTestHarness(t, 10)
	h.aiUnavailable.Store(true)

	verdict := h.gate.Review(context.Background(), h.metadata(), h.match(), nil)
	if verdict.Allow {
		t.Fatal("AI 判断失败应拦截")
	}
	if !strings.Contains(verdict.Reason, "AI 判断不可用") {
		t.Fatalf("拒绝理由应说明 AI 不可用: %+v", verdict)
	}
}

// TestGateDisabledWhenConfigOff 闸门关闭时 Enabled 为 false。
func TestGateDisabledWhenConfigOff(t *testing.T) {
	h := newGateTestHarness(t, 10)
	settings := h.cfgManager.GetSettings()
	settings.ContentSafety.DangerousCmd.AIGate.Enabled = false
	if err := h.cfgManager.UpdateSettings(settings); err != nil {
		t.Fatalf("关闭闸门失败: %v", err)
	}
	if h.gate.Enabled() {
		t.Fatal("闸门关闭后 Enabled 应为 false")
	}
}

// TestParseGateVerdict 确保格式错误、否定句和缺失理由不能被猜测为放行。
func TestParseGateVerdict(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		verdict   string
		wantError bool
	}{
		{name: "纯 JSON", raw: `{"verdict":"allow","reason":"无害"}`, verdict: "allow"},
		{name: "JSON 围栏", raw: "```json\r\n{\"verdict\":\"block\",\"reason\":\"删除根目录\"}\r\n```", verdict: "block"},
		{name: "带前后缀的 JSON", raw: "好的，我的判断是：\n{\"verdict\":\"block\",\"reason\":\"删除根目录\"}\n以上。", wantError: true},
		{name: "关键字不是裁决", raw: "BLOCK: 该命令危险", wantError: true},
		{name: "否定放行", raw: "I do not allow this command", wantError: true},
		{name: "缺失理由", raw: `{"verdict":"allow"}`, wantError: true},
		{name: "无效裁决", raw: `{"verdict":"unknown","reason":"allow or block"}`, wantError: true},
		{name: "无法解析", raw: "我不知道", wantError: true},
		{name: "空输出", raw: "   ", wantError: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			verdict, _, err := parseGateVerdict(testCase.raw)
			if testCase.wantError {
				if err == nil {
					t.Fatalf("应返回解析错误: %q", testCase.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if verdict != testCase.verdict {
				t.Fatalf("verdict = %q, want %q", verdict, testCase.verdict)
			}
		})
	}
}

func TestGateOnlyReviewsRuleMatchesAcrossResponseModes(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		stream, tool bool
	}{
		{name: "非流式文本"},
		{name: "非流式工具参数", tool: true},
		{name: "流式文本", stream: true},
		{name: "流式工具参数", stream: true, tool: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := newGateTestHarness(t, 3)
			h.aiVerdict.Store("allow")
			pipeline := NewContentSafetyPipelineWithRecorder(h.cfgManager, h.recorder, h.gate)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			AttachHookPipeline(c, pipeline, h.metadata())
			t.Cleanup(func() { ClearAttachedSensitiveData(c) })
			toolIndex := 0
			send := func(command string) {
				t.Helper()
				text := "```sh\n" + command + "\n```"
				if testCase.stream {
					var err error
					if testCase.tool {
						arguments, marshalErr := json.Marshal(map[string]string{"command": command})
						if marshalErr != nil {
							t.Fatal(marshalErr)
						}
						toolIndex++
						err = FeedAttachedStreamToolArgumentsForKey(c, fmt.Sprintf("tool-%d", toolIndex), string(arguments), "Bash")
					} else {
						err = FeedAttachedStreamText(c, text)
					}
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				block := map[string]any{"type": "text", "text": text}
				if testCase.tool {
					block = map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]string{"command": command}}
				}
				body, err := json.Marshal(map[string]any{"content": []any{block}})
				if err != nil {
					t.Fatal(err)
				}
				hook := &contentSafetyPostResponseHook{cfgManager: h.cfgManager, recorder: h.recorder, gate: h.gate}
				if _, err := hook.Run(context.Background(), h.metadata(), HookResult{ResponseBody: body}); err != nil {
					t.Fatal(err)
				}
			}
			send("echo safe")
			if h.aiCalls.Load() != 0 || len(h.recorder.entries) != 0 {
				t.Fatal("未命中本地规则的命令不应调用 AI 或产生复核记录")
			}
			send("rm -rf /\nsudo su")
			if h.aiCalls.Load() != 1 || len(h.recorder.entries) != 1 || !strings.Contains(h.recorder.entries[0].Command, "sudo su") {
				t.Fatalf("同一完整脚本的多条规则应一次审核全部内容: calls=%d records=%+v", h.aiCalls.Load(), h.recorder.entries)
			}
			send("LD_PRELOAD=/tmp/evil.so app")
			if h.aiCalls.Load() != 2 || len(h.recorder.entries) != 2 {
				t.Fatalf("第一段放行后仍须复核后续脚本: calls=%d records=%d", h.aiCalls.Load(), len(h.recorder.entries))
			}
			for _, entry := range h.recorder.entries {
				if entry.ConversationID != h.metadata().ConversationID || entry.RequestID != h.metadata().RequestID ||
					entry.AIReview == nil || entry.AIReview.RequestID == "" || entry.AIReview.Model != "gate-model" ||
					entry.AIReview.ChannelName != "gate-ai" || entry.AIReview.Reason != "测试理由" {
					t.Fatalf("复核记录缺少会话关联或实际模型结论: %+v", entry)
				}
			}
		})
	}
}

func TestGateFailureIsAuditedOnce(t *testing.T) {
	h := newGateTestHarness(t, 3)
	h.aiUnavailable.Store(true)
	pipeline := NewContentSafetyPipelineWithRecorder(h.cfgManager, h.recorder, h.gate)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	AttachHookPipeline(c, pipeline, h.metadata())
	t.Cleanup(func() { ClearAttachedSensitiveData(c) })
	err := FeedAttachedStreamToolArgumentsForKey(c, "tool-1", `{"command":"rm -rf /"}`)
	safetyErr := ContentSafetyErrorFrom(err)
	if safetyErr == nil || safetyErr.Code() != "DANGEROUS_COMMAND_BLOCKED" {
		t.Fatalf("AI 失败应明确拦截: %v", err)
	}
	if len(h.recorder.entries) != 1 || h.recorder.entries[0].AIReview == nil || h.recorder.entries[0].AIReview.Verdict != "error" {
		t.Fatalf("一次 AI 失败应只产生一条完整审计记录: %+v", h.recorder.entries)
	}
}

// TestGateStorePendingLifecycle 锁定 GateStore 的 pending 原子迁移。
func TestGateStorePendingLifecycle(t *testing.T) {
	store, err := sensitive.NewGateStore(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatalf("创建闸门存储失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pending, err := store.RecordPending(context.Background(), sensitive.GatePending{
		ConversationID: "conv-1", Rule: "destructive", Command: "rm -rf /",
		AIVerdict: "block", ExpiresAt: time.Now().Add(10 * time.Second),
	})
	if err != nil {
		t.Fatalf("写入待确认记录失败: %v", err)
	}
	waiting, err := store.ListWaiting(context.Background())
	if err != nil || len(waiting) != 1 {
		t.Fatalf("应有 1 条等待记录: %v %d", err, len(waiting))
	}
	// 同一条只能被迁移一次。
	if _, err := store.ResolvePending(context.Background(), pending.ID, "allowed"); err != nil {
		t.Fatalf("首次迁移失败: %v", err)
	}
	if _, err := store.ResolvePending(context.Background(), pending.ID, "blocked"); err != sensitive.ErrGatePendingNotFound {
		t.Fatalf("二次迁移应返回 ErrGatePendingNotFound，实际 %v", err)
	}
	// ExpireStalePending 只影响 waiting。
	if _, err := store.RecordPending(context.Background(), sensitive.GatePending{
		ConversationID: "conv-2", AIVerdict: "block", ExpiresAt: time.Now().Add(time.Second),
	}); err != nil {
		t.Fatalf("写入第二条待确认记录失败: %v", err)
	}
	affected, err := store.ExpireStalePending(context.Background())
	if err != nil || affected != 1 {
		t.Fatalf("应清理 1 条 waiting 记录: %v %d", err, affected)
	}
}

// TestGateStoreDecisions 锁定名单的"每会话每命令最新决定"语义。
func TestGateStoreDecisions(t *testing.T) {
	store, err := sensitive.NewGateStore(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatalf("创建闸门存储失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	commandHash := sensitive.CommandIdentity(sensitive.CommandMatch{Command: "rm -rf /", Context: "tool"})
	otherHash := sensitive.CommandIdentity(sensitive.CommandMatch{Command: "rm -rf /tmp/build", Context: "tool"})
	if _, _, err := store.AddDecision(context.Background(), sensitive.GateDecision{ConversationID: "conv-1", Decision: "allow"}); err == nil {
		t.Fatal("缺少完整命令身份的授权必须显式拒绝")
	}
	if _, _, err := store.AddDecision(context.Background(), sensitive.GateDecision{ConversationID: "conv-1", CommandHash: commandHash, Decision: "allow"}); err != nil {
		t.Fatalf("写入白名单失败: %v", err)
	}
	decision, ok, err := store.LatestDecisionForCommand(context.Background(), "conv-1", commandHash)
	if err != nil || !ok || decision.Decision != "allow" {
		t.Fatalf("应读到 allow: %v %v %+v", err, ok, decision)
	}
	// 后决定覆盖先决定。
	if _, _, err := store.AddDecision(context.Background(), sensitive.GateDecision{ConversationID: "conv-1", CommandHash: commandHash, Decision: "block"}); err != nil {
		t.Fatalf("写入黑名单失败: %v", err)
	}
	decision, ok, err = store.LatestDecisionForCommand(context.Background(), "conv-1", commandHash)
	if err != nil || !ok || decision.Decision != "block" {
		t.Fatalf("最新决定应为 block: %v %v %+v", err, ok, decision)
	}
	if _, _, err := store.AddDecision(context.Background(), sensitive.GateDecision{ConversationID: "conv-1", CommandHash: otherHash, Decision: "allow"}); err != nil {
		t.Fatalf("写入另一命令的决定失败: %v", err)
	}
	// ListDecisions 同会话不同命令独立保留，同命令只保留最后决定。
	all, err := store.ListDecisions(context.Background())
	if err != nil || len(all) != 2 {
		t.Fatalf("每命令应返回各自最新决定: %v %d", err, len(all))
	}
	// 只解除一条命令，不能连带撤销其他命令。
	if _, err := store.RemoveDecisionsForConversation(context.Background(), "conv-1", commandHash); err != nil {
		t.Fatalf("解除名单失败: %v", err)
	}
	if _, ok, err := store.LatestDecisionForCommand(context.Background(), "conv-1", commandHash); err != nil || ok {
		t.Fatalf("解除后不应再有该命令决定: %v", err)
	}
	if decision, ok, err := store.LatestDecisionForCommand(context.Background(), "conv-1", otherHash); err != nil || !ok || decision.Decision != "allow" {
		t.Fatalf("其他命令的决定应保留: %v %v %+v", err, ok, decision)
	}
}

// TestGateAIReviewDedupPerCompleteCommand 只复用完整命令与执行上下文相同的 AI 结论。
func TestGateAIReviewDedupPerCompleteCommand(t *testing.T) {
	h := newGateTestHarness(t, 10)
	h.aiVerdict.Store("allow")

	first := h.gate.Review(context.Background(), h.metadata(), h.match(), nil)
	if !first.Allow {
		t.Fatalf("AI 判安全应放行: %+v", first)
	}
	if calls := h.aiCalls.Load(); calls != 1 {
		t.Fatalf("首次审查应恰好调用一次大模型，实际 %d 次", calls)
	}

	second := h.gate.Review(context.Background(), h.metadata(), h.match(), nil)
	if !second.Allow {
		t.Fatalf("相同命令应按 AI 结论放行: %+v", second)
	}
	if first.AIReview == nil || second.AIReview == nil || first.AIReview.Reused ||
		!second.AIReview.Reused || first.AIReview.RequestID == "" || first.AIReview.RequestID != second.AIReview.RequestID {
		t.Fatalf("复用记录应关联首次复核，且不修改首次记录: first=%+v second=%+v", first.AIReview, second.AIReview)
	}
	if calls := h.aiCalls.Load(); calls != 1 {
		t.Fatalf("相同命令去重后不应再次调用大模型，实际 %d 次", calls)
	}
	pending, err := h.gate.ListPending()
	if err != nil || len(pending) != 2 {
		t.Fatalf("复用 AI 放行仍应为本次调用建立否决窗口: %v %+v", err, pending)
	}

	// 相同命令名但不同目标路径，不能复用前一次结论。
	differentPath := h.match()
	differentPath.Command = "rm -rf /home/user/build"
	differentPath.ExecutionContext = `{"command":"rm -rf /home/user/build","cwd":"/workspace"}`
	third := h.gate.Review(context.Background(), h.metadata(), differentPath, nil)
	if !third.Allow {
		t.Fatalf("新命令经 AI 判安全应放行: %+v", third)
	}
	if calls := h.aiCalls.Load(); calls != 2 {
		t.Fatalf("不同路径应重新送审，实际调用 %d 次", calls)
	}

	differentContext := h.match()
	differentContext.ExecutionContext = `{"command":"rm -rf /","cwd":"/other"}`
	fourth := h.gate.Review(context.Background(), h.metadata(), differentContext, nil)
	if !fourth.Allow || fourth.AIReview == nil || fourth.AIReview.Reused || h.aiCalls.Load() != 3 {
		t.Fatalf("工作目录变化应重新送审: calls=%d verdict=%+v", h.aiCalls.Load(), fourth)
	}

	// 不同会话、相同命令：去重不跨会话。
	otherMetadata := h.metadata()
	otherMetadata.ConversationID = "conv-dedup-other"
	fifth := h.gate.Review(context.Background(), otherMetadata, h.match(), nil)
	if !fifth.Allow {
		t.Fatalf("不同会话 AI 判安全应放行: %+v", fifth)
	}
	if calls := h.aiCalls.Load(); calls != 4 {
		t.Fatalf("不同会话应重新送审，实际调用 %d 次", calls)
	}
}

// TestNormalizeGateCommandKey 不允许折叠可能改变执行语义的参数、引号和操作符。
func TestNormalizeGateCommandKey(t *testing.T) {
	cases := []struct {
		first  string
		second string
	}{
		{"rm -rf /tmp/cache", "rm -rf /"},
		{"git push origin main", "git push --force origin main"},
		{"npm run build:web", "npm run build:api"},
		{"echo safe | cat", "echo safe; rm -rf /"},
		{"rm -rf '$DIR'", "rm -rf \"$DIR\""},
		{"rm -rf /Data", "rm -rf /data"},
		{"FOO=1 rm -rf /tmp/a", "FOO=2 rm -rf /tmp/a"},
	}
	for _, testCase := range cases {
		first := normalizeGateCommandKey(testCase.first)
		if first == "" || first != normalizeGateCommandKey(testCase.first) || first == normalizeGateCommandKey(testCase.second) {
			t.Errorf("不同执行语义不能共用身份: %q / %q", testCase.first, testCase.second)
		}
	}
	if normalizeGateCommandKey("") != "" {
		t.Fatal("空命令不能生成可复用的缓存身份")
	}
}

// TestGateVetoWindowReleasesWaiter 锁定否决窗口结束后的资源清理：
// waiters / vetoes 条目都必须注销，否则进程生命周期内无界增长。
func TestGateVetoWindowReleasesWaiter(t *testing.T) {
	h := newGateTestHarness(t, 10)
	h.aiVerdict.Store("allow")

	verdict := h.gate.Review(context.Background(), h.metadata(), h.match(), nil)
	if !verdict.Allow {
		t.Fatalf("AI 判安全应放行: %+v", verdict)
	}
	h.gate.mu.Lock()
	waiters, vetoes := len(h.gate.waiters), len(h.gate.vetoes)
	h.gate.mu.Unlock()
	if waiters != 0 || vetoes != 0 {
		t.Fatal("非流式放行不应创建后台否决监听")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	verdict = h.gate.Review(ctx, h.metadata(), h.match(), func() { t.Error("请求取消不应触发人工否决") })
	if !verdict.Allow {
		t.Fatalf("AI 缓存放行应成功建立流式否决窗口: %+v", verdict)
	}
	h.gate.mu.Lock()
	waiters, vetoes = len(h.gate.waiters), len(h.gate.vetoes)
	h.gate.mu.Unlock()
	if waiters != 1 || vetoes != 1 {
		t.Fatalf("应存在一个否决监听: waiters=%d vetoes=%d", waiters, vetoes)
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.gate.mu.Lock()
		waiters := len(h.gate.waiters)
		vetoes := len(h.gate.vetoes)
		h.gate.mu.Unlock()
		if waiters == 0 && vetoes == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("否决窗口结束后 waiters 与 vetoes 应全部清理")
}

func TestGateListDecisionNotifiesMatchingPendingOnly(t *testing.T) {
	h := newGateTestHarness(t, 10)
	metadata, match := h.metadata(), h.match()
	pending, decisionCh, err := h.gate.createPending(context.Background(), metadata, match, "block", "测试", true)
	if err != nil {
		t.Fatal(err)
	}
	defer h.gate.unregisterWaiter(pending.ID)
	other := match
	other.ExecutionContext = `{"command":"rm -rf /","cwd":"/different"}`
	otherPending, otherCh, err := h.gate.createPending(context.Background(), metadata, other, "block", "测试", true)
	if err != nil {
		t.Fatal(err)
	}
	defer h.gate.unregisterWaiter(otherPending.ID)
	if _, err := h.gate.AddDecision(metadata.ConversationID, "allow", match.Rule, match.Command, sensitive.CommandIdentity(match)); err != nil {
		t.Fatal(err)
	}
	select {
	case decision := <-decisionCh:
		if decision != "allow" {
			t.Fatalf("决定 = %s", decision)
		}
	default:
		t.Fatal("名单写入成功后必须通知正在等待的同一命令")
	}
	select {
	case <-otherCh:
		t.Fatal("不能放行执行上下文不同的命令")
	default:
	}
	current, err := h.store.GetPending(context.Background(), pending.ID)
	if err != nil || current.Status != "allowed" {
		t.Fatalf("名单与 pending 状态必须同时提交: %+v, %v", current, err)
	}
	late, err := h.store.RecordPending(context.Background(), sensitive.GatePending{
		ConversationID: metadata.ConversationID,
		CommandHash:    sensitive.CommandIdentity(match),
		ExpiresAt:      time.Now().Add(time.Minute),
	})
	if err != nil || late.Status != "allowed" {
		t.Fatalf("在名单提交后才插入的 pending 必须读取实际决定: %+v, %v", late, err)
	}
}

func TestGateListBlockVetoesActiveStream(t *testing.T) {
	h := newGateTestHarness(t, 10)
	h.aiVerdict.Store("allow")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vetoed := make(chan struct{}, 1)
	verdict := h.gate.Review(ctx, h.metadata(), h.match(), func() { vetoed <- struct{}{} })
	if !verdict.Allow {
		t.Fatalf("AI 放行失败: %+v", verdict)
	}
	match := h.match()
	if _, err := h.gate.AddDecision(h.metadata().ConversationID, "block", match.Rule, match.Command, sensitive.CommandIdentity(match)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-vetoed:
	case <-time.After(time.Second):
		t.Fatal("日志页加入黑名单后未否决正在输出的流")
	}
}

func TestSharedAIReviewCancellationIsScopedToWaiter(t *testing.T) {
	gate := NewCmdGate(nil, nil, nil, nil)
	shared, cancelShared := context.WithCancel(context.Background())
	defer cancelShared()
	flight := &inflightGateReview{done: make(chan struct{}), cancel: cancelShared, waiters: 2}
	gate.inflight["shared"] = flight
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstResult := make(chan error, 1)
	secondResult := make(chan GateVerdict, 1)
	go func() {
		_, err := gate.awaitAIReview(first, "shared", flight, false)
		firstResult <- err
	}()
	go func() {
		review, err := gate.awaitAIReview(context.Background(), "shared", flight, true)
		secondResult <- GateVerdict{Allow: err == nil && review.Verdict == "allow", AIReview: &review}
	}()
	cancelFirst()
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("首个等待者应取消: %v", err)
	}
	if shared.Err() != nil {
		t.Fatal("首个客户端取消不应中断其他等待者的模型调用")
	}
	gate.aiReviewMu.Lock()
	flight.review = sensitive.CommandAIReview{Verdict: "allow"}
	close(flight.done)
	gate.aiReviewMu.Unlock()
	verdict := <-secondResult
	if !verdict.Allow || !verdict.AIReview.Reused {
		t.Fatalf("其余等待者应收到合并审核结果: %+v", verdict)
	}
	if !errors.Is(shared.Err(), context.Canceled) {
		t.Fatal("最后一个等待者退出后必须释放共享调用")
	}
}

package hooks

import (
	"context"
	"encoding/json"
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
)

// gateTestHarness 组装一个可运行的闸门：临时配置 + 闸门存储 + 可编程的 AI 上游。
type gateTestHarness struct {
	gate          *CmdGate
	cfgManager    *config.ConfigManager
	store         *sensitive.GateStore
	recorder      *gateTestRecorder
	aiVerdict     atomic.Value // string：AI 上游下一次返回的 verdict
	aiUnavailable atomic.Bool  // true 时 AI 上游返回 500
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
	return sensitive.CommandMatch{Rule: "destructive", Command: "rm -rf /", Context: "tool"}
}

func (h *gateTestHarness) metadata() HookContext {
	return HookContext{APIType: "messages", Model: "test-model", RequestID: "req-1", ConversationID: "conv-1"}
}

// TestGateAIBlockThenUserAllows AI 判危险 → 挂起 → 用户放行 → 整体放行 + 会话白名单。
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

	// 同会话后续命中直接走白名单，不再调用 AI。
	h.aiVerdict.Store("block") // 即使 AI 再判危险也轮不到它
	again := h.gate.Review(context.Background(), h.metadata(), h.match(), nil)
	if !again.Allow || again.Reason != "会话白名单放行" {
		t.Fatalf("同会话后续命令应按白名单放行: %+v", again)
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

// TestParseGateVerdict 锁定模型输出解析：JSON 主路径 + 关键字回退 + 失败。
func TestParseGateVerdict(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		verdict   string
		wantError bool
	}{
		{name: "纯 JSON", raw: `{"verdict":"allow","reason":"无害"}`, verdict: "allow"},
		{name: "带前后缀的 JSON", raw: "好的，我的判断是：\n{\"verdict\":\"block\",\"reason\":\"删除根目录\"}\n以上。", verdict: "block"},
		{name: "关键字回退 block", raw: "BLOCK: 该命令危险", verdict: "block"},
		{name: "关键字回退 allow", raw: "我认为可以 allow 这个命令", verdict: "allow"},
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

// TestGateStoreDecisions 锁定名单的"每会话最新决定"语义。
func TestGateStoreDecisions(t *testing.T) {
	store, err := sensitive.NewGateStore(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatalf("创建闸门存储失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.AddDecision(context.Background(), sensitive.GateDecision{ConversationID: "conv-1", Decision: "allow"}); err != nil {
		t.Fatalf("写入白名单失败: %v", err)
	}
	decision, ok, err := store.LatestDecisionForConversation(context.Background(), "conv-1")
	if err != nil || !ok || decision.Decision != "allow" {
		t.Fatalf("应读到 allow: %v %v %+v", err, ok, decision)
	}
	// 后决定覆盖先决定。
	if _, err := store.AddDecision(context.Background(), sensitive.GateDecision{ConversationID: "conv-1", Decision: "block"}); err != nil {
		t.Fatalf("写入黑名单失败: %v", err)
	}
	decision, ok, err = store.LatestDecisionForConversation(context.Background(), "conv-1")
	if err != nil || !ok || decision.Decision != "block" {
		t.Fatalf("最新决定应为 block: %v %v %+v", err, ok, decision)
	}
	// ListDecisions 每会话一条。
	all, err := store.ListDecisions(context.Background())
	if err != nil || len(all) != 1 {
		t.Fatalf("每会话只应返回最新一条: %v %d", err, len(all))
	}
	// 解除名单。
	if _, err := store.RemoveDecisionsForConversation(context.Background(), "conv-1"); err != nil {
		t.Fatalf("解除名单失败: %v", err)
	}
	if _, ok, _ := store.LatestDecisionForConversation(context.Background(), "conv-1"); ok {
		t.Fatal("解除后不应再有名单决定")
	}
}

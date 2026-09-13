package llmgate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
)

// newGateTestEnv 创建带临时配置文件与调度器的测试环境。
func newGateTestEnv(t *testing.T) (*config.ConfigManager, *scheduler.ChannelScheduler) {
	t.Helper()
	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() {
		if err := cfgManager.Close(); err != nil {
			t.Errorf("关闭配置管理器失败: %v", err)
		}
	})
	// scheduler 的指标方法不允许 nil manager（ShouldSuspendKey 等直接调用其方法），
	// 五协议各建一份，与 content_safety_integration_test 的做法一致。
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
	return cfgManager, channelScheduler
}

func mustAddMessagesChannel(t *testing.T, cfgManager *config.ConfigManager, name, id string, active bool, keys []string) {
	t.Helper()
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		ID:          id,
		Name:        name,
		BaseURL:     "https://" + name + ".example.com",
		APIKeys:     keys,
		ServiceType: "claude",
	}); err != nil {
		t.Fatalf("添加 messages 渠道失败: %v", err)
	}
	if !active {
		cfg := cfgManager.GetConfig()
		for i := range cfg.Upstream {
			if cfg.Upstream[i].Name == name {
				cfg.Upstream[i].Status = "disabled"
				break
			}
		}
	}
}

// TestPickChannelPriorityOrder 锁定自动模式的四协议优先级：
// messages → responses → chat → gemini，先到先得。
func TestPickChannelPriorityOrder(t *testing.T) {
	cfgManager, sch := newGateTestEnv(t)
	// 只建 chat 与 gemini 渠道：messages/responses 为空应被跳过。
	if err := cfgManager.AddChatUpstream(config.UpstreamConfig{ID: "chat-1", Name: "chat-primary", BaseURL: "https://chat.example.com", APIKeys: []string{"sk-chat"}, ServiceType: "openai"}); err != nil {
		t.Fatalf("添加 chat 渠道失败: %v", err)
	}
	if err := cfgManager.AddGeminiUpstream(config.UpstreamConfig{ID: "gemini-1", Name: "gemini-primary", BaseURL: "https://gemini.example.com", APIKeys: []string{"sk-gemini"}, ServiceType: "gemini"}); err != nil {
		t.Fatalf("添加 gemini 渠道失败: %v", err)
	}
	selected, err := PickChannel(context.Background(), cfgManager, sch, "", "")
	if err != nil {
		t.Fatalf("PickChannel 失败: %v", err)
	}
	if selected.Kind != scheduler.ChannelKindChat || selected.Upstream.Name != "chat-primary" {
		t.Fatalf("应优先选择 chat 渠道，实际: kind=%s name=%s", selected.Kind, selected.Upstream.Name)
	}

	// 补一个 messages 渠道后应改选 messages。
	mustAddMessagesChannel(t, cfgManager, "messages-primary", "messages-1", true, []string{"sk-msg"})
	selected, err = PickChannel(context.Background(), cfgManager, sch, "", "")
	if err != nil {
		t.Fatalf("PickChannel 失败: %v", err)
	}
	if selected.Kind != scheduler.ChannelKindMessages || selected.Upstream.Name != "messages-primary" {
		t.Fatalf("应优先选择 messages 渠道，实际: kind=%s name=%s", selected.Kind, selected.Upstream.Name)
	}
}

// TestPickChannelSkipsUnavailable 锁定不可用渠道跳过：无 Key / 未启用的渠道不能被选中。
func TestPickChannelSkipsUnavailable(t *testing.T) {
	cfgManager, sch := newGateTestEnv(t)
	// messages 渠道没有 Key。
	mustAddMessagesChannel(t, cfgManager, "no-key", "messages-1", true, nil)
	// responses 渠道未启用（建满后置为 disabled，与生产"备用池"语义一致）。
	if err := cfgManager.AddResponsesUpstream(config.UpstreamConfig{ID: "resp-1", Name: "disabled-resp", BaseURL: "https://resp.example.com", APIKeys: []string{"sk-resp"}, ServiceType: "openai"}); err != nil {
		t.Fatalf("添加 responses 渠道失败: %v", err)
	}
	if err := cfgManager.SetResponsesChannelStatus(0, config.ChannelStatusDisabled); err != nil {
		t.Fatalf("停用 responses 渠道失败: %v", err)
	}
	if err := cfgManager.AddChatUpstream(config.UpstreamConfig{ID: "chat-1", Name: "chat-primary", BaseURL: "https://chat.example.com", APIKeys: []string{"sk-chat"}, ServiceType: "openai"}); err != nil {
		t.Fatalf("添加 chat 渠道失败: %v", err)
	}
	selected, err := PickChannel(context.Background(), cfgManager, sch, "", "")
	if err != nil {
		t.Fatalf("PickChannel 失败: %v", err)
	}
	if selected.Kind != scheduler.ChannelKindChat {
		t.Fatalf("无 Key 与未启用的渠道应被跳过，实际选到: kind=%s name=%s", selected.Kind, selected.Upstream.Name)
	}
}

// TestPickChannelNoChannel 锁定无可用渠道时显式报错（闸门依赖该错误走"失败即拦截"）。
func TestPickChannelNoChannel(t *testing.T) {
	cfgManager, sch := newGateTestEnv(t)
	if _, err := PickChannel(context.Background(), cfgManager, sch, "", ""); err == nil {
		t.Fatal("没有任何渠道时应返回错误")
	}
}

// TestPickChannelExplicitID 锁定显式指定行为：命中即用，不存在时显式报错（不静默回退）。
func TestPickChannelExplicitID(t *testing.T) {
	cfgManager, sch := newGateTestEnv(t)
	mustAddMessagesChannel(t, cfgManager, "messages-primary", "messages-1", true, []string{"sk-msg"})
	selected, err := PickChannel(context.Background(), cfgManager, sch, "messages-1", "")
	if err != nil {
		t.Fatalf("PickChannel 显式指定失败: %v", err)
	}
	if selected.Upstream.Name != "messages-primary" {
		t.Fatalf("应选中显式指定的渠道，实际: %s", selected.Upstream.Name)
	}
	if _, err := PickChannel(context.Background(), cfgManager, sch, "not-exist", ""); err == nil {
		t.Fatal("指定不存在的渠道 ID 应返回错误")
	}
}

// TestPickChannelGroupRouting 锁定编排选渠：模型 ID（分组匹配词）命中分组内渠道，
// 空模型走兜底分组；编排产生的 in-flight 预留由 PreviewChannel 立即释放。
func TestPickChannelGroupRouting(t *testing.T) {
	cfgManager, sch := newGateTestEnv(t)
	pool, err := cfgManager.CreateChannelPool("messages", config.ChannelPool{Name: "glm 组", ModelMatcher: "glm"})
	if err != nil {
		t.Fatalf("创建分组失败: %v", err)
	}
	// 分组内渠道与兜底分组渠道各一，分组内渠道优先级更靠后也必须被模型路由选中。
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		ID: "messages-glm", Name: "glm-channel", BaseURL: "https://glm.example.com",
		APIKeys: []string{"sk-glm"}, ServiceType: "claude", PoolID: pool.ID, Priority: 9,
	}); err != nil {
		t.Fatalf("添加分组渠道失败: %v", err)
	}
	mustAddMessagesChannel(t, cfgManager, "fallback-channel", "messages-fallback", true, []string{"sk-fallback"})

	// releasePick 释放编排选渠占用的 in-flight 预留（直接调 PickChannel 的调用方职责）。
	releasePick := func(selected *SelectedChannel) {
		if selected.Reserved {
			sch.ReleaseChannelReservation(selected.Kind, selected.ChannelIndex)
		}
	}

	selected, err := PickChannel(context.Background(), cfgManager, sch, "", "glm")
	if err != nil {
		t.Fatalf("PickChannel 失败: %v", err)
	}
	if selected.Upstream.Name != "glm-channel" {
		t.Fatalf("模型 glm 应命中分组渠道，实际: %s", selected.Upstream.Name)
	}
	releasePick(selected)

	selected, err = PickChannel(context.Background(), cfgManager, sch, "", "")
	if err != nil {
		t.Fatalf("PickChannel 失败: %v", err)
	}
	if selected.Upstream.Name != "fallback-channel" {
		t.Fatalf("空模型应走兜底分组，实际: %s", selected.Upstream.Name)
	}
	releasePick(selected)

	caller := &Caller{EnvConfig: config.NewEnvConfig(), CfgManager: cfgManager, ChannelScheduler: sch}
	preview, err := caller.PreviewChannel(context.Background(), "", "glm")
	if err != nil {
		t.Fatalf("PreviewChannel 失败: %v", err)
	}
	if preview.Upstream.Name != "glm-channel" {
		t.Fatalf("PreviewChannel 应命中分组渠道，实际: %s", preview.Upstream.Name)
	}
	if preview.Reserved {
		t.Fatal("PreviewChannel 不应保留 in-flight 预留")
	}
	if inFlight := sch.GetChannelInFlight(scheduler.ChannelKindMessages, preview.ChannelIndex); inFlight != 0 {
		t.Fatalf("PreviewChannel 应释放 in-flight 预留，实际: %d", inFlight)
	}
}

// TestCallClaudeUpstream 走完整 provider 链路：Claude serviceType 透传 + 响应文本抽取。
func TestCallClaudeUpstream(t *testing.T) {
	var receivedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &receivedBody); err != nil {
			t.Errorf("上游收到的请求体不是合法 JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "msg_test",
			"role":    "assistant",
			"model":   "test-model",
			"content": []map[string]any{{"type": "text", "text": "{\"verdict\":\"allow\",\"reason\":\"测试命令无害\"}"}},
			"usage":   map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	t.Cleanup(upstream.Close)

	cfgManager, sch := newGateTestEnv(t)
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		ID:           "messages-1",
		Name:         "claude-primary",
		BaseURL:      upstream.URL,
		APIKeys:      []string{"sk-test"},
		ServiceType:  "claude",
		DefaultModel: "test-model",
	}); err != nil {
		t.Fatalf("添加 messages 渠道失败: %v", err)
	}
	envCfg := config.NewEnvConfig()
	caller := &Caller{EnvConfig: envCfg, CfgManager: cfgManager, ChannelScheduler: sch}
	result, err := caller.Call(context.Background(), "", Prompt{
		System:   "你是安全审查员",
		Messages: []PromptMessage{{Role: "user", Content: "命令: rm -rf /tmp/test"}},
	})
	if err != nil {
		t.Fatalf("Call 失败: %v", err)
	}
	if !strings.Contains(result.Text, "allow") {
		t.Fatalf("应抽取到响应文本，实际: %q", result.Text)
	}
	if result.Usage == nil || result.Usage.InputTokens != 10 {
		t.Fatalf("应返回上游 usage，实际: %+v", result.Usage)
	}
	// 请求体应为 Claude Messages 格式（provider 链路的输入契约）；
	// system 会被 provider 的缓存断点注入改写为 blocks 数组，断言取其文本。
	systemText := ""
	switch system := receivedBody["system"].(type) {
	case string:
		systemText = system
	case []any:
		for _, block := range system {
			if typed, ok := block.(map[string]any); ok {
				if text, _ := typed["text"].(string); text != "" {
					systemText = text
				}
			}
		}
	}
	if systemText != "你是安全审查员" {
		t.Fatalf("system 提示词未送达上游: %+v", receivedBody["system"])
	}
	if receivedBody["model"] != "test-model" {
		t.Fatalf("应使用渠道 DefaultModel，实际: %v", receivedBody["model"])
	}
	if receivedBody["stream"] != false {
		t.Fatalf("内部调用必须非流式: %v", receivedBody["stream"])
	}
}

// TestCallTimeout 锁定整体超时生效（闸门依赖超时错误走"失败即拦截"）。
func TestCallTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	cfgManager, sch := newGateTestEnv(t)
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		ID: "messages-1", Name: "slow", BaseURL: upstream.URL,
		APIKeys: []string{"sk-test"}, ServiceType: "claude", DefaultModel: "test-model",
	}); err != nil {
		t.Fatalf("添加渠道失败: %v", err)
	}
	caller := &Caller{EnvConfig: config.NewEnvConfig(), CfgManager: cfgManager, ChannelScheduler: sch, TimeoutSecs: 1}
	if _, err := caller.Call(context.Background(), "", Prompt{Messages: []PromptMessage{{Role: "user", Content: "ping"}}}); err == nil {
		t.Fatal("上游超时应返回错误")
	}
}

package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/llmgate"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
)

// newAssistantTestEnv 组装可运行的助手服务：临时配置 + 可编程模型上游。
type assistantTestEnv struct {
	cfgManager *config.ConfigManager
	service    *Service
	modelReply chan string // 模型上游下一次返回的完整回复文本
}

func newAssistantTestEnv(t *testing.T) *assistantTestEnv {
	t.Helper()
	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	env := &assistantTestEnv{cfgManager: cfgManager, modelReply: make(chan string, 4)}
	modelUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 1<<16)
		n, _ := r.Body.Read(body)
		select {
		case reply := <-env.modelReply:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{{"type": "text", "text": reply}},
			})
		default:
			// 没有预设回复时返回占位（带完整请求体片段以便调试）。
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{{"type": "text", "text": "占位回复：" + string(body[:min(n, 200)])}},
			})
		}
	}))
	t.Cleanup(modelUpstream.Close)

	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		ID: "assistant-model", Name: "assistant-model", BaseURL: modelUpstream.URL,
		APIKeys: []string{"sk-secret-key-do-not-leak"}, ServiceType: "claude", DefaultModel: "assistant-model",
	}); err != nil {
		t.Fatalf("添加模型渠道失败: %v", err)
	}

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

	store, err := NewStore(filepath.Join(t.TempDir(), "assistant.db"))
	if err != nil {
		t.Fatalf("创建助手存储失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := NewService(cfgManager, caller, store)
	if err != nil {
		t.Fatalf("创建助手服务失败: %v", err)
	}
	env.service = service
	return env
}

// TestAssistantChatCreatesChannel 助手按行动协议创建渠道（无 Key），
// 且系统提示词绝不包含任何 API Key 明文。
func TestAssistantChatCreatesChannel(t *testing.T) {
	env := newAssistantTestEnv(t)
	session, err := env.service.CreateSession("测试会话")
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	env.modelReply <- "好的，我来帮你创建：\n```assistant-action\n{\"action\":\"create_channel\",\"kind\":\"chat\",\"name\":\"测试渠道\",\"baseUrl\":\"https://api.example.com\",\"serviceType\":\"openai\",\"defaultModel\":\"gpt-5\"}\n```\n已创建，请去渠道页填 Key。"

	outcome, err := env.service.Chat(context.Background(), session.ID, "帮我建一个 chat 渠道")
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if len(outcome.Actions) != 1 || !outcome.Actions[0].Success {
		t.Fatalf("应成功执行 1 个行动: %+v", outcome.Actions)
	}
	// 渠道已创建（无 Key）。
	found := false
	for _, upstream := range env.cfgManager.GetConfig().ChatUpstream {
		if upstream.Name == "测试渠道" {
			found = true
			if len(upstream.APIKeys) != 0 {
				t.Fatal("助手创建的渠道不应携带 API Key")
			}
		}
	}
	if !found {
		t.Fatal("渠道未创建")
	}
	// 回复正文不残留行动块。
	if strings.Contains(outcome.AssistantMessage.Content, "assistant-action") {
		t.Fatalf("回复正文不应包含行动块: %q", outcome.AssistantMessage.Content)
	}
}

// TestAssistantPromptNeverLeaksKeys 锁定 key 保护契约：发给模型的请求体中
// 不含任何渠道 API Key 明文（摘要只展示"已隐藏"）。
func TestAssistantPromptNeverLeaksKeys(t *testing.T) {
	env := newAssistantTestEnv(t)
	session, err := env.service.CreateSession("key 泄漏测试")
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	env.modelReply <- "收到"

	// 再加一个带显眼 Key 的渠道，确认摘要也不会带出去。
	if err := env.cfgManager.AddChatUpstream(config.UpstreamConfig{
		ID: "chat-with-key", Name: "带Key渠道", BaseURL: "https://chat.example.com",
		APIKeys: []string{"sk-leak-canary-123456"}, ServiceType: "openai", DefaultModel: "m",
	}); err != nil {
		t.Fatalf("添加渠道失败: %v", err)
	}

	_, err = env.service.Chat(context.Background(), session.ID, "列出我现在的渠道")
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	// 模型上游收到的是上次请求——直接校验 buildSystemPrompt 本身（覆盖摘要全量内容）。
	system := buildSystemPrompt(env.cfgManager)
	if strings.Contains(system, "sk-secret-key-do-not-leak") || strings.Contains(system, "sk-leak-canary-123456") {
		t.Fatal("系统提示词泄漏了 API Key 明文")
	}
	if !strings.Contains(system, "已隐藏") {
		t.Fatal("摘要应说明 Key 已隐藏")
	}
	if !strings.Contains(system, "带Key渠道") {
		t.Fatal("摘要应包含渠道名（脱敏不脱配置结构）")
	}
}

// TestAssistantRejectsUnknownAction 未登记行动显式拒绝。
func TestAssistantRejectsUnknownAction(t *testing.T) {
	env := newAssistantTestEnv(t)
	session, _ := env.service.CreateSession("拒绝测试")
	env.modelReply <- "```assistant-action\n{\"action\":\"delete_channel\",\"kind\":\"chat\",\"channelId\":\"x\"}\n```"

	outcome, err := env.service.Chat(context.Background(), session.ID, "删掉渠道")
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if len(outcome.Actions) != 1 || outcome.Actions[0].Success {
		t.Fatalf("未登记行动应失败: %+v", outcome.Actions)
	}
	if !strings.Contains(outcome.Actions[0].Detail, "不支持的行动") {
		t.Fatalf("应说明白名单: %+v", outcome.Actions[0])
	}
}

// TestExtractActions 锁定行动块解析：提取、剥离、未闭合围栏。
func TestExtractActions(t *testing.T) {
	reply := "先说明：\n```assistant-action\n{\"action\":\"create_pool\",\"kind\":\"chat\",\"name\":\"gpt5\"}\n```\n创建完成。"
	actions, body, err := extractActions(reply)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(actions) != 1 || actions[0].Action != "create_pool" || actions[0].Name != "gpt5" {
		t.Fatalf("行动解析错误: %+v", actions)
	}
	if strings.Contains(body, "assistant-action") || !strings.Contains(body, "创建完成") {
		t.Fatalf("正文剥离错误: %q", body)
	}

	// 无行动块。
	actions, body, err = extractActions("纯文本回复")
	if err != nil || len(actions) != 0 || body != "纯文本回复" {
		t.Fatalf("纯文本解析错误: %v %+v %q", err, actions, body)
	}

	// 未闭合围栏按无行动处理，正文保留。
	actions, body, err = extractActions("```assistant-action\n{\"action\":\"create_pool\"")
	if err != nil || len(actions) != 0 || !strings.Contains(body, "assistant-action") {
		t.Fatalf("未闭合围栏处理错误: %v %+v %q", err, actions, body)
	}
}

// TestAssistantUpdateChannelKeepsKeys 更新渠道（defaultModel）后原 Key 原样保留。
func TestAssistantUpdateChannelKeepsKeys(t *testing.T) {
	env := newAssistantTestEnv(t)
	if err := env.cfgManager.AddChatUpstream(config.UpstreamConfig{
		ID: "chat-keep", Name: "保留Key渠道", BaseURL: "https://keep.example.com",
		APIKeys: []string{"sk-original-key"}, ServiceType: "openai", DefaultModel: "old-model",
	}); err != nil {
		t.Fatalf("添加渠道失败: %v", err)
	}
	session, _ := env.service.CreateSession("更新测试")
	env.modelReply <- "```assistant-action\n{\"action\":\"update_channel\",\"kind\":\"chat\",\"channelId\":\"chat-keep\",\"defaultModel\":\"new-model\"}\n```\n已更新默认模型。"

	outcome, err := env.service.Chat(context.Background(), session.ID, "把默认模型改成 new-model")
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if len(outcome.Actions) != 1 || !outcome.Actions[0].Success {
		t.Fatalf("行动应成功: %+v", outcome.Actions)
	}
	for _, upstream := range env.cfgManager.GetConfig().ChatUpstream {
		if upstream.ID == "chat-keep" {
			if upstream.DefaultModel != "new-model" {
				t.Fatalf("默认模型未更新: %q", upstream.DefaultModel)
			}
			if len(upstream.APIKeys) != 1 || upstream.APIKeys[0] != "sk-original-key" {
				t.Fatalf("原有 Key 被破坏: %+v", upstream.APIKeys)
			}
			return
		}
	}
	t.Fatal("未找到渠道")
}

// TestAssistantStoreLifecycle 会话与消息持久化。
func TestAssistantStoreLifecycle(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "assistant.db"))
	if err != nil {
		t.Fatalf("创建存储失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	session, err := store.CreateSession("")
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if _, err := store.AppendMessage(Message{SessionID: session.ID, Role: "user", Content: "你好"}); err != nil {
		t.Fatalf("写入用户消息失败: %v", err)
	}
	if _, err := store.AppendMessage(Message{
		SessionID: session.ID, Role: "assistant", Content: "已处理",
		Actions: []ActionRecord{{Action: "create_channel", Success: true, Detail: "ok"}},
	}); err != nil {
		t.Fatalf("写入助手消息失败: %v", err)
	}
	messages, err := store.ListMessages(session.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("应读到 2 条消息: %v %d", err, len(messages))
	}
	if len(messages[1].Actions) != 1 || !messages[1].Actions[0].Success {
		t.Fatalf("行动结果未持久化: %+v", messages[1].Actions)
	}
	sessions, err := store.ListSessions()
	if err != nil || len(sessions) != 1 {
		t.Fatalf("应读到 1 个会话: %v %d", err, len(sessions))
	}
	if deleted, err := store.DeleteSession(session.ID); err != nil || !deleted {
		t.Fatalf("删除会话失败: %v %v", deleted, err)
	}
	if messages, err := store.ListMessages(session.ID); err != nil || len(messages) != 0 {
		t.Fatalf("删除会话后消息应清空: %v %d", err, len(messages))
	}
}

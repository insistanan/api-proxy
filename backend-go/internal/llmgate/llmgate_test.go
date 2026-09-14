package llmgate

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/api-proxy/internal/circuit"
	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/logger"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
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

func TestReadBoundedResponsePreservesSupportedEncodings(t *testing.T) {
	for _, encoding := range []string{"", "gzip", "deflate", "gzip-without-header"} {
		t.Run(encoding, func(t *testing.T) {
			plain := []byte(`{"content":[{"type":"text","text":"ok"}]}`)
			body := plain
			if encoding != "" {
				var compressed bytes.Buffer
				var writer io.WriteCloser
				if encoding == "deflate" {
					writer = zlib.NewWriter(&compressed)
				} else {
					writer = gzip.NewWriter(&compressed)
				}
				if _, err := writer.Write(plain); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				body = compressed.Bytes()
			}
			response := &http.Response{Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}
			if encoding != "gzip-without-header" {
				response.Header.Set("Content-Encoding", encoding)
			}
			decoded, err := readBoundedResponse(response)
			if err != nil || !bytes.Equal(decoded, plain) {
				t.Fatalf("解码结果 = %s, %v", decoded, err)
			}
		})
	}
}

func TestReadBoundedResponseLimitsCompressedAndDecodedBodies(t *testing.T) {
	large := bytes.Repeat([]byte("x"), maxResponseBodyBytes+1)
	if _, err := readBoundedResponse(&http.Response{Body: io.NopCloser(bytes.NewReader(large))}); err == nil {
		t.Fatal("原始响应体超过上限时必须报错")
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(large); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	response := &http.Response{Body: io.NopCloser(bytes.NewReader(compressed.Bytes()))}
	if _, err := readBoundedResponse(response); !errors.Is(err, utils.ErrDecompressedBodyTooLarge) {
		t.Fatalf("小压缩体展开超过上限时必须保留错误类型: %v", err)
	}
}

func TestGateNetworkErrorIsRetryableAndMasksActualKey(t *testing.T) {
	key := "q8!"
	cause := errors.New("request https://example.invalid/?key=" + key + ": connection closed")
	err := &gateAttemptError{Err: cause, message: redactCallText(cause.Error(), key)}
	if strings.Contains(err.Error(), key) {
		t.Fatal("即使实际 API Key 很短，错误文本也必须隐藏它")
	}
	if !errors.Is(err, cause) {
		t.Fatal("脱敏不能破坏原始错误链")
	}
	if !isRetryable(err) {
		t.Fatal("尚未收到 HTTP 状态码的网络错误应尝试下一 URL 或渠道")
	}
	if isRetryable(&gateAttemptError{StatusCode: http.StatusBadRequest}) {
		t.Fatal("客户端参数错误不能当成网络故障重试")
	}
}

func mustAddMessagesChannel(t *testing.T, cfgManager *config.ConfigManager, name, id string, active bool, keys []string) {
	t.Helper()
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		ID:           id,
		Name:         name,
		BaseURL:      "https://" + name + ".example.com",
		APIKeys:      keys,
		ServiceType:  "claude",
		DefaultModel: "test-model",
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
	if err := cfgManager.AddChatUpstream(config.UpstreamConfig{ID: "chat-1", Name: "chat-primary", BaseURL: "https://chat.example.com", APIKeys: []string{"sk-chat"}, ServiceType: "openai", DefaultModel: "chat-model"}); err != nil {
		t.Fatalf("添加 chat 渠道失败: %v", err)
	}
	if err := cfgManager.AddGeminiUpstream(config.UpstreamConfig{ID: "gemini-1", Name: "gemini-primary", BaseURL: "https://gemini.example.com", APIKeys: []string{"sk-gemini"}, ServiceType: "gemini", DefaultModel: "gemini-model"}); err != nil {
		t.Fatalf("添加 gemini 渠道失败: %v", err)
	}
	selected, err := PickChannel(context.Background(), cfgManager, sch, "", "", nil)
	if err != nil {
		t.Fatalf("PickChannel 失败: %v", err)
	}
	if selected.Kind != scheduler.ChannelKindChat || selected.Upstream.Name != "chat-primary" {
		t.Fatalf("应优先选择 chat 渠道，实际: kind=%s name=%s", selected.Kind, selected.Upstream.Name)
	}

	// 补一个 messages 渠道后应改选 messages。
	mustAddMessagesChannel(t, cfgManager, "messages-primary", "messages-1", true, []string{"sk-msg"})
	selected, err = PickChannel(context.Background(), cfgManager, sch, "", "", nil)
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
	if err := cfgManager.AddChatUpstream(config.UpstreamConfig{ID: "chat-1", Name: "chat-primary", BaseURL: "https://chat.example.com", APIKeys: []string{"sk-chat"}, ServiceType: "openai", DefaultModel: "chat-model"}); err != nil {
		t.Fatalf("添加 chat 渠道失败: %v", err)
	}
	selected, err := PickChannel(context.Background(), cfgManager, sch, "", "", nil)
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
	if _, err := PickChannel(context.Background(), cfgManager, sch, "", "", nil); err == nil {
		t.Fatal("没有任何渠道时应返回错误")
	}
}

// TestPickChannelExplicitID 锁定显式指定行为：命中即用，不存在时显式报错（不静默回退）。
func TestPickChannelExplicitID(t *testing.T) {
	cfgManager, sch := newGateTestEnv(t)
	mustAddMessagesChannel(t, cfgManager, "messages-primary", "messages-1", true, []string{"sk-msg"})
	selected, err := PickChannel(context.Background(), cfgManager, sch, "messages-1", "", nil)
	if err != nil {
		t.Fatalf("PickChannel 显式指定失败: %v", err)
	}
	if selected.Upstream.Name != "messages-primary" {
		t.Fatalf("应选中显式指定的渠道，实际: %s", selected.Upstream.Name)
	}
	if _, err := PickChannel(context.Background(), cfgManager, sch, "not-exist", "", nil); err == nil {
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

	// releasePick 释放编排选渠占用的许可（直接调 PickChannel 的调用方职责）。
	releasePick := func(selected *SelectedChannel) {
		if selected.Permit != nil {
			selected.Permit.Release()
			selected.Permit = nil
		}
	}

	selected, err := PickChannel(context.Background(), cfgManager, sch, "", "glm", nil)
	if err != nil {
		t.Fatalf("PickChannel 失败: %v", err)
	}
	if selected.Upstream.Name != "glm-channel" {
		t.Fatalf("模型 glm 应命中分组渠道，实际: %s", selected.Upstream.Name)
	}
	releasePick(selected)

	selected, err = PickChannel(context.Background(), cfgManager, sch, "", "", nil)
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
	if preview.Permit != nil {
		t.Fatal("PreviewChannel 不应保留许可（in-flight 预留 / 探测名额）")
	}
	if inFlight := sch.GetChannelInFlight(scheduler.ChannelKindMessages, preview.ChannelIndex); inFlight != 0 {
		t.Fatalf("PreviewChannel 应释放 in-flight 预留，实际: %d", inFlight)
	}
}

// TestPickChannelSkipsModelUnresolvableChannels 锁定：解析不出模型的渠道
// （空模型 + 无 DefaultModel 且无映射）在选渠阶段即被排除，不再等到调用期
// 报"未能解析可用模型"。
func TestPickChannelSkipsModelUnresolvableChannels(t *testing.T) {
	cfgManager, sch := newGateTestEnv(t)
	// messages 渠道优先级更高但空模型解析不出模型。
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		ID: "messages-1", Name: "no-default-model", BaseURL: "https://no-model.example.com",
		APIKeys: []string{"sk"}, ServiceType: "claude", Priority: 1,
	}); err != nil {
		t.Fatalf("添加 messages 渠道失败: %v", err)
	}
	if err := cfgManager.AddChatUpstream(config.UpstreamConfig{
		ID: "chat-1", Name: "chat-primary", BaseURL: "https://chat.example.com",
		APIKeys: []string{"sk-chat"}, ServiceType: "openai", DefaultModel: "chat-model",
	}); err != nil {
		t.Fatalf("添加 chat 渠道失败: %v", err)
	}
	selected, err := PickChannel(context.Background(), cfgManager, sch, "", "", nil)
	if err != nil {
		t.Fatalf("PickChannel 失败: %v", err)
	}
	if selected.Kind != scheduler.ChannelKindChat || selected.Upstream.Name != "chat-primary" {
		t.Fatalf("应跳过解析不出模型的渠道，实际: kind=%s name=%s", selected.Kind, selected.Upstream.Name)
	}
	if selected.Permit != nil {
		selected.Permit.Release()
		selected.Permit = nil
	}
}

// TestPreviewChannelReleasesCircuitProbe 锁定：PreviewChannel 占用的熔断半开
// 探测名额必须释放，否则"状态栏显示该渠道、实际请求却选不中它"。
func TestPreviewChannelReleasesCircuitProbe(t *testing.T) {
	cfgManager, sch := newGateTestEnv(t)
	mustAddMessagesChannel(t, cfgManager, "primary", "messages-1", true, []string{"sk-msg"})
	circuitManager := circuit.NewManager(circuit.Config{
		FailureThreshold: 1, MinRequests: 1, Cooldown: 20 * time.Millisecond, SuccessThreshold: 1,
	}, true)
	sch.SetCircuitManager(circuitManager)
	// 触发熔断 Open，等冷却到期（下次 Allow 惰性转 HalfOpen）。
	sch.RecordCircuitFailure(scheduler.ChannelKindMessages, 0)
	time.Sleep(30 * time.Millisecond)

	caller := &Caller{EnvConfig: config.NewEnvConfig(), CfgManager: cfgManager, ChannelScheduler: sch}
	preview, err := caller.PreviewChannel(context.Background(), "", "")
	if err != nil {
		t.Fatalf("PreviewChannel 失败: %v", err)
	}
	if preview.Upstream.Name != "primary" {
		t.Fatalf("冷却到期的半开渠道应可被探测选中，实际: %s", preview.Upstream.Name)
	}
	snap := circuitManager.SnapshotEntry("messages", 0)
	if snap.State != circuit.StateHalfOpen {
		t.Fatalf("渠道应处于半开状态，实际: %s", snap.State)
	}
	if snap.HalfOpenInFlight != 0 {
		t.Fatalf("PreviewChannel 必须释放熔断探测名额，实际 halfOpenInFlight=%d", snap.HalfOpenInFlight)
	}
}

// TestCallFailoverAcrossChannels 锁定自动模式渠道级故障转移：
// 首选渠道整体失败后按四协议优先级换渠道，而不是整体报错。
func TestCallFailoverAcrossChannels(t *testing.T) {
	var brokenCount atomic.Int32
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		brokenCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream broken"}}`))
	}))
	t.Cleanup(broken.Close)
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_ok","choices":[{"message":{"role":"assistant","content":"pong"}}]}`))
	}))
	t.Cleanup(working.Close)

	cfgManager, sch := newGateTestEnv(t)
	// messages 首选渠道始终 500；chat 渠道正常。
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		ID: "messages-1", Name: "broken", BaseURL: broken.URL,
		APIKeys: []string{"sk-broken"}, ServiceType: "claude", DefaultModel: "m1",
	}); err != nil {
		t.Fatalf("添加 messages 渠道失败: %v", err)
	}
	if err := cfgManager.AddChatUpstream(config.UpstreamConfig{
		ID: "chat-1", Name: "working", BaseURL: working.URL,
		APIKeys: []string{"sk-working"}, ServiceType: "openai", DefaultModel: "m2",
	}); err != nil {
		t.Fatalf("添加 chat 渠道失败: %v", err)
	}

	caller := &Caller{EnvConfig: config.NewEnvConfig(), CfgManager: cfgManager, ChannelScheduler: sch}
	result, err := caller.Call(context.Background(), "", Prompt{Messages: []PromptMessage{{Role: "user", Content: "ping"}}})
	if err != nil {
		t.Fatalf("渠道级失败应换渠道重试，实际整体失败: %v", err)
	}
	if result.Text != "pong" {
		t.Fatalf("应取到 chat 渠道响应，实际: %q", result.Text)
	}
	if brokenCount.Load() == 0 {
		t.Fatal("首选 messages 渠道应已被尝试过")
	}
}

// TestCallClaudeUpstream 走完整 provider 链路：Claude serviceType 透传 + 响应文本抽取。
func TestCallClaudeUpstream(t *testing.T) {
	previousWriter, previousFlags := log.Writer(), log.Flags()
	previousGinWriter, previousGinErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	logStore, err := logger.Setup(&logger.Config{DBPath: filepath.Join(t.TempDir(), "logs.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logger.CloseDefault(); err != nil {
			t.Error(err)
		}
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		gin.DefaultWriter, gin.DefaultErrorWriter = previousGinWriter, previousGinErrorWriter
	})
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
	sch.SetRequestLogStore(metrics.NewRequestLogStore(logStore))
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
	envCfg.EnableRequestLogs = true
	envCfg.EnableResponseLogs = true
	caller := &Caller{EnvConfig: envCfg, CfgManager: cfgManager, ChannelScheduler: sch}
	result, err := caller.Call(context.Background(), "", Prompt{
		System:   "你是安全审查员",
		Messages: []PromptMessage{{Role: "user", Content: "命令: rm -rf /tmp/test"}},
		Metadata: CallMetadata{RequestID: "review-1", ParentRequestID: "request-1", ConversationID: "conversation-1", APIType: "cmd_gate"},
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
	if result.RequestID != "review-1" || result.Model != "test-model" || result.ChannelName != "claude-primary" {
		t.Fatalf("应返回实际复核调用信息: %+v", result)
	}
	logStore.Flush()
	// cmd_gate 类调用不写流量正文（防闸门复核刷屏，见 a7da115）：
	// 请求元数据仍完整记录（下方 QueryRequestLogs 断言）。
	traffic, err := logStore.QueryTrafficByRequestID(context.Background(), "review-1")
	if err != nil || len(traffic) != 0 {
		t.Fatalf("cmd_gate 调用不应记录流量正文: %+v err=%v", traffic, err)
	}
	payloads, err := logStore.QueryRequestLogs(context.Background(), logger.QueryOptions{RequestID: "review-1"})
	if err != nil || len(payloads) != 1 {
		t.Fatalf("网页请求日志应有内部调用记录: %d err=%v", len(payloads), err)
	}
	var recorded metrics.RequestLogEntry
	if err := json.Unmarshal(payloads[0], &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.ParentRequestID != "request-1" || recorded.ConversationID != "conversation-1" || recorded.APIType != "cmd_gate" || !recorded.Success {
		t.Fatalf("复核日志应关联原请求与原会话: %+v", recorded)
	}
	// 非 cmd_gate 的内部调用（如助手）开启正文日志时记录
	// 提交内容、上游请求和响应三条流量。
	if _, err := caller.Call(context.Background(), "", Prompt{
		Messages: []PromptMessage{{Role: "user", Content: "assistant ping"}},
		Metadata: CallMetadata{RequestID: "review-body", ParentRequestID: "request-2"},
	}); err != nil {
		t.Fatal(err)
	}
	logStore.Flush()
	traffic, err = logStore.QueryTrafficByRequestID(context.Background(), "review-body")
	if err != nil || len(traffic) != 3 {
		t.Fatalf("非 cmd_gate 调用应持久化提交内容、上游请求和响应: %+v err=%v", traffic, err)
	}
	for _, entry := range traffic {
		if entry.APIType == "cmd_gate" || strings.Contains(entry.HeadersJSON, "sk-test") {
			t.Fatalf("调用分类应正确且请求头密钥必须脱敏: %+v", entry)
		}
	}
	if traffic[1].AttemptID == "" || traffic[1].AttemptID != traffic[2].AttemptID {
		t.Fatal("上游请求和响应必须属于同一次尝试")
	}
	envCfg.EnableRequestLogs = false
	envCfg.EnableResponseLogs = false
	if _, err := caller.Call(context.Background(), "", Prompt{
		Messages: []PromptMessage{{Role: "user", Content: "不记录正文"}},
		Metadata: CallMetadata{RequestID: "review-no-body"},
	}); err != nil {
		t.Fatal(err)
	}
	logStore.Flush()
	traffic, err = logStore.QueryTrafficByRequestID(context.Background(), "review-no-body")
	if err != nil || len(traffic) != 0 {
		t.Fatalf("关闭正文日志后不应继续记录正文: %+v err=%v", traffic, err)
	}
	payloads, err = logStore.QueryRequestLogs(context.Background(), logger.QueryOptions{RequestID: "review-no-body"})
	if err != nil || len(payloads) != 1 {
		t.Fatalf("关闭正文日志不应丢失请求元数据: %d err=%v", len(payloads), err)
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

// TestPickChannelAutoScansNamedPools 锁定自动模式（空模型）的全池扫描语义：
// 主链路分组路由对空模型只进兜底分组，挂在命名分组的渠道会被整体跳过——
// 内部调用不能继承该语义（用户场景：messages 渠道全部挂在命名分组，
// 自动模式却降级到 chat）。空模型必须扫描命名分组内的可用渠道。
func TestPickChannelAutoScansNamedPools(t *testing.T) {
	cfgManager, sch := newGateTestEnv(t)
	pool, err := cfgManager.CreateChannelPool("messages", config.ChannelPool{Name: "命名分组", ModelMatcher: "foo"})
	if err != nil {
		t.Fatalf("创建分组失败: %v", err)
	}
	// messages 渠道全部挂在命名分组（兜底分组为空），且配置了默认模型。
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		ID: "messages-named", Name: "named-channel", BaseURL: "https://named.example.com",
		APIKeys: []string{"sk-named"}, ServiceType: "claude", PoolID: pool.ID, DefaultModel: "named-model",
	}); err != nil {
		t.Fatalf("添加命名分组渠道失败: %v", err)
	}
	if err := cfgManager.AddChatUpstream(config.UpstreamConfig{
		ID: "chat-1", Name: "chat-fallback", BaseURL: "https://chat.example.com",
		APIKeys: []string{"sk-chat"}, ServiceType: "openai", DefaultModel: "chat-model",
	}); err != nil {
		t.Fatalf("添加 chat 渠道失败: %v", err)
	}

	selected, err := PickChannel(context.Background(), cfgManager, sch, "", "", nil)
	if err != nil {
		t.Fatalf("PickChannel 失败: %v", err)
	}
	if selected.Kind != scheduler.ChannelKindMessages || selected.Upstream.Name != "named-channel" {
		t.Fatalf("自动模式应全池扫描选中 messages 命名分组渠道，实际: kind=%s name=%s", selected.Kind, selected.Upstream.Name)
	}
	if selected.Permit != nil {
		selected.Permit.Release()
		selected.Permit = nil
	}
}

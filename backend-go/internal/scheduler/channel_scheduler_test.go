package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/conversation"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/session"
	"github.com/BenedictKing/api-proxy/internal/urlhealth"
)

// createTestConfigManager 创建测试用配置管理器
func createTestConfigManager(t *testing.T, cfg config.Config) (*config.ConfigManager, func()) {
	t.Helper()

	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "scheduler-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}

	// 创建临时配置文件
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("序列化配置失败: %v", err)
	}

	if err := os.WriteFile(configFile, data, 0644); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("写入配置文件失败: %v", err)
	}

	// 创建配置管理器
	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("创建配置管理器失败: %v", err)
	}

	cleanup := func() {
		cfgManager.Close()
		os.RemoveAll(tmpDir)
	}

	return cfgManager, cleanup
}

// createTestScheduler 创建测试用调度器
func createTestScheduler(t *testing.T, cfg config.Config) (*ChannelScheduler, func()) {
	t.Helper()

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()
	urlManager := urlhealth.NewURLManager(30*time.Second, 3)

	scheduler := NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, urlManager)

	return scheduler, func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		imagesMetrics.Stop()
		cleanup()
	}
}

// TestPromotionOrderingIsFuzzyOnly 锁定新契约：抢优先级（促销期）只在 Fuzzy
// 模式打开时生效，把促销渠道按位次整体提前；Fuzzy 关闭时固定走位次第一位渠道，
// 促销设置不参与调度。
func TestPromotionOrderingIsFuzzyOnly(t *testing.T) {
	// 设置促销截止时间为 5 分钟后
	promotionUntil := time.Now().Add(5 * time.Minute)

	cfg := config.Config{
		MessagePools: []config.ChannelPool{
			{ID: "biz", Name: "Biz", ModelMatcher: "claude", Priority: 1},
			{ID: config.DefaultChannelPoolID, Name: config.FallbackChannelPoolName, ModelMatcher: "*", Priority: 2},
		},
		Upstream: []config.UpstreamConfig{
			{
				Name:     "normal-channel",
				PoolID:   "biz",
				BaseURL:  "https://normal.example.com",
				APIKeys:  []string{"sk-normal-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted-channel",
				PoolID:         "biz",
				BaseURL:        "https://promoted.example.com",
				APIKeys:        []string{"sk-promoted-key"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil,
			},
		},
	}

	cases := []struct {
		name  string
		fuzzy bool
		want  int
	}{
		{name: "Fuzzy 关闭：促销不参与调度", fuzzy: false, want: 0},
		{name: "Fuzzy 打开：促销按位次提前", fuzzy: true, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scheduler, cleanup := createTestScheduler(t, cfg)
			defer cleanup()

			if err := scheduler.configManager.SetFuzzyModeEnabled(tc.fuzzy); err != nil {
				t.Fatalf("设置 Fuzzy 模式失败: %v", err)
			}

			result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "claude-3-5-sonnet")
			if err != nil {
				t.Fatalf("选择渠道失败: %v", err)
			}
			defer scheduler.ReleaseChannelReservation(result.Kind, result.ChannelIndex)

			if result.ChannelIndex != tc.want {
				t.Errorf("期望选择 index=%d，实际选择了 index=%d", tc.want, result.ChannelIndex)
			}
		})
	}
}

// TestSelectChannel_PromotionPrecedesTraceAffinity 锁定新契约：Trace 亲和不再
// 参与选渠；Fuzzy 模式下促销（抢优先级）渠道按位次整体提前，压过普通渠道位次。
func TestSelectChannel_PromotionPrecedesTraceAffinity(t *testing.T) {
	promotionUntil := time.Now().Add(5 * time.Minute)
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "affinity-channel",
				BaseURL:  "https://affinity.example.com",
				APIKeys:  []string{"sk-affinity"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted-channel",
				BaseURL:        "https://promoted.example.com",
				APIKeys:        []string{"sk-promoted"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 强优先级（抢优先级）只在 Fuzzy 模式下生效。
	if err := scheduler.configManager.SetFuzzyModeEnabled(true); err != nil {
		t.Fatalf("设置 Fuzzy 模式失败: %v", err)
	}

	const conversationID = "promotion-before-affinity"
	scheduler.SetTraceAffinityForKind(ChannelKindMessages, conversationID, 0)

	result, err := scheduler.SelectChannel(context.Background(), conversationID, map[int]bool{}, ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	defer scheduler.ReleaseChannelReservation(result.Kind, result.ChannelIndex)

	if result.ChannelIndex != 1 || result.Reason != "priority_order" {
		t.Fatalf("Fuzzy 模式下促销渠道应压倒 Trace 亲和，got index=%d reason=%s", result.ChannelIndex, result.Reason)
	}
}

func TestSelectChannel_ConfigOrderPrecedesPromotion(t *testing.T) {
	promotionUntil := time.Now().Add(5 * time.Minute)
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "override-channel",
				BaseURL:  "https://override.example.com",
				APIKeys:  []string{"sk-override"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted-channel",
				BaseURL:        "https://promoted.example.com",
				APIKeys:        []string{"sk-promoted"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	registry := conversation.NewRegistry()
	defer registry.Stop()
	record := registry.ObserveRequest(conversation.Observation{
		APIKind:        string(ChannelKindMessages),
		ConversationID: "override-before-promotion",
	})
	if _, err := registry.SetRouteOverride(record.ID, string(ChannelKindMessages), 0, "override-channel"); err != nil {
		t.Fatalf("SetRouteOverride() error = %v", err)
	}
	scheduler.SetConversationRegistry(registry)

	result, err := scheduler.SelectChannel(context.Background(), record.ID, map[int]bool{}, ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	defer scheduler.ReleaseChannelReservation(result.Kind, result.ChannelIndex)

	if result.ChannelIndex != 0 || result.Reason != "conversation_route_override" {
		t.Fatalf("对话路由覆盖应优先于促销，got index=%d reason=%s", result.ChannelIndex, result.Reason)
	}
}

// TestSelectChannel_ConfigOrderPrecedesTraceAffinity 锁定 864841f 后的调度契约：
// Trace 亲和不再改写配置顺序（生产选渠不使用亲和结果，仅会话观测保留）。
func TestSelectChannel_ConfigOrderPrecedesTraceAffinity(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "priority-channel",
				BaseURL:  "https://priority.example.com",
				APIKeys:  []string{"sk-priority"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "affinity-channel",
				BaseURL:  "https://affinity.example.com",
				APIKeys:  []string{"sk-affinity"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	const conversationID = "affinity-without-promotion"
	scheduler.SetTraceAffinityForKind(ChannelKindMessages, conversationID, 1)

	result, err := scheduler.SelectChannel(context.Background(), conversationID, map[int]bool{}, ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	defer scheduler.ReleaseChannelReservation(result.Kind, result.ChannelIndex)

	if result.ChannelIndex != 0 || result.Reason != "priority_order" {
		t.Fatalf("配置顺序应优先于 Trace 亲和，got index=%d reason=%s", result.ChannelIndex, result.Reason)
	}
}

func TestSchedulerScopesChannelCountAndSelectionToModelPool(t *testing.T) {
	cfg := config.Config{
		MessagePools: []config.ChannelPool{
			{ID: "sonnet", Name: "Sonnet", ModelMatcher: "sonnet", Priority: 1},
			{ID: config.DefaultChannelPoolID, Name: "兜底分组", ModelMatcher: "*", Priority: 2},
		},
		Upstream: []config.UpstreamConfig{
			{ID: "default", Name: "default", PoolID: config.DefaultChannelPoolID, BaseURL: "https://default.example.com", APIKeys: []string{"sk-default"}, Status: "active", Priority: 1},
			{ID: "sonnet-a", Name: "sonnet-a", PoolID: "sonnet", BaseURL: "https://sonnet-a.example.com", APIKeys: []string{"sk-a"}, Status: "active", Priority: 2},
			{ID: "sonnet-b", Name: "sonnet-b", PoolID: "sonnet", BaseURL: "https://sonnet-b.example.com", APIKeys: []string{"sk-b"}, Status: "active", Priority: 3},
			{ID: "vision", Name: "vision", PoolID: "sonnet", BaseURL: "https://vision.example.com", APIKeys: []string{"sk-vision"}, Status: "active", VisionCapable: true, ExcludeFromConversation: true},
		},
	}
	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	if got := scheduler.GetActiveChannelCountForModel(ChannelKindMessages, "claude-sonnet-4"); got != 3 {
		t.Fatalf("sonnet channel count = %d, want 3", got)
	}
	if got := scheduler.GetActiveChannelCountForModel(ChannelKindMessages, "claude-opus-4"); got != 1 {
		t.Fatalf("default channel count = %d, want 1", got)
	}

	selected, err := scheduler.SelectChannel(context.Background(), "pool-user", map[int]bool{}, ChannelKindMessages, "claude-sonnet-4")
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	defer scheduler.ReleaseChannelReservation(selected.Kind, selected.ChannelIndex)
	if selected.Upstream.PoolID != "sonnet" || selected.Upstream.ExcludeFromConversation {
		t.Fatalf("selected upstream = %#v", selected.Upstream)
	}
}

func TestMatchedGroupPrecedesFallbackGroupAndFallsBackAfterFailure(t *testing.T) {
	promotionUntil := time.Now().Add(5 * time.Minute)
	cfg := config.Config{
		MessagePools: []config.ChannelPool{
			{ID: "deepseek", Name: "DeepSeek", ModelMatcher: "deepseek", Priority: 1},
			{ID: config.DefaultChannelPoolID, Name: "兜底分组", ModelMatcher: "*", Priority: 2},
		},
		Upstream: []config.UpstreamConfig{
			{ID: "default-promoted", Name: "default-promoted", PoolID: config.DefaultChannelPoolID, BaseURL: "https://default.example.com", APIKeys: []string{"sk-default"}, Status: "active", Priority: 2, PromotionUntil: &promotionUntil},
			{ID: "deepseek-promoted", Name: "deepseek-promoted", PoolID: "deepseek", BaseURL: "https://deepseek.example.com", APIKeys: []string{"sk-deepseek"}, Status: "active", Priority: 1, PromotionUntil: &promotionUntil},
		},
	}
	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	if got := scheduler.GetActiveChannelCountForModel(ChannelKindMessages, "deepseek-chat"); got != 2 {
		t.Fatalf("deepseek 可尝试渠道数 = %d, want 2", got)
	}

	first, err := scheduler.SelectChannel(context.Background(), "pool-user", map[int]bool{}, ChannelKindMessages, "deepseek-chat")
	if err != nil {
		t.Fatalf("首次选择渠道失败: %v", err)
	}
	if first.ChannelIndex != 1 || first.Reason != "priority_order" {
		t.Fatalf("首次应选择命中分组渠道，got index=%d reason=%s", first.ChannelIndex, first.Reason)
	}
	scheduler.ReleaseChannelReservation(first.Kind, first.ChannelIndex)

	second, err := scheduler.SelectChannel(context.Background(), "pool-user", map[int]bool{first.ChannelIndex: true}, ChannelKindMessages, "deepseek-chat")
	if err != nil {
		t.Fatalf("命中分组渠道失败后的兜底选择失败: %v", err)
	}
	defer scheduler.ReleaseChannelReservation(second.Kind, second.ChannelIndex)
	if second.ChannelIndex != 0 || second.Reason != "priority_order" {
		t.Fatalf("命中分组失败后应进入兜底分组，got index=%d reason=%s", second.ChannelIndex, second.Reason)
	}
}

func TestSchedulerUsesFallbackGroupWhenMatchedGroupHasNoActiveChannel(t *testing.T) {
	cfg := config.Config{
		MessagePools: []config.ChannelPool{
			{ID: "sonnet", Name: "Sonnet", ModelMatcher: "sonnet", Priority: 1},
			{ID: config.DefaultChannelPoolID, Name: "兜底分组", ModelMatcher: "*", Priority: 2},
		},
		Upstream: []config.UpstreamConfig{
			{ID: "fallback", Name: "fallback", PoolID: config.DefaultChannelPoolID, BaseURL: "https://fallback.example.com", APIKeys: []string{"sk-fallback"}, Status: "active", Priority: 1},
			{ID: "sonnet", Name: "sonnet", PoolID: "sonnet", BaseURL: "https://sonnet.example.com", APIKeys: []string{"sk-sonnet"}, Status: "suspended", Priority: 1},
		},
	}
	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	selected, err := scheduler.SelectChannel(context.Background(), "pool-user", map[int]bool{}, ChannelKindMessages, "claude-sonnet-4")
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	defer scheduler.ReleaseChannelReservation(selected.Kind, selected.ChannelIndex)
	if selected.ChannelIndex != 0 || selected.Upstream.PoolID != config.DefaultChannelPoolID {
		t.Fatalf("selected upstream = %#v", selected.Upstream)
	}
}

// TestPromotedChannelSkippedAfterFailure 测试促销渠道在本次请求失败后被跳过
func TestPromotedChannelSkippedAfterFailure(t *testing.T) {
	promotionUntil := time.Now().Add(5 * time.Minute)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "normal-channel",
				BaseURL:  "https://normal.example.com",
				APIKeys:  []string{"sk-normal-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted-channel",
				BaseURL:        "https://promoted.example.com",
				APIKeys:        []string{"sk-promoted-key"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 标记促销渠道已失败
	failedChannels := map[int]bool{1: true}

	// 选择渠道 - 应该跳过促销渠道，选择普通渠道
	result, err := scheduler.SelectChannel(context.Background(), "test-user", failedChannels, ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 0 {
		t.Errorf("期望选择普通渠道 (index=0)，实际选择了 index=%d", result.ChannelIndex)
	}

	if result.Upstream.Name != "normal-channel" {
		t.Errorf("期望选择 normal-channel，实际选择了 %s", result.Upstream.Name)
	}
}

// TestFailureRateDoesNotAffectSelection 锁定新契约：失败率只用于前端展示，
// 不再参与选渠（渠道健康由三态熔断器负责），位次第一的渠道即使失败率很高
// 也仍然是本次请求的首选。
func TestFailureRateDoesNotAffectSelection(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "unhealthy-channel",
				BaseURL:  "https://unhealthy.example.com",
				APIKeys:  []string{"sk-unhealthy-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "healthy-channel",
				BaseURL:  "https://healthy.example.com",
				APIKeys:  []string{"sk-healthy-key"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 模拟第一个渠道失败率极高（旧实现会因此跳过它）
	metricsManager := scheduler.MetricsManager(ChannelKindMessages)
	for i := 0; i < 10; i++ {
		metricsManager.RecordFailure("https://unhealthy.example.com", "sk-unhealthy-key", 0)
	}

	// 选择渠道 - 失败率不影响选渠，仍按位次选择第一个渠道
	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	defer scheduler.ReleaseChannelReservation(result.Kind, result.ChannelIndex)

	if result.ChannelIndex != 0 {
		t.Errorf("期望按位次选择渠道 (index=0)，实际选择了 index=%d", result.ChannelIndex)
	}

	if result.Upstream.Name != "unhealthy-channel" {
		t.Errorf("期望选择 unhealthy-channel，实际选择了 %s", result.Upstream.Name)
	}
}

// TestExpiredPromotionDoesNotApply 测试过期的促销不再参与选渠
func TestExpiredPromotionDoesNotApply(t *testing.T) {
	// 设置促销截止时间为过去
	promotionUntil := time.Now().Add(-5 * time.Minute)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "healthy-channel",
				BaseURL:  "https://healthy.example.com",
				APIKeys:  []string{"sk-healthy-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "expired-promoted-channel",
				BaseURL:        "https://expired.example.com",
				APIKeys:        []string{"sk-expired-key"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil, // 已过期
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 模拟过期促销渠道不健康
	metricsManager := scheduler.MetricsManager(ChannelKindMessages)
	for i := 0; i < 10; i++ {
		metricsManager.RecordFailure("https://expired.example.com", "sk-expired-key", 1)
	}

	// 选择渠道 - 过期促销渠道不应该被优先选择，应该选择健康的渠道
	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 0 {
		t.Errorf("期望选择健康渠道 (index=0)，实际选择了 index=%d", result.ChannelIndex)
	}

	if result.Upstream.Name != "healthy-channel" {
		t.Errorf("期望选择 healthy-channel，实际选择了 %s", result.Upstream.Name)
	}
}

// TestSelectChannel_DuplicatePrioritiesNormalizeToFailoverOrder
// 验证：重复优先级会被配置层归一化为严格故障转移顺序，只有当前渠道失败后才进入下一渠道。
func TestSelectChannel_DuplicatePrioritiesNormalizeToFailoverOrder(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "provider-a",
				BaseURL:  "https://a.example.com",
				APIKeys:  []string{"key-a"},
				Priority: 1,
				Status:   "active",
			},
			{
				Name:     "provider-b",
				BaseURL:  "https://b.example.com",
				APIKeys:  []string{"key-b"},
				Priority: 1,
				Status:   "active",
			},
			{
				Name:     "provider-c",
				BaseURL:  "https://c.example.com",
				APIKeys:  []string{"key-c"},
				Priority: 1,
				Status:   "active",
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 使用不同 conversationID，避免 Trace 亲和把后续请求钉死在同一渠道
	first, err := scheduler.SelectChannel(context.Background(), "user-1", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("第一次选渠失败: %v", err)
	}
	if !first.Reserved() {
		t.Fatal("期望第一次选渠占用 in-flight 预留")
	}

	second, err := scheduler.SelectChannel(context.Background(), "user-2", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("第二次选渠失败: %v", err)
	}
	if second.ChannelIndex != first.ChannelIndex {
		t.Fatalf("严格优先级下应保持首选渠道，first=%d second=%d (reasons: %s, %s)",
			first.ChannelIndex, second.ChannelIndex, first.Reason, second.Reason)
	}

	failedChannels := map[int]bool{first.ChannelIndex: true}
	third, err := scheduler.SelectChannel(context.Background(), "user-3", failedChannels, ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("第三次选渠失败: %v", err)
	}
	if third.ChannelIndex == first.ChannelIndex {
		t.Fatalf("首选渠道失败后未切换，first=%d third=%d", first.ChannelIndex, third.ChannelIndex)
	}

	scheduler.ReleaseChannelReservation(second.Kind, second.ChannelIndex)
	scheduler.ReleaseChannelReservation(third.Kind, third.ChannelIndex)
	scheduler.ReleaseChannelReservation(first.Kind, first.ChannelIndex)
	fourth, err := scheduler.SelectChannel(context.Background(), "user-4", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("第四次选渠失败: %v", err)
	}
	if fourth.ChannelIndex != first.ChannelIndex {
		t.Fatalf("恢复后期望回到首选 index=%d，实际 index=%d (reason=%s)",
			first.ChannelIndex, fourth.ChannelIndex, fourth.Reason)
	}
}

// TestSelectChannel_ProtocolIsolation
// 验证：messages 与 responses 的在途负载彼此独立，不会互相抢占分摊结果。
func TestSelectChannel_ProtocolIsolation(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "msg-provider-a",
				BaseURL:  "https://a.example.com",
				APIKeys:  []string{"key-a"},
				Priority: 1,
				Status:   "active",
			},
			{
				Name:     "msg-provider-b",
				BaseURL:  "https://b.example.com",
				APIKeys:  []string{"key-b"},
				Priority: 1,
				Status:   "active",
			},
		},
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:     "resp-provider-a",
				BaseURL:  "https://ra.example.com",
				APIKeys:  []string{"rkey-a"},
				Priority: 1,
				Status:   "active",
			},
			{
				Name:     "resp-provider-b",
				BaseURL:  "https://rb.example.com",
				APIKeys:  []string{"rkey-b"},
				Priority: 1,
				Status:   "active",
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	msg1, err := scheduler.SelectChannel(context.Background(), "msg-user-1", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("messages 选渠失败: %v", err)
	}
	msg2, err := scheduler.SelectChannel(context.Background(), "msg-user-2", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("messages 第二次选渠失败: %v", err)
	}
	if msg1.ChannelIndex != msg2.ChannelIndex {
		t.Fatalf("messages 严格优先级应保持首选渠道: %d != %d", msg1.ChannelIndex, msg2.ChannelIndex)
	}

	// responses 协议有独立渠道列表与 in-flight 计数，首次选择不应被 messages 占用影响
	resp1, err := scheduler.SelectChannel(context.Background(), "resp-user-1", make(map[int]bool), ChannelKindResponses, "")
	if err != nil {
		t.Fatalf("responses 选渠失败: %v", err)
	}
	if resp1.ChannelIndex != 0 && resp1.ChannelIndex != 1 {
		t.Fatalf("responses 选渠 index 非法: %d", resp1.ChannelIndex)
	}

	// responses 也独立遵循自身严格优先级顺序
	resp2, err := scheduler.SelectChannel(context.Background(), "resp-user-2", make(map[int]bool), ChannelKindResponses, "")
	if err != nil {
		t.Fatalf("responses 第二次选渠失败: %v", err)
	}
	if resp1.ChannelIndex != resp2.ChannelIndex {
		t.Fatalf("responses 严格优先级应保持首选渠道: %d != %d", resp1.ChannelIndex, resp2.ChannelIndex)
	}
	scheduler.ReleaseChannelReservation(msg1.Kind, msg1.ChannelIndex)
	scheduler.ReleaseChannelReservation(msg2.Kind, msg2.ChannelIndex)
	scheduler.ReleaseChannelReservation(resp1.Kind, resp1.ChannelIndex)
	scheduler.ReleaseChannelReservation(resp2.Kind, resp2.ChannelIndex)
}

// TestSelectChannel_AdaptiveRespectsFailoverPriority
// 验证：带模型画像的自适应评分也不能越过配置优先级。
func TestSelectChannel_AdaptiveRespectsFailoverPriority(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "provider-a",
				BaseURL:  "https://a.example.com",
				APIKeys:  []string{"key-a"},
				Priority: 1,
				Status:   "active",
			},
			{
				Name:     "provider-b",
				BaseURL:  "https://b.example.com",
				APIKeys:  []string{"key-b"},
				Priority: 1,
				Status:   "active",
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 预热两边画像，避免一边无画像导致评分差过大
	if scheduler.profileManager != nil {
		for _, item := range []struct {
			baseURL string
			key     string
			idx     int
		}{
			{"https://a.example.com", "key-a", 0},
			{"https://b.example.com", "key-b", 1},
		} {
			for i := 0; i < 3; i++ {
				requestID := uint64(item.idx*100 + i + 1)
				scheduler.profileManager.StartRequest(item.baseURL, []string{item.key}, "claude-sonnet-4", item.idx, requestID)
				scheduler.profileManager.EndRequest(item.baseURL, []string{item.key}, "claude-sonnet-4", item.idx, requestID, true, 100)
			}
		}
	}

	first, err := scheduler.SelectChannel(context.Background(), "adaptive-user-1", make(map[int]bool), ChannelKindMessages, "claude-sonnet-4")
	if err != nil {
		t.Fatalf("adaptive 第一次选渠失败: %v", err)
	}
	second, err := scheduler.SelectChannel(context.Background(), "adaptive-user-2", make(map[int]bool), ChannelKindMessages, "claude-sonnet-4")
	if err != nil {
		t.Fatalf("adaptive 第二次选渠失败: %v", err)
	}
	if first.ChannelIndex != second.ChannelIndex {
		t.Fatalf("adaptive 路径越过了严格优先级: first=%d second=%d (reasons: %s, %s)",
			first.ChannelIndex, second.ChannelIndex, first.Reason, second.Reason)
	}
	scheduler.ReleaseChannelReservation(first.Kind, first.ChannelIndex)
	scheduler.ReleaseChannelReservation(second.Kind, second.ChannelIndex)
}

// TestChannelScheduler_StopIdempotent 验证：聚合 Stop 幂等，重复调用不 panic
// （下属组件的 Stop 是裸 close(channel)，幂等由调度器的 sync.Once 保证）。
func TestChannelScheduler_StopIdempotent(t *testing.T) {
	s, cleanup := createTestScheduler(t, config.Config{})
	defer cleanup()

	s.Stop()
	s.Stop() // 第二次调用不应 panic
}

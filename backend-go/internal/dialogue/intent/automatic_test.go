package intent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/stretchr/testify/require"
)

func automaticConfig() config.IntentRoutingConfig {
	cfg := config.DefaultIntentRouting()
	cfg.Enabled = true
	cfg.Strategy = "auto"
	cfg.Groups = []config.IntentGroupCapability{
		{PoolID: "language", Tasks: []string{"translation", "writing"}, Tiers: []int{0, 1}},
		{PoolID: "coding", Tasks: []string{"code_generation", "debugging"}, Tiers: []int{1}},
		{PoolID: "strong-code", Tasks: []string{"code_generation", "debugging"}, Tiers: []int{2}},
	}
	return cfg
}

func TestAutomaticMatchesTaskBeforeDifficulty(t *testing.T) {
	s := &Service{}
	for _, tc := range []struct {
		text, task, pool string
		tier             int
	}{
		{"翻译这封邮件", "translation", "language", 0},
		{"实现一个分页接口", "code_generation", "coding", 1},
		{"实现一个并发队列", "code_generation", "strong-code", 2},
		{"翻译这份分布式架构文档", "translation", "", 2},
		{"润色这封邮件", "writing", "language", 1},
	} {
		t.Run(tc.text, func(t *testing.T) {
			d, err := s.Classify(context.Background(), automaticConfig(), Input{Text: tc.text}, "test")
			require.NoError(t, err)
			require.Equal(t, tc.task, d.Task)
			require.Equal(t, tc.tier, d.Tier)
			require.Equal(t, tc.pool, d.PoolID)
		})
	}
	d, err := s.Classify(context.Background(), automaticConfig(), Input{Text: "嗯，再看看"}, "test")
	require.NoError(t, err)
	require.Empty(t, d.PoolID)
	require.Empty(t, d.Task)
	require.Equal(t, -1, d.Tier)
}

func TestAutomaticFeedbackIsScopedToTaskAndDifficulty(t *testing.T) {
	s := intentTestService(t)
	state := s.cloneStateLocked()
	state.putEvent(Event{RequestID: "coding", Protocol: "messages", Success: true,
		Decision: Decision{Task: "code_generation", Tier: 1}, Target: Target{PoolID: "preferred", ChannelID: "channel", Model: "model"}})
	state.putEvent(Event{RequestID: "legacy", Protocol: "messages", Success: true,
		Decision: Decision{Tier: 1}, Target: Target{PoolID: "preferred", ChannelID: "channel", Model: "model"}})
	require.NoError(t, s.saveLocked(state))
	require.NoError(t, s.Feedback("messages", "coding", true))
	require.NoError(t, s.Feedback("messages", "legacy", true))

	for _, tc := range []struct {
		protocol, task string
		tier           int
		pool           string
	}{
		{"messages", "code_generation", 1, "preferred"},
		{"messages", "translation", 1, "first"},
		{"messages", "code_generation", 2, "first"},
		{"chat", "code_generation", 1, "first"},
		{"messages", "", 1, "preferred"},
	} {
		routes := []config.IntentRoute{{PoolID: "first", Tier: tc.tier}, {PoolID: "preferred", Tier: tc.tier}}
		require.Equal(t, tc.pool, s.adaptiveChoice(tc.protocol, tc.task, routes).PoolID, "%+v", tc)
	}
	store, restored, err := openStateStore(s.path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.db.Close()) })
	// 存储恢复也必须保留任务维度，不能在重启后合并各任务的评价。
	require.Equal(t, s.state.Quality, restored.Quality)
}

func TestAutomaticResidenceDoesNotMislabelNewTaskFeedback(t *testing.T) {
	s := intentTestService(t)
	cfg := automaticConfig()
	cfg.Groups = []config.IntentGroupCapability{{PoolID: "default", Tasks: []string{"translation", "code_generation"}, Tiers: []int{0, 1, 2}}}
	require.NoError(t, s.Cfg.UpdateIntentRouting("messages", cfg))
	up := &config.UpstreamConfig{ID: "channel", PoolID: "default", ServiceType: "claude", BaseURL: "https://example.invalid"}
	for i, input := range []string{`{"input":"翻译你好"}`, `{"input":"实现一个函数"}`, `{"input":"实现一个函数"}`} {
		request := fmt.Sprint(i)
		ctx, release, err := s.Begin(context.Background(), "messages", "residence", request, []byte(input), "")
		require.NoError(t, err)
		turn := TurnFrom(ctx)
		if i > 0 {
			require.Empty(t, turn.event.Decision.Task)
			require.Equal(t, -1, turn.event.Decision.Tier)
		}
		require.NoError(t, turn.CommitTarget(up, "key", "model"))
		require.NoError(t, turn.Complete(true))
		release()
		if i > 0 {
			require.NoError(t, s.Feedback("messages", request, true))
		}
	}
	require.Empty(t, s.state.Quality)
}

func TestAutomaticFeedbackCannotSelectIneligibleGroup(t *testing.T) {
	s := intentTestService(t)
	cfg := automaticConfig()
	cfg.UseFeedback = true
	// 再多满意反馈也不能把复杂翻译送到只声明代码能力的分组。
	s.state.Quality["messages\x002\x00strong-code\x00channel\x00model"] = quality{Good: 1000}
	d, err := s.Classify(WithProtocol(context.Background(), "messages"), cfg, Input{Text: "翻译分布式架构文档"}, "test")
	require.NoError(t, err)
	require.Empty(t, d.PoolID)
	cfg.Classifier = "llm"
	cfg.ClassifierModel = "classifier"
	_, err = s.Classify(context.Background(), cfg, Input{Text: "翻译"}, "test")
	require.ErrorContains(t, err, "辅助推理服务")
}

func TestAutomaticTaskChangeRespectsResidenceAndOpaqueHistory(t *testing.T) {
	s := intentTestService(t)
	pool, err := s.Cfg.CreateChannelPool("messages", config.ChannelPool{Name: "代码组", ModelMatcher: "coding"})
	require.NoError(t, err)
	cfg := automaticConfig()
	cfg.Groups = []config.IntentGroupCapability{
		{PoolID: "default", Tasks: []string{"translation"}, Tiers: []int{0, 1, 2}},
		{PoolID: pool.ID, Tasks: []string{"code_generation"}, Tiers: []int{0, 1, 2}},
	}
	require.NoError(t, s.Cfg.UpdateIntentRouting("messages", cfg))
	ctx, release, err := s.Begin(context.Background(), "messages", "auto-conv", "first", []byte(`{"input":"翻译你好"}`), "")
	require.NoError(t, err)
	up := &config.UpstreamConfig{ID: "language-channel", PoolID: "default", ServiceType: "claude", BaseURL: "https://example.invalid"}
	require.NoError(t, TurnFrom(ctx).CommitTarget(up, "key", "model"))
	release()
	ctx, release, err = s.Begin(context.Background(), "messages", "auto-conv", "second", []byte(`{"input":"实现一个函数"}`), "")
	require.NoError(t, err)
	require.Equal(t, "default", scheduler.PoolConstraintFrom(ctx).PoolID)
	release()
	key := digest("messages\x00auto-conv")
	state := s.state.Sessions[key]
	state.Since = time.Now().Add(-time.Hour)
	state.Turns = 10
	s.state.Sessions[key] = state
	ctx, release, err = s.Begin(context.Background(), "messages", "auto-conv", "third", []byte(`{"input":"实现一个函数"}`), "")
	require.NoError(t, err)
	require.Equal(t, pool.ID, scheduler.PoolConstraintFrom(ctx).PoolID)
	release()
	ctx, release, err = s.Begin(context.Background(), "messages", "auto-conv", "opaque", []byte(`{"input":[{"type":"reasoning","encrypted_content":"opaque"},{"role":"user","content":"实现一个函数"}]}`), "")
	require.NoError(t, err)
	require.Equal(t, "language-channel", scheduler.PoolConstraintFrom(ctx).ChannelID)
	require.Error(t, TurnFrom(ctx).CheckTarget(up, "other-key", "other-model"))
	release()
}

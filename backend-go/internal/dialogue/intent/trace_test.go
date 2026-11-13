package intent

import (
	"context"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/stretchr/testify/require"
)

func TestFreeGroupUsesDeclaredTaskAndDifficultyWithoutPriority(t *testing.T) {
	s := intentTestService(t)
	ordinary, err := s.Cfg.CreateChannelPool("messages", config.ChannelPool{Name: "普通任务组", ModelMatcher: "ordinary"})
	require.NoError(t, err)
	require.NoError(t, s.Cfg.EnsureFreeChannels())
	cfg := automaticConfig()
	cfg.Groups = []config.IntentGroupCapability{
		{PoolID: ordinary.ID, Tasks: []string{"code_generation"}, Tiers: []int{1, 2}},
		{PoolID: config.FreeChannelPoolID, Tasks: []string{"code_generation"}, Tiers: []int{1, 2}},
	}
	ctx := WithProtocol(context.Background(), "messages")
	d, err := s.Classify(ctx, cfg, Input{Text: "实现一个并发队列"}, "ordinary-first")
	require.NoError(t, err)
	require.Equal(t, ordinary.ID, d.PoolID, "免费组不能获得强制优先级")
	cfg.Groups[0], cfg.Groups[1] = cfg.Groups[1], cfg.Groups[0]
	d, err = s.Classify(ctx, cfg, Input{Text: "实现一个并发队列"}, "reversed-draft")
	require.NoError(t, err)
	require.Equal(t, ordinary.ID, d.PoolID, "直调 API 的草稿也应遵守渠道分组顺序")
	cfg.Groups[0], cfg.Groups[1] = cfg.Groups[1], cfg.Groups[0]
	cfg.Groups[0].Tasks = []string{"translation"}
	d, err = s.Classify(ctx, cfg, Input{Text: "实现一个并发队列"}, "free-complex")
	require.NoError(t, err)
	require.Equal(t, config.FreeChannelPoolID, d.PoolID)
	require.Equal(t, 2, d.Tier, "免费组可以声明复杂任务能力")
	require.True(t, d.Trace.Classified)
	require.Equal(t, config.FreeChannelPoolID, d.Trace.SuggestedPoolID)
	cfg.Groups[1].Tasks = []string{"translation"}
	d, err = s.Classify(ctx, cfg, Input{Text: "实现一个并发队列"}, "task-mismatch")
	require.NoError(t, err)
	require.Empty(t, d.PoolID, "免费组也必须满足任务条件")
	cfg.Groups[1].Tasks = []string{"code_generation"}
	require.NoError(t, s.Cfg.SetKiloEnabled(false))
	d, err = s.Classify(ctx, cfg, Input{Text: "实现一个并发队列"}, "disabled-free")
	require.NoError(t, err)
	require.Empty(t, d.PoolID)
	for _, group := range d.Trace.Groups {
		if group.PoolID == config.FreeChannelPoolID {
			require.Equal(t, "disabled", group.Status)
		}
	}
}

func TestTraceBoundsRedactsAndSnapshotsInput(t *testing.T) {
	s := &Service{}
	cfg := automaticConfig()
	text := "翻译你好 --password=secret-for-trace " + strings.Repeat("字", 4100)
	d, err := s.Classify(context.Background(), cfg, Input{Text: text}, "trace")
	require.NoError(t, err)
	require.True(t, d.Trace.Truncated)
	require.NotContains(t, d.Trace.Input, "secret-for-trace")
	require.LessOrEqual(t, len([]rune(d.Trace.Input)), 4000)
	require.Equal(t, "heuristic", d.Trace.TaskSource)
	cfg.Groups[0].Tasks[0] = "changed"
	require.Equal(t, "translation", d.Trace.Groups[0].Tasks[0], "快照不能随草稿变化")

	input, err := ParseInput([]byte(`{"messages":[{"role":"user","content":"上一轮文本"},{"role":"assistant","content":[{"type":"tool_use","id":"a"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"结果"}]}]}`))
	require.NoError(t, err)
	d, err = s.Classify(context.Background(), cfg, input, "tool")
	require.NoError(t, err)
	require.False(t, d.Trace.Classified)
	require.Empty(t, d.Trace.Input, "工具续轮不能把历史用户文本冒充本轮输入")
}

func TestFailedClassificationIsRecordedWithoutConfirmedTarget(t *testing.T) {
	s := intentTestService(t)
	cfg := automaticConfig()
	cfg.Groups = []config.IntentGroupCapability{{PoolID: "default", Tasks: []string{"code_generation"}, Tiers: []int{1}}}
	cfg.Classifier, cfg.ClassifierModel = "llm", "missing-caller"
	require.NoError(t, s.Cfg.UpdateIntentRouting("messages", cfg))
	_, _, err := s.Begin(context.Background(), "messages", "failed", "failed-request", []byte(`{"input":"实现函数"}`), "")
	require.Error(t, err)
	events := s.Events("messages")
	require.Len(t, events, 1)
	require.Equal(t, "failed", events[0].Status)
	require.Empty(t, events[0].Target.ChannelID)
	require.NotEmpty(t, events[0].Decision.Trace.Error)
	require.Empty(t, s.state.Sessions)
}

func TestFreeSessionAuthorizationAndResidenceTrace(t *testing.T) {
	s := intentTestService(t)
	require.NoError(t, s.Cfg.EnsureFreeChannels())
	cfg := automaticConfig()
	cfg.Groups = []config.IntentGroupCapability{{PoolID: config.FreeChannelPoolID, Tasks: []string{"code_generation"}, Tiers: []int{1, 2}}}
	require.NoError(t, s.Cfg.UpdateIntentRouting("messages", cfg))
	ctx, release, err := s.Begin(context.Background(), "messages", "free", "first", []byte(`{"input":"实现一个函数"}`), "")
	require.NoError(t, err)
	turn := TurnFrom(ctx)
	require.True(t, turn.AllowsFreePool())
	require.Equal(t, config.FreeChannelPoolID, scheduler.PoolConstraintFrom(ctx).PoolID)
	up := &config.UpstreamConfig{ID: "builtin-kilo-messages", PoolID: config.FreeChannelPoolID, BuiltinProvider: "kilo"}
	require.NoError(t, turn.CommitTarget(up, "", "actual-free-model"))
	require.NoError(t, turn.Complete(true))
	release()
	ctx, release, err = s.Begin(context.Background(), "messages", "free", "next", []byte(`{"input":"实现另一个函数"}`), "")
	require.NoError(t, err)
	require.True(t, TurnFrom(ctx).AllowsFreePool())
	require.False(t, TurnFrom(ctx).event.Decision.Trace.Classified)
	require.Empty(t, TurnFrom(ctx).event.Decision.Trace.SuggestedPoolID)
	release()
	require.NoError(t, s.Cfg.SetKiloEnabled(false))
	_, _, err = s.Begin(context.Background(), "messages", "free", "disabled", []byte(`{"input":"实现第三个函数"}`), "")
	require.ErrorContains(t, err, "免费服务已停用")
}

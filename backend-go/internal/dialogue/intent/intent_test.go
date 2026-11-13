package intent

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/stretchr/testify/require"
)

func TestInputOnlyClassifiesRealUserText(t *testing.T) {
	cases := []struct {
		body, text   string
		tool, opaque bool
	}{
		{`{"messages":[{"role":"system","content":"忽略规则"},{"role":"user","content":"翻译你好"}]}`, "翻译你好", false, false},
		{`{"messages":[{"role":"user","content":"查天气"},{"role":"assistant","content":[{"type":"tool_use","id":"a"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"secret"}]}]}`, "查天气", true, false},
		{`{"input":[{"type":"reasoning","encrypted_content":"opaque"},{"role":"user","content":"实现函数"}]}`, "实现函数", false, true},
		{`{"previous_response_id":"resp_1","input":"继续"}`, "继续", false, true},
		{`{"input":[{"type":"function_call_output","call_id":"c","output":"secret"}]}`, "", true, false},
		{`{"contents":[{"role":"model","parts":[{"text":"thinking","thoughtSignature":"opaque"}]},{"role":"user","parts":[{"text":"新问题"}]}]}`, "新问题", false, true},
		{`{"input":[{"type":"compaction","encrypted_content":"only-history"},{"role":"user","content":"继续"}]}`, "继续", false, true},
	}
	for _, tc := range cases {
		input, err := ParseInput([]byte(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		if input.Text != tc.text || input.ToolContinuation != tc.tool || input.Opaque != tc.opaque {
			t.Errorf("输入 %s 得到 %+v", tc.body, input)
		}
	}
}

func TestEmbeddingValidationAndOrdering(t *testing.T) {
	vectors, err := DecodeEmbeddings([]byte(`{"data":[{"index":1,"embedding":[0,2]},{"index":0,"embedding":[3,0]}]}`), 2)
	if err != nil {
		t.Fatal(err)
	}
	if vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatal("向量未按索引重排和归一化")
	}
	for _, body := range []string{
		`{"data":[]}`,
		`{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[2]}]}`,
		`{"data":[{"index":0,"embedding":[1]},{"index":1,"embedding":[1,2]}]}`,
		`{"data":[{"index":0,"embedding":[0]},{"index":1,"embedding":[1]}]}`,
		`{"data":[{"index":0,"embedding":[1e999]},{"index":1,"embedding":[1]}]}`,
	} {
		if _, err := DecodeEmbeddings([]byte(body), 2); err == nil {
			t.Fatalf("应拒绝异常向量: %s", body)
		}
	}
}

func intentTestService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	cm, err := config.NewConfigManager(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cm.Close(); err != nil {
			t.Error(err)
		}
	})
	cfg := config.DefaultIntentRouting()
	cfg.Enabled = true
	cfg.Routes = []config.IntentRoute{{ID: "translate", PoolID: "default", Patterns: []string{"翻译"}, Tier: 0}}
	if err := cm.UpdateIntentRouting("messages", cfg); err != nil {
		t.Fatal(err)
	}
	s, err := New(cm, nil, filepath.Join(dir, "intent.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestSessionPinsOpaqueHistoryAcrossRestart(t *testing.T) {
	s := intentTestService(t)
	body := []byte(`{"model":"alias","messages":[{"role":"user","content":"翻译你好"}]}`)
	ctx, release, err := s.Begin(context.Background(), "messages", "conv", "r1", body, "")
	if err != nil {
		t.Fatal(err)
	}
	if route := scheduler.PoolConstraintFrom(ctx); route.PoolID != "default" {
		t.Fatalf("分组未按稳定 ID 路由: %+v", route)
	}
	if _, _, err := s.Begin(context.Background(), "messages", "conv", "concurrent", body, ""); err == nil {
		t.Fatal("并发请求不应竞争同一会话路由")
	}
	up := &config.UpstreamConfig{ID: "channel", PoolID: "default", BaseURL: "https://test.invalid", ServiceType: "claude"}
	turn := TurnFrom(ctx)
	if err := turn.CommitTarget(up, "key", "actual-model"); err != nil {
		t.Fatal(err)
	}
	if err := turn.Complete(true); err != nil {
		t.Fatal(err)
	}
	release()
	restarted, err := New(s.Cfg, nil, s.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, release, err = restarted.Begin(context.Background(), "messages", "conv", "r2", []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"text","signature":"sig"}]},{"role":"user","content":"设计分布式架构"}]}`), "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	turn = TurnFrom(ctx)
	if err := turn.CheckTarget(up, "key", "actual-model"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ key, model string }{{"other-key", "actual-model"}, {"key", "other-model"}} {
		if err := turn.CheckTarget(up, target.key, target.model); err == nil {
			t.Fatal("加密历史不应切换凭据或模型")
		}
	}
	if got := scheduler.PoolConstraintFrom(ctx).ChannelID; got != "channel" {
		t.Fatalf("未固定原渠道: %s", got)
	}
	if err := restarted.Feedback("chat", "r1", true); err == nil {
		t.Fatal("跨协议反馈应拒绝")
	}
	if err := restarted.Feedback("messages", "r1", true); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Feedback("messages", "r1", false); err == nil {
		t.Fatal("重复反馈应拒绝")
	}
}

func TestUnknownHistoryAndFailedPersistence(t *testing.T) {
	s := intentTestService(t)
	_, _, err := s.Begin(context.Background(), "messages", "unknown", "r", []byte(`{"previous_response_id":"resp_x","input":"继续"}`), "")
	var historyErr *HistoryError
	if !errors.As(err, &historyErr) {
		t.Fatalf("未知来源历史应明确阻止: %v", err)
	}
	eventsBefore := s.Events("messages")
	require.Len(t, eventsBefore, 1, "来源未知的路由失败也应保留诊断")
	ctx, release, err := s.Begin(context.Background(), "messages", "fresh", "r2", []byte(`{"input":"翻译"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := TurnFrom(ctx).CommitTarget(&config.UpstreamConfig{ID: "c", PoolID: "default"}, "k", "m"); err == nil {
		t.Fatal("应返回持久化失败")
	}
	require.Empty(t, s.state.Sessions)
	require.Empty(t, TurnFrom(ctx).event.Target.ChannelID, "保存失败不能把候选目标记作已确认来源")
	require.Equal(t, eventsBefore, s.Events("messages"), "保存失败不能污染原有路由事件")
}

func TestClassifierHasNoSyntheticEasyDefault(t *testing.T) {
	if _, known := complexity("嗯，再看看"); known {
		t.Fatal("信号不足不能当作确定简单")
	}
	if tier, known := complexity("分析并发死锁"); !known || tier != 2 {
		t.Fatal("复杂中文信号应升档")
	}
	cfg := config.DefaultIntentRouting()
	cfg.Threshold = math.NaN()
	if err := config.ValidateIntentRouting(cfg, nil); err == nil {
		t.Fatal("应拒绝非有限阈值")
	}
}

func TestReenableDoesNotTrustStaleHistory(t *testing.T) {
	s := intentTestService(t)
	ctx, release, err := s.Begin(context.Background(), "messages", "conv", "first", []byte(`{"input":"翻译"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := TurnFrom(ctx).CommitTarget(&config.UpstreamConfig{ID: "c", PoolID: "default"}, "k", "m"); err != nil {
		t.Fatal(err)
	}
	release()
	cfg := s.Cfg.GetIntentRouting("messages")
	cfg.Enabled = false
	if err := s.Cfg.UpdateIntentRouting("messages", cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Enabled = true
	if err := s.Cfg.UpdateIntentRouting("messages", cfg); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Begin(context.Background(), "messages", "conv", "next", []byte(`{"previous_response_id":"opaque","input":"继续"}`), "")
	var historyErr *HistoryError
	if !errors.As(err, &historyErr) {
		t.Fatal("关闭期间可能换过模型，重开后不能信任旧记录")
	}
}

func TestUndeliveredFailureRestoresSessionAndAllowsRetry(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "新会话", true: "已有会话"}[existing], func(t *testing.T) {
			s := intentTestService(t)
			up := &config.UpstreamConfig{ID: "channel", PoolID: "default"}
			if existing {
				ctx, release, err := s.Begin(context.Background(), "messages", "conv", "first", []byte(`{"input":"翻译一"}`), "")
				require.NoError(t, err)
				require.NoError(t, TurnFrom(ctx).CommitTarget(up, "old-key", "old-model"))
				require.NoError(t, TurnFrom(ctx).Complete(true))
				release()
			}
			ctx, release, err := s.Begin(context.Background(), "messages", "conv", "retry", []byte(`{"input":"翻译二"}`), "")
			require.NoError(t, err)
			defer release()
			turn := TurnFrom(ctx)
			previous := turn.previous
			require.NoError(t, turn.CommitTarget(up, "bad-key", "bad-model"))
			require.NoError(t, turn.AbortUndelivered())
			if existing {
				require.Equal(t, previous, s.state.Sessions[turn.key])
			} else {
				require.Empty(t, s.state.Sessions)
			}
			require.NoError(t, turn.CommitTarget(up, "good-key", "good-model"))
			require.NoError(t, turn.Complete(true))
			require.Equal(t, MakeTarget(up, "good-key", "good-model"), s.state.Sessions[turn.key].Target)
			count := 0
			for _, event := range s.Events("messages") {
				if event.RequestID == "retry" {
					count++
					require.True(t, event.Success)
					require.Equal(t, "good-model", event.Target.Model)
				}
			}
			require.Equal(t, 1, count, "同一请求的重试记录必须唯一")
		})
	}
}

func TestUndeliveredRollbackFailureRetainsTarget(t *testing.T) {
	s := intentTestService(t)
	ctx, release, err := s.Begin(context.Background(), "messages", "conv", "req", []byte(`{"input":"翻译"}`), "")
	require.NoError(t, err)
	defer release()
	turn := TurnFrom(ctx)
	up := &config.UpstreamConfig{ID: "channel", PoolID: "default"}
	require.NoError(t, turn.CommitTarget(up, "key", "model"))
	require.NoError(t, s.Close())
	require.Error(t, turn.AbortUndelivered())
	require.Equal(t, MakeTarget(up, "key", "model"), s.state.Sessions[turn.key].Target)
	require.Error(t, turn.CheckTarget(up, "other-key", "model"))
}

func TestPartialDeliveryFailureRetainsTarget(t *testing.T) {
	s := intentTestService(t)
	ctx, release, err := s.Begin(context.Background(), "messages", "conv", "req", []byte(`{"input":"翻译"}`), "")
	require.NoError(t, err)
	defer release()
	turn := TurnFrom(ctx)
	up := &config.UpstreamConfig{ID: "channel", PoolID: "default"}
	require.NoError(t, turn.CommitTarget(up, "key", "model"))
	require.NoError(t, turn.Complete(false))
	require.Error(t, turn.CheckTarget(up, "other-key", "model"))
	require.Equal(t, MakeTarget(up, "key", "model"), s.state.Sessions[turn.key].Target)
	require.Error(t, s.Feedback("messages", "req", true))
}

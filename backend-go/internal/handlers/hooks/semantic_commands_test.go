package hooks

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/semantic"
	"github.com/insistanan/api-proxy/internal/sensitive"
	"github.com/stretchr/testify/require"
)

type semanticTestEmbedder struct {
	fail         bool
	negativeSame bool
	calls        int
}

func (e *semanticTestEmbedder) EmbeddingSpace(config.SemanticEmbeddingConfig) (string, error) {
	return "test-space", nil
}
func (e *semanticTestEmbedder) EmbedSamples(_ context.Context, _ config.SemanticEmbeddingConfig, texts []string, _ string) ([][]float64, error) {
	e.calls++
	if e.fail {
		return nil, errors.New("模拟向量故障")
	}
	out := make([][]float64, len(texts))
	for i, text := range texts {
		if strings.Contains(text, "反例") && !e.negativeSame {
			out[i] = []float64{0, 1}
		} else {
			out[i] = []float64{1, 0}
		}
	}
	return out, nil
}
func semanticTestSetup(t *testing.T, negativeSame bool) (*semantic.Service, *semanticTestEmbedder, config.DangerousCmdConfig) {
	t.Helper()
	embedder := &semanticTestEmbedder{negativeSame: negativeSame}
	s, err := semantic.New(filepath.Join(t.TempDir(), "data.db"), embedder)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	profile := config.SemanticEmbeddingConfig{Source: "local", Model: "test", TimeoutMS: 1000}
	d, err := s.Save(context.Background(), semantic.Dataset{ID: "tools", Name: "测试工具", Kind: "safety", Samples: []semantic.Sample{
		{ID: "risk", Label: "destructive", Text: "危险测试", Split: "index", Family: "risk", Approved: true, Source: "test", Pattern: `^synthetic-danger --target all$`},
		{ID: "negative", Label: "destructive", Text: "安全反例", Negative: true, Split: "index", Family: "negative", Approved: true, Source: "test"},
	}})
	require.NoError(t, err)
	_, err = s.Build(context.Background(), d.ID, d.Revision, profile)
	require.NoError(t, err)
	return s, embedder, config.DangerousCmdConfig{Enabled: true, EnabledRules: []string{"destructive"}, Semantic: &config.SemanticSafetyConfig{Enabled: true, Mode: "block", DatasetIDs: []string{d.ID}, Embedding: profile, Threshold: 0.8, Margin: 0.1, FailurePolicy: "block"}}
}
func semanticTestCommand() sensitive.CommandMatch {
	return sensitive.ExtractToolCommands("Bash", `{"command":"synthetic-danger --target all","cwd":"/workspace"}`, false)[0]
}

func TestSemanticModesAndDeterministicVerification(t *testing.T) {
	for _, mode := range []string{"observe", "review", "block"} {
		t.Run(mode, func(t *testing.T) {
			s, _, settings := semanticTestSetup(t, false)
			settings.Semantic.Mode = mode
			recorder := &gateTestRecorder{}
			calls := 0
			var reviewed sensitive.CommandMatch
			err := reviewSemanticCommands(context.Background(), s, recorder, settings, HookContext{}, []sensitive.CommandMatch{semanticTestCommand()}, func(match sensitive.CommandMatch) *ContentSafetyError { calls++; reviewed = match; return nil })
			if mode == "block" {
				require.Error(t, err)
				require.Equal(t, "destructive", ContentSafetyErrorFrom(err).RuleName)
				require.Zero(t, calls)
			} else {
				require.NoError(t, err)
			}
			if mode == "observe" {
				require.Zero(t, calls)
				require.Len(t, recorder.entries, 1)
				require.Equal(t, sensitive.BlockTypeSemanticObserve, recorder.entries[0].BlockType)
			}
			if mode == "review" {
				require.Equal(t, 1, calls)
				require.Contains(t, reviewed.ExecutionContext, `"cwd":"/workspace"`)
				require.NotEmpty(t, reviewed.SemanticEvidence)
			}
		})
	}
}

func TestSemanticAmbiguityRequiresReviewAndDisabledRuleDoesNotMatch(t *testing.T) {
	s, _, settings := semanticTestSetup(t, true)
	calls := 0
	review := func(sensitive.CommandMatch) *ContentSafetyError { calls++; return nil }
	require.NoError(t, reviewSemanticCommands(context.Background(), s, nil, settings, HookContext{}, []sensitive.CommandMatch{semanticTestCommand()}, review))
	require.Equal(t, 1, calls, "反例分数接近时不能直接阻断，也不能绕过复核")
	settings.EnabledRules = []string{"reverse_shell"}
	calls = 0
	require.NoError(t, reviewSemanticCommands(context.Background(), s, nil, settings, HookContext{}, []sensitive.CommandMatch{semanticTestCommand()}, review))
	require.Zero(t, calls)
}

func TestSemanticDirectBlockChecksAllCandidatesBeforeReview(t *testing.T) {
	s, _, settings := semanticTestSetup(t, false)
	d, err := s.Get("tools")
	require.NoError(t, err)
	d.Samples = append(d.Samples, semantic.Sample{ID: "aaa", Label: "download_execute", Text: "更早的审核候选", Split: "index", Family: "another", Source: "test", Approved: true})
	d, err = s.Save(context.Background(), d)
	require.NoError(t, err)
	_, err = s.Build(context.Background(), d.ID, d.Revision, settings.Semantic.Embedding)
	require.NoError(t, err)
	settings.EnabledRules = append(settings.EnabledRules, "download_execute")
	calls := 0
	review := func(sensitive.CommandMatch) *ContentSafetyError { calls++; return nil }
	err = reviewSemanticCommands(context.Background(), s, nil, settings, HookContext{}, []sensitive.CommandMatch{semanticTestCommand()}, review)
	require.Error(t, err)
	require.Zero(t, calls, "首个候选可复核不应跳过后续确定性阻断条件")
	command := semanticTestCommand()
	command.Command = "synthetic-safe --target all"
	require.NoError(t, reviewSemanticCommands(context.Background(), s, nil, settings, HookContext{}, []sensitive.CommandMatch{command}, review))
	require.Equal(t, 1, calls, "相似度高但核验正则不匹配时仅复核一次")
}

func TestSemanticObservationReleasesReviewedStream(t *testing.T) {
	s, _, settings := semanticTestSetup(t, false)
	settings.Semantic.Mode = "observe"
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	cfg := manager.GetSettings()
	cfg.ContentSafety.DangerousCmd = settings
	require.NoError(t, manager.UpdateSettings(cfg))
	recorder := &gateTestRecorder{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	AttachHookPipeline(c, NewContentSafetyPipelineWithSemantic(manager, recorder, nil, s), HookContext{APIType: "messages", Stream: true})
	defer ClearAttachedSensitiveData(c)
	require.NoError(t, FeedAttachedStreamToolArgumentsForKey(c, "messages.0", `{"command":"synthetic-`, "Bash"))
	event := "data: {\"type\":\"content_block_delta\",\"delta\":{\"partial_json\":\"prefix\"}}\n\n"
	out, err := RestoreAttachedSSEEvent(c, event)
	require.NoError(t, err)
	require.Empty(t, out)
	require.NoError(t, FeedAttachedStreamToolArgumentsForKey(c, "messages.0", `danger --target all"}`))
	out, err = RestoreAttachedSSEEvent(c, "data: {\"type\":\"message_stop\"}\n\n")
	require.NoError(t, err)
	require.Contains(t, out, "prefix")
	require.Contains(t, out, "message_stop")
	require.Len(t, recorder.entries, 1)
	require.Equal(t, sensitive.BlockTypeSemanticObserve, recorder.entries[0].BlockType)
}

func TestSemanticFailurePoliciesAndEvidenceIdentity(t *testing.T) {
	s, embedder, settings := semanticTestSetup(t, false)
	embedder.fail = true
	calls := 0
	review := func(sensitive.CommandMatch) *ContentSafetyError { calls++; return nil }
	err := reviewSemanticCommands(context.Background(), s, nil, settings, HookContext{}, []sensitive.CommandMatch{semanticTestCommand()}, review)
	require.Equal(t, "semantic_unavailable", ContentSafetyErrorFrom(err).RuleName)
	require.Zero(t, calls)
	settings.Semantic.FailurePolicy = "review"
	require.NoError(t, reviewSemanticCommands(context.Background(), s, nil, settings, HookContext{}, []sensitive.CommandMatch{semanticTestCommand()}, review))
	require.Equal(t, 1, calls)
	settings.Semantic.Mode = "observe"
	recorder := &gateTestRecorder{}
	require.NoError(t, reviewSemanticCommands(context.Background(), s, recorder, settings, HookContext{}, []sensitive.CommandMatch{semanticTestCommand()}, review))
	require.Len(t, recorder.entries, 1)
	embedder.fail = false
	settings.Semantic.Mode = "review"
	hashes := []string{}
	collect := func(match sensitive.CommandMatch) *ContentSafetyError {
		hashes = append(hashes, sensitive.CommandIdentity(match))
		return nil
	}
	require.NoError(t, reviewSemanticCommands(context.Background(), s, nil, settings, HookContext{}, []sensitive.CommandMatch{semanticTestCommand()}, collect))
	d, err := s.Get("tools")
	require.NoError(t, err)
	d.Description = "修订"
	d, err = s.Save(context.Background(), d)
	require.NoError(t, err)
	_, err = s.Build(context.Background(), d.ID, d.Revision, settings.Semantic.Embedding)
	require.NoError(t, err)
	require.NoError(t, reviewSemanticCommands(context.Background(), s, nil, settings, HookContext{}, []sensitive.CommandMatch{semanticTestCommand()}, collect))
	require.NotEqual(t, hashes[0], hashes[1])
}

func TestSemanticPipelineHoldsStreamAndPreservesExistingRules(t *testing.T) {
	s, embedder, settings := semanticTestSetup(t, true)
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	settings.AIGate.Enabled = true
	cfg := manager.GetSettings()
	cfg.ContentSafety.DangerousCmd = settings
	require.NoError(t, manager.UpdateSettings(cfg))
	pipeline := NewContentSafetyPipelineWithSemantic(manager, &gateTestRecorder{}, nil, s)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	AttachHookPipeline(c, pipeline, HookContext{APIType: "messages", Stream: true})
	defer ClearAttachedSensitiveData(c)
	before := embedder.calls
	require.NoError(t, FeedAttachedStreamText(c, "synthetic-danger --target all"))
	require.Equal(t, before, embedder.calls)
	require.NoError(t, FeedAttachedStreamToolArgumentsForKey(c, "messages.0", `{"command":"synthetic-`, "Bash"))
	output, err := RestoreAttachedSSEEvent(c, `data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"prefix"}}`+"\n\n")
	require.NoError(t, err)
	require.Empty(t, output)
	require.Equal(t, before, embedder.calls, "参数不完整时不查询向量")
	err = FeedAttachedStreamToolArgumentsForKey(c, "messages.0", `danger --target all","cwd":"/workspace"}`)
	require.Error(t, err, "歧义命中需经过闸门；未装配闸门时不能交付")
	output, err = RestoreAttachedSSEEvent(c, "data: {\"type\":\"message_stop\"}\n\n")
	require.Error(t, err)
	require.Empty(t, output)
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	AttachHookPipeline(c2, pipeline, HookContext{APIType: "messages"})
	defer ClearAttachedSensitiveData(c2)
	before = embedder.calls
	_, err = RunAttachedPostResponseHooks(context.Background(), c2, []byte(`{"content":[{"type":"tool_use","name":"Bash","input":{"command":"rm -rf /"}}]}`), nil)
	require.Error(t, err)
	require.Equal(t, before, embedder.calls, "既有危险规则不能被安全反例覆盖")
	_, err = RunAttachedPostResponseHooks(context.Background(), c2, []byte(`{"content":[{"type":"text","text":"synthetic-danger --target all"},{"type":"tool_result","content":"synthetic-danger --target all"},{"type":"tool_use","name":"Write","input":{"content":"synthetic-danger --target all"}}]}`), nil)
	require.NoError(t, err)
	require.Equal(t, before, embedder.calls, "正文、工具结果与写文件内容不当作执行命令")
}
